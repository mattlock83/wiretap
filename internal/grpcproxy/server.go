package grpcproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/phrond/evermock/internal/grpcwire"
	tlsmgr "github.com/phrond/evermock/internal/tls"
)

// Config configures the gRPC proxy server.
type Config struct {
	ListenAddr         string
	TargetAddr         string
	InsecureSkipVerify bool
	TLSManager         *tlsmgr.Manager
	Recorder           Recorder
	Logger             *log.Logger
}

// Info reports proxy runtime configuration.
type Info struct {
	ListenAddr         string `json:"listen_addr"`
	TargetAddr         string `json:"target_addr,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

// Recorder receives captured gRPC exchanges.
type Recorder interface {
	RecordGRPC(exchange Exchange)
}

// Exchange captures a proxied gRPC request/response pair.
type Exchange struct {
	Path                string
	Service             string
	Method              string
	Authority           string
	RequestHeaders      map[string][]string
	RequestContentType  string
	RequestBody         []byte
	ResponseHeaders     map[string][]string
	ResponseTrailers    map[string][]string
	ResponseContentType string
	ResponseBody        []byte
	Status              int
	StatusMessage       string
	StartedAt           time.Time
	Duration            time.Duration
	Error               string
}

// Server proxies gRPC requests to a configured target.
type Server struct {
	cfg        Config
	server     *http.Server
	listener   net.Listener
	shutdownCh chan struct{}
	once       sync.Once

	mu     sync.RWMutex
	target *url.URL
	client *http.Client
}

var ErrAlreadyStarted = errors.New("grpc proxy already started")

// NewServer returns a proxy server instance.
func NewServer(cfg Config) *Server {
	s := &Server{
		cfg:        cfg,
		shutdownCh: make(chan struct{}),
	}
	if cfg.TargetAddr != "" {
		_ = s.SetTarget(cfg.TargetAddr)
	}
	s.configureClient(cfg.InsecureSkipVerify)
	return s
}

// Start begins listening for gRPC traffic.
func (s *Server) Start(ctx context.Context) (Info, error) {
	if s.server != nil {
		return Info{}, ErrAlreadyStarted
	}
	addr := s.cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return Info{}, fmt.Errorf("listen: %w", err)
	}

	tlsConfig, err := s.serverTLSConfig(hostFromAddr(addr))
	if err != nil {
		return Info{}, err
	}
	listener := tls.NewListener(ln, tlsConfig)

	s.listener = listener
	s.server = &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: 10 * time.Second,
	}

	shutdownCh := s.shutdownCh
	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if s.cfg.Logger != nil {
				s.cfg.Logger.Printf("grpc proxy serve error: %v", err)
			}
		}
		close(shutdownCh)
	}()

	info := s.Info()
	if info.ListenAddr == "" {
		info.ListenAddr = listener.Addr().String()
	}
	return info, nil
}

// Stop gracefully stops the proxy server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	var err error
	s.once.Do(func() {
		err = s.server.Shutdown(ctx)
	})
	shutdownCh := s.shutdownCh
	if shutdownCh != nil {
		select {
		case <-shutdownCh:
		case <-ctx.Done():
		}
	}
	s.server = nil
	s.listener = nil
	s.shutdownCh = make(chan struct{})
	s.once = sync.Once{}
	return err
}

// ListenAddr returns the bound address if the listener is active.
func (s *Server) ListenAddr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Info returns current proxy configuration.
func (s *Server) Info() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info := Info{ListenAddr: s.ListenAddr(), InsecureSkipVerify: s.cfg.InsecureSkipVerify}
	if s.target != nil {
		info.TargetAddr = s.target.Host
	}
	return info
}

// SetTarget updates the upstream target address.
func (s *Server) SetTarget(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("target is required")
	}
	parsed := raw
	if !strings.Contains(raw, "://") {
		parsed = "https://" + raw
	}
	urlVal, err := url.Parse(parsed)
	if err != nil {
		return fmt.Errorf("parse target: %w", err)
	}
	if urlVal.Host == "" {
		return errors.New("target host missing")
	}

	s.mu.Lock()
	s.target = urlVal
	s.mu.Unlock()
	return nil
}

// SetInsecureSkipVerify updates TLS verification behavior for upstream connections.
func (s *Server) SetInsecureSkipVerify(skip bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.InsecureSkipVerify = skip
	s.configureClient(skip)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
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

	started := time.Now()
	resp, err := s.forward(r, bodyBytes)
	if err != nil {
		s.record(Exchange{
			Path:               r.URL.Path,
			Service:            service,
			Method:             method,
			Authority:          r.Host,
			RequestHeaders:     cloneHeader(r.Header),
			RequestContentType: contentType,
			RequestBody:        payload,
			StartedAt:          started,
			Duration:           time.Since(started),
			Error:              err.Error(),
		})
		writeGRPCError(w, http.StatusBadGateway, 14, "upstream error")
		return
	}
	defer resp.Body.Close()

	respBodyBytes, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	respPayload := unwrapPayload(respBodyBytes)

	statusCode, statusMessage := grpcStatus(resp)

	responseHeaders := cloneHeader(resp.Header)
	responseTrailers := cloneHeader(resp.Trailer)

	s.record(Exchange{
		Path:                r.URL.Path,
		Service:             service,
		Method:              method,
		Authority:           r.Host,
		RequestHeaders:      cloneHeader(r.Header),
		RequestContentType:  contentType,
		RequestBody:         payload,
		ResponseHeaders:     responseHeaders,
		ResponseTrailers:    responseTrailers,
		ResponseContentType: resp.Header.Get("Content-Type"),
		ResponseBody:        respPayload,
		Status:              statusCode,
		StatusMessage:       statusMessage,
		StartedAt:           started,
		Duration:            time.Since(started),
	})

	writeGRPCResponse(w, resp.StatusCode, resp.Header, resp.Trailer, respBodyBytes)
}

func (s *Server) forward(original *http.Request, body []byte) (*http.Response, error) {
	s.mu.RLock()
	target := s.target
	client := s.client
	s.mu.RUnlock()
	if target == nil {
		return nil, errors.New("target not configured")
	}

	outReq := original.Clone(original.Context())
	outReq.Body = io.NopCloser(bytes.NewReader(body))
	outReq.ContentLength = int64(len(body))
	outReq.RequestURI = ""
	outReq.URL = target.ResolveReference(original.URL)
	outReq.Host = target.Host
	return client.Do(outReq)
}

func (s *Server) configureClient(skipVerify bool) {
	transport := &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: skipVerify},
	}
	s.client = &http.Client{Transport: transport}
}

func (s *Server) serverTLSConfig(host string) (*tls.Config, error) {
	if s.cfg.TLSManager == nil {
		return nil, errors.New("tls manager required")
	}
	cert, err := s.cfg.TLSManager.GetOrCreateCert(host)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func (s *Server) record(exchange Exchange) {
	if s.cfg.Recorder == nil {
		return
	}
	s.cfg.Recorder.RecordGRPC(exchange)
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

func grpcStatus(resp *http.Response) (int, string) {
	statusVal := headerValue(resp.Trailer, "grpc-status")
	message := headerValue(resp.Trailer, "grpc-message")
	if statusVal == "" {
		statusVal = headerValue(resp.Header, "grpc-status")
		if message == "" {
			message = headerValue(resp.Header, "grpc-message")
		}
	}
	if statusVal == "" {
		return 0, message
	}
	var status int
	_, _ = fmt.Sscanf(statusVal, "%d", &status)
	return status, message
}

func headerValue(headers map[string][]string, key string) string {
	for k, values := range headers {
		if strings.EqualFold(k, key) {
			if len(values) > 0 {
				return values[0]
			}
			return ""
		}
	}
	return ""
}

func writeGRPCResponse(w http.ResponseWriter, statusCode int, headers http.Header, trailers http.Header, body []byte) {
	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	for key := range trailers {
		w.Header().Add("Trailer", key)
	}
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
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
	w.Header().Set("Grpc-Status", fmt.Sprintf("%d", grpcStatus))
	w.Header().Set("Grpc-Message", message)
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
