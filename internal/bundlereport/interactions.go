package bundlereport

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

func applicationName(run *bundle.Run) string {
	if run.Application != nil {
		if run.Application.Name != "" {
			return run.Application.Name
		}
		if run.Application.ID != "" {
			return run.Application.ID
		}
	}
	if run.APIID != "" {
		return run.APIID
	}
	return "Unidentified system"
}

func protocolLabel(protocol string) string {
	switch protocol {
	case "http":
		return "HTTP"
	case "grpc":
		return "gRPC"
	case "graphql":
		return "GraphQL"
	case "kafka":
		return "Kafka"
	default:
		return orUnknown(protocol)
	}
}

func schemaProtocol(kind string) string {
	switch kind {
	case "openapi":
		return "http"
	case "protobuf":
		return "grpc"
	case "graphql":
		return "graphql"
	default:
		// Serialization formats and contract languages do not identify transport.
		return ""
	}
}

func interactionNote(interaction *artifact.Interaction) string {
	kind := interaction.Kind
	if interaction.StreamMode != "" {
		kind += " (" + interaction.StreamMode + ")"
	}
	return fmt.Sprintf("%s interaction over %s; capture %s. Events are shown in recorded observation order; sent/received are relative to the application. Cross-direction observation order does not establish causality. Contract coverage and execution of event interactions are not supported by this version.", kind, interaction.Transport, interaction.CaptureState)
}

type viewerEvent struct {
	Sequence                                         uint64
	DirectionSequence                                uint64
	ID, Kind, Direction, ObservedAt, Metadata, Links string
	Payloads                                         []viewerEventPayload
}

type viewerEventPayload struct {
	Name, Encoding, MediaType, Availability, MessageType, Body, Attachment, Note, Provenance string
}

func viewerEvents(interaction *artifact.Interaction) []viewerEvent {
	var events []viewerEvent
	for _, event := range interaction.Events {
		view := viewerEvent{ID: event.ID, Sequence: event.Sequence, DirectionSequence: event.DirectionSequence, Kind: event.Kind, Direction: event.Direction}
		if !event.ObservedAt.IsZero() {
			view.ObservedAt = event.ObservedAt.Format("2006-01-02T15:04:05.999999999Z07:00")
		}
		if len(event.Metadata) > 0 {
			data, _ := json.MarshalIndent(event.Metadata, "", "  ")
			view.Metadata = viewerPreview(string(data))
		}
		if len(event.Links) > 0 {
			data, _ := json.MarshalIndent(event.Links, "", "  ")
			view.Links = viewerPreview(string(data))
		}
		for _, payload := range event.Payloads {
			p := viewerEventPayload{Name: payload.Name, Encoding: payload.Encoding, MediaType: payload.MediaType, Availability: payload.Availability, MessageType: payload.MessageType}
			provenance := payload
			provenance.Body = nil
			data, _ := json.MarshalIndent(provenance, "", "  ")
			p.Provenance = string(data)
			if payload.Body != nil {
				raw := []byte(payload.Body.Text)
				if payload.Body.Encoding == "base64" || payload.Body.Bytes != "" {
					decoded, err := base64.StdEncoding.DecodeString(payload.Body.Bytes)
					if err != nil {
						p.Note = "Retained payload could not be decoded"
					} else {
						raw = decoded
						p.Note = "Binary payload"
					}
				} else {
					p.Body = viewerPreview(payload.Body.Text)
				}
				if p.Note != "Retained payload could not be decoded" {
					p.Attachment = fmt.Sprintf("payloads/%x.bin", sha256.Sum256(raw))
				}
				if payload.Body.Truncated {
					p.Note += " Capture truncated."
				}
			}
			view.Payloads = append(view.Payloads, p)
		}
		events = append(events, view)
	}
	return events
}

func writeInteractionMarkdown(page *strings.Builder, flow artifact.Flow) {
	page.WriteString(escape(interactionNote(flow.Interaction)) + "\n\n")
	for _, event := range viewerEvents(flow.Interaction) {
		fmt.Fprintf(page, "### Event %d: %s %s\n\n", event.Sequence, escape(event.Direction), escape(event.Kind))
		fmt.Fprintf(page, "Event ID: `%s`; direction sequence: %d. Observed at: %s.\n\n", escape(event.ID), event.DirectionSequence, escape(orUnknown(event.ObservedAt)))
		if event.Metadata != "" {
			page.WriteString("Protocol metadata:\n\n" + preview(event.Metadata))
		}
		if event.Links != "" {
			page.WriteString("Qualified correlation links:\n\n" + preview(event.Links))
		}
		for _, payload := range event.Payloads {
			fmt.Fprintf(page, "#### %s\n\nAvailability: **%s**; encoding: `%s`; media type: `%s`. %s\n\n", escape(payload.Name), escape(payload.Availability), escape(payload.Encoding), escape(payload.MediaType), escape(payload.Note))
			if payload.Attachment != "" {
				fmt.Fprintf(page, "[Retained payload](../../%s).\n\n", payload.Attachment)
			}
			if payload.Body != "" {
				page.WriteString(preview(payload.Body))
			}
			page.WriteString("Payload schema and transformation metadata:\n\n" + preview(payload.Provenance))
		}
	}
}
