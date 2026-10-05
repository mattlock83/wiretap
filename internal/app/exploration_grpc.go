package app

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/capture"
	"github.com/phrond/wiretap/internal/explore"
	"github.com/phrond/wiretap/internal/project"
	"github.com/phrond/wiretap/internal/proxy"
	"github.com/phrond/wiretap/internal/tracecontext"
	"go.opentelemetry.io/otel/trace"
)

// invokeGRPC uses the same transport, admission tracking and decoder as an
// authored RPC. The engine owns the returned provider flow; dependency calls
// continue through the normal capture routes under the generated case trace.
func (c *explorationCapture) invokeGRPC(ctx context.Context, input explore.GRPCRequest) (artifact.Flow, error) {
	if c.child == nil {
		return artifact.Flow{}, errors.New("gRPC exploration requires an active generated case")
	}
	if err := ctx.Err(); err != nil {
		return artifact.Flow{}, err
	}
	if len(input.Messages) > capture.MaxGRPCMessageRecords || len(input.ResponseGates) != 0 && len(input.ResponseGates) != len(input.Messages) {
		return artifact.Flow{}, errors.New("invalid bounded gRPC replay sequence")
	}
	remaining := 16 << 20
	for i, message := range input.Messages {
		// A valid prefix plus a larger mutated message must still fit the
		// maximum capture budget, including each native frame envelope.
		if len(message) > remaining-5 {
			return artifact.Flow{}, errors.New("gRPC replay request exceeds 16 MiB")
		}
		remaining -= len(message) + 5
		if len(input.ResponseGates) > 0 && (input.ResponseGates[i] < 0 || input.ResponseGates[i] > capture.MaxGRPCMessageRecords) {
			return artifact.Flow{}, errors.New("invalid gRPC response gate")
		}
	}
	var api project.API
	if c.app.project != nil {
		api = c.app.project.SUT
	}
	transport, err := c.app.sharedGRPCTransport("", c.parent.SUTBaseURL, api)
	if err != nil {
		return artifact.Flow{}, err
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.parent.SUTBaseURL+input.Procedure, reader)
	if err != nil {
		return artifact.Flow{}, fmt.Errorf("construct gRPC exploration request: %w", err)
	}
	req.Header = input.Metadata.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	for name := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "grpc-") || strings.HasPrefix(lower, ":") || lower == "content-length" || lower == "x-wiretap-session-id" {
			delete(req.Header, name)
		}
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	sc := trace.SpanContextFromContext(ctx)
	tracecontext.Inject(req.Header, sc)
	req.Proto, req.ProtoMajor, req.ProtoMinor = "HTTP/2.0", 2, 0
	req.ContentLength = -1
	admission, err := c.app.admitRequest(req, "")
	if err != nil {
		return artifact.Flow{}, err
	}
	defer admission.Release()
	admission.GRPCFlowID = input.ID
	var result artifact.Flow
	var recorded bool
	var resultMu sync.Mutex
	admission.RecordGRPC = func(ex proxy.GRPCExchange) {
		ex.TraceID, ex.RequestSpanID = sc.TraceID().String(), sc.SpanID().String()
		ex.TraceContextSource, ex.TestCorrelation = "injected", "trace"
		flow := grpcFlow(ex, c.parent.Store.grpcContracts, c.seed.Schemas, c.app.cfg.Capture.MaxBodyBytes)
		resultMu.Lock()
		result, recorded = flow, true
		resultMu.Unlock()
	}
	sink := newExplorationGRPCSink()
	sent := make(chan error, 1)
	go func() {
		var sendErr error
		defer func() { _ = writer.CloseWithError(sendErr); sent <- sendErr }()
		for index, message := range input.Messages {
			if len(input.ResponseGates) > 0 {
				if sendErr = sink.wait(ctx, uint64(input.ResponseGates[index])); sendErr != nil {
					return
				}
			}
			var header [5]byte
			binary.BigEndian.PutUint32(header[1:], uint32(len(message)))
			if _, sendErr = writer.Write(header[:]); sendErr != nil {
				return
			}
			if _, sendErr = writer.Write(message); sendErr != nil {
				return
			}
		}
	}()
	proxy.ForwardGRPC(sink, req, c.parent.SUTBaseURL, "test_to_sut", "", admission, c.app.cfg.Capture.MaxBodyBytes, transport)
	close(sink.done)
	_ = reader.Close()
	sendErr := <-sent
	resultMu.Lock()
	defer resultMu.Unlock()
	if !recorded {
		return artifact.Flow{}, errors.New("gRPC exchange finished without capture evidence")
	}
	// An upstream rejection can end a stream before the sender finishes. Its
	// actual direction observations decide evidence completeness, not io.Pipe's
	// close error. A cancelled/deadline call always remains an execution error.
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if sendErr != nil && !errors.Is(sendErr, io.ErrClosedPipe) && !errors.Is(sendErr, errExplorationRPCEnded) {
		return result, sendErr
	}
	return result, nil
}

var errExplorationRPCEnded = errors.New("gRPC ended before the next captured request message")

// The response sink retains no payloads; ForwardGRPC already captures bounded
// evidence. Counting frames lets finite bidirectional seeds preserve observed
// receive-before-send dependencies without replaying wall-clock delays.
type explorationGRPCSink struct {
	headers  http.Header
	observer *capture.GRPCObserver
	mu       sync.Mutex
	frames   uint64
	invalid  bool
	changed  chan struct{}
	done     chan struct{}
}

func newExplorationGRPCSink() *explorationGRPCSink {
	return &explorationGRPCSink{headers: http.Header{}, observer: capture.NewGRPCObserver(nil, "response", 1, 1), changed: make(chan struct{}), done: make(chan struct{})}
}
func (w *explorationGRPCSink) Header() http.Header { return w.headers }
func (w *explorationGRPCSink) WriteHeader(int)     {}
func (w *explorationGRPCSink) Flush()              {}
func (w *explorationGRPCSink) Write(p []byte) (int, error) {
	n, _ := w.observer.Write(p)
	snapshot := w.observer.Snapshot()
	w.mu.Lock()
	if snapshot.CompleteFrames != w.frames || !snapshot.CountsComplete && !w.invalid {
		w.frames, w.invalid = snapshot.CompleteFrames, !snapshot.CountsComplete
		close(w.changed)
		w.changed = make(chan struct{})
	}
	w.mu.Unlock()
	return n, nil
}
func (w *explorationGRPCSink) wait(ctx context.Context, frames uint64) error {
	for {
		w.mu.Lock()
		ready, invalid, changed := w.frames >= frames, w.invalid, w.changed
		w.mu.Unlock()
		if invalid {
			return errors.New("invalid gRPC response framing")
		}
		if ready {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-w.done:
			return errExplorationRPCEnded
		}
	}
}
