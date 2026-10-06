package bundlereport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

func TestAttemptSequenceUsesRequestStartAndPreservesEvidence(t *testing.T) {
	started := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	capture := &artifact.Artifact{
		TestCase: artifact.TestCase{ID: "attempt-1", Name: "withdrawal updates the account balance", Status: "pass"},
		Traffic: []artifact.Flow{
			{ID: "flow-1", Channel: "sut_to_dependency", AssetID: "accounts", ParentFlowID: "flow-2", TestCorrelation: "active_session", ParentCorrelation: "timing", StartedAt: started.Add(time.Millisecond), Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://accounts/api/accounts/1001"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Body: &artifact.Body{Text: `{"balance":100,"name":"pipe|name"}`}}}},
			{ID: "flow-2", Channel: "test_to_sut", StartedAt: started, Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://transactions/api/transactions/withdraw?label=one|two"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 201}}},
		},
	}
	before, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	page := attemptMarkdown(&bundle.Run{DependencyKinds: map[string]string{"accounts": "mock"}}, capture, "tests/attempt-1.json")
	requestHeading := "## 1. POST /api/transactions/withdraw?label=one|two"
	dependencyHeading := "## 2. GET /api/accounts/1001"
	if strings.Index(page, requestHeading) < 0 || strings.Index(page, dependencyHeading) < strings.Index(page, requestHeading) {
		t.Fatalf("sequence does not reflect request starts:\n%s", page)
	}
	for _, expected := range []string{
		"System → accounts (mock)",
		"single active test session (fallback)",
		"request timing overlap (inferred)",
		"Parent links record captured correlation",
		"[flow-2](#" + flowAnchor("flow-2") + ")",
		`<a id="` + flowAnchor("flow-1") + `"></a>`,
		"one&#124;two",
		"```json\n{\n  \"balance\": 100,",
		"Individual test assertions were not recorded",
		"Provider agreement and which response fields the consumer requires are not established",
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("missing %q", expected)
		}
	}
	after, err := json.Marshal(capture)
	if err != nil || string(before) != string(after) {
		t.Fatal("rendering mutated canonical capture")
	}
}

func TestMissingTimestampsDoNotInventSequence(t *testing.T) {
	capture := &artifact.Artifact{Traffic: []artifact.Flow{{ID: "first"}, {ID: "second", StartedAt: time.Now()}}}
	flows, chronological := orderedFlows(capture.Traffic)
	if chronological || flows[0].ID != "first" || flows[1].ID != "second" {
		t.Fatal("missing timestamps must preserve source order without claiming chronology")
	}
	page := attemptMarkdown(&bundle.Run{}, capture, "tests/attempt-1.json")
	if !strings.Contains(page, "do not infer chronological order from flow IDs") {
		t.Fatal("missing timestamp limitation absent")
	}
}

func TestRunMarkdownStatesProvenance(t *testing.T) {
	run := &bundle.Run{RunID: "new-run", APIID: "payments", SUTTargetKind: "service", TestSource: map[string]any{"revision": "abc123"}}
	page := runMarkdown(run)
	for _, expected := range []string{"API identity: payments", "abc123", "Not recorded at run level"} {
		if !strings.Contains(page, expected) {
			t.Errorf("missing %q", expected)
		}
	}
}

func TestAttemptPageRefreshesOlderPresentation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attempt.md")
	legacy := "---\nwiretap:\n  profile_version: \"1.0\"\n  kind: attempt\n  run_id: run\n  attempt_id: attempt\n  evidence: evidence/attempt.json\n---\nold body"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if currentAttemptPage(path, "run", "attempt", "evidence/attempt.json") {
		t.Fatal("older attempt presentation would never pick up new navigation and sequence")
	}
	current := strings.Replace(legacy, "  kind: attempt", "  kind: attempt\n  view_version: \"3\"", 1)
	if err := os.WriteFile(path, []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	if !currentAttemptPage(path, "run", "attempt", "evidence/attempt.json") {
		t.Fatal("current presentation should retain immutable-attempt fast path")
	}
}
