package app

import (
	"strings"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
)

const testProtoSchema = `syntax = "proto3";

package example;

service Service {
  rpc Process (ProcessRequest) returns (ProcessReply);
}

service Dependency {
  rpc Alpha (DependencyRequest) returns (DependencyReply);
  rpc Beta (DependencyRequest) returns (DependencyReply);
}

message ProcessRequest {
  string id = 1;
  string action = 2;
}

message ProcessReply {
  string result = 1;
}

message DependencyRequest {
  string id = 1;
  string action = 2;
}

message DependencyReply {
  string value = 1;
}
`

func TestDecodeGRPCPayloads_ProtoJSONInArtifact(t *testing.T) {
	art := &artifact.Artifact{
		Schemas: []artifact.SchemaRef{
			{
				ID:   "grpc-sut",
				Role: "sut",
				Type: "protobuf",
				Source: artifact.SchemaSource{
					Type:    "inline",
					Content: testProtoSchema,
				},
			},
			{
				ID:   "grpc-fake",
				Role: "fake",
				Type: "protobuf",
				Source: artifact.SchemaSource{
					Type:    "inline",
					Content: testProtoSchema,
				},
			},
		},
		Traffic: []artifact.Flow{
			{
				ID:       "flow-1",
				Protocol: "grpc",
				Channel:  "test_to_sut",
				Request: artifact.Message{GRPC: &artifact.GrpcMessage{
					Body: &artifact.Body{Encoding: "base64", Bytes: "CgMxMjMSB3Byb2Nlc3M="},
				}},
				Response: artifact.Message{GRPC: &artifact.GrpcMessage{
					Body: &artifact.Body{Encoding: "base64", Bytes: "ChthbHBoYTphbHBoYS1va3xiZXRhOmJldGEtb2s="},
				}},
				GRPC: &artifact.GrpcMeta{
					Service: "example.Service",
					Method:  "Process",
				},
			},
			{
				ID:       "flow-2",
				Protocol: "grpc",
				Channel:  "sut_to_mock",
				Request: artifact.Message{GRPC: &artifact.GrpcMessage{
					Body: &artifact.Body{Encoding: "base64", Bytes: "CgMxMjMSB3Byb2Nlc3M="},
				}},
				Response: artifact.Message{GRPC: &artifact.GrpcMessage{
					Body: &artifact.Body{Encoding: "base64", Bytes: "CghhbHBoYS1vaw=="},
				}},
				GRPC: &artifact.GrpcMeta{
					Service: "example.Dependency",
					Method:  "Alpha",
				},
			},
		},
	}

	decodeGRPCPayloads(art)

	assertDecodedBody(t, art.Traffic[0].Request.GRPC.Body, "grpc-sut", []string{`"id":"123"`, `"action":"process"`})
	assertDecodedBody(t, art.Traffic[0].Response.GRPC.Body, "grpc-sut", []string{`"result":"alpha:alpha-ok|beta:beta-ok"`})
	assertDecodedBody(t, art.Traffic[1].Request.GRPC.Body, "grpc-fake", []string{`"id":"123"`, `"action":"process"`})
	assertDecodedBody(t, art.Traffic[1].Response.GRPC.Body, "grpc-fake", []string{`"value":"alpha-ok"`})
}

func TestDecodeGRPCPayloads_LeavesBinaryWhenNoMatchingMethod(t *testing.T) {
	const raw = "CgMxMjMSB3Byb2Nlc3M="
	art := &artifact.Artifact{
		Schemas: []artifact.SchemaRef{
			{
				ID:   "grpc-sut",
				Role: "sut",
				Type: "protobuf",
				Source: artifact.SchemaSource{
					Type:    "inline",
					Content: testProtoSchema,
				},
			},
		},
		Traffic: []artifact.Flow{
			{
				ID:       "flow-1",
				Protocol: "grpc",
				Channel:  "test_to_sut",
				Request: artifact.Message{GRPC: &artifact.GrpcMessage{
					Body: &artifact.Body{Encoding: "base64", Bytes: raw},
				}},
				GRPC: &artifact.GrpcMeta{
					Service: "example.Service",
					Method:  "Unknown",
				},
			},
		},
	}

	decodeGRPCPayloads(art)

	body := art.Traffic[0].Request.GRPC.Body
	if body == nil {
		t.Fatalf("expected body")
	}
	if body.Bytes != raw {
		t.Fatalf("expected base64 bytes to remain unchanged")
	}
	if body.Text != "" {
		t.Fatalf("expected no text decode for unmatched method")
	}
	if body.Decoded != nil {
		t.Fatalf("expected no decoded body for unmatched method")
	}
}

func assertDecodedBody(t *testing.T, body *artifact.Body, schemaID string, contains []string) {
	t.Helper()
	if body == nil {
		t.Fatalf("expected body")
	}
	if body.Encoding != "utf8" {
		t.Fatalf("expected utf8 encoding, got %q", body.Encoding)
	}
	if body.Bytes != "" {
		t.Fatalf("expected binary bytes to be dropped")
	}
	if body.Text == "" {
		t.Fatalf("expected decoded text")
	}
	if body.Decoded == nil {
		t.Fatalf("expected decoded metadata")
	}
	if body.Decoded.Encoding != "protojson" {
		t.Fatalf("expected decoded encoding protojson, got %q", body.Decoded.Encoding)
	}
	if body.Decoded.SchemaRef != schemaID {
		t.Fatalf("expected schema ref %q, got %q", schemaID, body.Decoded.SchemaRef)
	}
	if body.Decoded.Text != body.Text {
		t.Fatalf("expected decoded text to mirror body text")
	}
	for _, fragment := range contains {
		if !strings.Contains(body.Text, fragment) {
			t.Fatalf("expected decoded text %q to contain %q", body.Text, fragment)
		}
	}
}
