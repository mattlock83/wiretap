package runoutput

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/postman"
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
	result, err := Generate(t.Context(), root, Config{Coverage: []string{"json", "yaml", "text"}, Postman: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Coverage) != 3 || len(result.Postman) != 1 || len(result.Errors) != 0 {
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
	textReport, err := os.ReadFile(result.Coverage["text"])
	if err != nil || filepath.Ext(result.Coverage["text"]) != ".txt" || !strings.Contains(string(textReport), "1 failed") {
		t.Fatalf("missing readable failed-run summary: %s, %v", textReport, err)
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

func TestAutomaticOutputsProtectSourceBackedByExports(t *testing.T) {
	for _, target := range []string{"coverage", "postman", "receipt"} {
		for _, alias := range []string{"source symlink", "output hardlink"} {
			t.Run(target+"/"+alias, func(t *testing.T) {
				root := failedRun(t)
				outputs, err := Generate(t.Context(), root, Defaults())
				if err != nil {
					t.Fatal(err)
				}
				output := outputs.Coverage["json"]
				switch target {
				case "postman":
					output = outputs.Postman[0]
				case "receipt":
					output = filepath.Join(root, "exports", "result.json")
				}
				source := filepath.Join(root, "tests", "attempt-1.json")
				before, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(output); err != nil {
					t.Fatal(err)
				}
				if alias == "source symlink" {
					if err := os.Rename(source, output); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(output, source); err != nil {
						t.Skip(err)
					}
				} else if err := os.Link(source, output); err != nil {
					t.Skip(err)
				}
				if _, err := Generate(t.Context(), root, Defaults()); err == nil {
					t.Fatal("accepted export that aliases capture evidence")
				}
				if after, err := os.ReadFile(source); err != nil || string(after) != string(before) {
					t.Fatalf("export changed source capture: %v", err)
				}
				if _, _, err := bundle.Load(root); err != nil {
					t.Fatalf("export changed source archive: %v", err)
				}
			})
		}
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

func jsonRESTRun(t *testing.T) string {
	t.Helper()
	return failedRun(t, func(c *artifact.Artifact) {
		c.Traffic[0].Response.HTTP.Headers = map[string][]string{"Content-Type": {"application/json"}}
		c.Traffic[0].Response.HTTP.Body = &artifact.Body{Text: `{"error":"unavailable"}`}
	})
}

func TestAutomaticArazzoIncludesDocumentAndSchemaCompanions(t *testing.T) {
	root := jsonRESTRun(t)
	result, err := Generate(t.Context(), root, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if result.Arazzo != filepath.Join(root, "exports", "arazzo", "arazzo.json") || len(result.ArazzoFiles) == 0 {
		t.Fatalf("missing Arazzo paths: %+v", result)
	}
	data, err := os.ReadFile(result.Arazzo)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Arazzo    string            `json:"arazzo"`
		Workflows []json.RawMessage `json:"workflows"`
	}
	if err := json.Unmarshal(data, &document); err != nil || document.Arazzo == "" || len(document.Workflows) != 1 {
		t.Fatalf("invalid Arazzo document: %s, %v", data, err)
	}
	for _, path := range result.ArazzoFiles {
		if !strings.HasPrefix(path, filepath.Join(root, "exports", "arazzo")+string(filepath.Separator)) {
			t.Fatalf("companion outside Arazzo directory: %s", path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	data, err = os.ReadFile(filepath.Join(root, "exports", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Result
	if err := json.Unmarshal(data, &saved); err != nil || saved.Arazzo != result.Arazzo || len(saved.ArazzoFiles) != len(result.ArazzoFiles) {
		t.Fatalf("missing Arazzo receipt: %s, %v", data, err)
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatalf("export changed capture evidence: %v", err)
	}
}

func TestAutomaticArazzoSkipsNonJSONTraffic(t *testing.T) {
	root := failedRun(t)
	result, err := Generate(t.Context(), root, Defaults())
	if err != nil || result.Arazzo != "" || len(result.ArazzoFiles) != 0 || len(result.Errors) != 0 {
		t.Fatalf("non-JSON traffic should not export Arazzo: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "exports", "arazzo")); !os.IsNotExist(err) {
		t.Fatalf("non-JSON traffic created an Arazzo directory: %v", err)
	}
}

func TestAutomaticArazzoFailureRetainsOtherOutputs(t *testing.T) {
	root := jsonRESTRun(t)
	if err := os.MkdirAll(filepath.Join(root, "exports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "exports", "arazzo"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Generate(t.Context(), root, Defaults())
	if err == nil || result.Arazzo != "" || len(result.ArazzoFiles) != 0 || len(result.Errors) != 1 || result.Errors[0].Format != "arazzo" || result.Coverage["json"] == "" || len(result.Postman) != 1 {
		t.Fatalf("Arazzo failure lost other exports: %+v, %v", result, err)
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatal(err)
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
