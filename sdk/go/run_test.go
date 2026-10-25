package wiretap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunPackagePreservesExitAndFinishesAfterTests(t *testing.T) {
	for _, tc := range []struct {
		name       string
		testCode   int
		finishCode int
		want       int
	}{
		{"pass", 0, 200, 0},
		{"test failure", 7, 200, 7},
		{"export failure", 0, 409, 1},
		{"both failures", 7, 409, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := t.TempDir()
			coveragePath := filepath.Join(bundle, "custom exports", "report.json")
			postmanPath := filepath.Join(bundle, "custom exports", "orders.postman.json")
			testsDone, finishCount := false, 0
			var applicationURL string
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				finishCount++
				if r.URL.Path != "/v1/finishRun" || !testsDone {
					t.Error("capture finalized before tests completed")
				}
				response, err := http.Get(applicationURL)
				if err != nil {
					t.Errorf("application stopped before finalization: %v", err)
				} else {
					response.Body.Close()
				}
				w.WriteHeader(tc.finishCode)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "complete", "run_id": "run-test", "bundle_path": bundle,
					"outputs": OutputResult{Coverage: map[string]string{"json": coveragePath}, Postman: []string{postmanPath}}})
			}))
			defer control.Close()
			options := RunOptions{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "app") })}
			start := func(_ context.Context, _ RunOptions) (*managedRuntime, error) {
				owner := &managedRuntime{runtime: Runtime{BaseURL: control.URL}, options: RunOptions{FinishTimeout: time.Second}, closed: make(chan struct{})}
				owner.info.RunID, owner.info.BundlePath = "run-test", bundle
				return owner, nil
			}
			log, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			previousStderr := os.Stderr
			os.Stderr = log
			t.Cleanup(func() { os.Stderr = previousStderr })
			got := runPackage(func() int {
				activeCapture.Lock()
				applicationURL = activeCapture.current.sutURL
				activeCapture.Unlock()
				if nested := runPackage(func() int { t.Error("nested suite ran"); return 0 }, RunOptions{}, "nested", start); nested != 1 {
					t.Errorf("nested Run returned %d", nested)
				}
				testsDone = true
				return tc.testCode
			}, options, "example.com/tests", start)
			os.Stderr = previousStderr
			data, err := os.ReadFile(log.Name())
			if err != nil {
				t.Fatal(err)
			}
			output := string(data)
			if strings.Count(output, "Wiretap archive: "+bundle+"\n") != 1 {
				t.Fatalf("archive diagnostic missing or duplicated: %s", output)
			}
			if tc.finishCode == http.StatusOK {
				for _, line := range []string{"Wiretap coverage (json): " + coveragePath, "Wiretap Postman: " + postmanPath} {
					if strings.Count(output, line+"\n") != 1 {
						t.Fatalf("completed export path missing or duplicated: %s", output)
					}
				}
			} else if strings.Contains(output, "Wiretap coverage") || strings.Contains(output, "Wiretap Postman") || !strings.Contains(output, "Wiretap finalization failed:") {
				t.Fatalf("failed finalization must retain its error without a successful export summary: %s", output)
			}
			if got != tc.want || finishCount != 1 {
				t.Fatalf("exit = %d, finishes = %d; want %d, 1", got, finishCount, tc.want)
			}
			if response, err := http.Get(applicationURL); err == nil {
				response.Body.Close()
				t.Fatal("owned application still running after Run returned")
			}
			activeCapture.Lock()
			defer activeCapture.Unlock()
			if activeCapture.running || activeCapture.current != nil {
				t.Fatal("Run left an active package behind")
			}
		})
	}
}

func TestRunPackageStartupFailureDoesNotRunTests(t *testing.T) {
	code := runPackage(func() int { t.Fatal("tests ran without capture"); return 0 }, RunOptions{}, "example.com/tests",
		func(context.Context, RunOptions) (*managedRuntime, error) { return nil, errors.New("missing binary") })
	if code != 1 {
		t.Fatalf("startup failure returned %d", code)
	}
}

func TestManagedIdentityIsStableAndCountsParallelAttempts(t *testing.T) {
	state := &packageCapture{pkg: "example.com/orders", attempt: make(map[string]int), sutURL: "http://app", options: RunOptions{ExplicitSession: true}}
	const count = 24
	ordinals := make(chan int, count)
	var wg sync.WaitGroup
	for range count {
		wg.Go(func() {
			opts := state.testOptions("TestOrders", TestOptions{})
			if opts.Identity.ID != "go:example.com/orders:TestOrders" || !opts.ExplicitSession || opts.SUTBaseURL != state.sutURL {
				t.Errorf("incorrect defaults: %+v, identity %+v", opts, opts.Identity)
			}
			ordinals <- *opts.Identity.AttemptOrdinal
		})
	}
	wg.Wait()
	close(ordinals)
	seen := make(map[int]bool)
	for ordinal := range ordinals {
		if ordinal < 1 || ordinal > count || seen[ordinal] {
			t.Fatalf("duplicate or invalid ordinal %d", ordinal)
		}
		seen[ordinal] = true
	}
	longName := strings.Repeat("long-subtest/", 100)
	first := state.testOptions(longName, TestOptions{}).Identity.ID
	if len(first) > 256 || first != state.testOptions(longName, TestOptions{}).Identity.ID {
		t.Fatal("long test identities must be bounded and stable")
	}
	ordinal := 9
	identity := &TestIdentity{ID: "custom", Variant: "variant", AttemptOrdinal: &ordinal}
	explicit := state.testOptions("TestOrders", TestOptions{Identity: identity, SUTBaseURL: "http://override"})
	if explicit.Identity == identity || *explicit.Identity != *identity || explicit.SUTBaseURL != "http://override" {
		t.Fatal("explicit identity or target was not preserved independently")
	}
	next := state.testOptions("TestOrders", TestOptions{Identity: &TestIdentity{ID: "custom", Variant: "variant"}})
	if *next.Identity.AttemptOrdinal != 10 || *identity.AttemptOrdinal != 9 {
		t.Fatal("automatic attempts must follow explicit ordinals without changing caller options")
	}
}
