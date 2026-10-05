package explore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/bundlereport"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/hooks"
)

const testSpec = `openapi: 3.0.3
info: {title: Orders, version: '1'}
paths:
  /orders:
    post:
      parameters:
        - {name: count, in: query, required: true, schema: {type: integer, minimum: 1, maximum: 5}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [user_id, quantity, name, mode]
              properties:
                user_id: {type: string}
                quantity: {type: integer, minimum: 1, maximum: 10}
                name: {type: string, minLength: 2, maxLength: 8}
                mode: {type: string, enum: [fast, slow]}
                code: {type: string, pattern: '^[A-Z]+$'}
                items: {type: array, minItems: 1, maxItems: 3, items: {type: integer}}
      responses:
        '201':
          description: created
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: string}}}
        '400':
          description: rejected
          content:
            application/json:
              schema: {type: object, required: [error], properties: {error: {type: string}}}
        '401': {description: unauthorized}
        '500': {description: failed}
`
const seedBody = `{"user_id":"seed-user","quantity":2,"name":"hello","mode":"fast","code":"AB","items":[1,2]}`

func makeSeed(t *testing.T, schema string, flows []artifact.Flow) (string, *artifact.Artifact, *spec) {
	t.Helper()
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Configure(bundle.Identity{APIID: "orders", SUTTargetKind: "service"}); err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "seed", Name: "creates an order", StartedAt: time.Now().UTC(), Status: "pass"}
	refs, err := w.Register(tc, []artifact.SchemaRef{{ID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: schema}}})
	if err != nil {
		t.Fatal(err)
	}
	if flows == nil {
		flows = []artifact.Flow{{ID: "request-1", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://old-provider/orders?count=2", Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: seedBody}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 201, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"id":"old-order"}`}}}}}
	}
	a := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: refs, Traffic: flows}
	if _, err = w.Commit(a); err != nil {
		t.Fatal(err)
	}
	if err = w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	s, err := loadSpec(refs[0])
	if err != nil {
		t.Fatal(err)
	}
	return w.Path(), a, s
}

func TestGenerateIsolatedInvalidInputs(t *testing.T) {
	_, a, s := makeSeed(t, testSpec, nil)
	steps, err := seedSteps(a)
	if err != nil {
		t.Fatal(err)
	}
	mutations, _, err := generate(s, steps, "orders", "revision", []int{400})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range mutations {
		r := steps[m.Step].Request.clone()
		if err = m.apply(&r, s); err != nil {
			t.Fatal(err)
		}
		if !exactViolation(s.validate(r), m.Target.Location, m.Target.Constraint) {
			t.Fatalf("not isolated: %+v", m)
		}
		seen[m.Target.Location+":"+m.Target.Constraint] = true
	}
	for _, key := range []string{"/body:required", "/body/quantity:required", "/body/quantity:type", "/body/quantity:minimum", "/body/quantity:maximum", "/body/name:minLength", "/body/name:maxLength", "/body/mode:enum", "/body/code:pattern", "/body/items:minItems", "/body/items:maxItems", "/query/count:required", "/query/count:type", "/query/count:minimum", "/query/count:maximum"} {
		if !seen[key] {
			t.Errorf("missing %s", key)
		}
	}
	second, _, _ := generate(s, steps, "orders", "revision", []int{400})
	x, _ := json.Marshal(mutations)
	y, _ := json.Marshal(second)
	if !bytes.Equal(x, y) {
		t.Fatal("generation is nondeterministic")
	}
}

func TestEnumMutationCannotHideOtherViolations(t *testing.T) {
	for _, s := range []*openapi3.Schema{
		{Type: &openapi3.Types{"boolean"}, Enum: []any{true, false}},
		{Type: &openapi3.Types{"string"}, Enum: []any{"A"}, Pattern: "^A$"},
	} {
		for _, v := range values(s, nil) {
			if v.constraint == "enum" {
				t.Fatalf("no isolated enum witness exists, got %#v", v.value)
			}
		}
	}
}

func TestExploreRejectionsAndBundleReports(t *testing.T) {
	source, _, s := makeSeed(t, testSpec, nil)
	original, _ := os.ReadFile(filepath.Join(source, "bundle.json"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := request{Method: r.Method, URL: "http://provider" + r.URL.String(), Headers: r.Header, Body: body}
		w.Header().Set("Content-Type", "application/json")
		if s.validate(req) != nil {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid input"}`)
		} else {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"fresh-order"}`)
		}
	}))
	defer server.Close()
	result, err := Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: legacyConfig{MaxCases: 5}})
	if err != nil || result.Passed != 5 || result.Failed != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	report, err := bundlereport.Rebuild(result.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	r := report.Contracts.Rejections
	if r == nil || r.Covered != 5 || r.NotCovered != result.Generated-5 {
		t.Fatalf("bad rejection coverage %+v", r)
	}
	for _, sp := range report.Contracts.Specs {
		for _, target := range sp.Targets {
			if target.Phase == "request" {
				for _, e := range target.Evidence {
					if strings.HasPrefix(e.AttemptID, "negative-") && e.Result == "covered" {
						t.Fatal("invalid request earned valid coverage")
					}
				}
			}
		}
	}
	for file, heading := range map[string]string{"docs/contract-coverage.md": "Provider rejection coverage", "docs/viewer.html": "Invalid-input coverage"} {
		data, _ := os.ReadFile(filepath.Join(result.Bundle, file))
		if !strings.Contains(string(data), heading) {
			t.Fatalf("%s missing rejection results", file)
		}
	}
	after, _ := os.ReadFile(filepath.Join(source, "bundle.json"))
	if !bytes.Equal(original, after) {
		t.Fatal("source bundle changed")
	}
	if _, err := os.Stat(filepath.Join(result.Bundle, "hooks")); !os.IsNotExist(err) {
		t.Fatal("hook code copied into bundle")
	}
}

func TestUnexpectedResponsesDoNotEarnRejectionCoverage(t *testing.T) {
	for _, status := range []int{200, 201, 302, 401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			source, _, s := makeSeed(t, testSpec, nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				req := request{Method: r.Method, URL: "http://p" + r.URL.String(), Headers: r.Header, Body: body}
				w.Header().Set("Content-Type", "application/json")
				if s.validate(req) == nil {
					w.WriteHeader(201)
					fmt.Fprint(w, `{"id":"order"}`)
				} else {
					w.WriteHeader(status)
					fmt.Fprint(w, `{"id":"order","error":"bad"}`)
				}
			}))
			defer server.Close()
			result, err := Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: legacyConfig{MaxCases: 1}})
			if err != nil || result.Failed != 1 {
				t.Fatalf("%+v %v", result, err)
			}
			report, err := coverage.Generate(context.Background(), result.Bundle, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if report.Contracts.Rejections.Covered != 0 || report.Contracts.Rejections.Failed != 1 {
				t.Fatal("incorrect rejection credit")
			}
		})
	}
}

func TestBaselineMustSucceed(t *testing.T) {
	source, _, _ := makeSeed(t, testSpec, nil)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(401) }))
	defer server.Close()
	r, err := Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir()})
	if err == nil || calls != 1 || r.Executed != 0 {
		t.Fatalf("%+v %v calls=%d", r, err, calls)
	}
	run, _, e := bundle.Load(r.Bundle)
	if e != nil || run.RunState != "interrupted" {
		t.Fatalf("%+v %v", run, e)
	}
}

// This real executable exercises the same stdin/stdout protocol used by Bash,
// PowerShell and other project-owned commands, without requiring a test SDK.
func TestLifecycleHelper(t *testing.T) {
	if os.Getenv("WIRETAP_EXPLORE_HELPER") != "1" {
		return
	}
	var in hookInput
	if json.NewDecoder(os.Stdin).Decode(&in) != nil {
		os.Exit(10)
	}
	log, _ := os.OpenFile(os.Getenv("WIRETAP_EVENT_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	fmt.Fprintln(log, in.Event+" "+in.CaseID)
	log.Close()
	out := hooks.Output{}
	if os.Getenv("WIRETAP_FAIL_EVENT") == in.Event {
		os.Exit(11)
	}
	admin := os.Getenv("WIRETAP_TEST_ADMIN_URL")
	switch in.Event {
	case "before_request":
		if os.Getenv("WIRETAP_SIGNING") == "1" {
			raw, err := base64.StdEncoding.DecodeString(in.Request.BodyBase64)
			if err != nil {
				os.Exit(15)
			}
			out.Headers = map[string]string{"X-Body-Hash": fmt.Sprintf("%x", sha256.Sum256(raw))}
		}
	case "before_case":
		resp, err := http.Post(admin+"/_fixture", "application/json", nil)
		if err != nil {
			os.Exit(12)
		}
		json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
	case "check_case":
		resp, err := http.Get(admin + "/_state")
		if err != nil {
			os.Exit(13)
		}
		var state struct{ Rows int }
		json.NewDecoder(resp.Body).Decode(&state)
		resp.Body.Close()
		want := 0
		if in.Mode == "baseline" {
			want = 1
		}
		out.Checks = []hooks.Check{{Name: "database effect", Passed: state.Rows == want}}
		if os.Getenv("WIRETAP_FORCE_PROPERTY_FAIL") == "1" && in.Mode == "rejection" {
			out.Checks[0].Passed = false
		}
	case "after_case":
		resp, err := http.Post(admin+"/_cleanup", "application/json", nil)
		if err != nil {
			os.Exit(14)
		}
		resp.Body.Close()
	}
	json.NewEncoder(os.Stdout).Encode(out)
	os.Exit(0)
}

func TestRequestHookSignsExactBytesAndDoesNotReuseURLCredentials(t *testing.T) {
	_, a, s := makeSeed(t, testSpec, nil)
	a.Traffic[0].Request.HTTP.URL = "http://old-user:old-password@old-provider/orders?count=2"
	a.Traffic[0].Request.HTTP.Body.Text = strings.ReplaceAll(seedBody, ",", ",\n  ")
	source, _, _ := makeSeed(t, testSpec, a.Traffic)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Body-Hash") != fmt.Sprintf("%x", sha256.Sum256(raw)) {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if s.validate(request{Method: r.Method, URL: "http://p" + r.URL.String(), Headers: r.Header, Body: raw}) == nil {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"new"}`)
		} else {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid"}`)
		}
	}))
	defer server.Close()
	t.Setenv("WIRETAP_EXPLORE_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("WIRETAP_SIGNING", "1")
	t.Setenv("WIRETAP_EVENT_LOG", filepath.Join(t.TempDir(), "events"))
	command := &legacyHookCommand{Command: []string{os.Args[0], "-test.run=TestLifecycleHelper"}}
	result, err := Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: legacyConfig{MaxCases: 3, Hooks: legacyHookLifecycle{BeforeRequest: command}}})
	if err != nil || result.Passed != 3 {
		t.Fatalf("signing failed: %+v %v", result, err)
	}
}

func TestExternalHooksFreshAuthFixturesAndCleanup(t *testing.T) {
	source, _, s := makeSeed(t, testSpec, nil)
	var mu sync.Mutex
	users, rows, serial, cleanups := 0, 0, 0, 0
	token := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/_fixture":
			serial++
			users++
			rows = 0
			token = fmt.Sprintf("Bearer secret-%d", serial)
			json.NewEncoder(w).Encode(hooks.Output{Bindings: map[string]any{"user_id": fmt.Sprintf("user-%d", serial)}, Headers: map[string]string{"Authorization": token}})
			return
		case "/_cleanup":
			users = 0
			rows = 0
			cleanups++
			fmt.Fprint(w, `{}`)
			return
		case "/_state":
			json.NewEncoder(w).Encode(map[string]int{"Rows": rows})
			return
		}
		if users != 1 || r.Header.Get("Authorization") != token {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		req := request{Method: r.Method, URL: "http://p" + r.URL.String(), Headers: r.Header, Body: body}
		if s.validate(req) != nil {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid input"}`)
		} else {
			rows++
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"order"}`)
		}
	}))
	defer server.Close()
	t.Setenv("WIRETAP_EXPLORE_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("WIRETAP_TEST_ADMIN_URL", server.URL)
	log := filepath.Join(t.TempDir(), "events")
	t.Setenv("WIRETAP_EVENT_LOG", log)
	command := &legacyHookCommand{Command: []string{os.Args[0], "-test.run=TestLifecycleHelper"}}
	cfg := legacyConfig{MaxCases: 3, Hooks: legacyHookLifecycle{BeforeRun: command, BeforeCase: command, BeforeRequest: command, CheckCase: command, AfterCase: command, AfterRun: command}, Bindings: []Binding{{Name: "user_id", In: "body", Path: "/user_id"}}}
	result, err := Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: cfg})
	if err != nil || result.Passed != 3 {
		t.Fatalf("%+v %v", result, err)
	}
	mu.Lock()
	if users != 0 || cleanups != 4 || serial != 4 {
		t.Errorf("users=%d cleanups=%d setups=%d", users, cleanups, serial)
	}
	mu.Unlock()
	events, _ := os.ReadFile(log)
	if !strings.HasPrefix(string(events), "before_run \nbefore_case baseline\n") || !strings.HasSuffix(string(events), "after_run \n") {
		t.Fatal(string(events))
	}
	filepath.WalkDir(result.Bundle, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			data, _ := os.ReadFile(path)
			if bytes.Contains(data, []byte("Bearer secret-")) {
				t.Errorf("credential leaked in %s", path)
			}
		}
		return err
	})
	// Even a failed setup receives cleanup and run teardown; no mutation can be
	// credited when its authentication/fixture setup failed.
	t.Setenv("WIRETAP_FAIL_EVENT", "before_case")
	result, err = Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: cfg})
	if err == nil || result.Executed != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	events, _ = os.ReadFile(log)
	if !strings.HasSuffix(string(events), "before_run \nbefore_case baseline\nafter_case baseline\nafter_run \n") {
		t.Fatal(string(events))
	}
	t.Setenv("WIRETAP_FAIL_EVENT", "")
	t.Setenv("WIRETAP_FORCE_PROPERTY_FAIL", "1")
	result, err = Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: cfg})
	if err != nil || result.Failed != 3 || result.Passed != 0 {
		t.Fatalf("failed properties earned credit: %+v %v", result, err)
	}
	report, err := coverage.Generate(context.Background(), result.Bundle, "", false)
	if err != nil || report.Contracts.Rejections.Covered != 0 {
		t.Fatalf("property failure coverage: %+v %v", report, err)
	}
	t.Setenv("WIRETAP_FORCE_PROPERTY_FAIL", "")
	for _, event := range []string{"before_request", "check_case", "after_case", "after_run"} {
		t.Run(event, func(t *testing.T) {
			mu.Lock()
			users, rows = 0, 0
			mu.Unlock()
			t.Setenv("WIRETAP_FAIL_EVENT", event)
			result, err := Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: cfg})
			if err == nil {
				t.Fatalf("ignored failed %s hook", event)
			}
			run, _, loadErr := bundle.Load(result.Bundle)
			if loadErr != nil || run.RunState != "interrupted" {
				t.Fatalf("failed hook left unreadable output: %+v %v", run, loadErr)
			}
			events, _ := os.ReadFile(log)
			if !strings.HasSuffix(string(events), "after_run \n") {
				t.Fatal("run cleanup was skipped")
			}
		})
	}
}
