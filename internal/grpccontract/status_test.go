package grpccontract

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
)

func statusTestContract(t *testing.T) *Contract {
	t.Helper()
	raw, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(raw, []string{"grpc.health.v1.Health"})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}
func statusTestEnvelope(t *testing.T, details ...*anypb.Any) string {
	t.Helper()
	raw, err := proto.Marshal(&statuspb.Status{Code: 3, Message: "invalid request", Details: details})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawStdEncoding.EncodeToString(raw)
}
func TestRichStatusLocalAndStandardTypes(t *testing.T) {
	boundary := statusTestContract(t)
	original := append([]byte{}, boundary.Bytes...)
	standard, _ := anypb.New(&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "name", Description: "must be provided"}}})
	local, _ := anypb.New(&healthpb.HealthCheckRequest{Service: "local detail"})
	unknown := &anypb.Any{TypeUrl: "https://never.fetch.invalid/example.Unknown", Value: []byte{8, 1}}
	msg := &artifact.GrpcMessage{StatusCode: 3, Metadata: artifact.Headers{}, Trailers: artifact.Headers{"grpc-status-details-bin": {statusTestEnvelope(t, standard, local, unknown)}}}
	CaptureStatusDetails(msg, boundary)
	s := msg.StatusDetails
	if s == nil || s.State != "decoded" || s.Code == nil || *s.Code != 3 || len(s.Details) != 3 {
		t.Fatalf("bad status: %+v", s)
	}
	if s.Details[0].DescriptorSource != "auxiliary" || s.Details[0].State != "decoded" || !strings.Contains(s.Details[0].Body.Text, "must be provided") {
		t.Fatalf("standard detail: %+v", s.Details[0])
	}
	if s.Details[1].DescriptorSource != "boundary" || s.Details[1].DescriptorSHA256 != boundary.SHA256 {
		t.Fatalf("local detail: %+v", s.Details[1])
	}
	if s.Details[2].State != "opaque" || s.Details[2].Wire == nil || s.Details[2].Body != nil {
		t.Fatalf("unknown detail: %+v", s.Details[2])
	}
	if len(statusValues(msg.Trailers)) != 0 || s.Wire == nil || s.AuxiliaryDescriptor == nil || s.AuxiliarySHA256 == "" {
		t.Fatal("missing source or duplicate envelope")
	}
	if string(original) != string(boundary.Bytes) {
		t.Fatal("service contract mutated")
	}
}

func TestRichStatusDiagnosticsAndLimits(t *testing.T) {
	large, _ := anypb.New(&healthpb.HealthCheckRequest{Service: strings.Repeat("x", 25000)})
	many := make([]*anypb.Any, 257)
	for i := range many {
		many[i] = &anypb.Any{TypeUrl: "x/unknown"}
	}
	cases := []struct {
		name              string
		headers, trailers []string
		code              int
		state             string
	}{
		{"absent", nil, nil, 3, ""},
		{"empty status", nil, []string{""}, 0, "decoded"},
		{"headers", []string{statusTestEnvelope(t)}, nil, 3, "decoded"},
		{"duplicate", nil, []string{statusTestEnvelope(t), statusTestEnvelope(t)}, 3, "malformed"},
		{"both locations", []string{statusTestEnvelope(t)}, []string{statusTestEnvelope(t)}, 3, "malformed"},
		{"invalid base64", nil, []string{"%%"}, 3, "malformed"},
		{"invalid protobuf", nil, []string{"/w=="}, 3, "malformed"},
		{"inconsistent", nil, []string{statusTestEnvelope(t)}, 7, "inconsistent"},
		{"oversized envelope", nil, []string{strings.Repeat("A", StatusRetentionBytes+1)}, 3, "truncated"},
		{"many details", nil, []string{statusTestEnvelope(t, many...)}, 3, "truncated"},
		{"bounded projections", nil, []string{statusTestEnvelope(t, large)}, 3, "decoded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &artifact.GrpcMessage{StatusCode: tc.code, Metadata: artifact.Headers{"Grpc-Status-Details-Bin": tc.headers}, Trailers: artifact.Headers{"Grpc-Status-Details-Bin": tc.trailers}}
			CaptureStatusDetails(msg, statusTestContract(t))
			if tc.state == "" {
				if msg.StatusDetails != nil {
					t.Fatal("absent details fabricated")
				}
				return
			}
			if msg.StatusDetails == nil || msg.StatusDetails.State != tc.state {
				t.Fatalf("%+v", msg.StatusDetails)
			}
			if tc.name == "bounded projections" && (msg.StatusDetails.Details[0].State != "truncated" || msg.StatusDetails.Details[0].Body != nil) {
				t.Fatal("projection escaped aggregate budget")
			}
		})
	}
	for _, encoded := range []string{"YWJjZA==", "YWJjZA"} {
		raw, err := DecodeMetadata(encoded)
		if err != nil || string(raw) != "abcd" {
			t.Fatalf("base64 %q: %s, %v", encoded, raw, err)
		}
	}
}

func TestRichStatusAuxiliaryDescriptorsRemainSeparateAndRejectConflicts(t *testing.T) {
	boundary := statusTestContract(t)
	auxiliary, err := StandardErrorContract(boundary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auxiliary.Files.FindDescriptorByName("grpc.health.v1.HealthCheckRequest"); err == nil {
		t.Fatal("helper graph copied the service contract")
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(auxiliary.Bytes, set); err != nil {
		t.Fatal(err)
	}
	for _, file := range set.File {
		if file.GetName() == "google/protobuf/any.proto" {
			file.MessageType[0].Field[0].Name = proto.String("different_type_url")
		}
	}
	raw, _ := proto.Marshal(set)
	conflicting, err := Parse(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StandardErrorContract(conflicting); err == nil {
		t.Fatal("accepted conflicting auxiliary descriptor")
	}
}

func TestRichStatusUnknownNestedAnyIsAnObservationLimit(t *testing.T) {
	nested, _ := anypb.New(&anypb.Any{TypeUrl: "type.invalid/unknown.Nested", Value: []byte{8, 1}})
	msg := &artifact.GrpcMessage{StatusCode: 3, Trailers: artifact.Headers{"grpc-status-details-bin": {statusTestEnvelope(t, nested)}}}
	CaptureStatusDetails(msg, statusTestContract(t))
	if msg.StatusDetails.Details[0].State != "opaque" || msg.StatusDetails.Details[0].Wire == nil || msg.StatusDetails.Details[0].Body != nil {
		t.Fatalf("unsupported projection confused with invalid protobuf: %+v", msg.StatusDetails.Details[0])
	}
}

func TestRichStatusMalformedMetadataSharesBudgetWithStatusMessage(t *testing.T) {
	large := strings.Repeat("x", 40<<10)
	for _, values := range [][]string{{"%%"}, {"", ""}} {
		msg := &artifact.GrpcMessage{StatusCode: 3, StatusMessage: large, Metadata: artifact.Headers{"Grpc-Message": {large}}, Trailers: artifact.Headers{"Grpc-Status-Details-Bin": values}}
		CaptureStatusDetails(msg, statusTestContract(t))
		if msg.StatusDetails == nil || msg.StatusDetails.State != "malformed" || statusMetadataSize(msg) > StatusRetentionBytes || msg.StatusMessage != "" || msg.Metadata["Grpc-Message"][0] != large {
			t.Fatal("malformed status retained an over-budget duplicate message or lost the original metadata")
		}
	}
}
