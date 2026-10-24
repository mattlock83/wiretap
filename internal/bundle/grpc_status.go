package bundle

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func statusBodyKey(flow, kind string, index *int) string {
	key := flow + "/response/" + kind
	if index != nil {
		key += fmt.Sprintf("/%d", *index)
	}
	return key
}

type statusBody struct {
	kind  string
	index *int
	body  *artifact.Body
}

func statusBodies(status *artifact.GrpcStatusDetails) []statusBody {
	if status == nil {
		return nil
	}
	out := []statusBody{{kind: "status-envelope", body: status.Wire}, {kind: "status-descriptors", body: status.AuxiliaryDescriptor}}
	for i := range status.Details {
		index := status.Details[i].Index
		out = append(out, statusBody{kind: "status-detail-wire", index: &index, body: status.Details[i].Wire}, statusBody{kind: "status-detail-json", index: &index, body: status.Details[i].Body})
	}
	return out
}

func (w *Writer) attachGRPCStatus(attempt *Attempt) error {
	for i := range attempt.Capture.Traffic {
		flow := &attempt.Capture.Traffic[i]
		if flow.Response.GRPC == nil {
			continue
		}
		for _, item := range statusBodies(flow.Response.GRPC.StatusDetails) {
			ref, err := w.attachBody(flow.ID, "response", item.body)
			if err != nil {
				return err
			}
			if ref != nil {
				ref.Kind, ref.StatusDetailIndex = item.kind, item.index
				attempt.Bodies = append(attempt.Bodies, *ref)
			}
		}
	}
	return nil
}

func restoreGRPCStatusBody(root string, flows map[string]*artifact.Flow, seen map[string]bool, ref BodyRef, readFile func(string) ([]byte, error)) error {
	key := statusBodyKey(ref.FlowID, ref.Kind, ref.StatusDetailIndex)
	if seen[key] || ref.Phase != "response" || ref.MessageIndex != nil || ref.EventID != "" || ref.PayloadName != "" {
		return fmt.Errorf("invalid or duplicate gRPC status attachment %q", key)
	}
	flow := flows[ref.FlowID]
	if flow == nil || flow.Response.GRPC == nil || flow.Response.GRPC.StatusDetails == nil {
		return fmt.Errorf("status attachment references missing response %q", ref.FlowID)
	}
	if len(flow.Response.GRPC.StatusDetails.Details) > grpccontract.StatusDetailLimit {
		return fmt.Errorf("status detail inventory exceeds retention limit")
	}
	var body *artifact.Body
	for _, item := range statusBodies(flow.Response.GRPC.StatusDetails) {
		if statusBodyKey(ref.FlowID, item.kind, item.index) == key {
			body = item.body
			break
		}
	}
	if body == nil || body.Bytes != "" || body.Text != "" {
		return fmt.Errorf("status attachment %q has no body or retains inline bytes", key)
	}
	limit := grpccontract.StatusRetentionBytes
	if ref.Kind == "status-descriptors" {
		limit = grpccontract.MaxDescriptorBytes
	}
	if ref.RetainedBytes < 0 || ref.RetainedBytes > limit || ref.Path != "payloads/"+ref.SHA256+".bin" {
		return fmt.Errorf("invalid status attachment %q", key)
	}
	path, err := containedPath(root, ref.Path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > int64(limit) {
		return fmt.Errorf("status attachment %q exceeds retention limit", key)
	}
	raw, err := readRef(root, ref.FileRef, readFile)
	if err != nil {
		return err
	}
	if len(raw) != ref.RetainedBytes {
		return fmt.Errorf("status attachment %q size mismatch", key)
	}
	if ref.Kind == "status-detail-json" {
		if ref.Representation != "text" || body.Encoding != "json" {
			return fmt.Errorf("invalid status projection encoding")
		}
		body.Text = string(raw)
	} else {
		if ref.Representation != "bytes" || body.Encoding != "base64" {
			return fmt.Errorf("invalid status wire encoding")
		}
		body.Bytes = base64.StdEncoding.EncodeToString(raw)
	}
	seen[key] = true
	return nil
}

func verifyGRPCStatus(capture artifact.Artifact, seen map[string]bool) error {
	return verifyGRPCStatusUsing(capture, seen, os.ReadFile)
}

func verifyGRPCStatusUsing(capture artifact.Artifact, seen map[string]bool, readContract func(string) ([]byte, error)) error {
	for _, flow := range capture.Traffic {
		if flow.Request.GRPC != nil && flow.Request.GRPC.StatusDetails != nil {
			return fmt.Errorf("gRPC request cannot contain response status details")
		}
		if flow.Response.GRPC == nil || flow.Response.GRPC.StatusDetails == nil {
			continue
		}
		status := flow.Response.GRPC.StatusDetails
		if status.Source != "headers" && status.Source != "trailers" {
			return fmt.Errorf("invalid gRPC status detail source")
		}
		switch status.State {
		case "decoded", "malformed", "inconsistent", "truncated", "redacted":
		default:
			return fmt.Errorf("invalid gRPC status detail state %q", status.State)
		}
		if len(status.Details) > grpccontract.StatusDetailLimit || status.Details == nil {
			return fmt.Errorf("invalid gRPC status detail inventory")
		}
		for _, item := range statusBodies(status) {
			if item.body != nil && !seen[statusBodyKey(flow.ID, item.kind, item.index)] {
				return fmt.Errorf("gRPC status body lacks verified attachment")
			}
		}
		if status.State == "redacted" && status.Wire != nil {
			return fmt.Errorf("redacted status retains envelope bytes")
		}
		var envelope *statuspb.Status
		if status.Wire != nil {
			raw, err := base64.StdEncoding.DecodeString(status.Wire.Bytes)
			if err != nil || len(raw) > grpccontract.StatusRetentionBytes {
				return fmt.Errorf("invalid status envelope bytes")
			}
			decoded := &statuspb.Status{}
			if err := (proto.UnmarshalOptions{RecursionLimit: 100}).Unmarshal(raw, decoded); err != nil {
				limited := status.State == "truncated" && strings.Contains(err.Error(), "exceeded maximum recursion depth")
				if status.State != "malformed" && !limited || status.Code != nil || len(status.Details) > 0 {
					return fmt.Errorf("invalid status envelope claims decoded details")
				}
			} else {
				envelope = decoded
				if status.Code == nil || *status.Code != int(decoded.Code) {
					return fmt.Errorf("status envelope code mismatch")
				}
				if status.Message != decoded.Message && status.State != "truncated" {
					return fmt.Errorf("status envelope message mismatch")
				}
				if status.State == "decoded" && *status.Code != flow.Response.GRPC.StatusCode {
					return fmt.Errorf("status envelope disagrees with terminal status")
				}
				if status.State != "truncated" && len(status.Details) != len(decoded.Details) {
					return fmt.Errorf("status envelope detail count mismatch")
				}
			}
		} else if status.State == "decoded" || status.State == "inconsistent" {
			return fmt.Errorf("decoded status lacks raw envelope")
		}
		if status.Wire != nil {
			for _, headers := range []artifact.Headers{flow.Response.GRPC.Metadata, flow.Response.GRPC.Trailers} {
				for key := range headers {
					if strings.EqualFold(key, "grpc-status-details-bin") {
						return fmt.Errorf("duplicate raw status envelope in metadata")
					}
				}
			}
		}
		ref := grpcSchemaFor(flow, capture.Schemas)
		if ref == nil {
			return fmt.Errorf("status details have no boundary contract")
		}
		rawContract, err := readContract(ref.Source.Path)
		if err != nil {
			return err
		}
		boundary, err := grpccontract.Parse(rawContract, ref.Services)
		if err != nil {
			return err
		}
		var auxiliary *grpccontract.Contract
		if status.AuxiliaryDescriptor != nil {
			raw, err := base64.StdEncoding.DecodeString(status.AuxiliaryDescriptor.Bytes)
			if err != nil || digest(raw) != status.AuxiliarySHA256 {
				return fmt.Errorf("status auxiliary descriptor hash mismatch")
			}
			auxiliary, err = grpccontract.Parse(raw, nil)
			if err != nil {
				return fmt.Errorf("status auxiliary descriptors: %w", err)
			}
			// Conflict checking is offline: pinned helper definitions are not used
			// to reinterpret persisted evidence, only retained descriptors are.
			var conflict error
			auxiliary.Files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
				if existing, e := boundary.Files.FindFileByPath(file.Path()); e == nil {
					left, right := protodesc.ToFileDescriptorProto(existing), protodesc.ToFileDescriptorProto(file)
					left.SourceCodeInfo, right.SourceCodeInfo = nil, nil
					if !proto.Equal(left, right) {
						conflict = fmt.Errorf("conflicting auxiliary status descriptor %q", file.Path())
						return false
					}
				}
				return true
			})
			if conflict != nil {
				return conflict
			}
		} else if status.AuxiliarySHA256 != "" {
			return fmt.Errorf("status auxiliary descriptor missing")
		}
		retained := len(status.Message) + len(flow.Response.GRPC.StatusMessage)
		for _, headers := range []artifact.Headers{flow.Response.GRPC.Metadata, flow.Response.GRPC.Trailers} {
			for key, values := range headers {
				for _, value := range values {
					retained += len(key) + len(value)
				}
			}
		}
		for _, item := range statusBodies(status) {
			if item.body == nil || item.kind == "status-descriptors" {
				continue
			}
			if item.kind == "status-detail-json" {
				retained += len(item.body.Text)
			} else {
				raw, _ := base64.StdEncoding.DecodeString(item.body.Bytes)
				retained += len(raw)
			}
		}
		for index, detail := range status.Details {
			if detail.Index != index {
				return fmt.Errorf("status detail indices must be contiguous")
			}
			retained += len(detail.TypeURL) + len(detail.Type)
			if detail.State == "redacted" && (detail.Wire != nil || status.Wire != nil) {
				return fmt.Errorf("redacted status detail retains containing wire bytes")
			}
			if envelope != nil {
				if index >= len(envelope.Details) || detail.TypeURL != envelope.Details[index].GetTypeUrl() {
					return fmt.Errorf("status detail identity mismatch")
				}
				if detail.Wire != nil {
					raw, err := base64.StdEncoding.DecodeString(detail.Wire.Bytes)
					if err != nil || !bytes.Equal(raw, envelope.Details[index].GetValue()) {
						return fmt.Errorf("status detail wire differs from envelope")
					}
				}
			}
			switch detail.State {
			case "decoded", "opaque", "malformed", "truncated", "redacted":
			default:
				return fmt.Errorf("invalid status detail state")
			}
			if detail.State == "redacted" {
				continue
			}
			if detail.Body != nil && detail.State != "decoded" {
				return fmt.Errorf("undecoded status detail has projection")
			}
			if detail.State != "decoded" {
				continue
			}
			if detail.Body == nil || detail.Wire == nil {
				return fmt.Errorf("decoded status detail lacks verified source")
			}
			contract := boundary
			switch detail.DescriptorSource {
			case "boundary":
			case "auxiliary":
				contract = auxiliary
			default:
				return fmt.Errorf("invalid status detail descriptor source")
			}
			if contract == nil || detail.DescriptorSHA256 != contract.SHA256 {
				return fmt.Errorf("status detail descriptor identity mismatch")
			}
			messageType, err := dynamicpb.NewTypes(contract.Files).FindMessageByURL(detail.TypeURL)
			if err != nil || string(messageType.Descriptor().FullName()) != detail.Type {
				return fmt.Errorf("status detail type mismatch")
			}
			raw, err := base64.StdEncoding.DecodeString(detail.Wire.Bytes)
			if err != nil {
				return err
			}
			projection, err := grpccontract.DecodeStatusDetail(raw, messageType, contract)
			if err != nil || !equivalentJSON(projection, []byte(detail.Body.Text)) {
				return fmt.Errorf("status detail projection disagrees with wire payload")
			}
		}
		if retained > grpccontract.StatusRetentionBytes {
			return fmt.Errorf("status detail evidence exceeds retention limit")
		}
	}
	return nil
}
