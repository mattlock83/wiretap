package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/phrond/evermock/core"
)

type Deps struct {
	Config    core.Config
	Logger    *slog.Logger
	Proxy     core.Component
	Mock      core.Component
	GRPCProxy core.Component
	GRPCMock  core.Component
}

type App struct {
	cfg       core.Config
	logger    *slog.Logger
	lifecycle *core.Lifecycle
	server    *http.Server
	store     *TestCaseStore

	proxyComponent     *ProxyComponent
	mockComponent      *MockComponent
	grpcProxyComponent *GrpcProxyComponent
	grpcMockComponent  *GrpcMockComponent
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

	store := NewTestCaseStore(cfg.Artifact.OutputDir)

	proxy := deps.Proxy
	if proxy == nil {
		component, err := NewProxyComponent(cfg.Proxy, logger, store)
		if err != nil {
			return nil, err
		}
		proxy = component
	}
	mock := deps.Mock
	if mock == nil {
		mock = NewMockComponent(cfg.Mock)
	}

	grpcProxy := deps.GRPCProxy
	if grpcProxy == nil {
		component, err := NewGrpcProxyComponent(cfg.GRPCProxy, logger, store)
		if err != nil {
			return nil, err
		}
		grpcProxy = component
	}
	grpcMock := deps.GRPCMock
	if grpcMock == nil {
		grpcMock = NewGrpcMockComponent(cfg.GRPCMock, nil)
	}

	lifecycle := &core.Lifecycle{
		Proxy:  proxy,
		Mock:   mock,
		Logger: logger,
	}

	app := &App{
		cfg:       cfg,
		logger:    logger,
		lifecycle: lifecycle,
		store:     store,
	}

	if component, ok := proxy.(*ProxyComponent); ok {
		app.proxyComponent = component
	}
	if component, ok := mock.(*MockComponent); ok {
		app.mockComponent = component
	}
	if component, ok := grpcProxy.(*GrpcProxyComponent); ok {
		app.grpcProxyComponent = component
	}
	if component, ok := grpcMock.(*GrpcMockComponent); ok {
		app.grpcMockComponent = component
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
