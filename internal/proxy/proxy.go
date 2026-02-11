package proxy

import (
	"bufio"
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

	tlsmgr "github.com/phrond/evermock/internal/tls"
)

// Mode controls how HTTPS traffic is handled.
type Mode string

const (
	ModeMITM        Mode = "mitm"
	ModePassthrough Mode = "passthrough"
)

// Config configures the proxy server.
type Config struct {
	ListenAddr string
	Mode       Mode
	TLSManager *tlsmgr.Manager
	Recorder   Recorder
	// UpstreamTLSConfig allows customizing TLS verification for upstream servers (used in MITM mode).
	UpstreamTLSConfig *tls.Config
	IdleTimeout       time.Duration
	Logger            *log.Logger
}

// Info reports proxy runtime configuration.
type Info struct {
	ListenAddr string `json:"listen_addr"`
	Mode       Mode   `json:"mode"`
}

// Server is an explicit forward proxy with CONNECT + MITM support.
type Server struct {
	cfg        Config
	server     *http.Server
	listener   net.Listener
	shutdownCh chan struct{}
	once       sync.Once
}

// Recorder receives captured HTTP exchanges.
type Recorder interface {
	RecordHTTP(exchange HTTPExchange)
}

// HTTPExchange captures a proxied request/response pair.
type HTTPExchange struct {
	SessionID       string
	Method          string
	URL             string
	RequestHeaders  map[string][]string
	RequestBody     []byte
	Status          int
	ResponseHeaders map[string][]string
	ResponseBody    []byte
	StartedAt       time.Time
	Duration        time.Duration
	Error           string
}

var ErrAlreadyStarted = errors.New("proxy already started")

// NewServer returns a proxy server instance.
func NewServer(cfg Config) *Server {
	if cfg.Mode == "" {
		cfg.Mode = ModePassthrough
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Second
	}
	return &Server{
		cfg:        cfg,
		shutdownCh: make(chan struct{}),
	}
}

// Start begins listening for proxy traffic.
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

	s.listener = ln
	s.server = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       s.cfg.IdleTimeout,
	}

	shutdownCh := s.shutdownCh
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if s.cfg.Logger != nil {
				s.cfg.Logger.Printf("proxy serve error: %v", err)
			}
		}
		close(shutdownCh)
	}()

	return Info{ListenAddr: ln.Addr().String(), Mode: s.cfg.Mode}, nil
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

// SetListenAddr updates the bind address before the server starts.
func (s *Server) SetListenAddr(addr string) error {
	if s.server != nil {
		return ErrAlreadyStarted
	}
	s.cfg.ListenAddr = strings.TrimSpace(addr)
	return nil
}

// SetMode updates the proxy mode before the server starts.
func (s *Server) SetMode(mode Mode) error {
	if s.server != nil {
		return ErrAlreadyStarted
	}
	if mode == "" {
		return errors.New("mode must not be empty")
	}
	if mode == ModeMITM && s.cfg.TLSManager == nil {
		return errors.New("mitm mode requires tls manager")
	}
	s.cfg.Mode = mode
	return nil
}

// CACertPEM returns the CA certificate when running in MITM mode.
func (s *Server) CACertPEM() ([]byte, error) {
	if s.cfg.TLSManager == nil {
		return nil, nil
	}
	return s.cfg.TLSManager.CACertPEM()
}

// ServeHTTP handles proxy requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	s.handleHTTP(w, r)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	transport := s.httpTransport(nil)
	started := time.Now()

	reqBody, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(reqBody))

	req := r.Clone(r.Context())
	sessionID := req.Header.Get("X-Phrond-Session-Id")
	req.Header.Del("X-Phrond-Session-Id")
	if req.URL.Scheme == "" {
		req.URL.Scheme = "http"
	}
	if req.URL.Host == "" {
		req.URL.Host = req.Host
	}
	req.RequestURI = ""
	req.Header.Del("Proxy-Connection")

	resp, err := transport.RoundTrip(req)
	if err != nil {
		s.recordHTTP(HTTPExchange{
			SessionID:      sessionID,
			Method:         req.Method,
			URL:            req.URL.String(),
			RequestHeaders: cloneHeader(req.Header),
			RequestBody:    reqBody,
			StartedAt:      started,
			Duration:       time.Since(started),
			Error:          err.Error(),
		})
		writeProxyError(w, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	s.recordHTTP(HTTPExchange{
		SessionID:       sessionID,
		Method:          req.Method,
		URL:             req.URL.String(),
		RequestHeaders:  cloneHeader(req.Header),
		RequestBody:     reqBody,
		Status:          resp.StatusCode,
		ResponseHeaders: cloneHeader(resp.Header),
		ResponseBody:    respBody,
		StartedAt:       started,
		Duration:        time.Since(started),
	})

	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}

	if s.cfg.Mode == ModeMITM && s.cfg.TLSManager == nil {
		writeProxyError(w, http.StatusInternalServerError, errors.New("mitm mode requires tls manager"))
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeProxyError(w, http.StatusInternalServerError, errors.New("hijacking not supported"))
		return
	}

	clientConn, buf, err := hijacker.Hijack()
	if err != nil {
		writeProxyError(w, http.StatusServiceUnavailable, err)
		return
	}
	_ = buf.Flush()

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	switch s.cfg.Mode {
	case ModeMITM:
		s.mitmTunnel(clientConn, host)
	default:
		s.passthroughTunnel(clientConn, host)
	}
}

func (s *Server) passthroughTunnel(clientConn net.Conn, host string) {
	upstreamConn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		_ = clientConn.Close()
		return
	}

	proxyStream(clientConn, upstreamConn)
}

func (s *Server) mitmTunnel(clientConn net.Conn, hostport string) {
	host := hostOnly(hostport)
	cert, err := s.cfg.TLSManager.GetOrCreateCert(host)
	if err != nil {
		_ = clientConn.Close()
		return
	}

	clientTLS := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"http/1.1"},
	})
	if err := clientTLS.Handshake(); err != nil {
		_ = clientTLS.Close()
		return
	}

	s.proxyTLSRequests(clientTLS)
}

func (s *Server) proxyTLSRequests(clientTLS net.Conn) {
	defer func() {
		_ = clientTLS.Close()
	}()

	clientReader := bufio.NewReader(clientTLS)
	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			return
		}

		started := time.Now()
		reqBody, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))

		sessionID := req.Header.Get("X-Phrond-Session-Id")
		req.Header.Del("X-Phrond-Session-Id")
		req.URL.Scheme = "https"
		if req.URL.Host == "" {
			req.URL.Host = req.Host
		}
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")

		resp, err := s.httpTransport(s.upstreamTLSConfig(req.URL.Host)).RoundTrip(req)
		if err != nil {
			s.recordHTTP(HTTPExchange{
				SessionID:      sessionID,
				Method:         req.Method,
				URL:            req.URL.String(),
				RequestHeaders: cloneHeader(req.Header),
				RequestBody:    reqBody,
				StartedAt:      started,
				Duration:       time.Since(started),
				Error:          err.Error(),
			})
			return
		}

		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		s.recordHTTP(HTTPExchange{
			SessionID:       sessionID,
			Method:          req.Method,
			URL:             req.URL.String(),
			RequestHeaders:  cloneHeader(req.Header),
			RequestBody:     reqBody,
			Status:          resp.StatusCode,
			ResponseHeaders: cloneHeader(resp.Header),
			ResponseBody:    respBody,
			StartedAt:       started,
			Duration:        time.Since(started),
		})

		_ = resp.Write(clientTLS)
		resp.Body.Close()

		if req.Close || resp.Close {
			return
		}
	}
}

func (s *Server) httpTransport(tlsConfig *tls.Config) *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       tlsConfig,
		DisableCompression:    false,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func (s *Server) upstreamTLSConfig(hostport string) *tls.Config {
	host := hostOnly(hostport)
	base := &tls.Config{ServerName: host}
	if s.cfg.UpstreamTLSConfig == nil {
		return base
	}
	clone := s.cfg.UpstreamTLSConfig.Clone()
	clone.ServerName = host
	return clone
}

func proxyStream(a net.Conn, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
	}()

	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

func copyHeader(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func (s *Server) recordHTTP(exchange HTTPExchange) {
	if s.cfg.Recorder == nil {
		return
	}
	s.cfg.Recorder.RecordHTTP(exchange)
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

func writeProxyError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	_, _ = w.Write([]byte(fmt.Sprintf("proxy error: %v", err)))
}

func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}

// ProxyURL is a helper for configuring HTTP clients.
func ProxyURL(listenAddr string) (*url.URL, error) {
	if !strings.HasPrefix(listenAddr, "http") {
		listenAddr = "http://" + listenAddr
	}
	return url.Parse(listenAddr)
}
