package redact

import (
	"encoding/json"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
)

func TestApplyRedactions(t *testing.T) {
	art := artifact.Artifact{
		Version: 1,
		TestCase: artifact.TestCase{
			ID:   "tc-1",
			Name: "redact",
		},
		Traffic: []artifact.Flow{
			{
				ID:       "flow-1",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request: artifact.Message{HTTP: &artifact.HttpMessage{
					Method: "POST",
					URL:    "http://example.com",
					Headers: artifact.Headers{
						"Authorization": {"secret"},
					},
					Trailers: artifact.Headers{
						"X-Checksum": {"sensitive"},
					},
					Body: &artifact.Body{
						Encoding: "utf8",
						Text:     `{"token":"abc","nested":{"id":123}}`,
					},
				}},
				Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}},
			},
		},
	}

	rules := []artifact.Redaction{
		{Field: "request.headers.authorization", Rule: "mask"},
		{Field: "request.trailers.x-checksum", Rule: "remove"},
		{Field: "request.body.token", Rule: "remove"},
	}

	if err := Apply(&art, rules); err != nil {
		t.Fatalf("apply redactions: %v", err)
	}

	flow := art.Traffic[0]
	req := flow.Request.HTTP
	if req == nil {
		t.Fatalf("expected http request")
	}

	values := req.Headers["Authorization"]
	if len(values) != 1 || values[0] == "secret" {
		t.Fatalf("expected authorization header to be masked")
	}
	if values[0] != "****" {
		t.Fatalf("expected default mask, got %q", values[0])
	}
	if _, ok := req.Trailers["X-Checksum"]; ok {
		t.Fatalf("expected request trailer to be removed")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(req.Body.Text), &payload); err != nil {
		t.Fatalf("parse redacted body: %v", err)
	}
	if _, ok := payload["token"]; ok {
		t.Fatalf("expected token to be removed")
	}
}
