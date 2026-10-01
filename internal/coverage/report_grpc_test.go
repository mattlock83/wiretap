package coverage

import (
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/grpccontract"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestGRPCFailedCallsRemainOperationObservationsWithoutCoverage(t *testing.T) {
	const procedure = "/grpc.health.v1.Health/Check"
	method := grpc_health_v1.File_grpc_health_v1_health_proto.Services().ByName("Health").Methods().ByName("Check")
	schema := &loadedSchema{Ref: artifact.SchemaRef{ID: "health", Role: "sut", Type: "protobuf"}, GRPC: &grpccontract.Contract{Methods: map[string]protoreflect.MethodDescriptor{procedure: method}}, Operations: []string{procedure}, Exercised: map[string]struct{}{}}
	entries := []flowEntry{{Status: "fail", TestCase: "failed health check", SchemaKeys: map[string]bool{schemaKey(schema.Ref): true}, Flow: artifact.Flow{ID: "flow", Protocol: "grpc", Channel: "test_to_sut", Request: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: procedure}}, Response: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", HTTPStatus: 200, StatusCode: 0}}}}}
	markCoverage(entries, []*loadedSchema{schema}, "test_to_sut")
	if len(schema.Exercised) != 0 {
		t.Fatal("failed call earned coverage")
	}
	samples := buildOperationSamples(entries, []*loadedSchema{schema}, "test_to_sut")
	specs := exportSpecs([]*loadedSchema{schema}, true, samples)
	if len(specs) != 1 || len(specs[0].NotCovered) != 1 || len(specs[0].NotCovered[0].Samples) != 1 {
		t.Fatalf("failed observation missing from operation: %+v", specs)
	}
	tests := buildTestCaseCoverage(entries, []*loadedSchema{schema}, nil)
	if len(tests) != 1 || len(tests[0].Provided) != 1 {
		t.Fatalf("failed observation missing from test: %+v", tests)
	}
}
