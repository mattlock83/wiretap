package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
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

func TestInterruptedRunPersistsArchiveState(t *testing.T) {
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
}

func TestRunFinalizationVerifiesRawArchive(t *testing.T) {
	for _, shutdown := range []string{"finish", "interrupt"} {
		for _, corruption := range []string{"none", "payload", "contract"} {
			t.Run(shutdown+"/"+corruption, func(t *testing.T) {
				cfg := core.DefaultConfig()
				cfg.Artifact.OutputDir = t.TempDir()
				instance, err := New(Deps{Config: cfg})
				if err != nil {
					t.Fatal(err)
				}
				writer, err := instance.ensureBundle()
				if err != nil {
					t.Fatal(err)
				}
				testcase := artifact.TestCase{ID: "integrity", Name: "retains evidence", StartedAt: time.Now().UTC(), Status: "pass"}
				schemas, err := writer.Register(testcase, []artifact.SchemaRef{{
					ID: "api", Role: "sut", Type: "openapi",
					Source: artifact.SchemaSource{Type: "inline", Content: "openapi: 3.0.3\ninfo: {title: Integrity, version: '1'}\npaths: {}\n"},
				}})
				if err != nil {
					t.Fatal(err)
				}
				capture := &artifact.Artifact{Version: 2, TestCase: testcase, Schemas: schemas, Traffic: []artifact.Flow{{
					ID: "flow-1", Protocol: "http", Channel: "test_to_sut",
					Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://sut.test/"}},
					Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Body: &artifact.Body{Text: "retained payload"}}},
				}}}
				if _, err := writer.Commit(capture); err != nil {
					t.Fatal(err)
				}
				if _, _, err := bundle.Load(writer.Path()); err != nil {
					t.Fatalf("initial archive is invalid: %v", err)
				}
				if corruption != "none" {
					path := schemas[0].Source.Path
					if corruption == "payload" {
						payloads, err := filepath.Glob(filepath.Join(writer.Path(), "payloads", "*.bin"))
						if err != nil || len(payloads) != 1 {
							t.Fatalf("expected one retained payload, got %v: %v", payloads, err)
						}
						path = payloads[0]
					}
					if err := os.WriteFile(path, []byte("changed after capture"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if shutdown == "finish" {
					var path string
					path, err = instance.finishRun(t.Context())
					if path != writer.Path() {
						t.Fatalf("finalization lost archive path: %q", path)
					}
				} else {
					err = instance.interruptRun()
				}
				if corruption != "none" {
					if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
						t.Fatalf("finalization accepted corrupt %s: %v", corruption, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("intact archive failed finalization: %v", err)
				}
				run, captures, err := bundle.Load(writer.Path())
				if err != nil {
					t.Fatal(err)
				}
				wantState := "complete"
				if shutdown == "interrupt" {
					wantState = "interrupted"
				}
				if run.RunState != wantState || len(captures) != 1 || captures[0].Traffic[0].Response.HTTP.Body.Text != "retained payload" {
					t.Fatalf("finalized archive lost state or capture: %+v", run)
				}
			})
		}
	}
}
