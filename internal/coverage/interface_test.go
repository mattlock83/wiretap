package coverage

import (
	"encoding/json"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/version"
)

func TestMachineReportRetainsIdentityAndBodyAvailability(t *testing.T) {
	ordinal := 2
	capture := &artifact.Artifact{Version: 2, TestCase: artifact.TestCase{ID: "attempt", Name: "a display label", StartedAt: time.Now(), Status: "fail", Identity: &artifact.TestIdentity{ID: "orders/create", Variant: "invalid", AttemptOrdinal: &ordinal}}, Traffic: []artifact.Flow{{ID: "flow", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://sut/orders", Body: &artifact.Body{Text: "{", Encoding: "utf8", OriginalBytes: 100, Truncated: true}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 400}}}}}
	run := &bundle.Run{EvidenceVersion: "3.1", RunID: "run", Revision: 1, RunState: "complete", Attempts: []bundle.AttemptRef{{AttemptID: "attempt", FileRef: bundle.FileRef{Path: "tests/attempt.json"}}}, Producer: &version.Producer{Name: "wiretap", Version: "capture-version"}, SUTSource: map[string]any{"revision": "app-commit"}}
	report, err := GenerateBundleSnapshot(t.TempDir(), run, []*artifact.Artifact{capture}, "Orders", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReportVersion != "1.0" || report.Producer.Name != "wiretap" || report.Capture.Producer.Version != "capture-version" || report.Capture.SUTSource["revision"] != "app-commit" {
		t.Fatalf("missing provenance: %+v", report)
	}
	sample := flowSampleFromFlow(flowEntry{Case: capture.TestCase, Flow: capture.Traffic[0]})
	if sample.TestIdentity.ID != "orders/create" || *sample.TestIdentity.AttemptOrdinal != 2 || !sample.Request.BodyInfo.Truncated || sample.Request.BodyInfo.OriginalBytes != 100 || sample.Request.BodyInfo.RetainedBytes != 1 || sample.Response.BodyInfo.Present {
		t.Fatalf("lossy report: %+v", sample)
	}
	raw, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"body_info", "test_identity", "truncated", "original_bytes"} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("missing %s in %s", key, raw)
		}
	}
}

func TestCoverageReportMatchesPublishedSchema(t *testing.T) {
	report, err := GenerateBundleSnapshot(t.TempDir(), &bundle.Run{EvidenceVersion: "3.1", RunID: "schema-run", Revision: 1, RunState: "complete"}, nil, "API", false)
	if err != nil {
		t.Fatal(err)
	}
	schema, object := validatePublishedReportSchema(t, report)
	object["report_version"] = "2.0"
	if err := schema.Validate(object); err == nil {
		t.Fatal("unknown report version accepted")
	}
}

func validatePublishedReportSchema(t *testing.T, report *Report) (*jsonschema.Schema, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	schemaBytes, err := os.ReadFile("../../docs/coverage-report.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var source any
	if err := json.Unmarshal(schemaBytes, &source); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("coverage-report.schema.json", source); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("coverage-report.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("report violates public schema: %v\n%s", err, raw)
	}
	return schema, document.(map[string]any)
}

func TestCoverageReportIncompleteGRPCMatchesPublishedSchema(t *testing.T) {
	ordinal := 0
	capture := &artifact.Artifact{
		Version:           2,
		CaptureIncomplete: true,
		TestCase: artifact.TestCase{
			ID: "incomplete-attempt", Name: "mixed incomplete capture", Status: "fail",
			StartedAt: time.Date(2026, 10, 24, 1, 2, 3, 0, time.UTC),
			Identity:  &artifact.TestIdentity{ID: "orders/create", Variant: "transport-failure", AttemptOrdinal: &ordinal},
		},
		Traffic: []artifact.Flow{
			{ID: "incomplete-rpc", Protocol: "grpc", Channel: "test_to_sut", TransportError: "connection reset before trailers",
				Request:  artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: "/example.Service/Call", StatusCode: -1}},
				Response: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: "/example.Service/Call", HTTPStatus: 200, StatusCode: -1}},
			},
			{ID: "http-request", Protocol: "http", Channel: "test_to_sut",
				Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://sut/orders", Body: &artifact.Body{Text: "{", Encoding: "utf8", OriginalBytes: 100, Truncated: true}}},
				Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 400}},
			},
		},
	}
	run := &bundle.Run{EvidenceVersion: "3.1", RunID: "incomplete-schema-run", Revision: 1, RunState: "interrupted", Attempts: []bundle.AttemptRef{{AttemptID: capture.TestCase.ID, FileRef: bundle.FileRef{Path: "tests/incomplete-attempt.json"}}}}
	report, err := GenerateBundleSnapshot(t.TempDir(), run, []*artifact.Artifact{capture}, "Orders", false)
	if err != nil {
		t.Fatal(err)
	}
	validatePublishedReportSchema(t, report)
	if len(report.TestCases) != 1 || len(report.TestCases[0].Traffic) != 2 {
		t.Fatalf("nonempty generated report lost flows: %+v", report.TestCases)
	}
	foundRPC, foundHTTP := false, false
	for _, flow := range report.TestCases[0].Traffic {
		if flow.TestIdentity == nil || flow.TestIdentity.ID != "orders/create" || flow.TestIdentity.AttemptOrdinal == nil || *flow.TestIdentity.AttemptOrdinal != 0 {
			t.Fatalf("generated report lost stable test identity: %+v", flow)
		}
		switch flow.FlowID {
		case "incomplete-rpc":
			foundRPC = true
			if flow.Response.GRPCStatus == nil || *flow.Response.GRPCStatus != -1 || !flow.CaptureIncomplete {
				t.Fatalf("missing terminal status was changed or hidden: %+v", flow)
			}
		case "http-request":
			foundHTTP = true
			body := flow.Request.BodyInfo
			if body == nil || !body.Present || !body.Truncated || body.Encoding != "utf8" || body.RetainedBytes != 1 || body.OriginalBytes != 100 || flow.Response.BodyInfo == nil || flow.Response.BodyInfo.Present {
				t.Fatalf("generated report lost HTTP body availability: %+v", flow)
			}
		}
	}
	if !foundRPC || !foundHTTP {
		t.Fatal("generated report omitted a protocol")
	}
}

func TestHTTPBodyAvailabilityRetainsRedactionAndOmission(t *testing.T) {
	info := bodyAvailability(&artifact.Body{Omitted: true, Redacted: true, OriginalBytes: 150})
	if !info.Present || !info.Omitted || !info.Redacted || info.Truncated || info.RetainedBytes != 0 {
		t.Fatalf("ambiguous body availability: %+v", info)
	}
}
