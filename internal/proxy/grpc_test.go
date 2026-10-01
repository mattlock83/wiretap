package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if !exchange.RequestTruncated || len(exchange.RequestFrames) > 1024 {
		t.Fatalf("early response snapshot: %#v", exchange)
	}
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
