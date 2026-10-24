package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/proxy"
	"github.com/phrond/wiretap/internal/redact"
	"github.com/phrond/wiretap/internal/runoutput"
	"github.com/phrond/wiretap/internal/tracecontext"
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
	Proxy       proxyOptions        `json:"proxy,omitempty"`
	Boundaries  json.RawMessage     `json:"boundaries,omitempty"`
	Application *bundle.Application `json:"application,omitempty"`
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
	application, err := a.resolveApplication(payload.Application)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := a.configureRunBoundaries(payload.Boundaries); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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
	if application != nil {
		if err := a.currentBundle().Configure(bundle.Identity{Application: application}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := a.initializeRunContracts(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	info := mergeProxyInfo(a.proxyComponent)
	sutURL := normalizeURL(info.ListenAddr)
	if a.project != nil && a.project.SUT.Protocol == "grpc" {
		sutURL = a.grpcSUTURL()
	}
	defaults := a.testDefaults()
	defaults.Application = a.currentBundle().Snapshot().Application
	response := map[string]any{
		"proxy":               info,
		"sut_url":             sutURL,
		"dependency_base_url": sutURL + "/__wiretap/dep",
		"defaults":            defaults,
		"dependencies":        a.grpcDependencyURLs(),
		"contracts":           a.currentBundle().Snapshot().Contracts,
	}
	a.addBundleLocations(response)
	writeJSON(w, http.StatusOK, response)
}

type setupTestRequest struct {
	Identity        *artifact.TestIdentity   `json:"test_identity,omitempty"`
	Application     *bundle.Application      `json:"application,omitempty"`
	SUTProtocol     string                   `json:"sut_protocol,omitempty"`
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
	ExplicitSession bool                     `json:"explicit_session,omitempty"`
	TraceContext    *tracecontext.Context    `json:"trace_context,omitempty"`
}

func (a *App) handleSetupTest(w http.ResponseWriter, r *http.Request) {
	a.extendGRPCControlDeadline(w)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capture proxy not configured"})
		return
	}
	payload := a.testDefaults()
	// Decode an explicitly supplied application as a whole object. In particular,
	// a failed partial override must never mutate project or run defaults.
	payload.Application = nil
	if err := decodeStrictJSON(r, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	application, err := a.resolveApplication(payload.Application)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	payload.Application = application
	// Project rules are a minimum policy, even when a caller supplies its own
	// rules or an empty list. Per-test rules can only add redactions.
	if a.project != nil {
		// Apply the minimum policy last so a per-test replacement cannot restore
		// a value masked by a project rule for the same field.
		rules := append(append([]artifact.Redaction(nil), payload.Redactions...), a.project.Redactions...)
		last := map[artifact.Redaction]int{}
		for i, rule := range rules {
			last[rule] = i
		}
		payload.Redactions = nil
		for i, rule := range rules {
			if last[rule] == i {
				payload.Redactions = append(payload.Redactions, rule)
			}
		}
	}
	if err := redact.Validate(payload.Redactions); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := payload.Identity.Validate(); err != nil {

		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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
	if payload.SUTProtocol != "" && payload.SUTProtocol != "http" && payload.SUTProtocol != "grpc" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sut_protocol must be http or grpc"})
		return
	}
	configuredGRPC := a.project != nil && a.project.SUT.Protocol == "grpc"
	if (payload.SUTProtocol == "grpc") != configuredGRPC {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gRPC SUT protocol must match the project configuration"})
		return
	}
	if payload.SUTProtocol == "grpc" && !requiresSchema(payload.SchemaRequired) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gRPC requires a Protobuf contract"})
		return
	}
	if payload.SUTProtocol == "grpc" && (len(schemas) != 0 || payload.SUTBaseURL != a.project.SUT.TargetURL()) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gRPC SUT target and contract are fixed by project configuration"})
		return
	}
	if payload.SUTProtocol != "grpc" {
		if err := validateSchemaSetup(schemas, !requiresSchema(payload.SchemaRequired)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if len(schemas) == 0 && !requiresSchema(payload.SchemaRequired) {
			schemas = []artifact.SchemaRef{omittedSchema(payload.APIID, "sut")}
		}
	}
	dependencies, dependencySchemas, err := normalizeDependencies(payload.Dependencies)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if a.project != nil {
		for id, configured := range a.project.Dependencies {
			if configured.Protocol != "grpc" {
				continue
			}
			selected, ok := dependencies[id]
			if !ok || selected.Protocol != "grpc" || selected.UpstreamURL != configured.TargetURL() {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("gRPC dependency %q must match the project configuration", id)})
				return
			}
		}
	}
	for id, selected := range dependencies {
		if selected.Protocol == "grpc" && (a.project == nil || a.project.Dependencies[id].Protocol != "grpc") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("gRPC dependency %q requires project configuration", id)})
			return
		}
		if selected.Protocol == "grpc" && len(selected.Schemas) != 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("gRPC dependency %q contract is fixed by project configuration", id)})
			return
		}
	}
	grpcSchemas, grpcContracts, err := a.resolveGRPCContracts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	for _, ref := range grpcSchemas {
		if ref.Role == "sut" {
			schemas = append(schemas, ref)
		} else {
			dependencySchemas = append(dependencySchemas, ref)
		}
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
	if err := bundleWriter.Configure(bundle.Identity{Application: payload.Application, APIID: payload.APIID, SUTTargetKind: payload.SUTTargetKind, DependencyKinds: dependencyKinds, TestSource: payload.TestSource, SUTSource: payload.SUTSource}); err != nil {
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
	store.SetGRPCContracts(grpcContracts)
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
	for _, rule := range payload.Redactions {
		store.AddRedaction(rule)
	}
	store.SetTraceContext(root)
	store.SetTestIdentity(payload.Identity)
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
	if payload.SUTProtocol == "grpc" {
		proxyURL = a.grpcSUTURL()
	}
	dependencyURLs := make(map[string]string, len(dependencies))
	for assetID := range dependencies {
		if dependencies[assetID].Protocol == "grpc" {
			dependencyURLs[assetID] = a.grpcDependencyURL(assetID)
			continue
		}
		if payload.ExplicitSession && !root.IsValid() {
			dependencyURLs[assetID] = proxyURL + "/__wiretap/session/" + sessionID + "/dep/" + assetID
		} else {
			dependencyURLs[assetID] = proxyURL + "/__wiretap/dep/" + assetID
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

type runtimeReportRequest struct {
	BundlePath string `json:"bundle_path,omitempty"`
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
	report, err := coverage.Generate(r.Context(), input, "", false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
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
	response["run_state"] = run.RunState
	response["snapshot_revision"] = run.Revision
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleFinishRun(w http.ResponseWriter, r *http.Request) {
	a.extendGRPCControlDeadline(w)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload struct {
		Outputs *runoutput.Config `json:"outputs,omitempty"`
	}
	if err := decodeStrictJSON(r, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	selected := payload.Outputs
	if selected == nil && a.project != nil {
		selected = a.project.Outputs
	}
	if selected != nil {
		if err := selected.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	runID := ""
	if current := a.currentBundle(); current != nil {
		runID = current.Snapshot().RunID
	}
	archivePath, err := a.finishRun(r.Context())
	if err != nil {
		response := map[string]string{"error": err.Error()}
		if archivePath != "" {
			response["bundle_path"] = archivePath
		}
		writeJSON(w, http.StatusConflict, response)
		return
	}
	response := map[string]any{"status": "complete"}
	if archivePath != "" {
		response["bundle_path"] = archivePath
		response["run_id"] = runID
		if selected != nil {
			outputs, err := runoutput.Generate(r.Context(), archivePath, *selected)
			response["outputs"] = outputs
			if err != nil {
				response["error"] = err.Error()
				writeJSON(w, http.StatusConflict, response)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) configureProxy(payload proxyOptions) error {
	if a.project != nil && a.project.SUT.Protocol == "grpc" && payload.TargetURL != "" && strings.TrimRight(payload.TargetURL, "/") != a.project.SUT.TargetURL() {
		return errors.New("gRPC target is fixed by the run boundary definition")
	}
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
		response["run_state"] = snapshot.RunState
		response["snapshot_revision"] = snapshot.Revision
	}
}

func (a *App) writeCaptureResponse(w http.ResponseWriter, art *artifact.Artifact, path, sessionID string) {
	response := map[string]any{"capture": art, "attempt_path": path}
	if sessionID != "" {
		response["session_id"] = sessionID
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
