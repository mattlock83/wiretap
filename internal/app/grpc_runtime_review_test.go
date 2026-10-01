package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/project"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
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

func TestGRPCDecodeDirectionEncodingAndAggregateLimits(t *testing.T) {
	desc := (&healthpb.HealthCheckRequest{}).ProtoReflect().Descriptor()
	body, _ := proto.Marshal(&healthpb.HealthCheckRequest{Service: strings.Repeat("x", 128)})
	var zipped bytes.Buffer
	writer := gzip.NewWriter(&zipped)
	writer.Write(body)
	writer.Close()
	frame := make([]byte, 5)
	frame[0] = 1
	binary.BigEndian.PutUint32(frame[1:], uint32(zipped.Len()))
	frame = append(frame, zipped.Bytes()...)
	missing := parseGRPCFrames(frame, false, "", desc, 1024)
	if len(missing) != 1 || missing[0].DecodeError == "" {
		t.Fatal("compressed response without grpc-encoding was decoded")
	}
	many := parseGRPCFrames(bytes.Repeat(frame, 10), false, "gzip", desc, 256)
	if len(many) > 3 || many[len(many)-1].DecodeError == "" {
		t.Fatalf("decompressed direction budget ignored: %d messages", len(many))
	}
	empty := parseGRPCFrames(bytes.Repeat([]byte{0, 0, 0, 0, 0}, 2000), false, "", desc, 4<<20)
	if len(empty) != 1024 || empty[1023].DecodeError == "" {
		t.Fatal("empty messages bypassed retention count")
	}
}
