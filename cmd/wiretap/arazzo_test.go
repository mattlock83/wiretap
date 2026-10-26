package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func TestCLIArazzoExportsPortableWorkflow(t *testing.T) {
	root := arazzoBundleFixture(t, true, "http")
	before := arazzoEvidenceFiles(t, root)
	for _, command := range []string{"report", "export"} {
		for _, option := range []string{"--output", "--output-dir"} {
			t.Run(command+option, func(t *testing.T) {
				directory := filepath.Join(t.TempDir(), "new", "arazzo")
				output := directory
				name := "arazzo.json"
				if option == "--output" {
					directory = t.TempDir()
					name = "captured-workflows.json"
					output = filepath.Join(directory, name)
					if err := os.WriteFile(output, []byte("old document"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if code := execute(context.Background(), []string{command, "--bundle", root, "--format", "arazzo", "--sut-name", "Inventory API", option, output}); code != 0 {
					t.Fatalf("Arazzo export exit = %d", code)
				}
				export, err := arazzo.Generate(root, "Inventory API")
				if err != nil {
					t.Fatal(err)
				}
				var want bytes.Buffer
				if err := arazzo.Write(&want, export.Document); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(directory, name))
				if err != nil || !bytes.Equal(data, want.Bytes()) {
					t.Fatalf("Arazzo document differs: %s, %v", data, err)
				}
				var document struct {
					Arazzo string `json:"arazzo"`
					Info   struct {
						Title string `json:"title"`
					} `json:"info"`
					Sources []struct {
						URL string `json:"url"`
					} `json:"sourceDescriptions"`
					Workflows []struct {
						Steps []struct {
							OperationPath string `json:"operationPath"`
						} `json:"steps"`
					} `json:"workflows"`
				}
				if err := json.Unmarshal(data, &document); err != nil || document.Arazzo != "1.0.1" || !strings.HasPrefix(document.Info.Title, "Inventory API") || len(document.Workflows) != 1 || len(document.Sources) != 1 {
					t.Fatalf("invalid Arazzo document: %+v, %v", document, err)
				}
				if steps := document.Workflows[0].Steps; len(steps) != 1 || !strings.Contains(steps[0].OperationPath, "#/paths/~1items/get") {
					t.Fatalf("missing captured OpenAPI operation: %+v", steps)
				}
				for _, companion := range export.Files {
					data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(companion.Path)))
					if err != nil || !bytes.Equal(data, companion.Data) {
						t.Fatalf("missing or changed companion %q: %v", companion.Path, err)
					}
				}
				if _, err := os.Stat(filepath.Join(directory, filepath.FromSlash(document.Sources[0].URL))); err != nil {
					t.Fatalf("OpenAPI reference does not resolve beside Arazzo document: %v", err)
				}
				if option == "--output" {
					info, err := os.Stat(output)
					if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
						t.Fatalf("existing permissions changed: %v, %v", info, err)
					}
				}
			})
		}
	}
	if !reflect.DeepEqual(before, arazzoEvidenceFiles(t, root)) {
		t.Fatal("Arazzo export changed archive evidence")
	}
}

func TestCLIArazzoOutputValidation(t *testing.T) {
	root := arazzoBundleFixture(t, true, "http")
	for _, options := range [][]string{
		{"--output", "workflow.json", "--output-dir", "arazzo"},
		{"--policy", "policy.json"},
		{"--postman-config", "postman.json"},
		{"--postman-omit-incomplete"},
		{"--run-set", "run-set.json"},
	} {
		t.Run(strings.Join(options, " "), func(t *testing.T) {
			args := append([]string{"export", "--bundle", root, "--format", "arazzo"}, options...)
			if code := execute(context.Background(), args); code != 2 {
				t.Fatalf("invalid options exit = %d", code)
			}
		})
	}
	code, output := executeCapturingStdout(t, []string{"export", "--bundle", root, "--format", "arazzo"})
	if code != 2 || len(output) != 0 {
		t.Fatalf("stdout export without OpenAPI companions = %d, %q", code, output)
	}
}

func TestCLIArazzoExportsObservedJSONWithoutContract(t *testing.T) {
	root := arazzoBundleFixture(t, false, "http")
	directory := t.TempDir()
	if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "arazzo", "--output-dir", directory}); code != 0 {
		t.Fatalf("observed JSON/REST export exit = %d", code)
	}
	data, err := os.ReadFile(filepath.Join(directory, "arazzo.json"))
	if err != nil || !strings.Contains(string(data), "observed/api_1.openapi.json") {
		t.Fatalf("missing observed OpenAPI reference: %s, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(directory, "observed", "api_1.openapi.json"))
	if err != nil || !strings.Contains(string(data), "Observed requests only") || !strings.Contains(string(data), `"/items"`) {
		t.Fatalf("missing observed OpenAPI companion: %s, %v", data, err)
	}
}

func TestCLIArazzoRequiresEligibleTrafficBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name, protocol string
		schema         bool
	}{
		{name: "HTML response", protocol: "html", schema: true},
		{name: "gRPC", protocol: "grpc", schema: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := arazzoBundleFixture(t, tc.schema, tc.protocol)
			directory := filepath.Join(t.TempDir(), "uncreated")
			if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "arazzo", "--output-dir", directory}); code != 1 {
				t.Fatalf("ineligible traffic exit = %d", code)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatalf("ineligible traffic created output directory: %v", err)
			}
			output := filepath.Join(t.TempDir(), "existing.json")
			if err := os.WriteFile(output, []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := exportArazzo(root, "", output, ""); err == nil || !strings.Contains(err.Error(), "JSON/REST") {
				t.Fatalf("missing eligibility explanation: %v", err)
			}
			if data, err := os.ReadFile(output); err != nil || string(data) != "keep" {
				t.Fatalf("ineligible traffic replaced existing output: %q, %v", data, err)
			}
		})
	}
}

func TestCLIArazzoProtectsSourceDestinations(t *testing.T) {
	for _, target := range []string{"manifest", "source directory", "directory alias", "companion hardlink", "companion directory"} {
		t.Run(target, func(t *testing.T) {
			root := arazzoBundleFixture(t, true, "http")
			before := arazzoEvidenceFiles(t, root)
			option, output := "--output", filepath.Join(root, "bundle.json")
			var existing string
			switch target {
			case "source directory":
				option, output = "--output-dir", filepath.Join(root, "tests")
			case "directory alias":
				alias := filepath.Join(t.TempDir(), "capture")
				if err := os.Symlink(root, alias); err != nil {
					t.Skip(err)
				}
				option, output = "--output-dir", filepath.Join(alias, "tests", "new")
			case "companion hardlink", "companion directory":
				option, output = "--output-dir", t.TempDir()
				export, err := arazzo.Generate(root, "")
				if err != nil || len(export.Files) == 0 {
					t.Fatalf("expected OpenAPI companion: %v", err)
				}
				existing = filepath.Join(output, "arazzo.json")
				if err := os.WriteFile(existing, []byte("keep"), 0644); err != nil {
					t.Fatal(err)
				}
				companion := filepath.Join(output, filepath.FromSlash(export.Files[0].Path))
				if err := os.MkdirAll(filepath.Dir(companion), 0755); err != nil {
					t.Fatal(err)
				}
				if target == "companion hardlink" {
					if err := os.Link(filepath.Join(root, "bundle.json"), companion); err != nil {
						t.Skip(err)
					}
				} else if err := os.Mkdir(companion, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "arazzo", option, output}); code == 0 {
				t.Fatal("accepted unsafe export destination")
			}
			if existing != "" {
				if data, err := os.ReadFile(existing); err != nil || string(data) != "keep" {
					t.Fatalf("failed preflight changed document: %q, %v", data, err)
				}
			}
			if !reflect.DeepEqual(before, arazzoEvidenceFiles(t, root)) {
				t.Fatal("rejected export changed archive evidence")
			}
		})
	}
}

func TestCLIArazzoCorruptArchivePreservesOutput(t *testing.T) {
	root := arazzoBundleFixture(t, true, "http")
	if err := os.WriteFile(filepath.Join(root, "tests", "items.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "arazzo.json")
	if err := os.WriteFile(output, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "arazzo", "--output", output}); code != 2 {
		t.Fatalf("corrupt archive exit = %d", code)
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "keep" {
		t.Fatalf("corrupt archive changed document: %q, %v", data, err)
	}
	directory := filepath.Join(t.TempDir(), "uncreated")
	if code := execute(context.Background(), []string{"export", "--bundle", root, "--format", "arazzo", "--output-dir", directory}); code != 2 {
		t.Fatalf("corrupt archive exit = %d", code)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("corrupt archive created output directory: %v", err)
	}
}

func arazzoBundleFixture(t *testing.T, withSchema bool, protocol string) string {
	t.Helper()
	if protocol == "grpc" {
		return postmanGRPCBundleFixture(t, false, false)
	}
	writer, err := bundle.NewAt(filepath.Join(t.TempDir(), "capture"))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 26, 1, 2, 3, 0, time.UTC)
	capture := &artifact.Artifact{
		Version: 1, TestCase: artifact.TestCase{ID: "items", Name: "reads items", StartedAt: started, Status: "pass"},
		Traffic: []artifact.Flow{{
			ID: "read-items", Protocol: "http", Channel: "test_to_sut", StartedAt: started,
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://sut/items"}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"items":[]}`}}},
		}},
	}
	if protocol == "html" {
		capture.Traffic[0].Response.HTTP.Headers = artifact.Headers{"Content-Type": {"text/html"}}
		capture.Traffic[0].Response.HTTP.Body = &artifact.Body{Text: "<html></html>"}
	}
	var schemas []artifact.SchemaRef
	if withSchema {
		schemas = []artifact.SchemaRef{{
			ID: "items-api", Role: "sut", Type: "openapi",
			Source: artifact.SchemaSource{Type: "inline", Content: `{"openapi":"3.0.3","info":{"title":"Items","version":"1"},"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}}}}`},
		}}
	}
	capture.Schemas, err = writer.Register(capture.TestCase, schemas)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}

func arazzoEvidenceFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
