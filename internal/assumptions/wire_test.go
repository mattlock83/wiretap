package assumptions

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
)

type wireTestFixture struct {
	schema   artifact.SchemaRef
	contract *grpccontract.Contract
}

func newWireTestFixture(t *testing.T, tag int32, additive bool) wireTestFixture {
	t.Helper()
	field := func(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type, typeName string, repeated bool) *descriptorpb.FieldDescriptorProto {
		label := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
		if repeated {
			label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED
		}
		f := &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(number), Type: &kind, Label: &label}
		if typeName != "" {
			f.TypeName = proto.String(typeName)
		}
		return f
	}
	input := &descriptorpb.DescriptorProto{Name: proto.String("InputPayload"), Field: []*descriptorpb.FieldDescriptorProto{
		field("label", tag, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", false),
		field("count", 3, descriptorpb.FieldDescriptorProto_TYPE_INT32, "", false),
		field("child", 4, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".example.Child", false),
		field("children", 5, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".example.Child", true),
		field("child_map", 6, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".example.InputPayload.ChildMapEntry", true),
		field("detail", 7, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".google.protobuf.Any", false),
	}, NestedType: []*descriptorpb.DescriptorProto{{Name: proto.String("ChildMapEntry"), Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)}, Field: []*descriptorpb.FieldDescriptorProto{
		field("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", false), field("value", 2, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".example.Child", false),
	}}}}
	output := &descriptorpb.DescriptorProto{Name: proto.String("OutputPayload"), Field: []*descriptorpb.FieldDescriptorProto{field("label", tag+10, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", false), field("count", 3, descriptorpb.FieldDescriptorProto_TYPE_INT32, "", false)}}
	if additive {
		input.Field = append(input.Field, field("unrelated", 100, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", false))
	}
	file := &descriptorpb.FileDescriptorProto{Name: proto.String("wire.proto"), Package: proto.String("example"), Syntax: proto.String("proto3"), Dependency: []string{"google/protobuf/any.proto"}, MessageType: []*descriptorpb.DescriptorProto{
		input, output, {Name: proto.String("Child"), Field: []*descriptorpb.FieldDescriptorProto{field("value", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", false)}},
	}, Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Example"), Method: []*descriptorpb.MethodDescriptorProto{{Name: proto.String("Call"), InputType: proto.String(".example.InputPayload"), OutputType: proto.String(".example.OutputPayload")}}}}}
	raw, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file, protodesc.ToFileDescriptorProto(anypb.File_google_protobuf_any_proto)}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "descriptor-set.protoset")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	schema := artifact.SchemaRef{ID: "example", Type: "protobuf", Services: []string{"example.Example"}, Source: artifact.SchemaSource{Type: "file", Path: path, Format: "file_descriptor_set", SHA256: hex.EncodeToString(sum[:])}}
	contract, err := grpccontract.Parse(raw, schema.Services)
	if err != nil {
		t.Fatal(err)
	}
	return wireTestFixture{schema, contract}
}

func wireTestMessage(t *testing.T, fixture wireTestFixture, response bool) *artifact.GrpcMessage {
	t.Helper()
	descriptor := fixture.contract.Methods["/example.Example/Call"].Input()
	if response {
		descriptor = fixture.contract.Methods["/example.Example/Call"].Output()
	}
	message := dynamicpb.NewMessage(descriptor)
	message.Set(descriptor.Fields().ByName("label"), protoreflect.ValueOfString("value"))
	message.Set(descriptor.Fields().ByName("count"), protoreflect.ValueOfInt32(7))
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := (protojson.MarshalOptions{UseProtoNames: true, Resolver: dynamicpb.NewTypes(fixture.contract.Files)}).Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return &artifact.GrpcMessage{Kind: "grpc", Procedure: "/example.Example/Call", Messages: []artifact.GrpcPayload{{Index: 0, Type: string(descriptor.FullName()), DecodeState: "decoded", Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(wire), OriginalBytes: int64(len(wire))}, Body: &artifact.Body{Encoding: "json", Text: string(projection)}}}}
}

func TestWireVerifierRejectsChangedFieldNumbersDespiteIdenticalJSON(t *testing.T) {
	old, changed := newWireTestFixture(t, 1, false), newWireTestFixture(t, 2, false)
	for _, response := range []bool{false, true} {
		source, target := wireTestMessage(t, old, response), wireTestMessage(t, changed, response)
		left, _ := normalizeBody(source.Messages[0].Body)
		right, _ := normalizeBody(target.Messages[0].Body)
		leftJSON, _ := jsonForWireTest(left)
		rightJSON, _ := jsonForWireTest(right)
		if leftJSON != rightJSON {
			t.Fatal("test fixture must have identical JSON despite changed wire tags")
		}
		var verifier wireVerifier
		if err := verifier.verify(source, target, &changed.schema, response); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("changed field tags were certified from JSON alone: %v", err)
		}
	}
}

func jsonForWireTest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func TestWireVerifierAllowsAdditiveContractsAndSerializationOrder(t *testing.T) {
	old, additive := newWireTestFixture(t, 1, false), newWireTestFixture(t, 1, true)
	source, target := wireTestMessage(t, old, false), wireTestMessage(t, additive, false)
	// Reverse known field serialization order without changing message meaning.
	raw := protowire.AppendTag(nil, 3, protowire.VarintType)
	raw = protowire.AppendVarint(raw, 7)
	raw = protowire.AppendTag(raw, 1, protowire.BytesType)
	raw = protowire.AppendString(raw, "value")
	source.Messages[0].Wire.Bytes = base64.StdEncoding.EncodeToString(raw)
	var verifier wireVerifier
	if err := verifier.verify(source, target, &additive.schema, false); err != nil {
		t.Fatal(err)
	}
	if err := verifier.verify(wireTestMessage(t, old, true), wireTestMessage(t, additive, true), &additive.schema, true); err != nil {
		t.Fatal(err)
	}
	if len(verifier.contracts) != 1 {
		t.Fatalf("descriptor was linked repeatedly instead of cached: %d", len(verifier.contracts))
	}
	// The cached verified descriptor is an immutable in-memory snapshot.
	if err := os.Remove(additive.schema.Source.Path); err != nil {
		t.Fatal(err)
	}
	if err := verifier.verify(source, target, &additive.schema, false); err != nil {
		t.Fatalf("already verified descriptor was reread: %v", err)
	}
}

func compressedWireTestPayload(t *testing.T, raw []byte) *artifact.Body {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(compressed.Bytes()), OriginalBytes: int64(compressed.Len())}
}

func TestWireVerifierGzipAndBounds(t *testing.T) {
	fixture := newWireTestFixture(t, 1, false)
	source, target := wireTestMessage(t, fixture, false), wireTestMessage(t, fixture, false)
	raw, err := base64.StdEncoding.DecodeString(source.Messages[0].Wire.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	source.Messages[0].Compressed = true
	source.Messages[0].Wire = compressedWireTestPayload(t, raw)
	source.Metadata = artifact.Headers{"Grpc-Encoding": {"gzip"}}
	var verifier wireVerifier
	if err := verifier.verify(source, target, &fixture.schema, false); err != nil {
		t.Fatal(err)
	}
	source.Metadata["Grpc-Encoding"] = []string{"br"}
	if err := verifier.verify(source, target, &fixture.schema, false); err == nil {
		t.Fatal("unsupported compression was accepted")
	}
	source.Metadata["Grpc-Encoding"] = []string{"gzip"}
	source.Messages[0].Wire = compressedWireTestPayload(t, bytes.Repeat([]byte{0}, maxComparedWireBytes+1))
	if err := verifier.verify(source, target, &fixture.schema, false); err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatalf("gzip expansion limit was bypassed: %v", err)
	}
	source.Messages[0].Compressed = false
	source.Messages[0].Wire = &artifact.Body{Encoding: "base64", Bytes: strings.Repeat("A", base64.StdEncoding.EncodedLen(maxComparedWireBytes)+4)}
	if err := verifier.verify(source, target, &fixture.schema, false); err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatalf("wire size limit was bypassed: %v", err)
	}
	source = wireTestMessage(t, fixture, false)
	source.Messages[0].Wire.Truncated = true
	if err := verifier.verify(source, target, &fixture.schema, false); err == nil {
		t.Fatal("truncated wire was accepted")
	}
	if err := verifier.verify(&artifact.GrpcMessage{}, target, &fixture.schema, false); err == nil {
		t.Fatal("unequal message counts were accepted")
	}
}

func TestWireVerifierRejectsUnknownFieldsRecursively(t *testing.T) {
	fixture := newWireTestFixture(t, 1, false)
	resolver := dynamicpb.NewTypes(fixture.contract.Files)
	descriptor := fixture.contract.Methods["/example.Example/Call"].Input()
	childDescriptor, err := fixture.contract.Files.FindDescriptorByName("example.Child")
	if err != nil {
		t.Fatal(err)
	}
	unknown := protowire.AppendTag(nil, 99, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	for _, kind := range []string{"root", "child", "children", "child_map", "Any"} {
		t.Run(kind, func(t *testing.T) {
			message := dynamicpb.NewMessage(descriptor)
			child := dynamicpb.NewMessage(childDescriptor.(protoreflect.MessageDescriptor))
			child.SetUnknown(unknown)
			switch kind {
			case "root":
				message.SetUnknown(unknown)
			case "child":
				message.Set(descriptor.Fields().ByName("child"), protoreflect.ValueOfMessage(child))
			case "children":
				message.Mutable(descriptor.Fields().ByName("children")).List().Append(protoreflect.ValueOfMessage(child))
			case "child_map":
				message.Mutable(descriptor.Fields().ByName("child_map")).Map().Set(protoreflect.ValueOfString("key").MapKey(), protoreflect.ValueOfMessage(child))
			case "Any":
				raw, err := proto.Marshal(child)
				if err != nil {
					t.Fatal(err)
				}
				value := message.Mutable(descriptor.Fields().ByName("detail")).Message()
				value.Set(value.Descriptor().Fields().ByName("type_url"), protoreflect.ValueOfString("type.googleapis.com/example.Child"))
				value.Set(value.Descriptor().Fields().ByName("value"), protoreflect.ValueOfBytes(raw))
			}
			if err := rejectUnknownWire(message, resolver, 0); err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("unknown %s fields were lost: %v", kind, err)
			}
		})
	}
}

func TestWireVerifierChecksFrozenDescriptorIdentityAndLimits(t *testing.T) {
	for _, kind := range []string{"missing schema", "hash mismatch", "relative path", "oversized descriptor", "missing selected method"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newWireTestFixture(t, 1, false)
			source, target := wireTestMessage(t, fixture, false), wireTestMessage(t, fixture, false)
			schema := &fixture.schema
			switch kind {
			case "missing schema":
				schema = nil
			case "hash mismatch":
				fixture.schema.Source.SHA256 = strings.Repeat("0", 64)
			case "relative path":
				fixture.schema.Source.Path = "descriptor-set.protoset"
			case "oversized descriptor":
				if err := os.Truncate(fixture.schema.Source.Path, maxComparedDescriptorBytes+1); err != nil {
					t.Fatal(err)
				}
			case "missing selected method":
				source.Procedure, target.Procedure = "/example.Example/Missing", "/example.Example/Missing"
			}
			var verifier wireVerifier
			if err := verifier.verify(source, target, schema, false); err == nil {
				t.Fatalf("invalid frozen descriptor accepted: %s", kind)
			}
		})
	}
}
