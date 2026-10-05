package bundle

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/grpccontract"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func validateContractResolutions(run Run) error {
	seen := map[string]bool{}
	for _, state := range run.Contracts {
		key := state.Role + ":" + state.APIID
		if seen[key] || state.APIID == "" || state.Protocol != "grpc" || (state.Role != "sut" && state.Role != "dependency") {
			return fmt.Errorf("invalid or duplicate boundary contract %q", key)
		}
		seen[key] = true
		if state.State != "pending" && state.State != "failed" && state.State != "resolved" {
			return fmt.Errorf("invalid contract resolution state %q", state.State)
		}
		if state.State != "resolved" {
			if run.RunState == "complete" {
				return fmt.Errorf("complete run contains unresolved gRPC contract %q", key)
			}
			continue
		}
		found := false
		for _, schema := range run.Schemas {
			if schema.Type == "protobuf" && schema.Role == state.Role && schema.ID == state.APIID && (state.Role == "sut" || schema.AssetID == state.APIID) && equalServices(schema.Services, state.Services) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("resolved gRPC contract %q has no matching frozen schema", key)
		}
	}
	return nil
}

func equalServices(left, right []string) bool {
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	return sameJSON(a, b)
}

func validateGRPCSnapshot(run Run, capture artifact.Artifact) error {
	for _, ref := range capture.Schemas {
		if ref.Type != "protobuf" {
			continue
		}
		found := false
		for _, frozen := range run.Schemas {
			if frozen.Type == "protobuf" && frozen.ID == ref.ID && frozen.Role == ref.Role && frozen.AssetID == ref.AssetID && frozen.Source.SHA256 == ref.Source.SHA256 && equalServices(frozen.Services, ref.Services) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("gRPC schema %q disagrees with run contract snapshot", ref.ID)
		}
	}
	return nil
}

func grpcSchemaFor(flow artifact.Flow, refs []artifact.SchemaRef) *artifact.SchemaRef {
	for i := range refs {
		ref := &refs[i]
		if ref.Type != "protobuf" || flow.Request.GRPC == nil || ref.ID != flow.Request.GRPC.SchemaRef {
			continue
		}
		if flow.Channel == "test_to_sut" && ref.Role == "sut" || flow.Channel == "sut_to_dependency" && ref.Role == "dependency" && ref.AssetID == flow.AssetID {
			return ref
		}
	}
	return nil
}

func validateGRPCMessages(version string, capture artifact.Artifact) error {
	for _, flow := range capture.Traffic {
		if flow.Interaction != nil {
			continue
		}
		hasGRPC := flow.Request.GRPC != nil || flow.Response.GRPC != nil
		if flow.Protocol != "grpc" {
			if hasGRPC {
				return fmt.Errorf("flow %q protocol disagrees with gRPC message kind", flow.ID)
			}
			continue
		}
		if !supportedEvidenceVersion(version) {
			return fmt.Errorf("gRPC flow %q requires bundle format version 3", flow.ID)
		}
		if flow.Request.GRPC == nil || flow.Response.GRPC == nil {
			return fmt.Errorf("gRPC flow %q requires tagged request and response", flow.ID)
		}
		if grpcSchemaFor(flow, capture.Schemas) == nil {
			return fmt.Errorf("gRPC flow %q references an unavailable boundary schema", flow.ID)
		}
		for _, msg := range []*artifact.GrpcMessage{flow.Request.GRPC, flow.Response.GRPC} {
			if len(msg.Messages) > 1024 {
				return fmt.Errorf("gRPC flow %q exceeds retained message limit", flow.ID)
			}
			if msg.Kind != "grpc" || msg.Procedure != flow.Request.GRPC.Procedure || msg.SchemaRef != flow.Request.GRPC.SchemaRef {
				return fmt.Errorf("gRPC flow %q has inconsistent procedure, schema or message kind", flow.ID)
			}
			parts := strings.Split(strings.TrimPrefix(msg.Procedure, "/"), "/")
			if !strings.HasPrefix(msg.Procedure, "/") || len(parts) != 2 || parts[0] == "" || parts[1] == "" || msg.Service != parts[0] || msg.Method != parts[1] {
				return fmt.Errorf("gRPC flow %q has inconsistent method identity", flow.ID)
			}
			if msg.StatusCode < -1 || msg.StatusCode > 16 {
				return fmt.Errorf("gRPC flow %q has invalid terminal status", flow.ID)
			}
			for index, payload := range msg.Messages {
				if payload.Index != index {
					return fmt.Errorf("gRPC flow %q has unordered message indices", flow.ID)
				}
				if payload.Redacted && payload.Wire != nil {
					return fmt.Errorf("gRPC flow %q retains redacted wire bytes", flow.ID)
				}
				if payload.Body != nil && payload.Body.Encoding != "json" {
					return fmt.Errorf("gRPC flow %q has invalid projection encoding", flow.ID)
				}
				if payload.Wire != nil && payload.Wire.Encoding != "base64" {
					return fmt.Errorf("gRPC flow %q has invalid wire encoding", flow.ID)
				}
			}
		}
	}
	return nil
}

// Projections are convenient to read but never an independent source of truth.
// Check them against the verified descriptor and wire attachment on every load.
func verifyGRPCPayloads(capture artifact.Artifact) error {
	contracts := map[string]*grpccontract.Contract{}
	for _, flow := range capture.Traffic {
		if flow.Protocol != "grpc" || flow.Interaction != nil {
			continue
		}
		ref := grpcSchemaFor(flow, capture.Schemas)
		contract := contracts[ref.Source.Path+strings.Join(ref.Services, ",")]
		if contract == nil {
			raw, err := os.ReadFile(ref.Source.Path)
			if err != nil {
				return err
			}
			contract, err = grpccontract.Parse(raw, ref.Services)
			if err != nil {
				return err
			}
			contracts[ref.Source.Path+strings.Join(ref.Services, ",")] = contract
		}
		method := contract.Methods[flow.Request.GRPC.Procedure]
		for direction, msg := range []*artifact.GrpcMessage{flow.Request.GRPC, flow.Response.GRPC} {
			var desc protoreflect.MessageDescriptor
			if method != nil {
				desc = method.Input()
				if direction == 1 {
					desc = method.Output()
				}
			}
			for _, payload := range msg.Messages {
				if payload.Redacted || payload.DecodeError != "" {
					continue
				}
				if payload.Wire == nil || payload.Body == nil || desc == nil {
					return fmt.Errorf("gRPC flow %q claims decoded message without verified source", flow.ID)
				}
				if payload.Type != "" && payload.Type != string(desc.FullName()) {
					return fmt.Errorf("gRPC flow %q message type disagrees with descriptor", flow.ID)
				}
				raw, err := base64.StdEncoding.DecodeString(payload.Wire.Bytes)
				if err != nil {
					return err
				}
				if len(raw) > 16<<20 {
					return fmt.Errorf("gRPC wire message exceeds 16 MiB")
				}
				if payload.Compressed {
					encoding := ""
					for key, values := range msg.Metadata {
						if strings.EqualFold(key, "grpc-encoding") && len(values) > 0 {
							encoding = values[0]
						}
					}
					if encoding != "gzip" {
						return fmt.Errorf("gRPC decoded message has unsupported compression %q", encoding)
					}
					reader, err := gzip.NewReader(bytes.NewReader(raw))
					if err != nil {
						return err
					}
					raw, err = io.ReadAll(io.LimitReader(reader, (16<<20)+1))
					_ = reader.Close()
					if err != nil || len(raw) > 16<<20 {
						return fmt.Errorf("gRPC decompressed message exceeds limit or is invalid")
					}
				}
				message := dynamicpb.NewMessage(desc)
				if err := proto.Unmarshal(raw, message); err != nil {
					return fmt.Errorf("gRPC flow %q wire decode: %w", flow.ID, err)
				}
				projection, err := (protojson.MarshalOptions{UseProtoNames: true, Resolver: dynamicpb.NewTypes(contract.Files)}).Marshal(message)
				if err != nil {
					return fmt.Errorf("gRPC flow %q projection: %w", flow.ID, err)
				}
				if !equivalentJSON(projection, []byte(payload.Body.Text)) {
					return fmt.Errorf("gRPC flow %q projection disagrees with wire payload", flow.ID)
				}
			}
		}
	}
	return nil
}

func equivalentJSON(left, right []byte) bool {
	decode := func(raw []byte) (any, error) {
		var value any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		err := d.Decode(&value)
		if err == nil {
			var extra any
			if d.Decode(&extra) != io.EOF {
				return nil, fmt.Errorf("trailing JSON")
			}
		}
		return value, err
	}
	a, err := decode(left)
	if err != nil {
		return false
	}
	b, err := decode(right)
	if err != nil {
		return false
	}
	return sameJSON(a, b)
}
