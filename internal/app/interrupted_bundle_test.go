package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/core"
)

func TestInterruptedRunRefreshesBundleView(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Artifact.OutputDir = t.TempDir()
	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := appInstance.ensureBundle()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- appInstance.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run ended with %v, want cancellation", err)
	}
	run, _, err := bundle.Load(writer.Path())
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "interrupted" {
		t.Fatalf("run state = %q", run.RunState)
	}
	page, err := os.ReadFile(filepath.Join(writer.Path(), "run.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "interrupted") {
		t.Fatal("run page does not show interrupted state")
	}
}
