package app

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/phrond/evermock/core"
	"github.com/phrond/evermock/internal/grpcmock"
	"github.com/phrond/evermock/internal/grpcproxy"
	tlsmgr "github.com/phrond/evermock/internal/tls"
)

type GrpcProxyComponent struct {
	server *grpcproxy.Server
	info   grpcproxy.Info
}

func NewGrpcProxyComponent(cfg core.GRPCProxyConfig, logger *slog.Logger, recorder grpcproxy.Recorder) (*GrpcProxyComponent, error) {
	baseDir := cfg.CABaseDir
	if baseDir == "" {
		baseDir = defaultCABaseDir()
	}
	mgr := tlsmgr.NewManager(baseDir)

	var stdLogger *log.Logger
	if logger != nil {
		stdLogger = log.New(os.Stdout, "grpc-proxy ", log.LstdFlags)
	}

	server := grpcproxy.NewServer(grpcproxy.Config{
		ListenAddr:         cfg.ListenAddr,
		TargetAddr:         cfg.TargetAddr,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		TLSManager:         mgr,
		Recorder:           recorder,
		Logger:             stdLogger,
	})

	return &GrpcProxyComponent{server: server}, nil
}

func (p *GrpcProxyComponent) Start(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	info, err := p.server.Start(ctx)
	if err != nil {
		return err
	}
	p.info = info
	return nil
}

func (p *GrpcProxyComponent) Stop(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.Stop(ctx)
}

func (p *GrpcProxyComponent) Info() grpcproxy.Info {
	if p == nil {
		return grpcproxy.Info{}
	}
	return p.info
}

func (p *GrpcProxyComponent) ListenAddr() string {
	if p == nil || p.server == nil {
		return ""
	}
	return p.server.ListenAddr()
}

func (p *GrpcProxyComponent) SetListenAddr(addr string) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.SetListenAddr(addr)
}

func (p *GrpcProxyComponent) SetTarget(target string) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.SetTarget(target)
}

func (p *GrpcProxyComponent) SetInsecureSkipVerify(skip bool) {
	if p == nil || p.server == nil {
		return
	}
	p.server.SetInsecureSkipVerify(skip)
}

type GrpcMockComponent struct {
	server     *grpcmock.Server
	listenAddr string
	addr       string
}

func NewGrpcMockComponent(cfg core.GRPCMockConfig, tlsManager *tlsmgr.Manager) *GrpcMockComponent {
	if tlsManager == nil {
		baseDir := cfg.CABaseDir
		if baseDir == "" {
			baseDir = defaultCABaseDir()
		}
		tlsManager = tlsmgr.NewManager(baseDir)
	}
	return &GrpcMockComponent{
		server:     grpcmock.NewServer(nil, nil, tlsManager),
		listenAddr: cfg.ListenAddr,
	}
}

func (m *GrpcMockComponent) Start(ctx context.Context) error {
	if m == nil || m.server == nil {
		return nil
	}
	addr, err := m.server.Start(ctx, m.listenAddr)
	if err != nil {
		return err
	}
	m.addr = addr
	return nil
}

func (m *GrpcMockComponent) Stop(ctx context.Context) error {
	if m == nil || m.server == nil {
		return nil
	}
	return m.server.Stop(ctx)
}

func (m *GrpcMockComponent) Addr() string {
	if m == nil {
		return ""
	}
	return m.addr
}

func (m *GrpcMockComponent) SetListenAddr(addr string) error {
	if m == nil || m.server == nil {
		return nil
	}
	if m.server.Running() {
		return errors.New("grpc mock server already started")
	}
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return errors.New("listen_addr is required")
	}
	m.listenAddr = trimmed
	return nil
}

func (m *GrpcMockComponent) RegisterStub(stub grpcmock.Stub) {
	if m == nil || m.server == nil {
		return
	}
	m.server.RegisterStub(stub)
}

func (m *GrpcMockComponent) ClearStubs() {
	if m == nil || m.server == nil {
		return
	}
	m.server.ClearStubs()
}

func (m *GrpcMockComponent) Recorder() *grpcmock.Recorder {
	if m == nil || m.server == nil {
		return nil
	}
	return m.server.Recorder()
}
