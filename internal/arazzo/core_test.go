package arazzo

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func jsonFlow(id, method, address string) artifact.Flow {
	return artifact.Flow{ID: id, Protocol: "http", Channel: "test_to_sut", StartedAt: time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC), Request: &artifact.HttpMessage{Method: method, URL: address}, Response: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"ok":true}`}}}
}

func captureArchive(t *testing.T, schemas []artifact.SchemaRef, captures ...*artifact.Artifact) string {
	t.Helper()
	writer, err := bundle.New(filepath.Join(t.TempDir(), "capture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(bundle.Identity{DependencyKinds: map[string]string{"inventory": "mock", "remote": "service"}}); err != nil {
		t.Fatal(err)
	}
	for _, capture := range captures {
		capture.Version = 2
		frozen, err := writer.RegisterPrepared(capture.TestCase, schemas)
		if err != nil {
			t.Fatal(err)
		}
		capture.Schemas = nil
		for _, schema := range frozen {
			capture.Schemas = append(capture.Schemas, schema.Ref)
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

func TestObservedWorkflowsKeepAttemptsBoundariesAndLiterals(t *testing.T) {
	one := jsonFlow("z-first", "POST", "http://sut/items?name=%24inputs.literal")
	one.Request.Headers = artifact.Headers{"Content-Type": {"application/json"}, "X-Test": {"{$steps.literal}"}, "Authorization": {"Bearer synthetic"}}
	one.Request.Body = &artifact.Body{Text: `{"id":18446744073709551615,"literal":"{$inputs.literal}"}`}
	two := jsonFlow("a-second", "GET", "http://sut/items/%2F")
	two.StartedAt = one.StartedAt.Add(time.Second)
	two.Response.Status = 404
	mock := jsonFlow("mock", "GET", "http://inventory/items")
	mock.Channel = "sut_to_dependency"
	mock.AssetID = "inventory"
	remote := mock
	remote.ID = "external"
	remote.AssetID = "remote"
	first := &artifact.Artifact{TestCase: artifact.TestCase{ID: "retry-1", Name: "buys item", Status: "fail", StartedAt: one.StartedAt}, Traffic: []artifact.Flow{two, remote, one, mock}}
	secondFlow := jsonFlow("retry", "GET", "http://sut/items")
	second := &artifact.Artifact{TestCase: artifact.TestCase{ID: "retry-2", Name: "buys item", Status: "pass", StartedAt: one.StartedAt.Add(time.Minute)}, Traffic: []artifact.Flow{secondFlow}}
	root := captureArchive(t, nil, second, first)
	export, err := Generate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Document.Workflows) != 3 {
		t.Fatalf("workflows: %+v", export.Document.Workflows)
	}
	var sut Workflow
	for _, workflow := range export.Document.Workflows {
		if workflow.AttemptID == "retry-1" && workflow.Boundary == "sut" {
			sut = workflow
		}
	}
	if len(sut.Steps) != 2 || sut.Steps[0].FlowID != "z-first" || sut.Steps[1].FlowID != "a-second" {
		t.Fatalf("lost request start order: %+v", sut)
	}
	if sut.Steps[1].SuccessCriteria[0].Condition != "$statusCode == 404" {
		t.Fatal("lost negative status expectation")
	}
	properties := sut.Inputs["properties"].(map[string]any)
	var raw bytes.Buffer
	if err := Write(&raw, export.Document); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw.String(), "18446744073709551615") || !strings.Contains(raw.String(), "{$inputs.literal}") {
		t.Fatalf("lost literals: %s", raw.String())
	}
	if len(properties) != 3 {
		t.Fatalf("expected query, header and body inputs: %+v", properties)
	}
	if sut.Steps[0].Headers["Authorization"][0] != "Bearer synthetic" {
		t.Fatal("lost captured auth header extension")
	}
	assertPortableSources(t, export)
	again, err := Generate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	var repeated bytes.Buffer
	_ = Write(&repeated, again.Document)
	if !bytes.Equal(raw.Bytes(), repeated.Bytes()) {
		t.Fatal("export is not deterministic")
	}
	run, captures, err := bundle.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	// Put the shared snapshot in reverse chronological order to exercise sorting.
	if captures[0].TestCase.ID == "retry-1" {
		captures[0], captures[1] = captures[1], captures[0]
	}
	before := append([]*artifact.Artifact(nil), captures...)
	snapshot, err := GenerateSnapshot(run, captures, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(export, snapshot) {
		t.Fatal("snapshot export differs from archive export")
	}
	for i := range before {
		if captures[i] != before[i] {
			t.Fatal("Arazzo reordered the shared capture snapshot")
		}
	}
}

func assertPortableSources(t *testing.T, export *Export) {
	t.Helper()
	dir := t.TempDir()
	for _, file := range export.Files {
		path := filepath.Join(dir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, file.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range export.Document.Sources {
		loader := openapi3.NewLoader()
		loader.IsExternalRefsAllowed = true
		doc, err := loader.LoadFromFile(filepath.Join(dir, filepath.FromSlash(source.URL)))
		if err != nil {
			t.Fatal(err)
		}
		if err := doc.Validate(context.Background()); err != nil {
			t.Fatalf("invalid portable source: %v", err)
		}
	}
}

const frozenSchema = `openapi: 3.0.3
info: {title: Items, version: "1"}
servers: [{url: "https://production.invalid/api"}]
paths:
  /items/{id}:
    get:
      servers: [{url: "https://override.invalid/v2"}]
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
        - {name: limit, in: query, schema: {type: integer}}
        - {name: search, in: query, schema: {type: string}}
        - {name: X-Mode, in: header, schema: {type: string}}
        - {name: session, in: cookie, schema: {type: string}}
      responses:
        '204': {description: No content}
        '200':
          description: OK
          content:
            application/json:
              schema: {type: object}
`

func TestFrozenOpenAPIMapsLocalHostAndOperationServer(t *testing.T) {
	flow := jsonFlow("read", "GET", "http://local.test/v2/items/42?limit=2&search=%24inputs.literal%2Bvalue")
	flow.Request.Headers = artifact.Headers{"X-Mode": {"fast"}, "cookie": {"session=synthetic"}}
	flow.Response = &artifact.HttpMessage{Status: 204}
	root := captureArchive(t, []artifact.SchemaRef{{ID: "items", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: frozenSchema}}}, &artifact.Artifact{TestCase: artifact.TestCase{ID: "test"}, Traffic: []artifact.Flow{flow}})
	export, err := Generate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if export.Document == nil {
		t.Fatal("bodyless JSON REST operation skipped")
	}
	step := export.Document.Workflows[0].Steps[0]
	if !strings.Contains(step.OperationPath, "~1items~1%7Bid%7D/get") {
		t.Fatalf("incorrect path pointer: %s", step.OperationPath)
	}
	if len(step.Parameters) != 5 {
		t.Fatalf("missing path/query/header/cookie inputs: %+v", step.Parameters)
	}
	properties := export.Document.Workflows[0].Inputs["properties"].(map[string]any)
	for _, parameter := range step.Parameters {
		if parameter.Name == "search" {
			name := strings.TrimPrefix(parameter.Value.(string), "$inputs.")
			if properties[name].(map[string]any)["default"] != "$inputs.literal+value" {
				t.Fatalf("query value changed: %+v", properties[name])
			}
		}
	}
	assertPortableSources(t, export)
}

func TestPreparedSourcesKeepFirstUseNumberingAndOneVerifiedSnapshot(t *testing.T) {
	schema := func(id string) artifact.SchemaRef {
		return artifact.SchemaRef{ID: id, Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: API, version: "1"}
paths:
  /` + id + `:
    get:
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: {type: object}
`}}
	}
	flows := []artifact.Flow{jsonFlow("first", "GET", "http://sut/two"), jsonFlow("second", "GET", "http://sut/one")}
	root := captureArchive(t, []artifact.SchemaRef{schema("one"), schema("two"), schema("unused")}, &artifact.Artifact{TestCase: artifact.TestCase{ID: "test"}, Traffic: flows})
	run, captures, err := bundle.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := GenerateSnapshot(run, captures, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(expected.Document.Sources) != 2 || len(expected.Files) != 3 {
		t.Fatalf("source or companion inventory changed: %+v", expected)
	}
	for i, ref := range []artifact.SchemaRef{captures[0].Schemas[1], captures[0].Schemas[0]} {
		if filepath.Base(expected.Document.Sources[i].URL) != filepath.Base(ref.Source.Path) {
			t.Fatalf("sources are not ordered by first use: %+v", expected.Document.Sources)
		}
	}
	prepared := map[[2]string]*sourceSpec{}
	sources := map[string]*sourceSpec{}
	files := map[string][]byte{}
	for i, flow := range flows {
		step, eligible, err := makeStep(captures[0], flow, prepared, sources, files, nil)
		if err != nil || !eligible || step.OperationPath != expected.Document.Workflows[0].Steps[i].OperationPath {
			t.Fatalf("step %d lost its verified source or numbering: %+v, %v", i, step, err)
		}
		if i == 0 {
			// The first request prepares but does not select schema one. Later
			// flows must use those verified bytes, just as selected sources do.
			if err := os.WriteFile(captures[0].Schemas[0].Source.Path, []byte("changed after preparation"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(prepared) != 3 || len(sources) != 2 {
		t.Fatalf("unused source was emitted or preparation was not shared: prepared=%d sources=%d", len(prepared), len(sources))
	}
	for _, file := range expected.Files {
		if !bytes.Equal(files[file.Path], file.Data) {
			t.Fatalf("verified companion changed during generation: %s", file.Path)
		}
	}
	if _, err := GenerateSnapshot(run, captures, ""); err == nil {
		t.Fatal("new generation reused a stale cache instead of detecting corruption")
	}
}

func TestExportRejectsUnrepresentableJSONCaptures(t *testing.T) {
	for name, change := range map[string]func(*artifact.Artifact){
		"incomplete":                 func(a *artifact.Artifact) { a.CaptureIncomplete = true },
		"transport":                  func(a *artifact.Artifact) { a.Traffic[0].TransportError = "connection failed" },
		"missing response":           func(a *artifact.Artifact) { a.Traffic[0].Response = nil },
		"truncated":                  func(a *artifact.Artifact) { a.Traffic[0].Response.Body.Truncated = true },
		"omitted":                    func(a *artifact.Artifact) { a.Traffic[0].Response.Body.Omitted = true },
		"invalid JSON":               func(a *artifact.Artifact) { a.Traffic[0].Response.Body.Text = `{"invalid"` },
		"duplicate JSON key":         func(a *artifact.Artifact) { a.Traffic[0].Response.Body.Text = `{"x":1,"x":2}` },
		"invalid request JSON":       func(a *artifact.Artifact) { a.Traffic[0].Request.Body.Text = `{"invalid"` },
		"duplicate request JSON key": func(a *artifact.Artifact) { a.Traffic[0].Request.Body.Text = `{"x":1,"x":2}` },
		"two JSON values":            func(a *artifact.Artifact) { a.Traffic[0].Response.Body.Text = `{} {}` },
		"invalid UTF8": func(a *artifact.Artifact) {
			a.Traffic[0].Response.Body.Bytes = "Iv8i"
			a.Traffic[0].Response.Body.Text = ""
		},
		"repeated query": func(a *artifact.Artifact) { a.Traffic[0].Request.URL += "?id=1&id=2" },
		"ambiguous content type": func(a *artifact.Artifact) {
			a.Traffic[0].Response.Headers["content-type"] = []string{"application/xml"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			flow := jsonFlow("flow", "POST", "http://sut/items")
			flow.Request.Headers = artifact.Headers{"Content-Type": {"application/json"}}
			flow.Request.Body = &artifact.Body{Text: `{}`}
			capture := &artifact.Artifact{TestCase: artifact.TestCase{ID: "test"}, Traffic: []artifact.Flow{flow}}
			change(capture)
			root := captureArchive(t, nil, capture)
			if _, err := Generate(root, ""); err == nil {
				t.Fatal("unrepresentable evidence was exported")
			}
		})
	}
}

func TestExportSkipsNonJSONAndMergesObservedMediaTypes(t *testing.T) {
	xml := jsonFlow("xml", "GET", "http://sut/xml")
	xml.Response.Headers["Content-Type"] = []string{"application/xml"}
	xml.Response.Body.Text = "<ok/>"
	flow := jsonFlow("one", "POST", "http://sut/items")
	flow.Request.Headers = artifact.Headers{"Content-Type": {"application/json"}}
	flow.Request.Body = &artifact.Body{Text: `null`}
	two := jsonFlow("two", "POST", "http://sut/items")
	two.Request.Headers = artifact.Headers{"Content-Type": {"application/problem+json"}}
	two.Request.Body = &artifact.Body{Text: `{}`}
	root := captureArchive(t, nil, &artifact.Artifact{TestCase: artifact.TestCase{ID: "test"}, Traffic: []artifact.Flow{xml, flow, two}})
	export, err := Generate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Document.Workflows[0].Steps) != 2 {
		t.Fatal("non-JSON included")
	}
	var source map[string]any
	if err := json.Unmarshal(export.Files[0].Data, &source); err != nil {
		t.Fatal(err)
	}
	content := source["paths"].(map[string]any)["/items"].(map[string]any)["post"].(map[string]any)["requestBody"].(map[string]any)["content"].(map[string]any)
	if len(content) != 2 {
		t.Fatalf("request media overwritten: %+v", content)
	}
	assertPortableSources(t, export)
	root = captureArchive(t, nil, &artifact.Artifact{TestCase: artifact.TestCase{ID: "xml"}, Traffic: []artifact.Flow{xml}})
	if export, err := Generate(root, ""); err != nil || export.Document != nil || len(export.Files) != 0 {
		t.Fatalf("non-JSON output: %+v, %v", export, err)
	}
}
