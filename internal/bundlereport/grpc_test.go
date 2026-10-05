package bundlereport

import (
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
)

func TestGRPCKnowledgePreservesOKAndFailedObservations(t *testing.T) {
	const procedure = "/example.Store/Get"
	run := &bundle.Run{RunID: "run", APIID: "app", RunState: "complete", DependencyKinds: map[string]string{"store": "mock"}, Attempts: []bundle.AttemptRef{{AttemptID: "ok", FileRef: bundle.FileRef{Path: "ok.json"}}, {AttemptID: "failed", FileRef: bundle.FileRef{Path: "failed.json"}}}}
	capture := func(id, status string) *artifact.Artifact {
		return &artifact.Artifact{TestCase: artifact.TestCase{ID: id, Status: status}, Traffic: []artifact.Flow{{ID: id + "-flow", Protocol: "grpc", Channel: "sut_to_dependency", AssetID: "store", Request: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", SchemaRef: "store", Procedure: procedure}}, Response: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", HTTPStatus: 200, StatusCode: 0}}}}}
	}
	report := &coverage.Report{Dependent: coverage.SpecCoverageGroup{Specs: []coverage.SpecCoverage{{SchemaID: "store", AssetID: "store", Type: "protobuf", DenominatorKnown: true, Exercised: []coverage.OperationSamples{{Operation: procedure, Samples: []coverage.FlowSample{{Protocol: "grpc", TestCaseID: "ok", FlowID: "ok-flow", Response: coverage.InteractionMessage{Status: 0}}}}}}}}, Contracts: &coverage.ContractCoverage{Specs: []*coverage.ContractSpec{{RulesVersion: coverage.ProtobufRulesVersion, SchemaID: "store", APIID: "store", Origin: "dependency"}}}}
	k := buildKnowledge(t.TempDir(), run, []*artifact.Artifact{capture("ok", "pass"), capture("failed", "fail")}, report)
	var api *knowledgeAPI
	for i := range k.APIs {
		if k.APIs[i].ID == "store" && k.APIs[i].SchemaID == "store" {
			api = &k.APIs[i]
		}
	}
	if api == nil || len(api.Operations) != 1 {
		t.Fatalf("missing API: %+v", k.APIs)
	}
	op := api.Operations[0]
	if len(op.Evidence) != 2 || len(op.RPCStatuses) != 1 || op.RPCStatuses[0] != 0 || op.EvidenceBasis == "incomplete_exchange" {
		t.Fatalf("gRPC observations lost: %+v", op)
	}
	if api.RulesVersion != coverage.ProtobufRulesVersion {
		t.Fatalf("wrong profile: %s", api.RulesVersion)
	}
	found := false
	for _, task := range k.Verification {
		if task.Kind == "verify_dependency" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing provider verification task: %+v", k.Verification)
	}
}

func TestGRPCViewerNamesStatusAndLinksEmptyWire(t *testing.T) {
	view := viewerGRPC(&artifact.GrpcMessage{StatusCode: 0, Messages: []artifact.GrpcPayload{{Index: 0, Body: &artifact.Body{Text: "{}"}, Wire: &artifact.Body{Encoding: "base64"}}}}, false)
	if view.Status != "OK (0)" || len(view.Messages) != 1 || len(view.Messages[0].Attachments) != 2 {
		t.Fatalf("incorrect RPC view: %+v", view)
	}
}

func TestGRPCKnowledgeKeepsFailedContractBoundary(t *testing.T) {
	run := &bundle.Run{RunID: "run", APIID: "app", RunState: "interrupted", DependencyKinds: map[string]string{"store": "mock"}, Contracts: []bundle.ContractResolution{{APIID: "store", Role: "dependency", Protocol: "grpc", Source: "reflection", State: "failed", Diagnostic: "contract acquisition failed"}}}
	k := buildKnowledge(t.TempDir(), run, nil, &coverage.Report{})
	if len(k.APIs) < 1 || k.APIs[0].ID != "store" || k.APIs[0].ContractResolution != "failed" || k.APIs[0].DenominatorKnown {
		t.Fatalf("unresolved boundary missing: %+v", k.APIs)
	}
	found := false
	for _, task := range k.Verification {
		if task.Kind == "resolve_contract" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing acquisition task")
	}
}
