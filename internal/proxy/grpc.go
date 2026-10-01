package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phrond/wiretap/internal/capture"
	"golang.org/x/net/http2"
)

// GRPCExchange retains bounded copies of the native gRPC frame stream. The
// stream forwarded to the application is never decoded or re-encoded.
type GRPCExchange struct {
	ID, CaseID, SessionID, Channel, AssetID                     string
	TraceID, RequestSpanID, TraceContextSource, TestCorrelation string
	Procedure                                                   string
	RequestHeaders, RequestTrailers                             map[string][]string
	ResponseHeaders, ResponseTrailers                           map[string][]string
	RequestFrames, ResponseFrames                               []byte
	RequestBytes, ResponseBytes                                 int64
	RequestTruncated, ResponseTruncated                         bool
	HTTPStatus, StatusCode                                      int
	StatusMessage, StatusSource                                 string
	StartedAt                                                   time.Time
	Duration                                                    time.Duration
	Error                                                       string
}

type flushingWriter struct{ w http.ResponseWriter }

func (f flushingWriter) Write(data []byte) (int, error) {
	n, err := f.w.Write(data)
	if flush, ok := f.w.(http.Flusher); ok {
		flush.Flush()
	}
	return n, err
}

func NewGRPCTransport(scheme string, upstreamTLS *tls.Config) *http2.Transport {
	if scheme == "http" {
		return &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
		}}
	}
	return &http2.Transport{TLSClientConfig: upstreamTLS}
}

// WriteGRPCError identifies local errors separately from upstream RPC status.
func WriteGRPCError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Grpc-Status", strconv.Itoa(code))
	w.Header().Set("Grpc-Message", url.PathEscape(message))
	w.Header().Set("X-Wiretap-Error", "proxy")
	w.WriteHeader(http.StatusOK)
}

// grpcCapture synchronizes snapshots with both transport goroutines and drain.
// Each RPC is committed once: either its terminal record or its drain snapshot.
type grpcCapture struct {
	mu                sync.Mutex
	once              sync.Once
	exchange          GRPCExchange
	request, response *capture.Writer
	requestEOF        atomic.Bool
	record            func(GRPCExchange)
}

func (c *grpcCapture) commit(reason string) {
	c.once.Do(func() {
		c.mu.Lock()
		ex := c.exchange
		c.mu.Unlock()
		request, response := c.request.Body(), c.response.Body()
		ex.RequestFrames, ex.RequestBytes = request.Bytes, request.OriginalBytes
		ex.ResponseFrames, ex.ResponseBytes = response.Bytes, response.OriginalBytes
		ex.RequestTruncated = ex.RequestTruncated || request.Truncated || !c.requestEOF.Load()
		ex.ResponseTruncated = ex.ResponseTruncated || response.Truncated || reason != ""
		if reason != "" {
			ex.Error, ex.StatusSource = reason, "proxy"
		}
		ex.Duration = time.Since(ex.StartedAt)
		if c.record != nil {
			c.record(ex)
		}
	})
}

type grpcRequestBody struct {
	io.ReadCloser
	capture         *grpcCapture
	trailers        *http.Header
	forwardTrailers *http.Header
}

func (b *grpcRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		_, _ = b.capture.request.Write(p[:n])
	}
	if err == io.EOF {
		if b.forwardTrailers != nil {
			*b.forwardTrailers = (*b.trailers).Clone()
		}
		b.capture.mu.Lock()
		prior := b.capture.exchange.RequestHeaders
		b.capture.mu.Unlock()
		headers, truncated := boundedGRPCHeadersAfter(prior, *b.trailers)
		b.capture.mu.Lock()
		b.capture.exchange.RequestTrailers = headers
		b.capture.exchange.RequestTruncated = b.capture.exchange.RequestTruncated || truncated
		b.capture.mu.Unlock()
		b.capture.requestEOF.Store(true)
	}
	return n, err
}

func ForwardGRPC(w http.ResponseWriter, r *http.Request, targetURL, channel, assetID string, admission *Admission, maxBodyBytes int64, transport *http2.Transport) {
	started := time.Now()
	// A zero/unlimited HTTP capture setting must not make a long-lived RPC unbounded.
	if maxBodyBytes <= 0 {
		maxBodyBytes = capture.DefaultMaxBodyBytes
	}
	if maxBodyBytes > 16<<20 {
		maxBodyBytes = 16 << 20
	}
	headers, truncated := boundedGRPCHeaders(r.Header)
	observer := &grpcCapture{exchange: GRPCExchange{ID: admission.GRPCFlowID, CaseID: admission.CaseID, SessionID: admission.SessionID, TestCorrelation: admission.TestCorrelation, Channel: channel, AssetID: assetID, Procedure: r.URL.Path, RequestHeaders: headers, RequestTruncated: truncated, StartedAt: started, StatusCode: -1}, request: capture.NewWriter(maxBodyBytes), response: capture.NewWriter(maxBodyBytes), record: admission.RecordGRPC}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if admission.TrackGRPC != nil {
		release := admission.TrackGRPC(cancel, func() { observer.commit("capture drain expired; RPC incomplete") })
		defer func() { observer.commit(""); release() }()
	} else {
		defer observer.commit("")
	}
	update := func(fn func(*GRPCExchange)) { observer.mu.Lock(); defer observer.mu.Unlock(); fn(&observer.exchange) }
	localError := func(code int, message string) {
		update(func(ex *GRPCExchange) { ex.Error, ex.StatusSource, ex.HTTPStatus = message, "proxy", http.StatusOK })
		WriteGRPCError(w, code, message)
	}
	if r.ProtoMajor != 2 {
		update(func(ex *GRPCExchange) { ex.Error = "native gRPC requires HTTP/2" })
		http.Error(w, "native gRPC requires HTTP/2", http.StatusHTTPVersionNotSupported)
		return
	}
	if r.Method != http.MethodPost {
		localError(3, "native gRPC requires POST")
		return
	}
	target, err := url.Parse(targetURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.Path != "" && target.Path != "/" {
		localError(14, "invalid upstream gRPC target")
		return
	}
	if value := r.Header.Get("Grpc-Timeout"); value != "" {
		timeout, err := parseGRPCTimeout(value)
		if err != nil {
			localError(3, err.Error())
			return
		}
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithDeadline(ctx, started.Add(timeout))
		defer deadlineCancel()
	}
	out := r.Clone(ctx)
	out.URL = &url.URL{Scheme: target.Scheme, Host: target.Host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
	out.Host, out.RequestURI, out.GetBody = target.Host, "", nil
	out.Body = &grpcRequestBody{ReadCloser: r.Body, capture: observer, trailers: &r.Trailer, forwardTrailers: &out.Trailer}
	defer out.Body.Close()
	out.Header = r.Header.Clone()
	stripGRPCHopHeaders(out.Header)
	out.Header.Del("X-Wiretap-Session-Id")
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			localError(4, "gRPC deadline exceeded before forwarding")
			return
		}
		out.Header.Set("Grpc-Timeout", encodeGRPCTimeout(remaining))
	}
	resp, err := transport.RoundTrip(out)
	if err != nil {
		code := 14
		if errors.Is(ctx.Err(), context.Canceled) {
			code = 1
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = 4
		}
		localError(code, "gRPC upstream: "+err.Error())
		return
	}
	defer resp.Body.Close()
	responseHeaders, responseTruncated := boundedGRPCHeaders(resp.Header)
	update(func(ex *GRPCExchange) {
		ex.HTTPStatus, ex.ResponseHeaders, ex.ResponseTruncated = resp.StatusCode, responseHeaders, responseTruncated
	})
	forwardedHeaders := resp.Header.Clone()
	stripGRPCHopHeaders(forwardedHeaders)
	copyHeader(w.Header(), forwardedHeaders)
	for name := range resp.Trailer {
		w.Header().Add("Trailer", name)
	}
	w.WriteHeader(resp.StatusCode)
	if resp.Header.Get("Grpc-Status") == "" {
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
	}
	_, copyErr := io.Copy(flushingWriter{w}, io.TeeReader(resp.Body, observer.response))
	for name, values := range resp.Trailer {
		for _, v := range values {
			w.Header().Add(http.TrailerPrefix+name, v)
		}
	}
	responseTrailers, trailersTruncated := boundedGRPCHeadersAfter(responseHeaders, resp.Trailer)
	code, source, statusErr := grpcTerminalStatus(resp.Header, resp.Trailer)
	statusMessage := http.Header(responseTrailers).Get("Grpc-Message")
	if statusMessage == "" {
		statusMessage = http.Header(responseHeaders).Get("Grpc-Message")
	}
	if len(statusMessage) > grpcMetadataBytes {
		statusMessage, trailersTruncated = statusMessage[:grpcMetadataBytes], true
	}
	update(func(ex *GRPCExchange) {
		ex.ResponseTrailers, ex.StatusCode, ex.StatusSource, ex.StatusMessage = responseTrailers, code, source, statusMessage
		ex.ResponseTruncated = ex.ResponseTruncated || trailersTruncated
		if statusErr != nil {
			ex.Error = statusErr.Error()
		}
		if copyErr != nil {
			ex.Error = copyErr.Error()
		}
	})
}

const grpcMetadataBytes = 64 << 10
const grpcMetadataEntries = 256

func boundedGRPCHeaders(headers http.Header) (map[string][]string, bool) {
	return boundedGRPCHeadersAfter(nil, headers)
}
func boundedGRPCHeadersAfter(prior map[string][]string, headers http.Header) (map[string][]string, bool) {
	result := map[string][]string{}
	bytes, entries := 0, 0
	for key, values := range prior {
		for _, value := range values {
			bytes += len(key) + len(value)
			entries++
		}
	}
	truncated := false
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		critical := func(key string) bool { return key == "Grpc-Encoding" || key == "Grpc-Status" || key == "Content-Type" }
		if critical(keys[i]) != critical(keys[j]) {
			return critical(keys[i])
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		values := headers[key]
		for _, value := range values {
			size := len(key) + len(value)
			if entries >= grpcMetadataEntries || bytes+size > grpcMetadataBytes {
				truncated = true
				continue
			}
			result[key] = append(result[key], value)
			bytes += size
			entries++
		}
	}
	return result, truncated
}
func stripGRPCHopHeaders(headers http.Header) {
	for _, named := range headers.Values("Connection") {
		for _, name := range strings.Split(named, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
}
func grpcTerminalStatus(headers, trailers http.Header) (int, string, error) {
	initial, terminal := headers.Values("Grpc-Status"), trailers.Values("Grpc-Status")
	source := "trailers"
	values := terminal
	if len(values) == 0 {
		values, source = initial, "headers"
	}
	if len(initial) > 0 && len(terminal) > 0 {
		return -1, source, fmt.Errorf("gRPC response has contradictory header and trailer status")
	}
	if len(values) != 1 {
		return -1, source, fmt.Errorf("gRPC response must include exactly one terminal grpc-status")
	}
	value := values[0]
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return -1, source, fmt.Errorf("invalid grpc-status %q", value)
	}
	code, err := strconv.Atoi(value)
	if err != nil || code < 0 || code > 16 {
		return -1, source, fmt.Errorf("invalid grpc-status %q", value)
	}
	return code, source, nil
}
func parseGRPCTimeout(value string) (time.Duration, error) {
	if len(value) < 2 || len(value) > 9 {
		return 0, fmt.Errorf("invalid grpc-timeout")
	}
	unit := map[byte]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second, 'm': time.Millisecond, 'u': time.Microsecond, 'n': time.Nanosecond}[value[len(value)-1]]
	digits := value[:len(value)-1]
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || strings.Trim(digits, "0123456789") != "" || unit == 0 || n < 0 {
		return 0, fmt.Errorf("invalid grpc-timeout")
	}
	if n > int64((1<<63-1)/unit) {
		return time.Duration(1<<63 - 1), nil
	}
	return time.Duration(n) * unit, nil
}
func encodeGRPCTimeout(duration time.Duration) string {
	for _, unit := range []struct {
		size   time.Duration
		suffix string
	}{{time.Nanosecond, "n"}, {time.Microsecond, "u"}, {time.Millisecond, "m"}, {time.Second, "S"}, {time.Minute, "M"}, {time.Hour, "H"}} {
		n := duration / unit.size
		if n < 100000000 {
			if n < 1 {
				n = 1
			}
			return strconv.FormatInt(int64(n), 10) + unit.suffix
		}
	}
	return "99999999H"
}
