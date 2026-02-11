package mock

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRendererTemplate(t *testing.T) {
	renderer := NewRenderer()
	stub := Stub{
		Response: StubResponse{
			Status: 200,
			Headers: map[string]string{
				"X-Path": "{{.Request.Path}}",
			},
			Body: "hello {{.Request.Query.name}}",
		},
	}

	req := RecordedRequest{
		Method: "GET",
		Path:   "/hello",
		URL:    "/hello?name=codex",
		Headers: map[string][]string{
			"X-Test": {"yes"},
		},
		Query: map[string][]string{
			"name": {"codex"},
		},
		Body: "",
	}

	resp, err := renderer.RenderResponse(stub, req)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}
	if resp.Body != "hello codex" {
		t.Fatalf("unexpected body: %q", resp.Body)
	}
	if resp.Headers["X-Path"][0] != "/hello" {
		t.Fatalf("unexpected header render: %q", resp.Headers["X-Path"][0])
	}
}

func TestRendererCELHelpers(t *testing.T) {
	renderer := NewRenderer()
	stub := Stub{
		Response: StubResponse{
			Status: 200,
			Headers: map[string]string{
				"X-Account": "{{cel \"query['account_id']\"}}",
			},
			Body: `{"meta": {{celJSON "{'path': path, 'method': method, 'account': query['account_id']}"}}}`,
		},
	}

	req := RecordedRequest{
		Method: "POST",
		Path:   "/payments/submit",
		URL:    "/payments/submit?account_id=acct-100",
		Query: map[string][]string{
			"account_id": {"acct-100"},
		},
	}

	resp, err := renderer.RenderResponse(stub, req)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if got := resp.Headers["X-Account"][0]; got != "acct-100" {
		t.Fatalf("unexpected cel header output: %q", got)
	}

	var payload map[string]map[string]string
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatalf("body should be valid json: %v", err)
	}
	if payload["meta"]["path"] != "/payments/submit" {
		t.Fatalf("unexpected json cel output: %+v", payload)
	}
	if payload["meta"]["method"] != "POST" {
		t.Fatalf("unexpected json cel method output: %+v", payload)
	}
}

func TestRendererCELDateMath(t *testing.T) {
	renderer := NewRenderer()
	stub := Stub{
		Response: StubResponse{
			Status: 200,
			Body:   `{{cel "now + duration('24h')"}}`,
		},
	}
	req := RecordedRequest{Method: "GET", Path: "/time", URL: "/time"}

	start := time.Now().UTC()
	resp, err := renderer.RenderResponse(stub, req)
	end := time.Now().UTC()
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	rendered, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(resp.Body))
	if err != nil {
		t.Fatalf("expected RFC3339 timestamp, got %q (%v)", resp.Body, err)
	}
	if rendered.Before(start.Add(23*time.Hour)) || rendered.After(end.Add(25*time.Hour)) {
		t.Fatalf("expected rendered timestamp about 24h in future, got %s", rendered)
	}
}

func TestRendererCELCompileError(t *testing.T) {
	renderer := NewRenderer()
	stub := Stub{
		Response: StubResponse{
			Status: 200,
			Body:   `{{cel "this is not valid ("}}`,
		},
	}
	req := RecordedRequest{Method: "GET", Path: "/broken", URL: "/broken"}

	_, err := renderer.RenderResponse(stub, req)
	if err == nil {
		t.Fatalf("expected render error")
	}
	if !strings.Contains(err.Error(), "cel compile") {
		t.Fatalf("expected cel compile error, got: %v", err)
	}
}
