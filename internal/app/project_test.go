package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/project"
)

func TestProjectDefaultsAndPerAPISchemaException(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	optional := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"value":true}`)) }))
	defer upstream.Close()
	instance.project = &project.Config{SUT: project.API{URL: upstream.URL, SchemaRequired: &optional}, Dependencies: map[string]project.API{"inventory": {URL: upstream.URL, Kind: "mock"}}}
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	if start["defaults"] == nil {
		t.Fatal("project defaults not returned")
	}
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "configured test"})
	for _, path := range []string{"/items/123", "/__wiretap/dep/inventory/items/123"} {
		res, err := http.Get(session["sut_url"].(string) + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal(res.Status)
		}
	}
	result := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"]})
	art := dependencyArtifact(t, result)
	if art.TestCase.Status != "pass" || art.SchemaValidation.Status != "skipped" || len(art.SchemaValidation.Skipped) != 2 || len(art.Traffic) != 2 {
		t.Fatalf("exception lost: %#v", art)
	}
	root := result["bundle_path"].(string)
	_, captures, err := bundle.Load(root)
	if err != nil || len(captures) != 1 {
		t.Fatalf("bundle: %v", err)
	}
	report, err := coverage.Generate(t.Context(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []coverage.SpecCoverageGroup{report.Provided, report.Dependent} {
		if len(group.Specs) != 1 || group.Specs[0].DenominatorKnown || len(group.Specs[0].Exercised) != 1 || len(group.Specs[0].Exercised[0].Samples) != 1 {
			t.Fatalf("unknown coverage must retain observations: %#v", group)
		}
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}

func TestSchemaExceptionDoesNotWeakenOtherAPIsOrIgnoreInvalidSpecs(t *testing.T) {
	required := true
	deps, schemas, err := normalizeDependencies([]dependencyRegistration{{AssetID: "optional", UpstreamURL: "http://dep"}, {AssetID: "strict", UpstreamURL: "http://dep", Schema: permissiveDependencySchema()}})
	if err != nil || len(deps) != 2 {
		t.Fatal(err)
	}
	if _, _, err := normalizeDependencies([]dependencyRegistration{{AssetID: "required", UpstreamURL: "http://dep", SchemaRequired: &required}}); err == nil {
		t.Fatal("schema requirement lost")
	}
	if _, _, err := normalizeDependencies([]dependencyRegistration{{AssetID: "invalid", UpstreamURL: "http://dep", Schema: &artifact.SchemaRef{Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: "bad schema"}}}}); err == nil {
		t.Fatal("invalid supplied schema ignored")
	}
	art := &artifact.Artifact{TestCase: artifact.TestCase{Status: "pass"}, Schemas: schemas, Traffic: []artifact.Flow{
		{ID: "optional", Protocol: "http", Channel: "sut_to_dependency", AssetID: "optional", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://dep/anything"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}},
		{ID: "strict", Protocol: "http", Channel: "sut_to_dependency", AssetID: "strict", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://dep/not-in-spec"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}},
	}}
	applySchemaValidation(art)
	if art.SchemaValidation.Status != "fail" || art.TestCase.Status != "pass" || len(art.SchemaValidation.Skipped) != 1 {
		t.Fatalf("strict validation lost: %#v", art.SchemaValidation)
	}
	for _, issue := range art.SchemaValidation.Errors {
		if issue.FlowID != "strict" || strings.Contains(issue.Message, "optional") {
			t.Fatalf("wrong API validation: %#v", issue)
		}
	}
}

func TestHTTPDependencyDefaultsDoNotDisableGRPCContractAcquisition(t *testing.T) {
	registered, schemas, err := normalizeDependencies([]dependencyRegistration{{AssetID: "health", UpstreamURL: "http://grpc.test", Protocol: "grpc"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 0 || len(registered["health"].Schemas) != 0 {
		t.Fatal("gRPC reflection boundary was replaced by an omitted HTTP schema")
	}
	optional := false
	if _, _, err := normalizeDependencies([]dependencyRegistration{{AssetID: "health", UpstreamURL: "http://grpc.test", Protocol: "grpc", SchemaRequired: &optional}}); err == nil || !strings.Contains(err.Error(), "gRPC requires a contract") {
		t.Fatalf("gRPC contract opt-out was accepted: %v", err)
	}
}
