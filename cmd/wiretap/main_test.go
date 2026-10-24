package main

import (
	"os"
	"testing"

	"github.com/mattlock83/wiretap/internal/core"
)

func TestApplyEnvProxySettings(t *testing.T) {
	cfg := core.DefaultConfig()

	t.Setenv("WIRETAP_PROXY_LISTEN", "127.0.0.1:18081")
	t.Setenv("WIRETAP_PROXY_CA_DIR", "/tmp/wiretap-ca")
	t.Setenv("WIRETAP_PROXY_MODE", "passthrough")
	t.Setenv("WIRETAP_MAX_CAPTURE_BYTES", "1024")
	t.Setenv("WIRETAP_OUTPUT_DIR", "/tmp/wiretap-bundles")

	applyEnv(&cfg)

	if cfg.Artifact.OutputDir != "/tmp/wiretap-bundles" {
		t.Fatalf("output root = %q", cfg.Artifact.OutputDir)
	}
	if cfg.Proxy.ListenAddr != "127.0.0.1:18081" {
		t.Fatalf("expected proxy listen addr, got %q", cfg.Proxy.ListenAddr)
	}
	if cfg.Proxy.CABaseDir != "/tmp/wiretap-ca" {
		t.Fatalf("expected proxy CA dir, got %q", cfg.Proxy.CABaseDir)
	}
	if cfg.Proxy.Mode != "passthrough" {
		t.Fatalf("expected proxy mode passthrough, got %q", cfg.Proxy.Mode)
	}
	if cfg.Capture.MaxBodyBytes != 1024 {
		t.Fatalf("expected max capture bytes 1024, got %d", cfg.Capture.MaxBodyBytes)
	}
}

func TestApplyEnvKeepsDefaults(t *testing.T) {
	cfg := core.DefaultConfig()
	before := cfg

	os.Unsetenv("WIRETAP_PROXY_LISTEN")
	os.Unsetenv("WIRETAP_PROXY_CA_DIR")
	os.Unsetenv("WIRETAP_PROXY_MODE")

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
