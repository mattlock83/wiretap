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
				Request: Message{HTTP: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com",
					Headers: Headers{
						"b": {"2"},
						"a": {"1"},
					},
				}},
				Response: Message{HTTP: &HttpMessage{Status: 200}},
			},
			{
				ID:       "a",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request:  Message{HTTP: &HttpMessage{Method: "GET", URL: "http://example.com/second"}},
				Response: Message{HTTP: &HttpMessage{Status: 200}},
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
				Request: Message{HTTP: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com/second",
				}},
				Response: Message{HTTP: &HttpMessage{Status: 200}},
			},
			{
				ID:       "b",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request: Message{HTTP: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com",
					Headers: Headers{
						"a": {"1"},
						"b": {"2"},
					},
				}},
				Response: Message{HTTP: &HttpMessage{Status: 200}},
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
		var message Message
		if err := json.Unmarshal([]byte(raw), &message); err == nil {
			t.Fatalf("invalid HTTP message accepted: %s", raw)
		}
	}
	message := Message{HTTP: &HttpMessage{Method: "GET"}}
	if err := json.Unmarshal([]byte(`null`), &message); err != nil || message.HTTP != nil {
		t.Fatalf("null message: %v", err)
	}
}

func TestGRPCMessageRequiresTagAndExplicitStatus(t *testing.T) {
	for _, raw := range []string{`{"procedure":"/a/b"}`, `{"grpc_status":0}`, `{"messages":[]}`, `{"kind":"future"}`, `{"kind":"grpc","http_status":200}`, `{"kind":"grpc","http_status":200,"grpc_status":null}`} {
		var message Message
		if err := json.Unmarshal([]byte(raw), &message); err == nil {
			t.Fatalf("invalid gRPC message accepted: %s", raw)
		}
	}
	var message Message
	if err := json.Unmarshal([]byte(`{"kind":"grpc","http_status":200,"grpc_status":0}`), &message); err != nil {
		t.Fatal(err)
	}
	if message.GRPC == nil || message.GRPC.StatusCode != 0 {
		t.Fatal("explicit OK status not restored")
	}
}
