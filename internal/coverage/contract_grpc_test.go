package coverage

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/grpccontract"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const grpcConditionsProto = `syntax = "proto3";
package fixture;
message Payload {
  enum State { option allow_alias = true; UNKNOWN = 0; ACTIVE = 1; ALIAS = 1; }
  bool enabled = 1;
  optional bool explicit = 2;
  oneof choice { string name = 3; int64 number = 4; }
  State state = 5;
  repeated bool flags = 6;
  map<string, bool> labels = 7;
  message Child { bool flag = 1; }
  Child child = 8;
  uint64 exact = 9;
}
service Example { rpc Call(Payload) returns (Payload); rpc Uncalled(Payload) returns(Payload); }
`

func grpcConditionFixture(t *testing.T, source, body string) (*bundle.Run, *artifact.Artifact) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.proto"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := grpccontract.Resolve(context.Background(), "", grpccontract.Source{Kind: "proto", Files: []string{"test.proto"}, ImportPaths: []string{dir}}, []string{"fixture.Example"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "descriptor.protoset")
	if err := os.WriteFile(path, c.Bytes, 0600); err != nil {
		t.Fatal(err)
	}
	ref := artifact.SchemaRef{ID: "example", Role: "sut", Type: "protobuf", Services: c.Services, Source: artifact.SchemaSource{Type: "file", Path: path}}
	m := dynamicpb.NewMessage(c.Methods["/fixture.Example/Call"].Input())
	if err := protojson.Unmarshal([]byte(body), m); err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	side := func() *artifact.GrpcMessage {
		return &artifact.GrpcMessage{Kind: "grpc", Procedure: "/fixture.Example/Call", Service: "fixture.Example", Method: "Call", SchemaRef: "example", Messages: []artifact.GrpcPayload{{Index: 0, Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(raw)}, Body: &artifact.Body{Encoding: "json", Text: body}}}}
	}
	f := artifact.Flow{ID: "flow", Protocol: "grpc", Channel: "test_to_sut", Request: artifact.Message{GRPC: side()}, Response: artifact.Message{GRPC: side()}}
	f.Response.GRPC.HTTPStatus = 200
	a := &artifact.Artifact{TestCase: artifact.TestCase{ID: "attempt", Name: "example", Status: "pass"}, Schemas: []artifact.SchemaRef{ref}, Traffic: []artifact.Flow{f}}
	run := &bundle.Run{RunID: "run", RunState: "complete", APIID: "example", Schemas: []artifact.SchemaRef{ref}, Attempts: []bundle.AttemptRef{{FileRef: bundle.FileRef{Path: "evidence/attempt.json"}}}}
	return run, a
}

func grpcTarget(t *testing.T, spec *ContractSpec, phase, location, family, condition string) *ContractTarget {
	t.Helper()
	for _, target := range spec.Targets {
		if target.Operation == "/fixture.Example/Call" && target.Phase == phase && target.Location == location && target.Family == family && target.Condition == condition {
			return target
		}
	}
	t.Fatalf("missing %s %s %s %s", phase, location, family, condition)
	return nil
}

func TestGRPCStructuralConditions(t *testing.T) {
	run, a := grpcConditionFixture(t, grpcConditionsProto, `{"explicit":false,"name":"x","state":"ACTIVE","flags":[true,false],"labels":{"a":true},"child":{"flag":true},"exact":"18446744073709551615"}`)
	spec := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	if spec.RulesVersion != ProtobufRulesVersion || !spec.DenominatorKnown {
		t.Fatalf("unexpected scope: %+v", spec)
	}
	for _, tc := range []struct{ loc, family, condition, state string }{
		{"/fields/1", "boolean", "false", "covered"},
		{"/fields/2", "presence", "present", "covered"},
		{"/fields/2", "presence", "absent", "not_covered"},
		{"/fields/2", "boolean", "false", "covered"},
		{"/fields/oneof:3,4", "oneof", "member:3", "covered"},
		{"/fields/oneof:3,4", "oneof", "unset", "not_covered"},
		{"/fields/5", "enum", "1", "covered"},
		{"/fields/6", "collection", "nonempty", "covered"},
		{"/fields/6/item", "boolean", "false", "covered"},
		{"/fields/6/item", "boolean", "true", "covered"},
		{"/fields/7/value", "boolean", "true", "covered"},
		{"/fields/8/1", "boolean", "true", "covered"},
	} {
		if got := grpcTarget(t, spec, "request", tc.loc, tc.family, tc.condition); got.State != tc.state {
			t.Errorf("%+v: %+v", tc, got)
		}
	}
	if len(spec.Combinations) != 2 || len(spec.Combinations[0].Hits) < 8 {
		t.Fatalf("missing combined evidence: %+v", spec.Combinations)
	}
	count := 0
	for _, target := range spec.Targets {
		if target.Operation == "/fixture.Example/Call" && target.Phase == "request" && target.Family == "enum" {
			count++
		}
		if target.Family == "oneof" && strings.Contains(target.Location, "2") {
			t.Fatal("synthetic optional oneof was counted")
		}
	}
	if count != 2 {
		t.Fatalf("enum aliases should collapse: %d", count)
	}
	for _, target := range spec.Targets {
		if target.Operation == "/fixture.Example/Uncalled" && target.State != "not_covered" {
			t.Fatal("uncalled operation lost uncovered inventory")
		}
	}
}

func TestGRPCNegativeOutcomeDoesNotCoverOutput(t *testing.T) {
	run, a := grpcConditionFixture(t, grpcConditionsProto, `{}`)
	a.Traffic[0].Response.GRPC.StatusCode = 3
	a.Traffic[0].Response.GRPC.Messages = nil
	spec := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	if spec.Counts["family:operation"].Covered != 1 {
		t.Fatal("legitimate error did not count method")
	}
	if grpcTarget(t, spec, "response", "/fields/1", "boolean", "false").State != "not_covered" {
		t.Fatal("error without output credited default fields")
	}
	if grpcTarget(t, spec, "request", "/fields/2", "presence", "absent").State != "covered" {
		t.Fatal("empty protobuf request lost presence coverage")
	}
}

func TestGRPCCoverageQualityGates(t *testing.T) {
	for _, name := range []string{"redacted", "missing", "bad-status", "unknown-fields", "unknown-enum", "failed", "incomplete", "bad-wire"} {
		t.Run(name, func(t *testing.T) {
			run, a := grpcConditionFixture(t, grpcConditionsProto, `{}`)
			p := &a.Traffic[0].Request.GRPC.Messages[0]
			switch name {
			case "redacted":
				p.Redacted = true
				p.Wire = nil
			case "missing":
				a.Traffic[0].Request.GRPC.Messages = nil
			case "bad-status":
				a.Traffic[0].Response.GRPC.StatusCode = 50
			case "unknown-fields":
				p.Wire.Bytes = base64.StdEncoding.EncodeToString(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
			case "unknown-enum":
				p.Wire.Bytes = base64.StdEncoding.EncodeToString(protowire.AppendVarint(protowire.AppendTag(nil, 5, protowire.VarintType), 99))
			case "failed":
				a.TestCase.Status = "fail"
			case "incomplete":
				a.CaptureIncomplete = true
			case "bad-wire":
				p.Wire.Bytes = "AA=="
			}
			spec := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
			if grpcTarget(t, spec, "request", "/fields/1", "boolean", "false").State == "covered" {
				t.Fatalf("%s earned coverage", name)
			}
		})
	}
}

func TestGRPCUnsupportedAnnotationsAndRecursion(t *testing.T) {
	run, a := grpcConditionFixture(t, strings.Replace(grpcConditionsProto, "uint64 exact = 9;", "uint64 exact = 9; Payload next = 10;", 1), `{}`)
	set := &descriptorpb.FileDescriptorSet{}
	raw, err := os.ReadFile(run.Schemas[0].Source.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(raw, set); err != nil {
		t.Fatal(err)
	}
	set.File[0].MessageType[0].Options = &descriptorpb.MessageOptions{}
	set.File[0].MessageType[0].Options.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 50000, protowire.VarintType), 1))
	raw, err = proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(run.Schemas[0].Source.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	spec := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	if spec.DenominatorKnown || spec.Counts["family:unsupported"].Unsupported == 0 {
		t.Fatal("unsupported constraints disappeared")
	}
	if got := grpcTarget(t, spec, "request", "/fields/1", "boolean", "false"); got.State != "unknown" {
		t.Fatalf("custom option assurance: %+v", got)
	}
}

func TestGRPCCoverageUsesWireAndPreservesHTTPProfile(t *testing.T) {
	if ContractRulesVersion != "wiretap-contract-1" {
		t.Fatal("adding protobuf changed HTTP keys")
	}
	run, a := grpcConditionFixture(t, grpcConditionsProto, `{}`)
	a.Traffic[0].Request.GRPC.Messages[0].Body.Text = `{"enabled":true}`
	spec := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	if grpcTarget(t, spec, "request", "/fields/1", "boolean", "false").State != "covered" {
		t.Fatal("measurement trusted JSON instead of wire")
	}
}

func TestGRPCConditionInventoryIsBoundedForFlatEnum(t *testing.T) {
	var source strings.Builder
	source.WriteString(`syntax="proto3";package fixture;message Payload {enum State {ZERO=0;`)
	for i := 1; i < 5000; i++ {
		fmt.Fprintf(&source, "VALUE_%d=%d;", i, i)
	}
	source.WriteString(`} State state=1;} service Example {rpc Call(Payload) returns(Payload);}`)
	run, a := grpcConditionFixture(t, source.String(), `{}`)
	spec := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	if len(spec.Targets) > grpcMaxConditionTargets || spec.DenominatorKnown || spec.Counts["family:unsupported"].Unsupported == 0 {
		t.Fatalf("unbounded or falsely complete inventory: %d targets, known=%v", len(spec.Targets), spec.DenominatorKnown)
	}
}

func TestGRPCCollectionObservationsAreBounded(t *testing.T) {
	run, a := grpcConditionFixture(t, grpcConditionsProto, `{"flags":[`+strings.TrimSuffix(strings.Repeat("true,", 5000), ",")+`]}`)
	spec := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	for _, combination := range spec.Combinations {
		if len(combination.Hits) > grpcMaxConditionTargets {
			t.Fatalf("too many hits: %d", len(combination.Hits))
		}
	}
	if grpcTarget(t, spec, "request", "/fields/8", "presence", "absent").State != "unknown" {
		t.Fatal("unmeasured fields were presented as fully evaluated")
	}
}

func TestGRPCUnknownEnumOnlyPayloadSurfacesUnknownObservation(t *testing.T) {
	source := `syntax="proto3";package fixture;message Payload {enum State {ZERO=0;ONE=1;} State state=1;} service Example {rpc Call(Payload) returns(Payload);}`
	run, a := grpcConditionFixture(t, source, `{"state":99}`)
	spec := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	for _, condition := range []string{"0", "1"} {
		target := grpcTarget(t, spec, "request", "/fields/1", "enum", condition)
		if target.State != "unknown" || len(target.Evidence) == 0 {
			t.Fatalf("unknown enum vanished: %+v", target)
		}
	}
}
