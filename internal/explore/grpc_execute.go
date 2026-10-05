package explore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/tracecontext"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

type grpcStepResult struct {
	flow                                       artifact.Flow
	assertions                                 []artifact.Assertion
	verified, failed, incomplete, schemaFailed bool
	diagnostic                                 string
}

func runGRPCStep(ctx context.Context, st step, m *mutation, input *HookInput, cfg Config, runtime *caseRuntime, call hookCaller) (out grpcStepResult, err error) {
	seed := st.GRPC
	messages := make([]proto.Message, len(seed.messages))
	for i, message := range seed.messages {
		messages[i] = proto.Clone(message)
	}
	request := GRPCRequest{ID: st.ID, Procedure: st.Operation, Metadata: seed.metadata.Clone(), ResponseGates: append([]int(nil), seed.gates...), SchemaID: seed.schemaID}
	for name, value := range input.Headers {
		request.Metadata.Set(name, value)
	}
	requestSize := 0
	for _, message := range messages {
		requestSize += proto.Size(message)
	}
	for _, binding := range cfg.Bindings {
		if binding.FlowID != "" && binding.FlowID != st.ID {
			continue
		}
		value, ok := input.Bindings[binding.Name]
		if !ok {
			return out, fmt.Errorf("missing runtime binding %q", binding.Name)
		}
		if binding.In == "metadata" {
			request.Metadata.Set(binding.Path, fmt.Sprint(value))
			continue
		}
		if binding.MessageIndex != nil && (*binding.MessageIndex < 0 || *binding.MessageIndex >= len(messages)) {
			return out, fmt.Errorf("binding %s message_index is outside captured request", binding.Name)
		}
		for index, message := range messages {
			if binding.MessageIndex != nil && *binding.MessageIndex != index {
				continue
			}
			originalSize := proto.Size(message)
			raw, e := (protojson.MarshalOptions{UseProtoNames: true, Resolver: dynamicpb.NewTypes(seed.contract.Files)}).Marshal(message)
			if e != nil {
				return out, e
			}
			raw, e = editJSON(raw, binding.Path, value, false)
			if e != nil {
				return out, fmt.Errorf("gRPC binding %s: %w", binding.Name, e)
			}
			if e = (protojson.UnmarshalOptions{Resolver: dynamicpb.NewTypes(seed.contract.Files), RecursionLimit: 100}).Unmarshal(raw, message); e != nil {
				return out, fmt.Errorf("gRPC binding %s is not valid ProtoJSON: %w", binding.Name, e)
			}
			requestSize += proto.Size(message) - originalSize
			if requestSize > 16<<20 {
				return out, fmt.Errorf("bound gRPC request exceeds 16 MiB")
			}
		}
	}
	for _, message := range messages {
		if e := seed.validator.Validate(message); e != nil {
			return out, fmt.Errorf("runtime bindings do not produce a valid gRPC seed request: %w", e)
		}
	}
	if m != nil {
		if m.GRPC == nil || m.GRPC.index >= len(messages) {
			return out, fmt.Errorf("gRPC mutation message is unavailable")
		}
		if e := m.GRPC.apply(messages[m.GRPC.index]); e != nil {
			return out, e
		}
		// Once the invalid message is sent, half-close. Later captured messages
		// belong to the original successful conversation, not this rejection check.
		messages = messages[:m.GRPC.index+1]
		request.ResponseGates = request.ResponseGates[:len(messages)]
	}
	for _, message := range messages {
		raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(message)
		if e != nil {
			return out, e
		}
		request.Messages = append(request.Messages, raw)
	}
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		tracecontext.Inject(request.Metadata, sc)
	}
	input.FlowID = st.ID
	input.Operation = st.Operation
	input.Request = nil
	input.GRPCRequest, err = marshalGRPCHook(request, messages, seed.contract)
	if err != nil {
		return out, err
	}
	prepared, err := call(ctx, "before_request", input)
	if err != nil {
		return out, err
	}
	for name, value := range prepared.Headers {
		request.Metadata.Set(name, value)
	}
	mergeValues(input.Bindings, prepared.Bindings)
	mergeValues(input.State, prepared.State)
	if sc.IsValid() {
		tracecontext.Inject(request.Metadata, sc)
		request.Metadata.Del("X-Wiretap-Session-Id")
	}
	for index, message := range messages {
		validation := seed.validator.Validate(message)
		if m != nil && index == m.GRPC.index {
			if !isolatedGRPCViolation(validation, m.GRPC.path, m.GRPC.rule) {
				return out, fmt.Errorf("final gRPC message does not isolate the planned violation")
			}
			out.verified = true
		} else if validation != nil {
			return out, fmt.Errorf("gRPC prefix message is invalid after runtime preparation: %w", validation)
		}
	}
	timeout, _ := time.ParseDuration(cfg.RequestTimeout)
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	out.flow, err = runtime.execution.InvokeGRPC(requestCtx, request)
	cancel()
	if err != nil {
		return out, err
	}
	if out.flow.ID == "" {
		return out, fmt.Errorf("gRPC transport returned no capture")
	}
	response := out.flow.Response.GRPC
	complete := artifact.GRPCDirectionComplete(out.flow.Request.GRPC, true) && artifact.GRPCDirectionComplete(response, true)
	if !complete || out.flow.TransportError != "" {
		out.failed = true
		out.incomplete = true
		out.diagnostic = "gRPC exchange could not be completely captured"
		return out, nil
	}
	responseValid := true
	decoded, e := grpcPayloadMessages(response, seed.method.Output(), seed.contract)
	if e != nil {
		responseValid = false
	} else {
		for _, message := range decoded {
			if seed.validator.Validate(message) != nil {
				responseValid = false
				break
			}
		}
	}
	if !seed.method.IsStreamingServer() && (len(decoded) > 1 || response.StatusCode == 0 && len(decoded) != 1) {
		responseValid = false
	}
	schemaCheck := artifact.Assertion{ID: "response-schema-" + st.ID, Channel: "test_to_sut", Phase: "response", FlowID: st.ID, Operator: "schema", Result: "pass"}
	if !responseValid {
		schemaCheck.Result = "fail"
		schemaCheck.Message = "Response violates the frozen Protobuf contract or declared validation rules"
		out.failed = true
		out.schemaFailed = true
		out.diagnostic = schemaCheck.Message
	}
	out.assertions = append(out.assertions, schemaCheck)
	expected := []int{0}
	checkID := "baseline-status-" + st.ID
	if m != nil {
		expected = cfg.ExpectedGRPCStatuses
		checkID = "expected-rejection"
	}
	statusOK := response.HTTPStatus == 200 && grpcUpstreamStatus(response) && containsStatus(expected, response.StatusCode)
	statusCheck := artifact.Assertion{ID: checkID, Channel: "test_to_sut", Phase: "response", FlowID: st.ID, Operator: "status_in", Expected: expected, Actual: response.StatusCode, Result: "pass"}
	if !statusOK {
		statusCheck.Result = "fail"
		out.failed = true
		out.diagnostic = fmt.Sprintf("unexpected gRPC status %d for procedure %s", response.StatusCode, st.Operation)
	}
	out.assertions = append(out.assertions, statusCheck)
	if !out.failed && m == nil {
		if err = extractGRPCValues(cfg.Extract, st.ID, response, input.Bindings); err != nil {
			return out, err
		}
	}
	return out, nil
}
func grpcUpstreamStatus(response *artifact.GrpcMessage) bool {
	return response != nil && (response.StatusSource == "headers" || response.StatusSource == "trailers")
}
func extractGRPCValues(rules []Extraction, flowID string, response *artifact.GrpcMessage, bindings map[string]any) error {
	for _, rule := range rules {
		if rule.FlowID != flowID {
			continue
		}
		if rule.In == "metadata" || rule.In == "trailers" {
			headers := response.Metadata
			if rule.In == "trailers" {
				headers = response.Trailers
			}
			var values []string
			for name, entries := range headers {
				if strings.EqualFold(name, rule.Path) {
					values = append(values, entries...)
				}
			}
			if len(values) != 1 {
				return fmt.Errorf("extraction %s requires exactly one metadata value", rule.Name)
			}
			bindings[rule.Name] = values[0]
			continue
		}
		index := 0
		if rule.MessageIndex != nil {
			index = *rule.MessageIndex
		}
		if index < 0 || index >= len(response.Messages) {
			return fmt.Errorf("extraction %s message_index is outside response", rule.Name)
		}
		payload := response.Messages[index]
		if payload.Body == nil {
			return fmt.Errorf("extraction %s requires decoded ProtoJSON", rule.Name)
		}
		body, e := decodeJSON([]byte(bodyText(&artifact.HttpMessage{Body: payload.Body})))
		if e != nil {
			return e
		}
		value, e := jsonAt(body, rule.Path)
		if e != nil {
			return fmt.Errorf("extraction %s failed", rule.Name)
		}
		bindings[rule.Name] = value
	}
	return nil
}
