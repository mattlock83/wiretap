package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/project"
)

const registeredBoundaries = ".wiretap-startRun.json"

// The control server's ordinary 10s write deadline must not cut off a supported
// discovery budget. Capture streams use their separate listener and deadlines.
func (a *App) extendGRPCControlDeadline(w http.ResponseWriter) {
	if a.project == nil {
		return
	}
	count := 0
	longest := 10 * time.Second
	visit := func(api project.API) {
		if api.Protocol != "grpc" {
			return
		}
		count++
		if api.GRPCSchema != nil && api.GRPCSchema.Source == "proto" {
			longest = time.Minute
		}
		if api.GRPCSchema != nil && api.GRPCSchema.Timeout != "" {
			if budget, err := time.ParseDuration(api.GRPCSchema.Timeout); err == nil && budget > longest {
				longest = budget
			}
		}
	}
	visit(a.project.SUT)
	for _, api := range a.project.Dependencies {
		visit(api)
	}
	if count == 0 {
		return
	}
	budget := time.Duration((count+3)/4)*longest + 10*time.Second
	if budget < a.cfg.HTTP.WriteTimeout {
		budget = a.cfg.HTTP.WriteTimeout
	}
	// Unsupported on test recorders; real HTTP/1 and HTTP/2 server writers support it.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
}

// API integrations use the same schema, normalization and validation as project
// files. Only boundaries may be registered: process/output settings stay local.
func (a *App) configureRunBoundaries(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if a.currentBundle() != nil || a.hasActiveSessions() {
		return errors.New("boundaries are fixed for the active run; finish it before registering new boundaries")
	}
	if a.project != nil && filepath.Base(a.project.Path) != registeredBoundaries {
		return errors.New("boundaries are already defined by project configuration")
	}
	var input struct {
		SUT          json.RawMessage            `json:"sut"`
		Dependencies map[string]json.RawMessage `json:"dependencies,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	if len(input.SUT) == 0 {
		return errors.New("boundaries.sut is required")
	}
	doc, err := json.Marshal(map[string]any{"version": 1, "sut": input.SUT, "dependencies": input.Dependencies})
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := project.Decode(doc, filepath.Join(dir, registeredBoundaries))
	if err != nil {
		return err
	}
	if err := a.proxyComponent.server.SetCaptureTLS(cfg.SUT.CaptureTLS.CertFile, cfg.SUT.CaptureTLS.KeyFile); err != nil {
		return err
	}
	if err := a.setReverseTarget(cfg.SUT.TargetURL()); err != nil {
		return err
	}
	a.project = cfg
	return nil
}

func (a *App) initializeRunContracts() error {
	w := a.currentBundle()
	if w == nil || a.project == nil {
		return nil
	}
	states := []bundle.ContractResolution{}
	add := func(id, role string, api project.API) {
		if api.Protocol != "grpc" {
			return
		}
		if id == "" {
			id = "sut"
		}
		source := "reflection"
		if api.GRPCSchema != nil {
			source = api.GRPCSchema.Source
		}
		states = append(states, bundle.ContractResolution{APIID: id, Role: role, Protocol: "grpc", Source: source, State: "pending", Services: append([]string(nil), api.Services...)})
	}
	add(a.project.SUT.ID, "sut", a.project.SUT)
	for _, id := range a.project.DependencyIDs() {
		add(id, "dependency", a.project.Dependencies[id])
	}
	if len(states) == 0 || len(w.Snapshot().Contracts) > 0 {
		return nil
	}
	kinds := map[string]string{}
	for _, id := range a.project.DependencyIDs() {
		kind := a.project.Dependencies[id].Kind
		if kind == "" {
			kind = "unknown"
		}
		kinds[id] = kind
	}
	id := a.project.SUT.ID
	if id == "" {
		id = "sut"
	}
	kind := a.project.SUT.Kind
	if kind == "" {
		kind = "unknown"
	}
	if err := w.Configure(bundle.Identity{APIID: id, SUTTargetKind: kind, DependencyKinds: kinds}); err != nil {
		return err
	}
	if err := w.SetContracts(states); err != nil {
		return err
	}
	return nil
}

func (a *App) recordGRPCResolution(state string, refs ...artifact.SchemaRef) error {
	if err := a.initializeRunContracts(); err != nil {
		return err
	}
	w := a.currentBundle()
	if w == nil {
		return nil
	}
	states := append([]bundle.ContractResolution(nil), w.Snapshot().Contracts...)
	for i := range states {
		states[i].State = state
		for _, ref := range refs {
			if ref.ID == states[i].APIID && ref.Role == states[i].Role {
				states[i].Services = append([]string(nil), ref.Services...)
			}
		}
		states[i].Diagnostic = ""
		if state == "failed" {
			states[i].Diagnostic = "Contract acquisition failed; no tests admitted. Check source configuration or upstream readiness and retry setupTest."
		}
	}
	if err := w.SetContracts(states); err != nil {
		return err
	}
	return nil
}
