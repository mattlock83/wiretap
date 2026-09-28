package coverage

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
)

func TestTraceMetadataSurvivesReportsWithoutReplayingIdentity(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"first", "retry"} {
		doc := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: id, Name: "same name", TraceID: id, RootSpanID: "root", StartedAt: time.Now(), Status: "pass"}, Traffic: []artifact.Flow{{ID: "flow-1", Protocol: "http", Channel: "test_to_sut", TraceID: id, RequestSpanID: "sender", TraceContextSource: "received", TestCorrelation: "trace", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://sut/items", Headers: artifact.Headers{"Traceparent": {"old"}, "Tracestate": {"old"}, "X-Phrond-Session-Id": {"old"}, "Accept": {"application/json"}}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}}}}
		if _, err := artifact.WriteFile(dir, doc); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Generate(t.Context(), dir, "example", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.TestCases) != 1 || len(report.TestCases[0].Attempts) != 2 {
		t.Fatalf("attempts lost: %#v", report.TestCases)
	}
	sample := report.TestCases[0].Traffic[0]
	if sample.TestCaseID == "" || sample.TestTraceID == "" || sample.RootSpanID != "root" || sample.RequestSpanID != "sender" {
		t.Fatalf("trace metadata lost: %#v", sample)
	}
	task, ok := workflowHTTPTaskDefinition(sample, "GET /items", "schema", "sut", "")
	if !ok {
		t.Fatal("no workflow task")
	}
	if len(task.With.Headers) != 1 || task.Metadata.Phrond.RequestHeaders["Traceparent"][0] != "old" {
		t.Fatal("workflow replay/evidence headers mixed")
	}
	request := sampleToPostmanRequest(sample.Request, "sut")
	if len(request.Header) != 1 {
		t.Fatalf("stale replay headers: %#v", request.Header)
	}
	var html bytes.Buffer
	if err := WriteReport(&html, report, "html"); err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"Attempt first", "Attempt retry", "test matched by trace", "context received"} {
		if !strings.Contains(html.String(), word) {
			t.Errorf("HTML missing %q", word)
		}
	}
	// Additive fields do not make legacy opaque trace IDs unreadable.
	raw, _ := json.Marshal(report)
	if !bytes.Contains(raw, []byte(`"request_span_id":"sender"`)) {
		t.Fatal("JSON metadata lost")
	}
}
