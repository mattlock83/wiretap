package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/capture"
	"github.com/mattlock83/wiretap/internal/project"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestGRPCDrainCommitsStreamingSnapshotAndFinishCancelsRPC(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	upstream := health.NewServer()
	upstream.SetServingStatus("provider", healthpb.HealthCheckResponse_SERVING)
	instance.project = &project.Config{SUT: project.API{ID: "provider", URL: healthServer(t, upstream), Protocol: "grpc", Services: []string{healthService}}}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "long running watch"})
	conn, err := grpc.NewClient(strings.TrimPrefix(setup["sut_url"].(string), "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := healthpb.NewHealthClient(conn).Watch(ctx, &healthpb.HealthCheckRequest{Service: "provider"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	result := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "drain_timeout_ms": 0})
	art := dependencyArtifact(t, result)
	if !art.CaptureIncomplete || len(art.Traffic) != 1 || art.Traffic[0].Response.GRPC == nil || !art.Traffic[0].Response.GRPC.CaptureTruncated {
		t.Fatalf("missing incomplete stream snapshot: %#v", art)
	}
	if art.Traffic[0].Request.GRPC.SchemaRef != "provider" || art.Traffic[0].Response.GRPC.StreamKind != "server_streaming" {
		t.Fatalf("lost method identity: %#v", art.Traffic[0])
	}
	path := result["attempt_path"].(string)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	if _, err := stream.Recv(); err == nil {
		t.Fatal("finish did not terminate long-lived stream")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("late stream completion mutated committed attempt")
	}
}

type hangingHealth struct {
	healthpb.UnimplementedHealthServer
	entered chan struct{}
}

func (s *hangingHealth) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestGRPCFinishCancelsHungUnaryAfterDrain(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	server := &hangingHealth{entered: make(chan struct{})}
	instance.project = &project.Config{SUT: project.API{ID: "provider", URL: healthServer(t, server), Protocol: "grpc", Services: []string{healthService}}}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "hung unary"})
	conn, err := grpc.NewClient(strings.TrimPrefix(setup["sut_url"].(string), "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "provider"})
		finished <- err
	}()
	select {
	case <-server.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	result := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "drain_timeout_ms": 0})
	art := dependencyArtifact(t, result)
	if len(art.Traffic) != 1 || !art.CaptureIncomplete {
		t.Fatalf("missing in-flight unary snapshot: %#v", art)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled call succeeded")
		}
	case <-ctx.Done():
		t.Fatal("hung unary outlived finish")
	}
}

// Observe actual frame boundaries first, then decode retained messages. Wire
// omission and unavailable projections are separate facts with separate budgets.
func TestGRPCDecodeDirectionEncodingAndAggregateLimits(t *testing.T) {
	desc := (&healthpb.HealthCheckRequest{}).ProtoReflect().Descriptor()
	observe := func(raw []byte, wireLimit int64, encoding string, decodeLimit int64) (*artifact.GrpcObservation, []artifact.GrpcPayload) {
		t.Helper()
		observer := capture.NewGRPCObserver(capture.NewGRPCClock(time.Now()), "response", wireLimit, capture.MaxGRPCMessageRecords)
		if n, err := observer.Write(raw); err != nil || n != len(raw) {
			t.Fatalf("observe: n=%d err=%v", n, err)
		}
		observer.Finish(io.EOF)
		snapshot := observer.Snapshot()
		return observedGRPCMessages(&snapshot, encoding, desc, decodeLimit, nil)
	}
	body, _ := proto.Marshal(&healthpb.HealthCheckRequest{Service: strings.Repeat("x", 128)})
	var zipped bytes.Buffer
	writer := gzip.NewWriter(&zipped)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 5)
	frame[0] = 1
	binary.BigEndian.PutUint32(frame[1:], uint32(zipped.Len()))
	frame = append(frame, zipped.Bytes()...)

	t.Run("compression metadata belongs to its direction", func(t *testing.T) {
		observation, messages := observe(frame, 1024, "", 1024)
		if observation.CompleteFrames != 1 || observation.OmittedFrames != 0 || len(messages) != 1 || messages[0].DecodeState != "unavailable" || messages[0].Body != nil || messages[0].Wire == nil {
			t.Fatalf("compressed response without grpc-encoding was decoded or lost: observation=%+v messages=%+v", observation, messages)
		}
	})
	t.Run("compression tokens use exact protocol spelling", func(t *testing.T) {
		_, messages := observe(frame, 1024, "GZIP", 1024)
		if len(messages) != 1 || messages[0].DecodeState != "unavailable" || messages[0].Body != nil {
			t.Fatal("unsupported codec spelling claimed decoded evidence")
		}
	})
	t.Run("projection budget does not change observed counts", func(t *testing.T) {
		observation, messages := observe(bytes.Repeat(frame, 10), 4<<20, "gzip", 384)
		if observation.CompleteFrames != 10 || observation.RetainedFrames != 10 || observation.OmittedFrames != 0 || !observation.CountsComplete || len(messages) != 10 {
			t.Fatalf("decode limits changed observation: %+v, %d messages", observation, len(messages))
		}
		decodedBytes, decoded, unavailable := 0, 0, 0
		for _, message := range messages {
			if message.Body != nil {
				decodedBytes += len(message.Body.Text) + int(message.Body.OriginalBytes)
			}
			switch message.DecodeState {
			case "decoded":
				decoded++
			case "unavailable":
				unavailable++
				if message.Body != nil || message.Wire == nil {
					t.Fatal("decode omission lost wire or retained untrusted projection")
				}
			default:
				t.Fatalf("valid compressed message became invalid: %+v", message)
			}
		}
		if decodedBytes > 384 || decoded != 1 || unavailable != 9 {
			t.Fatalf("aggregate decode limit ignored: bytes=%d decoded=%d unavailable=%d", decodedBytes, decoded, unavailable)
		}
	})
	t.Run("wire budget keeps full counts and explicit omitted range", func(t *testing.T) {
		observation, messages := observe(bytes.Repeat(frame, 10), int64(len(frame)*2), "gzip", 4096)
		if observation.CompleteFrames != 10 || observation.RetainedFrames != 2 || observation.OmittedFrames != 8 || observation.Omitted == nil || observation.Omitted.FirstIndex != 2 || observation.Omitted.Count != 8 || !observation.CountsComplete || len(messages) != 2 {
			t.Fatalf("missing bounded wire observations: %+v", observation)
		}
		for index, message := range messages {
			if message.Index != index || message.DecodeState != "decoded" {
				t.Fatalf("retained frame lost wire index or projection: %+v", message)
			}
		}
	})
	t.Run("zero-byte messages still consume record budget", func(t *testing.T) {
		observation, messages := observe(bytes.Repeat([]byte{0, 0, 0, 0, 0}, 2000), 4<<20, "", 4<<20)
		if observation.CompleteFrames != 2000 || observation.RetainedFrames != 1024 || observation.OmittedFrames != 976 || observation.Omitted == nil || observation.Omitted.FirstIndex != 1024 || observation.Omitted.Count != 976 || len(messages) != 1024 {
			t.Fatalf("empty messages bypassed record budget: %+v, %d messages", observation, len(messages))
		}
		for _, message := range messages {
			if message.DecodeState != "decoded" || message.Wire == nil || message.Wire.Bytes != "" || message.Body == nil || message.Body.Text != "{}" {
				t.Fatalf("empty message confused with missing evidence: %+v", message)
			}
		}
	})
}

func TestGRPCDeepValidPayloadHasPartialValidationRatherThanMismatch(t *testing.T) {
	value := structpb.NewStringValue("leaf")
	for i := 0; i < 150; i++ {
		value = structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{value}})
	}
	wire, err := proto.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := (proto.UnmarshalOptions{RecursionLimit: 1000}).Unmarshal(wire, &structpb.Value{}); err != nil {
		t.Fatalf("fixture is not valid protobuf: %v", err)
	}
	remaining := int64(1 << 20)
	payload := decodeGRPCPayload(wire, false, "", value.ProtoReflect().Descriptor(), &remaining, nil)
	if payload.DecodeState != "unavailable" || payload.Body != nil || payload.Wire == nil || !strings.Contains(payload.DecodeError, "recursion") {
		t.Fatalf("depth limit treated as invalid data: %+v", payload)
	}
	file := &descriptorpb.FileDescriptorProto{Name: proto.String("deep.proto"), Package: proto.String("test"), Syntax: proto.String("proto3"), Dependency: []string{"google/protobuf/struct.proto"}, Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Deep"), Method: []*descriptorpb.MethodDescriptorProto{{Name: proto.String("Check"), InputType: proto.String(".google.protobuf.Value"), OutputType: proto.String(".google.protobuf.Value")}}}}}
	descriptors, _ := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto), file}})
	path := filepath.Join(t.TempDir(), "deep.protoset")
	if err := os.WriteFile(path, descriptors, 0600); err != nil {
		t.Fatal(err)
	}
	flow := artifact.Flow{ID: "deep", Protocol: "grpc", Channel: "test_to_sut", Request: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: "/test.Deep/Check", SchemaRef: "deep", Messages: []artifact.GrpcPayload{payload}, Observation: &artifact.GrpcObservation{CompleteFrames: 1, RetainedFrames: 1, CountsComplete: true, EOF: true, End: "eof"}}}, Response: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", HTTPStatus: 200, StatusCode: 3, Observation: &artifact.GrpcObservation{CountsComplete: true, EOF: true, End: "eof"}}}}
	capture := &artifact.Artifact{Schemas: []artifact.SchemaRef{{ID: "deep", Role: "sut", Type: "protobuf", Services: []string{"test.Deep"}, Source: artifact.SchemaSource{Type: "file", Path: path}}}, Traffic: []artifact.Flow{flow}}
	applySchemaValidation(capture)
	if capture.SchemaValidation == nil || capture.SchemaValidation.Status != "partial" || len(capture.SchemaValidation.Errors) != 0 || len(capture.SchemaValidation.Unavailable) == 0 {
		t.Fatalf("observation depth limit became contract mismatch: %+v", capture.SchemaValidation)
	}
}

func TestGRPCCompressedUnknownFieldsConsumeAggregateDecodeBudget(t *testing.T) {
	wire := protowire.AppendTag(nil, 99, protowire.BytesType)
	wire = protowire.AppendBytes(wire, make([]byte, 64<<10))
	var zipped bytes.Buffer
	writer := gzip.NewWriter(&zipped)
	if _, err := writer.Write(wire); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 5)
	frame[0] = 1
	binary.BigEndian.PutUint32(frame[1:], uint32(zipped.Len()))
	frame = append(frame, zipped.Bytes()...)
	observer := capture.NewGRPCObserver(capture.NewGRPCClock(time.Now()), "response", 4<<20, capture.MaxGRPCMessageRecords)
	if _, err := observer.Write(bytes.Repeat(frame, 1024)); err != nil {
		t.Fatal(err)
	}
	observer.Finish(io.EOF)
	snapshot := observer.Snapshot()
	budget := int64(2*len(wire) + 4) // Exactly two expanded messages and their {} projections.
	summary, messages := observedGRPCMessages(&snapshot, "gzip", (&healthpb.HealthCheckRequest{}).ProtoReflect().Descriptor(), budget, nil)
	if summary.CompleteFrames != 1024 || summary.RetainedFrames != 1024 || summary.OmittedFrames != 0 || len(messages) != 1024 {
		t.Fatalf("decode limit altered wire observation: %+v", summary)
	}
	decoded, unavailable, expandedBytes := 0, 0, int64(0)
	for _, message := range messages {
		switch message.DecodeState {
		case "decoded":
			decoded++
			expandedBytes += message.Body.OriginalBytes + int64(len(message.Body.Text))
		case "unavailable":
			unavailable++
			if message.Wire == nil || message.Body != nil {
				t.Fatal("unavailable projection lost wire or retained body")
			}
		default:
			t.Fatalf("unknown fields are valid protobuf: %+v", message)
		}
	}
	if decoded != 2 || unavailable != 1022 || expandedBytes > budget {
		t.Fatalf("gzip expansion escaped direction budget: decoded=%d unavailable=%d bytes=%d", decoded, unavailable, expandedBytes)
	}
}

func TestGRPCCorruptGzipStillConsumesExpansionBudget(t *testing.T) {
	body := make([]byte, 64<<10)
	var zipped bytes.Buffer
	writer := gzip.NewWriter(&zipped)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte{}, zipped.Bytes()...)
	corrupt[len(corrupt)-8] ^= 0xff // Corrupt CRC32 after a valid compressed body.
	remaining := int64(2 * len(body))
	desc := (&healthpb.HealthCheckRequest{}).ProtoReflect().Descriptor()
	for index := 0; index < 4; index++ {
		payload := decodeGRPCPayload(corrupt, true, "gzip", desc, &remaining, nil)
		want := "invalid"
		if index >= 2 {
			want = "unavailable"
		}
		if payload.DecodeState != want || payload.Body != nil {
			t.Fatalf("frame %d: remaining=%d payload=%+v", index, remaining, payload)
		}
	}
	if remaining != 0 {
		t.Fatalf("checksum failures did not consume expansion budget: %d", remaining)
	}
}
