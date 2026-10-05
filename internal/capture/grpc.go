package capture

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
)

const MaxGRPCMessageRecords = 1024

// GRPCClock gives the two directions one monotonic observation order. This is
// the order in which Wiretap saw bytes, not a causal order between peers.
type GRPCClock struct {
	mu      sync.Mutex
	started time.Time
	next    uint64
	events  []GRPCEvent
}

type GRPCEvent struct {
	Sequence  uint64
	OffsetNS  int64
	Direction string
	Kind      string
}

type GRPCFrame struct {
	Index                              uint64
	CompressionFlag                    byte
	Payload                            []byte
	WireBytes                          uint32
	StartedOffsetNS, CompletedOffsetNS int64
	StartSequence, CompleteSequence    uint64
}

// GRPCPartial is separate from complete-frame counts. Header contains at most
// five bytes; Payload is only the bounded retained portion, if any.
type GRPCPartial struct {
	Index           uint64
	Header          []byte
	CompressionFlag byte
	WireBytes       uint32
	PayloadBytes    uint64
	Payload         []byte
	Retained        bool
	StartedOffsetNS int64
	StartSequence   uint64
}

// Retention stops permanently at the first omitted frame, so the omission
// summary is one bounded range even for streams of arbitrary duration.
type GRPCOmission struct{ FirstIndex, Count uint64 }

type GRPCObservation struct {
	Frames                                        []GRPCFrame
	CompleteFrames, RetainedFrames, OmittedFrames uint64
	ObservedBytes                                 int64
	CountsComplete                                bool
	EOF                                           bool
	End, Error                                    string
	Partial                                       *GRPCPartial
	Omission                                      *GRPCOmission
}

type GRPCObserver struct {
	clock         *GRPCClock
	direction     string
	limit         int64
	records       int
	observation   GRPCObservation
	retainedBytes int64
	stopped       bool
	header        [5]byte
	headerBytes   int
	payloadBytes  uint64
	payload       []byte
	current       GRPCFrame
	retaining     bool
}

func NewGRPCClock(started time.Time) *GRPCClock { return &GRPCClock{started: started} }
func NewGRPCObserver(clock *GRPCClock, direction string, maxBytes int64, records int) *GRPCObserver {
	if clock == nil {
		clock = NewGRPCClock(time.Now())
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	if maxBytes > 16<<20 {
		maxBytes = 16 << 20
	}
	if records <= 0 || records > MaxGRPCMessageRecords {
		records = MaxGRPCMessageRecords
	}
	return &GRPCObserver{clock: clock, direction: direction, limit: maxBytes, records: records, observation: GRPCObservation{CountsComplete: true, End: "open"}}
}
func (c *GRPCClock) tick() (uint64, int64) {
	sequence := c.next
	c.next++
	offset := time.Since(c.started).Nanoseconds()
	if offset < 0 {
		offset = 0
	}
	return sequence, offset
}
func (c *GRPCClock) mark(direction, kind string) {
	sequence, offset := c.tick()
	// The proxy emits a finite lifecycle, but bound records independently of its
	// caller so repeated Finish/Mark calls cannot grow a long-lived observation.
	if len(c.events) < 16 {
		c.events = append(c.events, GRPCEvent{Sequence: sequence, OffsetNS: offset, Direction: direction, Kind: kind})
	}
}
func (c *GRPCClock) Mark(direction, kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mark(direction, kind)
}

// Write never returns an observation error to the forwarding path. Invalid
// framing makes further frame counts unknown while byte accounting continues.
func (o *GRPCObserver) Write(p []byte) (int, error) {
	if o == nil {
		return len(p), nil
	}
	o.clock.mu.Lock()
	defer o.clock.mu.Unlock()
	n := len(p)
	o.observation.ObservedBytes += int64(n)
	if o.observation.End != "open" && n > 0 {
		o.observation.CountsComplete = false
		if o.observation.Error == "" {
			o.observation.Error = "gRPC bytes observed after body closure"
		}
	}
	if !o.observation.CountsComplete {
		return n, nil
	}
	for len(p) > 0 {
		if o.headerBytes == 0 {
			o.current = GRPCFrame{Index: o.observation.CompleteFrames}
			o.current.StartSequence, o.current.StartedOffsetNS = o.clock.tick()
		}
		if o.headerBytes < 5 {
			take := 5 - o.headerBytes
			if take > len(p) {
				take = len(p)
			}
			copy(o.header[o.headerBytes:], p[:take])
			o.headerBytes += take
			p = p[take:]
			if o.headerBytes < 5 {
				break
			}
			o.current.CompressionFlag = o.header[0]
			o.current.WireBytes = binary.BigEndian.Uint32(o.header[1:])
			if o.header[0] > 1 {
				o.observation.CountsComplete = false
				o.observation.Error = fmt.Sprintf("invalid gRPC compression flag %d", o.header[0])
				o.clock.mark(o.direction, "error")
				break
			}
			needed := int64(o.current.WireBytes) + 5
			if !o.stopped && (len(o.observation.Frames) >= o.records || needed > o.limit-o.retainedBytes) {
				o.stopped = true
			}
			o.retaining = !o.stopped
			// Do not allocate the advertised frame length. Payload chunks allocate only
			// as they arrive, and only when the entire envelope fits the byte budget.
			if o.current.WireBytes == 0 {
				o.complete()
				continue
			}
		}
		remaining := uint64(o.current.WireBytes) - o.payloadBytes
		take := uint64(len(p))
		if take > remaining {
			take = remaining
		}
		if o.retaining {
			o.payload = append(o.payload, p[:int(take)]...)
		}
		o.payloadBytes += take
		p = p[int(take):]
		if o.payloadBytes == uint64(o.current.WireBytes) {
			o.complete()
		}
	}
	return n, nil
}
func (o *GRPCObserver) complete() {
	o.current.CompleteSequence, o.current.CompletedOffsetNS = o.clock.tick()
	o.observation.CompleteFrames++
	if o.retaining {
		o.current.Payload = o.payload
		// Keep zero-byte application messages explicitly present.
		if o.current.Payload == nil {
			o.current.Payload = []byte{}
		}
		o.observation.Frames = append(o.observation.Frames, o.current)
		o.observation.RetainedFrames++
		o.retainedBytes += int64(o.current.WireBytes) + 5
	} else {
		if o.observation.Omission == nil {
			o.observation.Omission = &GRPCOmission{FirstIndex: o.current.Index}
		}
		o.observation.Omission.Count++
		o.observation.OmittedFrames++
	}
	o.headerBytes = 0
	o.payloadBytes = 0
	o.payload = nil
	o.retaining = false
}

// Finish reports what the body reader actually observed. Closing a request
// after an early response is not an inferred EOF/half-close.
func (o *GRPCObserver) Finish(err error) {
	if o == nil {
		return
	}
	o.clock.mu.Lock()
	defer o.clock.mu.Unlock()
	if o.observation.End != "open" {
		return
	}
	switch err {
	case io.EOF:
		o.observation.EOF = true
		o.observation.End = "eof"
		kind := "end"
		if o.direction == "request" {
			kind = "half_close"
		}
		o.clock.mark(o.direction, kind)
	case nil:
		o.observation.End = "closed"
	default:
		o.observation.End = "error"
		if o.observation.Error == "" {
			o.observation.Error = err.Error()
		}
		o.clock.mark(o.direction, "error")
	}
}
func (o *GRPCObserver) snapshot() GRPCObservation {
	if o == nil {
		return GRPCObservation{End: "unavailable"}
	}
	result := o.observation
	result.Frames = append([]GRPCFrame{}, o.observation.Frames...)
	for i := range result.Frames {
		result.Frames[i].Payload = append([]byte{}, result.Frames[i].Payload...)
	}
	if result.Omission != nil {
		copy := *result.Omission
		result.Omission = &copy
	}
	if o.headerBytes > 0 {
		result.Partial = &GRPCPartial{Index: o.current.Index, Header: append([]byte{}, o.header[:o.headerBytes]...), CompressionFlag: o.current.CompressionFlag, WireBytes: o.current.WireBytes, PayloadBytes: o.payloadBytes, Payload: append([]byte{}, o.payload...), Retained: o.retaining, StartedOffsetNS: o.current.StartedOffsetNS, StartSequence: o.current.StartSequence}
	}
	return result
}
func (o *GRPCObserver) Snapshot() GRPCObservation {
	if o == nil {
		return GRPCObservation{End: "unavailable"}
	}
	o.clock.mu.Lock()
	defer o.clock.mu.Unlock()
	return o.snapshot()
}

// Snapshot copies both directions under their shared lock. Later transport
// activity cannot modify a committed drain snapshot.
func (c *GRPCClock) Snapshot(request, response *GRPCObserver) (GRPCObservation, GRPCObservation, []GRPCEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return request.snapshot(), response.snapshot(), append([]GRPCEvent{}, c.events...)
}
