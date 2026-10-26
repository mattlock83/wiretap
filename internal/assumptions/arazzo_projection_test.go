package assumptions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/artifact"
)

func arazzoCheckFlow() artifact.Flow {
	return artifact.Flow{Protocol: "http", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "https://stock.example/items?mode=%24inputs.literal", Headers: artifact.Headers{"Content-Type": {"application/json"}, "X-Test": {"{$steps.literal}"}}, Body: &artifact.Body{Text: `{"id":18446744073709551615,"literal":"{$inputs.literal}"}`}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"available":true}`}}}}
}

func writeCheckArazzo(t *testing.T, root string) (string, *arazzo.Export) {
	t.Helper()
	export, err := arazzo.Generate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "exports", "arazzo", "arazzo.json")
	if export.Document == nil {
		return path, export
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writeErr, closeErr := arazzo.Write(file, export.Document), file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write Arazzo: %v / %v", writeErr, closeErr)
	}
	for _, companion := range export.Files {
		destination := filepath.Join(filepath.Dir(path), filepath.FromSlash(companion.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, companion.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path, export
}

func loadArazzoCheckSnapshot(t *testing.T, root string) *snapshot {
	t.Helper()
	s, err := loadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func editCheckArazzo(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeProjectionJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	object := value.(map[string]any)
	edit(object)
	data, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func firstCheckArazzoWorkflow(document map[string]any) map[string]any {
	return document["workflows"].([]any)[0].(map[string]any)
}

func firstCheckArazzoStep(document map[string]any) map[string]any {
	return firstCheckArazzoWorkflow(document)["steps"].([]any)[0].(map[string]any)
}

func TestVerifyArazzoAcceptsDefaultDirectoryCustomFilenameAndCosmetics(t *testing.T) {
	root := writeCheckArchive(t, consumerCheckIdentity(), "complete", checkCapture(false, arazzoCheckFlow()))
	path, export := writeCheckArazzo(t, root)
	s := loadArazzoCheckSnapshot(t, root)
	for _, input := range []string{"", filepath.Dir(path), path} {
		got, companions, err := s.verifyArazzo(input, "stock")
		if err != nil || got != path || len(companions) != len(export.Files) {
			t.Fatalf("input %q: path %q companions %v error %v", input, got, companions, err)
		}
	}
	custom := filepath.Join(filepath.Dir(path), "custom-workflows.json")
	if err := os.Rename(path, custom); err != nil {
		t.Fatal(err)
	}
	editCheckArazzo(t, custom, func(document map[string]any) {
		info := document["info"].(map[string]any)
		info["title"], info["description"] = "Custom SUT captured workflows", "Custom presentation"
		properties := firstCheckArazzoWorkflow(document)["inputs"].(map[string]any)["properties"].(map[string]any)
		for _, property := range properties {
			value := property.(map[string]any)["default"]
			if object, ok := value.(map[string]any); ok {
				object["id"] = json.Number("18446744073709551615.0")
			}
		}
	})
	if _, _, err := s.verifyArazzo(custom, "stock"); err != nil {
		t.Fatalf("cosmetic changes and equivalent exact number rejected: %v", err)
	}
}

func TestVerifyArazzoRejectsSelectedWorkflowEdits(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"literal": func(document map[string]any) {
			properties := firstCheckArazzoWorkflow(document)["inputs"].(map[string]any)["properties"].(map[string]any)
			for _, property := range properties {
				if object, ok := property.(map[string]any)["default"].(map[string]any); ok {
					object["id"] = json.Number("18446744073709551614")
				}
			}
		},
		"status": func(document map[string]any) {
			firstCheckArazzoStep(document)["successCriteria"] = []any{map[string]any{"condition": "$statusCode == 201"}}
		},
		"header": func(document map[string]any) {
			firstCheckArazzoStep(document)["x-wiretap-request-headers"].(map[string]any)["X-Test"] = []any{"edited"}
		},
		"operation": func(document map[string]any) {
			step := firstCheckArazzoStep(document)
			step["operationPath"] = strings.Replace(step["operationPath"].(string), "/post", "/get", 1)
		},
		"attempt":          func(document map[string]any) { firstCheckArazzoWorkflow(document)["x-wiretap-attempt-id"] = "other" },
		"flow":             func(document map[string]any) { firstCheckArazzoStep(document)["x-wiretap-flow-id"] = "other" },
		"boundary":         func(document map[string]any) { firstCheckArazzoWorkflow(document)["x-wiretap-boundary"] = "mock:other" },
		"missing workflow": func(document map[string]any) { document["workflows"] = []any{} },
		"extra workflow": func(document map[string]any) {
			copy := map[string]any{}
			for key, value := range firstCheckArazzoWorkflow(document) {
				copy[key] = value
			}
			copy["workflowId"] = "extra-workflow"
			document["workflows"] = append(document["workflows"].([]any), copy)
		},
		"extra step": func(document map[string]any) {
			workflow := firstCheckArazzoWorkflow(document)
			workflow["steps"] = append(workflow["steps"].([]any), firstCheckArazzoStep(document))
		},
		"version": func(document map[string]any) { document["arazzo"] = "1.0.0" },
		"source": func(document map[string]any) {
			document["sourceDescriptions"].([]any)[0].(map[string]any)["type"] = "arazzo"
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeCheckArchive(t, consumerCheckIdentity(), "complete", checkCapture(false, arazzoCheckFlow()))
			path, _ := writeCheckArazzo(t, root)
			editCheckArazzo(t, path, edit)
			if _, _, err := loadArazzoCheckSnapshot(t, root).verifyArazzo("", "stock"); err == nil {
				t.Fatal("edited projection certified")
			}
		})
	}
}

func TestVerifyArazzoRejectsMissingTamperedAndUnsafeCompanions(t *testing.T) {
	for _, scenario := range []string{"missing", "edited", "symlink", "directory symlink escape", "remote", "traversal", "duplicate document key", "duplicate companion key"} {
		t.Run(scenario, func(t *testing.T) {
			root := writeCheckArchive(t, consumerCheckIdentity(), "complete", checkCapture(false, arazzoCheckFlow()))
			path, export := writeCheckArazzo(t, root)
			companion := filepath.Join(filepath.Dir(path), filepath.FromSlash(export.Files[0].Path))
			switch scenario {
			case "missing":
				if err := os.Remove(companion); err != nil {
					t.Fatal(err)
				}
			case "edited":
				editCheckArazzo(t, companion, func(document map[string]any) { document["info"].(map[string]any)["title"] = "stale contract" })
			case "symlink":
				destination := filepath.Join(t.TempDir(), "source.json")
				if err := os.WriteFile(destination, export.Files[0].Data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(companion); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, companion); err != nil {
					t.Fatal(err)
				}
			case "directory symlink escape":
				destination := t.TempDir()
				if err := os.WriteFile(filepath.Join(destination, filepath.Base(companion)), export.Files[0].Data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(companion); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Dir(companion)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, filepath.Dir(companion)); err != nil {
					t.Fatal(err)
				}
			case "remote", "traversal":
				editCheckArazzo(t, path, func(document map[string]any) {
					address := "https://example.invalid/openapi.json"
					if scenario == "traversal" {
						address = "../outside.json"
					}
					document["sourceDescriptions"].([]any)[0].(map[string]any)["url"] = address
				})
			case "duplicate document key", "duplicate companion key":
				target := path
				if scenario == "duplicate companion key" {
					target = companion
				}
				data, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				data = append([]byte(`{"arazzo":"1.0.1",`), data[1:]...)
				if scenario == "duplicate companion key" {
					data = append([]byte(`{"openapi":"3.0.3",`), data[len(`{"arazzo":"1.0.1",`):]...)
				}
				if err := os.WriteFile(target, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := loadArazzoCheckSnapshot(t, root).verifyArazzo("", "stock"); err == nil {
				t.Fatal("unsafe or edited companion certified")
			}
		})
	}
}

func TestVerifyArazzoAllowsUnselectedBoundariesAndOnlyReadsSelectedSources(t *testing.T) {
	capture := checkCapture(false, arazzoCheckFlow(), arazzoCheckFlow())
	capture.Traffic[1].Channel, capture.Traffic[1].AssetID = "test_to_sut", ""
	capture.Traffic[1].Request.HTTP.URL = "https://shop.example/checkout"
	root := writeCheckArchive(t, consumerCheckIdentity(), "complete", capture)
	path, export := writeCheckArazzo(t, root)
	var ignoredSource string
	for _, workflow := range export.Document.Workflows {
		if workflow.Boundary == "sut" {
			operation := workflow.Steps[0].OperationPath
			name, _, _ := strings.Cut(strings.TrimPrefix(operation, "{$sourceDescriptions."), ".url}#")
			for _, source := range export.Document.Sources {
				if source.Name == name {
					ignoredSource = source.URL
				}
			}
		}
	}
	editCheckArazzo(t, path, func(document map[string]any) {
		for _, value := range document["workflows"].([]any) {
			workflow := value.(map[string]any)
			if workflow["x-wiretap-boundary"] == "sut" {
				workflow["steps"] = []any{}
			}
		}
	})
	if ignoredSource == "" {
		t.Fatal("missing unselected source")
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), filepath.FromSlash(ignoredSource))); err != nil {
		t.Fatal(err)
	}
	_, companions, err := loadArazzoCheckSnapshot(t, root).verifyArazzo("", "stock")
	if err != nil || len(companions) != 1 {
		t.Fatalf("unselected boundary became an obligation: %v, %v", companions, err)
	}
}

func TestVerifyArazzoRejectsPartiallyRepresentedSelectedTraffic(t *testing.T) {
	capture := checkCapture(false, arazzoCheckFlow(), arazzoCheckFlow())
	capture.Traffic[1].Response.HTTP.Headers["Content-Type"] = []string{"text/plain"}
	capture.Traffic[1].Response.HTTP.Body.Text = "plain response"
	root := writeCheckArchive(t, consumerCheckIdentity(), "complete", capture)
	writeCheckArazzo(t, root)
	if _, _, err := loadArazzoCheckSnapshot(t, root).verifyArazzo("", "stock"); err == nil || !strings.Contains(err.Error(), "not completely representable") {
		t.Fatalf("partial selected traffic certified: %v", err)
	}
}

func TestVerifyArazzoVerifiesTransitiveFrozenCompanions(t *testing.T) {
	contractDir := t.TempDir()
	rootContract := filepath.Join(contractDir, "openapi.json")
	contract := validatingHTTPDocument(`{"$ref":"payload.json"}`)
	if err := os.WriteFile(rootContract, []byte(contract), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contractDir, "payload.json"), []byte(validatingHTTPBodySchema), 0600); err != nil {
		t.Fatal(err)
	}
	capture := checkCapture(true, validatingHTTPFlow())
	capture.Schemas = []artifact.SchemaRef{{ID: "provided", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: rootContract}}}
	root := writeCheckArchive(t, providerCheckIdentity(), "complete", capture)
	path, export := writeCheckArazzo(t, root)
	s := loadArazzoCheckSnapshot(t, root)
	_, companions, err := s.verifyArazzo("", "")
	if err != nil || len(companions) != 2 {
		t.Fatalf("transitive companion inventory: %v / %v", companions, err)
	}
	source := export.Document.Sources[0].URL
	for _, member := range export.Files {
		if member.Path != source {
			companion := filepath.Join(filepath.Dir(path), filepath.FromSlash(member.Path))
			editCheckArazzo(t, companion, func(document map[string]any) { document["required"] = []any{"different"} })
		}
	}
	if _, _, err := s.verifyArazzo("", ""); err == nil {
		t.Fatal("tampered transitive companion certified")
	}
}

func TestVerifyArazzoAllowsOnlyImplicitAbsentDocumentForEmptyShard(t *testing.T) {
	root := writeCheckArchive(t, consumerCheckIdentity(), "complete")
	s := loadArazzoCheckSnapshot(t, root)
	path, companions, err := s.verifyArazzo("", "stock")
	if err != nil || path != "" || len(companions) != 0 {
		t.Fatalf("empty shard: %q %v %v", path, companions, err)
	}
	missing := filepath.Join(t.TempDir(), "arazzo.json")
	if _, _, err := s.verifyArazzo(missing, "stock"); err == nil {
		t.Fatal("explicit missing document accepted for empty shard")
	}
	for _, contents := range []string{
		`{"workflows":[{"workflowId":"old","x-wiretap-boundary":"mock:stock","steps":[]}]}`,
		`{"workflows":[]}`,
	} {
		stale := filepath.Join(t.TempDir(), "arazzo.json")
		if err := os.WriteFile(stale, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.verifyArazzo(stale, "stock"); err == nil || !strings.Contains(err.Error(), "stale or unverifiable") {
			t.Fatalf("existing document certified for empty shard: %v", err)
		}
	}
	defaultPath := filepath.Join(root, "exports", "arazzo", "arazzo.json")
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultPath, []byte(`{"workflows":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.verifyArazzo("", "stock"); err == nil || !strings.Contains(err.Error(), "stale or unverifiable") {
		t.Fatalf("existing default document certified for empty shard: %v", err)
	}
}
