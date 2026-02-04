package mock

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server implements an HTTP mock server with stub matching and recording.
type Server struct {
	registry *Registry
	recorder *Recorder
	renderer *Renderer

	mu       syncState
	listener net.Listener
	server   *http.Server
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
	return &Server{
		registry: registry,
		recorder: recorder,
		renderer: NewRenderer(),
	}
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

func (s *Server) RegisterStub(stub Stub) {
	s.registry.Register(stub)
}

func (s *Server) ClearStubs() {
	s.registry.Clear()
}

func (s *Server) Recorder() *Recorder {
	return s.recorder
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	bodyBytes, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	req := RecordedRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		URL:     r.URL.String(),
		Headers: cloneHeader(r.Header),
		Query:   cloneQuery(r.URL.Query()),
		Body:    string(bodyBytes),
	}

	stub, matched := s.registry.Match(r, bodyBytes)
	resp := RecordedResponse{
		Status:  http.StatusNotImplemented,
		Headers: map[string][]string{},
		Body:    "no matching stub",
	}
	var renderErr error
	if matched {
		resp, renderErr = s.renderer.RenderResponse(stub, req)
		if renderErr != nil {
			resp = RecordedResponse{
				Status:  http.StatusInternalServerError,
				Headers: map[string][]string{},
				Body:    "response template error",
			}
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
		Request:       req,
		Response:      resp,
		MatchedStubID: stub.ID,
		Matched:       matched,
		StartedAt:     started,
		FinishedAt:    time.Now(),
	}
	if renderErr != nil {
		exchange.Error = renderErr.Error()
	}
	s.recorder.Record(exchange)
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
