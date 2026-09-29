package bundlereport

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"github.com/phrond/evermock/internal/coverage"
	"gopkg.in/yaml.v3"
)

func mockFlow(id, asset, path, body string) artifact.Flow {
	return artifact.Flow{ID: id, Protocol: "http", Channel: "sut_to_dependency", AssetID: asset,
		Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://mock.test" + path}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: body}}},
	}
}

func decodedDraft(t *testing.T, data []byte) map[string]any {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := yaml.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func childMap(value map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		value = value[key].(map[string]any)
	}
	return value
}

func TestMockDraftsArePerDependencyAndDoNotInventRequirements(t *testing.T) {
	run := &bundle.Run{RunID: "run-1", Revision: 3, DependencyKinds: map[string]string{"inventory": "mock", "billing": "mock"}, Attempts: []bundle.AttemptRef{{AttemptID: "first", FileRef: bundle.FileRef{Path: "evidence/attempts/first.json"}}, {AttemptID: "second", FileRef: bundle.FileRef{Path: "evidence/attempts/second.json"}}}}
	first := &artifact.Artifact{TestCase: artifact.TestCase{ID: "first", Status: "pass"}, Traffic: []artifact.Flow{
		mockFlow("f1", "inventory", "/items/123?tag=secret-query&tag=two", `{"nested":{"id":1,"name":"secret-value"},"values":[1,null]}`),
		mockFlow("other", "billing", "/items/123", `{"billing_only":true}`),
	}}
	second := &artifact.Artifact{TestCase: artifact.TestCase{ID: "second", Status: "fail"}, Traffic: []artifact.Flow{
		mockFlow("f2", "inventory", "/items/456", `{"nested":{"id":1.5},"values":["string"],"failed_only":true}`),
	}}
	// A failed test's mock responses still document the assumptions it exercised.
	doc, draft := observedMockAPI("inventory", run, []*artifact.Artifact{first, second})
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed := decodedDraft(t, data)
	if draft.Exchanges != 2 || draft.Operations != 1 {
		t.Fatalf("unexpected counts: %+v", draft)
	}
	for _, forbidden := range []string{"secret-value", "secret-query", "billing_only", "additionalProperties", "requestBody"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("unexpected %q in draft", forbidden)
		}
	}
	operation := childMap(parsed, "paths", "/items/{param2}", "get")
	schema := childMap(operation, "responses", "200", "content", "application/json", "schema")
	if schema["required"] != nil || childMap(schema, "properties", "nested")["required"] != nil {
		t.Fatal("observed fields became required")
	}
	if childMap(schema, "properties", "nested", "properties", "id")["type"] != "number" {
		t.Fatal("numeric samples not merged")
	}
	if childMap(schema, "properties", "failed_only")["type"] != "boolean" {
		t.Fatal("failed test observation lost")
	}
	parameters := operation["parameters"].([]any)
	if parameters[0].(map[string]any)["required"] != true || parameters[1].(map[string]any)["required"] != nil {
		t.Fatal("only path parameters may be required")
	}
	refs := operation["x-wiretap-evidence"].([]any)
	if len(refs) != 2 || refs[1].(map[string]any)["test_status"] != "fail" || refs[0].(map[string]any)["path"] != "../../evidence/attempts/first.json" {
		t.Fatal("evidence provenance lost")
	}
	// Input order does not affect the generated document.
	reordered, _ := observedMockAPI("inventory", run, []*artifact.Artifact{second, first})
	otherData, _ := yaml.Marshal(reordered)
	if !bytes.Equal(data, otherData) {
		t.Fatal("draft depends on capture order")
	}
}

func TestMockDraftBoundsIncompleteAndUnsupportedEvidence(t *testing.T) {
	truncated := mockFlow("truncated", "inventory", "/body", `{"partial":`)
	truncated.Response.HTTP.Body.Truncated = true
	known := mockFlow("known", "inventory", "/body", `{"id":1}`)
	encoded := mockFlow("decoded", "inventory", "/compressed", `{"decoded":true}`)
	encoded.Response.HTTP.Body.ContentEncoding = "gzip" // Body text was decompressed by capture.
	broken := mockFlow("broken", "inventory", "/not-a-response", "")
	broken.TransportError = "connection failed"
	broken.Response.HTTP.Status = 502
	run := &bundle.Run{RunID: "run", Revision: 1, Attempts: []bundle.AttemptRef{{AttemptID: "a", FileRef: bundle.FileRef{Path: "evidence/attempts/a.json"}}}}
	doc, draft := observedMockAPI("inventory", run, []*artifact.Artifact{{TestCase: artifact.TestCase{ID: "a", Status: "fail"}, CaptureIncomplete: true, Traffic: []artifact.Flow{known, truncated, encoded, broken}}})
	data, _ := yaml.Marshal(doc)
	parsed := decodedDraft(t, data)
	if draft.Exchanges != 3 || len(childMap(parsed, "paths")) != 2 {
		t.Fatal("transport error became a mock operation")
	}
	schema := childMap(parsed, "paths", "/body", "get", "responses", "200", "content", "application/json", "schema")
	if len(schema) != 0 {
		t.Fatal("partial body produced a constrained schema")
	}
	if childMap(parsed, "paths", "/compressed", "get", "responses", "200", "content", "application/json", "schema", "properties", "decoded")["type"] != "boolean" {
		t.Fatal("decoded body shape lost")
	}
	if childMap(parsed, "x-wiretap")["excluded_exchanges"] != 1 || !bytes.Contains(data, []byte("capture_incomplete: true")) {
		t.Fatal("incomplete evidence was hidden")
	}
	empty, _ := observedMockAPI("unused", run, nil)
	emptyData, _ := yaml.Marshal(empty)
	if len(childMap(decodedDraft(t, emptyData), "paths")) != 0 {
		t.Fatal("unused mock invented operations")
	}
}

func TestMockDraftsAreAutomaticRebuildableAndSeparateFromContracts(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(bundle.Identity{DependencyKinds: map[string]string{"supplied": "mock", "omitted": "mock", "unused": "mock", "live": "service", "unknown": "unknown"}, SUTTargetKind: "mock"}); err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt-1", Name: "mock assumptions", Status: "fail", StartedAt: time.Now().UTC()}
	refs, err := writer.Register(tc, []artifact.SchemaRef{
		{ID: "provided", AssetID: "supplied", Role: "dependency", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: "openapi: 3.0.0\ninfo: {title: Inventory, version: '1'}\npaths: {}\n"}},
		{ID: "omitted", AssetID: "omitted", Role: "dependency", Type: "openapi", Source: artifact.SchemaSource{Type: "omitted"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flows := []artifact.Flow{}
	for _, id := range []string{"supplied", "omitted", "live", "unknown"} {
		flows = append(flows, mockFlow(id, id, "/items", `{"available":true}`))
	}
	sut := mockFlow("sut", "", "/sut-only", `{"sut_only":true}`)
	sut.Channel = "test_to_sut"
	flows = append(flows, sut)
	if _, err := writer.Commit(&artifact.Artifact{Version: 1, TestCase: tc, Schemas: refs, Traffic: flows}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	inventoryBefore, _ := os.ReadFile(filepath.Join(writer.Path(), "evidence/run.json"))
	evidenceBefore, _ := os.ReadFile(filepath.Join(writer.Path(), "evidence/attempts/attempt-1.json"))
	before, err := coverage.Generate(t.Context(), writer.Path(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(writer.Path()); err != nil {
		t.Fatal(err)
	}
	originals := map[string][]byte{}
	for _, id := range []string{"supplied", "omitted", "unused"} {
		path := filepath.Join(writer.Path(), mockDraftPath(id))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		parsed := decodedDraft(t, data)
		if childMap(parsed, "x-wiretap")["api_id"] != id {
			t.Fatal("wrong mock identity")
		}
		originals[id] = data
	}
	files, _ := filepath.Glob(filepath.Join(writer.Path(), "dependencies/mocks/*.yaml"))
	if len(files) != 3 {
		t.Fatalf("want only three mock drafts, got %v", files)
	}
	for _, rel := range []string{"dependencies/index.md", "viewer/index.html"} {
		data, err := os.ReadFile(filepath.Join(writer.Path(), rel))
		if err != nil || !strings.Contains(string(data), "Inferred mock assumptions") {
			t.Fatalf("missing draft navigation: %s: %v", rel, err)
		}
	}
	after, err := coverage.Generate(t.Context(), writer.Path(), "", false)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("drafts changed coverage: %v", err)
	}
	// Copy only canonical inputs; all mock drafts and pages must be rebuildable.
	copied := filepath.Join(t.TempDir(), "bundle")
	for _, dir := range []string{"evidence", "schemas", "payloads"} {
		source := filepath.Join(writer.Path(), dir)
		if _, err := os.Stat(source); os.IsNotExist(err) {
			continue
		}
		if err := os.CopyFS(filepath.Join(copied, dir), os.DirFS(source)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Rebuild(copied); err != nil {
		t.Fatal(err)
	}
	for id, want := range originals {
		got, err := os.ReadFile(filepath.Join(copied, mockDraftPath(id)))
		if err != nil || !bytes.Equal(want, got) {
			t.Fatalf("copied draft changed for %s: %v", id, err)
		}
	}
	inventoryAfter, _ := os.ReadFile(filepath.Join(writer.Path(), "evidence/run.json"))
	evidenceAfter, _ := os.ReadFile(filepath.Join(writer.Path(), "evidence/attempts/attempt-1.json"))
	if !bytes.Equal(inventoryBefore, inventoryAfter) || !bytes.Equal(evidenceBefore, evidenceAfter) {
		t.Fatal("rendering mutated evidence")
	}
}

func TestMockDraftRecordsRequestShapesWithoutMakingBodiesRequired(t *testing.T) {
	flow := mockFlow("request", "inventory", "/items", `{"accepted":true}`)
	flow.Request.HTTP.Method = "POST"
	flow.Request.HTTP.Headers = artifact.Headers{"Content-Type": {"application/json"}}
	flow.Request.HTTP.Body = &artifact.Body{Text: `{"id":"private-request-value","quantity":2}`}
	run := &bundle.Run{RunID: "run", Revision: 1, Attempts: []bundle.AttemptRef{{AttemptID: "a", FileRef: bundle.FileRef{Path: "evidence/attempts/a.json"}}}}
	doc, _ := observedMockAPI("inventory", run, []*artifact.Artifact{{TestCase: artifact.TestCase{ID: "a", Status: "pass"}, Traffic: []artifact.Flow{flow}}})
	raw, _ := yaml.Marshal(doc)
	parsed := decodedDraft(t, raw)
	body := childMap(parsed, "paths", "/items", "post", "requestBody")
	schema := childMap(body, "content", "application/json", "schema")
	if body["required"] != nil || schema["required"] != nil || schema["additionalProperties"] != nil {
		t.Fatal("request observations became requirements")
	}
	if childMap(schema, "properties", "quantity")["type"] != "integer" || bytes.Contains(raw, []byte("private-request-value")) {
		t.Fatal("request shape or privacy incorrect")
	}
}
