package wiretap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// ExploreEvent is one callback boundary inside an exploration invocation.
type ExploreEvent string

const (
	BeforeExplore ExploreEvent = "before_explore"
	BeforeCase    ExploreEvent = "before_case"
	BeforeRequest ExploreEvent = "before_request"
	CheckCase     ExploreEvent = "check_case"
	AfterCase     ExploreEvent = "after_case"
	AfterExplore  ExploreEvent = "after_explore"
)

type ExploreBinding struct {
	Name   string `json:"name"`
	FlowID string `json:"flow_id,omitempty"`
	In     string `json:"in"`
	Path   string `json:"path"`
	// MessageIndex selects a gRPC message. Omitted bindings apply to all
	// request messages; omitted extractions read response message zero.
	MessageIndex *int `json:"message_index,omitempty"`
}

type ExploreCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

type ExploreHookOutput struct {
	Bindings map[string]any    `json:"bindings,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	State    map[string]any    `json:"state,omitempty"`
	Checks   []ExploreCheck    `json:"checks,omitempty"`
}

type ExploreRequest struct {
	Method     string          `json:"method"`
	URL        string          `json:"url"`
	Headers    http.Header     `json:"headers"`
	Body       json.RawMessage `json:"body,omitempty"`
	BodyBase64 string          `json:"body_base64,omitempty"`
}

type ExploreGRPCRequest struct {
	Procedure      string            `json:"procedure"`
	Metadata       http.Header       `json:"metadata"`
	Messages       []json.RawMessage `json:"messages"`
	MessagesBase64 []string          `json:"messages_base64"`
	ResponseGates  []int             `json:"response_gates,omitempty"`
}

// ExploreHookInput carries runtime values, final request data and captured
// exchanges. Runtime fields use JSON names so raw API clients see the same data.
type ExploreHookInput struct {
	HookID          string              `json:"hook_id,omitempty"`
	InvocationID    string              `json:"invocation_id"`
	ParentAttemptID string              `json:"parent_attempt_id"`
	Version         int                 `json:"version"`
	Event           ExploreEvent        `json:"event"`
	RunID           string              `json:"run_id"`
	CaseID          string              `json:"case_id,omitempty"`
	Scenario        string              `json:"scenario"`
	Mode            string              `json:"mode,omitempty"`
	Operation       string              `json:"operation,omitempty"`
	Operations      []string            `json:"operations,omitempty"`
	FlowID          string              `json:"flow_id,omitempty"`
	Mutation        map[string]any      `json:"mutation,omitempty"`
	Request         *ExploreRequest     `json:"request,omitempty"`
	GRPCRequest     *ExploreGRPCRequest `json:"grpc_request,omitempty"`
	Bindings        map[string]any      `json:"bindings"`
	State           map[string]any      `json:"state"`
	Headers         map[string]string   `json:"headers"`
	Exchanges       []map[string]any    `json:"exchanges,omitempty"`
	Outcome         string              `json:"outcome,omitempty"`
	Error           string              `json:"error,omitempty"`
	TraceContext    map[string]string   `json:"trace_context,omitempty"`
}

type ExploreHook struct {
	Event ExploreEvent
	// Operations contains exact spec selectors, for example POST /orders/{id}
	// or /orders.v1.Orders/CreateOrder for native gRPC.
	// Empty means every operation selected by this exploration.
	Operations []string
	Handler    func(context.Context, ExploreHookInput) (ExploreHookOutput, error)
}

type ExploreOptions struct {
	Operations []string
	FlowID     string
	// MaxCases zero runs all supported cases; there is no implicit case cap.
	MaxCases             int
	RequestTimeout       time.Duration
	HookTimeout          time.Duration
	CleanupTimeout       time.Duration
	Hooks                []ExploreHook
	Bindings             []ExploreBinding
	Extract              []ExploreBinding
	ExpectedStatuses     []int
	ExpectedGRPCStatuses []int
	Redactions           []Redaction
}

// ExplorationHookDescriptor and the request/response types expose the HTTP
// protocol for callers that need their own callback dispatcher.
type ExplorationHookDescriptor struct {
	ID         string       `json:"id"`
	Event      ExploreEvent `json:"event"`
	Operations []string     `json:"operations,omitempty"`
}

type StartExplorationRequest struct {
	SessionID            string                      `json:"session_id"`
	Operations           []string                    `json:"operations,omitempty"`
	FlowID               string                      `json:"flow_id,omitempty"`
	MaxCases             int                         `json:"max_cases,omitempty"`
	RequestTimeout       string                      `json:"request_timeout,omitempty"`
	HookTimeout          string                      `json:"hook_timeout,omitempty"`
	Hooks                []ExplorationHookDescriptor `json:"hooks,omitempty"`
	Bindings             []ExploreBinding            `json:"bindings,omitempty"`
	Extract              []ExploreBinding            `json:"extract,omitempty"`
	ExpectedStatuses     []int                       `json:"expected_statuses,omitempty"`
	ExpectedGRPCStatuses []int                       `json:"expected_grpc_statuses,omitempty"`
	Redactions           []Redaction                 `json:"redactions,omitempty"`
}

type ExplorationReference struct {
	SessionID     string `json:"session_id"`
	ExplorationID string `json:"exploration_id"`
}

type ExplorationAction struct {
	ID     string           `json:"id"`
	HookID string           `json:"hook_id"`
	Event  ExploreEvent     `json:"event"`
	Input  ExploreHookInput `json:"input"`
}

type ExplorationResult struct {
	Generated  int      `json:"generated"`
	Executed   int      `json:"executed"`
	Passed     int      `json:"passed"`
	Failed     int      `json:"failed"`
	Baselines  int      `json:"baselines"`
	Skipped    int      `json:"skipped"`
	Complete   bool     `json:"complete"`
	Notes      []string `json:"notes,omitempty"`
	AttemptIDs []string `json:"attempt_ids,omitempty"`
	BundlePath string   `json:"bundle_path,omitempty"`
}

type ExplorationResponse struct {
	ExplorationID string             `json:"exploration_id"`
	State         string             `json:"state"`
	Action        *ExplorationAction `json:"action,omitempty"`
	Result        *ExplorationResult `json:"result,omitempty"`
	Error         string             `json:"error,omitempty"`
}

type RespondExplorationRequest struct {
	ExplorationReference
	ActionID string             `json:"action_id"`
	Output   *ExploreHookOutput `json:"output,omitempty"`
	Error    string             `json:"error,omitempty"`
}

type ExplorationError struct {
	Result *ExplorationResult
	Cause  error
}

func (e *ExplorationError) Error() string { return "Wiretap exploration: " + e.Cause.Error() }
func (e *ExplorationError) Unwrap() error { return e.Cause }

func (r *Runtime) StartExploration(ctx context.Context, request StartExplorationRequest) (ExplorationResponse, error) {
	var response ExplorationResponse
	err := r.call(ctx, "/v1/explore", request, &response)
	return response, err
}
func (r *Runtime) NextExploration(ctx context.Context, request ExplorationReference) (ExplorationResponse, error) {
	var response ExplorationResponse
	err := r.call(ctx, "/v1/explore/next", request, &response)
	return response, err
}
func (r *Runtime) RespondExploration(ctx context.Context, request RespondExplorationRequest) error {
	return r.call(ctx, "/v1/explore/respond", request, nil)
}
func (r *Runtime) CancelExploration(ctx context.Context, request ExplorationReference) error {
	return r.call(ctx, "/v1/explore/cancel", request, nil)
}

// Explore runs generated checks within the current test. Return hook errors
// rather than calling t.Fatal in callbacks. Callbacks must observe their context
// so cancellation can stop work before cleanup hooks run.
func (s *Session) Explore(ctx context.Context, options ExploreOptions) (*ExplorationResult, error) {
	if s.runtime == nil || s.ID == "" {
		return nil, errors.New("explore requires a session returned by StartTest")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.exploreMu.Lock()
	if s.closed || s.exploreCancel != nil {
		s.exploreMu.Unlock()
		return nil, errors.New("capture test is closed or already exploring")
	}
	ctx, cancelExplore := context.WithCancel(ctx)
	s.exploreCancel, s.exploreDone = cancelExplore, make(chan struct{})
	s.exploreCleanupTimeout = options.CleanupTimeout
	s.exploreMu.Unlock()
	defer func() {
		cancelExplore()
		s.exploreMu.Lock()
		close(s.exploreDone)
		s.exploreCancel = nil
		s.exploreMu.Unlock()
	}()
	if options.MaxCases < 0 || options.RequestTimeout < 0 || options.HookTimeout < 0 || options.CleanupTimeout < 0 {
		return nil, errors.New("exploration limits must not be negative")
	}
	hookTimeout, cleanupTimeout := options.HookTimeout, options.CleanupTimeout
	if hookTimeout == 0 {
		hookTimeout = 30 * time.Second
	}
	if cleanupTimeout == 0 {
		cleanupTimeout = 30 * time.Second
	}
	request := StartExplorationRequest{SessionID: s.ID, Operations: options.Operations, FlowID: options.FlowID, MaxCases: options.MaxCases, HookTimeout: hookTimeout.String(), Bindings: options.Bindings, Extract: options.Extract, ExpectedStatuses: options.ExpectedStatuses, ExpectedGRPCStatuses: options.ExpectedGRPCStatuses, Redactions: options.Redactions}
	if options.RequestTimeout > 0 {
		request.RequestTimeout = options.RequestTimeout.String()
	}
	callbacks := make(map[string]ExploreHook)
	for index, hook := range options.Hooks {
		if hook.Handler == nil {
			return nil, errors.New("exploration hook requires a callback")
		}
		switch hook.Event {
		case BeforeExplore, BeforeCase, BeforeRequest, CheckCase, AfterCase, AfterExplore:
		default:
			return nil, fmt.Errorf("unknown exploration hook: %s", hook.Event)
		}
		id := fmt.Sprintf("hook-%d", index+1)
		callbacks[id] = hook
		request.Hooks = append(request.Hooks, ExplorationHookDescriptor{ID: id, Event: hook.Event, Operations: hook.Operations})
	}
	// Registration finishes independently of caller cancellation so its ID is
	// available to cancel the server-side invocation and dispatch cleanup.
	startCtx, stopStart := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	started, err := s.runtime.StartExploration(startCtx, request)
	stopStart()
	if err != nil {
		return nil, err
	}
	if started.ExplorationID == "" {
		return nil, errors.New("exploration start did not return an execution ID")
	}
	ref := ExplorationReference{SessionID: s.ID, ExplorationID: started.ExplorationID}
	var firstErr error
	var cleanupCancel context.CancelFunc
	defer func() {
		if cleanupCancel != nil {
			cleanupCancel()
		}
	}()
	activeCtx := ctx
	beginCleanup := func(cause error) error {
		if firstErr == nil {
			firstErr = cause
		}
		if cleanupCancel != nil {
			return cause
		}
		activeCtx, cleanupCancel = context.WithTimeout(context.Background(), cleanupTimeout)
		return s.runtime.CancelExploration(activeCtx, ref)
	}
	if ctx.Err() != nil {
		if err := beginCleanup(ctx.Err()); err != nil {
			return nil, err
		}
	}
	for {
		next, err := s.runtime.NextExploration(activeCtx, ref)
		if err != nil {
			if err := beginCleanup(err); err != nil {
				return nil, &ExplorationError{Cause: errors.Join(firstErr, err)}
			}
			continue
		}
		if ctx.Err() != nil && cleanupCancel == nil {
			if err := beginCleanup(ctx.Err()); err != nil {
				return nil, &ExplorationError{Cause: errors.Join(firstErr, err)}
			}
			continue
		}
		if next.State != "running" {
			if next.Error != "" {
				firstErr = errors.Join(firstErr, errors.New(next.Error))
			}
			if firstErr == nil && (next.State != "complete" || next.Result != nil && next.Result.Failed > 0) {
				firstErr = fmt.Errorf("checks ended with state %s", next.State)
			}
			if firstErr != nil {
				return next.Result, &ExplorationError{Result: next.Result, Cause: firstErr}
			}
			return next.Result, nil
		}
		if next.Action == nil {
			continue
		}
		action := next.Action
		if cleanupCancel != nil && action.Event != AfterCase && action.Event != AfterExplore {
			continue
		}
		var output ExploreHookOutput
		hook, known := callbacks[action.HookID]
		if !known {
			err = fmt.Errorf("unregistered exploration hook: %s", action.HookID)
		} else {
			output, err = invokeExploreHook(activeCtx, hookTimeout, hook.Handler, action.Input)
		}
		response := RespondExplorationRequest{ExplorationReference: ref, ActionID: action.ID}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			response.Error = err.Error()
			if response.Error == "" {
				response.Error = "exploration hook failed"
			}
		} else {
			response.Output = &output
		}
		if err := s.runtime.RespondExploration(activeCtx, response); err != nil {
			if cleanupCancel != nil && staleExplorationAction(err) {
				continue
			}
			if err := beginCleanup(err); err != nil {
				return nil, &ExplorationError{Cause: errors.Join(firstErr, err)}
			}
		}
	}
}

func staleExplorationAction(err error) bool {
	var response *responseError
	if !errors.As(err, &response) || response.status != http.StatusConflict {
		return false
	}
	var body struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(response.body, &body) == nil && body.Error == "hook action is no longer pending"
}

func invokeExploreHook(ctx context.Context, timeout time.Duration, callback func(context.Context, ExploreHookInput) (ExploreHookOutput, error), input ExploreHookInput) (ExploreHookOutput, error) {
	if err := ctx.Err(); err != nil {
		return ExploreHookOutput{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Keep cancellation and local context values while replacing the parent
	// trace with the generated attempt's W3C context.
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier(input.TraceContext))
	type reply struct {
		output ExploreHookOutput
		err    error
	}
	done := make(chan reply, 1)
	go func() {
		defer func() {
			if value := recover(); value != nil {
				done <- reply{err: fmt.Errorf("exploration hook panicked: %v", value)}
			}
		}()
		output, err := callback(ctx, input)
		done <- reply{output: output, err: err}
	}()
	select {
	case result := <-done:
		return result.output, result.err
	case <-ctx.Done():
		return ExploreHookOutput{}, ctx.Err()
	}
}

func (s *Session) stopExploration() error {
	s.exploreMu.Lock()
	s.closed = true
	cancel, done := s.exploreCancel, s.exploreDone
	timeout := s.exploreCleanupTimeout
	s.exploreMu.Unlock()
	if cancel == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// Registration may still be settling when the framework ends this test.
	ctx, stop := context.WithTimeout(context.Background(), timeout+31*time.Second)
	defer stop()
	cancel()
	select {
	case <-done:
		return errors.New("enclosing test ended before exploration completed")
	case <-ctx.Done():
		return fmt.Errorf("exploration cleanup: %w", ctx.Err())
	}
}
