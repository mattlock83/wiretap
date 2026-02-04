package core

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type Config struct {
	HTTP  HTTPConfig
	Log   LogConfig
	Proxy ProxyConfig
	Mock  MockConfig
}

type HTTPConfig struct {
	ListenAddr   string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type LogConfig struct {
	Level  slog.Level
	Format string // "text" or "json"
}

type ProxyConfig struct {
	Mode string // "mitm" | "passthrough" (future)
}

type MockConfig struct {
	ListenAddr string
}

func DefaultConfig() Config {
	return Config{
		HTTP: HTTPConfig{
			ListenAddr:   "127.0.0.1:18000",
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		Log: LogConfig{
			Level:  slog.LevelInfo,
			Format: "text",
		},
		Proxy: ProxyConfig{
			Mode: "mitm",
		},
		Mock: MockConfig{
			ListenAddr: "127.0.0.1:19090",
		},
	}
}

func (c Config) Validate() error {
	var errs []error
	if c.HTTP.ListenAddr == "" {
		errs = append(errs, errors.New("http.listen_addr must not be empty"))
	}
	if c.Mock.ListenAddr == "" {
		errs = append(errs, errors.New("mock.listen_addr must not be empty"))
	}
	if c.HTTP.ReadTimeout < 0 {
		errs = append(errs, errors.New("http.read_timeout must be non-negative"))
	}
	if c.HTTP.WriteTimeout < 0 {
		errs = append(errs, errors.New("http.write_timeout must be non-negative"))
	}
	if c.HTTP.IdleTimeout < 0 {
		errs = append(errs, errors.New("http.idle_timeout must be non-negative"))
	}
	if c.Log.Format != "text" && c.Log.Format != "json" {
		errs = append(errs, fmt.Errorf("log.format must be 'text' or 'json', got %q", c.Log.Format))
	}
	if c.Proxy.Mode != "" && c.Proxy.Mode != "mitm" && c.Proxy.Mode != "passthrough" {
		errs = append(errs, fmt.Errorf("proxy.mode must be 'mitm' or 'passthrough', got %q", c.Proxy.Mode))
	}
	return errors.Join(errs...)
}
