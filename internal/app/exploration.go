package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/explore"
	"github.com/phrond/wiretap/internal/hooks"
	"github.com/phrond/wiretap/internal/redact"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type exploreRequest struct {
	SessionID string `json:"session_id"`
	explore.Selection
	explore.Config
}
type explorationSelector struct {
	SessionID     string `json:"session_id"`
	ExplorationID string `json:"exploration_id"`
}
type explorationResponse struct {
	explorationSelector
	ActionID string       `json:"action_id"`
	Output   hooks.Output `json:"output,omitempty"`
	Error    string       `json:"error,omitempty"`
}
type explorationAction struct {
	ID     string          `json:"id"`
	HookID string          `json:"hook_id"`
	Event  string          `json:"event"`
	Input  json.RawMessage `json:"input"`
}
type explorationState struct {
	ID     string             `json:"exploration_id"`
	State  string             `json:"state"`
	Action *explorationAction `json:"action,omitempty"`
	Result *explore.Result    `json:"result,omitempty"`
	Error  string             `json:"error,omitempty"`
}
type explorationJob struct {
	mu           sync.Mutex
	id, parentID string
	state        string
	result       *explore.Result
	err          string
	action       *explorationAction
	reply        chan explorationResponse
	changed      chan struct{}
	done         chan struct{}
	abandoned    chan struct{}
	abandonOnce  sync.Once
	cancel       context.CancelFunc
	cancelled    bool
	next         uint64
	lastReplyID  string
	lastReply    []byte
}

func decodeExplorationRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid exploration JSON: " + err.Error()})
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected one JSON object"})
		return false
	}
	return true
}

func (a *App) handleExplore(w http.ResponseWriter, r *http.Request) {
	var req exploreRequest
	if !decodeExplorationRequest(w, r, &req) {
		return
	}
	a.sessionsMu.Lock()
	parent := a.sessions[req.SessionID]
	if parent == nil || parent.closing || parent.explorationParent != "" {
		a.sessionsMu.Unlock()
		writeJSON(w, 409, map[string]string{"error": "an active authored session is required"})
		return
	}
	if parent.exploring != "" || parent.pending != 0 || a.hasRetiredWorkLocked() {
		a.sessionsMu.Unlock()
		writeJSON(w, 409, map[string]string{"error": "await current traffic or exploration before exploring"})
		return
	}
	// Session-specific dependency URLs cannot be redirected to a fresh owner.
	if parent.Explicit && !parent.Root.IsValid() && len(parent.Dependencies) > 0 {
		a.sessionsMu.Unlock()
		writeJSON(w, 400, map[string]string{"error": "exploring an explicit dependency session requires W3C trace context"})
		return
	}
	parent.exploring = "preparing"
	a.sessionsMu.Unlock()
	started := false
	defer func() {
		if !started {
			a.sessionsMu.Lock()
			parent.exploring = ""
			a.sessionsMu.Unlock()
		}
	}()
	seed, err := parent.Store.explorationSnapshot()
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	applySchemaValidation(seed)
	if err = redact.Apply(seed, seed.Redactions); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writer := a.currentBundle()
	if writer == nil {
		writeJSON(w, 409, map[string]string{"error": "capture run is not open"})
		return
	}
	plan, err := explore.Prepare(seed, writer.Snapshot().APIID, req.Selection, req.Config)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id := "explore-" + hex.EncodeToString(random[:])
	ctx, cancel := context.WithCancel(context.Background())
	job := &explorationJob{id: id, parentID: parent.ID, state: "running", changed: make(chan struct{}), done: make(chan struct{}), abandoned: make(chan struct{}), cancel: cancel}
	a.explorationMu.Lock()
	if a.explorations == nil {
		a.explorations = map[string]*explorationJob{}
	}
	// Completed jobs are released at parent teardown. Bound retained invocations
	// even when a test never tears down; never evict a result before its caller reads it.
	if len(a.explorations) >= 256 {
		a.explorationMu.Unlock()
		cancel()
		writeJSON(w, 409, map[string]string{"error": "too many exploration invocations; finish completed parent tests"})
		return
	}
	a.explorations[id] = job
	a.explorationMu.Unlock()
	a.sessionsMu.Lock()
	if parent.closing || parent.pending != 0 {
		a.sessionsMu.Unlock()
		a.explorationMu.Lock()
		delete(a.explorations, id)
		a.explorationMu.Unlock()
		cancel()
		writeJSON(w, 409, map[string]string{"error": "test traffic changed while preparing exploration; await requests before exploring"})
		return
	}
	parent.exploring = id
	started = true
	a.sessionsMu.Unlock()
	adapter := &explorationCapture{app: a, parent: parent, seed: seed}
	go func() {
		defer cancel()
		callHook := func(hookCtx context.Context, input explore.HookInput) (hooks.Output, error) {
			owner := adapter.child
			if input.Event == "before_explore" || input.Event == "after_explore" {
				owner = parent
				carrier := propagation.MapCarrier{}
				propagation.TraceContext{}.Inject(trace.ContextWithSpanContext(hookCtx, parent.Root), carrier)
				input.TraceContext = map[string]string(carrier)
			}
			if owner != nil {
				a.sessionsMu.Lock()
				owner.explorationFixtures = true
				a.sessionsMu.Unlock()
				defer func() { a.sessionsMu.Lock(); owner.explorationFixtures = false; a.sessionsMu.Unlock() }()
			}
			output, hookErr := job.callHook(hookCtx, input)
			if hookErr == nil && owner != nil {
				hookErr = a.waitExplorationTraffic(hookCtx, owner)
			}
			return output, hookErr
		}
		result, runErr := explore.Execute(ctx, plan, explore.Execution{Writer: writer, Target: parent.SUTBaseURL, InvocationID: id, ParentAttemptID: parent.CaseID, CallHook: callHook, BeginCase: adapter.begin, SnapshotCase: adapter.snapshot, EndCase: adapter.end, InvokeGRPC: adapter.invokeGRPC})
		if runErr != nil {
			parent.Store.AddCaptureDiagnostic("exploration " + id + ": " + runErr.Error())
		}
		job.mu.Lock()
		job.result = &result
		job.state = "complete"
		if runErr != nil || result.Failed > 0 {
			job.state = "failed"
		}
		if job.cancelled {
			job.state = "cancelled"
		}
		if runErr != nil {
			job.err = runErr.Error()
		}
		job.action = nil
		// Publish the terminal result and release the parent atomically to
		// pollers, so neither teardown nor another invocation races completion.
		a.sessionsMu.Lock()
		parent.exploring = ""
		if runErr != nil || result.Failed > 0 {
			parent.explorationFailed = true
		}
		a.sessionsMu.Unlock()
		job.signalLocked()
		close(job.done)
		job.mu.Unlock()
	}()
	writeJSON(w, 200, explorationState{ID: id, State: "running"})
}

func (j *explorationJob) signalLocked() { close(j.changed); j.changed = make(chan struct{}) }
func (j *explorationJob) cancelLocked() {
	if j.state != "running" {
		return
	}
	j.cancelled = true
	j.cancel()
	// Pending work must not be dispatched again after cancellation. Cleanup
	// actions use their own bounded context and remain available to the SDK.
	if j.action != nil && j.action.Event != "after_case" && j.action.Event != "after_explore" {
		j.action = nil
		j.signalLocked()
	}
}
func (j *explorationJob) snapshotLocked() explorationState {
	return explorationState{ID: j.id, State: j.state, Action: j.action, Result: j.result, Error: j.err}
}
func (a *App) explorationFor(w http.ResponseWriter, s explorationSelector) *explorationJob {
	a.explorationMu.Lock()
	job := a.explorations[s.ExplorationID]
	a.explorationMu.Unlock()
	if job == nil || s.SessionID == "" || job.parentID != s.SessionID {
		writeJSON(w, 404, map[string]string{"error": "exploration not found for session"})
		return nil
	}
	return job
}
func (a *App) handleExploreNext(w http.ResponseWriter, r *http.Request) {
	var s explorationSelector
	if !decodeExplorationRequest(w, r, &s) {
		return
	}
	j := a.explorationFor(w, s)
	if j == nil {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		j.mu.Lock()
		state := j.snapshotLocked()
		changed := j.changed
		j.mu.Unlock()
		if state.Action != nil || state.State != "running" {
			writeJSON(w, 200, state)
			return
		}
		select {
		case <-changed:
		case <-timer.C:
			writeJSON(w, 200, state)
			return
		case <-r.Context().Done():
			return
		}
	}
}
func (a *App) handleExploreRespond(w http.ResponseWriter, r *http.Request) {
	var response explorationResponse
	if !decodeExplorationRequest(w, r, &response) {
		return
	}
	j := a.explorationFor(w, response.explorationSelector)
	if j == nil {
		return
	}
	encoded, _ := json.Marshal(response)
	j.mu.Lock()
	defer j.mu.Unlock()
	if response.ActionID == j.lastReplyID && j.lastReplyID != "" {
		if bytes.Equal(encoded, j.lastReply) {
			writeJSON(w, 200, map[string]bool{"accepted": true})
		} else {
			writeJSON(w, 409, map[string]string{"error": "hook response conflicts with an accepted result"})
		}
		return
	}
	if j.state != "running" || j.action == nil || j.action.ID != response.ActionID {
		writeJSON(w, 409, map[string]string{"error": "hook action is no longer pending"})
		return
	}
	j.lastReplyID, j.lastReply = response.ActionID, encoded
	j.reply <- response
	j.action = nil
	j.signalLocked()
	writeJSON(w, 200, map[string]bool{"accepted": true})
}
func (a *App) handleExploreCancel(w http.ResponseWriter, r *http.Request) {
	var s explorationSelector
	if !decodeExplorationRequest(w, r, &s) {
		return
	}
	j := a.explorationFor(w, s)
	if j == nil {
		return
	}
	j.mu.Lock()
	j.cancelLocked()
	state := j.snapshotLocked()
	j.mu.Unlock()
	writeJSON(w, 200, state)
}
func (j *explorationJob) callHook(ctx context.Context, in explore.HookInput) (hooks.Output, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return hooks.Output{}, err
	}
	if len(raw) > 16<<20 {
		return hooks.Output{}, fmt.Errorf("hook input exceeds 16 MiB; narrow exploration capture")
	}
	select {
	case <-j.abandoned:
		return hooks.Output{}, fmt.Errorf("exploration client disconnected during runtime shutdown")
	default:
	}
	if err := ctx.Err(); err != nil {
		return hooks.Output{}, err
	}
	j.mu.Lock()
	if err := ctx.Err(); err != nil {
		j.mu.Unlock()
		return hooks.Output{}, err
	}
	j.next++
	action := &explorationAction{ID: fmt.Sprintf("hook-%d", j.next), HookID: in.HookID, Event: in.Event, Input: raw}
	j.action = action
	j.reply = make(chan explorationResponse, 1)
	reply := j.reply
	j.signalLocked()
	j.mu.Unlock()
	defer func() {
		j.mu.Lock()
		if j.action == action {
			j.action = nil
			j.signalLocked()
		}
		j.mu.Unlock()
	}()
	select {
	case response := <-reply:
		if response.Error != "" {
			return hooks.Output{}, fmt.Errorf("%s hook failed: %s", in.Event, response.Error)
		}
		return response.Output, nil
	case <-ctx.Done():
		return hooks.Output{}, fmt.Errorf("%s hook interrupted: %w", in.Event, ctx.Err())
	case <-j.abandoned:
		return hooks.Output{}, fmt.Errorf("%s hook client unavailable during shutdown", in.Event)
	}
}

// Abandon callbacks only during runtime shutdown. Normal cancellation keeps the
// SDK connected so its cleanup callbacks can finish.
func (a *App) stopExplorations(ctx context.Context) error {
	a.explorationMu.Lock()
	jobs := make([]*explorationJob, 0, len(a.explorations))
	for _, j := range a.explorations {
		jobs = append(jobs, j)
	}
	a.explorationMu.Unlock()
	for _, j := range jobs {
		j.cancel()
		j.abandonOnce.Do(func() { close(j.abandoned) })
	}
	for _, j := range jobs {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *TestCaseStore) explorationSnapshot() (*artifact.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.active
	if state == nil {
		return nil, errors.New("test capture is no longer active")
	}
	value := artifact.Artifact{Version: 2, TestCase: artifact.TestCase{ID: state.id, Name: state.name, StartedAt: state.startedAt}, Schemas: state.schemas, Traffic: state.flows, Redactions: state.redactions, CaptureIncomplete: state.captureIncomplete, CaptureDiagnostics: state.captureDiagnostics}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result artifact.Artifact
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

type explorationCapture struct {
	app    *App
	parent *runtimeSession
	seed   *artifact.Artifact
	child  *runtimeSession
}

func (c *explorationCapture) begin(ctx context.Context, tc artifact.TestCase) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	var traceID trace.TraceID
	var spanID trace.SpanID
	if _, err := rand.Read(traceID[:]); err != nil {
		return ctx, err
	}
	if _, err := rand.Read(spanID[:]); err != nil {
		return ctx, err
	}
	root := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	store := NewTestCaseStore(nil, c.app.cfg.Capture.MaxBodyBytes)
	store.SetGRPCContracts(c.parent.Store.grpcContracts)
	if _, err := store.StartCase(tc.Name, nil, nil, c.parent.SUTBaseURL); err != nil {
		return ctx, err
	}
	store.active.id = tc.ID
	store.active.startedAt = tc.StartedAt
	store.active.root = root
	store.active.schemas = c.seed.Schemas
	c.app.sessionsMu.Lock()
	defer c.app.sessionsMu.Unlock()
	if c.parent.pending != 0 || c.app.hasRetiredWorkLocked() {
		return ctx, errors.New("previous capture is still draining; cannot safely start another generated case")
	}
	id := c.app.nextSessionID()
	child := &runtimeSession{ID: id, Name: tc.Name, CaseID: tc.ID, Root: root, Store: store, Dependencies: c.parent.Dependencies, SUTBaseURL: c.parent.SUTBaseURL, Explicit: c.parent.Explicit, CreatedAt: time.Now().UTC(), drained: make(chan struct{}), explorationParent: c.parent.ID}
	c.app.sessions[id] = child
	c.app.traceOwners[root.TraceID().String()] = id
	if !c.parent.Explicit {
		c.app.defaultSessionID = id
	}
	c.child = child
	return trace.ContextWithSpanContext(ctx, root), nil
}
func (c *explorationCapture) snapshot(ctx context.Context, art *artifact.Artifact) error {
	if c.child == nil {
		return nil
	}
	if err := c.app.waitExplorationTraffic(ctx, c.child); err != nil {
		return err
	}
	return c.merge(art, false)
}

// A response can finish just before its recorder releases. Drain admitted work
// before changing fixture ownership or exposing a case to assertions.
func (a *App) waitExplorationTraffic(ctx context.Context, owner *runtimeSession) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		a.sessionsMu.Lock()
		pending := owner.pending
		a.sessionsMu.Unlock()
		if pending == 0 {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return errors.New("dependency capture did not drain")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (c *explorationCapture) end(ctx context.Context, art *artifact.Artifact) error {
	if c.child == nil {
		return nil
	}
	child := c.child
	c.app.controlMu.Lock()
	c.app.beginDrain(child)
	c.app.controlMu.Unlock()
	c.app.waitForDrain(ctx, child, 5*time.Second)
	err := c.merge(art, true)
	child.Store.AbortCase()
	c.app.sessionsMu.Lock()
	delete(c.app.sessions, child.ID)
	if !c.parent.Explicit && !c.parent.closing {
		c.app.defaultSessionID = c.parent.ID
	}
	c.app.sessionsMu.Unlock()
	c.child = nil
	return err
}
func (c *explorationCapture) merge(art *artifact.Artifact, validate bool) error {
	snapshot, err := c.child.Store.explorationSnapshot()
	if err != nil {
		return err
	}
	flows := art.Traffic[:0]
	for _, f := range art.Traffic {
		if !strings.HasPrefix(f.ID, "dependency-") {
			flows = append(flows, f)
		}
	}
	art.Traffic = flows
	for _, flow := range snapshot.Traffic {
		flow.ID = "dependency-" + flow.ID
		flow.ParentFlowID = ""
		flow.Assumption = &artifact.DependencyAssumption{EvidenceBasis: "observed_exchange", ConsumedFields: "unknown"}
		flow.Assumption.ProviderCheck.Status = "not_checked"
		art.Traffic = append(art.Traffic, flow)
	}
	art.TestCase.TraceID = c.child.Root.TraceID().String()
	art.TestCase.RootSpanID = c.child.Root.SpanID().String()
	art.CaptureIncomplete = art.CaptureIncomplete || snapshot.CaptureIncomplete
	if snapshot.CaptureIncomplete {
		art.TestCase.Status = "fail"
		art.CaptureDiagnostics = append(art.CaptureDiagnostics, snapshot.CaptureDiagnostics...)
	}
	linkCausality(art.Traffic)
	for i := range art.Traffic {
		art.Traffic[i].Fingerprint = flowFingerprint(art.Traffic[i])
	}
	if validate {
		snapshot.TestCase.Status = "pass"
		applySchemaValidation(snapshot)
		if art.SchemaValidation == nil {
			art.SchemaValidation = &artifact.SchemaValidation{Status: "pass"}
		}
		if snapshot.SchemaValidation != nil {
			for i := range snapshot.SchemaValidation.Errors {
				snapshot.SchemaValidation.Errors[i].FlowID = "dependency-" + snapshot.SchemaValidation.Errors[i].FlowID
			}
			for i := range snapshot.SchemaValidation.Unavailable {
				snapshot.SchemaValidation.Unavailable[i].FlowID = "dependency-" + snapshot.SchemaValidation.Unavailable[i].FlowID
			}
			art.SchemaValidation.Errors = append(art.SchemaValidation.Errors, snapshot.SchemaValidation.Errors...)
			art.SchemaValidation.Unavailable = append(art.SchemaValidation.Unavailable, snapshot.SchemaValidation.Unavailable...)
			art.SchemaValidation.Skipped = append(art.SchemaValidation.Skipped, snapshot.SchemaValidation.Skipped...)
			if len(snapshot.SchemaValidation.Unavailable) > 0 && art.SchemaValidation.Status == "pass" {
				art.SchemaValidation.Status = "partial"
			}
		}
		for _, assertion := range snapshot.Assertions {
			assertion.ID = "dependency-" + assertion.ID
			assertion.FlowID = "dependency-" + assertion.FlowID
			art.Assertions = append(art.Assertions, assertion)
		}
		if snapshot.TestCase.Status == "fail" {
			art.TestCase.Status = "fail"
			art.SchemaValidation.Status = "fail"
		}
	}
	return nil
}
