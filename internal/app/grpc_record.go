package app

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/proxy"
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
	request.Messages = parseGRPCFrames(ex.RequestFrames, ex.RequestTruncated, encoding, in, s.maxBodyBytes, resolver)
	response.Messages = parseGRPCFrames(ex.ResponseFrames, ex.ResponseTruncated, firstHeaderValue(response.Metadata, "Grpc-Encoding"), out, s.maxBodyBytes, resolver)
	if ex.ID == "" {
		ex.ID = s.nextFlowID()
	}
	flow := artifact.Flow{ID: ex.ID, Protocol: "grpc", Channel: ex.Channel, AssetID: ex.AssetID, TraceID: ex.TraceID, RequestSpanID: ex.RequestSpanID, TraceContextSource: ex.TraceContextSource, TestCorrelation: ex.TestCorrelation, StartedAt: ex.StartedAt, DurationMS: ex.Duration.Milliseconds(), TransportError: ex.Error, Request: artifact.Message{GRPC: request}, Response: artifact.Message{GRPC: response}}
	s.active.flows = append(s.active.flows, flow)
}

func parseGRPCFrames(raw []byte, truncated bool, encoding string, desc protoreflect.MessageDescriptor, maxBytes int64, resolvers ...*dynamicpb.Types) []artifact.GrpcPayload {
	var payloads []artifact.GrpcPayload
	if maxBytes <= 0 || maxBytes > 16<<20 {
		maxBytes = 16 << 20
	}
	remainingDecode := maxBytes
	for len(raw) > 0 {
		if len(payloads) >= 1023 {
			payloads = append(payloads, artifact.GrpcPayload{Index: len(payloads), DecodeError: "gRPC message retention limit exceeded"})
			break
		}
		item := artifact.GrpcPayload{Index: len(payloads)}
		if desc != nil {
			item.Type = string(desc.FullName())
		}
		if len(raw) < 5 {
			item.DecodeError = "incomplete gRPC frame header"
			payloads = append(payloads, item)
			break
		}
		flag := raw[0]
		size := uint64(binary.BigEndian.Uint32(raw[1:5]))
		raw = raw[5:]
		if flag > 1 {
			item.DecodeError = fmt.Sprintf("unsupported gRPC compression flag %d", flag)
			payloads = append(payloads, item)
			break
		}
		item.Compressed = flag == 1
		if size > uint64(len(raw)) {
			item.DecodeError = "incomplete gRPC message"
			if truncated {
				item.DecodeError = "capture truncated within gRPC message"
			}
			payloads = append(payloads, item)
			break
		}
		message := raw[:int(size)]
		raw = raw[int(size):]
		if size > uint64(maxBytes) {
			item.DecodeError = "gRPC message exceeds capture limit"
			payloads = append(payloads, item)
			continue
		}
		item.Wire = &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(message), OriginalBytes: int64(size)}
		if item.Compressed {
			if !strings.EqualFold(encoding, "gzip") {
				item.DecodeError = "unsupported gRPC message compression: " + encoding
				payloads = append(payloads, item)
				continue
			}
			reader, err := gzip.NewReader(bytes.NewReader(message))
			if err != nil {
				item.DecodeError = "invalid gzip message"
				payloads = append(payloads, item)
				continue
			}
			decoded, err := io.ReadAll(io.LimitReader(reader, remainingDecode+1))
			_ = reader.Close()
			if err != nil || int64(len(decoded)) > remainingDecode {
				item.DecodeError = "gzip message exceeds capture limit or is invalid"
				payloads = append(payloads, item)
				break
			}
			message = decoded
		}
		if desc == nil {
			item.DecodeError = "procedure not found in selected protobuf contract"
			payloads = append(payloads, item)
			continue
		}
		instance := dynamicpb.NewMessage(desc)
		unmarshal := proto.UnmarshalOptions{RecursionLimit: 100}
		if len(resolvers) > 0 && resolvers[0] != nil {
			unmarshal.Resolver = resolvers[0]
		}
		if err := unmarshal.Unmarshal(message, instance); err != nil {
			item.DecodeError = "protobuf decode failed: " + err.Error()
			payloads = append(payloads, item)
			continue
		}
		options := protojson.MarshalOptions{UseProtoNames: true}
		if len(resolvers) > 0 && resolvers[0] != nil {
			options.Resolver = resolvers[0]
		}
		jsonBytes, err := options.Marshal(instance)
		if err != nil {
			item.DecodeError = "protobuf JSON conversion failed: " + err.Error()
			payloads = append(payloads, item)
			continue
		}
		if int64(len(jsonBytes)) > remainingDecode {
			item.DecodeError = "decoded JSON exceeds capture limit"
			payloads = append(payloads, item)
			break
		}
		remainingDecode -= int64(len(jsonBytes))
		item.Body = &artifact.Body{Encoding: "json", Text: string(jsonBytes), OriginalBytes: int64(len(message))}
		payloads = append(payloads, item)
	}
	return payloads
}
