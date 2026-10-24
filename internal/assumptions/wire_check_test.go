package assumptions

import (
	"encoding/base64"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestCheckRejectsIdenticalJSONWithIncompatibleProtobufFieldNumbers(t *testing.T) {
	makeCapture := func(fixture wireTestFixture, provider bool) *artifact.Artifact {
		t.Helper()
		req, res := wireTestMessage(t, fixture, false), wireTestMessage(t, fixture, true)
		for direction, message := range []*artifact.GrpcMessage{req, res} {
			message.Service, message.Method, message.SchemaRef, message.StreamKind = "example.Example", "Call", "example", "unary"
			raw, err := base64.StdEncoding.DecodeString(message.Messages[0].Wire.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			message.ObservedBytes = int64(len(raw) + 5)
			message.Observation = &artifact.GrpcObservation{CompleteFrames: 1, RetainedFrames: 1, ObservedBytes: message.ObservedBytes, CountsComplete: true, EOF: true, End: "eof"}
			sequence := uint64(direction * 2)
			message.Messages[0].Observation = &artifact.GrpcPayloadObservation{WireBytes: uint64(len(raw)), StartSequence: sequence, CompleteSequence: sequence + 1, StartedOffsetNS: int64(sequence), CompletedOffsetNS: int64(sequence + 1)}
		}
		res.HTTPStatus, res.StatusSource = 200, "trailers"
		schema := fixture.schema
		schema.Role, schema.AssetID = "dependency", "stock"
		if provider {
			schema.Role, schema.AssetID = "sut", "inventory-api"
		}
		capture := checkCapture(provider, artifact.Flow{Protocol: "grpc", Request: artifact.Message{GRPC: req}, Response: artifact.Message{GRPC: res}})
		capture.Schemas = []artifact.SchemaRef{schema}
		return capture
	}
	consumer := makeCapture(newWireTestFixture(t, 1, false), false)
	provider := makeCapture(newWireTestFixture(t, 2, false), true)
	consumerReq, consumerRes := normalizedSignatures(t, consumer.Traffic[0])
	providerReq, providerRes := normalizedSignatures(t, provider.Traffic[0])
	if consumerReq != providerReq || consumerRes != providerRes {
		t.Fatal("regression fixture must look compatible when only decoded values are compared")
	}
	opts := checkHTTPPair(t, []*artifact.Artifact{consumer}, []*artifact.Artifact{provider})
	for _, mode := range []string{"consumer", "provider"} {
		opts.Mode = mode
		report := mustCheck(t, opts)
		if report.Compatible || report.Counts.Mismatched != 1 || report.Counts.Verified != 0 || !findingCode(report, "provider_wire_mismatch") {
			t.Fatalf("%s view certified incompatible protobuf field numbers from identical JSON: %+v", mode, report)
		}
	}
}
