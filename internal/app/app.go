package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/core"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"github.com/mattlock83/wiretap/internal/project"
	"golang.org/x/net/http2"
)

type Deps struct {
	Project *project.Config
	Config  core.Config
	Logger  *slog.Logger
	Proxy   core.Component
}

type App struct {
	project   *project.Config
	cfg       core.Config
	logger    *slog.Logger
	lifecycle *core.Lifecycle
	server    *http.Server
	bundleMu  sync.Mutex
	bundle    *bundle.Writer

	proxyComponent  *ProxyComponent
	grpcMu          sync.RWMutex
	grpcContracts   map[string]*grpccontract.Contract
	grpcSchemas     []artifact.SchemaRef
	grpcListeners   map[string]*grpcCaptureListener
	grpcTransportMu sync.Mutex
	grpcTransports  map[string]*http2.Transport
	grpcCallsMu     sync.Mutex
	grpcCalls       map[*activeGRPCCall]struct{}

	controlMu     sync.Mutex
	traceOwners   map[string]string
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
	// Preserve configured boundaries even if the run ends before any test starts.
	if a.project != nil {
		defaults := a.testDefaults()
		kinds := make(map[string]string, len(defaults.Dependencies))
		for _, dep := range defaults.Dependencies {
			kind := dep.UpstreamKind
			if kind == "" {
				kind = "unknown"
			}
			kinds[dep.AssetID] = kind
		}
		if err := w.Configure(bundle.Identity{ProviderMappings: a.project.ProviderMappings, TestSource: defaults.TestSource, SUTSource: defaults.SUTSource, Application: defaults.Application, APIID: defaults.APIID, SUTTargetKind: defaults.SUTTargetKind, DependencyKinds: kinds}); err != nil {
			return nil, err
		}
		// Freeze the configured HTTP inventory before any test starts. Empty or
		// interrupted runs still need a denominator for their coverage reports.
		var schemas []artifact.SchemaRef
		if defaults.SUTProtocol != "grpc" {
			schemas = append(schemas, defaults.Schemas...)
			for i := range schemas {
				schemas[i].Role = "sut"
				schemas[i].AssetID = defaults.APIID
			}
			if len(schemas) == 0 && !requiresSchema(defaults.SchemaRequired) {
				schemas = append(schemas, omittedSchema(defaults.APIID, "sut"))
			}
		}
		var httpDependencies []dependencyRegistration
		for _, dependency := range defaults.Dependencies {
			if dependency.Protocol != "grpc" {
				httpDependencies = append(httpDependencies, dependency)
			}
		}
		_, dependencySchemas, err := normalizeDependencies(httpDependencies)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, dependencySchemas...)
		for i := range schemas {
			if schemas[i].AssetName == "" {
				schemas[i].AssetName = schemas[i].ID
			}
		}
		if err := w.RegisterSchemas(schemas); err != nil {
			return nil, err
		}
	}
	a.bundle = w
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

	proxy := deps.Proxy
	if proxy == nil {
		component, err := NewProxyComponent(cfg.Proxy, logger, cfg.Capture.MaxBodyBytes)
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
		project:     deps.Project,
		logger:      logger,
		lifecycle:   lifecycle,
		sessions:    map[string]*runtimeSession{},
		traceOwners: map[string]string{},
	}

	if component, ok := proxy.(*ProxyComponent); ok {
		app.proxyComponent = component
		if deps.Project != nil && deps.Project.SUT.Protocol == "grpc" {
			if err := component.server.SetCaptureTLS(deps.Project.SUT.CaptureTLS.CertFile, deps.Project.SUT.CaptureTLS.KeyFile); err != nil {
				return nil, err
			}
		}
		component.SetDependencyResolver(app.resolveDependencyRoute)
		component.server.SetAdmissionResolver(app.admitRequest)
		component.server.SetGRPCHandler(func(w http.ResponseWriter, r *http.Request) { app.forwardGRPC(w, r, "") })
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

func (a *App) Run(ctx context.Context) (runErr error) {
	if a == nil || a.server == nil {
		return errors.New("app not initialized")
	}
	listener, err := net.Listen("tcp", a.cfg.HTTP.ListenAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	removeReadiness, err := publishReadiness(a.cfg.HTTP.ReadyFile, listener.Addr())
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, removeReadiness()) }()
	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("http server starting", "addr", listener.Addr().String())
		errCh <- a.server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("http server shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.server.Shutdown(shutdownCtx)
		if err := a.interruptRun(); err != nil {
			return errors.Join(ctx.Err(), err)
		}
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
