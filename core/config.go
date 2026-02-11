package core

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type Config struct {
	HTTP     HTTPConfig
	Log      LogConfig
	Proxy    ProxyConfig
	Mock     MockConfig
	Artifact ArtifactConfig
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
	ListenAddr string
	Mode       string // "mitm" | "passthrough"
	CABaseDir  string
}

type MockConfig struct {
	ListenAddr string
}

type ArtifactConfig struct {
	OutputDir string
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
			ListenAddr: "127.0.0.1:18080",
			Mode:       "mitm",
		},
		Mock: MockConfig{
			ListenAddr: "127.0.0.1:19090",
		},
		Artifact: ArtifactConfig{
			OutputDir: "artifacts",
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
	if c.Proxy.ListenAddr == "" {
		errs = append(errs, errors.New("proxy.listen_addr must not be empty"))
	}
	if c.Artifact.OutputDir == "" {
		errs = append(errs, errors.New("artifact.output_dir must not be empty"))
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
