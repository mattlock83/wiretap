package wiretap

import (
	"encoding/json"
	"fmt"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestApplicationMetadataIsForwardedOrLeftToRuntimeDefaults(t *testing.T) {
	setups := make(chan map[string]any, 2)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/setupTest" {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			setups <- payload
			json.NewEncoder(w).Encode(map[string]any{"session_id": "application-test"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"capture": map[string]any{"testcase": map[string]string{"status": "pass"}}})
	}))
	defer core.Close()
	runtime := Runtime{BaseURL: core.URL}
	t.Run("explicit application", func(t *testing.T) {
		runtime.StartTest(t, TestOptions{Application: &Application{Name: "Order service", ID: "orders", Description: "Accepts customer orders"}})
	})
	want := map[string]any{"name": "Order service", "id": "orders", "description": "Accepts customer orders"}
	if payload := <-setups; !reflect.DeepEqual(payload["application"], want) {
		t.Fatalf("application metadata changed: %#v", payload["application"])
	}
	t.Run("runtime default", func(t *testing.T) {
		runtime.StartTest(t, TestOptions{})
	})
	payload := <-setups
	if _, exists := payload["application"]; exists {
		t.Fatalf("omitted application should inherit runtime defaults: %#v", payload["application"])
	}
}

func TestStartTestPreservesNilEmptyAndPopulatedOverrides(t *testing.T) {
	setups := make(chan map[string]json.RawMessage, 1)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/setupTest" {
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			setups <- payload
			fmt.Fprint(w, `{"session_id":"overrides-test"}`)
			return
		}
		fmt.Fprint(w, `{"capture":{"testcase":{"status":"pass"}}}`)
	}))
	defer core.Close()
	runtime := Runtime{BaseURL: core.URL}
	schema := Schema{ID: "orders", Type: "openapi", Source: SchemaSource{Type: "file", Path: "orders.yaml"}}
	dependency := Dependency{AssetID: "inventory", UpstreamURL: "http://inventory"}
	for _, tc := range []struct {
		name    string
		options TestOptions
		present bool
		length  int
	}{
		{name: "nil inherits"},
		{name: "empty clears", options: TestOptions{Schemas: []Schema{}, Dependencies: []Dependency{}}, present: true},
		{name: "populated replaces", options: TestOptions{Schemas: []Schema{schema}, Dependencies: []Dependency{dependency}}, present: true, length: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime.StartTest(t, tc.options)
			payload := <-setups
			for _, key := range []string{"schemas", "dependencies"} {
				value, present := payload[key]
				if present != tc.present {
					t.Fatalf("%s presence = %v, want %v: %s", key, present, tc.present, value)
				}
				if !present {
					continue
				}
				var list []json.RawMessage
				if err := json.Unmarshal(value, &list); err != nil || list == nil || len(list) != tc.length {
					t.Fatalf("%s should contain %d entries: %s (%v)", key, tc.length, value, err)
				}
			}
			if len(payload["name"]) == 0 || len(payload["trace_context"]) == 0 {
				t.Fatalf("setup payload lost test identity or tracing: %#v", payload)
			}
		})
	}
}

func TestParallelTestsUseExistingClientsWithSeparateRoots(t *testing.T) {
	var mu sync.Mutex
	roots := map[string]string{}
	completed := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header)))
		mu.Lock()
		root := roots[r.URL.Path]
		mu.Unlock()
		if !sc.IsValid() || sc.TraceID().String() != root || sc.IsSampled() {
			t.Errorf("wrong request context: %s root %s", r.Header.Get("traceparent"), root)
		}
		io.WriteString(w, "ok")
	}))
	defer target.Close()
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/v1/setupTest" {
			name := payload["name"].(string)
			carrier := propagation.MapCarrier{"traceparent": payload["trace_context"].(map[string]any)["traceparent"].(string)}
			sc := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(r.Context(), carrier))
			roots["/"+name] = sc.TraceID().String()
			json.NewEncoder(w).Encode(map[string]any{"session_id": name, "sut_url": target.URL})
		} else {
			completed++
			json.NewEncoder(w).Encode(map[string]any{"capture": map[string]any{"testcase": map[string]string{"status": "pass"}}})
		}
	}))
	defer core.Close()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	runtime := Runtime{BaseURL: core.URL, TracerProvider: provider}
	original := target.Client()
	transport := original.Transport
	t.Run("group", func(t *testing.T) {
		for i := 0; i < 4; i++ {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				session := runtime.StartTest(t, TestOptions{SchemaRequired: new(bool), ExplicitSession: true})
				client := session.HTTPClient(original)
				res, err := client.Get(session.SUTURL + "/" + t.Name())
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
			})
		}
	})
	if original.Transport != transport {
		t.Fatal("original HTTP client was mutated")
	}
	if completed != 4 || len(roots) != 4 {
		t.Fatalf("wrong lifecycle: %d %v", completed, roots)
	}
	seen := map[string]bool{}
	for _, root := range roots {
		if seen[root] {
			t.Fatal("test roots reused")
		}
		seen[root] = true
	}
}

func TestFinishRunWithOutputsPreservesAvailablePathsOnExportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Outputs *OutputOptions `json:"outputs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Outputs == nil || !payload.Outputs.Postman || !payload.Outputs.Arazzo {
			t.Error("missing output selection")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"bundle_path":"/capture/run","outputs":{"coverage":{"json":"/capture/run/exports/coverage.json"},"postman":[],"arazzo":"/capture/run/exports/arazzo/arazzo.json","arazzo_files":["/capture/run/exports/arazzo/schemas/orders.json"],"errors":[{"format":"postman","message":"incomplete capture"}]}}`)
	}))
	defer server.Close()
	runtime := Runtime{BaseURL: server.URL}
	result, err := runtime.FinishRunWithOutputs(t.Context(), &OutputOptions{Coverage: []string{"json"}, Postman: true, Arazzo: true})
	if err == nil || result.BundlePath != "/capture/run" || result.Outputs == nil || result.Outputs.Coverage["json"] == "" || result.Outputs.Arazzo != "/capture/run/exports/arazzo/arazzo.json" || len(result.Outputs.ArazzoFiles) != 1 || len(result.Outputs.Errors) != 1 {
		t.Fatalf("lost export error or available outputs: %+v %v", result, err)
	}
}
