package capture

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func grpcFrame(flag byte, payload []byte) []byte {
	data := make([]byte, 5+len(payload))
	data[0] = flag
	binary.BigEndian.PutUint32(data[1:], uint32(len(payload)))
	copy(data[5:], payload)
	return data
}
func TestGRPCObserverArbitraryChunks(t *testing.T) {
	input := append(grpcFrame(0, []byte("one")), grpcFrame(1, []byte("two"))...)
	input = append(input, grpcFrame(0, nil)...)
	for chunk := 1; chunk <= len(input); chunk++ {
		observer := NewGRPCObserver(nil, "request", 1024, 1024)
		for data := input; len(data) > 0; {
			n := chunk
			if n > len(data) {
				n = len(data)
			}
			written, err := observer.Write(data[:n])
			if written != n || err != nil {
				t.Fatal(written, err)
			}
			data = data[n:]
		}
		observer.Finish(io.EOF)
		result := observer.Snapshot()
		if result.CompleteFrames != 3 || result.RetainedFrames != 3 || result.OmittedFrames != 0 || result.ObservedBytes != int64(len(input)) || !result.CountsComplete || !result.EOF || result.Partial != nil {
			t.Fatalf("chunk %d: %+v", chunk, result)
		}
		for i, want := range []string{"one", "two", ""} {
			frame := result.Frames[i]
			if string(frame.Payload) != want || frame.Index != uint64(i) || frame.WireBytes != uint32(len(want)) || frame.StartSequence >= frame.CompleteSequence || frame.StartedOffsetNS > frame.CompletedOffsetNS {
				t.Fatalf("chunk %d frame %d: %+v", chunk, i, frame)
			}
		}
		if result.Frames[1].CompressionFlag != 1 {
			t.Fatal("compression flag lost")
		}
	}
}
func TestGRPCObserverRetainsBoundedPrefixAndCountsOmissions(t *testing.T) {
	observer := NewGRPCObserver(nil, "response", 16, 1024)
	input := append(grpcFrame(0, []byte("one")), grpcFrame(0, bytes.Repeat([]byte{'x'}, 64))...)
	input = append(input, grpcFrame(0, nil)...)
	_, _ = observer.Write(input)
	observer.Finish(io.EOF)
	result := observer.Snapshot()
	if result.CompleteFrames != 3 || result.RetainedFrames != 1 || result.OmittedFrames != 2 || result.Omission == nil || result.Omission.FirstIndex != 1 || result.Omission.Count != 2 || result.ObservedBytes != int64(len(input)) || !result.CountsComplete {
		t.Fatalf("%+v", result)
	}
	if len(result.Frames) != 1 || string(result.Frames[0].Payload) != "one" {
		t.Fatalf("retained %+v", result.Frames)
	}
	// A gigantic advertised payload never causes a matching allocation.
	observer = NewGRPCObserver(nil, "request", 16, 1024)
	_, _ = observer.Write([]byte{0, 255, 255, 255, 255, 1})
	result = observer.Snapshot()
	if result.CompleteFrames != 0 || result.Partial == nil || result.Partial.WireBytes != ^uint32(0) || result.Partial.PayloadBytes != 1 || len(result.Partial.Payload) != 0 || cap(observer.payload) != 0 {
		t.Fatalf("oversized partial: %+v", result)
	}
}
func TestGRPCObserverEmptyMessageFloodIsBounded(t *testing.T) {
	observer := NewGRPCObserver(nil, "response", 4<<20, MaxGRPCMessageRecords)
	const count = 100000
	chunk := bytes.Repeat(grpcFrame(0, nil), 100)
	for i := 0; i < count/100; i++ {
		_, _ = observer.Write(chunk)
	}
	result := observer.Snapshot()
	if result.CompleteFrames != count || result.RetainedFrames != MaxGRPCMessageRecords || len(result.Frames) != MaxGRPCMessageRecords || result.OmittedFrames != count-MaxGRPCMessageRecords || result.Omission.FirstIndex != MaxGRPCMessageRecords || result.ObservedBytes != 5*count {
		t.Fatalf("%+v", result)
	}
}
func TestGRPCObserverPartialMalformedAndTermination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     []byte
		complete bool
		header   int
		payload  uint64
	}{
		{"partial header", []byte{0, 0, 0}, true, 3, 0},
		{"partial payload", []byte{0, 0, 0, 0, 2, 1}, true, 5, 1},
		{"malformed", []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false, 5, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := NewGRPCObserver(nil, "request", 1024, 1024)
			_, _ = observer.Write(tc.data)
			observer.Finish(io.EOF)
			result := observer.Snapshot()
			if result.CountsComplete != tc.complete || result.CompleteFrames != 0 || result.Partial == nil || len(result.Partial.Header) != tc.header || result.Partial.PayloadBytes != tc.payload || !result.EOF {
				t.Fatalf("%+v", result)
			}
		})
	}
	observer := NewGRPCObserver(nil, "request", 1024, 1024)
	observer.Finish(errors.New("cancelled"))
	result := observer.Snapshot()
	if result.EOF || result.End != "error" || result.Error != "cancelled" || !result.CountsComplete {
		t.Fatalf("%+v", result)
	}
	observer = NewGRPCObserver(nil, "request", 1024, 1024)
	observer.Finish(nil)
	if result := observer.Snapshot(); result.EOF || result.End != "closed" {
		t.Fatalf("close invented EOF: %+v", result)
	}
}
func TestGRPCObserverInterleavingAndImmutableSnapshot(t *testing.T) {
	clock := NewGRPCClock(time.Now())
	request := NewGRPCObserver(clock, "request", 1024, 1024)
	response := NewGRPCObserver(clock, "response", 1024, 1024)
	clock.Mark("request", "headers")
	_, _ = request.Write(grpcFrame(0, []byte("a")))
	clock.Mark("response", "headers")
	_, _ = response.Write(grpcFrame(0, []byte("b")))
	_, _ = request.Write(grpcFrame(0, []byte("c"))[:5])
	beforeReq, beforeRes, events := clock.Snapshot(request, response)
	if beforeReq.Frames[0].CompleteSequence >= beforeRes.Frames[0].StartSequence || beforeRes.Frames[0].CompleteSequence >= beforeReq.Partial.StartSequence || len(events) != 2 {
		t.Fatalf("request=%+v response=%+v events=%+v", beforeReq, beforeRes, events)
	}
	_, _ = request.Write([]byte("c"))
	request.Finish(io.EOF)
	response.Finish(io.EOF)
	beforeReq.Frames[0].Payload[0] = 'x'
	afterReq, afterRes, afterEvents := clock.Snapshot(request, response)
	if string(afterReq.Frames[0].Payload) != "a" || beforeReq.EOF || beforeRes.EOF || !afterReq.EOF || !afterRes.EOF || len(afterEvents) != 4 {
		t.Fatalf("snapshot was mutable: %+v %+v %+v", afterReq, afterRes, afterEvents)
	}
}
func TestGRPCObserverConcurrentDirectionsAndSnapshots(t *testing.T) {
	clock := NewGRPCClock(time.Now())
	request := NewGRPCObserver(clock, "request", 1024, 1024)
	response := NewGRPCObserver(clock, "response", 1024, 1024)
	var workers sync.WaitGroup
	for _, observer := range []*GRPCObserver{request, response} {
		workers.Add(1)
		go func(observer *GRPCObserver) {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				_, _ = observer.Write(grpcFrame(0, []byte("a")))
			}
			observer.Finish(io.EOF)
		}(observer)
	}
	for i := 0; i < 100; i++ {
		r, s, _ := clock.Snapshot(request, response)
		for _, o := range []GRPCObservation{r, s} {
			if o.CompleteFrames != o.RetainedFrames+o.OmittedFrames {
				t.Fatal("inconsistent snapshot")
			}
		}
	}
	workers.Wait()
	r, s, _ := clock.Snapshot(request, response)
	if r.CompleteFrames != 100 || s.CompleteFrames != 100 || !r.EOF || !s.EOF {
		t.Fatal("lost messages")
	}
}
