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
