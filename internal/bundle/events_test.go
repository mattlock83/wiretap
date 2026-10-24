package bundle

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func eventFixture(t *testing.T, identity Identity, schemas []artifact.SchemaRef) (*Writer, *artifact.Artifact) {
	t.Helper()
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(identity); err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "test-1", Name: "portable interaction", StartedAt: time.Date(2026, 10, 5, 1, 2, 3, 0, time.UTC), Status: "pass"}
	frozen, err := w.Register(tc, schemas)
	if err != nil {
		t.Fatal(err)
	}
	return w, &artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen}
}

func loadEventFixture(t *testing.T, w *Writer, capture *artifact.Artifact) (*Run, *artifact.Artifact, Attempt) {
	t.Helper()
	before, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	name, err := w.Commit(capture)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(capture)
	if err != nil || string(before) != string(after) {
		t.Fatalf("commit mutated original capture: %v", err)
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	run, captures, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || !sameJSON(capture.Traffic, captures[0].Traffic) {
		t.Fatalf("interaction round trip changed recorded traffic: %+v", captures)
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var attempt Attempt
	if err := json.Unmarshal(raw, &attempt); err != nil {
		t.Fatal(err)
	}
	return run, captures[0], attempt
}

func observedEvents(events ...artifact.InteractionEvent) []artifact.InteractionEvent {
	directionCounts := map[string]uint64{}
	for i := range events {
		events[i].ID = fmt.Sprintf("event-%d", i)
		events[i].Sequence = uint64(i)
		events[i].DirectionSequence = directionCounts[events[i].Direction]
		directionCounts[events[i].Direction]++
		events[i].ObservedAt = time.Date(2026, 10, 5, 1, 2, 3, i, time.UTC)
	}
	return events
}

func jsonEventPayload(name, value string) artifact.EventPayload {
	return artifact.EventPayload{Name: name, Encoding: "json", MediaType: "application/json", Availability: "present", Body: &artifact.Body{Encoding: "json", Text: value}}
}

func binaryEventPayload(name, encoding string, raw []byte) artifact.EventPayload {
	availability := "present"
	if len(raw) == 0 {
		availability = "empty"
	}
	return artifact.EventPayload{Name: name, Encoding: encoding, Availability: availability, Body: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(raw)}}
}

func bidiContract(t *testing.T) (artifact.SchemaRef, protoreflect.MessageDescriptor) {
	t.Helper()
	file := &descriptorpb.FileDescriptorProto{
		Name: proto.String("chat.proto"), Package: proto.String("example.chat"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Message"), Field: []*descriptorpb.FieldDescriptorProto{{Name: proto.String("text"), Number: proto.Int32(1), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()}}}},
		Service:     []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Chat"), Method: []*descriptorpb.MethodDescriptorProto{{Name: proto.String("Exchange"), InputType: proto.String(".example.chat.Message"), OutputType: proto.String(".example.chat.Message"), ClientStreaming: proto.Bool(true), ServerStreaming: proto.Bool(true)}}}},
	}
	descriptor, err := protodesc.NewFile(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}})
	if err != nil {
		t.Fatal(err)
	}
	return artifact.SchemaRef{ID: "chat-contract", Role: "provided", AssetID: "chat", Type: "protobuf", Protocol: "grpc", Services: []string{"example.chat.Chat"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(raw)}}, descriptor.Messages().ByName("Message")
}

func chatPayloads(t *testing.T, descriptor protoreflect.MessageDescriptor, schema artifact.SchemaRef, text string) []artifact.EventPayload {
	t.Helper()
	message := dynamicpb.NewMessage(descriptor)
	message.Set(descriptor.Fields().ByName("text"), protoreflect.ValueOfString(text))
	wire, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := protojson.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	raw := binaryEventPayload("wire", "protobuf", wire)
	raw.MessageType = string(descriptor.FullName())
	raw.WriterSchema = &artifact.ContractLink{ID: schema.ID, SHA256: schema.Source.SHA256}
	decoded := jsonEventPayload("decoded", string(projection))
	decoded.DerivedFrom = "wire"
	decoded.Transformation = "protobuf-json-v1"
	decoded.ReaderSchema = &artifact.ContractLink{ID: schema.ID, SHA256: schema.Source.SHA256}
	return []artifact.EventPayload{raw, decoded}
}

func TestProviderOnlyBidirectionalGRPCEventBundles(t *testing.T) {
	for _, finish := range []string{"complete", "cancelled", "incomplete"} {
		t.Run(finish, func(t *testing.T) {
			schema, descriptor := bidiContract(t)
			w, capture := eventFixture(t, Identity{Application: &Application{Name: "Chat server", ID: "example/chat-server"}}, []artifact.SchemaRef{schema})
			events := []artifact.InteractionEvent{
				{Direction: "received", Kind: "headers", Metadata: map[string]json.RawMessage{"grpc": json.RawMessage(`{"metadata":{"content-type":["application/grpc"]}}`)}},
				{Direction: "received", Kind: "message", Payloads: chatPayloads(t, descriptor, capture.Schemas[0], "hello")},
				{Direction: "sent", Kind: "headers", Metadata: map[string]json.RawMessage{"grpc": json.RawMessage(`{"metadata":{"server":["chat"]}}`)}},
				{Direction: "sent", Kind: "message", Payloads: chatPayloads(t, descriptor, capture.Schemas[0], "welcome")},
				{Direction: "received", Kind: "message", Payloads: chatPayloads(t, descriptor, capture.Schemas[0], "another")},
			}
			state := "complete"
			switch finish {
			case "complete":
				events = append(events,
					artifact.InteractionEvent{Direction: "received", Kind: "half_close"},
					artifact.InteractionEvent{Direction: "sent", Kind: "message", Payloads: chatPayloads(t, descriptor, capture.Schemas[0], "goodbye")},
					artifact.InteractionEvent{Direction: "sent", Kind: "trailers", Metadata: map[string]json.RawMessage{"grpc": json.RawMessage(`{"status":0,"message":"","metadata":{"grpc-status":["0"]}}`)}},
					artifact.InteractionEvent{Direction: "sent", Kind: "complete"})
			case "cancelled":
				events = append(events, artifact.InteractionEvent{Direction: "received", Kind: "cancel", Metadata: map[string]json.RawMessage{"grpc": json.RawMessage(`{"status":1,"message":"caller cancelled"}`)}})
			case "incomplete":
				state = "incomplete"
				capture.CaptureIncomplete = true
				capture.CaptureDiagnostics = []string{"capture ended before terminal status was observed"}
			}
			capture.Traffic = []artifact.Flow{{ID: "chat-stream", Protocol: "grpc", Interaction: &artifact.Interaction{Kind: "stream", Boundary: artifact.BoundaryRef{Role: "provided", ID: "chat"}, Transport: "http2", Operation: "/example.chat.Chat/Exchange", CaptureState: state, StreamMode: "bidirectional", Events: observedEvents(events...)}}}
			run, loaded, attempt := loadEventFixture(t, w, capture)
			if len(run.ProvidedAPIs) != 1 || run.ProvidedAPIs[0].ID != "chat" || len(run.Dependencies) != 0 {
				t.Fatalf("provider-only inventory invented dependencies or lost API: %+v / %+v", run.ProvidedAPIs, run.Dependencies)
			}
			if !sameJSON(run.RequiredCapabilities, []string{eventCapability}) || !sameJSON(attempt.RequiredCapabilities, []string{eventCapability}) {
				t.Fatalf("missing event capability: %v / %v", run.RequiredCapabilities, attempt.RequiredCapabilities)
			}
			got := loaded.Traffic[0].Interaction
			if got.Events[1].DirectionSequence != 1 || got.Events[3].DirectionSequence != 1 || got.Events[4].DirectionSequence != 2 {
				t.Fatal("interleaving changed directional message order")
			}
			if finish == "incomplete" && (!loaded.CaptureIncomplete || got.CaptureState != "incomplete" || got.Events[len(got.Events)-1].Kind != "message") {
				t.Fatal("incomplete stream acquired a synthetic terminal status")
			}
			if finish == "cancelled" && got.Events[len(got.Events)-1].Kind != "cancel" {
				t.Fatal("cancellation was lost")
			}
		})
	}
}

func TestConsumerOnlyUIGraphQLEventBundle(t *testing.T) {
	w, capture := eventFixture(t, Identity{Application: &Application{Name: "Orders UI", ID: "example/orders-ui", Description: "Browser application"}, DependencyKinds: map[string]string{"orders": "service"}}, []artifact.SchemaRef{{ID: "graphql-contract", Role: "dependency", AssetID: "orders", Type: "graphql", Source: artifact.SchemaSource{Type: "inline", Content: "type Query { order(id: ID!): Order }\ntype Subscription { orders: Order }\ntype Order { id: ID!, total: Int! }\n"}}})
	queryEvents := observedEvents(
		artifact.InteractionEvent{Direction: "sent", Kind: "message", Metadata: map[string]json.RawMessage{"graphql": json.RawMessage(`{"operation_name":"GetOrder","operation_type":"query"}`)}, Payloads: []artifact.EventPayload{jsonEventPayload("body", `{"query":"query GetOrder($id: ID!) { order(id: $id) { id total } }","variables":{"id":"42"}}`)}},
		artifact.InteractionEvent{Direction: "received", Kind: "message", Metadata: map[string]json.RawMessage{"http": json.RawMessage(`{"status":200}`)}, Payloads: []artifact.EventPayload{jsonEventPayload("body", `{"data":{"order":null},"errors":[{"message":"unknown order","path":["order"]}]}`)}},
	)
	subscriptionEvents := observedEvents(
		artifact.InteractionEvent{Direction: "sent", Kind: "message", Metadata: map[string]json.RawMessage{"graphql": json.RawMessage(`{"operation_name":"WatchOrders","operation_type":"subscription","message_type":"subscribe","id":"sub-1"}`)}, Payloads: []artifact.EventPayload{jsonEventPayload("body", `{"query":"subscription WatchOrders { orders { id total } }"}`)}},
		artifact.InteractionEvent{Direction: "received", Kind: "message", Metadata: map[string]json.RawMessage{"graphql": json.RawMessage(`{"message_type":"next","id":"sub-1"}`)}, Payloads: []artifact.EventPayload{jsonEventPayload("body", `{"data":{"orders":{"id":"42","total":100}}}`)}},
		artifact.InteractionEvent{Direction: "received", Kind: "message", Metadata: map[string]json.RawMessage{"graphql": json.RawMessage(`{"message_type":"next","id":"sub-1"}`)}, Payloads: []artifact.EventPayload{jsonEventPayload("body", `{"data":{"orders":{"id":"43","total":200}}}`)}},
		artifact.InteractionEvent{Direction: "sent", Kind: "complete", Metadata: map[string]json.RawMessage{"graphql": json.RawMessage(`{"message_type":"complete","id":"sub-1"}`)}},
	)
	boundary := artifact.BoundaryRef{Role: "dependency", ID: "orders"}
	capture.Traffic = []artifact.Flow{
		{ID: "graphql-query", Protocol: "graphql", Interaction: &artifact.Interaction{Kind: "request_response", Boundary: boundary, Transport: "http1", Operation: "GetOrder", CaptureState: "complete", Events: queryEvents}},
		{ID: "graphql-subscription", Protocol: "graphql", Interaction: &artifact.Interaction{Kind: "stream", Boundary: boundary, Transport: "websocket", Operation: "WatchOrders", CaptureState: "complete", StreamMode: "subscription", Events: subscriptionEvents}},
	}
	run, loaded, _ := loadEventFixture(t, w, capture)
	if run.Application == nil || run.Application.ID != "example/orders-ui" || run.ProvidedAPIs == nil || len(run.ProvidedAPIs) != 0 {
		t.Fatalf("UI identity created a provided API: %+v", run)
	}
	if len(run.Dependencies) != 1 || run.Dependencies[0].ID != "orders" || !sameJSON(run.Dependencies[0].Protocols, []string{"graphql"}) {
		t.Fatalf("wrong GraphQL dependency inventory: %+v", run.Dependencies)
	}
	for _, flow := range loaded.Traffic {
		if flow.ParentFlowID != "" || flow.Channel != "" || flow.Interaction.Boundary.Role != "dependency" {
			t.Fatal("consumer-only recording acquired a synthetic inbound request")
		}
	}
	if !strings.Contains(loaded.Traffic[0].Interaction.Events[1].Payloads[0].Body.Text, `"errors"`) {
		t.Fatal("GraphQL response lost application errors under HTTP 200")
	}
}

func TestMixedHTTPKafkaAvroAndAsyncAPIEventBundle(t *testing.T) {
	w, capture := eventFixture(t, Identity{Application: &Application{Name: "Orders"}, APIID: "orders", SUTTargetKind: "service", DependencyKinds: map[string]string{"order-events": "service"}}, []artifact.SchemaRef{
		{ID: "orders-api", Role: "sut", AssetID: "orders", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: testSchema}},
		{ID: "events-api", Role: "dependency", AssetID: "order-events", Type: "asyncapi", Protocol: "kafka", Source: artifact.SchemaSource{Type: "package", Path: "events.yaml", Files: []artifact.ContractFile{{Path: "events.yaml", Content: "asyncapi: 3.0.0\ninfo: {title: Orders, version: 1.0.0}\nchannels:\n  created:\n    address: orders.created\n    messages:\n      created: {$ref: './messages/created.yaml'}\n"}, {Path: "messages/created.yaml", Content: "payload: {type: object, properties: {orderId: {type: integer}}}\n"}}}},
		{ID: "event-value", Role: "dependency", AssetID: "order-events", Type: "avro", Source: artifact.SchemaSource{Type: "inline", Content: `{"type":"record","name":"OrderCreated","fields":[{"name":"orderId","type":"long"}]}`}},
		{ID: "event-key", Role: "dependency", AssetID: "order-events", Type: "avro", Source: artifact.SchemaSource{Type: "inline", Content: `"string"`}},
	})
	value := binaryEventPayload("value", "avro", []byte{84})
	value.WriterSchema = &artifact.ContractLink{ID: capture.Schemas[2].ID, SHA256: capture.Schemas[2].Source.SHA256}
	key := binaryEventPayload("key", "avro", []byte{4, '4', '2'})
	key.WriterSchema = &artifact.ContractLink{ID: capture.Schemas[3].ID, SHA256: capture.Schemas[3].Source.SHA256}
	projection := jsonEventPayload("decoded", `{"orderId":42}`)
	projection.DerivedFrom, projection.Transformation = "value", "avro-json-v1"
	projection.ReaderSchema = value.WriterSchema
	payloads := []artifact.EventPayload{key, value, projection, binaryEventPayload("header-0", "bytes", []byte{0, 255}), binaryEventPayload("header-1", "bytes", []byte{255, 0})}
	metadata := map[string]json.RawMessage{"kafka": json.RawMessage(`{"topic":"orders.created","partition":0,"offset":9007199254740993,"headers":[{"key":"trace-bin","payload":"header-0"},{"key":"trace-bin","payload":"header-1"}]}`)}
	capture.Traffic = []artifact.Flow{
		{ID: "incoming", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://orders/orders"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Body: &artifact.Body{Encoding: "json", Text: `[{"id":42}]`}}}},
		{ID: "publish", Protocol: "kafka", Interaction: &artifact.Interaction{Kind: "publish", Boundary: artifact.BoundaryRef{Role: "dependency", ID: "order-events"}, Transport: "tcp", Operation: "orders.created", CaptureState: "complete", Events: observedEvents(
			artifact.InteractionEvent{Direction: "sent", Kind: "message", Metadata: metadata, Payloads: payloads, Links: []artifact.EventLink{{FlowID: "incoming", Relation: "caused_by", Evidence: "trace"}}},
			artifact.InteractionEvent{Direction: "received", Kind: "ack", Metadata: map[string]json.RawMessage{"kafka": json.RawMessage(`{"partition":0,"offset":9007199254740993}`)}, Links: []artifact.EventLink{{FlowID: "publish", EventID: "event-0", Relation: "acknowledges", Evidence: "protocol"}}},
		)}},
		{ID: "consume", Protocol: "kafka", Interaction: &artifact.Interaction{Kind: "consume", Boundary: artifact.BoundaryRef{Role: "dependency", ID: "order-events"}, Transport: "tcp", Operation: "orders.created", CaptureState: "complete", Events: observedEvents(
			artifact.InteractionEvent{Direction: "received", Kind: "message", Metadata: metadata, Payloads: payloads, Links: []artifact.EventLink{{FlowID: "publish", EventID: "event-0", Relation: "delivery_of", Evidence: "protocol"}}},
		)}},
	}
	run, loaded, attempt := loadEventFixture(t, w, capture)
	if len(run.ProvidedAPIs) != 1 || len(run.Dependencies) != 1 || !sameJSON(run.Dependencies[0].Protocols, []string{"kafka"}) {
		t.Fatalf("serialization formats mistaken for protocols: %+v", run.Dependencies)
	}
	if !sameJSON(run.RequiredCapabilities, []string{packageCapability, eventCapability}) {
		t.Fatalf("missing package/event capabilities: %v", run.RequiredCapabilities)
	}
	if len(attempt.Bodies) != 11 {
		t.Fatalf("expected HTTP body and five Kafka payloads per message, got %d", len(attempt.Bodies))
	}
	if !strings.Contains(string(loaded.Traffic[1].Interaction.Events[0].Metadata["kafka"]), "9007199254740993") {
		t.Fatal("Kafka offset precision lost")
	}
	if loaded.Traffic[2].Interaction.Events[0].Links[0].Relation != "delivery_of" {
		t.Fatal("publish/delivery relationship lost")
	}
}

func TestEventPayloadAvailabilityRoundTrip(t *testing.T) {
	w, capture := eventFixture(t, Identity{}, nil)
	capture.CaptureIncomplete = true
	capture.CaptureDiagnostics = []string{"one message body was truncated and another was unavailable"}
	truncated := binaryEventPayload("truncated", "bytes", []byte{255})
	truncated.Availability = "truncated"
	truncated.Body.Truncated, truncated.Body.OriginalBytes = true, 10
	capture.Traffic = []artifact.Flow{{ID: "states", Protocol: "kafka", Interaction: &artifact.Interaction{Kind: "consume", Boundary: artifact.BoundaryRef{Role: "dependency", ID: "events"}, Transport: "tcp", Operation: "events", CaptureState: "incomplete", Events: observedEvents(artifact.InteractionEvent{Direction: "received", Kind: "message", Payloads: []artifact.EventPayload{
		binaryEventPayload("empty", "bytes", nil),
		{Name: "tombstone", Encoding: "avro", Availability: "null"},
		{Name: "secret-wire", Encoding: "avro", Availability: "redacted"},
		{Name: "secret-decoded", Encoding: "json", Availability: "redacted", DerivedFrom: "secret-wire", Transformation: "avro-json-v1"},
		truncated,
		{Name: "unavailable", Encoding: "bytes", Availability: "unavailable"},
		jsonEventPayload("json-null", "null"),
	}})}}}
	_, loaded, attempt := loadEventFixture(t, w, capture)
	if len(attempt.Bodies) != 3 {
		t.Fatalf("only empty, truncated and literal JSON null should retain files, got %d", len(attempt.Bodies))
	}
	if attempt.Bodies[0].SHA256 != digest(nil) || attempt.Bodies[0].RetainedBytes != 0 {
		t.Fatal("empty bytes lack an explicit verified zero-byte attachment")
	}
	payloads := loaded.Traffic[0].Interaction.Events[0].Payloads
	if payloads[0].Body == nil || payloads[1].Body != nil || payloads[2].Body != nil || payloads[3].Body != nil || payloads[4].Body.OriginalBytes != 10 {
		t.Fatal("empty, null, redacted or truncated payload distinctions lost")
	}
}

func minimalEventBundle(t *testing.T) (*Writer, *Run, Attempt) {
	t.Helper()
	w, capture := eventFixture(t, Identity{Application: &Application{Name: "Extension API"}}, nil)
	capture.Traffic = []artifact.Flow{{ID: "future-protocol", Protocol: "example.custom-v2", Interaction: &artifact.Interaction{Kind: "request_response", Boundary: artifact.BoundaryRef{Role: "provided", ID: "custom"}, Transport: "quic", Operation: "read", CaptureState: "complete", Events: observedEvents(
		artifact.InteractionEvent{Direction: "received", Kind: "message", Metadata: map[string]json.RawMessage{"example.custom": json.RawMessage(`{"features":["future"],"counter":9007199254740993}`)}, Payloads: []artifact.EventPayload{binaryEventPayload("message", "example.binary-v2", []byte{0, 1, 255})}},
		artifact.InteractionEvent{Direction: "sent", Kind: "message", Payloads: []artifact.EventPayload{jsonEventPayload("message", `{"ok":true}`)}},
	)}}}
	run, _, attempt := loadEventFixture(t, w, capture)
	return w, run, attempt
}

func TestUnknownProtocolPreservesOpaqueEventEnvelope(t *testing.T) {
	_, run, attempt := minimalEventBundle(t)
	if !sameJSON(run.ProvidedAPIs[0].Protocols, []string{"example.custom-v2"}) || attempt.Capture.Traffic[0].Interaction.Transport != "quic" {
		t.Fatal("unknown protocol or its transport was discarded")
	}
}

func TestEventBundleRejectsTamperingAndUnsupportedCapabilities(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(t *testing.T, w *Writer, run *Run, attempt *Attempt)
		want   string
	}{
		{"changed payload bytes", func(t *testing.T, w *Writer, _ *Run, attempt *Attempt) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(w.Path(), attempt.Bodies[0].Path), []byte("corrupted"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "hash mismatch"},
		{"non-UTF-8 text attachment", func(t *testing.T, w *Writer, _ *Run, attempt *Attempt) {
			raw := []byte{0xff, 0xfe}
			ref := &attempt.Bodies[0]
			ref.Representation, ref.SHA256, ref.RetainedBytes = "text", digest(raw), len(raw)
			ref.Path = "payloads/" + ref.SHA256 + ".bin"
			attempt.Capture.Traffic[0].Interaction.Events[0].Payloads[0].Body.Encoding = "text"
			if err := os.WriteFile(filepath.Join(w.Path(), ref.Path), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}, "not UTF-8"},
		{"unknown run capability", func(_ *testing.T, _ *Writer, run *Run, _ *Attempt) {
			run.RequiredCapabilities = append(run.RequiredCapabilities, "future-critical.v9")
		}, "unsupported required capability"},
		{"unknown attempt capability", func(_ *testing.T, _ *Writer, _ *Run, attempt *Attempt) {
			attempt.RequiredCapabilities = append(attempt.RequiredCapabilities, "future-critical.v9")
		}, "unsupported required capability"},
		{"undeclared event capability", func(_ *testing.T, _ *Writer, _ *Run, attempt *Attempt) { attempt.RequiredCapabilities = []string{} }, "capability declaration disagrees"},
		{"missing event attachment", func(_ *testing.T, _ *Writer, _ *Run, attempt *Attempt) { attempt.Bodies = attempt.Bodies[1:] }, "missing a verified attachment"},
		{"duplicate event attachment", func(_ *testing.T, _ *Writer, _ *Run, attempt *Attempt) {
			attempt.Bodies = append(attempt.Bodies, attempt.Bodies[0])
		}, "duplicate event attachment"},
		{"wrong payload selector", func(_ *testing.T, _ *Writer, _ *Run, attempt *Attempt) {
			attempt.Bodies[0].PayloadName = "does-not-exist"
		}, "missing body"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, run, attempt := minimalEventBundle(t)
			test.change(t, w, run, &attempt)
			raw, err := json.Marshal(attempt)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(w.Path(), run.Attempts[0].Path), raw, 0600); err != nil {
				t.Fatal(err)
			}
			run.Attempts[0].SHA256 = digest(raw)
			raw, err = json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(w.Path(), "bundle.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Load(w.Path()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected rejection containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestEventMetadataCannotPublishUnreadableAttempt(t *testing.T) {
	w, capture := eventFixture(t, Identity{}, nil)
	capture.Traffic = []artifact.Flow{{ID: "stream", Protocol: "custom", Interaction: &artifact.Interaction{
		Kind: "consume", Boundary: artifact.BoundaryRef{Role: "dependency", ID: "events"}, Transport: "tcp", Operation: "events", CaptureState: "complete",
		Events: observedEvents(artifact.InteractionEvent{Direction: "received", Kind: "message", Metadata: map[string]json.RawMessage{"custom": json.RawMessage(`{"offset":1,"offset":2}`)}}),
	}}}
	if _, err := w.Commit(capture); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("accepted ambiguous protocol metadata: %v", err)
	}
	if len(w.Snapshot().Attempts) != 0 {
		t.Fatal("published rejected attempt")
	}
}

func TestUnknownContractRoleCannotDisappearFromInventory(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref := artifact.SchemaRef{ID: "contract", Role: "provider", AssetID: "app", Type: "graphql", Source: artifact.SchemaSource{Type: "omitted"}}
	if err := w.RegisterSchemas([]artifact.SchemaRef{ref}); err == nil || !strings.Contains(err.Error(), "boundary role") {
		t.Fatalf("accepted unknown boundary role in writer: %v", err)
	}
	run := w.Snapshot()
	run.Schemas = []artifact.SchemaRef{ref}
	run.RequiredCapabilities = []string{packageCapability}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path(), "bundle.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(w.Path()); err == nil || !strings.Contains(err.Error(), "boundary role") {
		t.Fatalf("accepted unknown boundary role in reader: %v", err)
	}
}
