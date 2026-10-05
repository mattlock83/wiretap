package coverage

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/grpccontract"
)

type RejectionCoverage struct {
	Scope      string                     `json:"scope" yaml:"scope"`
	Covered    int                        `json:"covered" yaml:"covered"`
	Failed     int                        `json:"failed" yaml:"failed"`
	NotCovered int                        `json:"not_covered" yaml:"not_covered"`
	Targets    []*RejectionCoverageTarget `json:"targets" yaml:"targets"`
}
type RejectionCoverageTarget struct {
	artifact.RejectionTarget `yaml:",inline"`
	State                    string             `json:"state" yaml:"state"`
	Evidence                 []ContractEvidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

func buildRejectionCoverage(run *bundle.Run, captures []*artifact.Artifact) *RejectionCoverage {
	out := &RejectionCoverage{Scope: "supported isolated invalid-input targets generated from the selected provider seed; not all invalid values or combinations"}
	targets := map[string]*RejectionCoverageTarget{}
	grpcContracts := map[string]*grpccontract.Contract{}
	for _, a := range captures {
		if a.Exploration == nil {
			continue
		}
		for _, t := range a.Exploration.Catalogue {
			if targets[t.Key] == nil {
				targets[t.Key] = &RejectionCoverageTarget{RejectionTarget: t, State: "not_covered"}
			}
		}
	}
	for i, a := range captures {
		x := a.Exploration
		if x == nil || x.Mode != "rejection" || x.Target == nil {
			continue
		}
		t := targets[x.Target.Key]
		if t == nil {
			continue
		}
		for _, flow := range a.Traffic {
			if flow.ID != x.Target.FlowID || flow.Channel != "test_to_sut" {
				continue
			}
			e := ContractEvidence{RunID: run.RunID, AttemptID: a.TestCase.ID, FlowID: flow.ID, Path: run.Attempts[i].Path, TestName: a.TestCase.Name, Outcome: a.TestCase.Status, Phase: "request", InstanceLocation: x.Target.Location, MessageIndex: x.Target.MessageIndex, Validation: "invalid", Result: "observed", CaptureIncomplete: a.CaptureIncomplete, TransportError: flow.TransportError}
			e.ExpectedStatuses = append([]int(nil), x.Target.ExpectedStatuses...)
			statusOK, qualityOK := false, true
			if x.Target.Protocol == "grpc" {
				qualityOK = t.Protocol == "grpc" && grpcRejectionEvidence(a, flow, *x.Target, grpcContracts)
				if flow.Protocol == "grpc" && flow.Response.GRPC != nil {
					for _, status := range x.Target.ExpectedStatuses {
						if (status == 3 || status == 11) && status == flow.Response.GRPC.StatusCode {
							statusOK = true
						}
					}
				}
			} else if (x.Target.Protocol == "" || x.Target.Protocol == "http") && t.Protocol != "grpc" && flow.Protocol != "grpc" && flow.Response.HTTP != nil {
				for _, status := range x.Target.ExpectedStatuses {
					if status == flow.Response.HTTP.Status && status >= 400 && status < 500 && status != 401 && status != 403 && status != 429 {
						statusOK = true
					}
				}
			}
			checked, responseChecked := false, false
			for _, check := range a.Assertions {
				if check.FlowID != flow.ID {
					continue
				}
				if check.ID == "expected-rejection" && check.Operator == "status_in" && check.Result == "pass" {
					checked = true
				}
				if check.ID == "response-schema-"+flow.ID && check.Operator == "schema" && check.Result == "pass" {
					responseChecked = true
				}
			}
			if x.RequestVerified && statusOK && qualityOK && checked && responseChecked && a.TestCase.Status == "pass" && !a.CaptureIncomplete && flow.TransportError == "" {
				t.State = "covered"
				e.Result = "covered"
				e.Reason = "Intended request violation verified before transmission; expected rejection and response contract checked"
				if x.Target.Protocol == "grpc" {
					e.Reason += fmt.Sprintf(" (expected gRPC %v; received %d)", e.ExpectedStatuses, flow.Response.GRPC.StatusCode)
				}
			} else {
				if t.State != "covered" {
					t.State = "failed"
				}
				e.Reason = "Generated case did not satisfy rejection checks"
				if !x.RequestVerified || !qualityOK {
					e.Validation = "unknown"
				}
				if flow.Response.GRPC != nil {
					e.Reason += fmt.Sprintf(" (expected gRPC %v; received %d)", e.ExpectedStatuses, flow.Response.GRPC.StatusCode)
				} else if flow.Response.HTTP != nil {
					e.Reason += fmt.Sprintf(" (HTTP %d)", flow.Response.HTTP.Status)
				}
			}
			t.Evidence = append(t.Evidence, e)
		}
	}
	for _, key := range sortedMapKeys(targets) {
		t := targets[key]
		out.Targets = append(out.Targets, t)
		switch t.State {
		case "covered":
			out.Covered++
		case "failed":
			out.Failed++
		default:
			out.NotCovered++
		}
	}
	sort.Slice(out.Targets, func(i, j int) bool {
		a, b := out.Targets[i], out.Targets[j]
		return a.Operation+a.Location+a.Constraint < b.Operation+b.Location+b.Constraint
	})
	if len(out.Targets) == 0 {
		return nil
	}
	return out
}

// Rejection assurance requires complete decoded evidence in both directions.
// A non-OK response can legitimately contain no application message.
func grpcRejectionEvidence(a *artifact.Artifact, flow artifact.Flow, target artifact.RejectionTarget, cache map[string]*grpccontract.Contract) bool {
	if flow.Protocol != "grpc" || flow.Request.GRPC == nil || flow.Response.GRPC == nil || flow.Response.GRPC.HTTPStatus != 200 || !artifact.GRPCDirectionComplete(flow.Request.GRPC, true) || !artifact.GRPCDirectionComplete(flow.Response.GRPC, true) {
		return false
	}
	if source := flow.Response.GRPC.StatusSource; source != "headers" && source != "trailers" {
		return false
	}
	if err := artifact.ValidateGRPCObservation(flow); err != nil {
		return false
	}
	if target.MessageIndex == nil || *target.MessageIndex < 0 || *target.MessageIndex >= len(flow.Request.GRPC.Messages) || flow.Request.GRPC.Messages[*target.MessageIndex].Index != *target.MessageIndex {
		return false
	}
	for _, side := range []*artifact.GrpcMessage{flow.Request.GRPC, flow.Response.GRPC} {
		for _, payload := range side.Messages {
			if payload.DecodeState != "decoded" {
				return false
			}
		}
	}
	var schema *artifact.SchemaRef
	for i := range a.Schemas {
		ref := &a.Schemas[i]
		if ref.ID == target.SchemaID && ref.Type == "protobuf" && (ref.Role == "sut" || ref.Role == "provided") {
			schema = ref
			break
		}
	}
	if schema == nil || schema.Source.Type != "file" || flow.Request.GRPC.SchemaRef != schema.ID || flow.Response.GRPC.SchemaRef != schema.ID {
		return false
	}
	key := schema.Source.Path + "|" + strings.Join(schema.Services, ",")
	contract, loaded := cache[key]
	if !loaded {
		raw, err := os.ReadFile(schema.Source.Path)
		if err != nil {
			cache[key] = nil
			return false
		}
		contract, err = grpccontract.Parse(raw, schema.Services)
		if err != nil {
			cache[key] = nil
			return false
		}
		cache[key] = contract
	}
	if contract == nil {
		return false
	}
	method := contract.Methods[target.Operation]
	if method == nil || flow.Request.GRPC.Procedure != target.Operation || flow.Response.GRPC.Procedure != target.Operation {
		return false
	}
	if _, err := decodeGRPCDirection(flow.Request.GRPC, method.Input(), contract, method.IsStreamingClient(), true, true); err != nil {
		return false
	}
	if _, err := decodeGRPCDirection(flow.Response.GRPC, method.Output(), contract, method.IsStreamingServer(), false, true); err != nil {
		return false
	}
	return true
}
