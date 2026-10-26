package assumptions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func checkFixtureFlow() artifact.Flow {
	flow := httpNormalizationFixture()
	flow.Request.URL = "http://127.0.0.1:1234/items/a%2Fb?q=one&a=first"
	flow.Response.Trailers = nil
	return flow
}

func consumerCheckIdentity() bundle.Identity {
	return bundle.Identity{Application: &bundle.Application{ID: "shop"}, APIID: "shop-api", SUTTargetKind: "service", DependencyKinds: map[string]string{"stock": "mock"}}
}

func providerCheckIdentity() bundle.Identity {
	return bundle.Identity{Application: &bundle.Application{ID: "inventory"}, APIID: "inventory-api", SUTTargetKind: "service", SUTSource: map[string]any{"version": "v1"}}
}

// Build the actual portable archive and Arazzo projections used by the CLI.
// No hand-written manifest or workflow fixture can bypass the normal writer,
// attachment hashes, identity inventory or export completeness rules.
func writeCheckArchive(t *testing.T, identity bundle.Identity, state string, captures ...*artifact.Artifact) string {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(identity); err != nil {
		t.Fatal(err)
	}
	for index, capture := range captures {
		if capture.TestCase.ID == "" {
			capture.TestCase.ID = fmt.Sprintf("attempt-%d", index+1)
		}
		if capture.TestCase.Name == "" {
			capture.TestCase.Name = "test " + capture.TestCase.ID
		}
		if capture.TestCase.StartedAt.IsZero() {
			capture.TestCase.StartedAt = time.Date(2026, 10, 24, 1, index, 0, 0, time.UTC)
		}
		if capture.TestCase.Status == "" {
			capture.TestCase.Status = "pass"
		}
		capture.Version = 1
		frozen, err := writer.RegisterPrepared(capture.TestCase, capture.Schemas)
		if err != nil {
			t.Fatal(err)
		}
		capture.Schemas = nil
		for _, schema := range frozen {
			capture.Schemas = append(capture.Schemas, schema.Ref)
		}
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	if state != "open" {
		if err := writer.Finalize(state); err != nil {
			t.Fatal(err)
		}
	}
	export, exportErr := arazzo.Generate(writer.Path(), "")
	// Unrepresentable fixtures exercise rejection paths; no export may certify them.
	if exportErr == nil && export.Document != nil {
		directory := filepath.Join(writer.Path(), "exports", "arazzo")
		for _, companion := range export.Files {
			path := filepath.Join(directory, filepath.FromSlash(companion.Path))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, companion.Data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		file, err := os.Create(filepath.Join(directory, "arazzo.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := arazzo.Write(file, export.Document); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return writer.Path()
}

func checkCapture(provider bool, flows ...artifact.Flow) *artifact.Artifact {
	for i := range flows {
		flows[i].ID = fmt.Sprintf("flow-%d", i+1)
		flows[i].Channel, flows[i].AssetID = "sut_to_dependency", "stock"
		if provider {
			flows[i].Channel, flows[i].AssetID = "test_to_sut", ""
		}
	}
	return &artifact.Artifact{Traffic: flows}
}

func checkHTTPPair(t *testing.T, consumers, providers []*artifact.Artifact) Options {
	t.Helper()
	return Options{Consumer: writeCheckArchive(t, consumerCheckIdentity(), "complete", consumers...), Provider: writeCheckArchive(t, providerCheckIdentity(), "complete", providers...)}
}

func mustCheck(t *testing.T, options Options) *Report {
	t.Helper()
	report, err := Check(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func findingCode(report *Report, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func TestCheckConsumerObligationsIgnoreExtraProviderExamples(t *testing.T) {
	consumer := checkFixtureFlow()
	provider := cloneNormalizationFlow(t, consumer)
	provider.Request.URL = "https://provider.example/items/a%2Fb?a=first&q=one"
	provider.Request.Headers["Traceparent"] = []string{"provider-trace"}
	extra := cloneNormalizationFlow(t, provider)
	extra.Request.URL = "https://provider.example/unrelated"
	opts := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, provider, extra)})

	t.Run("explicit pair", func(t *testing.T) {
		report := mustCheck(t, opts)
		if !report.Compatible || report.Counts.Assumptions != 1 || report.Counts.Verified != 1 || len(report.Findings) != 0 || len(report.Matches) != 1 {
			t.Fatalf("supported mock example failed, or extra provider operation became an obligation: %+v", report)
		}
		if report.Dependency != "stock" || report.Binding != "explicit_inputs" || report.RulesVersion != RulesVersion || report.Consumer.ManifestSHA256 == "" {
			t.Fatalf("comparison lost selection or provenance: %+v", report)
		}
		for _, evidence := range []Evidence{report.Matches[0].Consumer, report.Matches[0].Provider} {
			if !strings.HasPrefix(evidence.EvidencePath, "tests/") || !strings.Contains(evidence.EvidencePath, ".json#/capture/traffic/") || evidence.RunID == "" || evidence.TestName == "" {
				t.Fatalf("match has no concrete test/flow reference: %+v", evidence)
			}
		}
	})

}

func TestCheckDistinguishesMissingScenarioFromResponseMismatch(t *testing.T) {
	for _, kind := range []string{"request", "response"} {
		t.Run(kind, func(t *testing.T) {
			consumer, provider := checkFixtureFlow(), checkFixtureFlow()
			if kind == "request" {
				consumer.Request.Body.Text = `{"amount":2,"items":[1,null]}`
			} else {
				consumer.Response.Body.Text = `{"available":false,"status":"pending","items":[1,2]}`
			}
			report := mustCheck(t, checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, provider)}))
			if report.Compatible || report.Counts.Verified != 0 || len(report.Findings) != 1 {
				t.Fatalf("unsupported example passed: %+v", report)
			}
			if kind == "request" {
				if report.Counts.Uncovered != 1 || !findingCode(report, "provider_request_uncovered") {
					t.Fatalf("missing concrete fixture was reported as a rejection: %+v", report)
				}
			} else if report.Counts.Mismatched != 1 || !findingCode(report, "provider_response_mismatch") || len(report.Findings[0].Differences) == 0 || report.Findings[0].Differences[0] != "response/body/value/available" {
				t.Fatalf("response mismatch lacks its concrete changed field: %+v", report)
			}
		})
	}
}

func TestCheckDoesNotSpliceResponsesAcrossProviderTests(t *testing.T) {
	consumer, first, second := checkFixtureFlow(), checkFixtureFlow(), checkFixtureFlow()
	consumer.Response.Body.Text = `{"available":true,"status":"shipped"}`
	first.Response.Body.Text = `{"available":true,"status":"pending"}`
	second.Response.Body.Text = `{"available":false,"status":"shipped"}`
	report := mustCheck(t, checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, first), checkCapture(true, second)}))
	if report.Compatible || report.Counts.Mismatched != 1 || len(report.Findings[0].Provider) != 2 {
		t.Fatalf("independent provider facts were combined into a fictional matching exchange: %+v", report)
	}
}

func TestCheckRequiresActualConsumerExamples(t *testing.T) {
	report := mustCheck(t, checkHTTPPair(t, nil, []*artifact.Artifact{checkCapture(true, checkFixtureFlow())}))
	if report.Compatible || report.Counts.Assumptions != 0 || !findingCode(report, "no_consumer_assumptions") {
		t.Fatalf("empty comparison falsely certified compatibility: %+v", report)
	}
}

func TestCheckProviderIdentityVersionAndKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*bundle.Identity, *bundle.Identity)
		code string
	}{
		{"application mismatch", func(c, p *bundle.Identity) {
			c.ProviderMappings["stock"] = bundle.ProviderRef{ApplicationID: "other", APIID: "inventory-api"}
		}, "provider_identity_mismatch"},
		{"API mismatch", func(c, p *bundle.Identity) {
			c.ProviderMappings["stock"] = bundle.ProviderRef{ApplicationID: "inventory", APIID: "other-api"}
		}, "provider_identity_mismatch"},
		{"version mismatch", func(c, p *bundle.Identity) {
			c.ProviderMappings["stock"] = bundle.ProviderRef{ApplicationID: "inventory", APIID: "inventory-api", Version: "v2"}
		}, "provider_version_unverified"},
		{"version absent", func(c, p *bundle.Identity) {
			c.ProviderMappings["stock"] = bundle.ProviderRef{ApplicationID: "inventory", APIID: "inventory-api", Version: "v1"}
			p.SUTSource = nil
		}, "provider_version_unverified"},
		{"mock provider", func(c, p *bundle.Identity) { p.SUTTargetKind = "mock" }, "provider_not_service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			consumer, provider := consumerCheckIdentity(), providerCheckIdentity()
			consumer.ProviderMappings = map[string]bundle.ProviderRef{}
			tc.edit(&consumer, &provider)
			opts := Options{Dependency: "stock", Consumer: writeCheckArchive(t, consumer, "complete", checkCapture(false, checkFixtureFlow())), Provider: writeCheckArchive(t, provider, "complete", checkCapture(true, checkFixtureFlow()))}
			report := mustCheck(t, opts)
			if report.Compatible || !findingCode(report, tc.code) {
				t.Fatalf("incorrect provider binding was certified: %+v", report)
			}
		})
	}
	consumer := consumerCheckIdentity()
	consumer.ProviderMappings = map[string]bundle.ProviderRef{"stock": {ApplicationID: "inventory", APIID: "inventory-api", Version: "v1"}}
	report := mustCheck(t, Options{Consumer: writeCheckArchive(t, consumer, "complete", checkCapture(false, checkFixtureFlow())), Provider: writeCheckArchive(t, providerCheckIdentity(), "complete", checkCapture(true, checkFixtureFlow()))})
	if !report.Compatible || report.Binding != "declared_mapping" {
		t.Fatalf("correct declared binding was not verified: %+v", report)
	}
}

func TestCheckFailedAndIncompleteProviderAttemptsAreNotWitnesses(t *testing.T) {
	for _, kind := range []string{"failed", "skipped", "capture incomplete", "interrupted run", "open run"} {
		t.Run(kind, func(t *testing.T) {
			provider := checkCapture(true, checkFixtureFlow())
			state := "complete"
			switch kind {
			case "failed":
				provider.TestCase.Status = "fail"
			case "skipped":
				provider.TestCase.Status = "skipped"
			case "capture incomplete":
				provider.CaptureIncomplete = true
			case "interrupted run":
				state = "interrupted"
			case "open run":
				state = "open"
			}
			opts := Options{Consumer: writeCheckArchive(t, consumerCheckIdentity(), "complete", checkCapture(false, checkFixtureFlow())), Provider: writeCheckArchive(t, providerCheckIdentity(), state, provider)}
			if kind == "capture incomplete" {
				if _, err := Check(t.Context(), opts); !errors.Is(err, ErrInput) {
					t.Fatalf("incomplete Arazzo capture accepted: %v", err)
				}
				return
			}
			report := mustCheck(t, opts)
			if report.Compatible || report.Counts.Verified != 0 {
				t.Fatalf("nonqualifying provider evidence was used: %+v", report)
			}
			if state == "complete" && (report.Counts.Uncovered != 1 || len(report.ExcludedProviderEvidence) != 1) {
				t.Fatalf("excluded provider evidence lacks a coverage gap and reason: %+v", report)
			}
			if state != "complete" && !findingCode(report, "provider_run_incomplete") {
				t.Fatalf("unfinished run lacks its diagnostic: %+v", report)
			}
		})
	}
}

func TestCheckRejectsCorruptMissingAndStaleInputs(t *testing.T) {
	for _, kind := range []string{"capture corruption", "missing provider document", "stale provider criterion", "wrong consumer document", "same archive", "invalid ignored header"} {
		t.Run(kind, func(t *testing.T) {
			opts := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, checkFixtureFlow())}, []*artifact.Artifact{checkCapture(true, checkFixtureFlow())})
			switch kind {
			case "capture corruption":
				if err := os.WriteFile(filepath.Join(opts.Provider, "tests", "attempt-1.json"), []byte(`{"not":"the committed attempt"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing provider document":
				if err := os.Remove(filepath.Join(opts.Provider, "exports", "arazzo", "arazzo.json")); err != nil {
					t.Fatal(err)
				}
			case "stale provider criterion":
				path := filepath.Join(opts.Provider, "exports", "arazzo", "arazzo.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "$statusCode == 200", "$statusCode == 201")), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong consumer document":
				opts.ConsumerArazzo = filepath.Join(opts.Provider, "exports", "arazzo", "arazzo.json")
			case "same archive":
				opts.Provider = opts.Consumer
			case "invalid ignored header":
				opts.IgnoreHeaders = []string{"invalid header"}
			}
			if _, err := Check(t.Context(), opts); !errors.Is(err, ErrInput) {
				t.Fatalf("invalid input did not return ErrInput: %v", err)
			}
		})
	}
}

func TestCheckDependencySelectionAndManifestPaths(t *testing.T) {
	consumer := consumerCheckIdentity()
	consumer.DependencyKinds["currency"] = "mock"
	opts := Options{Consumer: writeCheckArchive(t, consumer, "complete", checkCapture(false, checkFixtureFlow())), Provider: writeCheckArchive(t, providerCheckIdentity(), "complete", checkCapture(true, checkFixtureFlow()))}
	if _, err := Check(t.Context(), opts); !errors.Is(err, ErrInput) {
		t.Fatalf("ambiguous mock selection was silently guessed: %v", err)
	}
	opts.Dependency = "stock"
	opts.Consumer, opts.Provider = filepath.Join(opts.Consumer, "bundle.json"), filepath.Join(opts.Provider, "bundle.json")
	if report := mustCheck(t, opts); !report.Compatible || filepath.Base(report.Consumer.BundlePath) == "bundle.json" {
		t.Fatalf("explicit boundary or manifest paths failed: %+v", report)
	}
	opts.Dependency = "missing"
	if _, err := Check(t.Context(), opts); !errors.Is(err, ErrInput) {
		t.Fatalf("undeclared dependency was accepted: %v", err)
	}
	consumer.ProviderMappings = map[string]bundle.ProviderRef{"stock": {ApplicationID: "inventory", APIID: "inventory-api"}}
	opts.Consumer = writeCheckArchive(t, consumer, "complete", checkCapture(false, checkFixtureFlow()))
	opts.Dependency = ""
	if report := mustCheck(t, opts); !report.Compatible || report.Dependency != "stock" || report.Binding != "declared_mapping" {
		t.Fatalf("unique declared provider mapping was not selected: %+v", report)
	}
}

func TestCheckExplicitHeaderExceptionsAndCancellation(t *testing.T) {
	consumer, provider := checkFixtureFlow(), checkFixtureFlow()
	provider.Request.Headers["Authorization"] = []string{"Bearer other-dummy"}
	opts := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, provider)})
	if report := mustCheck(t, opts); report.Compatible || report.Counts.Uncovered != 1 {
		t.Fatalf("application authorization was silently ignored: %+v", report)
	}
	opts.IgnoreHeaders = []string{"AUTHORIZATION"}
	if report := mustCheck(t, opts); !report.Compatible {
		t.Fatalf("explicit application-header exception was not applied: %+v", report)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Check(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled comparison continued: %v", err)
	}
}

func TestDifferencePathsPreservesLargeNumericMismatch(t *testing.T) {
	left := `{"body":{"value":{"id":9007199254740992}}}`
	right := `{"body":{"value":{"id":9007199254740993}}}`
	paths, _ := jsonDifferences(left, right, "response")
	encoded, _ := json.Marshal(paths)
	if string(encoded) != `["response/body/value/id"]` {
		t.Fatalf("large numeric mismatch lost its field location: %s", encoded)
	}
}
