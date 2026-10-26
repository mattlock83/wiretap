package artifact

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCaptureJSONDeterministic(t *testing.T) {
	baseTime := time.Date(2026, 2, 4, 19, 0, 0, 0, time.UTC)

	artifactA := Artifact{
		Version: 1,
		TestCase: TestCase{
			ID:        "tc-1",
			Name:      "deterministic",
			StartedAt: baseTime,
		},
		Traffic: []Flow{
			{
				ID:       "b",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com",
					Headers: Headers{
						"b": {"2"},
						"a": {"1"},
					},
				},
				Response: &HttpMessage{Status: 200},
			},
			{
				ID:       "a",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request:  &HttpMessage{Method: "GET", URL: "http://example.com/second"},
				Response: &HttpMessage{Status: 200},
			},
		},
	}

	artifactB := Artifact{
		Version: 1,
		TestCase: TestCase{
			ID:        "tc-1",
			Name:      "deterministic",
			StartedAt: baseTime,
		},
		Traffic: []Flow{
			{
				ID:       "a",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com/second",
				},
				Response: &HttpMessage{Status: 200},
			},
			{
				ID:       "b",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com",
					Headers: Headers{
						"a": {"1"},
						"b": {"2"},
					},
				},
				Response: &HttpMessage{Status: 200},
			},
		},
	}

	artifactA.Normalize()
	outA, err := json.Marshal(&artifactA)
	if err != nil {
		t.Fatalf("encode artifact A: %v", err)
	}
	artifactB.Normalize()
	outB, err := json.Marshal(&artifactB)
	if err != nil {
		t.Fatalf("encode artifact B: %v", err)
	}
	if string(outA) != string(outB) {
		t.Fatalf("expected deterministic output")
	}
}

func TestHTTPMessageJSONRejectsInvalidFields(t *testing.T) {
	for _, raw := range []string{`{"method":123}`, `{"status":"200"}`, `{"body":"invalid"}`, `[]`} {
		var message *HttpMessage
		if err := json.Unmarshal([]byte(raw), &message); err == nil {
			t.Fatalf("invalid HTTP message accepted: %s", raw)
		}
	}
	message := &HttpMessage{Method: "GET"}
	if err := json.Unmarshal([]byte(`null`), &message); err != nil || message != nil {
		t.Fatalf("null message: %v", err)
	}
}

func TestFlowPreservesNullAndEmptyHTTPMessages(t *testing.T) {
	for _, tc := range []struct{ raw, request, response string }{
		{`{}`, `null`, `null`},
		{`{"request":null,"response":{}}`, `null`, `{}`},
		{`{"request":{},"response":null}`, `{}`, `null`},
	} {
		var flow Flow
		if err := json.Unmarshal([]byte(tc.raw), &flow); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(flow)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["request"]) != tc.request || string(fields["response"]) != tc.response {
			t.Fatalf("HTTP message presence changed for %s: %s", tc.raw, encoded)
		}
	}
}

func TestNativeRPCMessagesAndFlowsAreRejected(t *testing.T) {
	for _, raw := range []string{`{"procedure":"/a/b"}`, `{"grpc_status":0}`, `{"messages":[]}`, `{"kind":"future"}`, `{"kind":"grpc","http_status":200,"grpc_status":0}`, `{"Kind":"grpc"}`, `{"PROCEDURE":"/a/b"}`, `{"Grpc_Status":0}`, `{"MESSAGES":[]}`} {
		var message *HttpMessage
		if err := json.Unmarshal([]byte(raw), &message); err == nil {
			t.Fatalf("unsupported message accepted: %s", raw)
		}
	}
	for _, raw := range []string{`{"id":"rpc","protocol":"grpc"}`, `{"id":"rpc","grpc_events":[]}`, `{"id":"rpc","GRPC_EVENTS":[]}`, `{"id":"rpc","protocol":" GRpC "}`, `{"id":"rpc","protocol":"grpc-web"}`, `{"id":"rpc","protocol":"grpcs"}`, `{"id":"rpc","protocol":"grpc","PROTOCOL":"http"}`} {
		var flow Flow
		if err := json.Unmarshal([]byte(raw), &flow); err == nil {
			t.Fatalf("unsupported flow accepted: %s", raw)
		}
	}
}

func TestRemovedRPCSchemaMetadataCannotBeDecodedAsHTTP(t *testing.T) {
	for _, raw := range []string{`{"Type":" Protobuf "}`, `{"Protocol":" GRPC-Web "}`, `{"type":"protobuf","TYPE":"openapi"}`, `{"Services":[]}`, `{"ACQUIRED_FROM":"reflection"}`, `{"Acquisition":{}}`} {
		var schema SchemaRef
		if err := json.Unmarshal([]byte(raw), &schema); err == nil {
			t.Fatalf("unsupported schema accepted: %s", raw)
		}
	}
}

func TestRemovedEventAndOpaqueContractEvidenceIsRejected(t *testing.T) {
	for _, raw := range []string{`{"id":"custom","protocol":"example.custom-v2"}`, `{"id":"http","protocol":"http","interaction":{}}`, `{"id":"http","INTERACTION":null}`} {
		var flow Flow
		if err := json.Unmarshal([]byte(raw), &flow); err == nil {
			t.Fatalf("unsupported flow accepted: %s", raw)
		}
	}
	for _, raw := range []string{`{"type":"avro"}`, `{"type":"asyncapi"}`, `{"type":"graphql"}`, `{"type":"openapi","source":{"type":"package"}}`, `{"type":"openapi","source":{"type":"file","files":[]}}`} {
		var schema SchemaRef
		if err := json.Unmarshal([]byte(raw), &schema); err == nil {
			t.Fatalf("unsupported contract accepted: %s", raw)
		}
	}
}

func TestValidateCaptureRejectsDuplicateHTTPFlows(t *testing.T) {
	for _, flows := range [][]Flow{{{ID: "a", Protocol: "http"}, {ID: "a", Protocol: "http"}}, {{Protocol: "http"}}, {{ID: "event", Protocol: "kafka"}}} {
		if err := ValidateCapture(&Artifact{Traffic: flows}); err == nil {
			t.Fatalf("invalid flows accepted: %#v", flows)
		}
	}
}

func TestRemovedRPCSourcesAndUnknownSchemaFieldsAreRejected(t *testing.T) {
	for _, raw := range []string{`{"source":{"Import_Paths":["./proto"]}}`, `{"source":{"Type":" protobuf_inline "}}`, `{"source":{"format":"FILE_DESCRIPTOR_SET"}}`, `{"unknown":true}`, `{"source":{"unknown":true}}`} {
		var schema SchemaRef
		if err := json.Unmarshal([]byte(raw), &schema); err == nil {
			t.Fatalf("unsupported source or unknown schema field accepted: %s", raw)
		}
	}
}
