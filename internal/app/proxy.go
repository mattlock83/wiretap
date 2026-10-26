package app

import (
	"log"
	"log/slog"
	"os"

	"github.com/mattlock83/wiretap/internal/core"
	"github.com/mattlock83/wiretap/internal/proxy"
	tlsmgr "github.com/mattlock83/wiretap/internal/tls"
)

func newProxyServer(cfg core.ProxyConfig, logger *slog.Logger, maxBodyBytes int64) *proxy.Server {
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

	return proxy.NewServer(proxy.Config{
		ListenAddr:   cfg.ListenAddr,
		Mode:         mode,
		TLSManager:   mgr,
		Logger:       stdLogger,
		MaxBodyBytes: maxBodyBytes,
	})

}
