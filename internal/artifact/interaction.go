package artifact

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Interaction is the event representation of an API boundary conversation.
// Its events are observations, not executable instructions or success criteria.
// The pair representation in Flow.Request/Response must be empty when present.
type Interaction struct {
	Kind         string             `json:"kind"`
	Boundary     BoundaryRef        `json:"boundary"`
	Transport    string             `json:"transport"`
	Operation    string             `json:"operation"`
	CaptureState string             `json:"capture_state"`
	StreamMode   string             `json:"stream_mode,omitempty"`
	Events       []InteractionEvent `json:"events"`
}

type BoundaryRef struct {
	Role string `json:"role"`
	ID   string `json:"id"`
}

// Sequence records order at one observation point. It is not a distributed
// happens-before relation. Direction is relative to the application under test.
type InteractionEvent struct {
	ID                string                     `json:"id"`
	Sequence          uint64                     `json:"sequence"`
	Direction         string                     `json:"direction"`
	DirectionSequence uint64                     `json:"direction_sequence"`
	Kind              string                     `json:"kind"`
	ObservedAt        time.Time                  `json:"observed_at,omitempty"`
	Metadata          map[string]json.RawMessage `json:"metadata,omitempty"`
	Payloads          []EventPayload             `json:"payloads,omitempty"`
	Links             []EventLink                `json:"links,omitempty"`
}

// EventPayload keeps encodings (Avro, Protobuf, JSON...) independent of API and
// transport protocols. Empty bytes and null/tombstones are distinct values.
type EventPayload struct {
	Name           string        `json:"name"`
	Encoding       string        `json:"encoding"`
	MediaType      string        `json:"media_type,omitempty"`
	Availability   string        `json:"availability"`
	MessageType    string        `json:"message_type,omitempty"`
	WriterSchema   *ContractLink `json:"writer_schema,omitempty"`
	ReaderSchema   *ContractLink `json:"reader_schema,omitempty"`
	DerivedFrom    string        `json:"derived_from,omitempty"`
	Transformation string        `json:"transformation,omitempty"`
	Body           *Body         `json:"body,omitempty"`
}

// A contract link pins the frozen file bytes, not a mutable registry schema ID.
type ContractLink struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// Links qualify associations without turning timing into causal proof. External
// references pin a test snapshot and remain unresolved until explicitly loaded.
type EventLink struct {
	FlowID        string `json:"flow_id"`
	EventID       string `json:"event_id,omitempty"`
	Relation      string `json:"relation"`
	Evidence      string `json:"evidence"`
	RunID         string `json:"run_id,omitempty"`
	AttemptID     string `json:"attempt_id,omitempty"`
	AttemptSHA256 string `json:"attempt_sha256,omitempty"`
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func identifier(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func hexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// ValidateInteractions validates the recording structure and payload references,
// not business contracts or the correctness of observed protocol behavior.
func ValidateInteractions(capture *Artifact) error {
	flows := map[string]*Flow{}
	for i := range capture.Traffic {
		f := &capture.Traffic[i]
		if f.ID == "" || flows[f.ID] != nil {
			return fmt.Errorf("duplicate or empty flow ID %q", f.ID)
		}
		flows[f.ID] = f
	}
	for _, f := range capture.Traffic {
		v := f.Interaction
		if v == nil {
			continue
		}
		fail := func(reason string) error { return fmt.Errorf("interaction %q: %s", f.ID, reason) }
		if !identifier(f.ID) || strings.TrimSpace(f.Protocol) == "" {
			return fail("missing protocol or invalid ID")
		}
		if f.Request.HTTP != nil || f.Request.GRPC != nil || f.Response.HTTP != nil || f.Response.GRPC != nil {
			return fail("event and pair representations cannot be combined")
		}
		if f.Channel != "" || f.AssetID != "" {
			return fail("event interactions use boundary, not channel/asset_id")
		}
		if !oneOf(v.Kind, "request_response", "publish", "consume", "stream") {
			return fail("unsupported interaction kind")
		}
		if !oneOf(v.Boundary.Role, "provided", "dependency") || strings.TrimSpace(v.Boundary.ID) == "" {
			return fail("invalid boundary")
		}
		if v.Transport == "" || v.Operation == "" || !oneOf(v.CaptureState, "complete", "incomplete") {
			return fail("missing transport, operation or capture state")
		}
		if v.Kind == "stream" {
			if !oneOf(v.StreamMode, "client", "server", "bidirectional", "subscription") {
				return fail("stream requires an explicit mode")
			}
		} else if v.StreamMode != "" {
			return fail("stream mode on a non-stream interaction")
		}
		if v.Events == nil {
			return fail("missing events array")
		}
		eventIDs := map[string]bool{}
		directions := map[string]uint64{}
		for n, event := range v.Events {
			if !identifier(event.ID) || eventIDs[event.ID] {
				return fail("duplicate or invalid event ID")
			}
			eventIDs[event.ID] = true
			if event.Sequence != uint64(n) || !oneOf(event.Direction, "sent", "received") || event.DirectionSequence != directions[event.Direction] {
				return fail("invalid observed or directional event sequence")
			}
			directions[event.Direction]++
			if !oneOf(event.Kind, "headers", "message", "half_close", "trailers", "complete", "cancel", "error", "ack") {
				return fail("unsupported event kind")
			}
			payloads := map[string]EventPayload{}
			for _, p := range event.Payloads {
				if !identifier(p.Name) || p.Encoding == "" {
					return fail("invalid payload name or missing encoding")
				}
				if _, exists := payloads[p.Name]; exists {
					return fail("duplicate payload name")
				}
				payloads[p.Name] = p
				if !oneOf(p.Availability, "present", "empty", "null", "redacted", "truncated", "unavailable") {
					return fail("invalid payload availability")
				}
				retained := oneOf(p.Availability, "present", "empty", "truncated")
				if retained != (p.Body != nil) {
					return fail("payload availability disagrees with retained bytes")
				}
				if p.Body != nil {
					if !utf8.ValidString(p.Body.Text) {
						return fail("payload text is not UTF-8; use binary bytes")
					}
					if p.Body.Decoded != nil {
						return fail("decoded data must be a separately referenced payload")
					}
					if p.Body.Text != "" && (p.Body.Bytes != "" || p.Body.Encoding == "base64") {
						return fail("payload contains both text and binary bytes")
					}
					size := len(p.Body.Text)
					if p.Body.Bytes != "" || p.Body.Encoding == "base64" {
						data, err := base64.StdEncoding.DecodeString(p.Body.Bytes)
						if err != nil {
							return fail("invalid base64 payload")
						}
						size = len(data)
					}
					if p.Availability == "empty" && size != 0 || p.Availability == "present" && size == 0 {
						return fail("empty/present payload disagrees with bytes")
					}
					if p.Body.Truncated != (p.Availability == "truncated") {
						return fail("payload truncation disagrees with availability")
					}
				}
				for _, link := range []*ContractLink{p.WriterSchema, p.ReaderSchema} {
					if link == nil {
						continue
					}
					var matched *SchemaRef
					for _, schema := range capture.Schemas {
						role := schema.Role
						if role == "sut" {
							role = "provided"
						}
						if schema.ID == link.ID && schema.Source.SHA256 == link.SHA256 && hexDigest(link.SHA256) && role == v.Boundary.Role && schema.AssetID == v.Boundary.ID {
							if matched != nil {
								return fail("payload references an ambiguous boundary contract snapshot")
							}
							copy := schema
							matched = &copy
						}
					}
					if matched == nil {
						return fail("payload references an unavailable boundary contract snapshot")
					}
				}
			}
			for _, p := range event.Payloads {
				if p.DerivedFrom == "" {
					if p.Transformation != "" {
						return fail("transformation requires derived_from")
					}
					continue
				}
				source, ok := payloads[p.DerivedFrom]
				if !ok || p.DerivedFrom == p.Name || source.DerivedFrom != "" || p.Transformation == "" {
					return fail("invalid payload projection reference")
				}
				if source.Availability == "redacted" && p.Availability != "redacted" && p.Availability != "unavailable" {
					return fail("redacted source retains a projection")
				}
				if p.Availability == "redacted" && source.Body != nil {
					return fail("redacted projection retains its source bytes")
				}
			}
			for _, link := range event.Links {
				if link.FlowID == "" || !oneOf(link.Relation, "caused_by", "reply_to", "delivery_of", "acknowledges") || !oneOf(link.Evidence, "trace", "protocol", "hook", "timing", "unknown") {
					return fail("invalid event link")
				}
				if link.RunID != "" || link.AttemptID != "" || link.AttemptSHA256 != "" {
					if !identifier(link.RunID) || !identifier(link.AttemptID) || !hexDigest(link.AttemptSHA256) {
						return fail("external event link must pin run, attempt and digest")
					}
					continue
				}
				target := flows[link.FlowID]
				if target == nil {
					return fail("event link references missing flow")
				}
				if link.EventID != "" {
					found := false
					if target.Interaction != nil {
						for _, other := range target.Interaction.Events {
							if other.ID == link.EventID {
								found = true
								break
							}
						}
					}
					if !found {
						return fail("event link references missing event")
					}
				}
			}
		}
	}
	return nil
}
