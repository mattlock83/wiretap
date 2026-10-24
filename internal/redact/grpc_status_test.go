package redact

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func richRedactionFixture(t *testing.T, unknown bool) *artifact.Artifact {
	t.Helper()
	detail, _ := anypb.New(&errdetails.ErrorInfo{Metadata: map[string]string{"token": "rich-map-secret-44218", "public": "retained"}})
	if unknown {
		detail.TypeUrl = "type.invalid/unknown.Secret"
	}
	envelope, _ := proto.Marshal(&statuspb.Status{Code: 7, Message: "rich-message-secret-54182", Details: []*anypb.Any{detail}})
	msg := &artifact.GrpcMessage{Kind: "grpc", StatusCode: 7, StatusMessage: "rich-message-secret-54182", Metadata: artifact.Headers{"Grpc-Message": {"rich-message-secret-54182"}}, Trailers: artifact.Headers{"Grpc-Status-Details-Bin": {base64.StdEncoding.EncodeToString(envelope)}}}
	grpccontract.CaptureStatusDetails(msg, nil)
	return &artifact.Artifact{Traffic: []artifact.Flow{{Response: artifact.Message{GRPC: msg}}}}
}
func TestRichStatusRedactionOfMapsUnknownTypesAndWholeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, field, secret string
		unknown             bool
	}{
		{"map key", "response.status_details[*].body.metadata.token", "rich-map-secret-44218", false},
		{"map wildcard", "response.status_details[*].body.metadata[*]", "rich-map-secret-44218", false},
		{"unknown detail", "response.status_details[0].body.metadata.token", "rich-map-secret-44218", true},
		{"message header", "response.headers.grpc-message", "rich-message-secret-54182", false},
		{"message trailer", "response.trailers.grpc-message", "rich-message-secret-54182", false},
		{"whole status metadata", "response.headers.grpc-status-details-bin", "rich-map-secret-44218", false},
		{"whole status message copy", "response.trailers.grpc-status-details-bin", "rich-message-secret-54182", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := richRedactionFixture(t, tc.unknown)
			if err := Apply(capture, []artifact.Redaction{{Field: tc.field, Rule: "remove"}}); err != nil {
				t.Fatal(err)
			}
			msg := capture.Traffic[0].Response.GRPC
			raw, err := json.Marshal(capture)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), tc.secret) || strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte(tc.secret))) {
				t.Fatalf("secret retained: %s", raw)
			}
			if msg.StatusCode != 7 || msg.StatusDetails.Wire != nil {
				t.Fatal("redaction changed outcome or retained raw envelope")
			}
			if tc.unknown && msg.StatusDetails.Details[0].Wire != nil {
				t.Fatal("unknown selected detail raw retained")
			}
		})
	}
}
