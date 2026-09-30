package app

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/phrond/wiretap/internal/core"
	"github.com/phrond/wiretap/internal/proxy"
	tlsmgr "github.com/phrond/wiretap/internal/tls"
)

type ProxyComponent struct {
	server *proxy.Server
	info   proxy.Info
}

func NewProxyComponent(cfg core.ProxyConfig, logger *slog.Logger, maxBodyBytes int64) (*ProxyComponent, error) {
	mode := proxy.Mode(cfg.Mode)
	if mode == "" {
		mode = proxy.ModeMITM
	}

	var mgr *tlsmgr.Manager
	if mode == proxy.ModeMITM {
		mgr = tlsmgr.NewManager(cfg.CABaseDir)
	}

	var stdLogger *log.Logger
	if logger != nil {
		stdLogger = log.New(os.Stderr, "proxy ", log.LstdFlags)
	}

	server := proxy.NewServer(proxy.Config{
		ListenAddr:   cfg.ListenAddr,
		Mode:         mode,
		TLSManager:   mgr,
		Logger:       stdLogger,
		MaxBodyBytes: maxBodyBytes,
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
