package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/coverage"
	"github.com/phrond/evermock/internal/proxy"
	"github.com/phrond/evermock/internal/tracecontext"
	"go.opentelemetry.io/otel/trace"
)

type testCaseStartRequest struct {
	Name           string               `json:"name"`
	Tags           []string             `json:"tags"`
	Meta           map[string]any       `json:"meta"`
	SUTBaseURL     string               `json:"sut_base_url"`
	Schemas        []artifact.SchemaRef `json:"schemas"`
	GenerateSchema bool                 `json:"generate_schema,omitempty"`
}

type testCaseFinishRequest struct {
	Status string `json:"status"`
}

func (a *App) handleTestCaseStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req testCaseStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if a.store == nil || a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capture runtime not configured"})
		return
	}
	if a.hasActiveSessions() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "capture sessions are already active"})
		return
	}
	for i := range req.Schemas {
		req.Schemas[i].Role = "sut"
	}
	if err := validateSchemaSetup(req.Schemas, req.GenerateSchema); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if target := strings.TrimSpace(req.SUTBaseURL); target != "" {
		if err := a.setReverseTarget(target); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid sut_base_url: %v", err)})
			return
		}
	}
	if err := a.proxyComponent.Start(r.Context()); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tc, err := a.store.StartCase(req.Name, req.Tags, req.Meta, req.SUTBaseURL)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	for _, schema := range req.Schemas {
		a.store.AddSchema(schema)
	}
	a.sessionsMu.Lock()
	a.legacySession = &runtimeSession{CaseID: tc.ID, Store: a.store, drained: make(chan struct{})}
	a.sessionsMu.Unlock()
	writeJSON(w, http.StatusOK, tc)
}

func (a *App) handleTestCaseFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req testCaseFinishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if a.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
		return
	}
	a.sessionsMu.Lock()
	legacy := a.legacySession
	a.sessionsMu.Unlock()
	if legacy != nil {
		a.beginDrain(legacy)
		a.waitForDrain(r.Context(), legacy, 5*time.Second)
	}
	a.sessionsMu.Lock()
	a.legacySession = nil
	a.sessionsMu.Unlock()
	artifactDoc, path, err := a.store.FinishCase(defaultStatus(req.Status), a.environmentSnapshot())
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if a.proxyComponent != nil {
		// A legacy drain timeout must not become an unbounded server shutdown.
		stopCtx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		_ = a.proxyComponent.Stop(stopCtx)
	}
	logFinishedCase(a, artifactDoc, path)
	writeArtifactResponse(w, artifactDoc, path, "")
}

func (a *App) handleTestCaseStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
		return
	}
	writeJSON(w, http.StatusOK, a.store.Status())
}

func (a *App) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	artifactDoc := a.store.LastArtifact()
	if artifactDoc == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no artifact available"})
		return
	}
	data, err := artifact.EncodeBytes(artifactDoc)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=\"artifact.yaml\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (a *App) handleExpectations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var expectation Expectation
	if err := json.NewDecoder(r.Body).Decode(&expectation); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	a.store.AddExpectation(expectation)
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

func (a *App) handleSchemas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var schema artifact.SchemaRef
	if err := json.NewDecoder(r.Body).Decode(&schema); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	schema.Role = "sut"
	writeJSON(w, http.StatusOK, a.store.AddSchema(schema))
}

func (a *App) handleRedactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var rule artifact.Redaction
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	writeJSON(w, http.StatusOK, a.store.AddRedaction(rule))
}

type proxyStartRequest struct {
	ListenAddr string `json:"listen_addr,omitempty"`
	TargetURL  string `json:"target_url,omitempty"`
}

func (a *App) handleProxyStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "proxy not configured"})
		return
	}
	var payload proxyStartRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := a.configureProxy(payload); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := a.proxyComponent.Start(r.Context()); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, mergeProxyInfo(a.proxyComponent))
}

func (a *App) handleProxyMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := a.proxyComponent.SetMode(proxy.Mode(payload.Mode)); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (a *App) handleProxyCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	pemBytes, err := a.proxyComponent.CACertPEM()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(pemBytes) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "ca not available"})
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pemBytes)
}

type startServerRequest struct {
	Proxy proxyStartRequest `json:"proxy,omitempty"`
}

func (a *App) handleStartServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capture proxy not configured"})
		return
	}
	var payload startServerRequest
	if err := decodeStrictJSON(r, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := a.configureProxy(payload.Proxy); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := a.proxyComponent.Start(r.Context()); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	info := mergeProxyInfo(a.proxyComponent)
	writeJSON(w, http.StatusOK, map[string]any{
		"proxy":               info,
		"sut_url":             normalizeURL(info.ListenAddr),
		"dependency_base_url": normalizeURL(info.ListenAddr) + "/__phrond/dep",
	})
}

type setupTestRequest struct {
	Name            string                   `json:"name,omitempty"`
	Tags            []string                 `json:"tags,omitempty"`
	Meta            map[string]any           `json:"meta,omitempty"`
	SUTBaseURL      string                   `json:"sut_base_url,omitempty"`
	Schemas         []artifact.SchemaRef     `json:"schemas,omitempty"`
	SUTSchemas      []artifact.SchemaRef     `json:"sut_schemas,omitempty"`
	Dependencies    []dependencyRegistration `json:"dependencies,omitempty"`
	GenerateSchema  bool                     `json:"generate_schema,omitempty"`
	ExplicitSession bool                     `json:"explicit_session,omitempty"`
	TraceContext    *tracecontext.Context    `json:"trace_context,omitempty"`
}

func (a *App) handleSetupTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capture proxy not configured"})
		return
	}
	var payload setupTestRequest
	if err := decodeStrictJSON(r, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	var root trace.SpanContext
	if payload.TraceContext != nil {
		var err error
		root, err = payload.TraceContext.Validate()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	a.sessionsMu.Lock()
	_, reused := a.traceOwners[root.TraceID().String()]
	conflict := a.legacySession != nil || (root.IsValid() && reused) || (!payload.ExplicitSession && a.hasRetiredWorkLocked())
	for _, existing := range a.sessions {
		if !payload.ExplicitSession && !existing.Explicit {
			conflict = true
		}
	}
	a.sessionsMu.Unlock()
	if conflict {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "capture session or test trace conflicts, or a retired capture is still draining"})
		return
	}
	schemas := append([]artifact.SchemaRef{}, payload.Schemas...)
	schemas = append(schemas, payload.SUTSchemas...)
	for i := range schemas {
		schemas[i].Role = "sut"
	}
	if err := validateSchemaSetup(schemas, payload.GenerateSchema); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dependencies, dependencySchemas, err := normalizeDependencies(payload.Dependencies)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if target := strings.TrimSpace(payload.SUTBaseURL); target != "" {
		if err := a.setReverseTarget(target); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid sut_base_url: %v", err)})
			return
		}
	}
	if err := a.proxyComponent.Start(r.Context()); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = "scenario-" + time.Now().UTC().Format("20060102150405.000")
	}
	a.sessionsMu.Lock()
	if !payload.ExplicitSession {
		for _, existing := range a.sessions {
			if !existing.Explicit {
				a.sessionsMu.Unlock()
				writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("capture session %q is already active; run tests serially or set explicit_session", existing.ID)})
				return
			}
		}
	}
	sessionID := a.nextSessionID()
	store := NewTestCaseStore(a.cfg.Artifact.OutputDir, a.cfg.Capture.MaxBodyBytes)
	tc, err := store.StartCase(name, payload.Tags, payload.Meta, payload.SUTBaseURL)
	if err != nil {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	for _, schema := range schemas {
		store.AddSchema(schema)
	}
	for _, schema := range dependencySchemas {
		store.AddSchema(schema)
	}
	if payload.GenerateSchema {
		store.SetSchemaGeneration(true)
	}
	store.SetTraceContext(root)
	session := &runtimeSession{Root: root, CaseID: tc.ID, drained: make(chan struct{}), ID: sessionID, Name: name, Store: store, SUTBaseURL: payload.SUTBaseURL, Dependencies: dependencies, Explicit: payload.ExplicitSession, CreatedAt: time.Now().UTC()}
	a.sessions[sessionID] = session
	if root.IsValid() {
		a.traceOwners[root.TraceID().String()] = sessionID
	}
	if !payload.ExplicitSession {
		a.defaultSessionID = sessionID
	}
	a.sessionsMu.Unlock()

	proxyURL := normalizeURL(a.proxyComponent.ListenAddr())
	dependencyURLs := make(map[string]string, len(dependencies))
	for assetID := range dependencies {
		if payload.ExplicitSession && !root.IsValid() {
			dependencyURLs[assetID] = proxyURL + "/__phrond/session/" + sessionID + "/dep/" + assetID
		} else {
			dependencyURLs[assetID] = proxyURL + "/__phrond/dep/" + assetID
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"trace_context": payload.TraceContext,
		"session_id":    sessionID,
		"name":          name,
		"explicit":      payload.ExplicitSession,
		"proxy_listen":  proxyURL,
		"sut_url":       proxyURL,
		"sut_base_url":  payload.SUTBaseURL,
		"dependencies":  dependencyURLs,
	})
}

type teardownTestRequest struct {
	SessionID      string `json:"session_id"`
	Status         string `json:"status,omitempty"`
	DrainTimeoutMS *int64 `json:"drain_timeout_ms,omitempty"`
}

func (a *App) handleTeardownTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload teardownTestRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	timeout := 5 * time.Second
	if payload.DrainTimeoutMS != nil {
		if *payload.DrainTimeoutMS < 0 || *payload.DrainTimeoutMS > 60000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "drain_timeout_ms must be between 0 and 60000"})
			return
		}
		timeout = time.Duration(*payload.DrainTimeoutMS) * time.Millisecond
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	// The configured write timeout covers ordinary calls. A bounded drain
	// needs its own deadline plus time to validate and write the artifact.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(timeout + max(a.cfg.HTTP.WriteTimeout, 10*time.Second)))
	a.controlMu.Lock()
	a.sessionsMu.Lock()
	session := a.sessions[sessionID]
	a.sessionsMu.Unlock()
	if session == nil {
		a.controlMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if !a.beginDrain(session) {
		a.controlMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "capture session is already closing"})
		return
	}
	env := a.environmentSnapshot()
	env.SUTBaseURL = session.SUTBaseURL
	a.controlMu.Unlock()

	a.waitForDrain(r.Context(), session, timeout)
	artifactDoc, path, err := session.Store.FinishCase(defaultStatus(payload.Status), env)
	a.sessionsMu.Lock()
	delete(a.sessions, sessionID)
	a.sessionsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	logFinishedCase(a, artifactDoc, path)
	writeArtifactResponse(w, artifactDoc, path, sessionID)
}

type generateSchemaRequest struct {
	ArtifactsDir string `json:"artifacts_dir,omitempty"`
	OutputPath   string `json:"output_path,omitempty"`
	Title        string `json:"title,omitempty"`
	Version      string `json:"version,omitempty"`
	Force        bool   `json:"force,omitempty"`
}

func (a *App) handleGenerateSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload generateSchemaRequest
	if err := decodeStrictJSON(r, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	artifactsDir := strings.TrimSpace(payload.ArtifactsDir)
	if artifactsDir == "" {
		artifactsDir = a.cfg.Artifact.OutputDir
	}
	outputPath := strings.TrimSpace(payload.OutputPath)
	if outputPath == "" {
		outputPath = filepath.Join(artifactsDir, "generated.openapi.yaml")
	}
	report, err := coverage.Generate(r.Context(), artifactsDir, payload.Title, false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := writeGeneratedSchema(outputPath, report, coverage.GeneratedOpenAPIOptions{
		Title: payload.Title, Version: payload.Version,
	}, payload.Force); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrExist) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_path": outputPath, "artifact_count": report.ArtifactCount})
}

type runtimeReportRequest struct {
	ArtifactsDir     string `json:"artifacts_dir,omitempty"`
	SUTName          string `json:"sut_name,omitempty"`
	IncludeArtifacts bool   `json:"include_artifacts,omitempty"`
	CollectionPath   string `json:"collection_path,omitempty"`
	YAMLPath         string `json:"yaml_path,omitempty"`
	WorkflowPath     string `json:"workflow_path,omitempty"`
	HTMLPath         string `json:"html_path,omitempty"`
}

func (a *App) handleRuntimeReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload runtimeReportRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	artifactsDir := strings.TrimSpace(payload.ArtifactsDir)
	if artifactsDir == "" {
		artifactsDir = a.cfg.Artifact.OutputDir
	}
	report, err := coverage.Generate(r.Context(), artifactsDir, payload.SUTName, payload.IncludeArtifacts)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	collectionPath := firstNonEmpty(payload.CollectionPath, payload.YAMLPath, filepath.Join(artifactsDir, "coverage.postman_collection.json"))
	workflowPath := firstNonEmpty(payload.WorkflowPath, filepath.Join(artifactsDir, "coverage.workflow.yaml"))
	htmlPath := firstNonEmpty(payload.HTMLPath, filepath.Join(artifactsDir, "coverage.html"))
	for _, output := range []struct{ path, format string }{{collectionPath, "postman"}, {workflowPath, "workflow"}, {htmlPath, "html"}} {
		if err := writeCoverageReport(output.path, report, output.format); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	response := map[string]any{
		"collection_path": collectionPath, "workflow_path": workflowPath,
		"html_path": htmlPath, "report": report,
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleKillServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.hasActiveSessions() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "capture sessions are still active; teardown tests before stopping the runtime"})
		return
	}
	if err := a.lifecycle.Stop(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	a.resetCaptureRouting()
	writeJSON(w, http.StatusOK, map[string]string{"status": "killed"})
}

func (a *App) configureProxy(payload proxyStartRequest) error {
	if listen := strings.TrimSpace(payload.ListenAddr); listen != "" && listen != a.proxyComponent.ListenAddr() {
		if err := a.proxyComponent.SetListenAddr(listen); err != nil {
			return err
		}
	}
	if target := strings.TrimSpace(payload.TargetURL); target != "" {
		if err := a.setReverseTarget(target); err != nil {
			return fmt.Errorf("invalid proxy target_url: %w", err)
		}
	}
	return nil
}

func writeCoverageReport(path string, report *coverage.Report, format string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return coverage.WriteReport(file, report, format)
}

func writeGeneratedSchema(path string, report *coverage.Report, options coverage.GeneratedOpenAPIOptions, force bool) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("generated schema output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("generated schema already exists at %s; set force to overwrite: %w", path, err)
		}
		return err
	}
	defer file.Close()
	if err := coverage.WriteGeneratedOpenAPI(file, report, options); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	return nil
}

func (a *App) environmentSnapshot() *artifact.Environment {
	env := &artifact.Environment{}
	if a.proxyComponent != nil {
		info := mergeProxyInfo(a.proxyComponent)
		env.ProxyListen = normalizeURL(info.ListenAddr)
		env.ProxyMode = string(info.Mode)
	}
	return env
}

func writeArtifactResponse(w http.ResponseWriter, artifactDoc *artifact.Artifact, path, sessionID string) {
	resp := map[string]any{"artifact": artifactDoc, "path": path}
	if sessionID != "" {
		resp["session_id"] = sessionID
	}
	if artifactDoc != nil && artifactDoc.SchemaValidation != nil {
		resp["schema_validation"] = artifactDoc.SchemaValidation
	}
	writeJSON(w, http.StatusOK, resp)
}

func logFinishedCase(a *App, artifactDoc *artifact.Artifact, path string) {
	if a == nil || a.logger == nil || artifactDoc == nil {
		return
	}
	schemaStatus := ""
	schemaErrors := 0
	if artifactDoc.SchemaValidation != nil {
		schemaStatus = artifactDoc.SchemaValidation.Status
		schemaErrors = len(artifactDoc.SchemaValidation.Errors)
	}
	a.logger.Info("testcase finished", "id", artifactDoc.TestCase.ID, "name", artifactDoc.TestCase.Name, "status", artifactDoc.TestCase.Status, "schema_status", schemaStatus, "schema_errors", schemaErrors, "path", path)
}

func defaultStatus(status string) string {
	if value := strings.TrimSpace(status); value != "" {
		return value
	}
	return "pass"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func decodeStrictJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func normalizeURL(raw string) string {
	if raw == "" || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	return "http://" + raw
}

func mergeProxyInfo(component *ProxyComponent) proxy.Info {
	if component == nil {
		return proxy.Info{}
	}
	info := component.Info()
	if info.ListenAddr == "" {
		info.ListenAddr = component.ListenAddr()
	}
	return info
}
