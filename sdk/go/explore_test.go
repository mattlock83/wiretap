package wiretap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func exploreTestServer(t *testing.T, handler func(string, map[string]any) any) *Session {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		response := handler(r.URL.Path, input)
		if reply, ok := response.(exploreTestHTTPResponse); ok {
			w.WriteHeader(reply.status)
			response = reply.body
		}
		json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return &Session{ID: "parent", runtime: &Runtime{BaseURL: server.URL}}
}

type exploreTestHTTPResponse struct {
	status int
	body   any
}

func exploreAction(id, hook string, event ExploreEvent, input ExploreHookInput) ExplorationResponse {
	input.Event = event
	return ExplorationResponse{State: "running", Action: &ExplorationAction{ID: id, HookID: hook, Event: event, Input: input}}
}

func TestExploreScopedCallbacksAndTraceIsolation(t *testing.T) {
	var start map[string]any
	var replies []map[string]any
	generatedTrace := strings.Repeat("a", 32)
	steps := []ExplorationResponse{
		exploreAction("a", "hook-1", BeforeCase, ExploreHookInput{Operation: "POST /orders", TraceContext: map[string]string{"traceparent": "00-" + generatedTrace + "-" + strings.Repeat("b", 16) + "-01"}}),
		exploreAction("b", "hook-2", AfterCase, ExploreHookInput{}),
		{State: "complete", Result: &ExplorationResult{Generated: 2, Executed: 2, Passed: 2, BundlePath: "/run/bundle"}},
	}
	session := exploreTestServer(t, func(path string, input map[string]any) any {
		switch path {
		case "/v1/explore":
			start = input
			return ExplorationResponse{ExplorationID: "e", State: "running"}
		case "/v1/explore/next":
			next := steps[0]
			steps = steps[1:]
			return next
		case "/v1/explore/respond":
			replies = append(replies, input)
		}
		return map[string]any{}
	})
	parent := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{"traceparent": "00-" + strings.Repeat("c", 32) + "-" + strings.Repeat("d", 16) + "-01"})
	result, err := session.Explore(parent, ExploreOptions{Operations: []string{"POST /orders"}, Hooks: []ExploreHook{
		{Event: BeforeCase, Operations: []string{"POST /orders"}, Handler: func(ctx context.Context, input ExploreHookInput) (ExploreHookOutput, error) {
			if input.Operation != "POST /orders" || trace.SpanContextFromContext(ctx).TraceID().String() != generatedTrace {
				return ExploreHookOutput{}, errors.New("wrong callback operation or trace")
			}
			return ExploreHookOutput{Headers: map[string]string{"Authorization": "fresh"}}, nil
		}},
		{Event: AfterCase, Handler: func(ctx context.Context, input ExploreHookInput) (ExploreHookOutput, error) {
			if trace.SpanContextFromContext(ctx).IsValid() {
				return ExploreHookOutput{}, errors.New("missing generated context inherited parent trace")
			}
			return ExploreHookOutput{State: map[string]any{"cleaned": true}}, nil
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed != 2 || result.Failed != 0 || result.BundlePath != "/run/bundle" {
		t.Fatalf("wrong result: %+v", result)
	}
	if _, ok := start["max_cases"]; ok {
		t.Fatal("default max_cases must not be sent")
	}
	if start["session_id"] != "parent" {
		t.Fatalf("wrong session: %v", start)
	}
	want := []any{map[string]any{"id": "hook-1", "event": "before_case", "operations": []any{"POST /orders"}}, map[string]any{"id": "hook-2", "event": "after_case"}}
	if !reflect.DeepEqual(start["hooks"], want) {
		t.Fatalf("wrong descriptors: %#v", start["hooks"])
	}
	if len(replies) != 2 || replies[0]["output"].(map[string]any)["headers"].(map[string]any)["Authorization"] != "fresh" {
		t.Fatalf("wrong replies: %v", replies)
	}
	if trace.SpanContextFromContext(parent).TraceID().String() != strings.Repeat("c", 32) {
		t.Fatal("parent context changed")
	}
}

func TestExploreHookFailurePumpsCleanup(t *testing.T) {
	var replies []map[string]any
	var cleanup []ExploreEvent
	steps := []ExplorationResponse{
		exploreAction("a", "hook-1", CheckCase, ExploreHookInput{}),
		exploreAction("b", "hook-2", AfterCase, ExploreHookInput{}),
		exploreAction("c", "hook-3", AfterExplore, ExploreHookInput{}),
		{State: "failed", Result: &ExplorationResult{Failed: 1}, Error: "hook failed"},
	}
	session := exploreTestServer(t, func(path string, input map[string]any) any {
		switch path {
		case "/v1/explore":
			return ExplorationResponse{ExplorationID: "e", State: "running"}
		case "/v1/explore/next":
			next := steps[0]
			steps = steps[1:]
			return next
		case "/v1/explore/respond":
			replies = append(replies, input)
		}
		return map[string]any{}
	})
	clean := func(_ context.Context, in ExploreHookInput) (ExploreHookOutput, error) {
		cleanup = append(cleanup, in.Event)
		return ExploreHookOutput{}, nil
	}
	_, err := session.Explore(context.Background(), ExploreOptions{Hooks: []ExploreHook{
		{Event: CheckCase, Handler: func(context.Context, ExploreHookInput) (ExploreHookOutput, error) { panic("unexpected database write") }},
		{Event: AfterCase, Handler: clean}, {Event: AfterExplore, Handler: clean},
	}})
	var explorationError *ExplorationError
	if !errors.As(err, &explorationError) || explorationError.Result.Failed != 1 || !strings.Contains(err.Error(), "unexpected database write") {
		t.Fatalf("wrong error: %v", err)
	}
	if !reflect.DeepEqual(cleanup, []ExploreEvent{AfterCase, AfterExplore}) {
		t.Fatalf("missing cleanup: %v", cleanup)
	}
	if !strings.Contains(replies[0]["error"].(string), "unexpected database write") {
		t.Fatalf("failure missing from server reply: %v", replies)
	}
}

func TestExploreCancellationHasFreshCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	cancelled, cleanupSent := false, false
	session := exploreTestServer(t, func(path string, input map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		switch path {
		case "/v1/explore":
			return ExplorationResponse{ExplorationID: "e", State: "running"}
		case "/v1/explore/cancel":
			cancelled = true
		case "/v1/explore/next":
			if !cancelled {
				return exploreAction("a", "hook-1", BeforeCase, ExploreHookInput{})
			}
			if !cleanupSent {
				cleanupSent = true
				return exploreAction("b", "hook-2", AfterCase, ExploreHookInput{})
			}
			return ExplorationResponse{State: "cancelled", Result: &ExplorationResult{}}
		}
		return map[string]any{}
	})
	cleaned := false
	_, err := session.Explore(ctx, ExploreOptions{HookTimeout: time.Second, Hooks: []ExploreHook{
		{Event: BeforeCase, Handler: func(ctx context.Context, _ ExploreHookInput) (ExploreHookOutput, error) {
			cancel()
			<-ctx.Done()
			return ExploreHookOutput{}, ctx.Err()
		}},
		{Event: AfterCase, Handler: func(ctx context.Context, _ ExploreHookInput) (ExploreHookOutput, error) {
			if ctx.Err() != nil {
				return ExploreHookOutput{}, ctx.Err()
			}
			cleaned = true
			return ExploreHookOutput{}, nil
		}},
	}})
	if !errors.Is(err, context.Canceled) || !cleaned {
		t.Fatalf("cancel=%v cleaned=%v", err, cleaned)
	}
}

func TestBoundClientHonoursGeneratedCaseContext(t *testing.T) {
	var captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { captured = r.Header.Get("Traceparent") }))
	defer server.Close()
	ctx := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{"traceparent": "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01"})
	parent := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{"traceparent": "00-" + strings.Repeat("c", 32) + "-" + strings.Repeat("d", 16) + "-01"})
	session := Session{Context: parent}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	response, err := session.HTTPClient(nil).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !strings.Contains(captured, strings.Repeat("a", 32)) {
		t.Fatalf("generated context overwritten by parent: %s", captured)
	}
}

func TestExploreFrameworkCleanupCancelsAndWaits(t *testing.T) {
	ready := make(chan struct{})
	var mu sync.Mutex
	cancelled, cleanupSent := false, false
	session := exploreTestServer(t, func(path string, input map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		switch path {
		case "/v1/explore":
			return ExplorationResponse{ExplorationID: "e", State: "running"}
		case "/v1/explore/cancel":
			cancelled = true
		case "/v1/explore/next":
			if !cancelled {
				return exploreAction("a", "hook-1", BeforeCase, ExploreHookInput{})
			}
			if !cleanupSent {
				cleanupSent = true
				return exploreAction("b", "hook-2", AfterCase, ExploreHookInput{})
			}
			return ExplorationResponse{State: "cancelled", Result: &ExplorationResult{}}
		}
		return map[string]any{}
	})
	finished := make(chan error, 1)
	cleaned := make(chan struct{})
	go func() {
		_, err := session.Explore(context.Background(), ExploreOptions{Hooks: []ExploreHook{
			{Event: BeforeCase, Handler: func(ctx context.Context, _ ExploreHookInput) (ExploreHookOutput, error) {
				close(ready)
				<-ctx.Done()
				return ExploreHookOutput{}, ctx.Err()
			}},
			{Event: AfterCase, Handler: func(ctx context.Context, _ ExploreHookInput) (ExploreHookOutput, error) {
				close(cleaned)
				return ExploreHookOutput{}, nil
			}},
		}})
		finished <- err
	}()
	<-ready
	if err := session.stopExploration(); err == nil || !strings.Contains(err.Error(), "ended before exploration completed") {
		t.Fatalf("unfinished exploration did not fail cleanup: %v", err)
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("framework cleanup returned before after_case")
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel not propagated: %v", err)
	}
	if _, err := session.Explore(context.Background(), ExploreOptions{}); err == nil {
		t.Fatal("closed parent accepted new exploration")
	}
}

func TestExploreCancellationSkipsStaleSetupAndExpiredCleanupReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := false
	var events []string
	steps := []ExplorationResponse{
		exploreAction("stale", "hook-2", BeforeRequest, ExploreHookInput{}),
		exploreAction("cleanup", "hook-3", AfterCase, ExploreHookInput{}),
		exploreAction("finish", "hook-4", AfterExplore, ExploreHookInput{}),
		{State: "cancelled", Result: &ExplorationResult{}},
	}
	session := exploreTestServer(t, func(path string, input map[string]any) any {
		switch path {
		case "/v1/explore":
			return ExplorationResponse{ExplorationID: "e", State: "running"}
		case "/v1/explore/cancel":
			cancelled = true
		case "/v1/explore/next":
			if !cancelled {
				return exploreAction("setup", "hook-1", BeforeCase, ExploreHookInput{})
			}
			next := steps[0]
			steps = steps[1:]
			return next
		case "/v1/explore/respond":
			if input["action_id"] == "cleanup" {
				return exploreTestHTTPResponse{status: 409, body: map[string]string{"error": "hook action is no longer pending"}}
			}
		}
		return map[string]any{}
	})
	_, err := session.Explore(ctx, ExploreOptions{Hooks: []ExploreHook{
		{Event: BeforeCase, Handler: func(context.Context, ExploreHookInput) (ExploreHookOutput, error) {
			cancel()
			return ExploreHookOutput{}, context.Canceled
		}},
		{Event: BeforeRequest, Handler: func(context.Context, ExploreHookInput) (ExploreHookOutput, error) {
			events = append(events, "stale request")
			return ExploreHookOutput{}, nil
		}},
		{Event: AfterCase, Handler: func(context.Context, ExploreHookInput) (ExploreHookOutput, error) {
			events = append(events, "case cleanup")
			return ExploreHookOutput{}, nil
		}},
		{Event: AfterExplore, Handler: func(context.Context, ExploreHookInput) (ExploreHookOutput, error) {
			events = append(events, "explore cleanup")
			return ExploreHookOutput{}, nil
		}},
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel not propagated: %v", err)
	}
	if !reflect.DeepEqual(events, []string{"case cleanup", "explore cleanup"}) {
		t.Fatalf("wrong dispatch: %v", events)
	}
}

func TestExploreGRPCOptionsAndMessagesUseSameHTTPProtocol(t *testing.T) {
	var start map[string]any
	procedure := "/orders.v1.Orders/CreateOrder"
	steps := []ExplorationResponse{
		exploreAction("grpc", "hook-1", BeforeRequest, ExploreHookInput{Operation: procedure, GRPCRequest: &ExploreGRPCRequest{Procedure: procedure, Metadata: http.Header{"authorization": []string{"old"}}, Messages: []json.RawMessage{json.RawMessage(`{"quantity":0}`)}, MessagesBase64: []string{"EAA="}, ResponseGates: []int{0}}}),
		{State: "complete", Result: &ExplorationResult{Complete: true}},
	}
	session := exploreTestServer(t, func(path string, input map[string]any) any {
		switch path {
		case "/v1/explore":
			start = input
			return ExplorationResponse{ExplorationID: "e", State: "running"}
		case "/v1/explore/next":
			next := steps[0]
			steps = steps[1:]
			return next
		}
		return map[string]any{}
	})
	_, err := session.Explore(t.Context(), ExploreOptions{Operations: []string{procedure}, ExpectedGRPCStatuses: []int{3}, Bindings: []ExploreBinding{{Name: "user", In: "body", Path: "/user_id", MessageIndex: new(int)}}, Hooks: []ExploreHook{{Event: BeforeRequest, Operations: []string{procedure}, Handler: func(ctx context.Context, input ExploreHookInput) (ExploreHookOutput, error) {
		if input.GRPCRequest == nil || input.GRPCRequest.Procedure != procedure || string(input.GRPCRequest.Messages[0]) != `{"quantity":0}` {
			return ExploreHookOutput{}, errors.New("gRPC hook payload changed")
		}
		return ExploreHookOutput{Headers: map[string]string{"authorization": "fresh"}}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(start["expected_grpc_statuses"], []any{float64(3)}) {
		t.Fatalf("missing gRPC status expectation: %v", start)
	}
	if start["bindings"].([]any)[0].(map[string]any)["message_index"] != float64(0) {
		t.Fatalf("zero message index missing: %v", start)
	}
}
