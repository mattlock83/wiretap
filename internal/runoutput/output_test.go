package runoutput

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/postman"
)

func failedRun(t *testing.T, changes ...func(*artifact.Artifact)) string {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "a failed test", Status: "fail", StartedAt: time.Now().UTC()}
	frozen, err := writer.Register(tc, nil)
	if err != nil {
		t.Fatal(err)
	}
	capture := &artifact.Artifact{Version: 2, TestCase: tc, Schemas: frozen, Traffic: []artifact.Flow{{ID: "f-1", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://sut.test/orders"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 503, Body: &artifact.Body{Text: "unavailable"}}}}}}
	for _, change := range changes {
		change(capture)
	}
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}

func TestFailedAttemptsProduceEverySelectedOutput(t *testing.T) {
	root := failedRun(t)
	before, err := os.ReadFile(filepath.Join(root, "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(t.Context(), root, Config{Coverage: []string{"json", "yaml"}, Postman: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Coverage) != 2 || len(result.Postman) != 1 || len(result.Errors) != 0 {
		t.Fatalf("wrong outputs: %+v", result)
	}
	var report coverage.Report
	data, _ := os.ReadFile(result.Coverage["json"])
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.ArtifactCount != 1 || len(report.TestCases) != 1 || report.TestCases[0].Attempts[0].Status != "fail" {
		t.Fatalf("lost failed attempt: %+v", report)
	}
	var collection postman.Collection
	data, _ = os.ReadFile(result.Postman[0])
	if err := json.Unmarshal(data, &collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 || collection.Items[0].Items[0].Responses[0].Code != 503 {
		t.Fatal("lost captured response")
	}
	after, _ := os.ReadFile(filepath.Join(root, "bundle.json"))
	if string(before) != string(after) {
		t.Fatal("exports mutated evidence")
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatal(err)
	}
	// Regeneration replaces generated files without disturbing raw capture.
	if _, err := Generate(t.Context(), root, Defaults()); err != nil {
		t.Fatal(err)
	}
}

func TestOutputFailureRetainsCoverageAndArchive(t *testing.T) {
	root := failedRun(t)
	if err := os.MkdirAll(filepath.Join(root, "exports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "exports", "postman"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Generate(t.Context(), root, Defaults())
	if err == nil || len(result.Errors) == 0 || result.Coverage["json"] == "" || len(result.Postman) != 0 {
		t.Fatalf("failure hidden or coverage lost: %+v %v", result, err)
	}
	if _, err := os.Stat(result.Coverage["json"]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatal(err)
	}
	receipt, _ := os.ReadFile(filepath.Join(root, "exports", "result.json"))
	var saved Result
	if err := json.Unmarshal(receipt, &saved); err != nil || len(saved.Errors) == 0 {
		t.Fatalf("missing failure receipt: %s, %v", receipt, err)
	}
}

func TestOutputDirectorySymlinkCannotWriteOutsideArchive(t *testing.T) {
	root := failedRun(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "exports")); err != nil {
		t.Skip(err)
	}
	if _, err := Generate(t.Context(), root, Defaults()); err == nil {
		t.Fatal("accepted exports symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside archive: %v %v", entries, err)
	}
}

func TestAutomaticPostmanOmissionPolicyRetainsExactDiagnostics(t *testing.T) {
	root := failedRun(t, func(c *artifact.Artifact) {
		incomplete := c.Traffic[0]
		incomplete.ID = "incomplete"
		request := *incomplete.Request.HTTP
		request.Body = &artifact.Body{Text: "prefix", Truncated: true, OriginalBytes: 99}
		incomplete.Request.HTTP = &request
		c.Traffic = append(c.Traffic, incomplete)
	})
	result, err := Generate(t.Context(), root, Config{Coverage: []string{"json"}, Postman: true, PostmanOptions: &postman.Options{OmitIncomplete: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Postman) != 1 || len(result.PostmanOmissions) != 1 || result.PostmanOmissions[0].FlowID != "incomplete" || len(result.Errors) != 0 {
		t.Fatalf("missing omissions: %+v", result)
	}
	receipt, err := os.ReadFile(filepath.Join(root, "exports", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Result
	if err := json.Unmarshal(receipt, &saved); err != nil || len(saved.PostmanOmissions) != 1 {
		t.Fatalf("missing receipt diagnostics: %s %v", receipt, err)
	}
}

func TestNestedCompanionWritesRejectEverySymlinkAncestor(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "exports", "postman")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(parent, "schemas")); err != nil {
		t.Skip(err)
	}
	path := filepath.Join(parent, "schemas", "service", "api.proto")
	if err := writeFile(t.Context(), root, path, []byte("secret")); err == nil {
		t.Fatal("followed nested symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside: %v %v", entries, err)
	}
	if err := writeFile(t.Context(), root, filepath.Join(outside, "escape"), nil); err == nil {
		t.Fatal("allowed path escape")
	}
}
