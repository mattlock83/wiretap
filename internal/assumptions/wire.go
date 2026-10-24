package assumptions

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const maxComparedWireBytes = 16 << 20
const maxComparedDescriptorBytes = 64 << 20

type wireContract struct {
	contract *grpccontract.Contract
	types    *dynamicpb.Types
}

// A verifier belongs to one Check call. Frozen contracts with the same content
// and selected services share their linked descriptors without a global cache.
type wireVerifier struct {
	contracts map[string]*wireContract
}

// verify decodes the source bytes using the target contract, then checks the
// resulting application value against the target test's captured projection.
// Equal ProtoJSON alone cannot establish this: changed field numbers can retain
// identical names and values while the actual messages become incompatible.
func (v *wireVerifier) verify(source, target *artifact.GrpcMessage, schema *artifact.SchemaRef, response bool) error {
	if source == nil || target == nil || len(source.Messages) != len(target.Messages) {
		return fmt.Errorf("gRPC source and target need the same retained message count")
	}
	contract, err := v.load(schema)
	if err != nil {
		return err
	}
	procedure := "/" + strings.TrimPrefix(target.Procedure, "/")
	if strings.TrimPrefix(source.Procedure, "/") != strings.TrimPrefix(target.Procedure, "/") {
		return fmt.Errorf("gRPC source and target procedures disagree")
	}
	method := contract.contract.Methods[procedure]
	if method == nil {
		return fmt.Errorf("gRPC procedure %q is not in the selected target contract", procedure)
	}
	descriptor := method.Input()
	if response {
		descriptor = method.Output()
	}
	for i, payload := range source.Messages {
		raw, err := comparedWireBytes(payload, source.Metadata)
		if err != nil {
			return fmt.Errorf("gRPC message %d: %w", i, err)
		}
		message := dynamicpb.NewMessage(descriptor)
		if err := (proto.UnmarshalOptions{RecursionLimit: 100, Resolver: contract.types}).Unmarshal(raw, message); err != nil {
			return fmt.Errorf("gRPC message %d cannot decode under the target contract: %w", i, err)
		}
		if err := rejectUnknownWire(message.ProtoReflect(), contract.types, 0); err != nil {
			return fmt.Errorf("gRPC message %d: %w", i, err)
		}
		projection, err := (protojson.MarshalOptions{UseProtoNames: true, Resolver: contract.types}).Marshal(message)
		if err != nil {
			return fmt.Errorf("gRPC message %d cannot project under the target contract: %w", i, err)
		}
		expected, err := normalizeBody(target.Messages[i].Body)
		if err != nil || expected.Representation != "json" {
			return fmt.Errorf("gRPC target message %d needs complete decoded JSON", i)
		}
		actual, err := normalizeBody(&artifact.Body{Encoding: "json", Text: string(projection)})
		if err != nil || !reflect.DeepEqual(expected, actual) {
			return fmt.Errorf("gRPC message %d decodes to a different value under the target contract", i)
		}
	}
	return nil
}

func (v *wireVerifier) load(schema *artifact.SchemaRef) (*wireContract, error) {
	if schema == nil || schema.Type != "protobuf" || schema.Source.Type != "file" || schema.Source.Format != "file_descriptor_set" || !filepath.IsAbs(schema.Source.Path) || schema.Source.SHA256 == "" || len(schema.Services) == 0 {
		return nil, fmt.Errorf("a verified frozen target protobuf descriptor and selected services are required")
	}
	services := append([]string(nil), schema.Services...)
	sort.Strings(services)
	encoded, _ := json.Marshal(services)
	key := schema.Source.SHA256 + ":" + string(encoded)
	if cached := v.contracts[key]; cached != nil {
		return cached, nil
	}
	file, err := os.Open(schema.Source.Path)
	if err != nil {
		return nil, fmt.Errorf("read frozen target protobuf descriptor: %w", err)
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() || info.Size() > maxComparedDescriptorBytes {
		return nil, fmt.Errorf("frozen target protobuf descriptor must be a regular file up to 64 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxComparedDescriptorBytes+1))
	if err != nil || len(raw) > maxComparedDescriptorBytes {
		return nil, fmt.Errorf("read frozen target protobuf descriptor: invalid data or exceeds 64 MiB")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != schema.Source.SHA256 {
		return nil, fmt.Errorf("frozen target protobuf descriptor hash mismatch")
	}
	// Reuse the bundle's descriptor parser, including its stricter 16 MiB,
	// 256-file and selected-service limits. No imports or URLs are fetched.
	contract, err := grpccontract.Parse(raw, services)
	if err != nil {
		return nil, fmt.Errorf("parse frozen target protobuf descriptor: %w", err)
	}
	result := &wireContract{contract: contract, types: dynamicpb.NewTypes(contract.Files)}
	if v.contracts == nil {
		v.contracts = map[string]*wireContract{}
	}
	v.contracts[key] = result
	return result, nil
}

func comparedWireBytes(payload artifact.GrpcPayload, headers artifact.Headers) ([]byte, error) {
	if payload.Redacted || payload.Wire == nil || payload.Wire.Encoding != "base64" || payload.Wire.Text != "" || len(payload.Wire.Bytes) > base64.StdEncoding.EncodedLen(maxComparedWireBytes) {
		return nil, fmt.Errorf("complete retained protobuf wire bytes up to 16 MiB are required")
	}
	raw, err := retainedBodyBytes(payload.Wire)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxComparedWireBytes {
		return nil, fmt.Errorf("protobuf wire bytes exceed 16 MiB")
	}
	if !payload.Compressed {
		return raw, nil
	}
	var encodings []string
	for name, values := range headers {
		if strings.EqualFold(name, "grpc-encoding") {
			encodings = append(encodings, values...)
		}
	}
	if len(encodings) != 1 || encodings[0] != "gzip" {
		return nil, fmt.Errorf("compressed protobuf message requires an unambiguous grpc-encoding: gzip")
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid gzip protobuf message: %w", err)
	}
	defer reader.Close()
	raw, err = io.ReadAll(io.LimitReader(reader, maxComparedWireBytes+1))
	if err != nil || len(raw) > maxComparedWireBytes {
		return nil, fmt.Errorf("decompressed protobuf message exceeds 16 MiB or is invalid")
	}
	return raw, nil
}

func rejectUnknownWire(message protoreflect.Message, resolver *dynamicpb.Types, depth int) error {
	if depth > 100 {
		return fmt.Errorf("protobuf message nesting exceeds comparison limit")
	}
	if len(message.GetUnknown()) != 0 {
		return fmt.Errorf("protobuf message %q contains wire fields unknown to the target contract", message.Descriptor().FullName())
	}
	// Any.Value contains a nested protobuf message in a bytes field. Traversing
	// ordinary message fields alone would silently miss unknown fields inside it.
	if message.Descriptor().FullName() == "google.protobuf.Any" {
		fields := message.Descriptor().Fields()
		typeURL := message.Get(fields.ByName("type_url")).String()
		if typeURL != "" {
			kind, err := resolver.FindMessageByURL(typeURL)
			if err != nil {
				return fmt.Errorf("protobuf Any type %q is unavailable in the target contract", typeURL)
			}
			nested := kind.New()
			if err := (proto.UnmarshalOptions{RecursionLimit: 100, Resolver: resolver}).Unmarshal(message.Get(fields.ByName("value")).Bytes(), nested.Interface()); err != nil {
				return fmt.Errorf("protobuf Any value cannot decode under the target contract: %w", err)
			}
			if err := rejectUnknownWire(nested, resolver, depth+1); err != nil {
				return err
			}
		}
	}
	var problem error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			if field.MapValue().Kind() == protoreflect.MessageKind || field.MapValue().Kind() == protoreflect.GroupKind {
				value.Map().Range(func(_ protoreflect.MapKey, entry protoreflect.Value) bool {
					problem = rejectUnknownWire(entry.Message(), resolver, depth+1)
					return problem == nil
				})
			}
		} else if field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind {
			if field.IsList() {
				for i := 0; i < value.List().Len() && problem == nil; i++ {
					problem = rejectUnknownWire(value.List().Get(i).Message(), resolver, depth+1)
				}
			} else {
				problem = rejectUnknownWire(value.Message(), resolver, depth+1)
			}
		}
		return problem == nil
	})
	return problem
}
