package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/runset"
)

func TestRunSetCLIReportsAndProtectsOriginals(t *testing.T) {
	archive := func() string {
		writer, err := bundle.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Configure(bundle.Identity{Application: &bundle.Application{ID: "app"}, APIID: "api", SUTTargetKind: "service", SUTSource: map[string]any{"revision": "one"}}); err != nil {
			t.Fatal(err)
		}
		if err := writer.Finalize("complete"); err != nil {
			t.Fatal(err)
		}
		return writer.Path()
	}
	first, second := archive(), archive()
	output := filepath.Join(t.TempDir(), "run-set.json")
	args := []string{"run-set", "--bundle", first, "--bundle", filepath.Join(second, "bundle.json"), "--output", output}
	if code, _ := executeCapturingStdout(t, args); code != 0 {
		t.Fatalf("run-set exit %d", code)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var manifest runset.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil || len(manifest.Members) != 2 || filepath.IsAbs(manifest.Members[0].Path) {
		t.Fatalf("invalid portable manifest: %s %v", raw, err)
	}
	code, report := executeCapturingStdout(t, []string{"report", "--run-set", output})
	var combined runset.Coverage
	if err := json.Unmarshal(report, &combined); err != nil || code != 0 || len(combined.Members) != 2 || combined.Scope != "same_build_run_set" {
		t.Fatalf("invalid aggregate: %d %s %v", code, report, err)
	}
	for _, bad := range [][]string{
		{"run-set", "--bundle", first, "--output", filepath.Join(first, "bundle.json")},
		{"report", "--run-set", output, "--output", output},
		{"report", "--run-set", output, "--output", filepath.Join(first, "bundle.json")},
		{"report", "--run-set", output, "--format", "postman"},
	} {
		if code, _ := executeCapturingStdout(t, bad); code != 2 {
			t.Fatalf("unsafe usage exit %d for %v", code, bad)
		}
	}
	if _, _, err := bundle.Load(first); err != nil {
		t.Fatalf("original overwritten: %v", err)
	}
	if code, _ := executeCapturingStdout(t, []string{"run-set", "--bundle", first, "--output", filepath.Join(t.TempDir(), "absent", "set.json")}); code != 1 {
		t.Fatalf("output directory failure exit %d, want 1", code)
	}
}
