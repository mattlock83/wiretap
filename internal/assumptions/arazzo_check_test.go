package assumptions

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func integrationArazzoFlow() artifact.Flow {
	flow := httpNormalizationFixture()
	flow.Request.URL = "http://consumer.test/items?currency=AUD"
	flow.Response.Trailers = nil
	return flow
}

func writeArazzoCheckProjection(t *testing.T, root string) {
	t.Helper()
	export, err := arazzo.Generate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if export.Document != nil {
		base := filepath.Join(root, "exports", "arazzo")
		for _, file := range export.Files {
			path := filepath.Join(base, filepath.FromSlash(file.Path))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, file.Data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		var data bytes.Buffer
		if err := arazzo.Write(&data, export.Document); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "arazzo.json"), data.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}

}

func arazzoPair(t *testing.T, consumer, provider artifact.Flow) Options {
	t.Helper()
	opts := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, provider)})
	writeArazzoCheckProjection(t, opts.Consumer)
	writeArazzoCheckProjection(t, opts.Provider)
	return opts
}

func TestArazzoChecksUseCompleteArchiveResponsesWithArazzo(t *testing.T) {
	for _, scenario := range []string{"compatible", "response body", "response header", "request", "ignored header"} {
		t.Run(scenario, func(t *testing.T) {
			consumer, provider := integrationArazzoFlow(), integrationArazzoFlow()
			provider.Request.URL = "https://provider.test/items?currency=AUD"
			switch scenario {
			case "response body":
				provider.Response.Body.Text = `{"available":false,"status":"pending","items":[1,2]}`
			case "response header", "ignored header":
				provider.Response.Headers["X-Business"] = []string{"different"}
			case "request":
				provider.Request.Body.Text = `{"amount":2,"items":[1,null]}`
			}
			opts := arazzoPair(t, consumer, provider)
			if scenario == "ignored header" {
				opts.IgnoreHeaders = []string{"X-Business"}
			}
			report := mustCheck(t, opts)
			want := scenario == "compatible" || scenario == "ignored header"
			if report.Compatible != want || report.Counts.Assumptions != 1 {
				t.Fatalf("unexpected verdict: %+v", report)
			}
			if !want && scenario != "request" && !findingCode(report, "provider_response_mismatch") {
				t.Fatalf("response evidence lost: %+v", report)
			}
			if scenario == "request" && !findingCode(report, "provider_request_uncovered") {
				t.Fatalf("missing request evidence: %+v", report)
			}
			for _, source := range []Source{report.Consumer, report.Provider} {
				if source.Arazzo == "" || len(source.ArazzoFiles) == 0 || source.ManifestSHA256 == "" {
					t.Fatalf("incomplete Arazzo provenance: %+v", source)
				}
			}

		})
	}
}

func TestArazzoChecksPreserveContractDriftChecks(t *testing.T) {
	makeCapture := func(provider bool, enum string) *artifact.Artifact {
		flow := validatingHTTPFlow()
		capture := checkCapture(provider, flow)
		role, asset := "dependency", "stock"
		if provider {
			role, asset = "sut", ""
		}
		schema := strings.Replace(validatingHTTPBodySchema, `["open","closed"]`, enum, 1)
		capture.Schemas = []artifact.SchemaRef{{ID: "contract", Role: role, AssetID: asset, Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: validatingHTTPDocument(schema)}}}
		return capture
	}
	opts := checkHTTPPair(t, []*artifact.Artifact{makeCapture(false, `["open","closed"]`)}, []*artifact.Artifact{makeCapture(true, `["open","closed","new"]`)})
	writeArazzoCheckProjection(t, opts.Consumer)
	writeArazzoCheckProjection(t, opts.Provider)
	report := mustCheck(t, opts)
	if report.Compatible || !findingCode(report, "http_contract_mismatch") {
		t.Fatalf("Arazzo bypassed contract rules: %+v", report)
	}
}

func TestArazzoChecksKeepFailedProviderAndIdentityUnverified(t *testing.T) {
	for _, scenario := range []string{"failed provider", "incomplete provider", "wrong provider identity"} {
		t.Run(scenario, func(t *testing.T) {
			consumer := checkCapture(false, integrationArazzoFlow())
			provider := checkCapture(true, integrationArazzoFlow())
			identity := mappedConsumerCheckIdentity()
			providerIdentity, state := providerCheckIdentity(), "complete"
			code := "provider_request_uncovered"
			switch scenario {
			case "failed provider":
				provider.TestCase.Status = "fail"
			case "incomplete provider":
				state = "interrupted"
				code = "provider_run_incomplete"
			case "wrong provider identity":
				providerIdentity.Application.ID = "other"
				code = "provider_identity_mismatch"
			}
			opts := Options{Consumer: writeCheckArchive(t, identity, "complete", consumer), Provider: writeCheckArchive(t, providerIdentity, state, provider)}
			writeArazzoCheckProjection(t, opts.Consumer)
			writeArazzoCheckProjection(t, opts.Provider)
			report := mustCheck(t, opts)
			if report.Compatible || !findingCode(report, code) {
				t.Fatalf("untrusted provider was certified: %+v", report)
			}
		})
	}
}

func TestArazzoCheckSelectionAppliesToRunSets(t *testing.T) {
	first, second := integrationArazzoFlow(), integrationArazzoFlow()
	second.Request.URL = "http://consumer.test/other"
	second.Response.Body.Text = `{"other":true}`
	consumer := writeCheckArchive(t, mappedConsumerCheckIdentity(), "complete", checkCapture(false, first, second))
	providerOne := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "one"), "complete", checkCapture(true, cloneNormalizationFlow(t, first)))
	providerTwo := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "one"), "complete", checkCapture(true, cloneNormalizationFlow(t, second)))
	for _, root := range []string{consumer, providerOne, providerTwo} {
		writeArazzoCheckProjection(t, root)
	}
	consumerSet, providerSet := writeRunSet(t, consumer), writeRunSet(t, providerOne, providerTwo)
	report, err := CheckRunSets(t.Context(), consumerSet, providerSet, "", nil)
	if err != nil || !report.Compatible || report.Counts.Verified != 2 || len(report.Providers) != 2 {
		t.Fatalf("Arazzo shards: %+v, %v", report, err)
	}
	for _, source := range append(report.Consumers, report.Providers...) {
		if source.Arazzo == "" {
			t.Fatalf("wrong run-set inputs: %+v", source)
		}
	}
}

func mappedConsumerCheckIdentity() bundle.Identity {
	identity := consumerCheckIdentity()
	identity.ProviderMappings = map[string]bundle.ProviderRef{"stock": {ApplicationID: "inventory", APIID: "inventory-api"}}
	return identity
}
