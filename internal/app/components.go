package app

import (
	"context"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/phrond/evermock/core"
	"github.com/phrond/evermock/internal/mock"
	"github.com/phrond/evermock/internal/proxy"
	tlsmgr "github.com/phrond/evermock/internal/tls"
)

type ProxyComponent struct {
	server *proxy.Server
	info   proxy.Info
}

func NewProxyComponent(cfg core.ProxyConfig, logger *slog.Logger, recorder proxy.Recorder) (*ProxyComponent, error) {
	mode := proxy.Mode(cfg.Mode)
	if mode == "" {
		mode = proxy.ModeMITM
	}

	var mgr *tlsmgr.Manager
	if mode == proxy.ModeMITM {
		baseDir := cfg.CABaseDir
		if baseDir == "" {
			baseDir = defaultCABaseDir()
		}
		mgr = tlsmgr.NewManager(baseDir)
	}

	var stdLogger *log.Logger
	if logger != nil {
		stdLogger = log.New(os.Stdout, "proxy ", log.LstdFlags)
	}

	server := proxy.NewServer(proxy.Config{
		ListenAddr: cfg.ListenAddr,
		Mode:       mode,
		TLSManager: mgr,
		Recorder:   recorder,
		Logger:     stdLogger,
	})

	return &ProxyComponent{server: server}, nil
}

func (p *ProxyComponent) Start(ctx context.Context) error {
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

func (p *ProxyComponent) Stop(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.Stop(ctx)
}

func (p *ProxyComponent) Info() proxy.Info {
	if p == nil {
		return proxy.Info{}
	}
	return p.info
}

func (p *ProxyComponent) ListenAddr() string {
	if p == nil || p.server == nil {
		return ""
	}
	return p.server.ListenAddr()
}

func (p *ProxyComponent) SetMode(mode proxy.Mode) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.SetMode(mode)
}

func (p *ProxyComponent) CACertPEM() ([]byte, error) {
	if p == nil || p.server == nil {
		return nil, nil
	}
	return p.server.CACertPEM()
}

type MockComponent struct {
	server     *mock.Server
	listenAddr string
	addr       string
}

func NewMockComponent(cfg core.MockConfig) *MockComponent {
	return &MockComponent{
		server:     mock.NewServer(nil, nil),
		listenAddr: cfg.ListenAddr,
	}
}

func (m *MockComponent) Start(ctx context.Context) error {
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

func (m *MockComponent) Stop(ctx context.Context) error {
	if m == nil || m.server == nil {
		return nil
	}
	return m.server.Stop(ctx)
}

func (m *MockComponent) Addr() string {
	if m == nil {
		return ""
	}
	return m.addr
}

func (m *MockComponent) RegisterStub(stub mock.Stub) {
	if m == nil || m.server == nil {
		return
	}
	m.server.RegisterStub(stub)
}

func (m *MockComponent) ClearStubs() {
	if m == nil || m.server == nil {
		return
	}
	m.server.ClearStubs()
}

func (m *MockComponent) Recorder() *mock.Recorder {
	if m == nil || m.server == nil {
		return nil
	}
	return m.server.Recorder()
}

func defaultCABaseDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "phrond", "ca")
}
