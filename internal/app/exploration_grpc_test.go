package app

import (
	"context"
	"crypto/tls"
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
	"sync/atomic"
	"testing"
	"time"

	fixture "github.com/phrond/wiretap/examples/grpc-exploration/fixture"
	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/bundlereport"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/explore"
	"github.com/phrond/wiretap/internal/hooks"
	"github.com/phrond/wiretap/internal/project"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

type grpcExplorationFixture struct {
	*explorationFixture
	contract           *fixture.Contract
	state              *fixture.State
	server             *grpc.Server
	method, sourcePath string
	seedMessages       int
	mixed              bool
}

func newGRPCExplorationFixture(t *testing.T, method string, reflection, upstreamTLS, mixed bool, before ...func(context.Context) error) *grpcExplorationFixture {
	t.Helper()
	contract, err := fixture.Compile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state := &fixture.State{}
	control, a := dependencyCaptureRuntime(t)
	var captureDependency string
	deps := map[string]project.API{}
	if mixed {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"available":true}`)
		}))
		t.Cleanup(upstream.Close)
		optional := false
		deps["inventory"] = project.API{URL: upstream.URL, Kind: "mock", SchemaRequired: &optional}
		state.Before = func(ctx context.Context) error {
			req, _ := http.NewRequestWithContext(ctx, "GET", captureDependency+"/items", nil)
			incoming, _ := metadata.FromIncomingContext(ctx)
			for _, value := range incoming.Get("traceparent") {
				req.Header.Set("traceparent", value)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer res.Body.Close()
			_, err = io.Copy(io.Discard, res.Body)
			return err
		}
	}
	dependencyBefore := state.Before
	state.Before = func(ctx context.Context) error {
		for _, callback := range before {
			if err := callback(ctx); err != nil {
				return err
			}
		}
		if dependencyBefore != nil {
			return dependencyBefore(ctx)
		}
		return nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	api := project.API{ID: "orders", URL: "http://" + listener.Addr().String(), Protocol: "grpc", Kind: "service", Services: []string{fixture.Service}}
	var options []grpc.ServerOption
	if upstreamTLS {
		certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		certificate := certServer.TLS.Certificates[0]
		certServer.Close()
		caFile := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
			t.Fatal(err)
		}
		api.URL = "https://" + listener.Addr().String()
		api.UpstreamTLS = project.TLSConfig{CAFile: caFile}
		options = append(options, grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})))
	}
	server, err := contract.NewServer(state, reflection, options...)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	var sourcePath string
	if !reflection {
		sourcePath = filepath.Join(t.TempDir(), "orders.protoset")
		raw, err := proto.Marshal(contract.DescriptorSet)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sourcePath, raw, 0600); err != nil {
			t.Fatal(err)
		}
		api.GRPCSchema = &project.GRPCSchema{Source: "descriptor_set", Path: sourcePath}
	}
	a.project = &project.Config{SUT: api, Dependencies: deps}
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	captureURL := start["sut_url"].(string)
	captureDependency = captureURL + "/__wiretap/dep/inventory"
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "orders " + method})
	f := &grpcExplorationFixture{explorationFixture: &explorationFixture{app: a, url: control.URL, proxy: captureURL, session: setup["session_id"].(string)}, contract: contract, state: state, server: server, method: method, sourcePath: sourcePath, seedMessages: 1, mixed: mixed}
	conn, err := grpc.NewClient(strings.TrimPrefix(captureURL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	user, token := state.Reset()
	ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(t.Context(), "authorization", token), 5*time.Second)
	defer cancel()
	requests := []*dynamicpb.Message{contract.NewRequest(user, "widget", 2)}
	if method == "CreateOrders" || method == "ChatOrders" {
		requests = append(requests, contract.NewRequest(user, "gadget", 3))
		f.seedMessages = 2
	}
	if _, err := contract.Exercise(ctx, conn, method, requests); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.stopExplorations(ctx)
	})
	return f
}

func (f *grpcExplorationFixture) explore(t *testing.T, maxCases int, badAuth bool) explorationState {
	t.Helper()
	operation := fixture.ProcedureFor(f.method)
	var scopes []explore.HookScope
	for _, event := range []string{"before_explore", "before_case", "before_request", "check_case", "after_case", "after_explore"} {
		scopes = append(scopes, explore.HookScope{ID: event, Event: event, Operations: []string{operation}})
	}
	started := explorationPost(t, f.explorationFixture, "/v1/explore", exploreRequest{SessionID: f.session, Selection: explore.Selection{Operations: []string{operation}}, Config: explore.Config{MaxCases: maxCases, HookTimeout: "5s", RequestTimeout: "5s", HookScopes: scopes, Bindings: []explore.Binding{{Name: "user_id", In: "body", Path: "/user_id"}}}})
	return f.pump(t, explorationSelector{SessionID: f.session, ExplorationID: started.ID}, func(_ explorationAction, in explore.HookInput) (hooks.Output, string) {
		switch in.Event {
		case "before_case":
			user, token := f.state.Reset()
			return hooks.Output{Bindings: map[string]any{"user_id": user}, Headers: map[string]string{"Authorization": token}}, ""
		case "before_request":
			if in.GRPCRequest == nil || in.GRPCRequest.Procedure != operation || len(in.GRPCRequest.Messages) == 0 {
				t.Errorf("missing gRPC hook input: %+v", in)
			}
			if badAuth && in.Mode == "rejection" {
				return hooks.Output{Headers: map[string]string{"Authorization": "Bearer stale-token"}}, ""
			}
		case "check_case":
			expected := f.seedMessages
			if in.Mode == "rejection" {
				expected = 0
				if in.Mutation != nil && in.Mutation.MessageIndex != nil {
					expected = *in.Mutation.MessageIndex
				}
			}
			if badAuth && in.Mode == "rejection" {
				expected = 0
			}
			return hooks.Output{Checks: []hooks.Check{{Name: "only valid messages insert rows", Passed: f.state.Count() == expected}}}, ""
		case "after_case":
			f.state.Reset()
		}
		return hooks.Output{}, ""
	})
}

func TestGRPCExploreAllShapesReuseBundleHooksAndNativeCapture(t *testing.T) {
	for _, scenario := range []struct {
		method                 string
		reflection, tls, mixed bool
	}{
		{method: "CreateOrder"}, {method: "WatchOrders"}, {method: "CreateOrders"}, {method: "ChatOrders", mixed: true},
		{method: "CreateOrder", reflection: true, tls: true},
	} {
		name := scenario.method
		if scenario.tls {
			name += "_reflection_TLS"
		}
		t.Run(name, func(t *testing.T) {
			f := newGRPCExplorationFixture(t, scenario.method, scenario.reflection, scenario.tls, scenario.mixed)
			result := f.explore(t, 0, false)
			if result.State != "complete" || result.Result == nil || result.Result.Failed != 0 || result.Result.Generated < 4 || !result.Result.Complete {
				t.Fatalf("gRPC exploration: %+v, counts=%+v", result, result.Result)
			}
			root := f.app.currentBundle().Path()
			run, arts, err := bundle.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if run.RunState != "open" || len(run.Pending) != 1 || len(arts) != result.Result.Generated+1 {
				t.Fatalf("unexpected open run: %+v", run)
			}
			conditions := map[string]bool{}
			for _, art := range arts {
				if art.Exploration == nil || art.TestCase.Status != "pass" || art.CaptureIncomplete {
					t.Fatalf("generated evidence: %+v", art)
				}
				for _, target := range art.Exploration.Catalogue {
					conditions[target.Key] = true
				}
				var provider *artifact.Flow
				for i := range art.Traffic {
					if art.Traffic[i].Channel == "test_to_sut" {
						provider = &art.Traffic[i]
					}
				}
				if provider == nil || provider.Protocol != "grpc" || provider.Request.GRPC == nil || provider.Response.GRPC == nil || !artifact.GRPCDirectionComplete(provider.Request.GRPC, true) || !artifact.GRPCDirectionComplete(provider.Response.GRPC, true) {
					t.Fatalf("incomplete native evidence: %+v", provider)
				}
				if err := artifact.ValidateGRPCObservation(*provider); err != nil {
					t.Fatal(err)
				}
				if art.Exploration.Mode == "rejection" {
					if provider.Response.GRPC.StatusCode != 3 || !art.Exploration.RequestVerified || art.Exploration.Target.Protocol != "grpc" {
						t.Fatalf("missing rejection evidence: %+v", art.Exploration)
					}
				} else if scenario.method == "ChatOrders" {
					if provider.Response.GRPC.Messages[0].Observation.CompleteSequence >= provider.Request.GRPC.Messages[1].Observation.StartSequence {
						t.Fatal("bidirectional replay lost receive-before-send order")
					}
				}
				if scenario.mixed && art.Exploration.Mode == "baseline" {
					if len(art.Traffic) != f.seedMessages+1 {
						t.Fatalf("missing fresh dependencies: %+v", art.Traffic)
					}
					for _, dep := range art.Traffic {
						if dep.Channel == "sut_to_dependency" && (dep.ParentFlowID != provider.ID || dep.TraceID != provider.TraceID) {
							t.Fatalf("unlinked dependency: %+v", dep)
						}
					}
				}
				raw, _ := json.Marshal(art)
				if strings.Contains(string(raw), "Bearer example-token-") {
					t.Fatal("hook authentication leaked into evidence")
				}
			}
			parent := dependencyArtifact(t, postDependencyJSON(t, f.url, "/v1/teardownTest", map[string]any{"session_id": f.session, "status": "pass"}))
			if parent.TestCase.Status != "pass" || parent.Exploration != nil {
				t.Fatalf("parent changed: %+v", parent.TestCase)
			}
			postDependencyJSON(t, f.url, "/v1/finishRun", map[string]any{})
			f.server.Stop()
			if f.sourcePath != "" {
				if err := os.Remove(f.sourcePath); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := bundlereport.Refresh(root); err != nil {
				t.Fatalf("offline report: %v", err)
			}
			report, err := coverage.Generate(t.Context(), root, "Orders", true)
			if err != nil {
				t.Fatal(err)
			}
			if report.Contracts.Rejections == nil || report.Contracts.Rejections.Covered != len(conditions) || report.Contracts.Rejections.Failed != 0 {
				t.Fatalf("gRPC rejection coverage: %+v", report.Contracts.Rejections)
			}
		})
	}
}

func TestGRPCExploreAuthenticationFailureIsNotValidationCoverage(t *testing.T) {
	f := newGRPCExplorationFixture(t, "CreateOrder", false, false, false)
	result := f.explore(t, 1, true)
	if result.State != "failed" || result.Result == nil || result.Result.Failed != 1 {
		t.Fatalf("authentication was credited: %+v", result)
	}
	root := f.app.currentBundle().Path()
	parent := dependencyArtifact(t, postDependencyJSON(t, f.url, "/v1/teardownTest", map[string]any{"session_id": f.session, "status": "pass"}))
	if parent.TestCase.Status != "fail" {
		t.Fatal("ignored exploration failure passed parent")
	}
	postDependencyJSON(t, f.url, "/v1/finishRun", map[string]any{})
	report, err := coverage.Generate(t.Context(), root, "Orders", true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Contracts.Rejections == nil || report.Contracts.Rejections.Covered != 0 || report.Contracts.Rejections.Failed != 1 {
		t.Fatalf("authentication credited: %+v", report.Contracts.Rejections)
	}
}

func TestGRPCExploreRejectsUncapturableAggregateBeforeTransport(t *testing.T) {
	// Both messages individually fit; their native envelopes exceed the total
	// capture ceiling. No upstream or session state should be needed to reject it.
	payload := make([]byte, 8<<20)
	adapter := &explorationCapture{child: &runtimeSession{}}
	_, err := adapter.invokeGRPC(t.Context(), explore.GRPCRequest{Messages: [][]byte{payload, payload}, ResponseGates: []int{0, 0}})
	if err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatalf("uncapturable aggregate reached transport: %v", err)
	}
}

func TestGRPCExploreBlockedCallsCancelAndRunCleanup(t *testing.T) {
	for _, method := range []string{"CreateOrder", "ChatOrders"} {
		for _, mode := range []string{"cancel", "deadline"} {
			t.Run(method+"_"+mode, func(t *testing.T) {
				var blocked atomic.Bool
				entered, exited := make(chan struct{}, 1), make(chan struct{}, 1)
				f := newGRPCExplorationFixture(t, method, false, false, false, func(ctx context.Context) error {
					if !blocked.Load() {
						return nil
					}
					entered <- struct{}{}
					<-ctx.Done()
					exited <- struct{}{}
					return ctx.Err()
				})
				blocked.Store(true)
				var scopes []explore.HookScope
				for _, event := range []string{"before_case", "after_case", "after_explore"} {
					scopes = append(scopes, explore.HookScope{ID: event, Event: event})
				}
				requestTimeout := "5s"
				if mode == "deadline" {
					requestTimeout = "200ms"
				}
				started := explorationPost(t, f.explorationFixture, "/v1/explore", exploreRequest{
					SessionID: f.session, Selection: explore.Selection{Operations: []string{fixture.ProcedureFor(method)}},
					Config: explore.Config{MaxCases: 1, HookTimeout: "5s", RequestTimeout: requestTimeout, HookScopes: scopes,
						Bindings: []explore.Binding{{Name: "user_id", In: "body", Path: "/user_id"}}},
				})
				selector := explorationSelector{SessionID: f.session, ExplorationID: started.ID}
				cancelResult := make(chan error, 1)
				if mode == "cancel" {
					go func() {
						select {
						case <-entered:
						case <-time.After(5 * time.Second):
							cancelResult <- fmt.Errorf("generated call did not reach the SUT")
							return
						}
						data, _ := json.Marshal(selector)
						client := &http.Client{Timeout: 5 * time.Second}
						res, err := client.Post(f.url+"/v1/explore/cancel", "application/json", strings.NewReader(string(data)))
						if err == nil {
							defer res.Body.Close()
							_, _ = io.Copy(io.Discard, res.Body)
							if res.StatusCode != http.StatusOK {
								err = fmt.Errorf("cancel returned HTTP %d", res.StatusCode)
							}
						}
						cancelResult <- err
					}()
				}
				cleaned := map[string]int{}
				result := f.pump(t, selector, func(_ explorationAction, input explore.HookInput) (hooks.Output, string) {
					if input.Event == "before_case" {
						user, token := f.state.Reset()
						return hooks.Output{Bindings: map[string]any{"user_id": user}, Headers: map[string]string{"Authorization": token}}, ""
					}
					cleaned[input.Event]++
					return hooks.Output{}, ""
				})
				if mode == "cancel" {
					if err := <-cancelResult; err != nil {
						t.Fatal(err)
					}
					if result.State != "cancelled" {
						t.Fatalf("unexpected cancellation state: %+v", result)
					}
				} else if result.State != "failed" {
					t.Fatalf("unexpected deadline state: %+v", result)
				}
				select {
				case <-exited:
				case <-time.After(time.Second):
					t.Fatal("SUT call was left running after cancellation/deadline")
				}
				if cleaned["after_case"] != 1 || cleaned["after_explore"] != 1 || f.state.Count() != 0 {
					t.Fatalf("missing cleanup or side effect after cancellation: %+v", cleaned)
				}
				root := f.app.currentBundle().Path()
				parent := dependencyArtifact(t, postDependencyJSON(t, f.url, "/v1/teardownTest", map[string]any{"session_id": f.session, "status": "pass"}))
				if parent.TestCase.Status != "fail" {
					t.Fatal("parent ignored cancelled exploration")
				}
				postDependencyJSON(t, f.url, "/v1/finishRun", map[string]any{})
				_, arts, err := bundle.Load(root)
				if err != nil {
					t.Fatal(err)
				}
				for _, art := range arts {
					if art.Exploration != nil && (!art.CaptureIncomplete || art.TestCase.Status != "fail") {
						t.Fatalf("cancelled capture claimed complete/pass: %+v", art.TestCase)
					}
				}
			})
		}
	}
}
