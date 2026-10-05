package redact

import (
	"encoding/json"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
)

// Removing a field projection also removes every raw representation containing
// that value. Re-encoding a masked Any would fabricate bytes the service never sent.
func redactStatusDetails(msg *artifact.GrpcMessage, selector, path string, rule artifact.Redaction) {
	removeStatusEnvelope(msg)
	status := msg.StatusDetails
	if status == nil {
		return
	}
	indices := parsePath(strings.TrimPrefix(selector, "status_details"))
	for index := range status.Details {
		if len(indices) == 1 && indices[0].index != nil && *indices[0].index != index {
			continue
		}
		detail := &status.Details[index]
		detail.Wire, detail.State = nil, "redacted"
		detail.Diagnostic = "original detail bytes omitted after configured redaction"
		path = strings.TrimPrefix(path, "body.")
		if detail.Body == nil {
			continue
		}
		var projection any
		decoder := json.NewDecoder(strings.NewReader(detail.Body.Text))
		decoder.UseNumber()
		if detail.Body.Bytes != "" || decoder.Decode(&projection) != nil {
			detail.Body = nil
			continue
		}
		switch projection.(type) {
		case map[string]any, []any:
			if path == "body" || path == "" || len(parsePath(path)) == 0 {
				detail.Body = nil
				continue
			}
			applyBodyRule(&artifact.HttpMessage{Body: detail.Body}, path, rule)
		default:
			detail.Body = nil
		}
	}
}

func removeStatusEnvelope(msg *artifact.GrpcMessage) {
	remove := artifact.Redaction{Rule: "remove"}
	applyHeaderRule(msg.Metadata, "grpc-status-details-bin", remove)
	applyHeaderRule(msg.Trailers, "grpc-status-details-bin", remove)
	if msg.StatusDetails != nil {
		msg.StatusDetails.Wire = nil
		msg.StatusDetails.State = "redacted"
		msg.StatusDetails.Diagnostic = "original status envelope omitted after configured redaction"
	}
}

func redactWholeStatusDetails(msg *artifact.GrpcMessage) {
	removeStatusEnvelope(msg)
	// google.rpc.Status.message commonly duplicates grpc-message. A whole
	// envelope rule must not expose that same message via another view.
	remove := artifact.Redaction{Rule: "remove"}
	applyHeaderRule(msg.Metadata, "grpc-message", remove)
	applyHeaderRule(msg.Trailers, "grpc-message", remove)
	msg.StatusMessage = ""
	if msg.StatusDetails == nil {
		return
	}
	msg.StatusDetails.Message = ""
	for i := range msg.StatusDetails.Details {
		detail := &msg.StatusDetails.Details[i]
		detail.Wire, detail.Body = nil, nil
		detail.State, detail.Diagnostic = "redacted", "detail omitted after whole status metadata redaction"
	}
}
