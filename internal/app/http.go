package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/mattlock83/wiretap/internal/core"
	"github.com/mattlock83/wiretap/internal/version"
)

func newLogger(cfg core.LogConfig) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{Level: cfg.Level}
	if cfg.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, handlerOpts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, handlerOpts))
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.handleHealthz)
	mux.HandleFunc("/v1/capabilities", a.handleCapabilities)
	mux.HandleFunc("/v1/startRun", a.handleStartRun)
	mux.HandleFunc("/v1/setupTest", a.handleSetupTest)
	mux.HandleFunc("/v1/teardownTest", a.handleTeardownTest)
	mux.HandleFunc("/v1/report", a.handleRuntimeReport)
	mux.HandleFunc("/v1/finishRun", a.handleFinishRun)
	mux.HandleFunc("/v1/proxy/ca", a.handleProxyCA)
	return loggingMiddleware(a.logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Teardown owns its short critical section and drains outside the
		// control lock so parallel tests and health checks remain responsive.
		if r.URL.Path != "/v1/teardownTest" && r.URL.Path != "/healthz" && r.URL.Path != "/v1/capabilities" {
			a.controlMu.Lock()
			defer a.controlMu.Unlock()
		}
		mux.ServeHTTP(w, r)
	}))
}

// Capabilities describe this binary's control contract, independently of saved
// archive and measurement versions. Reading them never starts a capture run.
func (a *App) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api_version": "1.0", "producer": version.Current(),
		"capabilities": []string{"capture.http", "outputs.coverage", "outputs.arazzo", "test.identity", "run.finalize"},
	})
}

func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
