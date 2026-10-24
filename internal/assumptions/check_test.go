package assumptions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/postman"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func consumerCheckIdentity() bundle.Identity {
	return bundle.Identity{Application: &bundle.Application{ID: "shop"}, APIID: "shop-api", SUTTargetKind: "service", DependencyKinds: map[string]string{"stock": "mock"}}
}

func providerCheckIdentity() bundle.Identity {
	return bundle.Identity{Application: &bundle.Application{ID: "inventory"}, APIID: "inventory-api", SUTTargetKind: "service", SUTSource: map[string]any{"version": "v1"}}
}

// Build the actual portable archive and Postman projections used by the CLI.
// No hand-written manifest or collection fixture can bypass the normal writer,
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
		frozen, err := writer.Register(capture.TestCase, capture.Schemas)
		if err != nil {
			t.Fatal(err)
		}
		capture.Schemas = frozen
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	if state != "open" {
		if err := writer.Finalize(state); err != nil {
			t.Fatal(err)
		}
	}
	result, err := postman.GenerateAllWithOptions(writer.Path(), "", postman.Options{OmitIncomplete: true})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(writer.Path(), "exports", "postman")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, export := range result.Exports {
		file, err := os.Create(filepath.Join(directory, export.Filename))
		if err != nil {
			t.Fatal(err)
		}
		writeErr := postman.WriteExport(file, export)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("write Postman projection: %v / %v", writeErr, closeErr)
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
	return Options{Mode: "consumer", Consumer: writeCheckArchive(t, consumerCheckIdentity(), "complete", consumers...), Provider: writeCheckArchive(t, providerCheckIdentity(), "complete", providers...)}
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

func TestCheckConsumerAndProviderVerifySameObligation(t *testing.T) {
	consumer := httpNormalizationFixture()
	provider := cloneNormalizationFlow(t, consumer)
	provider.Request.HTTP.URL = "https://provider.example/items/a%2Fb?a=first&a=second&q=one"
	provider.Request.HTTP.Headers["Traceparent"] = []string{"provider-trace"}
	extra := cloneNormalizationFlow(t, provider)
	extra.Request.HTTP.URL = "https://provider.example/unrelated"
	opts := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, provider, extra)})
	for _, mode := range []string{"consumer", "provider"} {
		t.Run(mode, func(t *testing.T) {
			opts.Mode = mode
			report := mustCheck(t, opts)
			if !report.Compatible || report.Counts.Assumptions != 1 || report.Counts.Verified != 1 || len(report.Findings) != 0 || len(report.Matches) != 1 {
				t.Fatalf("supported mock example failed, or extra provider operation became an obligation: %+v", report)
			}
			if report.Dependency != "stock" || report.Mode != mode || report.Binding != "explicit_inputs" || report.RulesVersion != RulesVersion || report.Consumer.ManifestSHA256 == "" {
				t.Fatalf("comparison lost selection or provenance: %+v", report)
			}
			for _, evidence := range []Evidence{report.Matches[0].Consumer, report.Matches[0].Provider} {
				if !strings.HasPrefix(evidence.EvidencePath, "tests/") || !strings.Contains(evidence.EvidencePath, ".json#/capture/traffic/") || evidence.RunID == "" || evidence.TestName == "" {
					t.Fatalf("match has no concrete test/flow reference: %+v", evidence)
				}
			}
		})
	}
}

func TestCheckDistinguishesMissingScenarioFromResponseMismatch(t *testing.T) {
	for _, kind := range []string{"request", "response"} {
		t.Run(kind, func(t *testing.T) {
			consumer, provider := httpNormalizationFixture(), httpNormalizationFixture()
			if kind == "request" {
				consumer.Request.HTTP.Body.Text = `{"amount":2,"items":[1,null]}`
			} else {
				consumer.Response.HTTP.Body.Text = `{"available":false,"status":"pending","items":[1,2]}`
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
	consumer, first, second := httpNormalizationFixture(), httpNormalizationFixture(), httpNormalizationFixture()
	consumer.Response.HTTP.Body.Text = `{"available":true,"status":"shipped"}`
	first.Response.HTTP.Body.Text = `{"available":true,"status":"pending"}`
	second.Response.HTTP.Body.Text = `{"available":false,"status":"shipped"}`
	report := mustCheck(t, checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, first), checkCapture(true, second)}))
	if report.Compatible || report.Counts.Mismatched != 1 || len(report.Findings[0].Provider) != 2 {
		t.Fatalf("independent provider facts were combined into a fictional matching exchange: %+v", report)
	}
}

func TestCheckRequiresActualConsumerExamples(t *testing.T) {
	report := mustCheck(t, checkHTTPPair(t, nil, []*artifact.Artifact{checkCapture(true, httpNormalizationFixture())}))
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
			opts := Options{Mode: "consumer", Dependency: "stock", Consumer: writeCheckArchive(t, consumer, "complete", checkCapture(false, httpNormalizationFixture())), Provider: writeCheckArchive(t, provider, "complete", checkCapture(true, httpNormalizationFixture()))}
			report := mustCheck(t, opts)
			if report.Compatible || !findingCode(report, tc.code) {
				t.Fatalf("incorrect provider binding was certified: %+v", report)
			}
		})
	}
	consumer := consumerCheckIdentity()
	consumer.ProviderMappings = map[string]bundle.ProviderRef{"stock": {ApplicationID: "inventory", APIID: "inventory-api", Version: "v1"}}
	report := mustCheck(t, Options{Mode: "provider", Consumer: writeCheckArchive(t, consumer, "complete", checkCapture(false, httpNormalizationFixture())), Provider: writeCheckArchive(t, providerCheckIdentity(), "complete", checkCapture(true, httpNormalizationFixture()))})
	if !report.Compatible || report.Binding != "declared_mapping" {
		t.Fatalf("correct declared binding was not verified: %+v", report)
	}
}

func TestCheckFailedAndIncompleteProviderAttemptsAreNotWitnesses(t *testing.T) {
	for _, kind := range []string{"failed", "skipped", "capture incomplete", "interrupted run", "open run"} {
		t.Run(kind, func(t *testing.T) {
			provider := checkCapture(true, httpNormalizationFixture())
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
			opts := Options{Mode: "consumer", Consumer: writeCheckArchive(t, consumerCheckIdentity(), "complete", checkCapture(false, httpNormalizationFixture())), Provider: writeCheckArchive(t, providerCheckIdentity(), state, provider)}
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
	for _, kind := range []string{"capture corruption", "missing provider collections", "stale provider response", "wrong consumer collection", "same archive", "invalid mode", "invalid ignored header"} {
		t.Run(kind, func(t *testing.T) {
			opts := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, httpNormalizationFixture())}, []*artifact.Artifact{checkCapture(true, httpNormalizationFixture())})
			switch kind {
			case "capture corruption":
				path := filepath.Join(opts.Provider, "tests", "attempt-1.json")
				if err := os.WriteFile(path, []byte(`{"not":"the committed attempt"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing provider collections":
				if err := os.RemoveAll(filepath.Join(opts.Provider, "exports", "postman")); err != nil {
					t.Fatal(err)
				}
			case "stale provider response":
				files, err := filepath.Glob(filepath.Join(opts.Provider, "exports", "postman", "*.json"))
				if err != nil || len(files) != 1 {
					t.Fatalf("provider collection: %v / %v", files, err)
				}
				data, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				data = []byte(strings.Replace(string(data), `\"available\":true`, `\"available\":false`, -1))
				if err := os.WriteFile(files[0], data, 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong consumer collection":
				files, err := filepath.Glob(filepath.Join(opts.Consumer, "exports", "postman", "sut-*.postman_collection.json"))
				if err != nil || len(files) != 1 {
					t.Fatalf("consumer SUT collection: %v / %v", files, err)
				}
				opts.ConsumerCollections = files
			case "same archive":
				opts.Provider = opts.Consumer
			case "invalid mode":
				opts.Mode = "sideways"
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
	opts := Options{Mode: "consumer", Consumer: writeCheckArchive(t, consumer, "complete", checkCapture(false, httpNormalizationFixture())), Provider: writeCheckArchive(t, providerCheckIdentity(), "complete", checkCapture(true, httpNormalizationFixture()))}
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
	opts.Consumer = writeCheckArchive(t, consumer, "complete", checkCapture(false, httpNormalizationFixture()))
	opts.Dependency = ""
	if report := mustCheck(t, opts); !report.Compatible || report.Dependency != "stock" || report.Binding != "declared_mapping" {
		t.Fatalf("unique declared provider mapping was not selected: %+v", report)
	}
}

func TestCheckExplicitHeaderExceptionsAndCancellation(t *testing.T) {
	consumer, provider := httpNormalizationFixture(), httpNormalizationFixture()
	provider.Request.HTTP.Headers["Authorization"] = []string{"Bearer other-dummy"}
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

func grpcCheckCapture(t *testing.T, provider bool, status healthpb.HealthCheckResponse_ServingStatus) *artifact.Artifact {
	t.Helper()
	descriptor, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}})
	if err != nil {
		t.Fatal(err)
	}
	schema := artifact.SchemaRef{ID: "health", AssetID: "stock", Role: "dependency", Type: "protobuf", Services: []string{"grpc.health.v1.Health"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(descriptor)}}
	if provider {
		schema.Role, schema.AssetID = "sut", "inventory-api"
	}
	side := func(message proto.Message, sequence uint64) *artifact.GrpcMessage {
		wire, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		projection, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		count := int64(len(wire) + 5)
		return &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health", StreamKind: "unary", ObservedBytes: count,
			Observation: &artifact.GrpcObservation{CompleteFrames: 1, RetainedFrames: 1, ObservedBytes: count, CountsComplete: true, EOF: true, End: "eof"},
			Messages:    []artifact.GrpcPayload{{Index: 0, Type: string(message.ProtoReflect().Descriptor().FullName()), DecodeState: "decoded", Wire: &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString(wire), OriginalBytes: int64(len(wire))}, Body: &artifact.Body{Encoding: "json", Text: string(projection)}, Observation: &artifact.GrpcPayloadObservation{WireBytes: uint64(len(wire)), StartSequence: sequence, CompleteSequence: sequence + 1, StartedOffsetNS: int64(sequence), CompletedOffsetNS: int64(sequence + 1)}}},
		}
	}
	req, res := side(&healthpb.HealthCheckRequest{Service: "inventory"}, 0), side(&healthpb.HealthCheckResponse{Status: status}, 2)
	res.HTTPStatus, res.StatusSource = 200, "trailers"
	capture := checkCapture(provider, artifact.Flow{Protocol: "grpc", Request: artifact.Message{GRPC: req}, Response: artifact.Message{GRPC: res}})
	capture.Schemas = []artifact.SchemaRef{schema}
	return capture
}

func TestCheckNativeGRPCUsesVerifiedWireAndV3Collections(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(fmt.Sprint(matching), func(t *testing.T) {
			status := healthpb.HealthCheckResponse_SERVING
			if !matching {
				status = healthpb.HealthCheckResponse_NOT_SERVING
			}
			opts := checkHTTPPair(t, []*artifact.Artifact{grpcCheckCapture(t, false, healthpb.HealthCheckResponse_SERVING)}, []*artifact.Artifact{grpcCheckCapture(t, true, status)})
			report := mustCheck(t, opts)
			if report.Compatible != matching || matching && report.Counts.Verified != 1 || !matching && report.Counts.Mismatched != 1 {
				t.Fatalf("native gRPC request/response contract comparison failed: %+v", report)
			}
		})
	}
}

func TestDifferencePathsPreservesLargeNumericMismatch(t *testing.T) {
	left := `{"body":{"value":{"id":9007199254740992}}}`
	right := `{"body":{"value":{"id":9007199254740993}}}`
	paths := differencePaths(left, right)
	encoded, _ := json.Marshal(paths)
	if string(encoded) != `["response/body/value/id"]` {
		t.Fatalf("large numeric mismatch lost its field location: %s", encoded)
	}
}
