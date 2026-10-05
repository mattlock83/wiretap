package coverage

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

func grpcRejectionFixture(t *testing.T) (*bundle.Run, *artifact.Artifact) {
	return grpcRejectionFixtureSource(t, grpcConditionsProto)
}

func grpcRejectionFixtureSource(t *testing.T, source string) (*bundle.Run, *artifact.Artifact) {
	t.Helper()
	run, capture := grpcConditionFixture(t, source, `{"enabled":true}`)
	flow := &capture.Traffic[0]
	for _, side := range []*artifact.GrpcMessage{flow.Request.GRPC, flow.Response.GRPC} {
		side.StreamKind = "unary"
		side.Observation = &artifact.GrpcObservation{CountsComplete: true, EOF: true, End: "eof"}
	}
	request := flow.Request.GRPC
	raw, err := base64.StdEncoding.DecodeString(request.Messages[0].Wire.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages[0].DecodeState = "decoded"
	request.Messages[0].Wire.OriginalBytes = int64(len(raw))
	request.Messages[0].Observation = &artifact.GrpcPayloadObservation{WireBytes: uint64(len(raw)), StartSequence: 0, CompleteSequence: 1, StartedOffsetNS: 0, CompletedOffsetNS: 1}
	request.ObservedBytes = int64(5 + len(raw))
	request.Observation.CompleteFrames, request.Observation.RetainedFrames, request.Observation.ObservedBytes = 1, 1, request.ObservedBytes
	flow.Response.GRPC.Messages = nil
	flow.Response.GRPC.StatusCode = 3
	flow.Response.GRPC.StatusSource = "trailers"
	index := 0
	target := artifact.RejectionTarget{Key: "invalid-enabled", Protocol: "grpc", SchemaID: "example", Operation: "/fixture.Example/Call", FlowID: flow.ID, Location: "/messages/0/body/enabled", MessageIndex: &index, Constraint: "bool.const", ExpectedStatuses: []int{3}}
	capture.Exploration = &artifact.Exploration{Mode: "rejection", Catalogue: []artifact.RejectionTarget{target}, Target: &target, RequestVerified: true}
	capture.Assertions = []artifact.Assertion{{ID: "expected-rejection", FlowID: flow.ID, Operator: "status_in", Result: "pass"}, {ID: "response-schema-" + flow.ID, FlowID: flow.ID, Operator: "schema", Result: "pass"}}
	return run, capture
}

func TestGRPCRejectionEvidenceRetainsEachStreamWitness(t *testing.T) {
	source := strings.Replace(grpcConditionsProto, "rpc Call(Payload) returns (Payload)", "rpc Call(stream Payload) returns (stream Payload)", 1)
	run, first := grpcRejectionFixtureSource(t, source)
	_, second := grpcRejectionFixtureSource(t, source)
	for _, capture := range []*artifact.Artifact{first, second} {
		capture.Traffic[0].Request.GRPC.StreamKind = "bidirectional"
		capture.Traffic[0].Response.GRPC.StreamKind = "bidirectional"
	}
	second.TestCase.ID = "second"
	second.Exploration.Catalogue = nil
	index := 1
	second.Exploration.Target.MessageIndex = &index
	second.Exploration.Target.Location = "/messages/1/body/enabled"
	second.Exploration.Target.ExpectedStatuses = []int{11}
	second.Traffic[0].Response.GRPC.StatusCode = 11
	request := second.Traffic[0].Request.GRPC
	payload := request.Messages[0]
	payload.Index = index
	payload.Observation = &artifact.GrpcPayloadObservation{WireBytes: payload.Observation.WireBytes, StartSequence: 2, CompleteSequence: 3, StartedOffsetNS: 2, CompletedOffsetNS: 3}
	request.Messages = append(request.Messages, payload)
	request.ObservedBytes *= 2
	request.Observation.CompleteFrames, request.Observation.RetainedFrames = 2, 2
	request.Observation.ObservedBytes = request.ObservedBytes
	run.Attempts = append(run.Attempts, bundle.AttemptRef{FileRef: bundle.FileRef{Path: "tests/second.json"}})
	result := buildRejectionCoverage(run, []*artifact.Artifact{first, second})
	if result.Covered != 1 || len(result.Targets) != 1 || len(result.Targets[0].Evidence) != 2 {
		t.Fatalf("expected one rule with two native witnesses: %+v", result)
	}
	for i, evidence := range result.Targets[0].Evidence {
		if evidence.Result != "covered" || evidence.MessageIndex == nil || *evidence.MessageIndex != i || evidence.InstanceLocation != []*artifact.Artifact{first, second}[i].Exploration.Target.Location {
			t.Fatalf("witness index/location lost or not credited: %+v", evidence)
		}
		if len(evidence.ExpectedStatuses) != 1 || evidence.ExpectedStatuses[0] != []*artifact.Artifact{first, second}[i].Exploration.Target.ExpectedStatuses[0] {
			t.Fatalf("per-case rejection expectation replaced by catalogue: %+v", evidence)
		}
	}
}

func TestGRPCRejectionRequiresNativeStatusAndCompleteEvidence(t *testing.T) {
	for name, change := range map[string]func(*artifact.Artifact){
		"trailers only": func(*artifact.Artifact) {},
		"out of range": func(a *artifact.Artifact) {
			a.Traffic[0].Response.GRPC.StatusCode = 11
			a.Exploration.Target.ExpectedStatuses = []int{11}
		},
		"auth failure":          func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.StatusCode = 16 },
		"permission denied":     func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.StatusCode = 7 },
		"timeout":               func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.StatusCode = 4 },
		"internal":              func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.StatusCode = 13 },
		"transport status":      func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.HTTPStatus = 503 },
		"unknown status source": func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.StatusSource = "" },
		"proxy status":          func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.StatusSource = "proxy" },
		"wrong boundary schema": func(a *artifact.Artifact) { a.Traffic[0].Request.GRPC.SchemaRef = "different" },
		"open request": func(a *artifact.Artifact) {
			a.Traffic[0].Request.GRPC.Observation.EOF = false
			a.Traffic[0].Request.GRPC.Observation.End = "open"
		},
		"unknown counts":      func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.Observation.CountsComplete = false },
		"missing observation": func(a *artifact.Artifact) { a.Traffic[0].Response.GRPC.Observation = nil },
		"redacted":            func(a *artifact.Artifact) { a.Traffic[0].Request.GRPC.Messages[0].Redacted = true },
		"undecoded": func(a *artifact.Artifact) {
			a.Traffic[0].Request.GRPC.Messages[0].DecodeState = "unavailable"
			a.Traffic[0].Request.GRPC.Messages[0].DecodeError = "limit"
		},
		"corrupt wire":  func(a *artifact.Artifact) { a.Traffic[0].Request.GRPC.Messages[0].Wire.Bytes = "!" },
		"wrong message": func(a *artifact.Artifact) { i := 1; a.Exploration.Target.MessageIndex = &i },
		"wrong protocol": func(a *artifact.Artifact) {
			a.Exploration.Target.Protocol = ""
			a.Exploration.Target.ExpectedStatuses = []int{400}
			a.Traffic[0].Response.HTTP = &artifact.HttpMessage{Status: 400}
		},
		"no check":   func(a *artifact.Artifact) { a.Assertions = nil },
		"unverified": func(a *artifact.Artifact) { a.Exploration.RequestVerified = false },
	} {
		t.Run(name, func(t *testing.T) {
			run, capture := grpcRejectionFixture(t)
			change(capture)
			result := buildRejectionCoverage(run, []*artifact.Artifact{capture})
			want := 0
			if name == "trailers only" || name == "out of range" {
				want = 1
			}
			if result.Covered != want {
				t.Fatalf("covered=%d want=%d: %+v", result.Covered, want, result.Targets[0].Evidence)
			}
			if want == 1 && result.Targets[0].Evidence[0].MessageIndex == nil {
				t.Fatal("missing native message evidence index")
			}
		})
	}
}

func TestGRPCGeneratedInvalidInputDoesNotEarnValidRequestCoverage(t *testing.T) {
	run, capture := grpcRejectionFixture(t)
	report := buildContractCoverage(run, []*artifact.Artifact{capture})
	for _, target := range report.Specs[0].Targets {
		if target.Phase == "request" && target.State == "covered" {
			t.Fatalf("invalid message credited positive request target: %+v", target)
		}
	}
	for _, combination := range report.Specs[0].Combinations {
		if combination.Phase == "request" && combination.Evidence.Result == "covered" {
			t.Fatal("invalid request combination credited coverage")
		}
	}
	if report.Rejections.Covered != 1 {
		t.Fatal("expected native rejection was not covered")
	}
	if len(report.Specs[0].Violations) != 1 || !report.Specs[0].Violations[0].Expected {
		t.Fatal("deliberate constraint violation became an unexpected defect")
	}
}
