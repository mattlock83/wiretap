package bundlereport

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
)

const grpcTimelineNote = "Request means caller → callee; response means callee → caller at this API boundary. Sequence numbers record the proxy's observation order, not application causality. Times are offsets from this RPC's start. Message indices are zero-based within each direction."

type grpcTimelineEntry struct {
	Sequence                               uint64
	OffsetNS                               int64
	Offset, Direction, Kind, Label, Anchor string
}

// Messages remain on their original direction. The timeline links to them, so
// a bidirectional stream never duplicates payload bytes or becomes several RPCs.
func grpcTimeline(flow artifact.Flow, anchor func(string, int) string) []grpcTimelineEntry {
	var rows []grpcTimelineEntry
	add := func(sequence uint64, offset int64, direction, kind, label, link string) {
		rows = append(rows, grpcTimelineEntry{Sequence: sequence, OffsetNS: offset, Offset: time.Duration(offset).String(), Direction: direction, Kind: kind, Label: label, Anchor: link})
	}
	for _, event := range flow.GRPCEvents {
		add(event.Sequence, event.OffsetNS, event.Direction, event.Kind, strings.ReplaceAll(event.Kind, "_", " "), "")
	}
	for _, part := range []struct {
		direction string
		message   *artifact.GrpcMessage
	}{{"request", flow.Request.GRPC}, {"response", flow.Response.GRPC}} {
		if part.message == nil {
			continue
		}
		for _, payload := range part.message.Messages {
			if payload.Observation == nil {
				continue
			}
			o := payload.Observation
			add(o.StartSequence, o.StartedOffsetNS, part.direction, "message_started", fmt.Sprintf("Message %d started", payload.Index), anchor(part.direction, payload.Index))
			add(o.CompleteSequence, o.CompletedOffsetNS, part.direction, "message_complete", fmt.Sprintf("Message %d complete · %d wire bytes", payload.Index, o.WireBytes), anchor(part.direction, payload.Index))
		}
		if part.message.Observation != nil && part.message.Observation.Partial != nil {
			p := part.message.Observation.Partial
			add(p.StartSequence, p.StartedOffsetNS, part.direction, "partial_message", fmt.Sprintf("Message %d incomplete · %d/5 header bytes, %d/%d payload bytes observed", p.Index, p.HeaderBytes, p.ObservedPayloadBytes, p.WireBytes), "")
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Sequence < rows[j].Sequence })
	return rows
}

func grpcObservationSummary(observation *artifact.GrpcObservation) string {
	if observation == nil {
		return "Message counts, timing and direction completion were not recorded; retained payload count is not a total."
	}
	o := observation
	summary := fmt.Sprintf("%d complete frames observed; %d retained; %d omitted; %d bytes observed (including envelopes).", o.CompleteFrames, o.RetainedFrames, o.OmittedFrames, o.ObservedBytes)
	if o.CountsComplete {
		summary += " Counts are complete for the observed interval."
	} else {
		summary += " Total counts are unknown beyond the valid observed prefix."
	}
	if o.EOF {
		summary += " EOF observed."
	} else {
		summary += " EOF not observed; direction end: " + o.End + "."
	}
	if o.Partial != nil {
		p := o.Partial
		summary += fmt.Sprintf(" Incomplete message %d: %d/5 header bytes, %d/%d payload bytes observed; no completion time.", p.Index, p.HeaderBytes, p.ObservedPayloadBytes, p.WireBytes)
	}
	if o.Omitted != nil {
		summary += fmt.Sprintf(" Omitted message indices: %d–%d.", o.Omitted.FirstIndex, o.Omitted.FirstIndex+o.Omitted.Count-1)
	}
	if o.Error != "" {
		summary += " Observation error: " + o.Error
	}
	return summary
}

func grpcPayloadTiming(observation *artifact.GrpcPayloadObservation) string {
	if observation == nil {
		return "Message timing and wire length were not recorded."
	}
	return fmt.Sprintf("Started +%s (sequence %d); complete +%s (sequence %d); %d wire payload bytes.", time.Duration(observation.StartedOffsetNS), observation.StartSequence, time.Duration(observation.CompletedOffsetNS), observation.CompleteSequence, observation.WireBytes)
}

func grpcMessageAnchor(prefix, direction string, index int) string {
	return "grpc-" + shortHash(prefix) + "-" + direction + fmt.Sprintf("-%d", index)
}

func writeGRPCTimeline(page *strings.Builder, flow artifact.Flow) {
	rows := grpcTimeline(flow, func(direction string, index int) string { return grpcMessageAnchor(flow.ID, direction, index) })
	if len(rows) == 0 {
		return
	}
	page.WriteString("### Observed RPC timeline\n\n" + grpcTimelineNote + "\n\n")
	page.WriteString("| Sequence | Offset | Direction | Observation |\n| --- | --- | --- | --- |\n")
	for _, row := range rows {
		label := tableCell(row.Label)
		if row.Anchor != "" {
			label = "[" + label + "](#" + row.Anchor + ")"
		}
		fmt.Fprintf(page, "| %d | +%s | %s | %s |\n", row.Sequence, row.Offset, row.Direction, label)
	}
	page.WriteString("\n")
}

type knowledgeGRPCCapture struct {
	StreamKind   string                 `json:"stream_kind"`
	OrderMeaning string                 `json:"order_meaning"`
	Request      knowledgeGRPCDirection `json:"request"`
	Response     knowledgeGRPCDirection `json:"response"`
	Lifecycle    []artifact.GrpcEvent   `json:"lifecycle_events"`
}
type knowledgeGRPCDirection struct {
	Observation *artifact.GrpcObservation `json:"observation"`
	Messages    []knowledgeGRPCPayload    `json:"messages"`
}
type knowledgeGRPCPayload struct {
	Index       int                              `json:"message_index"`
	Type        string                           `json:"type,omitempty"`
	Observation *artifact.GrpcPayloadObservation `json:"observation"`
	JSONPath    string                           `json:"json_path,omitempty"`
	WirePath    string                           `json:"wire_path,omitempty"`
	Redacted    bool                             `json:"redacted,omitempty"`
	DecodeError string                           `json:"decode_error,omitempty"`
	DecodeState string                           `json:"decode_state,omitempty"`
}

func knowledgeGRPC(flow artifact.Flow) *knowledgeGRPCCapture {
	if flow.Request.GRPC == nil || flow.Response.GRPC == nil {
		return nil
	}
	direction := func(message *artifact.GrpcMessage) knowledgeGRPCDirection {
		result := knowledgeGRPCDirection{Observation: message.Observation, Messages: []knowledgeGRPCPayload{}}
		for _, payload := range message.Messages {
			p := knowledgeGRPCPayload{Index: payload.Index, Type: payload.Type, Observation: payload.Observation, Redacted: payload.Redacted, DecodeError: payload.DecodeError, DecodeState: payload.DecodeState}
			if attachment := statusAttachment(payload.Body, "", ""); attachment != nil {
				p.JSONPath = attachment.URL
			}
			if attachment := statusAttachment(payload.Wire, "", ""); attachment != nil {
				p.WirePath = attachment.URL
			}
			result.Messages = append(result.Messages, p)
		}
		return result
	}
	return &knowledgeGRPCCapture{StreamKind: flow.Request.GRPC.StreamKind, OrderMeaning: grpcTimelineNote, Request: direction(flow.Request.GRPC), Response: direction(flow.Response.GRPC), Lifecycle: append([]artifact.GrpcEvent{}, flow.GRPCEvents...)}
}

type viewerGRPCPayload struct {
	Index                            int
	Anchor, Type, Timing, Body, Note string
	Attachments                      []viewerAttachment
}

func viewerGRPCPayloads(message *artifact.GrpcMessage) []viewerGRPCPayload {
	var views []viewerGRPCPayload
	for _, payload := range message.Messages {
		view := viewerGRPCPayload{Index: payload.Index, Type: payload.Type, Timing: grpcPayloadTiming(payload.Observation), Note: payload.DecodeError}
		if payload.DecodeState != "" {
			view.Note = "Payload decoding: " + payload.DecodeState + ". " + view.Note
		}
		if payload.Compressed {
			view.Note += " Compressed on the wire."
		}
		if payload.Redacted {
			view.Note += " Fields redacted before persistence."
		}
		if payload.Body != nil {
			view.Body = viewerPreview(payload.Body.Text)
		} else {
			view.Note += " No decoded payload retained."
		}
		if attachment := statusAttachment(payload.Body, "../", "Retained JSON payload"); attachment != nil {
			view.Attachments = append(view.Attachments, *attachment)
		}
		if attachment := statusAttachment(payload.Wire, "../", "Retained Protobuf wire message"); attachment != nil {
			view.Attachments = append(view.Attachments, *attachment)
		}
		views = append(views, view)
	}
	return views
}
