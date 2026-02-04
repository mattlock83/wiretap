package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/mock"
	"github.com/phrond/evermock/internal/proxy"
)

type testCaseStartRequest struct {
	Name       string         `json:"name"`
	Tags       []string       `json:"tags"`
	Meta       map[string]any `json:"meta"`
	SUTBaseURL string         `json:"sut_base_url"`
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

	resp := map[string]any{
		"artifact": artifactDoc,
		"path":     path,
	}
	writeJSON(w, http.StatusOK, resp)
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
	writeJSON(w, http.StatusOK, artifactDoc)
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

func (a *App) handleAssertions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
		return
	}
	var assertion artifact.Assertion
	if err := json.NewDecoder(r.Body).Decode(&assertion); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	stored := a.store.AddAssertion(assertion)
	writeJSON(w, http.StatusOK, stored)
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

func (a *App) handleProxyStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.proxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "proxy not configured"})
		return
	}
	if err := a.proxyComponent.Start(r.Context()); err != nil {
		if errors.Is(err, proxy.ErrAlreadyStarted) {
			writeJSON(w, http.StatusOK, a.proxyComponent.Info())
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a.proxyComponent.Info())
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
		info := a.proxyComponent.Info()
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
