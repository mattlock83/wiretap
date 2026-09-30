package bundlereport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
)

func TestMarkdownCanBeRebuiltWithoutChangingExports(t *testing.T) {
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "uses inventory", StartedAt: time.Now().UTC(), Status: "pass"}
	frozen, err := w.Register(tc, []artifact.SchemaRef{{ID: "inventory", Role: "dependency", Type: "openapi", AssetID: "inventory", AssetName: "Inventory", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.0
info: {title: Inventory, version: "1"}
paths:
  /items:
    get:
      responses:
        "200": {description: ok}
`}}})
	if err != nil {
		t.Fatal(err)
	}
	art := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen, Traffic: []artifact.Flow{{ID: "flow-1", Protocol: "http", Channel: "sut_to_dependency", AssetID: "inventory", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://mock/items"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Body: &artifact.Body{Text: `{"available":true}`}}}}}}
	if _, err := w.Commit(art); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(w.Path()); err != nil {
		t.Fatal(err)
	}
	if err := Validate(w.Path()); err != nil {
		t.Fatalf("generated view is invalid: %v", err)
	}
	var operationPages []string
	filepath.WalkDir(filepath.Join(w.Path(), "dependencies"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(path, "operations") {
			operationPages = append(operationPages, path)
		}
		return err
	})
	if len(operationPages) != 1 {
		t.Fatalf("wanted one grouped dependency page, got %v", operationPages)
	}
	if data, err := os.ReadFile(operationPages[0]); err != nil || !bytes.Contains(data, []byte("Consumer field use and provider agreement are unknown")) {
		t.Fatalf("assumption semantics absent: %v", err)
	}
	before, err := coverage.Generate(t.Context(), w.Path(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := os.ReadFile(filepath.Join(w.Path(), "contract-coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	var actual coverage.ContractCoverage
	if err := json.Unmarshal(catalogue, &actual); err != nil {
		t.Fatal(err)
	}
	actualJSON, _ := json.Marshal(actual)
	expectedJSON, _ := json.Marshal(before.Contracts)
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatal("published catalogue differs from the shared coverage calculation")
	}
	html, err := os.ReadFile(filepath.Join(w.Path(), "viewer/index.html"))
	if err != nil || !bytes.Contains(html, []byte(`id="contracts"`)) || !bytes.Contains(html, []byte(before.Contracts.Specs[0].Targets[0].Key)) {
		t.Fatal("HTML Conditions tab is missing the published keys")
	}
	var jsonBefore bytes.Buffer
	if err := coverage.WriteReport(&jsonBefore, before, "json"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"index.md", "run.md", "coverage.md", "contract-coverage.md", "contract-coverage.json", "tests/attempt-1.md", "tests/index.md", "apis/index.md", "dependencies/index.md"} {
		if err := os.Remove(filepath.Join(w.Path(), rel)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(operationPages[0]); err != nil {
		t.Fatal(err)
	}
	after, err := coverage.Generate(t.Context(), w.Path(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	var jsonAfter bytes.Buffer
	if err := coverage.WriteReport(&jsonAfter, after, "json"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(jsonBefore.Bytes(), jsonAfter.Bytes()) {
		t.Fatal("Markdown changed JSON export")
	}
	if _, err := Refresh(w.Path()); err != nil {
		t.Fatal(err)
	}
	if err := Validate(w.Path()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.Path(), "tests", "attempt-1.md")); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(t.TempDir(), "copied")
	if err := filepath.WalkDir(w.Path(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(w.Path(), path)
		if err != nil {
			return err
		}
		target := filepath.Join(copied, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	copiedReport, err := coverage.Generate(t.Context(), copied, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var copiedJSON bytes.Buffer
	if err := coverage.WriteReport(&copiedJSON, copiedReport, "json"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(jsonAfter.Bytes(), copiedJSON.Bytes()) {
		t.Fatal("copied bundle changed JSON export")
	}
	if _, err := Rebuild(copied); err != nil {
		t.Fatal(err)
	}
	if err := Validate(copied); err != nil {
		t.Fatal(err)
	}
}

func TestGroupedViewScaleTracksAttemptsNotExchanges(t *testing.T) {
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref := artifact.SchemaRef{ID: "inventory", Role: "dependency", Type: "openapi", AssetID: "inventory", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.0
info: {title: Inventory, version: "1"}
paths:
  /items:
    get:
      responses: {"200": {description: ok}}
`}}
	for i := 0; i < 100; i++ {
		tc := artifact.TestCase{ID: fmt.Sprintf("attempt-%03d", i), Name: "reads inventory", StartedAt: time.Now().UTC(), Status: "pass"}
		frozen, err := w.Register(tc, []artifact.SchemaRef{ref})
		if err != nil {
			t.Fatal(err)
		}
		art := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen}
		for j := 0; j < 10; j++ {
			art.Traffic = append(art.Traffic, artifact.Flow{ID: fmt.Sprintf("flow-%02d", j), Protocol: "http", Channel: "sut_to_dependency", AssetID: "inventory", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://mock/items"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}})
		}
		if _, err := w.Commit(art); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Rebuild(w.Path()); err != nil {
		t.Fatal(err)
	}
	var pages, grouped int
	err = filepath.WalkDir(w.Path(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		pages++
		if strings.Contains(path, "dependencies") && strings.Contains(path, "operations") {
			grouped++
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Count(data, []byte("[Attempt ")) != 1000 || bytes.Count(data, []byte("  - Example:")) != 3 {
				return fmt.Errorf("grouped evidence list or preview bound incorrect")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if grouped != 1 || pages > 120 {
		t.Fatalf("wanted one grouped operation and O(attempts) pages, got %d operations, %d pages", grouped, pages)
	}
}
