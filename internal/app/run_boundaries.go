package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/mattlock83/wiretap/internal/project"
)

const registeredBoundaries = ".wiretap-startRun.json"

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
	if err := a.setReverseTarget(cfg.SUT.TargetURL()); err != nil {
		return err
	}
	a.project = cfg
	return nil
}
