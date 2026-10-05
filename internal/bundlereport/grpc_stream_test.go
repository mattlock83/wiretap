package bundlereport

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/grpccontract"
)

func streamReportFixture(t *testing.T) (*artifact.Artifact, *bundle.Writer) {
	t.Helper()
	work := t.TempDir()
	source := `syntax = "proto3"; package stream; message Value { string value = 1; } service Chat { rpc Exchange(stream Value) returns (stream Value); }`
	if err := os.WriteFile(filepath.Join(work, "stream.proto"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	contract, err := grpccontract.Resolve(t.Context(), "http://127.0.0.1:1", grpccontract.Source{Kind: "proto", Files: []string{"stream.proto"}, ImportPaths: []string{work}}, []string{"stream.Chat"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(bundle.Identity{APIID: "chat", SUTTargetKind: "service"}); err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "bidi", Name: "interleaved chat", Status: "pass", StartedAt: time.Now().UTC()}
	schemas, err := w.Register(tc, []artifact.SchemaRef{{ID: "chat", AssetID: "chat", Role: "sut", Type: "protobuf", Services: []string{"stream.Chat"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(contract.Bytes)}}})
	if err != nil {
		t.Fatal(err)
	}
	payload := func(index int, value string, sequence uint64) artifact.GrpcPayload {
		wire := append([]byte{10, byte(len(value))}, []byte(value)...)
		text, _ := json.Marshal(map[string]string{"value": value})
		return artifact.GrpcPayload{Index: index, Type: "stream.Value", DecodeState: "decoded",
			Body:        &artifact.Body{Encoding: "json", Text: string(text), OriginalBytes: int64(len(wire))},
			Wire:        &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(wire), OriginalBytes: int64(len(wire))},
			Observation: &artifact.GrpcPayloadObservation{WireBytes: uint64(len(wire)), StartedOffsetNS: int64(sequence) * 1e6, CompletedOffsetNS: int64(sequence+1) * 1e6, StartSequence: sequence, CompleteSequence: sequence + 1}}
	}
	message := func(payloads ...artifact.GrpcPayload) *artifact.GrpcMessage {
		var count int64
		for _, p := range payloads {
			count += 5 + p.Wire.OriginalBytes
		}
		return &artifact.GrpcMessage{Kind: "grpc", Procedure: "/stream.Chat/Exchange", Service: "stream.Chat", Method: "Exchange", SchemaRef: "chat", StreamKind: "bidirectional", ObservedBytes: count, Messages: payloads,
			Observation: &artifact.GrpcObservation{CompleteFrames: uint64(len(payloads)), RetainedFrames: uint64(len(payloads)), ObservedBytes: count, CountsComplete: true, EOF: true, End: "eof"}}
	}
	flow := artifact.Flow{ID: "chat-flow", Protocol: "grpc", Channel: "test_to_sut", StartedAt: tc.StartedAt, DurationMS: 12,
		Request:    artifact.Message{GRPC: message(payload(0, "one", 1), payload(1, "two", 6))},
		Response:   artifact.Message{GRPC: message(payload(0, "alpha", 4), payload(1, "omega", 9))},
		GRPCEvents: []artifact.GrpcEvent{{Sequence: 0, OffsetNS: 0, Direction: "request", Kind: "headers"}, {Sequence: 3, OffsetNS: 3e6, Direction: "response", Kind: "headers"}, {Sequence: 8, OffsetNS: 8e6, Direction: "request", Kind: "half_close"}, {Sequence: 11, OffsetNS: 11e6, Direction: "response", Kind: "end"}, {Sequence: 12, OffsetNS: 12e6, Direction: "response", Kind: "trailers"}}}
	flow.Response.GRPC.HTTPStatus = 200
	flow.Response.GRPC.StatusCode = 0
	flow.Response.GRPC.StatusSource = "trailers"
	if err := artifact.ValidateGRPCObservation(flow); err != nil {
		t.Fatal(err)
	}
	return &artifact.Artifact{Version: 2, TestCase: tc, Schemas: schemas, Traffic: []artifact.Flow{flow}, SchemaValidation: &artifact.SchemaValidation{Status: "pass"}}, w
}

func TestBidirectionalStreamViewsPreserveOneRPCAndInterleaving(t *testing.T) {
	capture, writer := streamReportFixture(t)
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(writer.Path()); err != nil {
		t.Fatal(err)
	}
	if err := Validate(writer.Path()); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(writer.Path(), name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	markdown := read("docs/tests/bidi.md")
	for _, expected := range []string{"Observed RPC timeline", "not application causality", "2 complete frames observed; 2 retained; 0 omitted", "EOF observed", "Method kind: `bidirectional`", "Message 0 complete", "Message 1 complete", `"value": "omega"`} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Markdown missing %q", expected)
		}
	}
	timeline := grpcTimeline(capture.Traffic[0], func(direction string, index int) string { return grpcMessageAnchor("chat-flow", direction, index) })
	if len(timeline) != 13 {
		t.Fatalf("timeline dropped events: %+v", timeline)
	}
	for i, row := range timeline {
		if row.Sequence != uint64(i) {
			t.Fatalf("cross-direction order wrong: %+v", timeline)
		}
	}
	if timeline[4].Direction != "response" || timeline[6].Direction != "request" || timeline[8].Kind != "half_close" {
		t.Fatalf("stream flattened: %+v", timeline)
	}
	html := read("docs/viewer.html")
	for _, expected := range []string{"Observed RPC timeline", "Counts are complete for the observed interval", "Message 1", "Message 0 complete", "omega"} {
		if !strings.Contains(html, expected) {
			t.Errorf("viewer missing %q", expected)
		}
	}
	if strings.Count(html, "omega") != 1 {
		t.Fatal("viewer duplicates retained payload in timeline and message view")
	}
	var knowledge knowledgeIndex
	if err := json.Unmarshal([]byte(read("docs/knowledge.json")), &knowledge); err != nil {
		t.Fatal(err)
	}
	if len(knowledge.Attempts) != 1 || len(knowledge.APIs) != 1 || len(knowledge.APIs[0].Operations[0].Evidence) != 1 {
		t.Fatalf("stream became several exchanges: %+v", knowledge.APIs)
	}
	evidence := knowledge.APIs[0].Operations[0].Evidence[0]
	stream := evidence.GRPCCapture
	if evidence.GRPCStatus == nil || *evidence.GRPCStatus != 0 || stream == nil || stream.StreamKind != "bidirectional" || len(stream.Request.Messages) != 2 || len(stream.Response.Messages) != 2 || len(stream.Lifecycle) != 5 {
		t.Fatalf("knowledge lost RPC details: %+v", evidence)
	}
	for _, direction := range []knowledgeGRPCDirection{stream.Request, stream.Response} {
		if direction.Observation == nil || direction.Observation.CompleteFrames != 2 {
			t.Fatal("knowledge lost exact counters")
		}
		for _, payload := range direction.Messages {
			for _, attachment := range []string{payload.JSONPath, payload.WirePath} {
				if attachment == "" {
					t.Fatal("missing message reference")
				}
				if _, err := os.Stat(filepath.Join(writer.Path(), attachment)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestStreamQualityNeverInventsTotalsOrCompletion(t *testing.T) {
	observation := &artifact.GrpcObservation{CompleteFrames: 4, RetainedFrames: 1, OmittedFrames: 3, ObservedBytes: 33, End: "error", Error: "broken frame", Omitted: &artifact.GrpcOmission{FirstIndex: 1, Count: 3}, Partial: &artifact.GrpcPartialFrame{Index: 4, HeaderBytes: 3, StartedOffsetNS: 100}}
	summary := grpcObservationSummary(observation)
	for _, expected := range []string{"4 complete frames observed; 1 retained; 3 omitted", "Total counts are unknown", "EOF not observed", "Incomplete message 4", "no completion time", "Omitted message indices: 1–3", "broken frame"} {
		if !strings.Contains(summary, expected) {
			t.Errorf("missing %q: %s", expected, summary)
		}
	}
	if !strings.Contains(grpcObservationSummary(nil), "not a total") {
		t.Fatal("legacy retained count used as observed total")
	}
	if !strings.Contains(grpcPayloadTiming(nil), "not recorded") {
		t.Fatal("missing timing fabricated")
	}
}

func TestPartialGRPCValidationReasonsRemainVisible(t *testing.T) {
	capture, writer := streamReportFixture(t)
	capture.SchemaValidation = &artifact.SchemaValidation{Status: "partial", Unavailable: []artifact.SchemaValidationError{{FlowID: "chat-flow", Phase: "response", SchemaID: "chat", Message: "custom Any type unavailable in frozen contract"}}}
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(writer.Path()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docs/tests/bidi.md", "docs/viewer.html"} {
		data, err := os.ReadFile(filepath.Join(writer.Path(), name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "Validation unavailable") || !strings.Contains(string(data), "custom Any type unavailable") {
			t.Fatalf("%s hid partial validation reason", name)
		}
	}
}
