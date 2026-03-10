package app

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phrond/evermock/core"
)

func TestStandaloneStartUsesScenarioHeaderOverride(t *testing.T) {
	templateDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Mock.ListenAddr = "127.0.0.1:0"

	appInstance, err := New(Deps{Config: cfg})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() {
		_ = appInstance.StopStandalone(context.Background())
	})

	defaultTemplate := filepath.Join(templateDir, "account-default.json")
	scenarioTemplate := filepath.Join(templateDir, "account-low-balance.json")

	if err := os.WriteFile(defaultTemplate, []byte(`{
  "id":"account-default",
  "match":{"method":"GET","path":"/accounts/acct-100"},
  "response":{
    "status":200,
    "headers":{"Content-Type":"application/json"},
    "body":"{\"id\":\"acct-100\",\"name\":\"Default {{.Request.Method}}\",\"balance\":1000,\"currency\":\"USD\",\"status\":\"active\"}"
  }
}`), 0o644); err != nil {
		t.Fatalf("write default template: %v", err)
	}
	if err := os.WriteFile(scenarioTemplate, []byte(`{
  "id":"account-low-balance",
  "scenario_id":"low-balance",
  "match":{"method":"GET","path":"/accounts/acct-100"},
  "response":{
    "status":200,
    "headers":{"Content-Type":"application/json"},
    "body":"{\"id\":\"acct-100\",\"name\":\"Scenario {{index .Request.Headers \"X-Demo-Scenario\"}}\",\"balance\":40,\"currency\":\"USD\",\"status\":\"blocked\"}"
  }
}`), 0o644); err != nil {
		t.Fatalf("write scenario template: %v", err)
	}

	start, err := appInstance.StartStandalone(context.Background(), StandaloneStartOptions{
		ScenarioHeader: "X-Demo-Scenario",
		TemplateFiles:  []string{defaultTemplate, scenarioTemplate},
		StackSize:      5,
	})
	if err != nil {
		t.Fatalf("start standalone: %v", err)
	}

	if got := strings.TrimSpace(start.Status); got != "standalone_started" {
		t.Fatalf("expected standalone_started, got %q", got)
	}
	if got := strings.TrimSpace(start.ScenarioHeader); got != "X-Demo-Scenario" {
		t.Fatalf("expected custom scenario header, got %q", got)
	}
	if start.TemplatesLoaded != 2 {
		t.Fatalf("expected two templates loaded")
	}
	if start.Mock.ListenAddr == "" {
		t.Fatalf("expected mock listen address")
	}

	mockBaseURL := start.Mock.ListenAddr
	if !strings.HasPrefix(mockBaseURL, "http://") && !strings.HasPrefix(mockBaseURL, "https://") {
		mockBaseURL = "http://" + mockBaseURL
	}

	status, body := httpGetText(t, mockBaseURL+"/accounts/acct-100")
	if status != http.StatusOK {
		t.Fatalf("expected default template status 200, got %d body=%q", status, body)
	}
	if !strings.Contains(body, `"status":"active"`) {
		t.Fatalf("expected default template body, got %q", body)
	}
	if !strings.Contains(body, "Default GET") {
		t.Fatalf("expected rendered default template method, got %q", body)
	}

	status, body = httpGetTextWithHeaders(t, mockBaseURL+"/accounts/acct-100", map[string]string{
		"X-Demo-Scenario": "low-balance",
	})
	if status != http.StatusOK {
		t.Fatalf("expected scenario template status 200, got %d body=%q", status, body)
	}
	if !strings.Contains(body, `"status":"blocked"`) {
		t.Fatalf("expected scenario template body, got %q", body)
	}
	if !strings.Contains(body, "Scenario low-balance") {
		t.Fatalf("expected rendered scenario value, got %q", body)
	}

	if err := appInstance.StopStandalone(context.Background()); err != nil {
		t.Fatalf("stop standalone: %v", err)
	}
}

func httpGetTextWithHeaders(t *testing.T, url string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body %s: %v", url, err)
	}
	return resp.StatusCode, body.String()
}
