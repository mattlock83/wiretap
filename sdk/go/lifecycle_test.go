package phrond

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type inspectTransport func(*http.Request) (*http.Response, error)

func (f inspectTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCleanupHonorsLongDrainDeadline(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = inspectTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < time.Minute {
			t.Fatalf("cleanup deadline was shortened: %v", deadline)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	runtime := Runtime{BaseURL: "http://phrond.test"}
	if err := runtime.call(ctx, "/v1/teardownTest", map[string]any{"drain_timeout_ms": 60000}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestFinalGoTestOutcomes(t *testing.T) {
	if os.Getenv("PHROND_GO_OUTCOMES") != "" {
		runOutcomeFixture(t)
		return
	}
	result := filepath.Join(t.TempDir(), "outcomes.json")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestFinalGoTestOutcomes$")
	command.Env = append(os.Environ(), "PHROND_GO_OUTCOMES="+result)
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("expected failing fixture: %v\n%s", err, output)
	}
	var outcomes map[string]map[string]string
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("missing results: %v\n%s", err, output)
	}
	if err := json.Unmarshal(data, &outcomes); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fatal", "cleanup"} {
		if outcomes[name]["capture"] != "fail" || outcomes[name]["span"] != "Error" {
			t.Errorf("wrong %s outcome: %v", name, outcomes[name])
		}
	}
	if outcomes["skip"]["capture"] != "skip" || outcomes["validation"]["span"] != "Error" {
		t.Fatalf("wrong skip/validation outcomes: %v", outcomes)
	}
}

// Run as an intentionally failing child test process so t.Fatal and t.Cleanup
// are exercised by the real Go runner without failing the parent suite.
func runOutcomeFixture(t *testing.T) {
	var mu sync.Mutex
	outcomes := map[string]map[string]string{}
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if r.URL.Path == "/v1/setupTest" {
			json.NewEncoder(w).Encode(map[string]any{"session_id": payload["name"]})
			return
		}
		name := filepath.Base(payload["session_id"].(string))
		mu.Lock()
		outcomes[name] = map[string]string{"capture": payload["status"].(string)}
		mu.Unlock()
		status := payload["status"].(string)
		if name == "validation" {
			status = "fail"
		}
		json.NewEncoder(w).Encode(map[string]any{"capture": map[string]any{"testcase": map[string]string{"status": status}}})
	}))
	defer server.Close()
	runtime := Runtime{BaseURL: server.URL, TracerProvider: provider}
	for _, name := range []string{"fatal", "cleanup", "skip", "validation"} {
		t.Run(name, func(t *testing.T) {
			runtime.StartTest(t, TestOptions{GenerateSchema: true})
			switch name {
			case "fatal":
				t.Fatal("original failure")
			case "cleanup":
				t.Cleanup(func() { t.Error("cleanup failure") })
			case "skip":
				t.Skip("intentional skip")
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	for _, span := range recorder.Ended() {
		outcomes[filepath.Base(span.Name())]["span"] = span.Status().Code.String()
	}
	data, _ := json.Marshal(outcomes)
	if err := os.WriteFile(os.Getenv("PHROND_GO_OUTCOMES"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
