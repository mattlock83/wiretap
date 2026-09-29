package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/phrond/evermock/internal/core"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(execute(ctx, os.Args[1:]))
}

func applyEnv(cfg *core.Config) error {
	if cfg == nil {
		return nil
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_HTTP_LISTEN")); v != "" {
		cfg.HTTP.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_LOG_FORMAT")); v != "" {
		cfg.Log.Format = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_LOG_LEVEL")); v != "" {
		switch strings.ToLower(v) {
		case "debug", "info", "warn", "warning", "error":
			cfg.Log.Level = parseLogLevel(v)
		default:
			return fmt.Errorf("invalid WIRETAP_LOG_LEVEL: %s", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_PROXY_MODE")); v != "" {
		cfg.Proxy.Mode = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_PROXY_LISTEN")); v != "" {
		cfg.Proxy.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_PROXY_CA_DIR")); v != "" {
		cfg.Proxy.CABaseDir = v
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_OUTPUT_DIR")); v != "" {
		cfg.Artifact.OutputDir = v
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_MAX_CAPTURE_BYTES")); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("WIRETAP_MAX_CAPTURE_BYTES: %w", err)
		}
		cfg.Capture.MaxBodyBytes = parsed
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_HTTP_READ_TIMEOUT")); v != "" {
		value, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("WIRETAP_HTTP_READ_TIMEOUT: %w", err)
		}
		cfg.HTTP.ReadTimeout = value
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_HTTP_WRITE_TIMEOUT")); v != "" {
		value, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("WIRETAP_HTTP_WRITE_TIMEOUT: %w", err)
		}
		cfg.HTTP.WriteTimeout = value
	}
	if v := strings.TrimSpace(os.Getenv("WIRETAP_HTTP_IDLE_TIMEOUT")); v != "" {
		value, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("WIRETAP_HTTP_IDLE_TIMEOUT: %w", err)
		}
		cfg.HTTP.IdleTimeout = value
	}
	return nil
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
