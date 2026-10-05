package artifact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGRPCObservationRequiresExplicitZeroValues(t *testing.T) {
	for name, value := range map[string]any{
		"direction": GrpcObservation{End: "open"},
		"message":   GrpcPayloadObservation{},
		"partial":   GrpcPartialFrame{},
		"omission":  GrpcOmission{},
		"event":     GrpcEvent{},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			decode := func(raw []byte) error {
				switch name {
				case "direction":
					var v GrpcObservation
					return json.Unmarshal(raw, &v)
				case "message":
					var v GrpcPayloadObservation
					return json.Unmarshal(raw, &v)
				case "partial":
					var v GrpcPartialFrame
					return json.Unmarshal(raw, &v)
				case "omission":
					var v GrpcOmission
					return json.Unmarshal(raw, &v)
				default:
					var v GrpcEvent
					return json.Unmarshal(raw, &v)
				}
			}
			if err := decode(raw); err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			for key, original := range fields {
				for _, null := range []bool{false, true} {
					delete(fields, key)
					if null {
						fields[key] = json.RawMessage("null")
					}
					changed, _ := json.Marshal(fields)
					if err := decode(changed); err == nil {
						t.Fatalf("accepted missing/null %s", key)
					}
					fields[key] = original
				}
			}
		})
	}
}

func TestGRPCObservationRejectsReorderedDirection(t *testing.T) {
	request := &GrpcMessage{ObservedBytes: 10, Observation: &GrpcObservation{CompleteFrames: 2, RetainedFrames: 2, ObservedBytes: 10, CountsComplete: true, EOF: true, End: "eof"}}
	for i := 0; i < 2; i++ {
		request.Messages = append(request.Messages, GrpcPayload{Index: i, DecodeState: "decoded", Observation: &GrpcPayloadObservation{StartSequence: uint64(i * 2), CompleteSequence: uint64(i*2 + 1), StartedOffsetNS: int64(i * 2), CompletedOffsetNS: int64(i*2 + 1)}})
	}
	flow := Flow{Protocol: "grpc", Request: Message{GRPC: request}, Response: Message{GRPC: &GrpcMessage{Observation: &GrpcObservation{CountsComplete: true, EOF: true, End: "eof"}}}}
	if err := ValidateGRPCObservation(flow); err != nil {
		t.Fatal(err)
	}
	request.Messages[0].Observation, request.Messages[1].Observation = request.Messages[1].Observation, request.Messages[0].Observation
	if err := ValidateGRPCObservation(flow); err == nil || !strings.Contains(err.Error(), "wire order") {
		t.Fatalf("accepted reordered direction: %v", err)
	}
}
