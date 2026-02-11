package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
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
