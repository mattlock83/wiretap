package app

import (
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
	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/bundlereport"
	"github.com/phrond/evermock/internal/coverage"
	"github.com/phrond/evermock/internal/proxy"
	"github.com/phrond/evermock/internal/tracecontext"
	"go.opentelemetry.io/otel/trace"
)

type proxyOptions struct {
	ListenAddr string `json:"listen_addr,omitempty"`
	TargetURL  string `json:"target_url,omitempty"`
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

type startRunRequest struct {
	Proxy proxyOptions `json:"proxy,omitempty"`
}

func (a *App) handleStartRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capture proxy not configured"})
		return
	}
	var payload startRunRequest
	if err := decodeStrictJSON(r, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := a.configureProxy(payload.Proxy); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := a.startRun(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	info := mergeProxyInfo(a.proxyComponent)
	response := map[string]any{
		"proxy":               info,
		"sut_url":             normalizeURL(info.ListenAddr),
		"dependency_base_url": normalizeURL(info.ListenAddr) + "/__phrond/dep",
		"defaults":            a.testDefaults(),
	}
	a.addBundleLocations(response)
	writeJSON(w, http.StatusOK, response)
}

type setupTestRequest struct {
	SchemaRequired  *bool                    `json:"schema_required,omitempty"`
	Redactions      []artifact.Redaction     `json:"redactions,omitempty"`
	APIID           string                   `json:"api_id,omitempty"`
	SUTTargetKind   string                   `json:"sut_target_kind,omitempty"`
	TestSource      map[string]any           `json:"test_source,omitempty"`
	SUTSource       map[string]any           `json:"sut_source,omitempty"`
	Name            string                   `json:"name,omitempty"`
	Tags            []string                 `json:"tags,omitempty"`
	Meta            map[string]any           `json:"meta,omitempty"`
	SUTBaseURL      string                   `json:"sut_base_url,omitempty"`
	Schemas         []artifact.SchemaRef     `json:"schemas,omitempty"`
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
	payload := a.testDefaults()
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
	conflict := (root.IsValid() && reused) || (!payload.ExplicitSession && a.hasRetiredWorkLocked())
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
	if payload.APIID == "" {
		if current := a.currentBundle(); current != nil {
			payload.APIID = current.Snapshot().APIID
		}
	}
	for i := range schemas {
		schemas[i].Role = "sut"
		if payload.APIID != "" {
			schemas[i].AssetID = payload.APIID
		}
	}
	if err := validateSchemaSetup(schemas, payload.GenerateSchema || (!requiresSchema(payload.SchemaRequired) && len(schemas) == 0)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(schemas) == 0 && !payload.GenerateSchema && !requiresSchema(payload.SchemaRequired) {
		schemas = []artifact.SchemaRef{omittedSchema(payload.APIID, "sut")}
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
	bundleWriter, err := a.ensureBundle()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	dependencyKinds := make(map[string]string, len(dependencies))
	for id, dep := range dependencies {
		dependencyKinds[id] = dep.UpstreamKind
	}
	if err := bundleWriter.Configure(bundle.Identity{APIID: payload.APIID, SUTTargetKind: payload.SUTTargetKind, DependencyKinds: dependencyKinds, TestSource: payload.TestSource, SUTSource: payload.SUTSource}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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
	store := NewTestCaseStore(bundleWriter, a.cfg.Capture.MaxBodyBytes)
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
	for _, rule := range payload.Redactions {
		store.AddRedaction(rule)
	}
	store.SetTraceContext(root)
	if err := store.RegisterActive(); err != nil {
		store.AbortCase()
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
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
	response := map[string]any{
		"id": tc.ID, "started_at": tc.StartedAt,
		"trace_context": payload.TraceContext,
		"session_id":    sessionID,
		"name":          name,
		"explicit":      payload.ExplicitSession,
		"proxy_listen":  proxyURL,
		"sut_url":       proxyURL,
		"sut_base_url":  payload.SUTBaseURL,
		"dependencies":  dependencyURLs,
	}
	a.addBundleLocations(response)
	writeJSON(w, http.StatusOK, response)
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
	if err := decodeStrictJSON(r, &payload); err != nil {
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
	closing := session != nil && session.closing
	a.sessionsMu.Unlock()
	if session == nil {
		a.controlMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if closing && !session.Store.HasPendingCommit() {
		a.controlMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "capture session is already closing"})
		return
	}
	session.finishMu.Lock()
	defer session.finishMu.Unlock()
	a.sessionsMu.Lock()
	current := a.sessions[sessionID]
	a.sessionsMu.Unlock()
	if current != session {
		a.controlMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if !a.beginDrain(session) && !session.Store.HasPendingCommit() {
		a.controlMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "capture session is already closing"})
		return
	}
	env := a.environmentSnapshot()
	env.SUTBaseURL = session.SUTBaseURL
	a.controlMu.Unlock()

	a.waitForDrain(r.Context(), session, timeout)
	artifactDoc, path, err := session.Store.FinishCase(defaultStatus(payload.Status), env)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	a.sessionsMu.Lock()
	delete(a.sessions, sessionID)
	a.sessionsMu.Unlock()
	logFinishedCase(a, artifactDoc, path)
	a.writeCaptureResponse(w, artifactDoc, path, sessionID)
}

type generateSchemaRequest struct {
	BundlePath string `json:"bundle_path,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
	Title      string `json:"title,omitempty"`
	Version    string `json:"version,omitempty"`
	Force      bool   `json:"force,omitempty"`
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
	bundlePath := strings.TrimSpace(payload.BundlePath)
	if bundlePath == "" {
		if current := a.currentBundle(); current != nil {
			bundlePath = current.Path()
		}
	}
	if bundlePath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bundle_path is required when no capture run exists"})
		return
	}
	outputPath := strings.TrimSpace(payload.OutputPath)
	if outputPath == "" {
		outputPath = filepath.Join(bundlePath, "generated.openapi.yaml")
	}
	report, err := coverage.Generate(r.Context(), bundlePath, payload.Title, false)
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
	BundlePath   string `json:"bundle_path,omitempty"`
	WorkflowPath string `json:"workflow_path,omitempty"`
}

func (a *App) handleRuntimeReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload runtimeReportRequest
	if err := decodeStrictJSON(r, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	input := strings.TrimSpace(payload.BundlePath)
	if input == "" {
		if current := a.currentBundle(); current != nil {
			input = current.Path()
		}
	}
	if input == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bundle_path is required when no capture run exists"})
		return
	}
	report, err := bundlereport.Rebuild(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	workflowPath := strings.TrimSpace(payload.WorkflowPath)
	if workflowPath != "" {
		if err := writeCoverageReport(workflowPath, report, "workflow"); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	response := map[string]any{"report": report}
	run, _, err := bundle.Load(input)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	absolute, _ := filepath.Abs(input)
	response["run_id"] = run.RunID
	response["bundle_path"] = absolute
	response["index_path"] = filepath.Join(absolute, "index.md")
	response["viewer_path"] = filepath.Join(absolute, "viewer", "index.html")
	response["run_state"] = run.RunState
	response["snapshot_revision"] = run.Revision
	if workflowPath != "" {
		response["workflow_path"] = workflowPath
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleFinishRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if _, err := a.finishRun(r.Context()); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "complete"})
}

func (a *App) configureProxy(payload proxyOptions) error {
	if payload.TargetURL == "" && a.reverseTarget == "" {
		payload.TargetURL = a.testDefaults().SUTBaseURL
	}
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

func (a *App) addBundleLocations(response map[string]any) {
	if current := a.currentBundle(); current != nil {
		snapshot := current.Snapshot()
		response["run_id"] = snapshot.RunID
		response["bundle_path"] = current.Path()
		response["index_path"] = current.IndexPath()
		response["viewer_path"] = filepath.Join(current.Path(), "viewer", "index.html")
		response["run_state"] = snapshot.RunState
		response["snapshot_revision"] = snapshot.Revision
	}
}

func (a *App) writeCaptureResponse(w http.ResponseWriter, art *artifact.Artifact, path, sessionID string) {
	response := map[string]any{"capture": art, "attempt_path": path}
	if sessionID != "" {
		response["session_id"] = sessionID
	}
	if current := a.currentBundle(); current != nil && path != "" {
		if _, err := bundlereport.Refresh(current.Path()); err != nil {
			response["view_error"] = err.Error()
			if a.logger != nil {
				a.logger.Warn("bundle view refresh failed", "error", err)
			}
		} else if art != nil {
			response["attempt_page_path"] = filepath.Join(current.Path(), "tests", art.TestCase.ID+".md")
		}
	}
	a.addBundleLocations(response)
	writeJSON(w, http.StatusOK, response)
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
