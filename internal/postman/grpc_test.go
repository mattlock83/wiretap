package postman

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dlclark/regexp2"
	streaming "github.com/phrond/wiretap/examples/grpc-streaming/fixture"
	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/grpccontract"
	"github.com/santhosh-tekuri/jsonschema/v6"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
)

func grpcFixture(t *testing.T, method string) (artifact.Flow, artifact.SchemaRef) {
	t.Helper()
	contract, err := streaming.Compile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(contract.Message.ParentFile())}})
	if err != nil {
		t.Fatal(err)
	}
	schema := artifact.SchemaRef{ID: "streams", Role: "sut", Type: "protobuf", Services: []string{streaming.Service}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(descriptor)}}
	kind := map[string]string{"Echo": "unary", "Watch": "server_streaming", "Collect": "client_streaming", "Chat": "bidirectional"}[method]
	makeMessage := func() *artifact.GrpcMessage {
		return &artifact.GrpcMessage{Kind: "grpc", Procedure: streaming.Procedure(method), Service: streaming.Service, Method: method, SchemaRef: "streams", StreamKind: kind, Observation: &artifact.GrpcObservation{CountsComplete: true, EOF: true, End: "eof"}}
	}
	req, res := makeMessage(), makeMessage()
	res.HTTPStatus, res.StatusSource = 200, "trailers"
	req.Metadata = artifact.Headers{"X-Tag": {"one", "two"}, "example-bin": {"AAEC"}}
	res.Metadata = artifact.Headers{"content-type": {"application/grpc"}}
	res.Trailers = artifact.Headers{"grpc-status": {"0"}, "reply-bin": {"/w"}}
	sequence := uint64(0)
	appendMessage := func(direction *artifact.GrpcMessage, text string) {
		payload := contract.NewMessage(text, 0, nil)
		wire, _ := proto.Marshal(payload)
		projection, _ := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(payload)
		direction.Messages = append(direction.Messages, artifact.GrpcPayload{Index: len(direction.Messages), Type: string(contract.Message.FullName()), DecodeState: "decoded", Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(wire), OriginalBytes: int64(len(wire))}, Body: &artifact.Body{Encoding: "json", Text: string(projection)}, Observation: &artifact.GrpcPayloadObservation{WireBytes: uint64(len(wire)), StartSequence: sequence, CompleteSequence: sequence + 1, StartedOffsetNS: int64(sequence), CompletedOffsetNS: int64(sequence + 1)}})
		direction.ObservedBytes += int64(5 + len(wire))
		direction.Observation.ObservedBytes = direction.ObservedBytes
		direction.Observation.CompleteFrames++
		direction.Observation.RetainedFrames++
		sequence += 2
	}
	appendMessage(req, "first")
	if method == "Collect" {
		appendMessage(req, "second")
	}
	appendMessage(res, "first response")
	if method == "Chat" {
		appendMessage(req, "second")
	}
	if method == "Watch" || method == "Chat" {
		appendMessage(res, "second response")
	}
	return artifact.Flow{ID: "random-flow-id", Protocol: "grpc", Channel: "test_to_sut", Request: artifact.Message{GRPC: req}, Response: artifact.Message{GRPC: res}, StartedAt: time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)}, schema
}

type v3Regexp struct{ *regexp2.Regexp }

func (r v3Regexp) MatchString(value string) bool { ok, _ := r.Regexp.MatchString(value); return ok }

func validateV3(t *testing.T, raw []byte) {
	t.Helper()
	schemaRaw, err := os.ReadFile("testdata/postman-v3.0.0.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaJSON, value any
	if err := json.Unmarshal(schemaRaw, &schemaJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseRegexpEngine(func(pattern string) (jsonschema.Regexp, error) {
		r, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		r.MatchTimeout = time.Second
		return v3Regexp{r}, nil
	})
	const location = "https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json"
	if err := compiler.AddResource(location, schemaJSON); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("does not conform to official v3 schema: %v", err)
	}
}

func TestGRPCV3AllShapesAndHTTPCompatibility(t *testing.T) {
	var captures []*artifact.Artifact
	for _, method := range []string{"Echo", "Watch", "Collect", "Chat"} {
		flow, schema := grpcFixture(t, method)
		c := capture(method, "scenario "+method, flow.StartedAt, []artifact.Flow{httpFlow("http", "https://shop.test/", 200, flow.StartedAt), flow})
		c.Schemas = []artifact.SchemaRef{schema}
		captures = append(captures, c)
	}
	root := fixtureBundle(t, captures...)
	legacy, err := Generate(root, "Streams")
	if err != nil {
		t.Fatal(err)
	}
	all, err := GenerateAll(root, "Streams")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[1].GRPC == nil || !strings.HasSuffix(all[1].Filename, ".grpc.postman_collection.json") {
		t.Fatalf("unexpected exports: %+v", all)
	}
	var before, after, grpc bytes.Buffer
	if err := Write(&before, legacy[0].Collection); err != nil {
		t.Fatal(err)
	}
	if err := WriteExport(&after, all[0]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("HTTP v2 export changed")
	}
	if err := WriteExport(&grpc, all[1]); err != nil {
		t.Fatal(err)
	}
	validateV3(t, grpc.Bytes())
	for _, folder := range all[1].GRPC.Items {
		response := folder.Items[0].Examples[0].Response
		want := map[string][]string{"scenario Echo": {"outgoing", "incoming"}, "scenario Watch": {"outgoing", "incoming", "incoming"}, "scenario Collect": {"outgoing", "outgoing", "incoming"}, "scenario Chat": {"outgoing", "incoming", "outgoing", "incoming"}}[folder.Name]
		var got []string
		for _, message := range response.Messages {
			got = append(got, message.Type)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s stream order = %v, want %v", folder.Name, got, want)
		}
		if response.Trailers[1] != (Header{Key: "reply-bin", Value: "/w=="}) {
			t.Fatalf("binary trailer lost: %+v", response.Trailers)
		}
	}
}

func TestGRPCV3OmitsRunResultsAndTimes(t *testing.T) {
	flow, _ := grpcFixture(t, "Chat")
	c := capture("attempt-one", "stable scenario", flow.StartedAt, []artifact.Flow{flow})
	first, err := generateGRPC([]*artifact.Artifact{c}, "Streams", "test_to_sut", "")
	if err != nil {
		t.Fatal(err)
	}
	c.TestCase.ID, c.TestCase.Status = "different-attempt", "fail"
	c.TestCase.StartedAt = c.TestCase.StartedAt.Add(time.Hour)
	c.Traffic[0].ID, c.Traffic[0].TraceID, c.Traffic[0].DurationMS = "different-flow", "random-trace", 444
	for _, msg := range []*artifact.GrpcMessage{flow.Request.GRPC, flow.Response.GRPC} {
		for i := range msg.Messages {
			msg.Messages[i].Observation.StartedOffsetNS *= 99
			msg.Messages[i].Observation.CompletedOffsetNS *= 99
		}
	}
	second, err := generateGRPC([]*artifact.Artifact{c}, "Streams", "test_to_sut", "")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatal("run/results/timing changed interaction knowledge")
	}
}

func TestGRPCV3PreservesExactTestFolderNames(t *testing.T) {
	flow, _ := grpcFixture(t, "Echo")
	name := " Parent/child scenario "
	c := capture("attempt", name, flow.StartedAt, []artifact.Flow{flow})
	collection, err := generateGRPC([]*artifact.Artifact{c}, "Streams", "test_to_sut", "")
	if err != nil {
		t.Fatal(err)
	}
	if collection.Items[0].Name != name {
		t.Fatalf("test folder name changed: %q", collection.Items[0].Name)
	}
}

func TestGRPCV3PreservesRichErrorsAndRejectsIncomplete(t *testing.T) {
	flow, _ := grpcFixture(t, "Echo")
	res := flow.Response.GRPC
	res.StatusCode, res.StatusMessage = 3, "invalid input"
	res.Trailers["grpc-status"] = []string{"3"}
	res.Messages = nil
	res.ObservedBytes, res.Observation.ObservedBytes, res.Observation.CompleteFrames, res.Observation.RetainedFrames = 0, 0, 0, 0
	detail, _ := anypb.New(&statuspb.Status{Code: 5, Message: "nested detail"})
	wire, _ := proto.Marshal(&statuspb.Status{Code: 3, Message: "invalid input", Details: []*anypb.Any{detail}})
	res.Trailers["grpc-status-details-bin"] = []string{base64.StdEncoding.EncodeToString(wire)}
	grpccontract.CaptureStatusDetails(res, nil)
	item, err := exportGRPCFlow(flow, 1)
	if err != nil {
		t.Fatal(err)
	}
	response := item.Examples[0].Response
	if response.Code != 3 || len(response.Messages) != 1 || response.Messages[0].Type != "outgoing" {
		t.Fatalf("error fabricated reply: %+v", response)
	}
	found := false
	for _, header := range response.Trailers {
		if header.Key == "grpc-status-details-bin" && header.Value == base64.StdEncoding.EncodeToString(wire) {
			found = true
		}
	}
	if !found {
		t.Fatalf("rich status wire lost: %+v", response)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*artifact.Flow)
	}{
		{"missing observations", func(f *artifact.Flow) { f.Request.GRPC.Observation = nil }},
		{"unknown status", func(f *artifact.Flow) { f.Response.GRPC.StatusCode = -1 }},
		{"local error", func(f *artifact.Flow) { f.Response.GRPC.StatusSource = "proxy" }},
		{"truncated", func(f *artifact.Flow) { f.Response.GRPC.CaptureTruncated = true }},
		{"undecoded", func(f *artifact.Flow) {
			f.Request.GRPC.Messages[0].DecodeState = "unavailable"
			f.Request.GRPC.Messages[0].DecodeError = "unknown descriptor"
		}},
		{"request trailers", func(f *artifact.Flow) { f.Request.GRPC.Trailers = artifact.Headers{"trailer": {"unsupported"}} }},
		{"bad binary metadata", func(f *artifact.Flow) { f.Request.GRPC.Metadata["bad-bin"] = []string{"not-base64!"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := grpcFixture(t, "Echo")
			tc.mutate(&f)
			if _, err := exportGRPCFlow(f, 1); err == nil {
				t.Fatal("accepted incomplete/unrepresentable interaction")
			}
		})
	}
}

func TestGRPCV3MockBoundariesAndDuplicateScenarios(t *testing.T) {
	flow, schema := grpcFixture(t, "Echo")
	flow.Channel, flow.AssetID = "sut_to_dependency", "stock"
	schema.Role, schema.AssetID = "dependency", "stock"
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(bundle.Identity{DependencyKinds: map[string]string{"stock": "mock"}}); err != nil {
		t.Fatal(err)
	}
	tc := capture("one", "one scenario", flow.StartedAt, []artifact.Flow{flow})
	tc.Schemas = []artifact.SchemaRef{schema}
	tc.Schemas, err = w.Register(tc.TestCase, tc.Schemas)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(tc); err != nil {
		t.Fatal(err)
	}
	exports, err := GenerateAll(w.Path(), "Streams")
	if err != nil {
		t.Fatal(err)
	}
	if len(exports) != 1 || exports[0].Filename != "mock-stock.grpc.postman_collection.json" {
		t.Fatalf("wrong target: %+v", exports)
	}
	if _, err := generateGRPC([]*artifact.Artifact{tc, tc}, "Stock", "sut_to_dependency", "stock"); err == nil || !strings.Contains(err.Error(), "duplicate test name") {
		t.Fatalf("ambiguous folders accepted: %v", err)
	}
}
