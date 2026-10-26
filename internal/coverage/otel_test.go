package coverage

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func TestTraceMetadataSurvivesReports(t *testing.T) {
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "retry"} {
		doc := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: id, Name: "same name", TraceID: id, RootSpanID: "root", StartedAt: time.Now(), Status: "pass"}, Traffic: []artifact.Flow{{ID: "flow-1", Protocol: "http", Channel: "test_to_sut", TraceID: id, RequestSpanID: "sender", TraceContextSource: "received", TestCorrelation: "trace", Request: &artifact.HttpMessage{Method: "GET", URL: "http://sut/items", Headers: artifact.Headers{"Traceparent": {"old"}, "Tracestate": {"old"}, "X-Wiretap-Session-Id": {"old"}, "Accept": {"application/json"}}}, Response: &artifact.HttpMessage{Status: 200}}}}
		if _, err := writer.Register(doc.TestCase, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(doc); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Generate(t.Context(), writer.Path(), "example", false)
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
	// Trace metadata is present in diagnostic exports as well.
	raw, _ := json.Marshal(report)
	if !bytes.Contains(raw, []byte(`"request_span_id":"sender"`)) {
		t.Fatal("JSON metadata lost")
	}
}
