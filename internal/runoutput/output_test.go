package runoutput

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
)

func failedRun(t *testing.T, changes ...func(*artifact.Artifact)) string {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "a failed test", Status: "fail", StartedAt: time.Now().UTC()}
	_, err = writer.RegisterPrepared(tc, nil)
	if err != nil {
		t.Fatal(err)
	}
	capture := &artifact.Artifact{Version: 2, TestCase: tc, Traffic: []artifact.Flow{{ID: "f-1", Protocol: "http", Channel: "test_to_sut", Request: &artifact.HttpMessage{Method: "GET", URL: "http://sut.test/orders"}, Response: &artifact.HttpMessage{Status: 503, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"error":"unavailable"}`}}}}}
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

func TestFailedAttemptsProduceFixedOutputs(t *testing.T) {
	root := failedRun(t)
	before, err := os.ReadFile(filepath.Join(root, "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Coverage) != 2 || result.Arazzo == "" || len(result.Errors) != 0 {
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
	html, err := os.ReadFile(result.Coverage["html"])
	if err != nil || !strings.Contains(string(html), `data-wiretap-report="coverage"`) || !strings.Contains(string(html), "a failed test") {
		t.Fatalf("missing HTML report: %v", err)
	}
	var document arazzo.Document
	data, _ = os.ReadFile(result.Arazzo)
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Workflows) != 1 || document.Workflows[0].Steps[0].SuccessCriteria[0].Condition != "$statusCode == 503" {
		t.Fatal("lost captured status")
	}
	after, _ := os.ReadFile(filepath.Join(root, "bundle.json"))
	if string(before) != string(after) {
		t.Fatal("exports mutated evidence")
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatal(err)
	}
	// Regeneration replaces generated files without disturbing raw capture.
	if _, err := Generate(t.Context(), root); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotOutputsReuseVerifiedCaptures(t *testing.T) {
	root := failedRun(t, func(capture *artifact.Artifact) {
		capture.TestCase.Meta = map[string]any{"description": strings.Repeat("retained test metadata ", 1000)}
	})
	snapshot, err := Load(t.Context(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The verified capture is larger than this new limit. Output protection may
	// check the pinned manifest, but must not decode the capture a second time.
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILE_BYTES", strconv.Itoa(len(manifest)))
	if _, _, err := bundle.Load(root); !errors.Is(err, bundle.ErrLoadLimit) {
		t.Fatalf("fixture does not distinguish a repeated archive load: %v", err)
	}
	result, err := snapshot.Generate(t.Context())
	if err != nil || result == nil || len(result.Coverage) != 2 || result.Arazzo == "" {
		t.Fatalf("verified snapshot did not produce fixed outputs: %+v, %v", result, err)
	}
}

func TestSnapshotOutputsRejectChangedManifest(t *testing.T) {
	root := failedRun(t)
	snapshot, err := Load(t.Context(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bundle.json")
	manifest, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(manifest, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := snapshot.Generate(t.Context()); err == nil || result != nil || !strings.Contains(err.Error(), "changed after loading") {
		t.Fatalf("used obsolete evidence inventory: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "exports")); !os.IsNotExist(err) {
		t.Fatalf("changed manifest received exports: %v", err)
	}
}

func TestOutputFailureRetainsCoverageAndArchive(t *testing.T) {
	root := failedRun(t)
	if err := os.MkdirAll(filepath.Join(root, "exports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "exports", "arazzo"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Generate(t.Context(), root)
	if err == nil || len(result.Errors) == 0 || result.Coverage["json"] == "" || result.Arazzo != "" {
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

func TestHTMLFailureRetainsJSONAndArazzo(t *testing.T) {
	root := failedRun(t)
	if err := os.MkdirAll(filepath.Join(root, "exports", "coverage.html"), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := Generate(t.Context(), root)
	if err == nil || result.Coverage["html"] != "" || result.Coverage["json"] == "" || result.Arazzo == "" || len(result.Errors) != 1 || result.Errors[0].Format != "coverage.html" {
		t.Fatalf("HTML failure hid other outputs: %+v, %v", result, err)
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatalf("HTML output error changed source evidence: %v", err)
	}
}

func TestOutputDirectorySymlinkCannotWriteOutsideArchive(t *testing.T) {
	root := failedRun(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "exports")); err != nil {
		t.Skip(err)
	}
	if _, err := Generate(t.Context(), root); err == nil {
		t.Fatal("accepted exports symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside archive: %v %v", entries, err)
	}
}

func TestAutomaticOutputsProtectSourceBackedByExports(t *testing.T) {
	for _, target := range []string{"coverage", "html", "arazzo", "receipt"} {
		for _, alias := range []string{"source symlink", "output hardlink"} {
			t.Run(target+"/"+alias, func(t *testing.T) {
				root := failedRun(t)
				outputs, err := Generate(t.Context(), root)
				if err != nil {
					t.Fatal(err)
				}
				output := outputs.Coverage["json"]
				switch target {
				case "html":
					output = outputs.Coverage["html"]
				case "arazzo":
					output = outputs.Arazzo
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
				if _, err := Generate(t.Context(), root); err == nil {
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

func jsonRESTRun(t *testing.T) string {
	t.Helper()
	return failedRun(t, func(c *artifact.Artifact) {
		c.Traffic[0].Response.Headers = map[string][]string{"Content-Type": {"application/json"}}
		c.Traffic[0].Response.Body = &artifact.Body{Text: `{"error":"unavailable"}`}
	})
}

func TestAutomaticArazzoIncludesDocumentAndSchemaCompanions(t *testing.T) {
	root := jsonRESTRun(t)
	result, err := Generate(t.Context(), root)
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

func TestArazzoCompanionPublicationFailurePreservesWorkflow(t *testing.T) {
	root := t.TempDir()
	schemas := filepath.Join(root, "schemas")
	if err := os.Mkdir(schemas, 0700); err != nil {
		t.Fatal(err)
	}
	workflow := filepath.Join(root, "arazzo.json")
	if err := os.WriteFile(workflow, []byte("previous workflow"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(schemas, 0500); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = os.Chmod(schemas, 0700) })
	if err := os.WriteFile(filepath.Join(schemas, "probe"), nil, 0600); err == nil {
		t.Skip("filesystem permits writes to a read-only directory")
	}
	export := &arazzo.Export{Document: &arazzo.Document{}, Files: []arazzo.File{{Path: "schemas/openapi.json", Data: []byte("new spec")}}}
	files, err := ArazzoFiles(export, "arazzo.json")
	if err != nil {
		t.Fatal(err)
	}
	writer := archiveoutput.Writer{Directory: root, Private: true}
	if err := writer.Write(t.Context(), files); err == nil {
		t.Fatal("published companion into read-only directory")
	}
	if data, err := os.ReadFile(workflow); err != nil || string(data) != "previous workflow" {
		t.Fatalf("replaced workflow before publishing its sources: %q, %v", data, err)
	}
}

func TestAutomaticArazzoSkipsNonJSONTraffic(t *testing.T) {
	root := failedRun(t, func(c *artifact.Artifact) {
		c.Traffic[0].Response.Headers = artifact.Headers{"Content-Type": {"text/plain"}}
		c.Traffic[0].Response.Body = &artifact.Body{Text: "unavailable"}
	})
	result, err := Generate(t.Context(), root)
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
	result, err := Generate(t.Context(), root)
	if err == nil || result.Arazzo != "" || len(result.ArazzoFiles) != 0 || len(result.Errors) != 1 || result.Errors[0].Format != "arazzo" || result.Coverage["json"] == "" {
		t.Fatalf("Arazzo failure lost other exports: %+v, %v", result, err)
	}
	if _, _, err := bundle.Load(root); err != nil {
		t.Fatal(err)
	}
}

func TestNestedCompanionWritesRejectEverySymlinkAncestor(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "exports", "arazzo")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(parent, "schemas")); err != nil {
		t.Skip(err)
	}
	writer := archiveoutput.Writer{Directory: filepath.Join(root, "exports"), CreateDir: true, Private: true}
	if err := writer.Write(t.Context(), []archiveoutput.File{{Name: "arazzo/schemas/service/openapi.json", Data: []byte("secret")}}); err == nil {
		t.Fatal("followed nested symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside: %v %v", entries, err)
	}
	if err := writer.Write(t.Context(), []archiveoutput.File{{Name: "../escape"}}); err == nil {
		t.Fatal("allowed path escape")
	}
}
