package coverage

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
)

func grpcStreamConditionFixture(t *testing.T) (*artifact.Artifact, func() *ContractCoverage) {
	t.Helper()
	source := strings.Replace(grpcConditionsProto, "rpc Call(Payload) returns (Payload)", "rpc Call(stream Payload) returns (stream Payload)", 1)
	run, a := grpcConditionFixture(t, source, `{"enabled":true}`)
	var seq uint64
	for _, side := range []*artifact.GrpcMessage{a.Traffic[0].Request.GRPC, a.Traffic[0].Response.GRPC} {
		side.StreamKind = "bidirectional"
		raw, _ := base64.StdEncoding.DecodeString(side.Messages[0].Wire.Bytes)
		side.Messages[0].DecodeState = "decoded"
		side.Messages[0].Observation = &artifact.GrpcPayloadObservation{WireBytes: uint64(len(raw)), StartSequence: seq, CompleteSequence: seq + 1, StartedOffsetNS: int64(seq), CompletedOffsetNS: int64(seq + 1)}
		side.ObservedBytes = int64(5 + len(raw))
		side.Observation = &artifact.GrpcObservation{CompleteFrames: 1, RetainedFrames: 1, ObservedBytes: side.ObservedBytes, CountsComplete: true, EOF: true, End: "eof"}
		seq += 2
	}
	return a, func() *ContractCoverage { return buildContractCoverage(run, []*artifact.Artifact{a}) }
}

func TestGRPCStreamConditionsUseExactMessages(t *testing.T) {
	a, measure := grpcStreamConditionFixture(t)
	res := a.Traffic[0].Response.GRPC
	p := res.Messages[0]
	p.Index = 1
	p.Wire = &artifact.Body{Encoding: "base64"}
	p.Body = &artifact.Body{Encoding: "json", Text: `{}`}
	p.Observation = &artifact.GrpcPayloadObservation{WireBytes: 0, StartSequence: 4, CompleteSequence: 5, StartedOffsetNS: 4, CompletedOffsetNS: 5}
	res.Messages = append(res.Messages, p)
	res.Observation.CompleteFrames, res.Observation.RetainedFrames = 2, 2
	res.ObservedBytes += 5
	res.Observation.ObservedBytes = res.ObservedBytes
	spec := measure().Specs[0]
	for _, value := range []string{"true", "false"} {
		if target := grpcTarget(t, spec, "response", "/fields/1", "boolean", value); target.State != "covered" {
			t.Fatalf("%s not covered: %+v", value, target)
		}
	}
	seen := map[int]bool{}
	for _, combination := range spec.Combinations {
		if combination.Phase == "response" && combination.Evidence.MessageIndex != nil {
			seen[*combination.Evidence.MessageIndex] = true
		}
	}
	if !seen[0] || !seen[1] {
		t.Fatal("stream observations flattened into one message")
	}
}

func TestGRPCStreamEmptyAndIncompleteEvidence(t *testing.T) {
	t.Run("empty stream exercises only method", func(t *testing.T) {
		a, measure := grpcStreamConditionFixture(t)
		for _, side := range []*artifact.GrpcMessage{a.Traffic[0].Request.GRPC, a.Traffic[0].Response.GRPC} {
			side.Messages = nil
			side.ObservedBytes = 0
			side.Observation = &artifact.GrpcObservation{CountsComplete: true, EOF: true, End: "eof"}
		}
		spec := measure().Specs[0]
		for _, target := range spec.Targets {
			if target.Operation != "/fixture.Example/Call" {
				continue
			}
			if target.Family == "operation" {
				if target.State != "covered" {
					t.Fatalf("empty stream method: %+v", target)
				}
			} else if target.State == "covered" {
				t.Fatal("empty stream covered a payload")
			}
		}
	})
	for _, change := range []string{"missing observations", "omissions", "partial frame", "open request", "decode unavailable"} {
		t.Run(change, func(t *testing.T) {
			a, measure := grpcStreamConditionFixture(t)
			side := a.Traffic[0].Request.GRPC
			switch change {
			case "missing observations":
				side.Observation = nil
			case "omissions":
				side.Observation.OmittedFrames = 1
			case "partial frame":
				side.Observation.Partial = &artifact.GrpcPartialFrame{Index: 1, HeaderBytes: 2}
			case "open request":
				side.Observation.EOF = false
				side.Observation.End = "open"
			case "decode unavailable":
				side.Messages[0].DecodeState = "unavailable"
				side.Messages[0].DecodeError = "unknown encoding"
			}
			for _, target := range measure().Specs[0].Targets {
				if target.Operation == "/fixture.Example/Call" && target.State == "covered" {
					t.Fatalf("incomplete stream earned coverage: %+v", target)
				}
			}
		})
	}
}

func TestGRPCNonOKCannotHideExtraUnaryResponse(t *testing.T) {
	run, a := grpcConditionFixture(t, grpcConditionsProto, `{}`)
	response := a.Traffic[0].Response.GRPC
	response.StatusCode = 3
	response.Messages = append(response.Messages, response.Messages[0])
	response.Messages[1].Index = 1
	for _, target := range buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0].Targets {
		if target.Operation == "/fixture.Example/Call" && target.State == "covered" {
			t.Fatal("invalid unary cardinality earned coverage")
		}
	}
}
