package artifact

import (
	"strings"
	"testing"
)

func interactionFixture() *Artifact {
	return &Artifact{Traffic: []Flow{{ID: "flow", Protocol: "grpc", Interaction: &Interaction{
		Kind: "stream", Boundary: BoundaryRef{Role: "provided", ID: "chat"},
		Transport: "http2", Operation: "/chat.Chat/Connect", CaptureState: "complete", StreamMode: "bidirectional",
		Events: []InteractionEvent{
			{ID: "a", Sequence: 0, Direction: "received", DirectionSequence: 0, Kind: "message", Payloads: []EventPayload{{Name: "wire", Encoding: "protobuf", Availability: "present", Body: &Body{Encoding: "base64", Bytes: "CgFh"}}}},
			{ID: "b", Sequence: 1, Direction: "sent", DirectionSequence: 0, Kind: "message"},
			{ID: "c", Sequence: 2, Direction: "received", DirectionSequence: 1, Kind: "half_close"},
			{ID: "d", Sequence: 3, Direction: "sent", DirectionSequence: 1, Kind: "trailers"},
		},
	}}}}
}

func TestInteractionStructureRejectsAmbiguity(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Artifact)
		want string
	}{
		{"pair and events", func(a *Artifact) { a.Traffic[0].Request.HTTP = &HttpMessage{} }, "cannot be combined"},
		{"legacy boundary", func(a *Artifact) { a.Traffic[0].Channel = "test_to_sut" }, "use boundary"},
		{"missing mode", func(a *Artifact) { a.Traffic[0].Interaction.StreamMode = "" }, "explicit mode"},
		{"missing events", func(a *Artifact) { a.Traffic[0].Interaction.Events = nil }, "events array"},
		{"duplicate ID", func(a *Artifact) { a.Traffic[0].Interaction.Events[1].ID = "a" }, "event ID"},
		{"global order", func(a *Artifact) { a.Traffic[0].Interaction.Events[1].Sequence = 2 }, "sequence"},
		{"direction order", func(a *Artifact) { a.Traffic[0].Interaction.Events[2].DirectionSequence = 2 }, "sequence"},
		{"direction", func(a *Artifact) { a.Traffic[0].Interaction.Events[0].Direction = "client" }, "sequence"},
		{"empty is not present", func(a *Artifact) { a.Traffic[0].Interaction.Events[0].Payloads[0].Body.Bytes = "" }, "disagrees with bytes"},
		{"null retains bytes", func(a *Artifact) { a.Traffic[0].Interaction.Events[0].Payloads[0].Availability = "null" }, "retained bytes"},
		{"invalid UTF-8", func(a *Artifact) {
			a.Traffic[0].Interaction.Events[0].Payloads[0].Body = &Body{Text: string([]byte{0xff, 0xfe})}
		}, "not UTF-8"},
		{"base64 does not discard text", func(a *Artifact) {
			p := &a.Traffic[0].Interaction.Events[0].Payloads[0]
			p.Availability, p.Body.Bytes, p.Body.Text = "empty", "", "must not be lost"
		}, "both text and binary"},
		{"truncation", func(a *Artifact) { a.Traffic[0].Interaction.Events[0].Payloads[0].Availability = "truncated" }, "truncation"},
		{"missing local flow", func(a *Artifact) {
			a.Traffic[0].Interaction.Events[0].Links = []EventLink{{FlowID: "missing", Relation: "reply_to", Evidence: "protocol"}}
		}, "missing flow"},
		{"missing local event", func(a *Artifact) {
			a.Traffic[0].Interaction.Events[0].Links = []EventLink{{FlowID: "flow", EventID: "missing", Relation: "reply_to", Evidence: "protocol"}}
		}, "missing event"},
		{"external without digest", func(a *Artifact) {
			a.Traffic[0].Interaction.Events[0].Links = []EventLink{{FlowID: "remote", Relation: "reply_to", Evidence: "protocol", RunID: "run", AttemptID: "attempt"}}
		}, "must pin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := interactionFixture()
			tc.edit(a)
			if err := ValidateInteractions(a); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestInteractionLinksDoNotInventCausality(t *testing.T) {
	a := interactionFixture()
	a.Traffic[0].Interaction.Events[1].Links = []EventLink{
		{FlowID: "flow", EventID: "a", Relation: "reply_to", Evidence: "timing"},
		{FlowID: "remote", EventID: "event", Relation: "delivery_of", Evidence: "unknown", RunID: "run", AttemptID: "test", AttemptSHA256: strings.Repeat("a", 64)},
	}
	if err := ValidateInteractions(a); err != nil {
		t.Fatal(err)
	}
	// Recording a protocol violation must remain possible: a half-close need not
	// be the final received observation, nor does a complete capture imply OK.
	a.Traffic[0].Interaction.Events[2].Kind = "cancel"
	if err := ValidateInteractions(a); err != nil {
		t.Fatal(err)
	}
}

func TestInteractionPayloadRequiresUniqueBoundaryContract(t *testing.T) {
	a := interactionFixture()
	hash := strings.Repeat("a", 64)
	a.Schemas = []SchemaRef{{ID: "schema", Type: "protobuf", Role: "provided", AssetID: "chat", Source: SchemaSource{Type: "file", Path: "contracts/first/root", SHA256: hash}}}
	a.Traffic[0].Interaction.Events[0].Payloads[0].WriterSchema = &ContractLink{ID: "schema", SHA256: hash}
	if err := ValidateInteractions(a); err != nil {
		t.Fatal(err)
	}
	a.Schemas[0].AssetID = "another-api"
	if err := ValidateInteractions(a); err == nil {
		t.Fatal("accepted a contract on another boundary")
	}
	a.Schemas[0].AssetID = "chat"
	other := a.Schemas[0]
	other.Source.Path = "contracts/other-imports/root"
	a.Schemas = append(a.Schemas, other)
	if err := ValidateInteractions(a); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("accepted ambiguous contract package reference: %v", err)
	}
}

func TestInteractionProjectionRedactionRemovesBothRepresentations(t *testing.T) {
	for _, redact := range []string{"source", "projection"} {
		t.Run(redact, func(t *testing.T) {
			a := interactionFixture()
			event := &a.Traffic[0].Interaction.Events[0]
			event.Payloads = append(event.Payloads, EventPayload{Name: "json", Encoding: "json", Availability: "present", DerivedFrom: "wire", Transformation: "protojson", Body: &Body{Text: `{"value":"a"}`}})
			if err := ValidateInteractions(a); err != nil {
				t.Fatal(err)
			}
			i := 0
			if redact == "projection" {
				i = 1
			}
			event.Payloads[i].Availability, event.Payloads[i].Body = "redacted", nil
			if err := ValidateInteractions(a); err == nil {
				t.Fatal("accepted leaked alternate representation")
			}
			event.Payloads[1-i].Availability, event.Payloads[1-i].Body = "redacted", nil
			if err := ValidateInteractions(a); err != nil {
				t.Fatal(err)
			}
		})
	}
}
