package grpcmock

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phrond/evermock/internal/grpcwire"
	tlsmgr "github.com/phrond/evermock/internal/tls"
)

// Server implements a gRPC mock server with stub matching and recording.
type Server struct {
	registry   *Registry
	recorder   *Recorder
	tlsManager *tlsmgr.Manager

	mu       syncState
	listener net.Listener
	server   *http.Server
}

type syncState struct {
	mu      sync.Mutex
	running bool
}

// NewServer creates a mock server using the provided registry/recorder.
func NewServer(registry *Registry, recorder *Recorder, tlsManager *tlsmgr.Manager) *Server {
	if registry == nil {
		registry = NewRegistry()
	}
	if recorder == nil {
		recorder = NewRecorder()
	}
	return &Server{
		registry:   registry,
		recorder:   recorder,
		tlsManager: tlsManager,
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

	tlsConfig, err := s.serverTLSConfig(hostFromAddr(addr))
	if err != nil {
		return "", err
	}
	listener := tls.NewListener(ln, tlsConfig)

	s.listener = listener
	s.server = &http.Server{Handler: http.HandlerFunc(s.handle)}
	s.mu.running = true

	go func() {
		_ = s.server.Serve(listener)
	}()

	return listener.Addr().String(), nil
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
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	started := time.Now()
	if r.Method != http.MethodPost {
		writeGRPCError(w, http.StatusMethodNotAllowed, 12, "method not allowed")
		return
	}
	contentType := r.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/grpc") {
		writeGRPCError(w, http.StatusUnsupportedMediaType, 12, "invalid content-type")
		return
	}
	bodyBytes, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	payload := unwrapPayload(bodyBytes)
	service, method := grpcwire.ParsePath(r.URL.Path)

	req := RecordedRequest{
		Path:        r.URL.Path,
		Service:     service,
		Method:      method,
		Authority:   r.Host,
		ContentType: contentType,
		Headers:     cloneHeader(r.Header),
		Body:        payload,
	}

	stub, matched := s.registry.Match(req)
	resp := RecordedResponse{
		Status:      12,
		Headers:     map[string][]string{},
		Trailers:    map[string][]string{},
		ContentType: "application/grpc",
	}
	body := []byte{}
	var renderErr error

	if matched {
		resp.Status = stub.Reply.Status
		resp.StatusMessage = stub.Reply.StatusMessage
		resp.Headers = stub.Reply.Headers
		resp.Trailers = stub.Reply.Trailers
		if stub.Reply.BodyBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(stub.Reply.BodyBase64)
			if err != nil {
				renderErr = err
				resp.Status = 13
				resp.StatusMessage = "invalid response body"
			} else {
				resp.Body = decoded
			}
		}
	}

	if resp.Status == 0 {
		resp.Status = 0
	}

	if len(resp.Body) > 0 {
		body = grpcwire.EncodeFrame(resp.Body)
	}

	writeGRPCStubResponse(w, resp, body)

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

func writeGRPCStubResponse(w http.ResponseWriter, resp RecordedResponse, body []byte) {
	for key, values := range resp.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Content-Type", "application/grpc")

	trailers := cloneHeader(resp.Trailers)
	trailers["Grpc-Status"] = []string{strconv.Itoa(resp.Status)}
	if resp.StatusMessage != "" {
		trailers["Grpc-Message"] = []string{resp.StatusMessage}
	}

	for key := range trailers {
		w.Header().Add("Trailer", key)
	}

	w.WriteHeader(http.StatusOK)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
	for key, values := range trailers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

func writeGRPCError(w http.ResponseWriter, httpStatus int, grpcStatus int, message string) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Add("Trailer", "Grpc-Status")
	w.Header().Add("Trailer", "Grpc-Message")
	w.WriteHeader(httpStatus)
	w.Header().Set("Grpc-Status", strconv.Itoa(grpcStatus))
	w.Header().Set("Grpc-Message", message)
}

func unwrapPayload(body []byte) []byte {
	frames, err := grpcwire.DecodeFrames(body)
	if err != nil {
		return body
	}
	payload := grpcwire.JoinMessages(frames)
	if len(payload) == 0 {
		return body
	}
	return payload
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

func (s *Server) serverTLSConfig(host string) (*tls.Config, error) {
	if s.tlsManager == nil {
		return nil, errors.New("tls manager required")
	}
	cert, err := s.tlsManager.GetOrCreateCert(host)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func hostFromAddr(addr string) string {
	if addr == "" {
		return "127.0.0.1"
	}
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1"
	}
	if host == "" || host == "0.0.0.0" {
		return "127.0.0.1"
	}
	return host
}
