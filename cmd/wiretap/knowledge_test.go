package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/knowledge"
)

func TestKnowledgeCLIWorkflowAndExport(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "run")
	w, err := bundle.NewAt(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	c := knowledge.Config{Version: 1, BundleID: "urn:test:cli:bundle", Application: knowledge.Object{"id": "urn:test:cli", "title": "CLI application"}, Subjects: []knowledge.Object{{"id": "application", "key": "cli", "kind": "application", "title": "CLI application"}}, Selection: knowledge.Object{"mode": "replace-selected-tests", "scope": "complete", "test_ids": []any{}}}
	configPath := filepath.Join(root, "publication-config.json")
	b, err := knowledge.Canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(configPath, b, 0644)
	output := filepath.Join(root, "knowledge")
	args := []string{"--bundle", input, "--config", configPath, "--output", output}
	if code := execute(context.Background(), append([]string{"check"}, args...)); code != 1 {
		t.Fatalf("unpublished check = %d", code)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("check created output")
	}
	for _, command := range []string{"publish", "check"} {
		if code := execute(context.Background(), append([]string{command}, args...)); code != 0 {
			t.Fatalf("%s = %d", command, code)
		}
	}
	if code := execute(context.Background(), []string{"validate", "--bundle", output}); code != 0 {
		t.Fatalf("validate = %d", code)
	}
	export := filepath.Join(root, "records.json")
	if code := execute(context.Background(), []string{"export", "--bundle", output, "--format", "json", "--output", export}); code != 0 {
		t.Fatalf("export = %d", code)
	}
	b, _ = os.ReadFile(export)
	v, err := knowledge.Decode(b)
	if err != nil || v.(map[string]any)["manifest"] == nil {
		t.Fatal("export did not return verified publication records")
	}
	if code := execute(context.Background(), []string{"export", "--bundle", output, "--format", "postman", "--output", export}); code != 1 {
		t.Fatalf("Postman export of a publication = %d, want capture bundle error", code)
	}
	after, err := os.ReadFile(export)
	if err != nil || string(after) != string(b) {
		t.Fatal("failed Postman export changed existing records export")
	}
}
