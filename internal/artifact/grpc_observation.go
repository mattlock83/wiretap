package artifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

func requireObservationFields(data []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range names {
		if value, exists := fields[name]; !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("gRPC observation requires explicit %s", name)
		}
	}
	return nil
}

func (o *GrpcObservation) UnmarshalJSON(data []byte) error {
	if err := requireObservationFields(data, "complete_frames", "retained_frames", "omitted_frames", "observed_bytes", "counts_complete", "eof", "end"); err != nil {
		return err
	}
	type plain GrpcObservation
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = GrpcObservation(value)
	return nil
}
func (o *GrpcPayloadObservation) UnmarshalJSON(data []byte) error {
	if err := requireObservationFields(data, "wire_bytes", "started_offset_ns", "completed_offset_ns", "start_sequence", "complete_sequence"); err != nil {
		return err
	}
	type plain GrpcPayloadObservation
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = GrpcPayloadObservation(value)
	return nil
}
func (o *GrpcPartialFrame) UnmarshalJSON(data []byte) error {
	if err := requireObservationFields(data, "index", "header_bytes", "wire_bytes", "observed_payload_bytes", "started_offset_ns", "start_sequence"); err != nil {
		return err
	}
	type plain GrpcPartialFrame
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = GrpcPartialFrame(value)
	return nil
}
func (o *GrpcOmission) UnmarshalJSON(data []byte) error {
	if err := requireObservationFields(data, "first_index", "count"); err != nil {
		return err
	}
	type plain GrpcOmission
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = GrpcOmission(value)
	return nil
}
func (o *GrpcEvent) UnmarshalJSON(data []byte) error {
	if err := requireObservationFields(data, "sequence", "offset_ns", "direction", "kind"); err != nil {
		return err
	}
	type plain GrpcEvent
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = GrpcEvent(value)
	return nil
}

// GrpcObservation describes one direction of a native RPC. Counts describe
// complete frames actually observed, not messages inferred from a retained list.
type GrpcObservation struct {
	CompleteFrames uint64            `json:"complete_frames"`
	RetainedFrames uint64            `json:"retained_frames"`
	OmittedFrames  uint64            `json:"omitted_frames"`
	ObservedBytes  int64             `json:"observed_bytes"`
	CountsComplete bool              `json:"counts_complete"`
	EOF            bool              `json:"eof"`
	End            string            `json:"end"`
	Error          string            `json:"error,omitempty"`
	Omitted        *GrpcOmission     `json:"omitted,omitempty"`
	Partial        *GrpcPartialFrame `json:"partial,omitempty"`
}

type GrpcOmission struct {
	FirstIndex uint64 `json:"first_index"`
	Count      uint64 `json:"count"`
}

type GrpcPartialFrame struct {
	Index                uint64 `json:"index"`
	HeaderBytes          int    `json:"header_bytes"`
	WireBytes            uint64 `json:"wire_bytes"`
	ObservedPayloadBytes uint64 `json:"observed_payload_bytes"`
	StartedOffsetNS      int64  `json:"started_offset_ns"`
	StartSequence        uint64 `json:"start_sequence"`
}

type GrpcPayloadObservation struct {
	WireBytes         uint64 `json:"wire_bytes"`
	StartedOffsetNS   int64  `json:"started_offset_ns"`
	CompletedOffsetNS int64  `json:"completed_offset_ns"`
	StartSequence     uint64 `json:"start_sequence"`
	CompleteSequence  uint64 `json:"complete_sequence"`
}

// GrpcEvent references a lifecycle observation. Application messages are stored
// exactly once on their direction, with sequence/timing on each payload.
type GrpcEvent struct {
	Sequence  uint64 `json:"sequence"`
	OffsetNS  int64  `json:"offset_ns"`
	Direction string `json:"direction"`
	Kind      string `json:"kind"`
}

// GRPCDirectionComplete describes evidence quality, not RPC success. A complete
// expected non-OK RPC can exercise a method without a successful response body.
func GRPCDirectionComplete(msg *GrpcMessage, requireObservation bool) bool {
	if msg == nil || msg.CaptureTruncated {
		return false
	}
	if msg.Observation == nil {
		return !requireObservation
	}
	o := msg.Observation
	return o.CountsComplete && o.EOF && o.End == "eof" && o.Partial == nil && o.OmittedFrames == 0 && o.Error == ""
}

// ValidateGRPCObservation validates recording consistency, not the observed
// protocol lifecycle. Invalid traffic must remain recordable as evidence.
func ValidateGRPCObservation(flow Flow) error {
	has := flow.Request.GRPC != nil && flow.Request.GRPC.Observation != nil || flow.Response.GRPC != nil && flow.Response.GRPC.Observation != nil
	if !has {
		if len(flow.GRPCEvents) > 0 {
			return fmt.Errorf("gRPC events require observations")
		}
		return nil
	}
	if flow.Protocol != "grpc" || flow.Interaction != nil || flow.Request.GRPC == nil || flow.Response.GRPC == nil || flow.Request.GRPC.Observation == nil || flow.Response.GRPC.Observation == nil {
		return fmt.Errorf("gRPC observations require both directions")
	}
	type stamp struct {
		sequence uint64
		offset   int64
	}
	var stamps []stamp
	add := func(seq uint64, offset int64) { stamps = append(stamps, stamp{seq, offset}) }
	for _, msg := range []*GrpcMessage{flow.Request.GRPC, flow.Response.GRPC} {
		o := msg.Observation
		if o.ObservedBytes < 0 || o.ObservedBytes != msg.ObservedBytes || o.RetainedFrames != uint64(len(msg.Messages)) || o.RetainedFrames > 1024 || o.OmittedFrames > o.CompleteFrames || o.RetainedFrames != o.CompleteFrames-o.OmittedFrames {
			return fmt.Errorf("inconsistent gRPC observation counts")
		}
		if !oneOf(o.End, "open", "eof", "error", "closed") || o.EOF != (o.End == "eof") {
			return fmt.Errorf("inconsistent gRPC direction end")
		}
		if o.End == "error" && o.Error == "" {
			return fmt.Errorf("missing gRPC observation error")
		}
		if o.OmittedFrames == 0 && o.Omitted != nil || o.OmittedFrames > 0 && (o.Omitted == nil || o.Omitted.Count != o.OmittedFrames || o.Omitted.FirstIndex != o.RetainedFrames) {
			return fmt.Errorf("inconsistent gRPC omission range")
		}
		var minimumBytes uint64
		var prior *GrpcPayloadObservation
		for i, p := range msg.Messages {
			if p.Index != i || p.Observation == nil {
				return fmt.Errorf("invalid retained gRPC wire index or timing")
			}
			if !oneOf(p.DecodeState, "decoded", "invalid", "unavailable") || p.DecodeState == "decoded" && p.DecodeError != "" || p.DecodeState != "decoded" && p.DecodeError == "" {
				return fmt.Errorf("inconsistent gRPC payload decoding state")
			}
			if p.DecodeState != "decoded" && p.Body != nil && !p.Redacted {
				return fmt.Errorf("undecoded gRPC payload retains an unverified projection")
			}
			v := p.Observation
			if v.WireBytes > uint64(^uint32(0)) || v.StartedOffsetNS < 0 || v.CompletedOffsetNS < v.StartedOffsetNS || v.CompleteSequence <= v.StartSequence {
				return fmt.Errorf("invalid gRPC payload timing or length")
			}
			if prior != nil && (v.StartSequence <= prior.CompleteSequence || v.StartedOffsetNS < prior.CompletedOffsetNS) {
				return fmt.Errorf("gRPC message order disagrees with direction wire order")
			}
			prior = v
			if p.Wire != nil && p.Wire.OriginalBytes != int64(v.WireBytes) {
				return fmt.Errorf("gRPC wire length disagrees with observation")
			}
			minimumBytes += 5 + v.WireBytes
			add(v.StartSequence, v.StartedOffsetNS)
			add(v.CompleteSequence, v.CompletedOffsetNS)
		}
		if o.OmittedFrames > uint64(o.ObservedBytes)/5 {
			return fmt.Errorf("gRPC omitted counts exceed observed bytes")
		}
		minimumBytes += o.OmittedFrames * 5
		if o.Partial != nil {
			p := o.Partial
			if p.Index != o.CompleteFrames || p.HeaderBytes < 1 || p.HeaderBytes > 5 || p.StartedOffsetNS < 0 || p.WireBytes > uint64(^uint32(0)) || p.ObservedPayloadBytes > p.WireBytes || p.HeaderBytes < 5 && (p.WireBytes != 0 || p.ObservedPayloadBytes != 0) {
				return fmt.Errorf("inconsistent partial gRPC frame")
			}
			if o.CountsComplete && p.HeaderBytes == 5 && p.ObservedPayloadBytes == p.WireBytes {
				return fmt.Errorf("complete gRPC frame recorded as partial")
			}
			minimumBytes += uint64(p.HeaderBytes) + p.ObservedPayloadBytes
			if prior != nil && (p.StartSequence <= prior.CompleteSequence || p.StartedOffsetNS < prior.CompletedOffsetNS) {
				return fmt.Errorf("partial gRPC frame precedes retained frames")
			}
			add(p.StartSequence, p.StartedOffsetNS)
		}
		if minimumBytes > uint64(o.ObservedBytes) {
			return fmt.Errorf("gRPC frame lengths exceed observed bytes")
		}
		if o.CountsComplete && o.OmittedFrames == 0 && minimumBytes != uint64(o.ObservedBytes) {
			return fmt.Errorf("gRPC observed bytes disagree with frames")
		}
	}
	if len(flow.GRPCEvents) > 16 {
		return fmt.Errorf("too many gRPC lifecycle events")
	}
	var last uint64
	for i, e := range flow.GRPCEvents {
		if !oneOf(e.Direction, "request", "response", "rpc") || !oneOf(e.Kind, "headers", "half_close", "end", "trailers", "cancel", "error") || e.OffsetNS < 0 || i > 0 && e.Sequence <= last {
			return fmt.Errorf("invalid gRPC lifecycle event")
		}
		last = e.Sequence
		add(e.Sequence, e.OffsetNS)
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].sequence < stamps[j].sequence })
	for i := 1; i < len(stamps); i++ {
		if stamps[i].sequence == stamps[i-1].sequence || stamps[i].offset < stamps[i-1].offset {
			return fmt.Errorf("inconsistent gRPC observation ordering")
		}
	}
	return nil
}
