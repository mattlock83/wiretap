package grpcboundaries_test

import (
	"os"
	"strings"
	"testing"

	wiretap "github.com/mattlock83/wiretap/sdk/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestExistingGRPCClient(t *testing.T) {
	if !strings.EqualFold(os.Getenv("WIRETAP_GRPC_EXAMPLE"), "1") {
		t.Skip("run examples/grpc-boundaries/run.sh to start Wiretap and the example servers")
	}
	session := (&wiretap.Runtime{}).StartTest(t, wiretap.TestOptions{})
	conn, err := grpc.NewClient(strings.TrimPrefix(session.SUTURL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	response, err := healthpb.NewHealthClient(conn).Check(session.Context, &healthpb.HealthCheckRequest{Service: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v", response.Status)
	}
}
