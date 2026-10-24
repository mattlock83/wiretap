package bundle

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
)

const testSchema = `openapi: 3.0.0
info: {title: Orders, version: "1"}
paths:
  /orders:
    get:
      responses:
        "200": {description: ok}
`

func TestBundleRoundTripAndFrozenSchema(t *testing.T) {
	dir := t.TempDir()
	schemaFile := filepath.Join(dir, "schema.yaml")
	if err := os.WriteFile(schemaFile, []byte(testSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(filepath.Join(dir, "output"))
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "reads orders", StartedAt: time.Now().UTC(), Status: "pass"}
	frozen, err := w.Register(tc, []artifact.SchemaRef{{ID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: schemaFile}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaFile, []byte("invalid: later edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := []byte{0, 1, 255, 42}
	art := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen, Meta: map[string]any{"large": json.Number("9007199254740993")}, Traffic: []artifact.Flow{{
		ID: "flow-1", Protocol: "http", Channel: "test_to_sut",
		Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://sut/orders", Body: &artifact.Body{Text: "hello"}}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Body: &artifact.Body{Bytes: base64.StdEncoding.EncodeToString(binary), Encoding: "base64"}}},
	}}}
	path, err := w.Commit(art)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "attempt-1.json") {
		t.Fatalf("unexpected attempt path %q", path)
	}
	if art.Traffic[0].Request.HTTP.Body.Text != "hello" {
		t.Fatal("commit mutated inline response")
	}
	run, captures, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "open" || len(captures) != 1 || len(run.Attempts) != 1 {
		t.Fatalf("unexpected run or captures: %+v %d", run, len(captures))
	}
	got := captures[0]
	if got.Traffic[0].Request.HTTP.Body.Text != "hello" || got.Traffic[0].Response.HTTP.Body.Bytes != base64.StdEncoding.EncodeToString(binary) {
		t.Fatal("body round-trip failed")
	}
	if got.Meta["large"] != json.Number("9007199254740993") {
		t.Fatalf("number changed: %#v", got.Meta["large"])
	}
	data, err := os.ReadFile(got.Schemas[0].Source.Path)
	if err != nil || !bytes.Contains(data, []byte("/orders")) {
		t.Fatalf("schema was not frozen: %v", err)
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	final, _, err := Load(w.Path())
	if err != nil || final.RunState != "complete" {
		t.Fatalf("final state: %+v %v", final, err)
	}
}

func TestBundleRejectsCorruptBodyAndDuplicateJSONKey(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "test", StartedAt: time.Now().UTC()}
	if _, err := w.Register(tc, nil); err != nil {
		t.Fatal(err)
	}
	art := &artifact.Artifact{Version: 1, TestCase: tc, Traffic: []artifact.Flow{{ID: "flow-1", Request: artifact.Message{HTTP: &artifact.HttpMessage{Body: &artifact.Body{Text: "secret"}}}}}}
	if _, err := w.Commit(art); err != nil {
		t.Fatal(err)
	}
	hash := digest([]byte("secret"))
	if err := os.WriteFile(filepath.Join(w.Path(), "payloads", hash+".bin"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(w.Path()); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("wanted integrity error, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.Path(), "bundle.json"), []byte(`{"format_version":"3.0","format_version":"3.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(w.Path()); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("wanted duplicate-key error, got %v", err)
	}
}

func TestExternalCyclicSchemasSurviveSourceRemoval(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.yaml")
	other := filepath.Join(dir, "other.yaml")
	rootSchema := `openapi: 3.0.0
info: {title: Cyclic, version: "1"}
paths:
  /items:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "other.yaml#/components/schemas/Item"}
components:
  schemas:
    Root: {type: object, properties: {item: {$ref: "other.yaml#/components/schemas/Item"}}}
`
	otherSchema := `components:
  schemas:
    Item:
      type: object
      properties:
        root: {$ref: "root.yaml#/components/schemas/Root"}
`
	if err := os.WriteFile(root, []byte(rootSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(otherSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(filepath.Join(dir, "output"))
	if err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "cyclic", StartedAt: time.Now().UTC()}
	frozen, err := w.Register(tc, []artifact.SchemaRef{{ID: "cyclic", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: root}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(&artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	_, captures, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || len(captures[0].Schemas) != 1 {
		t.Fatal("frozen schema unavailable")
	}
	manifestData, err := os.ReadFile(filepath.Join(filepath.Dir(captures[0].Schemas[0].Source.Path), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest schemaManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("expected two frozen documents, got %d", len(manifest.Files))
	}
}
