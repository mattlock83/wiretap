package grpcstreaming_test

import (
	"os"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/examples/grpc-streaming/fixture"
	wiretap "github.com/mattlock83/wiretap/sdk/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNativeStreamingShapes(t *testing.T) {
	if os.Getenv("WIRETAP_GRPC_STREAMING_EXAMPLE") != "1" {
		t.Skip("run ./examples/grpc-streaming/run.sh")
	}
	contract, err := fixture.Compile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method string
		count        int
		compressed   bool
	}{
		{"unary echo", "Echo", 1, false},
		{"server stream", "Watch", 3, false},
		{"client stream", "Collect", 3, false},
		{"bidirectional conversation", "Chat", 3, false},
		{"compressed conversation", "Chat", 2, true},
		{"empty server stream", "Watch", 0, false},
		{"empty client stream", "Collect", 0, false},
		{"empty bidirectional stream", "Chat", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// run.sh owns the runtime and finalization because it starts the
			// provider with the dynamic dependency listener. RPCs use grpc-go.
			session := (&wiretap.Runtime{}).StartTest(t, wiretap.TestOptions{})
			if os.Getenv("WIRETAP_EXAMPLE_FAIL") == "1" && test.method == "Echo" {
				t.Cleanup(func() { t.Error("intentional user cleanup failure") })
			}
			conn, err := grpc.NewClient(strings.TrimPrefix(session.SUTURL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var options []grpc.CallOption
			if test.compressed {
				options = append(options, grpc.UseCompressor("gzip"))
			}
			messages, err := contract.Exercise(session.Context, conn, test.method, test.count, options...)
			if err != nil {
				t.Fatal(err)
			}
			expected := test.count
			if test.method == "Echo" || test.method == "Collect" {
				expected = 1
			}
			if len(messages) != expected {
				t.Fatalf("received %d messages; want %d", len(messages), expected)
			}
		})
	}
}
