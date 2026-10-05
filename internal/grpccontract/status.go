package grpccontract

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const StatusRetentionBytes = 64 << 10
const StatusDetailLimit = 256

var errStatusProjectionUnavailable = errors.New("status projection unavailable")

// CaptureStatusDetails moves a retained binary metadata envelope into typed
// evidence. It only touches the observer's copies, never upstream/client bytes.
// Raw envelope, detail bytes and projections share the response metadata budget.
func CaptureStatusDetails(msg *artifact.GrpcMessage, contract *Contract) {
	if msg == nil {
		return
	}
	initial, terminal := statusValues(msg.Metadata), statusValues(msg.Trailers)
	if len(initial)+len(terminal) == 0 {
		return
	}
	// The terminal message convenience field duplicates metadata. Prefer the
	// original bounded metadata when that extra scalar would exhaust retention.
	if statusMetadataSize(msg) > StatusRetentionBytes {
		msg.StatusMessage = ""
	}
	result := &artifact.GrpcStatusDetails{Source: "trailers", State: "malformed", Details: []artifact.GrpcStatusDetail{}}
	msg.StatusDetails = result
	values := terminal
	if len(terminal) == 0 {
		values, result.Source = initial, "headers"
	}
	if len(values) != 1 || len(initial) > 0 && len(terminal) > 0 {
		result.Diagnostic = "expected exactly one grpc-status-details-bin value in headers or trailers"
		return
	}
	encoded := values[0]
	if len(encoded) > StatusRetentionBytes {
		result.State, result.Diagnostic = "truncated", "status envelope exceeds retention budget"
		removeStatusValues(msg)
		return
	}
	raw, err := DecodeMetadata(encoded)
	if err != nil {
		result.Diagnostic = "invalid binary-metadata base64"
		return
	}
	removeStatusValues(msg)
	remaining := StatusRetentionBytes - statusMetadataSize(msg) - len(raw)
	if remaining < 0 {
		result.State, result.Diagnostic = "truncated", "status envelope exceeds remaining metadata budget"
		return
	}
	result.Wire = statusWire(raw)
	envelope := &statuspb.Status{}
	if err := (proto.UnmarshalOptions{RecursionLimit: 100}).Unmarshal(raw, envelope); err != nil {
		result.Diagnostic = "invalid google.rpc.Status protobuf"
		if strings.Contains(err.Error(), "exceeded maximum recursion depth") {
			result.State, result.Diagnostic = "truncated", "status envelope decoding exceeds recursion limit"
		}
		return
	}
	code := int(envelope.Code)
	result.Code = &code
	// Scalar copies also consume the retention budget. Large messages remain in
	// the raw envelope, with an explicit unavailable projection.
	if len(envelope.Message) > remaining {
		result.State, result.Diagnostic = "truncated", "status message projection exceeds retention budget"
		return
	}
	result.Message = envelope.Message
	remaining -= len(result.Message)
	result.State = "decoded"
	if code != msg.StatusCode || msg.StatusCode < 0 {
		result.State, result.Diagnostic = "inconsistent", "status envelope code disagrees with terminal grpc-status"
	}
	var auxiliary *Contract
	var auxiliaryErr error
	auxiliaryTried := false
	for index, detail := range envelope.Details {
		if index >= StatusDetailLimit {
			result.State, result.Diagnostic = "truncated", "status detail count exceeds 256"
			break
		}
		item := artifact.GrpcStatusDetail{Index: index, TypeURL: detail.GetTypeUrl(), State: "opaque"}
		if len(item.TypeURL) > remaining {
			result.State, result.Diagnostic = "truncated", "status detail identity exceeds retention budget"
			break
		}
		remaining -= len(item.TypeURL)
		if len(detail.GetValue()) <= remaining {
			item.Wire = statusWire(detail.GetValue())
			remaining -= len(detail.GetValue())
		} else {
			item.State, item.Diagnostic = "truncated", "detail bytes exceed retention budget; retained in envelope only"
			result.Details = append(result.Details, item)
			continue
		}
		resolver := contract
		messageType, err := findStatusType(resolver, item.TypeURL)
		if err == nil {
			item.DescriptorSource, item.DescriptorSHA256 = "boundary", contract.SHA256
		} else {
			if !auxiliaryTried {
				auxiliary, auxiliaryErr = StandardErrorContract(contract)
				auxiliaryTried = true
			}
			err = auxiliaryErr
			if err == nil {
				messageType, err = findStatusType(auxiliary, item.TypeURL)
			}
			if err == nil {
				resolver = auxiliary
				item.DescriptorSource, item.DescriptorSHA256 = "auxiliary", auxiliary.SHA256
				result.AuxiliaryDescriptor, result.AuxiliarySHA256 = statusWire(auxiliary.Bytes), auxiliary.SHA256
			}
		}
		if err != nil {
			item.Diagnostic = "type is unavailable in frozen boundary or standard error descriptors"
			result.Details = append(result.Details, item)
			continue
		}
		typeName := string(messageType.Descriptor().FullName())
		if len(typeName) > remaining {
			item.State, item.Diagnostic = "truncated", "detail type identity exceeds retention budget"
			result.Details = append(result.Details, item)
			continue
		}
		item.Type = typeName
		remaining -= len(item.Type)
		projection, err := DecodeStatusDetail(detail.GetValue(), messageType, resolver)
		if err != nil {
			item.State, item.Diagnostic = "malformed", "detail cannot be decoded with frozen descriptors"
			if errors.Is(err, errStatusProjectionUnavailable) {
				item.State, item.Diagnostic = "opaque", "readable projection unavailable with frozen descriptors"
			}
		} else if len(projection) > remaining {
			item.State, item.Diagnostic = "truncated", "detail projection exceeds retention budget"
		} else {
			remaining -= len(projection)
			item.State = "decoded"
			item.Body = &artifact.Body{Encoding: "json", Text: string(projection), OriginalBytes: int64(len(detail.GetValue()))}
		}
		result.Details = append(result.Details, item)
	}
}

// DecodeMetadata accepts the padded and unpadded forms allowed by gRPC.
func DecodeMetadata(encoded string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return raw, nil
	}
	return base64.RawStdEncoding.DecodeString(encoded)
}

func statusValues(headers artifact.Headers) []string {
	var values []string
	for key, items := range headers {
		if strings.EqualFold(key, "grpc-status-details-bin") {
			values = append(values, items...)
		}
	}
	return values
}
func removeStatusValues(msg *artifact.GrpcMessage) {
	for _, headers := range []artifact.Headers{msg.Metadata, msg.Trailers} {
		for key := range headers {
			if strings.EqualFold(key, "grpc-status-details-bin") {
				delete(headers, key)
			}
		}
	}
}
func statusMetadataSize(msg *artifact.GrpcMessage) int {
	bytes := len(msg.StatusMessage)
	for _, headers := range []artifact.Headers{msg.Metadata, msg.Trailers} {
		for key, values := range headers {
			for _, value := range values {
				bytes += len(key) + len(value)
			}
		}
	}
	return bytes
}
func statusWire(raw []byte) *artifact.Body {
	return &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(raw), OriginalBytes: int64(len(raw))}
}
func findStatusType(contract *Contract, typeURL string) (protoreflect.MessageType, error) {
	if contract == nil {
		return nil, protoregistry.NotFound
	}
	return dynamicpb.NewTypes(contract.Files).FindMessageByURL(typeURL)
}

func DecodeStatusDetail(raw []byte, messageType protoreflect.MessageType, contract *Contract) ([]byte, error) {
	message := messageType.New().Interface()
	resolver := dynamicpb.NewTypes(contract.Files)
	if err := (proto.UnmarshalOptions{RecursionLimit: 100, Resolver: resolver}).Unmarshal(raw, message); err != nil {
		if strings.Contains(err.Error(), "exceeded maximum recursion depth") {
			return nil, fmt.Errorf("%w: recursion limit exceeded", errStatusProjectionUnavailable)
		}
		return nil, err
	}
	projection, err := (protojson.MarshalOptions{UseProtoNames: true, Resolver: resolver}).Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errStatusProjectionUnavailable, err)
	}
	return projection, nil
}

// StandardErrorContract returns a separately identified helper graph, containing
// pinned google.rpc error types. Existing boundary types are resolved first;
// conflicting helper definitions are refused.
// The service contract itself is never extended or reidentified.
func StandardErrorContract(boundary *Contract) (*Contract, error) {
	files := map[string]*descriptorpb.FileDescriptorProto{}
	var add func(protoreflect.FileDescriptor) error
	add = func(file protoreflect.FileDescriptor) error {
		incoming := protodesc.ToFileDescriptorProto(file)
		if boundary != nil {
			if existing, err := boundary.Files.FindFileByPath(file.Path()); err == nil {
				left, right := protodesc.ToFileDescriptorProto(existing), proto.Clone(incoming).(*descriptorpb.FileDescriptorProto)
				left.SourceCodeInfo, right.SourceCodeInfo = nil, nil
				if !proto.Equal(left, right) {
					return fmt.Errorf("conflicting auxiliary descriptor %q", file.Path())
				}
			}
		}
		if existing := files[file.Path()]; existing != nil {
			left, right := proto.Clone(existing).(*descriptorpb.FileDescriptorProto), proto.Clone(incoming).(*descriptorpb.FileDescriptorProto)
			left.SourceCodeInfo, right.SourceCodeInfo = nil, nil
			if !proto.Equal(left, right) {
				return fmt.Errorf("conflicting auxiliary descriptor %q", file.Path())
			}
			return nil
		}
		for i := 0; i < file.Imports().Len(); i++ {
			if err := add(file.Imports().Get(i).FileDescriptor); err != nil {
				return err
			}
		}
		files[file.Path()] = incoming
		return nil
	}
	for _, file := range []protoreflect.FileDescriptor{statuspb.File_google_rpc_status_proto, errdetails.File_google_rpc_error_details_proto} {
		if err := add(file); err != nil {
			return nil, err
		}
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, file := range files {
		set.File = append(set.File, file)
	}
	sort.Slice(set.File, func(i, j int) bool { return set.File[i].GetName() < set.File[j].GetName() })
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(set)
	if err != nil {
		return nil, err
	}
	contract, err := Parse(raw, nil)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	contract.SHA256 = hex.EncodeToString(sum[:])
	return contract, nil
}
