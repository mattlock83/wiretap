package app

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/capture"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"github.com/mattlock83/wiretap/internal/proxy"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func (s *TestCaseStore) RecordGRPC(ex proxy.GRPCExchange) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || (ex.CaseID != "" && ex.CaseID != s.active.id) {
		return
	}
	key := "sut"
	if ex.Channel == "sut_to_dependency" {
		key = "dep:" + ex.AssetID
	}
	contract := s.grpcContracts[key]
	var method protoreflect.MethodDescriptor
	if contract != nil {
		method = contract.Methods[ex.Procedure]
	}
	service, methodName := "", ""
	parts := strings.Split(strings.TrimPrefix(ex.Procedure, "/"), "/")
	if len(parts) == 2 {
		service, methodName = parts[0], parts[1]
	}
	schemaID := ""
	for _, schema := range s.active.schemas {
		if schema.Type == "protobuf" && ((ex.Channel == "test_to_sut" && schema.Role == "sut") || (ex.Channel == "sut_to_dependency" && schema.Role == "dependency" && schema.AssetID == ex.AssetID)) {
			schemaID = schema.ID
			break
		}
	}
	request := &artifact.GrpcMessage{Kind: "grpc", Procedure: ex.Procedure, Service: service, Method: methodName, SchemaRef: schemaID, Metadata: cloneHeaders(ex.RequestHeaders), Trailers: cloneHeaders(ex.RequestTrailers), CaptureTruncated: ex.RequestTruncated, ObservedBytes: ex.RequestBytes}
	response := &artifact.GrpcMessage{Kind: "grpc", Procedure: ex.Procedure, Service: service, Method: methodName, SchemaRef: schemaID, Metadata: cloneHeaders(ex.ResponseHeaders), Trailers: cloneHeaders(ex.ResponseTrailers), HTTPStatus: ex.HTTPStatus, StatusCode: ex.StatusCode, StatusMessage: ex.StatusMessage, CaptureTruncated: ex.ResponseTruncated, ObservedBytes: ex.ResponseBytes, StatusSource: ex.StatusSource}
	encoding := firstHeaderValue(request.Metadata, "Grpc-Encoding")
	var in, out protoreflect.MessageDescriptor
	var resolver *dynamicpb.Types
	if contract != nil {
		resolver = dynamicpb.NewTypes(contract.Files)
	}
	if method != nil {
		in, out = method.Input(), method.Output()
		kind := "unary"
		if method.IsStreamingClient() && method.IsStreamingServer() {
			kind = "bidirectional"
		} else if method.IsStreamingClient() {
			kind = "client_streaming"
		} else if method.IsStreamingServer() {
			kind = "server_streaming"
		}
		request.StreamKind, response.StreamKind = kind, kind
	}
	request.Observation, request.Messages = observedGRPCMessages(ex.RequestObservation, encoding, in, s.maxBodyBytes, resolver)
	response.Observation, response.Messages = observedGRPCMessages(ex.ResponseObservation, firstHeaderValue(response.Metadata, "Grpc-Encoding"), out, s.maxBodyBytes, resolver)
	grpccontract.CaptureStatusDetails(response, contract)
	if ex.ID == "" {
		ex.ID = s.nextFlowID()
	}
	flow := artifact.Flow{ID: ex.ID, Protocol: "grpc", Channel: ex.Channel, AssetID: ex.AssetID, TraceID: ex.TraceID, RequestSpanID: ex.RequestSpanID, TraceContextSource: ex.TraceContextSource, TestCorrelation: ex.TestCorrelation, StartedAt: ex.StartedAt, DurationMS: ex.Duration.Milliseconds(), TransportError: ex.Error, Request: artifact.Message{GRPC: request}, Response: artifact.Message{GRPC: response}}
	for _, event := range ex.Events {
		flow.GRPCEvents = append(flow.GRPCEvents, artifact.GrpcEvent{Sequence: event.Sequence, OffsetNS: event.OffsetNS, Direction: event.Direction, Kind: event.Kind})
	}
	s.active.flows = append(s.active.flows, flow)
}

func decodeGRPCPayload(message []byte, compressed bool, encoding string, desc protoreflect.MessageDescriptor, remaining *int64, resolver *dynamicpb.Types) artifact.GrpcPayload {
	item := artifact.GrpcPayload{Compressed: compressed, Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(message), OriginalBytes: int64(len(message))}}
	if desc != nil {
		item.Type = string(desc.FullName())
	}
	fail := func(reason string) artifact.GrpcPayload {
		item.DecodeError = reason
		item.DecodeState = "unavailable"
		return item
	}
	invalid := func(reason string) artifact.GrpcPayload {
		item.DecodeError = reason
		item.DecodeState = "invalid"
		return item
	}
	if *remaining <= 0 {
		return fail("decoded message budget exhausted")
	}
	if compressed {
		if encoding != "gzip" {
			return fail("unsupported gRPC message compression: " + encoding)
		}
		reader, err := gzip.NewReader(bytes.NewReader(message))
		if err != nil {
			return invalid("invalid gzip message")
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, *remaining+1))
		_ = reader.Close()
		if int64(len(decoded)) > *remaining {
			*remaining = 0
			return fail("gzip message exceeds capture limit")
		}
		if err != nil {
			// A checksum or truncated-stream error can arrive after expansion.
			// Failed decoding still consumes the direction's work budget.
			*remaining -= int64(len(decoded))
			return invalid("invalid gzip message")
		}
		message = decoded
	}
	// Bound work as well as retained JSON: unknown Protobuf fields can expand
	// substantially under gzip while their JSON projection remains just {}.
	if int64(len(message)) > *remaining {
		*remaining = 0
		return fail("protobuf decode bytes exceed capture limit")
	}
	*remaining -= int64(len(message))
	if desc == nil {
		return fail("procedure not found in selected protobuf contract")
	}
	instance := dynamicpb.NewMessage(desc)
	unmarshal := proto.UnmarshalOptions{RecursionLimit: 100}
	if resolver != nil {
		unmarshal.Resolver = resolver
	}
	if err := unmarshal.Unmarshal(message, instance); err != nil {
		// protobuf-go exposes no sentinel for this resource limit. Its pinned
		// decoder reports this text; depth exhaustion is not malformed traffic.
		if strings.Contains(err.Error(), "exceeded maximum recursion depth") {
			return fail("protobuf recursion limit exceeded")
		}
		return invalid("protobuf decode failed: " + err.Error())
	}
	options := protojson.MarshalOptions{UseProtoNames: true}
	if resolver != nil {
		options.Resolver = resolver
	}
	jsonBytes, err := options.Marshal(instance)
	if err != nil {
		return fail("protobuf JSON conversion failed: " + err.Error())
	}
	if int64(len(jsonBytes)) > *remaining {
		*remaining = 0
		return fail("decoded JSON exceeds capture limit")
	}
	*remaining -= int64(len(jsonBytes))
	item.Body = &artifact.Body{Encoding: "json", Text: string(jsonBytes), OriginalBytes: int64(len(message))}
	item.DecodeState = "decoded"
	return item
}

func observedGRPCMessages(observed *capture.GRPCObservation, encoding string, desc protoreflect.MessageDescriptor, limit int64, resolver *dynamicpb.Types) (*artifact.GrpcObservation, []artifact.GrpcPayload) {
	if limit <= 0 || limit > 16<<20 {
		limit = 16 << 20
	}
	summary := &artifact.GrpcObservation{CompleteFrames: observed.CompleteFrames, RetainedFrames: observed.RetainedFrames, OmittedFrames: observed.OmittedFrames, ObservedBytes: observed.ObservedBytes, CountsComplete: observed.CountsComplete, EOF: observed.EOF, End: observed.End, Error: observed.Error}
	if o := observed.Omission; o != nil {
		summary.Omitted = &artifact.GrpcOmission{FirstIndex: o.FirstIndex, Count: o.Count}
	}
	if p := observed.Partial; p != nil {
		summary.Partial = &artifact.GrpcPartialFrame{Index: p.Index, HeaderBytes: len(p.Header), WireBytes: uint64(p.WireBytes), ObservedPayloadBytes: p.PayloadBytes, StartedOffsetNS: p.StartedOffsetNS, StartSequence: p.StartSequence}
	}
	messages := make([]artifact.GrpcPayload, 0, len(observed.Frames))
	for _, frame := range observed.Frames {
		payload := decodeGRPCPayload(frame.Payload, frame.CompressionFlag == 1, encoding, desc, &limit, resolver)
		payload.Index = int(frame.Index)
		payload.Observation = &artifact.GrpcPayloadObservation{WireBytes: uint64(frame.WireBytes), StartedOffsetNS: frame.StartedOffsetNS, CompletedOffsetNS: frame.CompletedOffsetNS, StartSequence: frame.StartSequence, CompleteSequence: frame.CompleteSequence}
		messages = append(messages, payload)
	}
	return summary, messages
}
