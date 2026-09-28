package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/bundlereport"
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
	bundleMu  sync.Mutex
	bundle    *bundle.Writer

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

func (a *App) ensureBundle() (*bundle.Writer, error) {
	a.bundleMu.Lock()
	defer a.bundleMu.Unlock()
	if a.bundle != nil {
		return a.bundle, nil
	}
	w, err := bundle.New(a.cfg.Artifact.OutputDir)
	if err != nil {
		return nil, err
	}
	if _, err := bundlereport.Refresh(w.Path()); err != nil {
		return nil, err
	}
	a.bundle = w
	a.store.SetSink(w)
	return w, nil
}

func (a *App) currentBundle() *bundle.Writer {
	a.bundleMu.Lock()
	defer a.bundleMu.Unlock()
	return a.bundle
}

func (a *App) closeBundle(state string) error {
	a.bundleMu.Lock()
	defer a.bundleMu.Unlock()
	if a.bundle == nil {
		return nil
	}
	if err := a.bundle.Finalize(state); err != nil {
		return err
	}
	a.bundle = nil
	a.store.SetSink(nil)
	return nil
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
		closed := a.currentBundle()
		if err := a.closeBundle("interrupted"); err != nil {
			a.logger.Error("failed to finalize interrupted bundle", "error", err)
		} else if closed != nil {
			if _, err := bundlereport.Refresh(closed.Path()); err != nil {
				a.logger.Error("failed to refresh interrupted bundle view", "error", err)
			}
		}
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
