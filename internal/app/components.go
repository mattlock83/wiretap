package app

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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

func (p *ProxyComponent) SetListenAddr(addr string) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.SetListenAddr(addr)
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

func (p *ProxyComponent) SetReverseTarget(target string) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.SetReverseTarget(target)
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

func (m *MockComponent) SetListenAddr(addr string) error {
	if m == nil || m.server == nil {
		return nil
	}
	if m.server.Running() {
		return errors.New("mock server already started")
	}
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return errors.New("listen_addr is required")
	}
	m.listenAddr = trimmed
	return nil
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

func (m *MockComponent) RemoveStubsByID(ids []string) int {
	if m == nil || m.server == nil {
		return 0
	}
	return m.server.RemoveStubsByID(ids)
}

func (m *MockComponent) ListStubs() []mock.Stub {
	if m == nil || m.server == nil {
		return nil
	}
	return m.server.ListStubs()
}

func (m *MockComponent) SetFallbackUpstream(baseURL string) error {
	if m == nil || m.server == nil {
		return nil
	}
	return m.server.SetFallbackUpstream(baseURL)
}

func (m *MockComponent) Recorder() *mock.Recorder {
	if m == nil || m.server == nil {
		return nil
	}
	return m.server.Recorder()
}

func (m *MockComponent) SetRecordingEnabled(enabled bool) {
	if m == nil || m.server == nil {
		return
	}
	m.server.SetRecordingEnabled(enabled)
}

func (m *MockComponent) SetSessionRecordingEnabled(sessionID string, enabled bool) {
	if m == nil || m.server == nil {
		return
	}
	m.server.SetSessionRecordingEnabled(sessionID, enabled)
}

func (m *MockComponent) SetDefaultSession(sessionID string) {
	if m == nil || m.server == nil {
		return
	}
	m.server.SetDefaultSession(sessionID)
}

func (m *MockComponent) ValidateExternalUpstream(baseURL string) error {
	if m == nil || m.server == nil {
		return errors.New("mock component not configured")
	}
	return m.server.ValidateExternalUpstream(baseURL)
}

func (m *MockComponent) SetExternalUpstream(sessionID, assetID, baseURL string, preserveHost bool) error {
	if m == nil || m.server == nil {
		return errors.New("mock component not configured")
	}
	return m.server.SetExternalUpstream(sessionID, assetID, baseURL, preserveHost)
}

func (m *MockComponent) RemoveExternalUpstreams(sessionID string) {
	if m == nil || m.server == nil {
		return
	}
	m.server.RemoveExternalUpstreams(sessionID)
}

func defaultCABaseDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "phrond", "ca")
}
