package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	streaming "github.com/phrond/wiretap/examples/grpc-streaming/fixture"
	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/bundlereport"
	"github.com/phrond/wiretap/internal/core"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/project"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func streamContract(t *testing.T) *streaming.Contract {
	t.Helper()
	contract, err := streaming.Compile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return contract
}
func streamServer(t *testing.T, contract *streaming.Contract, before func(context.Context) error) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := contract.NewServer(before)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	return "http://" + listener.Addr().String()
}
func streamAPI(t *testing.T, url string) project.API {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stream.proto"), []byte(streaming.Source), 0600); err != nil {
		t.Fatal(err)
	}
	return project.API{ID: "streams", URL: url, Protocol: "grpc", Kind: "service", Services: []string{streaming.Service}, GRPCSchema: &project.GRPCSchema{Source: "proto", Files: []string{"stream.proto"}, ImportPaths: []string{dir}}}
}
func streamConnection(t *testing.T, url string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(strings.TrimPrefix(url, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
func streamFlow(t *testing.T, art artifact.Artifact) artifact.Flow {
	t.Helper()
	var grpcFlows []artifact.Flow
	for _, flow := range art.Traffic {
		if flow.Protocol == "grpc" {
			grpcFlows = append(grpcFlows, flow)
		}
	}
	if len(grpcFlows) != 1 {
		t.Fatalf("expected one gRPC flow, got %d", len(grpcFlows))
	}
	return grpcFlows[0]
}
func requireStreamCoverage(t *testing.T, root, method string, dependency, covered bool) {
	t.Helper()
	report, err := coverage.Generate(t.Context(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	group := report.Provided
	if dependency {
		group = report.Dependent
	}
	found := false
	for _, spec := range group.Specs {
		if spec.Type != "protobuf" {
			continue
		}
		for _, operation := range spec.Exercised {
			if operation.Operation == streaming.Procedure(method) {
				found = true
			}
		}
	}
	if found != covered {
		t.Fatalf("%s covered=%v, want %v; group=%+v", method, found, covered, group)
	}
}

func TestNativeGRPCAllShapesProviderConsumerAndMixed(t *testing.T) {
	for _, mode := range []string{"provider", "consumer", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			control, instance := dependencyCaptureRuntime(t)
			contract := streamContract(t)
			optional := false
			var target, captureDependency string
			switch mode {
			case "provider":
				target = streamServer(t, contract, nil)
				instance.project = &project.Config{SUT: streamAPI(t, target)}
			case "consumer":
				target = streamServer(t, contract, nil)
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := grpc.NewClient(strings.TrimPrefix(captureDependency, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
					if err != nil {
						http.Error(w, err.Error(), 502)
						return
					}
					defer conn.Close()
					_, err = contract.Exercise(r.Context(), conn, strings.TrimPrefix(r.URL.Path, "/"), 2)
					if err != nil {
						http.Error(w, err.Error(), 502)
						return
					}
					fmt.Fprint(w, "ok")
				}))
				defer provider.Close()
				dependency := streamAPI(t, target)
				dependency.ID = "stream-mock"
				dependency.Kind = "mock"
				instance.project = &project.Config{SUT: project.API{ID: "http-provider", URL: provider.URL, SchemaRequired: &optional}, Dependencies: map[string]project.API{"stream-mock": dependency}}
			case "mixed":
				mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true}`) }))
				defer mock.Close()
				target = streamServer(t, contract, func(ctx context.Context) error {
					req, _ := http.NewRequestWithContext(ctx, "GET", captureDependency+"/status", nil)
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						return err
					}
					defer res.Body.Close()
					_, err = io.Copy(io.Discard, res.Body)
					return err
				})
				instance.project = &project.Config{SUT: streamAPI(t, target), Dependencies: map[string]project.API{"http-mock": {URL: mock.URL, Kind: "mock", SchemaRequired: &optional}}}
			}
			started := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
			if mode == "consumer" {
				captureDependency = started["dependencies"].(map[string]any)["stream-mock"].(string)
			}
			if mode == "mixed" {
				captureDependency = started["sut_url"].(string) + "/__wiretap/dep/http-mock"
			}
			var root string
			for _, method := range []string{"Echo", "Watch", "Collect", "Chat"} {
				setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": mode + " " + method})
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				if mode == "consumer" {
					res, err := http.Get(setup["sut_url"].(string) + "/" + method)
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(res.Body)
					res.Body.Close()
					if res.StatusCode != 200 {
						t.Fatalf("HTTP %d %s", res.StatusCode, body)
					}
				} else {
					conn := streamConnection(t, setup["sut_url"].(string))
					output, err := contract.Exercise(ctx, conn, method, 2)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "provider" {
						direct, err := contract.Exercise(ctx, streamConnection(t, target), method, 2)
						if err != nil {
							t.Fatal(err)
						}
						if len(output) != len(direct) {
							t.Fatal("proxy changed result cardinality")
						}
						for i := range output {
							if !proto.Equal(output[i], direct[i]) {
								t.Fatal("proxy changed application result")
							}
						}
					}
				}
				cancel()
				finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
				art := dependencyArtifact(t, finished)
				flow := streamFlow(t, art)
				root = finished["bundle_path"].(string)
				if art.TestCase.Status != "pass" || flow.Response.GRPC.StatusCode != 0 || !artifact.GRPCDirectionComplete(flow.Request.GRPC, true) || !artifact.GRPCDirectionComplete(flow.Response.GRPC, true) {
					t.Fatalf("incomplete passing %s: %+v", method, art)
				}
				wantRequest, wantResponse := uint64(1), uint64(1)
				if method == "Collect" || method == "Chat" {
					wantRequest = 2
				}
				if method == "Watch" || method == "Chat" {
					wantResponse = 2
				}
				if flow.Request.GRPC.Observation.CompleteFrames != wantRequest || flow.Response.GRPC.Observation.CompleteFrames != wantResponse {
					t.Fatalf("%s cardinality request=%+v response=%+v", method, flow.Request.GRPC.Observation, flow.Response.GRPC.Observation)
				}
				if method == "Chat" && flow.Response.GRPC.Messages[0].Observation.CompleteSequence >= flow.Request.GRPC.Messages[1].Observation.StartSequence {
					t.Fatal("bidi response was not observed before next request")
				}
				if mode != "provider" && len(art.Traffic) != 2 {
					t.Fatalf("mixed boundary missing: %+v", art.Traffic)
				}
				run, captures, err := bundle.Load(root)
				if err != nil {
					t.Fatal(err)
				}
				if len(run.Schemas) == 0 || len(captures) == 0 {
					t.Fatal("bundle lost contracts or tests")
				}
				restored := streamFlow(t, *captures[len(captures)-1])
				if restored.Response.GRPC.Messages[0].Wire == nil || restored.Response.GRPC.Messages[0].Body == nil || restored.Response.GRPC.Messages[0].Observation == nil {
					t.Fatal("offline bundle lost indexed message payload or timing")
				}
				requireStreamCoverage(t, root, method, mode == "consumer", true)
			}
			postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
			// Frozen contracts and attachments must remain sufficient after source removal.
			for _, api := range append([]project.API{instance.project.SUT}, mapAPIs(instance.project.Dependencies)...) {
				if api.GRPCSchema != nil {
					if err := os.Remove(filepath.Join(api.GRPCSchema.ImportPaths[0], "stream.proto")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := bundlereport.Rebuild(root); err != nil {
				t.Fatal(err)
			}
			knowledge, err := os.ReadFile(filepath.Join(root, "docs/knowledge.json"))
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				APIs []struct {
					Operations []struct {
						Evidence []struct {
							Capture *struct {
								StreamKind string `json:"stream_kind"`
								Request    struct {
									Observation *artifact.GrpcObservation `json:"observation"`
								} `json:"request"`
								Response struct {
									Observation *artifact.GrpcObservation `json:"observation"`
								} `json:"response"`
								Events []artifact.GrpcEvent `json:"lifecycle_events"`
							} `json:"grpc_capture"`
						} `json:"evidence"`
					} `json:"operations"`
				} `json:"apis"`
			}
			if err := json.Unmarshal(knowledge, &document); err != nil {
				t.Fatal(err)
			}
			foundBidi := false
			for _, api := range document.APIs {
				for _, operation := range api.Operations {
					for _, evidence := range operation.Evidence {
						if capture := evidence.Capture; capture != nil && capture.StreamKind == "bidirectional" {
							foundBidi = true
							if capture.Request.Observation == nil || capture.Response.Observation == nil || capture.Request.Observation.CompleteFrames != 2 || capture.Response.Observation.CompleteFrames != 2 || len(capture.Events) == 0 {
								t.Fatalf("knowledge omitted bidi accounting/lifecycle: %+v", capture)
							}
						}
					}
				}
			}
			if !foundBidi {
				t.Fatal("knowledge omitted bidirectional evidence")
			}
		})
	}
}
func mapAPIs(apis map[string]project.API) []project.API {
	var result []project.API
	for _, api := range apis {
		result = append(result, api)
	}
	return result
}

func TestNativeGRPCEmptyStreamsMessagesAndGzip(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	contract := streamContract(t)
	instance.project = &project.Config{SUT: streamAPI(t, streamServer(t, contract, nil))}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	for _, method := range []string{"Echo", "Watch", "Collect", "Chat"} {
		t.Run(method, func(t *testing.T) {
			setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "empty " + method})
			conn := streamConnection(t, setup["sut_url"].(string))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var err error
			if method == "Echo" {
				err = conn.Invoke(ctx, streaming.Procedure(method), contract.NewMessage("", 0, nil), contract.NewMessage("", 0, nil))
			} else {
				_, err = contract.Exercise(ctx, conn, method, 0)
			}
			if err != nil {
				t.Fatal(err)
			}
			finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
			flow := streamFlow(t, dependencyArtifact(t, finished))
			if !artifact.GRPCDirectionComplete(flow.Request.GRPC, true) || !artifact.GRPCDirectionComplete(flow.Response.GRPC, true) {
				t.Fatalf("empty stream incomplete: %+v", flow)
			}
			if method == "Echo" {
				for _, direction := range []*artifact.GrpcMessage{flow.Request.GRPC, flow.Response.GRPC} {
					if len(direction.Messages) != 1 || direction.Messages[0].Observation.WireBytes != 0 || direction.Messages[0].Wire == nil {
						t.Fatal("zero-byte message lost")
					}
				}
			}
			if method == "Watch" && flow.Response.GRPC.Observation.CompleteFrames != 0 {
				t.Fatal("invented empty-stream response")
			}
			if (method == "Chat" || method == "Collect") && flow.Request.GRPC.Observation.CompleteFrames != 0 {
				t.Fatal("invented empty-stream request")
			}
			requireStreamCoverage(t, finished["bundle_path"].(string), method, false, true)
		})
	}
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "compressed bidi"})
	_, err := contract.Exercise(t.Context(), streamConnection(t, setup["sut_url"].(string)), "Chat", 2, grpc.UseCompressor("gzip"))
	if err != nil {
		t.Fatal(err)
	}
	finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	flow := streamFlow(t, dependencyArtifact(t, finished))
	for _, direction := range []*artifact.GrpcMessage{flow.Request.GRPC, flow.Response.GRPC} {
		for _, message := range direction.Messages {
			if !message.Compressed || message.Body == nil || message.DecodeError != "" {
				t.Fatalf("gzip stream decode: %+v", message)
			}
		}
	}
	if _, _, err := bundle.Load(finished["bundle_path"].(string)); err != nil {
		t.Fatal(err)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}

func TestNativeGRPCStreamOmissionsStayUncovered(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limit    int64
		count    int
		value    string
		retained uint64
	}{{"record limit", 4 << 20, 1050, "", 1024}, {"byte limit", 64, 20, "hello", 5}} {
		t.Run(tc.name, func(t *testing.T) {
			control, instance := dependencyCaptureRuntime(t)
			instance.cfg.Capture.MaxBodyBytes = tc.limit
			contract := streamContract(t)
			instance.project = &project.Config{SUT: streamAPI(t, streamServer(t, contract, nil))}
			postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
			setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": tc.name})
			conn := streamConnection(t, setup["sut_url"].(string))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			stream, err := conn.NewStream(ctx, streaming.StreamDescription("Watch"), streaming.Procedure("Watch"))
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.SendMsg(contract.NewMessage(tc.value, int32(tc.count), nil)); err != nil {
				t.Fatal(err)
			}
			if err := stream.CloseSend(); err != nil {
				t.Fatal(err)
			}
			got := 0
			for {
				err := stream.RecvMsg(contract.NewMessage("", 0, nil))
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				got++
			}
			if got != tc.count {
				t.Fatal("retention budget changed forwarded messages")
			}
			finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
			flow := streamFlow(t, dependencyArtifact(t, finished))
			observation := flow.Response.GRPC.Observation
			if observation == nil || observation.CompleteFrames != uint64(tc.count) || observation.RetainedFrames != tc.retained || observation.OmittedFrames != uint64(tc.count)-tc.retained || !observation.CountsComplete || !observation.EOF || observation.Omitted == nil || observation.Omitted.FirstIndex != tc.retained {
				t.Fatalf("incorrect omission accounting: %+v", observation)
			}
			if _, _, err := bundle.Load(finished["bundle_path"].(string)); err != nil {
				t.Fatal(err)
			}
			requireStreamCoverage(t, finished["bundle_path"].(string), "Watch", false, false)
			postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
		})
	}
}

func TestNativeGRPCBidiDrainSnapshotIsImmutable(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	contract := streamContract(t)
	instance.project = &project.Config{SUT: streamAPI(t, streamServer(t, contract, nil))}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "active bidi"})
	conn := streamConnection(t, setup["sut_url"].(string))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := conn.NewStream(ctx, streaming.StreamDescription("Chat"), streaming.Procedure("Chat"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SendMsg(contract.NewMessage("before snapshot", 0, nil)); err != nil {
		t.Fatal(err)
	}
	if err := stream.RecvMsg(contract.NewMessage("", 0, nil)); err != nil {
		t.Fatal(err)
	}
	finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass", "drain_timeout_ms": 0})
	art := dependencyArtifact(t, finished)
	flow := streamFlow(t, art)
	if !art.CaptureIncomplete || flow.Request.GRPC.Observation.EOF || flow.Response.GRPC.Observation.EOF || flow.Request.GRPC.Observation.CompleteFrames != 1 || flow.Response.GRPC.Observation.CompleteFrames != 1 {
		t.Fatalf("invalid active snapshot: %+v", flow)
	}
	path := finished["attempt_path"].(string)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := http.Post(control.URL+"/v1/setupTest", "application/json", strings.NewReader(`{"name":"unsafe untraced next test"}`))
	if err != nil {
		t.Fatal(err)
	}
	blocked.Body.Close()
	if blocked.StatusCode != http.StatusConflict {
		t.Fatal("retired stream allowed ambiguous serial ownership")
	}
	nextTrace := "00-33333333333333333333333333333333-4444444444444444-01"
	next := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "next test", "explicit_session": true, "trace_context": map[string]string{"traceparent": nextTrace}})
	nextContext := metadata.NewOutgoingContext(ctx, metadata.Pairs("traceparent", nextTrace))
	if _, err := contract.Exercise(nextContext, conn, "Echo", 1); err != nil {
		t.Fatal(err)
	}
	nextFinished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": next["session_id"], "status": "pass"})
	if nextArt := dependencyArtifact(t, nextFinished); len(nextArt.Traffic) != 1 || streamFlow(t, nextArt).Request.GRPC.Method != "Echo" {
		t.Fatal("old stream leaked into next test")
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	if err := stream.RecvMsg(contract.NewMessage("", 0, nil)); err == nil {
		t.Fatal("finish did not terminate retired stream")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("retired stream mutated committed attempt")
	}
	requireStreamCoverage(t, finished["bundle_path"].(string), "Chat", false, false)
}

func TestNativeGRPCStreamCancellationStaysUncovered(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	contract := streamContract(t)
	instance.project = &project.Config{SUT: streamAPI(t, streamServer(t, contract, nil))}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "cancel watch"})
	conn := streamConnection(t, setup["sut_url"].(string))
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := conn.NewStream(ctx, streaming.StreamDescription("Watch"), streaming.Procedure("Watch"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SendMsg(contract.NewMessage("waiting", -1, nil)); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if err := stream.RecvMsg(contract.NewMessage("", 0, nil)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := stream.RecvMsg(contract.NewMessage("", 0, nil)); status.Code(err) != codes.Canceled {
		t.Fatalf("cancel status=%v", err)
	}
	finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	flow := streamFlow(t, dependencyArtifact(t, finished))
	if flow.TransportError == "" && artifact.GRPCDirectionComplete(flow.Response.GRPC, true) {
		t.Fatal("cancellation claimed complete provider response")
	}
	requireStreamCoverage(t, finished["bundle_path"].(string), "Watch", false, false)
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}

func TestNativeGRPCParallelStreamsUseW3CThroughDependency(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	contract := streamContract(t)
	dependency := streamAPI(t, streamServer(t, contract, nil))
	dependency.ID = "dependency"
	dependency.Kind = "mock"
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var dependencyConn *grpc.ClientConn
	provider := streamServer(t, contract, func(ctx context.Context) error {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		incoming, _ := metadata.FromIncomingContext(ctx)
		outgoing := metadata.NewOutgoingContext(ctx, metadata.Pairs("traceparent", incoming.Get("traceparent")[0]))
		_, err := contract.Exercise(outgoing, dependencyConn, "Echo", 1)
		return err
	})
	instance.project = &project.Config{SUT: streamAPI(t, provider), Dependencies: map[string]project.API{"dependency": dependency}}
	started := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	dependencyConn = streamConnection(t, started["dependencies"].(map[string]any)["dependency"].(string))
	traces := []string{strings.Repeat("1", 32), strings.Repeat("2", 32)}
	var sessions []map[string]any
	for i, trace := range traces {
		sessions = append(sessions, postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": fmt.Sprintf("parallel stream %d", i), "explicit_session": true, "trace_context": map[string]string{"traceparent": "00-" + trace + "-3333333333333333-01"}}))
	}
	conn := streamConnection(t, sessions[0]["sut_url"].(string))
	results := make(chan error, 2)
	for _, trace := range traces {
		go func(trace string) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("traceparent", "00-"+trace+"-4444444444444444-01"))
			_, err := contract.Exercise(ctx, conn, "Chat", 2)
			results <- err
		}(trace)
	}
	for range traces {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("streams did not overlap")
		}
	}
	close(release)
	for range traces {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for i, session := range sessions {
		finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
		art := dependencyArtifact(t, finished)
		if len(art.Traffic) != 2 {
			t.Fatalf("lost provider/dependency stream: %+v", art.Traffic)
		}
		for _, flow := range art.Traffic {
			if flow.TraceID != traces[i] || flow.TestCorrelation != "trace" || !artifact.GRPCDirectionComplete(flow.Request.GRPC, true) || !artifact.GRPCDirectionComplete(flow.Response.GRPC, true) {
				t.Fatalf("wrong stream ownership or completion: %+v", flow)
			}
		}
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}

func TestNativeGRPCStreamingTLS(t *testing.T) {
	contract := streamContract(t)
	upstream := httptest.NewUnstartedServer(contract.NewServer(nil))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	certificate := upstream.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "grpc.crt"), filepath.Join(dir, "grpc.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	api := streamAPI(t, upstream.URL)
	api.UpstreamTLS = project.TLSConfig{CAFile: certPath}
	api.CaptureTLS = project.TLSConfig{CertFile: certPath, KeyFile: keyPath}
	cfg := core.DefaultConfig()
	cfg.Proxy.ListenAddr = "127.0.0.1:0"
	cfg.Artifact.OutputDir = t.TempDir()
	instance, err := New(Deps{Config: cfg, Project: &project.Config{SUT: api}})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(instance.routes())
	defer control.Close()
	defer func() { _ = instance.proxyComponent.Stop(t.Context()) }()
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	var conn *grpc.ClientConn
	for _, method := range []string{"Echo", "Watch", "Collect", "Chat"} {
		setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "TLS " + method})
		if conn == nil {
			endpoint := setup["sut_url"].(string)
			if !strings.HasPrefix(endpoint, "https://") {
				t.Fatalf("missing TLS capture endpoint: %s", endpoint)
			}
			conn, err = grpc.NewClient(strings.TrimPrefix(endpoint, "https://"), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool})))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, err := contract.Exercise(ctx, conn, method, 2)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
		flow := streamFlow(t, dependencyArtifact(t, finished))
		if !artifact.GRPCDirectionComplete(flow.Request.GRPC, true) || !artifact.GRPCDirectionComplete(flow.Response.GRPC, true) {
			t.Fatalf("TLS stream incomplete: %+v", flow)
		}
		requireStreamCoverage(t, finished["bundle_path"].(string), method, false, true)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}
