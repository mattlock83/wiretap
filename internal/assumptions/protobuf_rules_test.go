package assumptions

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func protobufRulesFixture(t *testing.T, change func(*descriptorpb.FileDescriptorProto)) wireTestFixture {
	t.Helper()
	fixture := newWireTestFixture(t, 1, false)
	raw, err := os.ReadFile(fixture.schema.Source.Path)
	if err != nil {
		t.Fatal(err)
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	file := set.File[0]
	file.EnumType = []*descriptorpb.EnumDescriptorProto{{Name: proto.String("State"), Value: []*descriptorpb.EnumValueDescriptorProto{
		{Name: proto.String("UNKNOWN"), Number: proto.Int32(0)}, {Name: proto.String("READY"), Number: proto.Int32(1)}, {Name: proto.String("PENDING"), Number: proto.Int32(2)},
	}}}
	for _, message := range file.MessageType[:2] {
		message.Field = append(message.Field,
			&descriptorpb.FieldDescriptorProto{Name: proto.String("state"), Number: proto.Int32(8), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(), TypeName: proto.String(".example.State")},
			&descriptorpb.FieldDescriptorProto{Name: proto.String("unused"), Number: proto.Int32(9), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum()},
		)
	}
	if change != nil {
		change(file)
	}
	raw, err = proto.Marshal(&set)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.schema.Source.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	fixture.schema.Source.SHA256 = hex.EncodeToString(sum[:])
	fixture.contract, err = grpccontract.Parse(raw, fixture.schema.Services)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func protobufRulesSample(t *testing.T, fixture wireTestFixture) sample {
	t.Helper()
	return sample{schema: &fixture.schema, flow: artifact.Flow{Protocol: "grpc", Request: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: "/example.Example/Call"}}}}
}

func TestProtobufRulesFindEnumAndUnobservedFieldDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*descriptorpb.FileDescriptorProto)
	}{
		{"enum addition", func(f *descriptorpb.FileDescriptorProto) {
			f.EnumType[0].Value = append(f.EnumType[0].Value, &descriptorpb.EnumValueDescriptorProto{Name: proto.String("CANCELLED"), Number: proto.Int32(3)})
		}},
		{"enum removal", func(f *descriptorpb.FileDescriptorProto) { f.EnumType[0].Value = f.EnumType[0].Value[:2] }},
		{"enum number", func(f *descriptorpb.FileDescriptorProto) { f.EnumType[0].Value[2].Number = proto.Int32(3) }},
		{"observed field numeric type", func(f *descriptorpb.FileDescriptorProto) {
			f.MessageType[0].Field[1].Type = descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()
		}},
		{"unobserved field type", func(f *descriptorpb.FileDescriptorProto) {
			f.MessageType[0].Field[len(f.MessageType[0].Field)-1].Type = descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()
		}},
		{"unobserved optional field addition", func(f *descriptorpb.FileDescriptorProto) {
			f.MessageType[0].Field = append(f.MessageType[0].Field, &descriptorpb.FieldDescriptorProto{Name: proto.String("extra"), Number: proto.Int32(10), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()})
		}},
		{"json field name", func(f *descriptorpb.FileDescriptorProto) {
			f.MessageType[0].Field[0].JsonName = proto.String("renamedLabel")
		}},
		{"custom field option", func(f *descriptorpb.FileDescriptorProto) {
			f.MessageType[0].Field[0].Options = &descriptorpb.FieldOptions{}
			wire := protowire.AppendTag(nil, 50001, protowire.VarintType)
			f.MessageType[0].Field[0].Options.ProtoReflect().SetUnknown(protowire.AppendVarint(wire, 3))
		}},
		{"stream flag", func(f *descriptorpb.FileDescriptorProto) { f.Service[0].Method[0].ServerStreaming = proto.Bool(true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			consumer, provider := protobufRulesFixture(t, nil), protobufRulesFixture(t, tc.change)
			var verifier wireVerifier
			differences, err := verifier.rulesMatch(protobufRulesSample(t, consumer), protobufRulesSample(t, provider))
			if err != nil || len(differences) == 0 || !strings.HasPrefix(differences[0], "protobuf/contract/") {
				t.Fatalf("selected protobuf rule drift was missed: %v / %v", differences, err)
			}
		})
	}
}

func TestProtobufRulesIgnoreOrderCodeGenerationAndUnrelatedDeclarations(t *testing.T) {
	consumer := protobufRulesFixture(t, nil)
	provider := protobufRulesFixture(t, func(f *descriptorpb.FileDescriptorProto) {
		f.Name = proto.String("another-source-file.proto")
		f.Options = &descriptorpb.FileOptions{GoPackage: proto.String("provider.example/generated"), JavaPackage: proto.String("com.provider"), Deprecated: proto.Bool(true)}
		f.MessageType[0].Options = &descriptorpb.MessageOptions{Deprecated: proto.Bool(true)}
		f.MessageType[0].Field[0].Options = &descriptorpb.FieldOptions{Deprecated: proto.Bool(true)}
		for _, message := range f.MessageType[:2] {
			for left, right := 0, len(message.Field)-1; left < right; left, right = left+1, right-1 {
				message.Field[left], message.Field[right] = message.Field[right], message.Field[left]
			}
		}
		f.EnumType[0].Value[1], f.EnumType[0].Value[2] = f.EnumType[0].Value[2], f.EnumType[0].Value[1]
		f.EnumType[0].Options = &descriptorpb.EnumOptions{Deprecated: proto.Bool(true)}
		f.EnumType[0].Value[0].Options = &descriptorpb.EnumValueOptions{Deprecated: proto.Bool(true)}
		f.Service[0].Options = &descriptorpb.ServiceOptions{Deprecated: proto.Bool(true)}
		f.Service[0].Method[0].Options = &descriptorpb.MethodOptions{Deprecated: proto.Bool(true)}
		f.MessageType = append(f.MessageType, &descriptorpb.DescriptorProto{Name: proto.String("Unrelated")})
		f.Service[0].Method = append(f.Service[0].Method, &descriptorpb.MethodDescriptorProto{Name: proto.String("UnrelatedCall"), InputType: proto.String(".example.Unrelated"), OutputType: proto.String(".example.Unrelated")})
	})
	var verifier wireVerifier
	if differences, err := verifier.rulesMatch(protobufRulesSample(t, consumer), protobufRulesSample(t, provider)); err != nil || len(differences) != 0 {
		t.Fatalf("equivalent API rules differ because of ordering, generator settings or unused declarations: %v / %v", differences, err)
	}
}

func TestProtobufRulesHandleRecursiveGraphsAndUnavailableDescriptors(t *testing.T) {
	makeRecursive := func(f *descriptorpb.FileDescriptorProto) {
		f.MessageType[0].Field = append(f.MessageType[0].Field, &descriptorpb.FieldDescriptorProto{Name: proto.String("next"), Number: proto.Int32(10), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".example.InputPayload")})
	}
	consumer, provider := protobufRulesFixture(t, makeRecursive), protobufRulesFixture(t, makeRecursive)
	var verifier wireVerifier
	if differences, err := verifier.rulesMatch(protobufRulesSample(t, consumer), protobufRulesSample(t, provider)); err != nil || len(differences) != 0 {
		t.Fatalf("equivalent recursive graphs failed: %v / %v", differences, err)
	}
	missing := protobufRulesSample(t, provider)
	missing.schema = nil
	if _, err := verifier.rulesMatch(protobufRulesSample(t, consumer), missing); err == nil {
		t.Fatal("missing frozen descriptor was treated as equal")
	}
}

func TestProtobufRulesPreservePreferredAliasJSONNames(t *testing.T) {
	makeAliases := func(f *descriptorpb.FileDescriptorProto) {
		f.EnumType[0].Options = &descriptorpb.EnumOptions{AllowAlias: proto.Bool(true)}
		f.EnumType[0].Value = append(f.EnumType[0].Value, &descriptorpb.EnumValueDescriptorProto{Name: proto.String("UNSPECIFIED"), Number: proto.Int32(0)})
	}
	consumer := protobufRulesFixture(t, makeAliases)
	provider := protobufRulesFixture(t, func(f *descriptorpb.FileDescriptorProto) {
		makeAliases(f)
		values := f.EnumType[0].Value
		values[0], values[len(values)-1] = values[len(values)-1], values[0]
	})
	var verifier wireVerifier
	differences, err := verifier.rulesMatch(protobufRulesSample(t, consumer), protobufRulesSample(t, provider))
	if err != nil || len(differences) == 0 {
		t.Fatalf("unchanged name-number set hid a different preferred ProtoJSON alias: %v / %v", differences, err)
	}
	for _, path := range differences {
		if strings.Contains(path, "/json_names/") {
			return
		}
	}
	t.Fatalf("preferred enum label difference has no concrete diagnostic: %v", differences)
}

func TestProtobufRulesPreserveImportedEnumOpenness(t *testing.T) {
	makeFixture := func(syntax string) wireTestFixture {
		fixture := protobufRulesFixture(t, nil)
		raw, err := os.ReadFile(fixture.schema.Source.Path)
		if err != nil {
			t.Fatal(err)
		}
		var set descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(raw, &set); err != nil {
			t.Fatal(err)
		}
		root := set.File[0]
		root.Syntax = proto.String("proto2")
		enums := root.EnumType
		root.EnumType = nil
		root.Dependency = append(root.Dependency, "state.proto")
		set.File = append(set.File, &descriptorpb.FileDescriptorProto{Name: proto.String("state.proto"), Package: proto.String("example"), Syntax: proto.String(syntax), EnumType: enums})
		raw, err = proto.Marshal(&set)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.schema.Source.Path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		fixture.schema.Source.SHA256 = hex.EncodeToString(sum[:])
		fixture.contract, err = grpccontract.Parse(raw, fixture.schema.Services)
		if err != nil {
			t.Fatal(err)
		}
		return fixture
	}
	consumer, provider := makeFixture("proto2"), makeFixture("proto3")
	var verifier wireVerifier
	differences, err := verifier.rulesMatch(protobufRulesSample(t, consumer), protobufRulesSample(t, provider))
	if err != nil || len(differences) == 0 {
		t.Fatalf("unchanged root message syntax hid imported enum openness drift: %v / %v", differences, err)
	}
}

func TestProtobufRuleGraphLimitsAndExtensionsFailClosed(t *testing.T) {
	consumer := protobufRulesFixture(t, nil)
	for _, kind := range []string{"deep graph", "extension inventory"} {
		t.Run(kind, func(t *testing.T) {
			provider := protobufRulesFixture(t, func(f *descriptorpb.FileDescriptorProto) {
				if kind == "extension inventory" {
					f.Syntax = proto.String("proto2")
					f.MessageType[0].ExtensionRange = []*descriptorpb.DescriptorProto_ExtensionRange{{Start: proto.Int32(1000), End: proto.Int32(2000)}}
					return
				}
				f.MessageType[0].Field = append(f.MessageType[0].Field, &descriptorpb.FieldDescriptorProto{Name: proto.String("chain"), Number: proto.Int32(10), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".example.Node0")})
				for i := 0; i <= 129; i++ {
					node := &descriptorpb.DescriptorProto{Name: proto.String(fmt.Sprintf("Node%d", i))}
					if i < 129 {
						node.Field = []*descriptorpb.FieldDescriptorProto{{Name: proto.String("next"), Number: proto.Int32(1), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(fmt.Sprintf(".example.Node%d", i+1))}}
					}
					f.MessageType = append(f.MessageType, node)
				}
			})
			var verifier wireVerifier
			if _, err := verifier.rulesMatch(protobufRulesSample(t, consumer), protobufRulesSample(t, provider)); err == nil {
				t.Fatalf("incomplete or over-budget rule projection passed: %s", kind)
			}
		})
	}
}

func protobufRulesCapture(t *testing.T, fixture wireTestFixture, provider bool) *artifact.Artifact {
	t.Helper()
	makeMessage := func(response bool, sequence uint64) *artifact.GrpcMessage {
		message := wireTestMessage(t, fixture, response)
		descriptor := fixture.contract.Methods["/example.Example/Call"].Input()
		if response {
			descriptor = fixture.contract.Methods["/example.Example/Call"].Output()
		}
		value := dynamicpb.NewMessage(descriptor)
		raw, err := base64.StdEncoding.DecodeString(message.Messages[0].Wire.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if err := proto.Unmarshal(raw, value); err != nil {
			t.Fatal(err)
		}
		value.Set(descriptor.Fields().ByName("state"), protoreflect.ValueOfEnum(1))
		raw, err = proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		projection, err := (protojson.MarshalOptions{UseProtoNames: true, Resolver: dynamicpb.NewTypes(fixture.contract.Files)}).Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		message.Service, message.Method, message.SchemaRef, message.StreamKind = "example.Example", "Call", "example", "unary"
		message.ObservedBytes = int64(len(raw) + 5)
		message.Observation = &artifact.GrpcObservation{CompleteFrames: 1, RetainedFrames: 1, ObservedBytes: message.ObservedBytes, CountsComplete: true, EOF: true, End: "eof"}
		message.Messages[0].Wire = &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(raw), OriginalBytes: int64(len(raw))}
		message.Messages[0].Body = &artifact.Body{Encoding: "json", Text: string(projection)}
		message.Messages[0].Observation = &artifact.GrpcPayloadObservation{WireBytes: uint64(len(raw)), StartSequence: sequence, CompleteSequence: sequence + 1, StartedOffsetNS: int64(sequence), CompletedOffsetNS: int64(sequence + 1)}
		return message
	}
	req, res := makeMessage(false, 0), makeMessage(true, 2)
	res.HTTPStatus, res.StatusSource = 200, "trailers"
	schema := fixture.schema
	schema.Role, schema.AssetID = "dependency", "stock"
	if provider {
		schema.Role, schema.AssetID = "sut", "inventory-api"
	}
	capture := checkCapture(provider, artifact.Flow{Protocol: "grpc", Request: artifact.Message{GRPC: req}, Response: artifact.Message{GRPC: res}})
	capture.Schemas = []artifact.SchemaRef{schema}
	return capture
}

func TestCheckProtobufEnumDriftFailsDespiteMatchingObservedJSONAndWire(t *testing.T) {
	for _, kind := range []string{"addition", "removal"} {
		t.Run(kind, func(t *testing.T) {
			consumerFixture := protobufRulesFixture(t, nil)
			providerFixture := protobufRulesFixture(t, func(f *descriptorpb.FileDescriptorProto) {
				if kind == "addition" {
					f.EnumType[0].Value = append(f.EnumType[0].Value, &descriptorpb.EnumValueDescriptorProto{Name: proto.String("CANCELLED"), Number: proto.Int32(3)})
				} else {
					f.EnumType[0].Value = f.EnumType[0].Value[:2]
				}
			})
			consumer, provider := protobufRulesCapture(t, consumerFixture, false), protobufRulesCapture(t, providerFixture, true)
			consumerReq, consumerRes := normalizedSignatures(t, consumer.Traffic[0])
			providerReq, providerRes := normalizedSignatures(t, provider.Traffic[0])
			if consumerReq != providerReq || consumerRes != providerRes {
				t.Fatal("enum-drift fixture must have the same observed request and response values")
			}
			options := checkHTTPPair(t, []*artifact.Artifact{consumer}, []*artifact.Artifact{provider})
			for _, mode := range []string{"consumer", "provider"} {
				options.Mode = mode
				report := mustCheck(t, options)
				if report.Compatible || report.Counts.Mismatched != 1 || report.Counts.Verified != 0 || !findingCode(report, "protobuf_contract_mismatch") {
					encoded, _ := json.Marshal(report)
					t.Fatalf("%s certified enum declaration drift because READY still matched: %s", mode, encoded)
				}
			}
		})
	}
}
