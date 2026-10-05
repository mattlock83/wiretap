package bundlereport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
)

func TestEventRecordingPreservesInterleavingAndQualifiedMeaning(t *testing.T) {
	flow := artifact.Flow{ID: "chat", Protocol: "grpc", Interaction: &artifact.Interaction{Kind: "stream", StreamMode: "bidirectional", Boundary: artifact.BoundaryRef{Role: "dependency", ID: "chat"}, Transport: "http2", Operation: "/chat.Chat/Connect", CaptureState: "complete", Events: []artifact.InteractionEvent{
		{ID: "sent", Direction: "sent", Kind: "message", Payloads: []artifact.EventPayload{{Name: "wire", Encoding: "protobuf", Availability: "empty", Body: &artifact.Body{Encoding: "base64"}}}},
		{ID: "received", Sequence: 1, Direction: "received", Kind: "message"},
		{ID: "half-close", Sequence: 2, DirectionSequence: 1, Direction: "sent", Kind: "half_close"},
	}}}
	capture := &artifact.Artifact{TestCase: artifact.TestCase{ID: "ui-test", Name: "chat UI", Status: "pass"}, Traffic: []artifact.Flow{flow}}
	run := &bundle.Run{RunID: "run", RunState: "complete", DependencyKinds: map[string]string{"chat": "service"}, Attempts: []bundle.AttemptRef{{AttemptID: "ui-test", FileRef: bundle.FileRef{Path: "tests/ui-test.json"}}}}
	run.Application = &bundle.Application{Name: "Chat UI", ID: "chat-ui", Description: "Browser application"}
	markdown := attemptMarkdown(run, capture, "tests/ui-test.json")
	for _, want := range []string{"Event 0: sent message", "Event 1: received message", "Event 2: sent half", "not supported by this version", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855.bin"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("missing %q in %s", want, markdown)
		}
	}
	view := viewerEvents(flow.Interaction)
	if len(view) != 3 || view[1].Direction != "received" || view[0].Payloads[0].Attachment == "" {
		t.Fatalf("bad event view: %+v", view)
	}
	knowledge := buildKnowledge(t.TempDir(), run, []*artifact.Artifact{capture}, &coverage.Report{})
	if len(knowledge.APIs) != 1 || knowledge.APIs[0].Role != "dependency" || knowledge.APIs[0].Protocol != "grpc" {
		t.Fatalf("consumer-only recording manufactured a provider: %+v", knowledge.APIs)
	}
	op := knowledge.APIs[0].Operations[0]
	if op.ContractMatch != "unsupported" || len(op.RPCStatuses) != 0 || op.Evidence[0].Measurement != "unsupported" {
		t.Fatalf("unsupported stream gained an assertion: %+v", op)
	}
	root := t.TempDir()
	if err := writeViewer(root, run, []*artifact.Artifact{capture}, &coverage.Report{}, nil); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(root, "docs", "viewer.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1>Chat UI</h1>", "Browser application", "sent message", "received message", "half_close", "coverage is not applicable", "../payloads/e3b0"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("viewer missing %q", want)
		}
	}
}

func TestSchemaProtocolDoesNotInferHTTPForOpaqueContracts(t *testing.T) {
	for _, kind := range []string{"avro", "asyncapi", "unknown"} {
		if schemaProtocol(kind) != "" {
			t.Fatalf("invented protocol for %s", kind)
		}
	}
	if protocolLabel("kafka") != "Kafka" || protocolLabel("graphql") != "GraphQL" {
		t.Fatal("protocol label lost")
	}
}

func TestEventBundleReportsRoundTripForSingleRoleSystems(t *testing.T) {
	for _, role := range []string{"provided", "dependency"} {
		t.Run(role, func(t *testing.T) {
			w, err := bundle.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = w.Configure(bundle.Identity{Application: &bundle.Application{ID: "chat-app", Name: "Chat application"}}); err != nil {
				t.Fatal(err)
			}
			tc := artifact.TestCase{ID: "stream-test", Name: "chat stream", Status: "pass"}
			refs, err := w.Register(tc, nil)
			if err != nil {
				t.Fatal(err)
			}
			capture := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: refs, Traffic: []artifact.Flow{{ID: "chat", Protocol: "grpc", Interaction: &artifact.Interaction{
				Kind: "stream", StreamMode: "bidirectional", Boundary: artifact.BoundaryRef{Role: role, ID: "chat"}, Transport: "http2", Operation: "/chat.Chat/Connect", CaptureState: "complete",
				Events: []artifact.InteractionEvent{
					{ID: "sent", Direction: "sent", Kind: "message", Payloads: []artifact.EventPayload{{Name: "body", Encoding: "json", Availability: "present", Body: &artifact.Body{Encoding: "json", Text: `{"message":"hello"}`}}}},
					{ID: "received", Sequence: 1, Direction: "received", Kind: "message"},
					{ID: "half-close", Sequence: 2, DirectionSequence: 1, Direction: "sent", Kind: "half_close"},
					{ID: "complete", Sequence: 3, DirectionSequence: 1, Direction: "received", Kind: "complete", Metadata: map[string]json.RawMessage{"grpc": json.RawMessage(`{"status_code":0}`)}},
				},
			}}}}
			if _, err = w.Commit(capture); err != nil {
				t.Fatal(err)
			}
			if err = w.Finalize("complete"); err != nil {
				t.Fatal(err)
			}
			report, err := Rebuild(w.Path())
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Unmeasured) != 1 {
				t.Fatalf("event interaction was silently ignored: %+v", report)
			}
			data, err := os.ReadFile(filepath.Join(w.Path(), "docs", "knowledge.json"))
			if err != nil {
				t.Fatal(err)
			}
			var knowledge knowledgeIndex
			if err = json.Unmarshal(data, &knowledge); err != nil {
				t.Fatal(err)
			}
			if len(knowledge.APIs) != 1 || knowledge.APIs[0].Role != role {
				t.Fatalf("invented another API role: %+v", knowledge.APIs)
			}
			data, err = os.ReadFile(filepath.Join(w.Path(), "docs", "viewer.html"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "Chat application") || !strings.Contains(string(data), "Retained payload") {
				t.Fatal("event viewer did not render retained payload")
			}
		})
	}
}

func TestProvidedOpaqueContractIsVisibleWithoutLegacyAPIIdentity(t *testing.T) {
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Configure(bundle.Identity{Application: &bundle.Application{ID: "app", Name: "Order application"}}); err != nil {
		t.Fatal(err)
	}
	if err = w.RegisterSchemas([]artifact.SchemaRef{{ID: "orders-contract", Role: "provided", AssetID: "orders", Type: "graphql", Source: artifact.SchemaSource{Type: "inline", Content: "type Query { orders: [ID!]! }"}}}); err != nil {
		t.Fatal(err)
	}
	if err = w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	report, err := Rebuild(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Provided.Specs) != 1 || report.Provided.Specs[0].DenominatorKnown || len(report.Contracts.Specs) != 1 || report.Contracts.Specs[0].APIID != "orders" {
		t.Fatalf("provided contract disappeared: %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(w.Path(), "docs", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	var knowledge knowledgeIndex
	if err = json.Unmarshal(data, &knowledge); err != nil {
		t.Fatal(err)
	}
	if len(knowledge.APIs) != 1 || knowledge.APIs[0].ID != "orders" || knowledge.APIs[0].Protocol != "graphql" || knowledge.APIs[0].Measurement != "unsupported" {
		t.Fatalf("bad provider inventory: %+v", knowledge.APIs)
	}
	data, err = os.ReadFile(filepath.Join(w.Path(), "docs", "viewer.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Provider coverage is not applicable") {
		t.Fatal("registered provider was presented as absent")
	}
}
