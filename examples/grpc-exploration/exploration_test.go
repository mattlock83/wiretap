package grpcexploration_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/examples/grpc-exploration/fixture"
	wiretap "github.com/phrond/wiretap/sdk/go"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func TestOrdersRejectInvalidInputs(t *testing.T) {
	if os.Getenv("WIRETAP_GRPC_EXPLORATION_EXAMPLE") != "1" {
		t.Skip("run ./examples/grpc-exploration/run.sh")
	}
	contract, err := fixture.Compile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state := &fixture.State{}
	server, err := contract.NewServer(state, os.Getenv("WIRETAP_GRPC_CONTRACT_SOURCE") != "descriptor_set")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", os.Getenv("WIRETAP_GRPC_EXPLORATION_SUT_ADDR"))
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	t.Cleanup(server.Stop)

	// Keep the ordinary test framework, native grpc-go client and fixture helpers.
	session := (&wiretap.Runtime{}).StartTest(t, wiretap.TestOptions{Redactions: []wiretap.Redaction{{Field: "request.metadata.authorization", Rule: "mask"}}})
	conn, err := grpc.NewClient(strings.TrimPrefix(session.SUTURL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	user, token := state.Reset()
	ctx, cancel := context.WithTimeout(session.Context, 10*time.Second)
	defer cancel()
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", token, "traceparent", carrier["traceparent"]))
	response := contract.NewResponse()
	if err := conn.Invoke(ctx, fixture.Procedure, contract.NewRequest(user, "sku-123", 2), response); err != nil {
		t.Fatal(err)
	}
	if state.Count() != 1 {
		t.Fatal("happy-path order was not stored")
	}
	if response.Get(contract.Response.Fields().ByName("id")).String() == "" {
		t.Fatal("happy-path response has no order ID")
	}

	result, err := session.Explore(t.Context(), wiretap.ExploreOptions{
		Operations:           []string{fixture.Procedure},
		ExpectedGRPCStatuses: []int{3}, // INVALID_ARGUMENT; auth errors never satisfy a rejection check.
		Bindings:             []wiretap.ExploreBinding{{Name: "user_id", In: "body", Path: "/user_id"}},
		Hooks: []wiretap.ExploreHook{
			{Event: wiretap.BeforeCase, Handler: func(ctx context.Context, input wiretap.ExploreHookInput) (wiretap.ExploreHookOutput, error) {
				user, token := state.Reset()
				return wiretap.ExploreHookOutput{Bindings: map[string]any{"user_id": user}, State: map[string]any{"token": token}}, nil
			}},
			{Event: wiretap.BeforeRequest, Operations: []string{fixture.Procedure}, Handler: func(ctx context.Context, input wiretap.ExploreHookInput) (wiretap.ExploreHookOutput, error) {
				if input.GRPCRequest == nil || input.GRPCRequest.Procedure != fixture.Procedure {
					return wiretap.ExploreHookOutput{}, fmt.Errorf("expected the final gRPC request")
				}
				token, ok := input.State["token"].(string)
				if !ok {
					return wiretap.ExploreHookOutput{}, fmt.Errorf("case setup did not supply a token")
				}
				return wiretap.ExploreHookOutput{Headers: map[string]string{"authorization": token}}, nil
			}},
			{Event: wiretap.CheckCase, Handler: func(ctx context.Context, input wiretap.ExploreHookInput) (wiretap.ExploreHookOutput, error) {
				want := 0
				if input.Mode == "baseline" {
					want = 1
				}
				return wiretap.ExploreHookOutput{Checks: []wiretap.ExploreCheck{{Name: "only valid orders are stored", Passed: state.Count() == want}}}, nil
			}},
			{Event: wiretap.AfterCase, Handler: func(ctx context.Context, input wiretap.ExploreHookInput) (wiretap.ExploreHookOutput, error) {
				state.Reset()
				return wiretap.ExploreHookOutput{}, nil
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Generated == 0 || result.Passed != result.Generated || result.Failed != 0 || !result.Complete {
		t.Fatalf("unexpected exploration result: %+v", result)
	}
	t.Logf("%d generated rejection checks passed; authored and generated attempts share %s", result.Passed, result.BundlePath)
}
