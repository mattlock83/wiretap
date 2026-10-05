package bundle

import (
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
)

func grpcObservedBundleFixture(t *testing.T) (string, Attempt, Run) {
	t.Helper()
	root, attempt, run := grpcBundleFixture(t)
	flow := &attempt.Capture.Traffic[0]
	var seq uint64
	for _, side := range []struct {
		phase   string
		message *artifact.GrpcMessage
	}{{"request", flow.Request.GRPC}, {"response", flow.Response.GRPC}} {
		var size int
		for _, ref := range attempt.Bodies {
			if ref.Phase == side.phase && ref.Kind == "wire" {
				size = ref.RetainedBytes
			}
		}
		msg := side.message
		msg.StreamKind = "unary"
		msg.Messages[0].DecodeState = "decoded"
		msg.Messages[0].Wire.OriginalBytes = int64(size)
		msg.Messages[0].Observation = &artifact.GrpcPayloadObservation{WireBytes: uint64(size), StartSequence: seq, CompleteSequence: seq + 1, StartedOffsetNS: int64(seq), CompletedOffsetNS: int64(seq + 1)}
		msg.ObservedBytes = int64(size + 5)
		msg.Observation = &artifact.GrpcObservation{CompleteFrames: 1, RetainedFrames: 1, ObservedBytes: msg.ObservedBytes, CountsComplete: true, EOF: true, End: "eof"}
		seq += 2
	}
	attempt.RequiredCapabilities = []string{grpcObservationCapability}
	run.RequiredCapabilities = []string{grpcObservationCapability}
	rewriteGRPCAttempt(t, root, attempt, run)
	return root, attempt, run
}

func TestGRPCObservationRoundTripAndTampering(t *testing.T) {
	root, _, _ := grpcObservedBundleFixture(t)
	if _, captures, err := Load(root); err != nil {
		t.Fatal(err)
	} else if captures[0].Traffic[0].Request.GRPC.Observation.CompleteFrames != 1 {
		t.Fatal("lost observed counts")
	}
	for name, change := range map[string]func(*Attempt){
		"frame counts": func(a *Attempt) { a.Capture.Traffic[0].Request.GRPC.Observation.CompleteFrames++ },
		"bytes":        func(a *Attempt) { a.Capture.Traffic[0].Request.GRPC.Observation.ObservedBytes++ },
		"made-up bytes": func(a *Attempt) {
			m := a.Capture.Traffic[0].Request.GRPC
			m.Observation.ObservedBytes++
			m.ObservedBytes++
		},
		"duplicate sequence":   func(a *Attempt) { a.Capture.Traffic[0].Response.GRPC.Messages[0].Observation.StartSequence = 0 },
		"time reversal":        func(a *Attempt) { a.Capture.Traffic[0].Request.GRPC.Messages[0].Observation.CompletedOffsetNS = -1 },
		"wire length":          func(a *Attempt) { a.Capture.Traffic[0].Response.GRPC.Messages[0].Observation.WireBytes++ },
		"missing decode state": func(a *Attempt) { a.Capture.Traffic[0].Request.GRPC.Messages[0].DecodeState = "" },
		"unverified projection": func(a *Attempt) {
			p := &a.Capture.Traffic[0].Request.GRPC.Messages[0]
			p.DecodeState, p.DecodeError = "unavailable", "unknown compression"
		},
		"complete partial frame": func(a *Attempt) {
			m := a.Capture.Traffic[0].Response.GRPC
			m.ObservedBytes += 5
			m.Observation.ObservedBytes += 5
			m.Observation.Partial = &artifact.GrpcPartialFrame{Index: 1, HeaderBytes: 5, StartSequence: 4, StartedOffsetNS: 4}
		},
		"missing direction": func(a *Attempt) { a.Capture.Traffic[0].Response.GRPC.Observation = nil },
		"invented omission": func(a *Attempt) {
			a.Capture.Traffic[0].Request.GRPC.Observation.Omitted = &artifact.GrpcOmission{FirstIndex: 0, Count: 1}
		},
		"missing capability": func(a *Attempt) { a.RequiredCapabilities = []string{} },
		"stream kind":        func(a *Attempt) { a.Capture.Traffic[0].Request.GRPC.StreamKind = "bidirectional" },
	} {
		t.Run(name, func(t *testing.T) {
			root, attempt, run := grpcObservedBundleFixture(t)
			change(&attempt)
			rewriteGRPCAttempt(t, root, attempt, run)
			if _, _, err := Load(root); err == nil {
				t.Fatal("accepted inconsistent observations")
			}
		})
	}
}

func TestGRPCOmittedFramesRemainExplicit(t *testing.T) {
	root, attempt, run := grpcObservedBundleFixture(t)
	m := attempt.Capture.Traffic[0].Response.GRPC
	m.ObservedBytes += 15
	m.Observation.ObservedBytes = m.ObservedBytes
	m.Observation.CompleteFrames = 4
	m.Observation.OmittedFrames = 3
	m.Observation.Omitted = &artifact.GrpcOmission{FirstIndex: 1, Count: 3}
	m.CaptureTruncated = true
	rewriteGRPCAttempt(t, root, attempt, run)
	_, captures, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded := captures[0].Traffic[0].Response.GRPC
	if len(loaded.Messages) != 1 || loaded.Observation.CompleteFrames != 4 || artifact.GRPCDirectionComplete(loaded, true) {
		t.Fatal("omissions became fabricated messages or complete assurance")
	}
}
