package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/phrond/evermock/core"
)

func newLogger(cfg core.LogConfig) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{Level: cfg.Level}
	if cfg.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, handlerOpts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, handlerOpts))
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.handleHealthz)
	mux.HandleFunc("/v1/lifecycle/start", a.handleLifecycleStart)
	mux.HandleFunc("/v1/lifecycle/stop", a.handleLifecycleStop)
	mux.HandleFunc("/v1/start", a.handleRuntimeStart)
	mux.HandleFunc("/v1/setup", a.handleRuntimeSetup)
	mux.HandleFunc("/v1/queue-response", a.handleQueueResponse)
	mux.HandleFunc("/v1/teardown", a.handleRuntimeTeardown)
	mux.HandleFunc("/v1/report", a.handleRuntimeReport)
	mux.HandleFunc("/v1/kill", a.handleRuntimeKill)
	mux.HandleFunc("/v1/listQueued", a.handleListQueued)
	mux.HandleFunc("/v1/listResponded", a.handleListResponded)
	mux.HandleFunc("/v1/inspectResponse", a.handleInspectResponse)
	mux.HandleFunc("/v1/testcases/start", a.handleTestCaseStart)
	mux.HandleFunc("/v1/testcases/finish", a.handleTestCaseFinish)
	mux.HandleFunc("/v1/testcases/status", a.handleTestCaseStatus)
	mux.HandleFunc("/v1/artifact", a.handleArtifact)
	mux.HandleFunc("/v1/stubs", a.handleStubs)
	mux.HandleFunc("/v1/stubs/clear", a.handleStubsClear)
	mux.HandleFunc("/v1/mock/exchanges", a.handleMockExchanges)
	mux.HandleFunc("/v1/expectations", a.handleExpectations)
	mux.HandleFunc("/v1/schemas", a.handleSchemas)
	mux.HandleFunc("/v1/redactions", a.handleRedactions)
	mux.HandleFunc("/v1/proxy/start", a.handleProxyStart)
	mux.HandleFunc("/v1/mock/start", a.handleMockStart)
	mux.HandleFunc("/v1/proxy/mode", a.handleProxyMode)
	mux.HandleFunc("/v1/proxy/ca", a.handleProxyCA)
	return loggingMiddleware(a.logger, mux)
}

func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleLifecycleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := a.lifecycle.Start(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (a *App) handleLifecycleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := a.lifecycle.Stop(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
