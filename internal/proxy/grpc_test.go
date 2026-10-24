package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/capture"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestGRPCTerminalStatusValidation(t *testing.T) {
	for _, tc := range []struct {
		name              string
		initial, terminal []string
		code              int
		invalid           bool
	}{
		{name: "success trailers", terminal: []string{"0"}, code: 0},
		{name: "trailers only", initial: []string{"7"}, code: 7},
		{name: "missing", invalid: true},
		{name: "non numeric", terminal: []string{"bad"}, invalid: true},
		{name: "range", terminal: []string{"17"}, invalid: true},
		{name: "negative", terminal: []string{"-1"}, invalid: true},
		{name: "contradictory", initial: []string{"0"}, terminal: []string{"7"}, invalid: true},
		{name: "duplicate", terminal: []string{"0", "0"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, err := grpcTerminalStatus(http.Header{"Grpc-Status": tc.initial}, http.Header{"Grpc-Status": tc.terminal})
			if (err != nil) != tc.invalid || !tc.invalid && code != tc.code {
				t.Fatalf("code=%d error=%v", code, err)
			}
		})
	}
}

func TestGRPCTimeoutNeverExtendsDeadline(t *testing.T) {
	for _, duration := range []time.Duration{time.Nanosecond, 25 * time.Millisecond, 99 * time.Second, 24 * time.Hour} {
		encoded := encodeGRPCTimeout(duration)
		decoded, err := parseGRPCTimeout(encoded)
		if err != nil || decoded > duration || decoded <= 0 {
			t.Fatalf("%s -> %s -> %s, %v", duration, encoded, decoded, err)
		}
	}
	for _, invalid := range []string{"", "0", "123456789S", "+1S", "1s", "1.2S"} {
		if _, err := parseGRPCTimeout(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestGRPCMetadataBudgetIncludesTrailers(t *testing.T) {
	initial, truncated := boundedGRPCHeaders(http.Header{"Large": {strings.Repeat("x", grpcMetadataBytes-10)}})
	if truncated {
		t.Fatal("initial headers should fit")
	}
	trailers, truncated := boundedGRPCHeadersAfter(initial, http.Header{"Trailer": {strings.Repeat("x", 100)}})
	if !truncated || len(trailers) != 0 {
		t.Fatalf("trailers escaped direction budget: %v", trailers)
	}
}

func TestGRPCForwardsTrailersAndFramesWithoutModification(t *testing.T) {
	requestFrame := []byte{0, 0, 0, 0, 2, 8, 1}
	responseFrame := []byte{0, 0, 0, 0, 2, 8, 2}
	upstream := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if got := r.Trailer.Get("Request-Bin"); got != "YWJj" {
			t.Errorf("request trailer lost: %q", got)
		}
		if !bytes.Equal(data, requestFrame) {
			t.Errorf("request changed: %x", data)
		}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Add("Trailer", "Grpc-Status")
		w.Header().Add("Trailer", "Example-Bin")
		w.Write(responseFrame)
		w.Header().Set("Grpc-Status", "0")
		w.Header().Add("Example-Bin", "YWJj")
		w.Header().Add("Example-Bin", "ZGVm")
	}), &http2.Server{}))
	defer upstream.Close()
	transport := NewGRPCTransport("http", nil)
	defer transport.CloseIdleConnections()
	var exchange GRPCExchange
	req := httptest.NewRequest("POST", "http://capture/example.Service/Call", bytes.NewReader(requestFrame))
	req.ProtoMajor = 2
	req.Trailer = http.Header{"Request-Bin": nil}
	req.Body = &trailerBody{Reader: bytes.NewReader(requestFrame), trailers: &req.Trailer}
	req.Header.Set("Content-Type", "application/grpc")
	response := httptest.NewRecorder()
	ForwardGRPC(response, req, upstream.URL, "test_to_sut", "", &Admission{RecordGRPC: func(ex GRPCExchange) { exchange = ex }}, 4<<20, transport)
	if !bytes.Equal(response.Body.Bytes(), responseFrame) || exchange.StatusCode != 0 || exchange.Error != "" || exchange.RequestTruncated {
		t.Fatalf("response=%x exchange=%#v", response.Body.Bytes(), exchange)
	}
	if exchange.RequestObservation == nil || exchange.ResponseObservation == nil || !exchange.RequestObservation.EOF || !exchange.ResponseObservation.EOF || exchange.RequestObservation.CompleteFrames != 1 || exchange.ResponseObservation.CompleteFrames != 1 {
		t.Fatalf("missing complete observations: %#v", exchange)
	}
	if !bytes.Equal(exchange.RequestObservation.Frames[0].Payload, requestFrame[5:]) || !bytes.Equal(exchange.ResponseObservation.Frames[0].Payload, responseFrame[5:]) {
		t.Fatal("observer changed payloads")
	}
	if got := response.Result().Trailer.Values("Example-Bin"); len(got) != 2 {
		t.Fatalf("lost binary trailers: %v", got)
	}
}

func TestGRPCEarlyResponseClosesRequestAndRetainsSafeSnapshot(t *testing.T) {
	upstream := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first := make([]byte, 1)
		_, _ = r.Body.Read(first)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "7")
	}), &http2.Server{}))
	defer upstream.Close()
	transport := NewGRPCTransport("http", nil)
	defer transport.CloseIdleConnections()
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		for {
			if _, err := writer.Write(make([]byte, 1024)); err != nil {
				return
			}
		}
	}()
	req := httptest.NewRequest("POST", "http://capture/example.Service/Call", reader)
	req.ProtoMajor = 2
	req.Header.Set("Content-Type", "application/grpc")
	var exchange GRPCExchange
	ForwardGRPC(httptest.NewRecorder(), req, upstream.URL, "test_to_sut", "", &Admission{RecordGRPC: func(ex GRPCExchange) { exchange = ex }}, 1024, transport)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("upstream response did not close request body")
	}
	if exchange.RequestObservation == nil || exchange.RequestObservation.EOF {
		t.Fatal("early response invented request half-close")
	}
	retainedBytes := 0
	for _, frame := range exchange.RequestObservation.Frames {
		retainedBytes += 5 + len(frame.Payload)
	}
	if partial := exchange.RequestObservation.Partial; partial != nil {
		retainedBytes += len(partial.Header) + len(partial.Payload)
	}
	if !exchange.RequestTruncated || retainedBytes > 1024 {
		t.Fatalf("early response snapshot: %d retained bytes, %#v", retainedBytes, exchange)
	}
}

func TestGRPCRequestCloseWaitsForEOFObservation(t *testing.T) {
	clock := capture.NewGRPCClock(time.Now())
	observer := &grpcCapture{clock: clock, request: capture.NewGRPCObserver(clock, "request", 1024, 10)}
	source := &gatedGRPCRequestBody{read: make(chan struct{}), closed: make(chan struct{}), eof: true}
	trailers := http.Header{"Example-Bin": {"YWJj"}}
	body := &grpcRequestBody{ReadCloser: source, capture: observer, trailers: &trailers}

	// Pause observation after the underlying reader returns EOF. The HTTP/2
	// transport can close the request concurrently after receiving its response.
	observer.mu.Lock()
	readDone := make(chan error, 1)
	go func() {
		_, err := body.Read(make([]byte, 1))
		readDone <- err
	}()
	<-source.read
	closeDone := make(chan error, 1)
	go func() { closeDone <- body.Close() }()
	<-source.closed
	closedBeforeObservation := false
	select {
	case <-closeDone:
		closedBeforeObservation = true
	case <-time.After(25 * time.Millisecond):
	}
	observer.mu.Unlock()
	if err := <-readDone; err != io.EOF {
		t.Fatalf("request read = %v", err)
	}
	if !closedBeforeObservation {
		if err := <-closeDone; err != nil {
			t.Fatal(err)
		}
	}
	if observation := observer.request.Snapshot(); !observation.EOF || observation.End != "eof" {
		t.Fatalf("concurrent close lost observed request EOF: %+v", observation)
	}
	if observer.exchange.RequestTrailers["Example-Bin"][0] != "YWJj" {
		t.Fatal("concurrent close lost observed request trailers")
	}
}

func TestGRPCRequestCloseUnblocksActiveReadWithoutInventingEOF(t *testing.T) {
	clock := capture.NewGRPCClock(time.Now())
	observer := &grpcCapture{clock: clock, request: capture.NewGRPCObserver(clock, "request", 1024, 10)}
	source := &gatedGRPCRequestBody{read: make(chan struct{}), closed: make(chan struct{})}
	body := &grpcRequestBody{ReadCloser: source, capture: observer}
	readDone := make(chan error, 1)
	go func() {
		_, err := body.Read(make([]byte, 1))
		readDone <- err
	}()
	<-source.read
	closeDone := make(chan error, 1)
	go func() { closeDone <- body.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing request did not release its blocked read")
	}
	if err := <-readDone; err != io.ErrClosedPipe {
		t.Fatalf("interrupted request read = %v", err)
	}
	if observation := observer.request.Snapshot(); observation.EOF || observation.End == "open" {
		t.Fatalf("closing an open request invented EOF or left capture open: %+v", observation)
	}
}

type gatedGRPCRequestBody struct {
	read, closed chan struct{}
	eof          bool
}

func (b *gatedGRPCRequestBody) Read([]byte) (int, error) {
	close(b.read)
	if b.eof {
		return 0, io.EOF
	}
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *gatedGRPCRequestBody) Close() error {
	close(b.closed)
	return nil
}

func TestGRPCSnapshotIsCommittedOnceBeforeCancellation(t *testing.T) {
	calls := 0
	observer := &grpcCapture{exchange: GRPCExchange{StartedAt: time.Now()}, request: nil, response: nil, record: func(ex GRPCExchange) {
		calls++
		if ex.Error == "" {
			t.Fatal("snapshot lacks local termination reason")
		}
	}}
	observer.commit("capture drain expired")
	observer.commit("")
	if calls != 1 {
		t.Fatalf("committed %d times", calls)
	}
}

type trailerBody struct {
	*bytes.Reader
	trailers *http.Header
}

func (b *trailerBody) Read(data []byte) (int, error) {
	n, err := b.Reader.Read(data)
	if err == io.EOF {
		(*b.trailers).Set("Request-Bin", "YWJj")
	}
	return n, err
}
func (b *trailerBody) Close() error { return nil }

func TestGRPCBidirectionalObservationPreservesForwardingAndInterleaving(t *testing.T) {
	requestFrames := [][]byte{{0, 0, 0, 0, 1, 1}, {0, 0, 0, 0, 1, 2}}
	responseFrames := [][]byte{{0, 0, 0, 0, 1, 3}, {0, 0, 0, 0, 1, 4}}
	upstream := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		for i := range requestFrames {
			frame := make([]byte, len(requestFrames[i]))
			if _, err := io.ReadFull(r.Body, frame); err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(frame, requestFrames[i]) {
				t.Errorf("changed request %x", frame)
			}
			if _, err := w.Write(responseFrames[i]); err != nil {
				t.Error(err)
				return
			}
			w.(http.Flusher).Flush()
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Grpc-Status", "0")
	}), &http2.Server{}))
	defer upstream.Close()
	transport := NewGRPCTransport("http", nil)
	defer transport.CloseIdleConnections()
	reader, writer := io.Pipe()
	downstream := &signalGRPCResponseWriter{ResponseRecorder: httptest.NewRecorder(), first: make(chan struct{})}
	sent := make(chan error, 1)
	go func() {
		defer writer.Close()
		if _, err := writer.Write(requestFrames[0]); err != nil {
			sent <- err
			return
		}
		select {
		case <-downstream.first:
		case <-time.After(5 * time.Second):
			sent <- io.ErrNoProgress
			return
		}
		_, err := writer.Write(requestFrames[1])
		sent <- err
	}()
	request := httptest.NewRequest("POST", "http://capture/example.Service/Chat", reader)
	request.ProtoMajor = 2
	request.Header.Set("Content-Type", "application/grpc")
	var exchange GRPCExchange
	ForwardGRPC(downstream, request, upstream.URL, "test_to_sut", "", &Admission{RecordGRPC: func(ex GRPCExchange) { exchange = ex }}, 1024, transport)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if exchange.Error != "" || exchange.StatusCode != 0 || exchange.RequestTruncated || exchange.ResponseTruncated {
		t.Fatalf("%+v", exchange)
	}
	req, res := exchange.RequestObservation, exchange.ResponseObservation
	if req == nil || res == nil || req.CompleteFrames != 2 || res.CompleteFrames != 2 || !req.EOF || !res.EOF {
		t.Fatalf("request=%+v response=%+v", req, res)
	}
	if req.Frames[0].CompleteSequence >= res.Frames[0].StartSequence || res.Frames[0].CompleteSequence >= req.Frames[1].StartSequence {
		t.Fatalf("interleaving lost: request=%+v response=%+v", req.Frames, res.Frames)
	}
	if !bytes.Equal(downstream.Body.Bytes(), append(responseFrames[0], responseFrames[1]...)) {
		t.Fatal("forwarded response changed")
	}
	foundHalfClose, foundTrailers := false, false
	for _, event := range exchange.Events {
		if event.Direction == "request" && event.Kind == "half_close" {
			foundHalfClose = true
		}
		if event.Direction == "response" && event.Kind == "trailers" {
			foundTrailers = true
		}
	}
	if !foundHalfClose || !foundTrailers {
		t.Fatalf("lifecycle lost: %+v", exchange.Events)
	}
}

type signalGRPCResponseWriter struct {
	*httptest.ResponseRecorder
	first chan struct{}
	once  sync.Once
}

func (w *signalGRPCResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	w.once.Do(func() { close(w.first) })
	return n, err
}
