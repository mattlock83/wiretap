package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattlock83/wiretap/internal/coverage"
)

func TestReportPolicyEmitsCoverageOnFailureAndIsOptIn(t *testing.T) {
	archive := httpBundleFixture(t)
	policy := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policy, []byte(`{"policy_version":"1.0","require_complete":true,"apis":[{"api_id":"missing-api","origin":"provided","required_operations":["GET /items"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, data := executeCapturingStdout(t, []string{"report", "--bundle", archive, "--policy", policy})
	var report coverage.Report
	if err := json.Unmarshal(data, &report); err != nil || report.ReportVersion != "1.0" || code != 1 {
		t.Fatalf("lost report or policy verdict: %d %v %s", code, err, data)
	}
	code, _ = executeCapturingStdout(t, []string{"report", "--bundle", archive})
	if code != 0 {
		t.Fatalf("policy applied without opt-in: %d", code)
	}
	output := filepath.Join(t.TempDir(), "report.json")
	code, _ = executeCapturingStdout(t, []string{"report", "--bundle", archive, "--policy", policy, "--output", output})
	if code != 1 {
		t.Fatalf("saved report lost verdict: %d", code)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"report", "--bundle", archive, "--policy", policy, "--output", policy},
		{"report", "--bundle", archive, "--policy", policy, "--format", "all"},
	} {
		if code, _ := executeCapturingStdout(t, args); code != 2 {
			t.Fatalf("invalid policy usage returned %d", code)
		}
	}
}

func TestCoveragePolicyRejectsUnknownKeysAndTrailingDocuments(t *testing.T) {
	for _, input := range []string{`{"policy_version":"1.0","require_completed":true}`, `{"policy_version":"1.0","require_complete":true,"require_complete":false}`, `{"policy_version":"1.0"} {}`, `null`} {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		code, _ := executeCapturingStdout(t, []string{"report", "--bundle", httpBundleFixture(t), "--policy", path})
		if code != 2 {
			t.Fatalf("invalid policy accepted (%d): %s", code, input)
		}
	}
}
