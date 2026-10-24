package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
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

func TestGRPCWildcardRedactionAndStatusCopies(t *testing.T) {
	message := &artifact.GrpcMessage{Kind: "grpc", StatusMessage: "secret", Metadata: artifact.Headers{"Grpc-Message": {"secret"}}, Trailers: artifact.Headers{"Grpc-Message": {"secret"}}, Messages: []artifact.GrpcPayload{{Body: &artifact.Body{Encoding: "json", Text: `{"users":[{"token":"secret","large":9007199254740993},{"token":"secret"}],"labels":{"a":{"token":"secret"}}}`}, Wire: &artifact.Body{Encoding: "base64", Bytes: "c2VjcmV0"}}}}
	art := &artifact.Artifact{Traffic: []artifact.Flow{{Protocol: "grpc", Response: artifact.Message{GRPC: message}}}}
	if err := Apply(art, []artifact.Redaction{{Field: "response.messages[*].body.users[*].token", Rule: "remove"}, {Field: "response.messages[*].body.labels[*].token", Rule: "remove"}, {Field: "response.trailers.grpc-message", Rule: "remove"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(art)
	if strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), "9007199254740993") || message.Messages[0].Wire != nil || !message.Messages[0].Redacted {
		t.Fatalf("redaction failed: %s", raw)
	}
}

func TestGRPCRedactionOmitsUndecodablePayload(t *testing.T) {
	message := &artifact.GrpcMessage{Kind: "grpc", Messages: []artifact.GrpcPayload{{Body: &artifact.Body{Text: "secret"}, Wire: &artifact.Body{Encoding: "base64", Bytes: "c2VjcmV0"}}}}
	art := &artifact.Artifact{Traffic: []artifact.Flow{{Request: artifact.Message{GRPC: message}}}}
	if err := Apply(art, []artifact.Redaction{{Field: "request.messages[*].body.token", Rule: "remove"}}); err != nil {
		t.Fatal(err)
	}
	if message.Messages[0].Body != nil || message.Messages[0].Wire != nil || message.Messages[0].DecodeError == "" {
		t.Fatalf("undecodable payload retained: %+v", message.Messages[0])
	}
}

func TestGRPCRedactionRemovesOriginalWirePayload(t *testing.T) {
	art := &artifact.Artifact{Traffic: []artifact.Flow{{Protocol: "grpc", Request: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Metadata: artifact.Headers{"Authorization": {"Bearer secret"}}, Messages: []artifact.GrpcPayload{{Index: 0, Body: &artifact.Body{Encoding: "json", Text: `{"token":"secret","name":"item"}`}, Wire: &artifact.Body{Encoding: "base64", Bytes: "c2VjcmV0"}}}}}}}}
	if err := Apply(art, []artifact.Redaction{{Field: "request.metadata.authorization", Rule: "mask"}, {Field: "request.body.token", Rule: "remove"}}); err != nil {
		t.Fatal(err)
	}
	request := art.Traffic[0].Request.GRPC
	if request.Metadata["Authorization"][0] != "****" || request.Messages[0].Wire != nil || !request.Messages[0].Redacted || request.Messages[0].Body.Text != `{"name":"item"}` {
		t.Fatalf("sensitive gRPC payload survived redaction: %#v", request)
	}
}
