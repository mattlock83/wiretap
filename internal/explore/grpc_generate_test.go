package explore

import (
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	"github.com/bufbuild/protocompile"
	"github.com/phrond/wiretap/examples/grpc-exploration/fixture"
	"github.com/phrond/wiretap/internal/grpccontract"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestGenerateGRPCDeclaredViolations(t *testing.T) {
	compiled, err := fixture.Compile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(compiled.DescriptorSet)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := grpccontract.Parse(raw, []string{fixture.Service})
	if err != nil {
		t.Fatal(err)
	}
	validator, err := protovalidate.New(protovalidate.WithExtensionTypeResolver(dynamicpb.NewTypes(contract.Files)))
	if err != nil {
		t.Fatal(err)
	}
	request := compiled.NewRequest("valid-user", "ABC", 2)
	seed := &grpcSeed{contract: contract, validator: validator, method: contract.Methods[fixture.Procedure], messages: []proto.Message{request}, schemaID: "orders"}
	mutations, notes, err := generateGRPC([]step{{ID: "request", Operation: fixture.Procedure, GRPC: seed}}, []int{0}, "orders", contract.SHA256, []int{3})
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations) == 0 {
		t.Fatalf("no declared constraints generated; notes=%v", notes)
	}
	found := map[string]bool{}
	for _, mutation := range mutations {
		changed := proto.Clone(request)
		if err := mutation.GRPC.apply(changed); err != nil {
			t.Fatal(err)
		}
		if !isolatedGRPCViolation(validator.Validate(changed), mutation.GRPC.path, mutation.GRPC.rule) {
			t.Fatalf("mutation not isolated: %+v", mutation.Target)
		}
		found[mutation.Target.Constraint] = true
	}
	for _, rule := range []string{"string.min_len", "string.max_len", "int32.gte", "int32.lte"} {
		if !found[rule] {
			t.Errorf("missing %s; found=%v; notes=%v", rule, found, notes)
		}
	}
}

func grpcRuleSeed(t *testing.T, declarations, valid string) *grpcSeed {
	t.Helper()
	source := `syntax = "proto3";
package generator;
import "buf/validate/validate.proto";
` + declarations + `
service Provider { rpc Call(Request) returns (Request); }`
	compiler := protocompile.Compiler{Resolver: protocompile.CompositeResolver{
		&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{"rules.proto": source})},
		protocompile.ResolverFunc(func(path string) (protocompile.SearchResult, error) {
			file, err := protoregistry.GlobalFiles.FindFileByPath(path)
			return protocompile.SearchResult{Desc: file}, err
		}),
	}}
	files, err := compiler.Compile(t.Context(), "rules.proto")
	if err != nil {
		t.Fatal(err)
	}
	message := dynamicpb.NewMessage(files[0].Messages().ByName("Request"))
	if err := protojson.Unmarshal([]byte(valid), message); err != nil {
		t.Fatal(err)
	}
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(message); err != nil {
		t.Fatalf("invalid test seed: %v", err)
	}
	return &grpcSeed{validator: validator, messages: []proto.Message{message}, schemaID: "rules"}
}

func ruleMutations(t *testing.T, seed *grpcSeed) ([]mutation, []string) {
	t.Helper()
	mutations, notes, err := generateGRPC([]step{{ID: "request", Operation: "/generator.Provider/Call", GRPC: seed}}, []int{0}, "rules", "revision", []int{3})
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range mutations {
		message := proto.Clone(seed.messages[mutation.GRPC.index])
		if err := mutation.GRPC.apply(message); err != nil {
			t.Fatal(err)
		}
		if !isolatedGRPCViolation(seed.validator.Validate(message), mutation.GRPC.path, mutation.GRPC.rule) {
			t.Fatalf("generated mutation does not isolate its declared rule: %+v", mutation.Target)
		}
	}
	return mutations, notes
}

func TestGRPCDeclaredRuleFamilies(t *testing.T) {
	seed := grpcRuleSeed(t, `
enum State { UNKNOWN = 0; READY = 1; }
message Nested { string name = 1 [(buf.validate.field).string.min_len = 2]; }
message Request {
  optional string name = 1 [(buf.validate.field).required = true];
  bool enabled = 2 [(buf.validate.field).bool.const = true];
  State state = 3 [(buf.validate.field).enum.defined_only = true];
  repeated string items = 4 [(buf.validate.field).repeated = {min_items: 1, max_items: 3}];
  repeated string unique_items = 5 [(buf.validate.field).repeated.unique = true];
  map<string, string> labels = 6 [(buf.validate.field).map.min_pairs = 1];
  Nested nested = 7;
  bytes blob = 8 [(buf.validate.field).bytes.min_len = 2];
  int64 big = 9 [(buf.validate.field).int64 = {gte: 9007199254740993, lte: 9007199254740995}];
}`, `{"name":"name","enabled":true,"state":"READY","items":["one"],"uniqueItems":["one"],"labels":{"a":"b"},"nested":{"name":"ok"},"blob":"QUI=","big":"9007199254740994"}`)
	mutations, notes := ruleMutations(t, seed)
	found := map[string]bool{}
	for _, mutation := range mutations {
		found[mutation.Target.Constraint] = true
	}
	for _, rule := range []string{"required", "bool.const", "enum.defined_only", "repeated.min_items", "repeated.max_items", "repeated.unique", "map.min_pairs", "string.min_len", "bytes.min_len", "int64.gte", "int64.lte"} {
		if !found[rule] {
			t.Errorf("missing %s; generated=%v; notes=%v", rule, found, notes)
		}
	}
}

func TestGRPCPlainProtoAndOverlappingCELGenerateNoFalseTargets(t *testing.T) {
	for _, tc := range []struct{ name, declaration, valid string }{
		{"plain", `message Request { string name = 1; int32 quantity = 2; }`, `{"name":"valid","quantity":1}`},
		{"overlap", `message Request { string name = 1 [(buf.validate.field).string.min_len = 2, (buf.validate.field).cel = {id:"also.min_len", message:"length", expression:"this.size() >= 2"}]; }`, `{"name":"valid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations, notes := ruleMutations(t, grpcRuleSeed(t, tc.declaration, tc.valid))
			if len(mutations) != 0 {
				t.Fatalf("invented or non-isolated targets: %+v", mutations)
			}
			if tc.name == "overlap" && !strings.Contains(strings.Join(notes, " "), "non-isolated") {
				t.Fatalf("missing explanation of skipped candidate: %v", notes)
			}
		})
	}
}

func TestGRPCRepeatedGrowthBoundedBeforeAllocationAndAfterBinding(t *testing.T) {
	seed := grpcRuleSeed(t, `message Request { repeated bytes payloads = 1 [(buf.validate.field).repeated.max_items = 1023]; }`, `{"payloads":["eA=="]}`)
	mutations, _ := ruleMutations(t, seed)
	if len(mutations) != 1 {
		t.Fatalf("expected bounded small witness, got %d", len(mutations))
	}
	large := proto.Clone(seed.messages[0])
	field := large.ProtoReflect().Descriptor().Fields().ByName("payloads")
	large.ProtoReflect().Mutable(field).List().Set(0, protoreflect.ValueOfBytes(make([]byte, 1<<20)))
	if err := mutations[0].GRPC.apply(large); err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatalf("runtime binding bypassed growth budget: %v", err)
	}
	seed.messages[0] = large
	mutations, notes := ruleMutations(t, seed)
	if len(mutations) != 0 || !strings.Contains(strings.Join(notes, " "), "16 MiB") {
		t.Fatalf("large growth was not skipped before cloning: %d mutations, notes=%v", len(mutations), notes)
	}
}

func TestGRPCStreamMessageTargetsShareSemanticKeys(t *testing.T) {
	seed := grpcRuleSeed(t, `message Request { string name = 1 [(buf.validate.field).string.min_len = 2]; }`, `{"name":"valid"}`)
	seed.messages = append(seed.messages, proto.Clone(seed.messages[0]))
	mutations, _ := ruleMutations(t, seed)
	if len(mutations) != 2 || mutations[0].Target.Key != mutations[1].Target.Key || *mutations[0].Target.MessageIndex != 0 || *mutations[1].Target.MessageIndex != 1 || mutations[0].Target.Location == mutations[1].Target.Location {
		t.Fatalf("stream targets must retain distinct message evidence for one semantic rule: %+v", mutations)
	}
}
