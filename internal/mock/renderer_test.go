package mock

import (
	"encoding/json"
	"regexp"
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

func TestRendererSprigAndCustomHelpers(t *testing.T) {
	renderer := NewRenderer()
	stub := Stub{
		Response: StubResponse{
			Status: 200,
			Headers: map[string]string{
				"X-Slug":  "{{trimAll \"-\" (regexReplaceAll \"[^a-z0-9]+\" (lower .Request.Path) \"-\")}}",
				"X-Trace": "{{uuid}}",
			},
			Body: `{{- $req := mustFromJSON .Request.Body -}}
{{- $from := default "acct-unknown" (index $req "from_account") -}}
{{- $currency := upper (default "usd" (index $req "currency")) -}}
{{- $amount := int (default 0 (index $req "amount")) -}}
{{- $priority := "normal" -}}
{{- if ge $amount 500 }}{{- $priority = "high" -}}{{- end -}}
{{mustToJSON (dict
  "from" $from
  "priority" $priority
  "tags" (list "payment" $currency)
  "expires_at" (formatTime "2006-01-02T15:04:05Z07:00" (nowPlus "2h"))
  "echo" (celJSON "{'method': method, 'path': path}")
)}}`,
		},
	}

	req := RecordedRequest{
		Method: "POST",
		Path:   "/Payments/Submit",
		URL:    "/Payments/Submit",
		Body:   `{"from_account":"acct-100","amount":750}`,
	}

	start := time.Now().UTC()
	resp, err := renderer.RenderResponse(stub, req)
	end := time.Now().UTC()
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if got := resp.Headers["X-Slug"][0]; got != "payments-submit" {
		t.Fatalf("unexpected slug: %q", got)
	}
	trace := resp.Headers["X-Trace"][0]
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(trace) {
		t.Fatalf("unexpected trace id: %q", trace)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatalf("expected JSON body: %v", err)
	}
	if payload["from"] != "acct-100" {
		t.Fatalf("unexpected from field: %+v", payload)
	}
	if payload["priority"] != "high" {
		t.Fatalf("unexpected priority field: %+v", payload)
	}
	tags, ok := payload["tags"].([]any)
	if !ok || len(tags) != 2 || tags[1] != "USD" {
		t.Fatalf("unexpected tags field: %+v", payload["tags"])
	}
	if payload["echo"] != `{"method":"POST","path":"/Payments/Submit"}` {
		t.Fatalf("unexpected CEL echo field: %+v", payload["echo"])
	}

	expiresRaw, ok := payload["expires_at"].(string)
	if !ok {
		t.Fatalf("expires_at should be string, got %T", payload["expires_at"])
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresRaw)
	if err != nil {
		t.Fatalf("expires_at should be RFC3339, got %q (%v)", expiresRaw, err)
	}
	if expiresAt.Before(start.Add(110*time.Minute)) || expiresAt.After(end.Add(130*time.Minute)) {
		t.Fatalf("expected expires_at around +2h, got %s", expiresAt)
	}
}

func TestRendererGoFakeItFuncsExposed(t *testing.T) {
	renderer := NewRenderer()
	stub := Stub{
		Response: StubResponse{
			Status: 200,
			Body:   `{"name":"{{FirstName}}","city":"{{city}}","n":{{IntRange 10 12}}}`,
		},
	}

	req := RecordedRequest{
		Method: "GET",
		Path:   "/faker",
		URL:    "/faker",
	}

	resp, err := renderer.RenderResponse(stub, req)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatalf("expected JSON body: %v", err)
	}

	name, ok := payload["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		t.Fatalf("expected non-empty fake name, got %+v", payload["name"])
	}
	city, ok := payload["city"].(string)
	if !ok || strings.TrimSpace(city) == "" {
		t.Fatalf("expected non-empty fake city from lower alias, got %+v", payload["city"])
	}
	number, ok := payload["n"].(float64)
	if !ok {
		t.Fatalf("expected numeric fake value, got %+v", payload["n"])
	}
	if number < 10 || number > 12 {
		t.Fatalf("expected IntRange in [10,12], got %v", number)
	}
}

func TestRendererSessionVarsAcrossTemplates(t *testing.T) {
	renderer := NewRenderer()
	req := RecordedRequest{Method: "GET", Path: "/accounts/current", URL: "/accounts/current"}

	setter := Stub{
		SessionID: "session-a",
		Response: StubResponse{
			Status: 200,
			Body:   `{{- setVar "account_number" "acct-123" -}}stored`,
		},
	}
	getter := Stub{
		SessionID: "session-a",
		Response: StubResponse{
			Status: 200,
			Body:   `{{mustGetVar "account_number"}}`,
		},
	}

	setResp, err := renderer.RenderResponse(setter, req)
	if err != nil {
		t.Fatalf("unexpected set render error: %v", err)
	}
	if strings.TrimSpace(setResp.Body) != "stored" {
		t.Fatalf("unexpected set body: %q", setResp.Body)
	}

	getResp, err := renderer.RenderResponse(getter, req)
	if err != nil {
		t.Fatalf("unexpected get render error: %v", err)
	}
	if strings.TrimSpace(getResp.Body) != "acct-123" {
		t.Fatalf("expected account from session vars, got %q", getResp.Body)
	}
}

func TestRendererSessionVarsIsolationAndClear(t *testing.T) {
	renderer := NewRenderer()
	req := RecordedRequest{Method: "GET", Path: "/payments", URL: "/payments"}

	_, err := renderer.RenderResponse(Stub{
		SessionID: "session-a",
		Response:  StubResponse{Status: 200, Body: `{{- setVar "account_number" "acct-123" -}}ok`},
	}, req)
	if err != nil {
		t.Fatalf("unexpected set render error: %v", err)
	}

	_, err = renderer.RenderResponse(Stub{
		SessionID: "session-b",
		Response:  StubResponse{Status: 200, Body: `{{mustGetVar "account_number"}}`},
	}, req)
	if err == nil {
		t.Fatalf("expected missing var error for different session")
	}
	if !strings.Contains(err.Error(), "mustGetVar") {
		t.Fatalf("expected mustGetVar error, got: %v", err)
	}

	renderer.ClearSessionVars("session-a")

	_, err = renderer.RenderResponse(Stub{
		SessionID: "session-a",
		Response:  StubResponse{Status: 200, Body: `{{mustGetVar "account_number"}}`},
	}, req)
	if err == nil {
		t.Fatalf("expected missing var error after clear session")
	}

	_, err = renderer.RenderResponse(Stub{
		SessionID: "session-a",
		Response:  StubResponse{Status: 200, Body: `{{setVar "account_number" "acct-456"}}`},
	}, req)
	if err != nil {
		t.Fatalf("unexpected set render error after clear: %v", err)
	}

	renderer.ClearAllSessionVars()

	_, err = renderer.RenderResponse(Stub{
		SessionID: "session-a",
		Response:  StubResponse{Status: 200, Body: `{{mustGetVar "account_number"}}`},
	}, req)
	if err == nil {
		t.Fatalf("expected missing var error after clear all sessions")
	}
}
