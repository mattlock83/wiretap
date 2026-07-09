package mock

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Server implements an HTTP mock server with stub matching and recording.
type Server struct {
	registry *Registry
	recorder *Recorder

	mu               syncState
	listener         net.Listener
	server           *http.Server
	fallbackUpstream string
	recordingMu      sync.Mutex
	skipRecording    map[string]struct{}
	exchangeSeq      atomic.Uint64
	recordSeq        atomic.Uint64
	recordingEnabled atomic.Bool

	// defaultSessionMu guards defaultSession: the session that unlabeled
	// traffic (no session prefix in the URL) is attributed to. Set while
	// exactly one zero-config session is active.
	defaultSessionMu sync.RWMutex
	defaultSession   string
}

type syncState struct {
	mu      sync.Mutex
	running bool
}

// NewServer creates a mock server using the provided registry/recorder.
func NewServer(registry *Registry, recorder *Recorder) *Server {
	if registry == nil {
		registry = NewRegistry()
	}
	if recorder == nil {
		recorder = NewRecorder()
	}
	server := &Server{
		registry:      registry,
		recorder:      recorder,
		skipRecording: map[string]struct{}{},
	}
	server.recordingEnabled.Store(true)
	return server
}

func (s *Server) SetRecordingEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.recordingEnabled.Store(enabled)
}

// SetDefaultSession sets (or clears, with "") the session that unlabeled
// traffic is attributed to. Used for zero-config correlation when a single
// session is active.
func (s *Server) SetDefaultSession(sessionID string) {
	if s == nil {
		return
	}
	s.defaultSessionMu.Lock()
	s.defaultSession = strings.TrimSpace(sessionID)
	s.defaultSessionMu.Unlock()
}

func (s *Server) getDefaultSession() string {
	if s == nil {
		return ""
	}
	s.defaultSessionMu.RLock()
	defer s.defaultSessionMu.RUnlock()
	return s.defaultSession
}

func (s *Server) SetSessionRecordingEnabled(sessionID string, enabled bool) {
	if s == nil {
		return
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return
	}
	s.recordingMu.Lock()
	defer s.recordingMu.Unlock()
	if enabled {
		delete(s.skipRecording, trimmed)
		return
	}
	s.skipRecording[trimmed] = struct{}{}
}

// Start launches the server on the provided address (":0" for random port).
// Returns the resolved address.
func (s *Server) Start(ctx context.Context, addr string) (string, error) {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	if s.mu.running {
		if s.listener != nil {
			return s.listener.Addr().String(), nil
		}
		return "", nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	s.listener = ln
	s.server = &http.Server{
		Handler: http.HandlerFunc(s.handle),
	}
	s.mu.running = true

	go func() {
		_ = s.server.Serve(ln)
	}()

	return ln.Addr().String(), nil
}

// Stop shuts down the server gracefully.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	if !s.mu.running {
		return nil
	}
	s.mu.running = false
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// Running reports whether the mock server is currently serving traffic.
func (s *Server) Running() bool {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	return s.mu.running
}

func (s *Server) RegisterStub(stub Stub) {
	s.registry.Register(stub)
}

func (s *Server) ClearStubs() {
	s.registry.Clear()
}

func (s *Server) RemoveStubsByID(ids []string) int {
	return s.registry.RemoveIDs(ids)
}

func (s *Server) ListStubs() []Stub {
	return s.registry.List()
}

func (s *Server) SetFallbackUpstream(baseURL string) error {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		s.fallbackUpstream = ""
		return nil
	}
	if _, err := url.Parse(trimmed); err != nil {
		return err
	}
	s.fallbackUpstream = strings.TrimRight(trimmed, "/")
	return nil
}

func (s *Server) Recorder() *Recorder {
	return s.recorder
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	bodyBytes, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	sessionID, assetID, trimmedPath := parseMockPath(r.URL.Path)
	if sessionID == "" {
		// Zero-config correlation: unlabeled traffic belongs to the single
		// active session, when one is registered.
		sessionID = s.getDefaultSession()
	}
	traceID := strings.TrimSpace(r.Header.Get("X-Phrond-Trace-Id"))
	recordURL := r.URL.String()
	if trimmedPath != r.URL.Path {
		copyURL := *r.URL
		copyURL.Path = trimmedPath
		recordURL = copyURL.String()
	}

	req := RecordedRequest{
		Method:  r.Method,
		Path:    trimmedPath,
		URL:     recordURL,
		Headers: cloneHeader(r.Header),
		Query:   cloneQuery(r.URL.Query()),
		Body:    string(bodyBytes),
	}

	stub, matched := s.registry.Match(r, bodyBytes, MatchTarget{
		SessionID: sessionID,
		AssetID:   assetID,
		Path:      trimmedPath,
	})
	resp := RecordedResponse{
		Status:  http.StatusNotImplemented,
		DelayMS: 0,
		Headers: map[string][]string{},
		Body:    "no matching stub",
	}
	if matched {
		resp = literalResponse(stub)
	} else if s.fallbackUpstream != "" {
		recordedID := "recorded-" + strconv.FormatUint(s.recordSeq.Add(1), 10)
		upstreamResp, upstreamErr := s.forwardToUpstream(r, trimmedPath, bodyBytes)
		if upstreamErr != nil {
			resp = RecordedResponse{
				Status:  http.StatusBadGateway,
				DelayMS: 0,
				Headers: map[string][]string{},
				Body:    upstreamErr.Error(),
			}
		} else {
			resp = upstreamResp
			matched = true
			stub.ID = recordedID
			stub.SessionID = sessionID
			stub.AssetID = assetID
			stub.Source = "recorded"
			stub.Match = StubMatch{
				Method: r.Method,
				Path:   trimmedPath,
				Body:   string(bodyBytes),
			}
			stub.Response = StubResponse{
				Status:  resp.Status,
				Headers: toSingleHeaders(resp.Headers),
				Body:    resp.Body,
				DelayMS: resp.DelayMS,
				Fault:   resp.Fault,
			}
		}
	}

	if resp.DelayMS > 0 {
		time.Sleep(time.Duration(resp.DelayMS) * time.Millisecond)
	}
	if strings.EqualFold(resp.Fault, "connection_error") {
		s.writeConnectionError(w)
		exchange := Exchange{
			ID:            "exchange-" + strconv.FormatUint(s.exchangeSeq.Add(1), 10),
			SessionID:     sessionID,
			AssetID:       assetID,
			TraceID:       traceID,
			Request:       req,
			Response:      resp,
			MatchedStubID: stub.ID,
			Matched:       matched,
			MatchedStub:   matchedStubSnapshot(matched, stub),
			StartedAt:     started,
			FinishedAt:    time.Now(),
			Error:         "connection_error",
		}
		s.recordExchange(exchange)
		return
	}
	if strings.EqualFold(resp.Fault, "ssl_error") {
		resp.Status = 525
		if resp.Body == "" {
			resp.Body = "ssl handshake failure"
		}
	}

	for key, values := range resp.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.Status)
	if resp.Body != "" {
		_, _ = io.WriteString(w, resp.Body)
	}

	exchange := Exchange{
		ID:            "exchange-" + strconv.FormatUint(s.exchangeSeq.Add(1), 10),
		SessionID:     sessionID,
		AssetID:       assetID,
		TraceID:       traceID,
		Request:       req,
		Response:      resp,
		MatchedStubID: stub.ID,
		Matched:       matched,
		MatchedStub:   matchedStubSnapshot(matched, stub),
		StartedAt:     started,
		FinishedAt:    time.Now(),
	}
	s.recordExchange(exchange)
}

// literalResponse returns the stub's configured response verbatim. Bodies and
// headers are returned exactly as registered — there is no templating.
func literalResponse(stub Stub) RecordedResponse {
	status := stub.Response.Status
	if status == 0 {
		status = http.StatusOK
	}
	headers := make(map[string][]string, len(stub.Response.Headers))
	for key, value := range stub.Response.Headers {
		headers[key] = []string{value}
	}
	return RecordedResponse{
		Status:  status,
		DelayMS: stub.Response.DelayMS,
		Headers: headers,
		Body:    stub.Response.Body,
		Fault:   stub.Response.Fault,
	}
}

// matchedStubSnapshot returns a copy of the stub that served a request, or nil
// when no stub matched. The copy preserves the response template.
func matchedStubSnapshot(matched bool, stub Stub) *Stub {
	if !matched {
		return nil
	}
	snapshot := stub
	return &snapshot
}

func (s *Server) forwardToUpstream(r *http.Request, trimmedPath string, body []byte) (RecordedResponse, error) {
	target, err := url.Parse(s.fallbackUpstream)
	if err != nil {
		return RecordedResponse{}, err
	}
	target.Path = strings.TrimSuffix(target.Path, "/") + trimmedPath
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return RecordedResponse{}, err
	}
	req.Header = http.Header(cloneHeaderDirect(r.Header))
	req.Host = target.Host

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return RecordedResponse{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return RecordedResponse{
		Status:  resp.StatusCode,
		Headers: cloneHeader(resp.Header),
		Body:    string(respBody),
	}, nil
}

func (s *Server) writeConnectionError(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "connection_error")
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "connection_error")
		return
	}
	_ = conn.Close()
}

// parseMockPath strips phrond routing segments from an incoming path and
// returns (sessionID, assetID, remaining path). Supported forms:
//
//	/__phrond/session/<sid>/dep/<assetID>/rest...  (explicit session + dependency)
//	/__phrond/session/<sid>/rest...                (explicit session)
//	/__phrond/dep/<assetID>/rest...                (dependency, zero-config session)
//	/rest...                                       (plain)
func parseMockPath(rawPath string) (sessionID, assetID, remaining string) {
	parts := strings.Split(strings.TrimPrefix(rawPath, "/"), "/")
	idx := 0
	if len(parts) > idx && parts[idx] == "__phrond" {
		idx++
		if len(parts) > idx+1 && parts[idx] == "session" && strings.TrimSpace(parts[idx+1]) != "" {
			sessionID = strings.TrimSpace(parts[idx+1])
			idx += 2
		}
		if len(parts) > idx+1 && parts[idx] == "dep" && strings.TrimSpace(parts[idx+1]) != "" {
			assetID = strings.TrimSpace(parts[idx+1])
			idx += 2
		}
	}
	if idx == 0 {
		return "", "", rawPath
	}
	remaining = "/"
	if len(parts) > idx {
		remaining += strings.Join(parts[idx:], "/")
	}
	return sessionID, assetID, remaining
}

func toSingleHeaders(in map[string][]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, values := range in {
		if len(values) == 0 {
			continue
		}
		out[key] = values[0]
	}
	return out
}

func cloneHeaderDirect(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for key, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

func cloneHeader(header http.Header) map[string][]string {
	out := make(map[string][]string, len(header))
	for key, values := range header {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

func cloneQuery(values map[string][]string) map[string][]string {
	out := make(map[string][]string, len(values))
	for key, vals := range values {
		copied := make([]string, len(vals))
		copy(copied, vals)
		out[key] = copied
	}
	return out
}

func (s *Server) recordExchange(exchange Exchange) {
	if s == nil || s.recorder == nil {
		return
	}
	if !s.recordingEnabled.Load() {
		return
	}
	if sessionID := strings.TrimSpace(exchange.SessionID); sessionID != "" {
		s.recordingMu.Lock()
		_, disabled := s.skipRecording[sessionID]
		s.recordingMu.Unlock()
		if disabled {
			return
		}
	}
	s.recorder.Record(exchange)
}
