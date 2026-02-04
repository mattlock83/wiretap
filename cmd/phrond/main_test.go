package main

import (
	"os"
	"testing"

	"github.com/phrond/evermock/core"
)

func TestApplyEnvProxySettings(t *testing.T) {
	cfg := core.DefaultConfig()

	t.Setenv("PHROND_PROXY_LISTEN", "127.0.0.1:18081")
	t.Setenv("PHROND_PROXY_CA_DIR", "/tmp/phrond-ca")
	t.Setenv("PHROND_PROXY_MODE", "passthrough")

	applyEnv(&cfg)

	if cfg.Proxy.ListenAddr != "127.0.0.1:18081" {
		t.Fatalf("expected proxy listen addr, got %q", cfg.Proxy.ListenAddr)
	}
	if cfg.Proxy.CABaseDir != "/tmp/phrond-ca" {
		t.Fatalf("expected proxy CA dir, got %q", cfg.Proxy.CABaseDir)
	}
	if cfg.Proxy.Mode != "passthrough" {
		t.Fatalf("expected proxy mode passthrough, got %q", cfg.Proxy.Mode)
	}
}

func TestApplyEnvKeepsDefaults(t *testing.T) {
	cfg := core.DefaultConfig()
	before := cfg

	os.Unsetenv("PHROND_PROXY_LISTEN")
	os.Unsetenv("PHROND_PROXY_CA_DIR")
	os.Unsetenv("PHROND_PROXY_MODE")

	applyEnv(&cfg)

	if cfg.Proxy.ListenAddr != before.Proxy.ListenAddr {
		t.Fatalf("expected listen addr to remain default")
	}
	if cfg.Proxy.CABaseDir != before.Proxy.CABaseDir {
		t.Fatalf("expected CA dir to remain default")
	}
	if cfg.Proxy.Mode != before.Proxy.Mode {
		t.Fatalf("expected proxy mode to remain default")
	}
}
