package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/grpcmock"
	"github.com/phrond/evermock/internal/grpcproxy"
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
	if a.grpcProxyComponent != nil {
		if err := a.grpcProxyComponent.Start(r.Context()); err != nil && !errors.Is(err, grpcproxy.ErrAlreadyStarted) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if a.grpcMockComponent != nil {
		if err := a.grpcMockComponent.Start(r.Context()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	if a.mockComponent != nil {
		if recorder := a.mockComponent.Recorder(); recorder != nil {
			recorder.Clear()
		}
	}
	if a.grpcMockComponent != nil {
		if recorder := a.grpcMockComponent.Recorder(); recorder != nil {
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
	var grpcExchanges []grpcmock.Exchange
	if a.grpcMockComponent != nil {
		if recorder := a.grpcMockComponent.Recorder(); recorder != nil {
			grpcExchanges = recorder.Exchanges()
		}
	}

	env := a.environmentSnapshot()
	artifactDoc, path, err := a.store.FinishCase(req.Status, env, exchanges, grpcExchanges)
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
	if a.grpcMockComponent != nil {
		_ = a.grpcMockComponent.Stop(r.Context())
	}
	if a.grpcProxyComponent != nil {
		_ = a.grpcProxyComponent.Stop(r.Context())
	}

	resp := map[string]any{
		"artifact": artifactDoc,
		"path":     path,
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

type grpcProxyStartRequest struct {
	Target             string `json:"target"`
	InsecureSkipVerify *bool  `json:"insecure_skip_verify,omitempty"`
}

func (a *App) handleGrpcProxyStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.grpcProxyComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "grpc proxy not configured"})
		return
	}
	var payload grpcProxyStartRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if payload.Target != "" {
		if err := a.grpcProxyComponent.SetTarget(payload.Target); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if payload.InsecureSkipVerify != nil {
		a.grpcProxyComponent.SetInsecureSkipVerify(*payload.InsecureSkipVerify)
	}
	if err := a.grpcProxyComponent.Start(r.Context()); err != nil {
		if errors.Is(err, grpcproxy.ErrAlreadyStarted) {
			writeJSON(w, http.StatusOK, mergeGrpcProxyInfo(a.grpcProxyComponent))
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, mergeGrpcProxyInfo(a.grpcProxyComponent))
}

func (a *App) handleGrpcMockStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.grpcMockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "grpc mock not configured"})
		return
	}
	if err := a.grpcMockComponent.Start(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"addr": a.grpcMockComponent.Addr()})
}

func (a *App) handleGrpcStubs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.grpcMockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "grpc mock server not configured"})
		return
	}

	var stub grpcmock.Stub
	if err := json.NewDecoder(r.Body).Decode(&stub); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(stub.ID) == "" {
		stub.ID = "grpc-stub"
	}
	a.grpcMockComponent.RegisterStub(stub)
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered", "id": stub.ID})
}

func (a *App) handleGrpcStubsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.grpcMockComponent == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "grpc mock server not configured"})
		return
	}
	a.grpcMockComponent.ClearStubs()
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
	if a.grpcProxyComponent != nil {
		info := mergeGrpcProxyInfo(a.grpcProxyComponent)
		if info.ListenAddr != "" {
			env.GRPCProxyListen = normalizeGRPCURL(info.ListenAddr)
		}
	}
	if a.grpcMockComponent != nil {
		if addr := a.grpcMockComponent.Addr(); addr != "" {
			env.GRPCMockBaseURL = normalizeGRPCURL(addr)
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

func normalizeGRPCURL(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	return "https://" + raw
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

func mergeGrpcProxyInfo(component *GrpcProxyComponent) grpcproxy.Info {
	if component == nil {
		return grpcproxy.Info{}
	}
	info := component.Info()
	if info.ListenAddr == "" {
		if addr := component.ListenAddr(); addr != "" {
			info.ListenAddr = addr
		}
	}
	return info
}
