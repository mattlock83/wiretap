package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/phrond/evermock/internal/core"
)

type Deps struct {
	Config core.Config
	Logger *slog.Logger
	Proxy  core.Component
}

type App struct {
	cfg       core.Config
	logger    *slog.Logger
	lifecycle *core.Lifecycle
	server    *http.Server
	store     *TestCaseStore

	proxyComponent *ProxyComponent

	controlMu     sync.Mutex
	traceOwners   map[string]string
	legacySession *runtimeSession
	reverseTarget string
	sessionsMu    sync.Mutex
	sessions      map[string]*runtimeSession
	// defaultSessionID names the single active zero-config session that
	// unlabeled traffic is attributed to ("" when none / explicit-only).
	defaultSessionID string
	sessionSeq       uint64
	inFlight         int
}

func New(deps Deps) (*App, error) {
	cfg := deps.Config
	if cfg == (core.Config{}) {
		cfg = core.DefaultConfig()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger := deps.Logger
	if logger == nil {
		logger = newLogger(cfg.Log)
	}

	store := NewTestCaseStore(cfg.Artifact.OutputDir, cfg.Capture.MaxBodyBytes)

	proxy := deps.Proxy
	if proxy == nil {
		component, err := NewProxyComponent(cfg.Proxy, logger, nil, cfg.Capture.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		proxy = component
	}
	lifecycle := &core.Lifecycle{
		Proxy:  proxy,
		Logger: logger,
	}

	app := &App{
		cfg:         cfg,
		logger:      logger,
		lifecycle:   lifecycle,
		store:       store,
		sessions:    map[string]*runtimeSession{},
		traceOwners: map[string]string{},
	}

	if component, ok := proxy.(*ProxyComponent); ok {
		app.proxyComponent = component
		component.SetDependencyResolver(app.resolveDependencyRoute)
		component.server.SetAdmissionResolver(app.admitRequest)
	}

	app.server = &http.Server{
		Addr:         cfg.HTTP.ListenAddr,
		Handler:      app.routes(),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	if a == nil || a.server == nil {
		return errors.New("app not initialized")
	}

	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("http server starting", "addr", a.server.Addr)
		errCh <- a.server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("http server shutting down")
		_ = a.server.Shutdown(context.Background())
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
