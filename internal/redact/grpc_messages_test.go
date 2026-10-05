package redact

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
)

func streamRedactionFixture() *artifact.Artifact {
	payload := func(index int, secret string) artifact.GrpcPayload {
		return artifact.GrpcPayload{Index: index, Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString([]byte(secret))}, Body: &artifact.Body{Encoding: "json", Text: `{"token":"` + secret + `","public":"value"}`}}
	}
	return &artifact.Artifact{Traffic: []artifact.Flow{{Request: artifact.Message{GRPC: &artifact.GrpcMessage{Messages: []artifact.GrpcPayload{payload(0, "first-distinctive-6138"), payload(2, "third-distinctive-9931")}}}}}}
}
func TestGRPCMessageSelectorUsesOriginalWireIndex(t *testing.T) {
	capture := streamRedactionFixture()
	if err := Apply(capture, []artifact.Redaction{{Field: "request.messages[2].body.token", Rule: "remove"}}); err != nil {
		t.Fatal(err)
	}
	messages := capture.Traffic[0].Request.GRPC.Messages
	if messages[0].Redacted || messages[0].Wire == nil || !strings.Contains(messages[0].Body.Text, "first-distinctive-6138") {
		t.Fatal("selector changed unselected first message")
	}
	if !messages[1].Redacted || messages[1].Wire != nil || strings.Contains(messages[1].Body.Text, "third-distinctive-9931") {
		t.Fatal("selector failed to remove original third-message copies")
	}
	if !strings.Contains(messages[1].Body.Text, "public") {
		t.Fatal("unselected field was removed")
	}
}
func TestGRPCMessageSelectorForOmittedMessageDoesNotSelectSlicePosition(t *testing.T) {
	capture := streamRedactionFixture()
	before, _ := json.Marshal(capture)
	if err := Apply(capture, []artifact.Redaction{{Field: "request.messages[1].body.token", Rule: "remove"}}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(capture)
	if string(before) != string(after) {
		t.Fatal("omitted wire index selected retained message at slice position 1")
	}
}
func TestGRPCMessageAliasesSelectEveryRetainedMessage(t *testing.T) {
	for _, selector := range []string{"request.body.token", "request.messages[*].body.token"} {
		t.Run(selector, func(t *testing.T) {
			capture := streamRedactionFixture()
			if err := Apply(capture, []artifact.Redaction{{Field: selector, Rule: "remove"}}); err != nil {
				t.Fatal(err)
			}
			for _, message := range capture.Traffic[0].Request.GRPC.Messages {
				if message.Wire != nil || !message.Redacted || strings.Contains(message.Body.Text, "distinctive") {
					t.Fatalf("message alias did not remove containing wire and projection field: %+v", message)
				}
			}
		})
	}
}
func TestGRPCMessageAndStatusSelectorsRemainIndependent(t *testing.T) {
	capture := richRedactionFixture(t, false)
	capture.Traffic[0].Request = streamRedactionFixture().Traffic[0].Request
	rules := []artifact.Redaction{{Field: "request.messages[2].body.token", Rule: "remove"}, {Field: "response.status_details[*].body.metadata.token", Rule: "remove"}, {Field: "response.headers.grpc-message", Rule: "remove"}}
	if err := Apply(capture, rules); err != nil {
		t.Fatal(err)
	}
	response := capture.Traffic[0].Response.GRPC
	if response.StatusCode != 7 || response.StatusMessage != "" || response.StatusDetails.Message != "" || response.StatusDetails.Wire != nil {
		t.Fatal("mixed rules changed outcome or retained status-message copies")
	}
	if capture.Traffic[0].Request.GRPC.Messages[0].Wire == nil {
		t.Fatal("mixed rules changed unselected application message")
	}
	raw, _ := json.Marshal(capture)
	for _, secret := range []string{"third-distinctive-9931", "rich-map-secret-44218", "rich-message-secret-54182"} {
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte(secret))) {
			t.Fatalf("secret retained by mixed rules: %s", secret)
		}
	}
}
