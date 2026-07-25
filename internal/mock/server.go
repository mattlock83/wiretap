package mock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phrond/evermock/internal/capture"
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
	externalClient   *http.Client
	externalMu       sync.RWMutex
	externalRoutes   map[string]map[string]externalUpstream
	maxBodyBytes     int64

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

type externalUpstream struct {
	baseURL      *url.URL
	preserveHost bool
}

// NewServer creates a mock server using the provided registry/recorder.
func NewServer(registry *Registry, recorder *Recorder, maxBodyBytes ...int64) *Server {
	if registry == nil {
		registry = NewRegistry()
	}
	if recorder == nil {
		recorder = NewRecorder()
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	server := &Server{
		registry:      registry,
		recorder:      recorder,
		skipRecording: map[string]struct{}{},
		externalClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		externalRoutes: map[string]map[string]externalUpstream{},
		maxBodyBytes:   capture.DefaultMaxBodyBytes,
	}
	if len(maxBodyBytes) > 0 {
		server.maxBodyBytes = maxBodyBytes[0]
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

// ValidateExternalUpstream checks that a BYO mock target is an absolute HTTP
// URL and does not point back at this Phrond mock listener.
func (s *Server) ValidateExternalUpstream(baseURL string) error {
	parsed, err := parseExternalUpstream(baseURL)
	if err != nil {
		return err
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("external mock upstream_url must not include a query or fragment")
	}
	if s.targetsOwnListener(parsed) {
		return errors.New("external mock upstream_url must not point to the Phrond mock listener")
	}
	return nil
}

// SetExternalUpstream registers a per-session, per-dependency capture gateway.
func (s *Server) SetExternalUpstream(sessionID, assetID, baseURL string, preserveHost bool) error {
	sessionID = strings.TrimSpace(sessionID)
	assetID = strings.TrimSpace(assetID)
	if sessionID == "" || assetID == "" {
		return errors.New("session_id and asset_id are required for an external mock")
	}
	if err := s.ValidateExternalUpstream(baseURL); err != nil {
		return err
	}
	parsed, _ := parseExternalUpstream(baseURL)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")

	s.externalMu.Lock()
	defer s.externalMu.Unlock()
	routes := s.externalRoutes[sessionID]
	if routes == nil {
		routes = map[string]externalUpstream{}
		s.externalRoutes[sessionID] = routes
	}
	routes[assetID] = externalUpstream{baseURL: parsed, preserveHost: preserveHost}
	return nil
}

// RemoveExternalUpstreams removes all BYO mock routes owned by a session.
func (s *Server) RemoveExternalUpstreams(sessionID string) {
	if s == nil {
		return
	}
	s.externalMu.Lock()
	delete(s.externalRoutes, strings.TrimSpace(sessionID))
	s.externalMu.Unlock()
}

func parseExternalUpstream(raw string) (*url.URL, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, errors.New("external mock upstream_url is required")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("invalid external mock upstream_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("external mock upstream_url scheme must be http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("external mock upstream_url must be absolute")
	}
	return parsed, nil
}

func (s *Server) targetsOwnListener(target *url.URL) bool {
	if s == nil || s.listener == nil || target == nil {
		return false
	}
	listenHost, listenPort, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return false
	}
	targetPort := target.Port()
	if targetPort == "" {
		if target.Scheme == "https" {
			targetPort = "443"
		} else {
			targetPort = "80"
		}
	}
	if listenPort != targetPort {
		return false
	}
	targetHost := strings.TrimSpace(target.Hostname())
	if strings.EqualFold(listenHost, targetHost) {
		return true
	}
	return isLocalHost(listenHost) && isLocalHost(targetHost)
}

func isLocalHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || host == "0.0.0.0" || host == "::" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func (s *Server) externalUpstream(sessionID, assetID string) (externalUpstream, bool) {
	s.externalMu.RLock()
	defer s.externalMu.RUnlock()
	routes := s.externalRoutes[sessionID]
	if routes == nil {
		return externalUpstream{}, false
	}
	upstream, ok := routes[assetID]
	return upstream, ok
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

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
		copyURL.RawPath = stripMockEscapedPath(r.URL.EscapedPath())
		recordURL = copyURL.String()
	}

	req := RecordedRequest{
		Method:  r.Method,
		Path:    trimmedPath,
		URL:     recordURL,
		Headers: cloneHeader(r.Header),
		Query:   cloneQuery(r.URL.Query()),
	}

	if upstream, ok := s.externalUpstream(sessionID, assetID); ok {
		s.handleExternal(w, r, req, sessionID, assetID, traceID, started, upstream)
		return
	}

	capturedRequest, replayBody, cleanup, err := s.captureMockRequest(r, s.fallbackUpstream != "")
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		http.Error(w, "capture request body: "+err.Error(), http.StatusInternalServerError)
		return
	}
	req.Trailers = cloneHeader(r.Trailer)
	req.Body = string(capturedRequest.Bytes)
	req.BodyBytes = capturedRequest.OriginalBytes
	req.BodyTruncated = capturedRequest.Truncated

	stub, matched := s.registry.Match(r, capturedRequest.Bytes, MatchTarget{
		SessionID: sessionID,
		AssetID:   assetID,
		Path:      trimmedPath,
	}, !capturedRequest.Truncated)
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
		upstreamResp, upstreamErr := s.forwardToUpstream(r, trimmedPath, replayBody, capturedRequest.OriginalBytes)
		if upstreamErr != nil {
			resp = RecordedResponse{
				Status:  http.StatusBadGateway,
				DelayMS: 0,
				Headers: map[string][]string{},
				Body:    upstreamErr.Error(),
			}
		} else {
			resp, upstreamErr = s.writeUpstreamResponse(w, upstreamResp)
			matched = true
			stub.ID = recordedID
			stub.SessionID = sessionID
			stub.AssetID = assetID
			stub.Source = "recorded"
			stub.Match = StubMatch{
				Method: r.Method,
				Path:   trimmedPath,
			}
			if !capturedRequest.Truncated {
				stub.Match.Body = string(capturedRequest.Bytes)
			}
			stub.Response = StubResponse{
				Status:  resp.Status,
				Headers: toSingleHeaders(resp.Headers),
				Body:    resp.Body,
				DelayMS: resp.DelayMS,
				Fault:   resp.Fault,
			}
			exchange := Exchange{
				ID:            "exchange-" + strconv.FormatUint(s.exchangeSeq.Add(1), 10),
				SessionID:     sessionID,
				AssetID:       assetID,
				TraceID:       traceID,
				Request:       req,
				Response:      resp,
				MatchedStubID: stub.ID,
				Matched:       true,
				MatchedStub:   matchedStubSnapshot(true, stub),
				MockMode:      "builtin",
				StartedAt:     started,
				FinishedAt:    time.Now(),
			}
			if upstreamErr != nil {
				exchange.Error = upstreamErr.Error()
			}
			s.recordExchange(exchange)
			return
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
			Response:      s.boundedRecordedResponse(resp),
			MatchedStubID: stub.ID,
			Matched:       matched,
			MatchedStub:   matchedStubSnapshot(matched, stub),
			MockMode:      "builtin",
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
		Response:      s.boundedRecordedResponse(resp),
		MatchedStubID: stub.ID,
		Matched:       matched,
		MatchedStub:   matchedStubSnapshot(matched, stub),
		MockMode:      "builtin",
		StartedAt:     started,
		FinishedAt:    time.Now(),
	}
	s.recordExchange(exchange)
}

func (s *Server) captureMockRequest(r *http.Request, spool bool) (capture.Body, io.Reader, func(), error) {
	if !spool {
		body, err := capture.ReadAll(r.Body, s.maxBodyBytes)
		_ = r.Body.Close()
		return body, nil, nil, err
	}

	file, err := os.CreateTemp("", "phrond-mock-request-*")
	if err != nil {
		return capture.Body{}, nil, nil, err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	writer := capture.NewWriter(s.maxBodyBytes)
	_, copyErr := io.Copy(io.MultiWriter(file, writer), r.Body)
	_ = r.Body.Close()
	if copyErr != nil {
		cleanup()
		return capture.Body{}, nil, nil, copyErr
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return capture.Body{}, nil, nil, err
	}
	return writer.Body(), file, cleanup, nil
}

func (s *Server) boundedRecordedResponse(resp RecordedResponse) RecordedResponse {
	body := capture.FromBytes([]byte(resp.Body), s.maxBodyBytes)
	resp.Body = string(body.Bytes)
	resp.BodyBytes = body.OriginalBytes
	resp.BodyTruncated = body.Truncated
	return resp
}

func (s *Server) handleExternal(
	w http.ResponseWriter,
	r *http.Request,
	recordedRequest RecordedRequest,
	sessionID string,
	assetID string,
	traceID string,
	started time.Time,
	upstream externalUpstream,
) {
	upstreamResponse, requestCapture, err := s.forwardToExternal(r, upstream)
	capturedRequest := requestCapture.Body()
	recordedRequest.Body = string(capturedRequest.Bytes)
	recordedRequest.BodyBytes = capturedRequest.OriginalBytes
	recordedRequest.BodyTruncated = capturedRequest.Truncated
	recordedRequest.Trailers = cloneHeader(r.Trailer)

	exchange := Exchange{
		ID:        "exchange-" + strconv.FormatUint(s.exchangeSeq.Add(1), 10),
		SessionID: sessionID,
		AssetID:   assetID,
		TraceID:   traceID,
		Request:   recordedRequest,
		MockMode:  "external",
		StartedAt: started,
	}
	if err != nil {
		exchange.Error = err.Error()
		response := RecordedResponse{
			Status:  http.StatusBadGateway,
			Headers: map[string][]string{"Content-Type": {"text/plain"}},
			Body:    "proxy error: " + err.Error(),
		}
		exchange.Response = s.boundedRecordedResponse(response)
		exchange.FinishedAt = time.Now()
		writeRecordedResponse(w, response)
		s.recordExchange(exchange)
		return
	}

	response, copyErr := s.writeUpstreamResponse(w, upstreamResponse)
	exchange.Response = response
	exchange.FinishedAt = time.Now()
	if copyErr != nil {
		exchange.Error = copyErr.Error()
	}
	s.recordExchange(exchange)
}

func (s *Server) forwardToExternal(r *http.Request, upstream externalUpstream) (*http.Response, *capture.Writer, error) {
	target := *upstream.baseURL
	target.Path = joinGatewayPath(upstream.baseURL.Path, stripMockPath(r.URL.Path))
	target.RawPath = joinGatewayPath(upstream.baseURL.EscapedPath(), stripMockEscapedPath(r.URL.EscapedPath()))
	target.RawQuery = r.URL.RawQuery
	target.ForceQuery = r.URL.ForceQuery

	body, requestCapture := capture.WrapReadCloser(r.Body, s.maxBodyBytes)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), body)
	if err != nil {
		return nil, requestCapture, err
	}
	req.ContentLength = r.ContentLength
	req.Header = endToEndHeader(r.Header)
	if _, present := r.Header["User-Agent"]; !present {
		req.Header["User-Agent"] = nil
	}
	req.Trailer = r.Trailer
	if len(req.Trailer) > 0 {
		req.ContentLength = -1
	}
	if upstream.preserveHost {
		req.Host = r.Host
	} else {
		req.Host = target.Host
	}

	resp, err := s.externalClient.Do(req)
	if err != nil {
		return nil, requestCapture, err
	}
	return resp, requestCapture, nil
}

func (s *Server) writeUpstreamResponse(w http.ResponseWriter, resp *http.Response) (RecordedResponse, error) {
	if resp == nil {
		return RecordedResponse{}, errors.New("upstream response is nil")
	}
	defer resp.Body.Close()

	headers := endToEndHeader(resp.Header)
	if resp.ContentLength >= 0 && headers.Get("Content-Length") == "" {
		headers.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	for key := range resp.Trailer {
		w.Header().Add("Trailer", key)
	}
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)

	responseCapture := capture.NewWriter(s.maxBodyBytes)
	_, copyErr := io.Copy(w, io.TeeReader(resp.Body, responseCapture))
	for key, values := range resp.Trailer {
		w.Header().Del(key)
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	captured := responseCapture.Body()
	return RecordedResponse{
		Status:        status,
		Headers:       cloneHeader(headers),
		Trailers:      cloneHeader(resp.Trailer),
		Body:          string(captured.Bytes),
		BodyBytes:     captured.OriginalBytes,
		BodyTruncated: captured.Truncated,
	}, copyErr
}

func writeRecordedResponse(w http.ResponseWriter, resp RecordedResponse) {
	headers := http.Header(resp.Headers)
	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	for key := range resp.Trailers {
		w.Header().Add("Trailer", key)
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if resp.Body != "" {
		_, _ = io.WriteString(w, resp.Body)
	}
	for key, values := range resp.Trailers {
		w.Header().Del(key)
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

func endToEndHeader(header http.Header) http.Header {
	out := http.Header(cloneHeader(header))
	for _, value := range out.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			out.Del(strings.TrimSpace(token))
		}
	}
	for _, key := range []string{
		"Connection",
		"Proxy-Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		out.Del(key)
	}
	return out
}

func stripMockPath(rawPath string) string {
	_, _, remaining := parseMockPath(rawPath)
	return remaining
}

func stripMockEscapedPath(escapedPath string) string {
	parts := strings.Split(strings.TrimPrefix(escapedPath, "/"), "/")
	decoded := make([]string, len(parts))
	for i, part := range parts {
		value, err := url.PathUnescape(part)
		if err != nil {
			return stripMockPath(escapedPath)
		}
		decoded[i] = value
	}
	idx := 0
	if len(decoded) > idx && decoded[idx] == "__phrond" {
		idx++
		if len(decoded) > idx+1 && decoded[idx] == "session" && strings.TrimSpace(decoded[idx+1]) != "" {
			idx += 2
		}
		if len(decoded) > idx+1 && decoded[idx] == "dep" && strings.TrimSpace(decoded[idx+1]) != "" {
			idx += 2
		}
	}
	if idx == 0 {
		return escapedPath
	}
	if len(parts) <= idx {
		return "/"
	}
	return "/" + strings.Join(parts[idx:], "/")
}

func joinGatewayPath(base, applicationPath string) string {
	base = strings.TrimRight(base, "/")
	if applicationPath == "" {
		applicationPath = "/"
	}
	if !strings.HasPrefix(applicationPath, "/") {
		applicationPath = "/" + applicationPath
	}
	return base + applicationPath
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

func (s *Server) forwardToUpstream(r *http.Request, trimmedPath string, body io.Reader, bodyBytes int64) (*http.Response, error) {
	target, err := url.Parse(s.fallbackUpstream)
	if err != nil {
		return nil, err
	}
	target.Path = strings.TrimSuffix(target.Path, "/") + trimmedPath
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header = http.Header(cloneHeaderDirect(r.Header))
	req.Host = target.Host
	req.ContentLength = r.ContentLength
	if req.ContentLength < 0 && len(r.Trailer) == 0 {
		req.ContentLength = bodyBytes
	}
	req.Trailer = http.Header(cloneHeader(r.Trailer))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
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
