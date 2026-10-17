package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/core"
	"github.com/phrond/wiretap/internal/project"
)

func TestConfiguredBoundariesSurviveRunWithNoTests(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Artifact.OutputDir = t.TempDir()
	cfg.Proxy.CABaseDir = t.TempDir()
	appInstance, err := New(Deps{Config: cfg, Project: &project.Config{
		Application:  &bundle.Application{Name: "Orders", ID: "orders-app", Description: "Order processing"},
		SUT:          project.API{ID: "orders", Kind: "service", URL: "http://localhost:8080"},
		Dependencies: map[string]project.API{"payments": {Kind: "service", URL: "http://localhost:8081"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := appInstance.ensureBundle()
	if err != nil {
		t.Fatal(err)
	}
	if err := appInstance.closeBundle("complete"); err != nil {
		t.Fatal(err)
	}
	run, _, err := bundle.Load(writer.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Attempts) != 0 || len(run.ProvidedAPIs) != 1 || len(run.Dependencies) != 1 {
		t.Fatalf("configured inventory lost: %+v", run)
	}
	if run.Application == nil || run.Application.Name != "Orders" || run.Application.ID != "orders-app" || run.Application.Description != "Order processing" {
		t.Fatalf("configured application identity lost: %+v", run.Application)
	}
	dep := run.Dependencies[0]
	if dep.ID != "payments" || !dep.Configured || dep.TargetKind != "service" || len(dep.ObservedIn) != 0 {
		t.Fatalf("unused dependency misclassified: %+v", dep)
	}
}

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
	page, err := os.ReadFile(filepath.Join(writer.Path(), "docs/run.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "interrupted") {
		t.Fatal("run page does not show interrupted state")
	}
}
