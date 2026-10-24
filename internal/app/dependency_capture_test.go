package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/core"
	"github.com/mattlock83/wiretap/internal/coverage"
)

const dependencyCaptureSUTSchema = `openapi: 3.0.3
info:
  title: Orders API
  version: 1.0.0
paths:
  /orders:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [sku]
              properties:
                sku: {type: string}
      responses:
        '200':
          description: accepted
          content:
            application/json:
              schema:
                type: object
                required: [accepted]
                properties:
                  accepted: {type: boolean}
`

const dependencyCaptureInventorySchema = `openapi: 3.0.3
info:
  title: Inventory API
  version: 1.0.0
paths:
  /api/items:
    post:
      parameters:
        - {name: fixed, in: query, schema: {type: string}}
        - {name: include, in: query, schema: {type: string}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [sku]
              properties:
                sku: {type: string}
      responses:
        '200':
          description: inventory
          content:
            application/json:
              schema:
                type: object
                required: [available]
                properties:
                  available: {type: boolean}
`

func TestExternalDependencyCaptureCorrelatesBothTrafficDirections(t *testing.T) {
	var observedTrace string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/items" || r.URL.Query().Get("fixed") != "yes" || r.URL.Query().Get("include") != "stock" {
			t.Errorf("unexpected forwarded dependency URL: %s", r.URL.String())
		}
		if r.Header.Get("X-Wiretap-Session-Id") != "" {
			t.Error("capture session header leaked to the external dependency")
		}
		if r.Header.Get("X-Consumer") != "orders" {
			t.Errorf("consumer header was not forwarded: %#v", r.Header)
		}
		observedTrace = r.Header.Get("traceparent")
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"sku":"widget"}` {
			t.Errorf("forwarded dependency body = %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"available":true}`))
	}))
	defer upstream.Close()

	var dependencyURL string
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, _ := http.NewRequest(http.MethodPost, dependencyURL+"/items?include=stock", strings.NewReader(`{"sku":"widget"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Consumer", "orders")
		request.Header.Set("traceparent", r.Header.Get("traceparent"))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer sut.Close()

	control, appInstance := dependencyCaptureRuntime(t)
	runtime := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{
		"proxy": map[string]any{"target_url": sut.URL},
	})
	proxyURL := runtime["sut_url"].(string)
	dependencyURL = proxyURL + "/__wiretap/dep/inventory"

	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
		"name":          "creates an order through an existing mock",
		"trace_context": map[string]string{"traceparent": "00-11111111111111111111111111111111-2222222222222222-00"},
		"sut_base_url":  sut.URL,
		"schemas": []artifact.SchemaRef{{ID: "orders-api", Type: "openapi", Source: artifact.SchemaSource{
			Type: "inline", Content: dependencyCaptureSUTSchema,
		}}},
		"dependencies": []dependencyRegistration{{
			AssetID: "inventory", AssetName: "Inventory Service", UpstreamURL: upstream.URL + "/api?fixed=yes",
			Schema: &artifact.SchemaRef{ID: "inventory-api", Type: "openapi", Source: artifact.SchemaSource{
				Type: "inline", Content: dependencyCaptureInventorySchema,
			}},
		}},
	})
	if got := setup["dependencies"].(map[string]any)["inventory"]; got != dependencyURL {
		t.Fatalf("dependency capture URL = %v, want %s", got, dependencyURL)
	}

	response, err := http.Post(proxyURL+"/orders", "application/json", strings.NewReader(`{"sku":"widget"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SUT response status = %d", response.StatusCode)
	}

	finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{
		"session_id": setup["session_id"], "status": "pass",
	})
	art := dependencyArtifact(t, finished)
	if len(art.Traffic) != 2 {
		t.Fatalf("captured %d flows, want inbound and dependency: %#v", len(art.Traffic), art.Traffic)
	}
	var inbound, downstream *artifact.Flow
	for i := range art.Traffic {
		switch art.Traffic[i].Channel {
		case "test_to_sut":
			inbound = &art.Traffic[i]
		case "sut_to_dependency":
			downstream = &art.Traffic[i]
		}
	}
	if inbound == nil || downstream == nil {
		t.Fatalf("expected both traffic channels: %#v", art.Traffic)
	}
	if downstream.AssetID != "inventory" || downstream.ParentFlowID != inbound.ID {
		t.Fatalf("dependency identity or causality missing: %#v", downstream)
	}
	if inbound.TraceID == "" || downstream.TraceID != inbound.TraceID || !strings.Contains(observedTrace, inbound.TraceID) {
		t.Fatalf("trace correlation failed: inbound=%q dependency=%q upstream=%q", inbound.TraceID, downstream.TraceID, observedTrace)
	}
	if downstream.Request.HTTP.URL != upstream.URL+"/api/items?fixed=yes&include=stock" {
		t.Fatalf("recorded dependency URL = %q", downstream.Request.HTTP.URL)
	}
	if downstream.Request.HTTP.Body.Text != `{"sku":"widget"}` || downstream.Response.HTTP.Body.Text != `{"available":true}` {
		t.Fatalf("dependency payload capture failed: %#v", downstream)
	}
	if art.SchemaValidation == nil || art.SchemaValidation.Status != "pass" {
		t.Fatalf("strict inbound/dependency schema validation failed: %#v", art.SchemaValidation)
	}
	var dependencySchema *artifact.SchemaRef
	for i := range art.Schemas {
		if art.Schemas[i].Role == "dependency" {
			dependencySchema = &art.Schemas[i]
		}
	}
	if dependencySchema == nil || dependencySchema.AssetID != "inventory" || dependencySchema.AssetName != "Inventory Service" || dependencySchema.AssetSHA == "" {
		t.Fatalf("dependency schema identity missing: %#v", art.Schemas)
	}
	if appInstance.proxyComponent == nil {
		t.Fatal("expected dependency capture to reuse the existing proxy")
	}
	report, err := coverage.Generate(t.Context(), appInstance.currentBundle().Path(), "Orders API", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Provided.Specs) != 1 || len(report.Dependent.Specs) != 1 || report.Dependent.Specs[0].AssetID != "inventory" {
		t.Fatalf("provided/dependency coverage missing: %#v", report)
	}
	if len(report.TestCases) != 1 || len(report.TestCases[0].Provided) != 1 || len(report.TestCases[0].Dependent) != 1 {
		t.Fatalf("per-test bidirectional coverage missing: %#v", report.TestCases)
	}
}

func TestExplicitDependencyCaptureSessionsStayIsolated(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("first")) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("second")) }))
	defer second.Close()
	control, _ := dependencyCaptureRuntime(t)
	runtime := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})

	createSession := func(name, target string) map[string]any {
		return postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
			"name": name, "explicit_session": true, "schema_required": false,
			"dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: target, Schema: permissiveDependencySchema()}},
		})
	}
	firstSession := createSession("first parallel test", first.URL)
	secondSession := createSession("second parallel test", second.URL)
	for _, scenario := range []struct {
		session map[string]any
		want    string
	}{{firstSession, "first"}, {secondSession, "second"}} {
		captureURL := scenario.session["dependencies"].(map[string]any)["inventory"].(string)
		response, err := http.Get(captureURL + "/items")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != scenario.want {
			t.Fatalf("session response = %q, want %q", body, scenario.want)
		}
	}
	response, err := http.Get(runtime["sut_url"].(string) + "/__wiretap/dep/inventory/items")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unscoped parallel dependency response = %d, want 409", response.StatusCode)
	}
	for _, session := range []map[string]any{firstSession, secondSession} {
		art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{
			"session_id": session["session_id"], "status": "pass",
		}))
		if len(art.Traffic) != 1 || art.Traffic[0].Channel != "sut_to_dependency" || art.Traffic[0].AssetID != "inventory" {
			t.Fatalf("session leaked or lost dependency traffic: %#v", art.Traffic)
		}
	}
}

func TestDependencyUpstreamFailureRecords502AndTransportError(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	upstreamURL := upstream.URL
	upstream.Close()
	control, _ := dependencyCaptureRuntime(t)
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
		"name": "dependency unavailable", "schema_required": false,
		"dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: upstreamURL, Schema: permissiveDependencySchema()}},
	})
	response, err := http.Get(session["dependencies"].(map[string]any)["inventory"].(string) + "/items")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("dependency failure status = %d, want 409", response.StatusCode)
	}
	art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{
		"session_id": session["session_id"], "status": "fail",
	}))
	if len(art.Traffic) != 1 || art.Traffic[0].Response.HTTP.Status != http.StatusBadGateway || art.Traffic[0].TransportError == "" {
		t.Fatalf("dependency failure was not captured truthfully: %#v", art.Traffic)
	}
}

func TestDependencySchemaViolationsFailTheOwningTest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer upstream.Close()
	control, _ := dependencyCaptureRuntime(t)
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
		"name": "dependency contract drift", "schema_required": false,
		"dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: upstream.URL, Schema: permissiveDependencySchema()}},
	})
	response, err := http.Get(session["dependencies"].(map[string]any)["inventory"].(string) + "/items")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	art := dependencyArtifact(t, postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{
		"session_id": session["session_id"], "status": "pass",
	}))
	if art.TestCase.Status != "fail" || art.SchemaValidation == nil || art.SchemaValidation.Status != "fail" {
		t.Fatalf("dependency schema violation did not fail the test: %#v", art)
	}
	if len(art.SchemaValidation.Errors) == 0 || art.SchemaValidation.Errors[0].Channel != "sut_to_dependency" {
		t.Fatalf("dependency schema issue lost channel attribution: %#v", art.SchemaValidation)
	}
}

func TestDependencyCaptureAlwaysRequiresItsOwnReadableSchema(t *testing.T) {
	control, _ := dependencyCaptureRuntime(t)
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	payload, _ := json.Marshal(map[string]any{
		"name": "missing dependency schema", "schema_required": false,
		"dependencies": []dependencyRegistration{{AssetID: "inventory", UpstreamURL: "http://127.0.0.1:9090"}},
	})
	response, err := http.Post(control.URL+"/v1/setupTest", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "requires an OpenAPI schema") {
		t.Fatalf("missing dependency schema status = %d, body = %s", response.StatusCode, body)
	}
}

func TestDependencyCausalityFallsBackToRequestTiming(t *testing.T) {
	started := time.Now()
	flows := []artifact.Flow{
		{ID: "flow-1", Channel: "sut_to_dependency", StartedAt: started.Add(time.Millisecond)},
		{ID: "flow-2", Channel: "test_to_sut", TraceID: "inbound", StartedAt: started, DurationMS: 5},
	}
	linkCausality(flows)
	if flows[0].ParentFlowID != "flow-2" {
		t.Fatalf("timing-based parent = %q, want flow-2", flows[0].ParentFlowID)
	}
}

func permissiveDependencySchema() *artifact.SchemaRef {
	return &artifact.SchemaRef{ID: "inventory-api", Type: "openapi", Source: artifact.SchemaSource{
		Type: "inline",
		Content: `openapi: 3.0.3
info: {title: Inventory, version: 1.0.0}
paths:
  /items:
    get:
      responses:
        '200': {description: ok}
        '502': {description: unavailable}
`,
	}}
}

func dependencyCaptureRuntime(t *testing.T) (*httptest.Server, *App) {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Proxy.Mode = "passthrough"
	cfg.Artifact.OutputDir = t.TempDir()
	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(appInstance.routes())
	t.Cleanup(control.Close)
	t.Cleanup(func() {
		if appInstance.proxyComponent != nil {
			_ = appInstance.proxyComponent.Stop(t.Context())
		}
	})
	return control, appInstance
}

func postDependencyJSON(t *testing.T, baseURL, path string, payload any) map[string]any {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d: %#v", path, response.StatusCode, decoded)
	}
	return decoded
}

func dependencyArtifact(t *testing.T, response map[string]any) artifact.Artifact {
	t.Helper()
	data, err := json.Marshal(response["capture"])
	if err != nil {
		t.Fatal(err)
	}
	var doc artifact.Artifact
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
