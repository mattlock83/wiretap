package explore

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/grpccontract"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type grpcSeed struct {
	contract  *grpccontract.Contract
	validator protovalidate.Validator
	method    protoreflect.MethodDescriptor
	metadata  http.Header
	messages  []proto.Message
	gates     []int
	schemaID  string
}
type grpcMutation struct {
	index int
	path  string
	rule  string
	apply func(proto.Message) error
}

func prepareGRPC(seed *artifact.Artifact, ref artifact.SchemaRef, apiID string, selection Selection, cfg Config) (*Plan, error) {
	raw, err := os.ReadFile(ref.Source.Path)
	if err != nil {
		return nil, err
	}
	contract, err := grpccontract.Parse(raw, ref.Services)
	if err != nil {
		return nil, err
	}
	validator, err := protovalidate.New(protovalidate.WithExtensionTypeResolver(dynamicpb.NewTypes(contract.Files)))
	if err != nil {
		return nil, err
	}
	var steps []step
	var previousEnd time.Time
	for _, flow := range seed.Traffic {
		if flow.Channel != "test_to_sut" {
			continue
		}
		if err := artifact.ValidateGRPCObservation(flow); err != nil {
			return nil, fmt.Errorf("invalid gRPC capture: %w", err)
		}
		if flow.Protocol != "grpc" || flow.Request.GRPC == nil || flow.Response.GRPC == nil {
			return nil, fmt.Errorf("gRPC exploration requires native gRPC provider flows")
		}
		if flow.TransportError != "" || !grpcUpstreamStatus(flow.Response.GRPC) || flow.Response.GRPC.StatusCode != 0 || flow.Response.GRPC.HTTPStatus != 200 {
			return nil, fmt.Errorf("seed RPC %s must finish successfully", flow.ID)
		}
		if !artifact.GRPCDirectionComplete(flow.Request.GRPC, true) || !artifact.GRPCDirectionComplete(flow.Response.GRPC, true) {
			return nil, fmt.Errorf("seed RPC %s requires complete bounded request and response observations", flow.ID)
		}
		if !previousEnd.IsZero() && flow.StartedAt.Before(previousEnd) {
			return nil, fmt.Errorf("concurrent provider RPCs cannot be explored as a sequential scenario")
		}
		previousEnd = flow.StartedAt.Add(time.Duration(flow.DurationMS) * time.Millisecond)
		method := contract.Methods[flow.Request.GRPC.Procedure]
		if method == nil {
			return nil, fmt.Errorf("RPC %s is absent from the frozen provider contract", flow.Request.GRPC.Procedure)
		}
		messages, err := grpcPayloadMessages(flow.Request.GRPC, method.Input(), contract)
		if err != nil {
			return nil, err
		}
		responses, err := grpcPayloadMessages(flow.Response.GRPC, method.Output(), contract)
		if err != nil {
			return nil, err
		}
		for _, message := range append(append([]proto.Message(nil), messages...), responses...) {
			if err = validator.Validate(message); err != nil {
				return nil, fmt.Errorf("seed RPC %s does not satisfy its declared validation rules: %w", flow.ID, err)
			}
		}
		metadata := http.Header(flow.Request.GRPC.Metadata).Clone()
		for key := range metadata {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, "grpc-") || lower == "content-type" || lower == "content-length" || lower == "te" || lower == "traceparent" || lower == "tracestate" || lower == "authorization" || lower == "proxy-authorization" || lower == "cookie" || lower == "x-wiretap-session-id" {
				delete(metadata, key)
			}
		}
		gates := make([]int, len(messages))
		for i, request := range flow.Request.GRPC.Messages {
			for _, response := range flow.Response.GRPC.Messages {
				if response.Observation.CompleteSequence < request.Observation.StartSequence {
					gates[i]++
				}
			}
		}
		steps = append(steps, step{ID: flow.ID, Operation: flow.Request.GRPC.Procedure, GRPC: &grpcSeed{contract: contract, validator: validator, method: method, metadata: metadata, messages: messages, gates: gates, schemaID: ref.ID}})
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("seed has no provider gRPC requests")
	}
	selected, operations, err := selectOperations(steps, selection, &cfg)
	if err != nil {
		return nil, err
	}
	revision, err := grpccontract.SemanticRevision(contract.Bytes, contract.Services)
	if err != nil {
		return nil, err
	}
	mutations, notes, err := generateGRPC(steps, selected, apiID, revision, cfg.ExpectedGRPCStatuses)
	if err != nil {
		return nil, err
	}
	if len(mutations) == 0 {
		return nil, fmt.Errorf("no supported isolated invalid-input cases are declared by this Protobuf contract; use supported buf.validate rules (plain Protobuf types do not imply business validation)")
	}
	for _, binding := range cfg.Bindings {
		if binding.In != "body" && binding.In != "metadata" {
			return nil, fmt.Errorf("gRPC bindings support body and metadata")
		}
		if binding.FlowID != "" && !hasStep(steps, binding.FlowID) {
			return nil, fmt.Errorf("binding refers to unknown provider flow %q", binding.FlowID)
		}
	}
	for _, extract := range cfg.Extract {
		if extract.In != "body" && extract.In != "metadata" && extract.In != "trailers" {
			return nil, fmt.Errorf("gRPC extraction supports body, metadata and trailers")
		}
		if !hasStep(steps, extract.FlowID) {
			return nil, fmt.Errorf("extraction refers to unknown provider flow %q", extract.FlowID)
		}
	}
	cfg.Redactions = append(append([]artifact.Redaction(nil), seed.Redactions...), cfg.Redactions...)
	return &Plan{seed: seed, cfg: cfg, steps: steps, mutations: mutations, selected: selected, operations: operations, notes: notes, grpc: true}, nil
}
func grpcPayloadMessages(side *artifact.GrpcMessage, desc protoreflect.MessageDescriptor, contract *grpccontract.Contract) ([]proto.Message, error) {
	var out []proto.Message
	for _, payload := range side.Messages {
		if payload.Redacted || payload.DecodeState != "decoded" || payload.Body == nil || payload.Body.Truncated || payload.Wire == nil || payload.Wire.Truncated {
			return nil, fmt.Errorf("gRPC exploration requires complete unredacted decoded message payloads")
		}
		message := dynamicpb.NewMessage(desc)
		raw, err := base64.StdEncoding.DecodeString(payload.Wire.Bytes)
		if err != nil {
			return nil, err
		}
		if payload.Compressed {
			encoding := ""
			for name, values := range side.Metadata {
				if strings.EqualFold(name, "grpc-encoding") && len(values) == 1 {
					encoding = values[0]
				}
			}
			if encoding != "gzip" {
				return nil, fmt.Errorf("unsupported gRPC message compression")
			}
			reader, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, err
			}
			raw, err = io.ReadAll(io.LimitReader(reader, 16<<20+1))
			reader.Close()
			if err != nil || len(raw) > 16<<20 {
				return nil, fmt.Errorf("compressed gRPC message exceeds decode limit")
			}
		}
		if err = (proto.UnmarshalOptions{Resolver: dynamicpb.NewTypes(contract.Files), RecursionLimit: 100}).Unmarshal(raw, message); err != nil {
			return nil, fmt.Errorf("decode captured protobuf: %w", err)
		}
		if grpcUnknownFields(message.ProtoReflect(), 0) {
			return nil, fmt.Errorf("gRPC exploration requires a contract that describes every captured protobuf field")
		}

		if err := proto.CheckInitialized(message); err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, nil
}
func marshalGRPCHook(request GRPCRequest, messages []proto.Message, contract *grpccontract.Contract) (*GRPCHookRequest, error) {
	out := &GRPCHookRequest{Procedure: request.Procedure, Metadata: request.Metadata.Clone(), ResponseGates: append([]int(nil), request.ResponseGates...)}
	for i, message := range messages {
		raw, err := (protojson.MarshalOptions{UseProtoNames: true, Resolver: dynamicpb.NewTypes(contract.Files)}).Marshal(message)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, json.RawMessage(raw))
		out.MessagesBase64 = append(out.MessagesBase64, base64.StdEncoding.EncodeToString(request.Messages[i]))
	}
	return out, nil
}

func grpcUnknownFields(message protoreflect.Message, depth int) bool {
	if depth > 100 || len(message.GetUnknown()) > 0 {
		return true
	}
	unknown := false
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			if field.MapValue().Message() != nil {
				value.Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
					unknown = grpcUnknownFields(v.Message(), depth+1)
					return !unknown
				})
			}
		} else if field.IsList() {
			if field.Message() != nil {
				list := value.List()
				for i := 0; i < list.Len() && !unknown; i++ {
					unknown = grpcUnknownFields(list.Get(i).Message(), depth+1)
				}
			}
		} else if field.Message() != nil {
			unknown = grpcUnknownFields(value.Message(), depth+1)
		}
		return !unknown
	})
	return unknown
}
