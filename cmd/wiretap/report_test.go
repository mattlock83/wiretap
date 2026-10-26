package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
)

func TestCLICoverageReportsUseRawArchive(t *testing.T) {
	bundlePath := httpBundleFixture(t)

	code, data := executeCapturingStdout(t, []string{"report", "--bundle", bundlePath})
	if code != 0 {
		t.Fatalf("coverage report exit = %d", code)
	}
	var report coverage.Report
	if err := json.Unmarshal(data, &report); err != nil || report.ArtifactCount != 2 || len(report.TestCases) != 2 {
		t.Fatalf("invalid coverage report: %+v, %v", report, err)
	}
	for _, name := range []string{"index.md", "docs", "publication.json"} {
		if _, err := os.Stat(filepath.Join(bundlePath, name)); !os.IsNotExist(err) {
			t.Fatalf("report created generated publication path %q: %v", name, err)
		}
	}
}

func httpBundleFixture(t *testing.T) string {
	t.Helper()
	writer, err := bundle.NewAt(filepath.Join(t.TempDir(), "capture"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC)
	flow := func(id, method, url string, status int, offset time.Duration) artifact.Flow {
		return artifact.Flow{
			ID: id, Protocol: "http", Channel: "test_to_sut", StartedAt: start.Add(offset),
			Request: &artifact.HttpMessage{Method: method, URL: url},
			Response: &artifact.HttpMessage{
				Status: status, Headers: artifact.Headers{"Content-Type": {"application/json"}},
				Body: &artifact.Body{Text: `{"ok":true}`},
			},
		}
	}
	dependency := flow("dependency", "GET", "http://dependency/private", 200, 2*time.Second)
	dependency.Channel = "sut_to_dependency"
	dependency.AssetID = "inventory"
	for _, capture := range []*artifact.Artifact{
		{Version: 1, TestCase: artifact.TestCase{ID: "read-items", Name: "reads items", StartedAt: start, Status: "pass"}, Traffic: []artifact.Flow{
			flow("a-later", "POST", "http://sut/items", 201, time.Second),
			dependency,
			flow("z-earlier", "GET", "http://sut/items?limit=1", 200, 0),
		}},
		{Version: 1, TestCase: artifact.TestCase{ID: "missing-item", Name: "missing item", StartedAt: start.Add(time.Minute), Status: "pass"}, Traffic: []artifact.Flow{
			flow("missing", "GET", "http://sut/items/missing", 404, time.Minute),
		}},
	} {
		if _, err := writer.Register(capture.TestCase, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}

func executeCapturingStdout(t *testing.T, args []string) (int, []byte) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	original := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = original }()
	code := execute(context.Background(), args)
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, data
}

func TestTextReportReadsVerifiedArchive(t *testing.T) {
	root := httpBundleFixture(t)
	code, output := executeCapturingStdout(t, []string{"report", "--bundle", root, "--format", "text"})
	if code != 0 || !strings.Contains(string(output), "2 (2 passed, 0 failed, 0 skipped)") {
		t.Fatalf("text summary failed: exit %d: %s", code, output)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "read-items.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, output = executeCapturingStdout(t, []string{"report", "--bundle", root, "--format", "text"})
	if code != 2 || len(output) != 0 {
		t.Fatalf("corrupt evidence produced a summary: exit %d: %s", code, output)
	}
}
