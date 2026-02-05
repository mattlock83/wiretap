package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/phrond/evermock/core"
	"github.com/phrond/evermock/internal/app"
)

func main() {
	cfg := core.DefaultConfig()
	applyEnv(&cfg)
	cli := parseCLI(&cfg)

	logger := appLogger(cfg.Log)
	if handleCLI(cli, cfg) {
		return
	}
	appInstance, err := app.New(app.Deps{Config: cfg, Logger: logger})
	if err != nil {
		logger.Error("failed to initialize", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := appInstance.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func appLogger(cfg core.LogConfig) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Level}
	if cfg.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func applyEnv(cfg *core.Config) {
	if cfg == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_HTTP_LISTEN")); v != "" {
		cfg.HTTP.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_MOCK_LISTEN")); v != "" {
		cfg.Mock.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_LOG_FORMAT")); v != "" {
		cfg.Log.Format = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_LOG_LEVEL")); v != "" {
		cfg.Log.Level = parseLogLevel(v)
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_PROXY_MODE")); v != "" {
		cfg.Proxy.Mode = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_PROXY_LISTEN")); v != "" {
		cfg.Proxy.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_PROXY_CA_DIR")); v != "" {
		cfg.Proxy.CABaseDir = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_GRPC_PROXY_LISTEN")); v != "" {
		cfg.GRPCProxy.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_GRPC_PROXY_TARGET")); v != "" {
		cfg.GRPCProxy.TargetAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_GRPC_PROXY_INSECURE")); v != "" {
		cfg.GRPCProxy.InsecureSkipVerify = parseBool(v)
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_GRPC_MOCK_LISTEN")); v != "" {
		cfg.GRPCMock.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_GRPC_CA_DIR")); v != "" {
		cfg.GRPCProxy.CABaseDir = v
		cfg.GRPCMock.CABaseDir = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_ARTIFACT_DIR")); v != "" {
		cfg.Artifact.OutputDir = v
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_HTTP_READ_TIMEOUT")); v != "" {
		cfg.HTTP.ReadTimeout = parseDuration(v, cfg.HTTP.ReadTimeout)
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_HTTP_WRITE_TIMEOUT")); v != "" {
		cfg.HTTP.WriteTimeout = parseDuration(v, cfg.HTTP.WriteTimeout)
	}
	if v := strings.TrimSpace(os.Getenv("PHROND_HTTP_IDLE_TIMEOUT")); v != "" {
		cfg.HTTP.IdleTimeout = parseDuration(v, cfg.HTTP.IdleTimeout)
	}
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

func parseDuration(raw string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
