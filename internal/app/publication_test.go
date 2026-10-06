package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/phrond/wiretap/internal/core"
	"github.com/phrond/wiretap/internal/knowledge"
	"github.com/phrond/wiretap/internal/project"
)

func TestFinishedRunPublishesConfiguredKnowledge(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "publication.json")
	output := filepath.Join(root, "docs/knowledge")
	c := knowledge.Config{Version: 1, BundleID: "urn:test:auto:bundle", Application: knowledge.Object{"id": "urn:test:auto", "title": "Automatic publication"}, Subjects: []knowledge.Object{{"id": "application", "key": "automatic", "kind": "application", "title": "Automatic publication"}}, Selection: knowledge.Object{"mode": "replace-selected-tests", "scope": "complete", "test_ids": []any{}}}
	b, err := knowledge.Canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(configPath, b, 0644)
	cfg := core.DefaultConfig()
	cfg.HTTP.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.CABaseDir = filepath.Join(root, "ca")
	cfg.Artifact.OutputDir = filepath.Join(root, "archives")
	a, err := New(Deps{Config: cfg, Project: &project.Config{SUT: project.API{ID: "sut", Kind: "service"}, Publication: &project.Publication{Config: configPath, Output: output}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	archivePath, err := a.finishRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if archivePath == "" {
		t.Fatal("source archive path lost")
	}
	if v := knowledge.Validate(output); !v.Valid() || v.Application != "valid" {
		t.Fatalf("%+v", v)
	}
}
