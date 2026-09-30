package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
)

func TestReplaySequenceFreshIDsCookiesAndPrefix(t *testing.T) {
	schema := `openapi: 3.0.3
info: {title: Accounts, version: '1'}
paths:
  /login:
    post:
      responses:
        '200': {description: session, content: {application/json: {schema: {type: object}}}}
  /users/{id}:
    put:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      requestBody:
        required: true
        content: {application/json: {schema: {type: object, required: [name], properties: {name: {type: string, minLength: 1}}}}}
      responses:
        '200': {description: updated}
        '400': {description: invalid}
  /finish:
    post:
      responses:
        '200': {description: done}
`
	var flows []artifact.Flow
	for i, path := range []string{"/login", "/users/old", "/finish"} {
		method := "POST"
		var body *artifact.Body
		if i == 1 {
			method = "PUT"
			body = &artifact.Body{Text: `{"name":"valid"}`}
		}
		flows = append(flows, artifact.Flow{ID: fmt.Sprintf("step-%d", i+1), Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: method, URL: "http://old" + path, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: body}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}})
	}
	source, _, _ := makeSeed(t, schema, flows)
	var mu sync.Mutex
	login, finish := 0, 0
	current := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/login" {
			login++
			current = fmt.Sprintf("user-%d", login)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: current})
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":%q}`, current)
			return
		}
		if r.URL.Path == "/finish" {
			finish++
			return
		}
		c, err := r.Cookie("session")
		if r.URL.Path != "/users/"+current || err != nil || c.Value != current {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var v map[string]any
		_ = json.Unmarshal(body, &v)
		if name, ok := v["name"].(string); !ok || name == "" {
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	cfg := Config{MaxCases: 3, Bindings: []Binding{{Name: "user", FlowID: "step-2", In: "path", Path: "id"}}, Extract: []Extraction{{Name: "user", FlowID: "step-1", In: "body", Path: "/id"}}}
	r, err := Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: cfg})
	if err != nil || r.Passed != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	mu.Lock()
	if login != 4 || finish != 1 {
		t.Errorf("prefix replay: login=%d finish=%d", login, finish)
	}
	mu.Unlock()
	_, attempts, err := bundle.Load(r.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range attempts {
		if a.Exploration.Mode == "rejection" {
			if len(a.Traffic) != 2 {
				t.Fatal("replayed steps after rejected request")
			}
			if strings.Contains(a.Traffic[1].Request.HTTP.URL, "/old") {
				t.Fatal("stale resource ID")
			}
		}
	}
}

func TestExpectedStatusStillRequiresValidResponseBody(t *testing.T) {
	source, _, s := makeSeed(t, testSpec, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if s.validate(request{Method: r.Method, URL: "http://p" + r.URL.String(), Headers: r.Header, Body: body}) == nil {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"order"}`)
		} else {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"wrong":"shape"}`)
		}
	}))
	defer server.Close()
	r, err := Run(context.Background(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Config: Config{MaxCases: 1}})
	if err != nil || r.Failed != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestUnsupportedSeedsAndUnknownBindingsFailBeforeExecution(t *testing.T) {
	source, _, _ := makeSeed(t, testSpec, nil)
	for _, cfg := range []Config{{Bindings: []Binding{{Name: "n", FlowID: "unknown", In: "header", Path: "X-Test"}}}, {ExpectedStatuses: []int{500}}, {ExpectedStatuses: []int{401}}} {
		if _, err := Run(context.Background(), Options{Bundle: source, Target: "http://127.0.0.1:1", Output: t.TempDir(), Config: cfg}); err == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
}
