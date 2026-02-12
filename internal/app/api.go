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
	"github.com/phrond/evermock/internal/coverage"
	"github.com/phrond/evermock/internal/mock"
	"github.com/phrond/evermock/internal/proxy"
)

type testCaseStartRequest struct {
	Name       string               `json:"name"`
	Tags       []string             `json:"tags"`
	Meta       map[string]any       `json:"meta"`
	SUTBaseURL string               `json:"sut_base_url"`
	Schemas    []artifact.SchemaRef `json:"schemas"`
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
	if a.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
		return
	}
	if len(req.Schemas) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one schema is required"})
		return
	}

	if a.proxyComponent != nil {
		if err := a.proxyComponent.Start(r.Context()); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if a.mockComponent != nil {
		if err := a.mockComponent.Start(r.Context()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	if a.mockComponent != nil {
		if recorder := a.mockComponent.Recorder(); recorder != nil {
			recorder.Clear()
		}
	}

	tc, err := a.store.StartCase(req.Name, req.Tags, req.Meta, req.SUTBaseURL)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	for _, schema := range req.Schemas {
		a.store.AddSchema(schema)
	}
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

	var exchanges []mock.Exchange
	if a.mockComponent != nil {
		if recorder := a.mockComponent.Recorder(); recorder != nil {
			exchanges = recorder.Exchanges()
		}
	}

	env := a.environmentSnapshot()
	artifactDoc, path, err := a.store.FinishCase(req.Status, env, exchanges)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	if a.mockComponent != nil {
		_ = a.mockComponent.Stop(r.Context())
	}
	if a.proxyComponent != nil {
		_ = a.proxyComponent.Stop(r.Context())
	}

	if a.logger != nil && artifactDoc != nil {
		schemaStatus := ""
		schemaErrors := 0
		if artifactDoc.SchemaValidation != nil {
			schemaStatus = artifactDoc.SchemaValidation.Status
			schemaErrors = len(artifactDoc.SchemaValidation.Errors)
		}
		a.logger.Info("testcase finished",
			"id", artifactDoc.TestCase.ID,
			"name", artifactDoc.TestCase.Name,
			"status", artifactDoc.TestCase.Status,
			"schema_status", schemaStatus,
			"schema_errors", schemaErrors,
			"path", path,
		)
	}

	resp := map[string]any{
		"artifact": artifactDoc,
		"path":     path,
	}
	if artifactDoc != nil && artifactDoc.SchemaValidation != nil {
		resp["schema_validation"] = artifactDoc.SchemaValidation
	}
	writeJSON(w, http.StatusOK, resp)
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
	status := a.store.Status()
	writeJSON(w, http.StatusOK, status)
}

func (a *App) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
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

func (a *App) handleStubs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.mockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mock server not configured"})
		return
	}

	var stub mock.Stub
	if err := json.NewDecoder(r.Body).Decode(&stub); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(stub.ID) == "" {
		stub.ID = "stub"
	}
	a.mockComponent.RegisterStub(stub)
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered", "id": stub.ID})
}

func (a *App) handleStubsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.mockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mock server not configured"})
		return
	}
	a.mockComponent.ClearStubs()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (a *App) handleMockExchanges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.mockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mock server not configured"})
		return
	}
	recorder := a.mockComponent.Recorder()
	if recorder == nil {
		writeJSON(w, http.StatusOK, []mock.Exchange{})
		return
	}
	writeJSON(w, http.StatusOK, recorder.Exchanges())
}

type proxyStartRequest struct {
	ListenAddr string `json:"listen_addr,omitempty"`
}

type mockStartRequest struct {
	ListenAddr string `json:"listen_addr,omitempty"`
}

func (a *App) handleExpectations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
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
	if a.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
		return
	}
	var schema artifact.SchemaRef
	if err := json.NewDecoder(r.Body).Decode(&schema); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	stored := a.store.AddSchema(schema)
	writeJSON(w, http.StatusOK, stored)
}

func (a *App) handleRedactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
		return
	}
	var rule artifact.Redaction
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	stored := a.store.AddRedaction(rule)
	writeJSON(w, http.StatusOK, stored)
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
	if payload.ListenAddr != "" {
		if err := a.proxyComponent.SetListenAddr(payload.ListenAddr); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := a.proxyComponent.Start(r.Context()); err != nil {
		if errors.Is(err, proxy.ErrAlreadyStarted) {
			writeJSON(w, http.StatusOK, mergeProxyInfo(a.proxyComponent))
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, mergeProxyInfo(a.proxyComponent))
}

func (a *App) handleMockStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.mockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mock not configured"})
		return
	}
	var payload mockStartRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if payload.ListenAddr != "" {
		if err := a.mockComponent.SetListenAddr(payload.ListenAddr); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := a.mockComponent.Start(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"addr": a.mockComponent.Addr()})
}

func (a *App) handleProxyMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "proxy not configured"})
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
	if a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "proxy not configured"})
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
	Proxy struct {
		ListenAddr string `json:"listen_addr,omitempty"`
		TargetURL  string `json:"target_url,omitempty"`
	} `json:"proxy,omitempty"`
	Mock struct {
		ListenAddr string `json:"listen_addr,omitempty"`
	} `json:"mock,omitempty"`
	StackSize       int `json:"stack_size,omitempty"`
	RecordResponses struct {
		DependencyURL string `json:"dependency_url,omitempty"`
	} `json:"record_responses,omitempty"`
}

func (a *App) handleStartServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.proxyComponent == nil || a.mockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "runtime components not configured"})
		return
	}

	var payload startServerRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if payload.Proxy.ListenAddr != "" {
		if err := a.proxyComponent.SetListenAddr(payload.Proxy.ListenAddr); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	if payload.Mock.ListenAddr != "" {
		if err := a.mockComponent.SetListenAddr(payload.Mock.ListenAddr); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	if payload.StackSize > 0 {
		a.sessionsMu.Lock()
		a.stackSize = payload.StackSize
		a.sessionsMu.Unlock()
	}
	if strings.TrimSpace(payload.RecordResponses.DependencyURL) != "" {
		if err := a.mockComponent.SetFallbackUpstream(payload.RecordResponses.DependencyURL); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	if err := a.proxyComponent.Start(r.Context()); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := a.mockComponent.Start(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	a.sessionsMu.Lock()
	stackSize := a.stackSize
	a.sessionsMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"proxy": map[string]any{
			"listen_addr": a.proxyComponent.ListenAddr(),
			"target_url":  strings.TrimSpace(payload.Proxy.TargetURL),
		},
		"mock": map[string]any{
			"listen_addr": a.mockComponent.Addr(),
		},
		"stack_size": stackSize,
	})
}

type setupTestRequest struct {
	Name        string               `json:"name,omitempty"`
	Tags        []string             `json:"tags,omitempty"`
	Meta        map[string]any       `json:"meta,omitempty"`
	SUTBaseURL  string               `json:"sut_base_url,omitempty"`
	SUTSchemas  []artifact.SchemaRef `json:"sut_schemas,omitempty"`
	FakeSchemas []artifact.SchemaRef `json:"fake_schemas,omitempty"`
}

func (a *App) handleSetupTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.mockComponent == nil || a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "runtime components not configured"})
		return
	}
	if a.mockComponent.Addr() == "" || a.proxyComponent.ListenAddr() == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is not started; call startServer() first"})
		return
	}

	var payload setupTestRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if len(payload.SUTSchemas) == 0 && len(payload.FakeSchemas) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one schema is required"})
		return
	}

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = "scenario-" + time.Now().UTC().Format("20060102150405.000")
	}

	store := NewTestCaseStore(a.cfg.Artifact.OutputDir)
	if _, err := store.StartCase(name, payload.Tags, payload.Meta, payload.SUTBaseURL); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	for _, schema := range payload.SUTSchemas {
		schema.Role = "sut"
		store.AddSchema(schema)
	}
	for _, schema := range payload.FakeSchemas {
		schema.Role = "fake"
		store.AddSchema(schema)
	}

	a.sessionsMu.Lock()
	sessionID := a.nextSessionID()
	session := &runtimeSession{
		ID:             sessionID,
		Name:           name,
		Store:          store,
		SUTBaseURL:     payload.SUTBaseURL,
		CreatedAt:      time.Now().UTC(),
		MockBaseURL:    normalizeURL(a.mockComponent.Addr()) + sessionPathPrefix(sessionID),
		QueueOrder:     []string{},
		Queued:         map[string]*queuedResponse{},
		RespondedOrder: []string{},
		Responded:      map[string]*queuedResponse{},
	}
	a.sessions[sessionID] = session
	a.sessionsMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":    sessionID,
		"name":          name,
		"proxy_listen":  normalizeURL(a.proxyComponent.ListenAddr()),
		"mock_base_url": session.MockBaseURL,
	})
}

type queueResponseRequest struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id,omitempty"`
	Match     struct {
		Method       string            `json:"method,omitempty"`
		Path         string            `json:"path,omitempty"`
		Headers      map[string]string `json:"headers,omitempty"`
		Query        map[string]string `json:"query,omitempty"`
		Body         string            `json:"body,omitempty"`
		BodyContains string            `json:"body_contains,omitempty"`
	} `json:"match"`
	Response struct {
		Status          int               `json:"status,omitempty"`
		DelayMS         int               `json:"delay_ms,omitempty"`
		Headers         map[string]string `json:"headers,omitempty"`
		Body            string            `json:"body,omitempty"`
		BodyFile        string            `json:"body_file,omitempty"`
		ConnectionError bool              `json:"connection_error,omitempty"`
		SSLError        bool              `json:"ssl_error,omitempty"`
	} `json:"response"`
}

func (a *App) handleQueueResponse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.mockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mock component not configured"})
		return
	}

	var payload queueResponseRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	method := strings.ToUpper(strings.TrimSpace(payload.Match.Method))
	path := strings.TrimSpace(payload.Match.Path)
	if method == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "match.method is required"})
		return
	}
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "match.path is required"})
		return
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if payload.Response.ConnectionError && payload.Response.SSLError {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "connection_error and ssl_error cannot both be true"})
		return
	}

	a.sessionsMu.Lock()
	session := a.sessions[sessionID]
	if session == nil {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	a.syncSessionResponsesLocked(session)
	if len(session.Queued) >= a.stackSize {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("stack is full (size=%d)", a.stackSize)})
		return
	}

	responseID := strings.TrimSpace(payload.ID)
	if responseID == "" {
		responseID = "response-" + fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	if _, exists := session.Queued[responseID]; exists {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "response id already queued"})
		return
	}
	if _, exists := session.Responded[responseID]; exists {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "response id already used"})
		return
	}

	body := payload.Response.Body
	if strings.TrimSpace(payload.Response.BodyFile) != "" {
		content, err := os.ReadFile(resolvePath(payload.Response.BodyFile))
		if err != nil {
			a.sessionsMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		body = string(content)
	}
	fault := ""
	if payload.Response.ConnectionError {
		fault = "connection_error"
	} else if payload.Response.SSLError {
		fault = "ssl_error"
	}
	stub := mock.Stub{
		ID:        responseID,
		SessionID: sessionID,
		Match: mock.StubMatch{
			Method:       method,
			Path:         withSessionPath(sessionID, path),
			Headers:      payload.Match.Headers,
			Query:        payload.Match.Query,
			Body:         payload.Match.Body,
			BodyContains: payload.Match.BodyContains,
		},
		Response: mock.StubResponse{
			Status:  payload.Response.Status,
			DelayMS: payload.Response.DelayMS,
			Headers: payload.Response.Headers,
			Body:    body,
			Fault:   fault,
		},
	}
	session.Queued[responseID] = &queuedResponse{
		ID:        responseID,
		Stub:      stub,
		Source:    "queued",
		CreatedAt: time.Now().UTC(),
	}
	session.QueueOrder = append(session.QueueOrder, responseID)
	a.sessionsMu.Unlock()

	a.mockComponent.RegisterStub(stub)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "queued",
		"session_id": sessionID,
		"id":         responseID,
	})
}

type teardownTestRequest struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status,omitempty"`
}

func (a *App) handleTeardownTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.mockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mock component not configured"})
		return
	}

	var payload teardownTestRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	status := strings.TrimSpace(payload.Status)
	if status == "" {
		status = "pass"
	}

	a.sessionsMu.Lock()
	session := a.sessions[sessionID]
	if session == nil {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	a.syncSessionResponsesLocked(session)
	queuedIDs := make([]string, 0, len(session.Queued))
	for _, id := range session.QueueOrder {
		if _, ok := session.Queued[id]; ok {
			queuedIDs = append(queuedIDs, id)
		}
	}
	delete(a.sessions, sessionID)
	a.sessionsMu.Unlock()

	if len(queuedIDs) > 0 {
		a.mockComponent.RemoveStubsByID(queuedIDs)
	}

	var exchanges []mock.Exchange
	if recorder := a.mockComponent.Recorder(); recorder != nil {
		for _, exchange := range recorder.Exchanges() {
			if exchange.SessionID == sessionID {
				exchanges = append(exchanges, exchange)
			}
		}
	}

	env := a.environmentSnapshot()
	env.MockBaseURL = session.MockBaseURL

	artifactDoc, path, err := session.Store.FinishCase(status, env, exchanges)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	resp := map[string]any{
		"artifact":   artifactDoc,
		"path":       path,
		"session_id": sessionID,
	}
	if artifactDoc != nil && artifactDoc.SchemaValidation != nil {
		resp["schema_validation"] = artifactDoc.SchemaValidation
	}
	writeJSON(w, http.StatusOK, resp)
}

type runtimeReportRequest struct {
	ArtifactsDir     string `json:"artifacts_dir,omitempty"`
	SUTName          string `json:"sut_name,omitempty"`
	IncludeArtifacts bool   `json:"include_artifacts,omitempty"`
	CollectionPath   string `json:"collection_path,omitempty"`
	YAMLPath         string `json:"yaml_path,omitempty"` // Deprecated alias for collection_path.
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

	collectionPath := strings.TrimSpace(payload.CollectionPath)
	if collectionPath == "" {
		collectionPath = strings.TrimSpace(payload.YAMLPath)
	}
	if collectionPath == "" {
		collectionPath = filepath.Join(artifactsDir, "coverage.postman_collection.json")
	}
	htmlPath := strings.TrimSpace(payload.HTMLPath)
	if htmlPath == "" {
		htmlPath = filepath.Join(artifactsDir, "coverage.html")
	}

	if err := writeCoverageReport(collectionPath, report, "postman"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := writeCoverageReport(htmlPath, report, "html"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"collection_path": collectionPath,
		"html_path":       htmlPath,
		"report":          report,
	})
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

func (a *App) handleKillServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	a.sessionsMu.Lock()
	var staleIDs []string
	for _, session := range a.sessions {
		for _, id := range session.QueueOrder {
			if _, ok := session.Queued[id]; ok {
				staleIDs = append(staleIDs, id)
			}
		}
	}
	a.sessions = map[string]*runtimeSession{}
	a.sessionsMu.Unlock()

	if len(staleIDs) > 0 {
		a.mockComponent.RemoveStubsByID(staleIDs)
	}

	if a.mockComponent != nil {
		a.mockComponent.ClearStubs()
		if recorder := a.mockComponent.Recorder(); recorder != nil {
			recorder.Clear()
		}
	}
	if err := a.lifecycle.Stop(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "killed"})
}

func (a *App) handleListQueued(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}

	a.sessionsMu.Lock()
	session := a.sessions[sessionID]
	if session == nil {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	a.syncSessionResponsesLocked(session)
	out := make([]map[string]any, 0, len(session.Queued))
	for i := len(session.QueueOrder) - 1; i >= 0; i-- {
		id := session.QueueOrder[i]
		item := session.Queued[id]
		if item == nil {
			continue
		}
		_, unprefixedPath := stripSessionPath(item.Stub.Match.Path)
		out = append(out, map[string]any{
			"id":         item.ID,
			"session_id": sessionID,
			"created_at": item.CreatedAt,
			"source":     item.Source,
			"match": map[string]any{
				"method":        item.Stub.Match.Method,
				"path":          unprefixedPath,
				"headers":       item.Stub.Match.Headers,
				"query":         item.Stub.Match.Query,
				"body":          item.Stub.Match.Body,
				"body_contains": item.Stub.Match.BodyContains,
			},
			"response": map[string]any{
				"status":   item.Stub.Response.Status,
				"delay_ms": item.Stub.Response.DelayMS,
				"headers":  item.Stub.Response.Headers,
				"body":     item.Stub.Response.Body,
				"fault":    item.Stub.Response.Fault,
			},
		})
	}
	a.sessionsMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"items":      out,
	})
}

func (a *App) handleListResponded(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}

	a.sessionsMu.Lock()
	session := a.sessions[sessionID]
	if session == nil {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	a.syncSessionResponsesLocked(session)
	out := make([]map[string]any, 0, len(session.Responded))
	for _, id := range session.RespondedOrder {
		item := session.Responded[id]
		if item == nil {
			continue
		}
		_, unprefixedPath := stripSessionPath(item.Stub.Match.Path)
		entry := map[string]any{
			"id":           item.ID,
			"session_id":   sessionID,
			"created_at":   item.CreatedAt,
			"responded_at": item.RespondedAt,
			"source":       item.Source,
			"match": map[string]any{
				"method":        item.Stub.Match.Method,
				"path":          unprefixedPath,
				"headers":       item.Stub.Match.Headers,
				"query":         item.Stub.Match.Query,
				"body":          item.Stub.Match.Body,
				"body_contains": item.Stub.Match.BodyContains,
			},
			"response": map[string]any{
				"status":   item.Stub.Response.Status,
				"delay_ms": item.Stub.Response.DelayMS,
				"headers":  item.Stub.Response.Headers,
				"body":     item.Stub.Response.Body,
				"fault":    item.Stub.Response.Fault,
			},
		}
		if item.LastExchange != nil {
			entry["exchange"] = item.LastExchange
		}
		out = append(out, entry)
	}
	a.sessionsMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"items":      out,
	})
}

func (a *App) handleGetResponse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if sessionID == "" || id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id and id are required"})
		return
	}

	a.sessionsMu.Lock()
	session := a.sessions[sessionID]
	if session == nil {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	a.syncSessionResponsesLocked(session)
	item := session.Queued[id]
	state := "queued"
	if item == nil {
		item = session.Responded[id]
		state = "responded"
	}
	if item == nil {
		a.sessionsMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "response id not found"})
		return
	}
	_, unprefixedPath := stripSessionPath(item.Stub.Match.Path)
	resp := map[string]any{
		"id":           item.ID,
		"state":        state,
		"session_id":   sessionID,
		"created_at":   item.CreatedAt,
		"responded_at": item.RespondedAt,
		"source":       item.Source,
		"match": map[string]any{
			"method":        item.Stub.Match.Method,
			"path":          unprefixedPath,
			"headers":       item.Stub.Match.Headers,
			"query":         item.Stub.Match.Query,
			"body":          item.Stub.Match.Body,
			"body_contains": item.Stub.Match.BodyContains,
		},
		"response": map[string]any{
			"status":   item.Stub.Response.Status,
			"delay_ms": item.Stub.Response.DelayMS,
			"headers":  item.Stub.Response.Headers,
			"body":     item.Stub.Response.Body,
			"fault":    item.Stub.Response.Fault,
		},
	}
	if item.LastExchange != nil {
		resp["exchange"] = item.LastExchange
	}
	a.sessionsMu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

func (a *App) environmentSnapshot() *artifact.Environment {
	env := &artifact.Environment{}
	if a.proxyComponent != nil {
		info := mergeProxyInfo(a.proxyComponent)
		if info.ListenAddr != "" {
			env.ProxyListen = normalizeURL(info.ListenAddr)
		}
		env.ProxyMode = string(info.Mode)
	}
	if a.mockComponent != nil {
		if addr := a.mockComponent.Addr(); addr != "" {
			env.MockBaseURL = normalizeURL(addr)
		}
	}
	return env
}

func normalizeURL(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
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
		if addr := component.ListenAddr(); addr != "" {
			info.ListenAddr = addr
		}
	}
	return info
}
