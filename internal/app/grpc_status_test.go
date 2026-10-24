package app

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/postman"
	"github.com/mattlock83/wiretap/internal/project"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const richStatusSecret = "distinctive-rich-error-secret-2751af"

type richHealthServer struct {
	healthpb.UnimplementedHealthServer
}

func (*richHealthServer) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	result, err := status.New(codes.InvalidArgument, "invalid request").WithDetails(&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "token", Description: richStatusSecret}}}, &healthpb.HealthCheckRequest{Service: "custom boundary-local detail"})
	if err != nil {
		return nil, err
	}
	return nil, result.Err()
}
func callRichHealth(t *testing.T, origin string) *status.Status {
	t.Helper()
	conn, err := grpc.NewClient(strings.TrimPrefix(origin, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "invalid"})
	result, ok := status.FromError(err)
	if !ok || result.Code() != codes.InvalidArgument {
		t.Fatalf("unexpected result: %v", err)
	}
	return result
}

func TestNativeGRPCRichErrorsRemainTransparentAndPortable(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	upstream := healthServer(t, &richHealthServer{})
	direct := callRichHealth(t, upstream)
	instance.project = &project.Config{SUT: project.API{ID: "health", URL: upstream, Protocol: "grpc", Services: []string{healthService}}}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	payload := map[string]any{"name": "reject invalid request with typed error details"}
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", payload)
	proxied := callRichHealth(t, setup["sut_url"].(string))
	if !proto.Equal(direct.Proto(), proxied.Proto()) {
		t.Fatal("proxy altered native client status/details")
	}
	finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	root := finished["bundle_path"].(string)
	_, captures, err := bundle.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || len(captures[0].Traffic) != 1 {
		t.Fatal("unexpected capture count")
	}
	response := captures[0].Traffic[0].Response.GRPC
	if response == nil || response.StatusCode != 3 || len(response.Messages) != 0 || response.StatusDetails == nil || len(response.StatusDetails.Details) != 2 {
		t.Fatalf("lost error evidence: %+v", response)
	}
	if response.StatusDetails.Details[0].State != "decoded" {
		t.Fatalf("standard detail unavailable: %+v", response.StatusDetails)
	}
	if response.StatusDetails.Details[1].DescriptorSource != "boundary" {
		t.Fatal("custom Any did not use frozen boundary registry")
	}
	exports, exportErr := postman.GenerateAll(root, "Health")
	if exportErr != nil {
		t.Fatal(exportErr)
	}
	if len(exports) != 1 || exports[0].GRPC == nil {
		t.Fatalf("wrong gRPC-only exports: %+v", exports)
	}
	example := exports[0].GRPC.Items[0].Items[0].Examples[0].Response
	if example.Code != 3 || len(example.Messages) != 1 || example.Messages[0].Type != "outgoing" {
		t.Fatalf("non-OK example lost status or invented response: %+v", example)
	}
	var encoded string
	for _, header := range append(example.Metadata, example.Trailers...) {
		if header.Key == "grpc-status-details-bin" {
			encoded = header.Value
		}
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var exported statuspb.Status
	if err := proto.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(direct.Proto(), &exported) {
		t.Fatal("v3 export changed the RPC error details")
	}
}
