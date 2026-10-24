package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/core"
)

func TestRunPublishesReadinessAndRemovesOnShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	instance := readinessTestApp(t, path)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = instance.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("runtime did not stop")
		}
	})

	var ready runtimeReadiness
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			if err := json.Unmarshal(data, &ready); err != nil {
				t.Fatalf("published partial or invalid readiness JSON: %v", err)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-done:
			t.Fatalf("runtime exited before readiness: %v", runErr)
		case <-deadline:
			t.Fatal("runtime did not publish readiness")
		case <-ticker.C:
		}
	}
	address, err := url.Parse(ready.ControlURL)
	if err != nil || address.Scheme != "http" || address.Hostname() != "127.0.0.1" || address.Port() == "" || address.Port() == "0" {
		t.Fatalf("invalid bound control address %q: %v", ready.ControlURL, err)
	}
	if ready.PID != os.Getpid() {
		t.Fatalf("readiness pid = %d, want %d", ready.PID, os.Getpid())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("readiness permissions = %o, want 600", info.Mode().Perm())
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(ready.ControlURL + "/healthz")
	if err != nil {
		t.Fatalf("published control address is unreachable: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("control health returned %d", response.StatusCode)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runtime did not stop")
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("run ended with %v, want cancellation", runErr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readiness file survived shutdown: %v", err)
	}
}

func TestRunFailsCleanlyWhenReadinessCannotBePublished(t *testing.T) {
	for _, scenario := range []string{"missing-parent", "destination-directory"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "missing", "ready.json")
			if scenario == "destination-directory" {
				path = filepath.Join(dir, "ready.json")
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			instance := readinessTestApp(t, path)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			if err := instance.Run(ctx); err == nil || !strings.Contains(err.Error(), "runtime readiness file") {
				t.Fatalf("startup did not report readiness failure: %v", err)
			}
			files, err := filepath.Glob(filepath.Join(dir, ".wiretap-ready-*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("startup left temporary readiness files %v: %v", files, err)
			}
			if instance.currentBundle() != nil {
				t.Fatal("failed readiness publication started a capture run")
			}
		})
	}
}

func readinessTestApp(t *testing.T, readyFile string) *App {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.HTTP.ReadyFile = readyFile
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.CABaseDir = t.TempDir()
	cfg.Artifact.OutputDir = t.TempDir()
	instance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}
