package core

import (
	"log/slog"
	"testing"
)

func TestConfigValidateDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected defaults to validate, got %v", err)
	}
	if cfg.Capture.MaxBodyBytes != 4*1024*1024 {
		t.Fatalf("default max capture bytes = %d", cfg.Capture.MaxBodyBytes)
	}
}

func TestConfigValidateErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HTTP.ListenAddr = ""
	cfg.Proxy.ListenAddr = ""
	cfg.Artifact.OutputDir = ""
	cfg.HTTP.ReadTimeout = -1
	cfg.Capture.MaxBodyBytes = -1
	cfg.Log.Format = "xml"
	cfg.Proxy.Mode = "socks5"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestConfigValidateLogFormat(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Log.Format = "json"
	cfg.Log.Level = slog.LevelDebug
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected json log format to validate, got %v", err)
	}
}
