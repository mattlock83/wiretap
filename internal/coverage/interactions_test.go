package coverage

import (
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func TestEventInteractionsAreRetainedButExplicitlyUnmeasured(t *testing.T) {
	flow := artifact.Flow{ID: "events", Protocol: "grpc", Interaction: &artifact.Interaction{Kind: "stream", StreamMode: "bidirectional", Boundary: artifact.BoundaryRef{Role: "dependency", ID: "chat"}, Transport: "http2", Operation: "/chat.Chat/Connect", CaptureState: "complete", Events: []artifact.InteractionEvent{{ID: "send", Direction: "sent", Kind: "message"}, {ID: "receive", Sequence: 1, Direction: "received", Kind: "message"}}}}
	capture := &artifact.Artifact{TestCase: artifact.TestCase{ID: "ui-test", Name: "chat UI", Status: "pass"}, Traffic: []artifact.Flow{flow}}
	run := &bundle.Run{RunID: "run", RunState: "complete", Attempts: []bundle.AttemptRef{{AttemptID: "ui-test", FileRef: bundle.FileRef{Path: "tests/ui-test.json"}}}}
	report, err := GenerateBundleSnapshot(t.TempDir(), run, []*artifact.Artifact{capture}, "UI", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unmeasured) != 1 || len(report.Contracts.Unmeasured) != 1 || report.Contracts.Complete {
		t.Fatalf("event measurement limitation is missing: %+v", report)
	}
	if len(report.Provided.Specs) != 0 {
		t.Fatalf("invented a provided API: %+v", report.Provided)
	}
	if len(report.TestCases) != 1 || len(report.TestCases[0].Traffic) != 1 || report.TestCases[0].Traffic[0].Interaction == nil || len(report.TestCases[0].Traffic[0].Interaction.Events) != 2 {
		t.Fatalf("event evidence disappeared from test report: %+v", report.TestCases)
	}
	// Unsupported events must not take away coverage from a separate supported
	// request that has its own complete evidence.
	schema := artifact.SchemaRef{ID: "health", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `{"openapi":"3.0.3","info":{"title":"Health","version":"1"},"paths":{"/health":{"get":{"responses":{"200":{"description":"ok"}}}}}}`}}
	run.APIID, run.Schemas, capture.Schemas = "health", []artifact.SchemaRef{schema}, []artifact.SchemaRef{schema}
	capture.Traffic = append(capture.Traffic, artifact.Flow{ID: "http", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://service/health"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}})
	report, err = GenerateBundleSnapshot(t.TempDir(), run, []*artifact.Artifact{capture}, "mixed", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Provided.Specs) != 1 || len(report.Provided.Specs[0].Exercised) != 1 || report.Provided.Specs[0].Exercised[0].Operation != "GET /health" || len(report.Unmeasured) != 1 {
		t.Fatalf("supported HTTP measurement was lost: %+v", report)
	}
}

func TestOpaqueContractsKeepUnknownCoverageDenominators(t *testing.T) {
	for _, kind := range []string{"avro", "asyncapi", "graphql"} {
		t.Run(kind, func(t *testing.T) {
			run := &bundle.Run{RunID: "run", RunState: "complete", Schemas: []artifact.SchemaRef{{ID: "catalog", AssetID: "catalog", Role: "dependency", Type: kind, Source: artifact.SchemaSource{Type: "file", Path: "contracts/catalog"}}}}
			report, err := GenerateBundleSnapshot(t.TempDir(), run, nil, "UI", false)
			if err != nil {
				t.Fatal(err)
			}
			if report.Dependent.Specs[0].DenominatorKnown || report.Contracts.Specs[0].DenominatorKnown || report.Contracts.Complete || len(report.Notes) == 0 {
				t.Fatalf("unsupported contract looked measurable: %+v", report)
			}
		})
	}
}

func TestExplicitProvidedBoundariesKeepIndependentIdentityAndCoverage(t *testing.T) {
	contract := `{"openapi":"3.0.3","info":{"title":"Health","version":"1"},"paths":{"/health":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	refs := []artifact.SchemaRef{
		{ID: "alpha-contract", Role: "provided", AssetID: "alpha", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: contract}},
		{ID: "beta-contract", Role: "provided", AssetID: "beta", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: contract}},
		{ID: "graphql-contract", Role: "provided", AssetID: "graphql-api", Type: "graphql", Source: artifact.SchemaSource{Type: "inline", Content: "type Query { health: String }"}},
	}
	capture := &artifact.Artifact{TestCase: artifact.TestCase{ID: "test", Name: "alpha health", Status: "pass"}, Schemas: refs, Traffic: []artifact.Flow{{ID: "health", Protocol: "http", Channel: "test_to_sut", AssetID: "alpha", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://alpha/health"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}}}}
	run := &bundle.Run{Application: &bundle.Application{Name: "Application", ID: "app"}, RunID: "run", RunState: "complete", Schemas: refs, Attempts: []bundle.AttemptRef{{AttemptID: "test", FileRef: bundle.FileRef{Path: "tests/test.json"}}}}
	report, err := GenerateBundleSnapshot(t.TempDir(), run, []*artifact.Artifact{capture}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Provided.Specs) != 3 || len(report.Dependent.Specs) != 0 || len(report.Contracts.Specs) != 3 {
		t.Fatalf("provided boundaries disappeared: %+v", report)
	}
	for _, spec := range report.Provided.Specs {
		switch spec.AssetID {
		case "alpha":
			if len(spec.Exercised) != 1 {
				t.Fatal("alpha request lost")
			}
		case "beta":
			if len(spec.Exercised) != 0 {
				t.Fatal("alpha request credited to beta")
			}
		case "graphql-api":
			if spec.DenominatorKnown {
				t.Fatal("opaque schema gained a known denominator")
			}
		default:
			t.Fatalf("unexpected API %q", spec.AssetID)
		}
	}
	for _, spec := range report.Contracts.Specs {
		if spec.APIID == "" || spec.APIID == "app" || spec.Origin != "provided" {
			t.Fatalf("application identity replaced API boundary: %+v", spec)
		}
		if spec.APIID == "graphql-api" && (spec.DenominatorKnown || len(spec.Notes) == 0) {
			t.Fatal("opaque provided contract was not qualified")
		}
		for _, target := range spec.Targets {
			if spec.APIID == "beta" && target.State == "covered" {
				t.Fatal("alpha condition credited to beta")
			}
		}
	}
}
