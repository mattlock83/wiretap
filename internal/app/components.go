package app

import (
	"context"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/phrond/evermock/internal/core"
	"github.com/phrond/evermock/internal/capture"
	"github.com/phrond/evermock/internal/proxy"
	tlsmgr "github.com/phrond/evermock/internal/tls"
)

type ProxyComponent struct {
	server *proxy.Server
	info   proxy.Info
}

func NewProxyComponent(cfg core.ProxyConfig, logger *slog.Logger, recorder proxy.Recorder, maxBodyBytes ...int64) (*ProxyComponent, error) {
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
		ListenAddr:   cfg.ListenAddr,
		Mode:         mode,
		TLSManager:   mgr,
		Recorder:     recorder,
		Logger:       stdLogger,
		MaxBodyBytes: captureLimit(maxBodyBytes),
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

func (p *ProxyComponent) SetDependencyResolver(resolver proxy.DependencyResolver) {
	if p == nil || p.server == nil {
		return
	}
	p.server.SetDependencyResolver(resolver)
}

func captureLimit(values []int64) int64 {
	if len(values) == 0 {
		return capture.DefaultMaxBodyBytes
	}
	return values[0]
}

func defaultCABaseDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "phrond", "ca")
}
