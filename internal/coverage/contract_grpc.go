package coverage

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Protobuf conditions are evaluated on descriptor-backed values, never on the
// human-readable JSON projection (which loses presence and numeric information).
type grpcConditions struct {
	targets []*ContractTarget
	fields  []grpcFieldConditions
	oneofs  []grpcOneofConditions
}
type grpcFieldConditions struct {
	desc                         protoreflect.FieldDescriptor
	presence, values, collection []*ContractTarget
	child                        *grpcConditions
}
type grpcOneofConditions struct {
	desc    protoreflect.OneofDescriptor
	targets map[protoreflect.FieldNumber]*ContractTarget // zero means unset
}

const grpcMaxConditionTargets = 4096

type grpcConditionBuilder struct {
	spec                    *ContractSpec
	procedure, phase, opKey string
}

func (b *grpcConditionBuilder) add(location, readable, family, condition string) *ContractTarget {
	if len(b.spec.Targets) >= grpcMaxConditionTargets-1 {
		return b.limit(location, readable)
	}
	t := &ContractTarget{Key: contractDigest([]string{ProtobufRulesVersion, b.opKey, b.spec.Revision, b.phase, location, family, condition}), OperationKey: b.opKey, Operation: b.procedure, Phase: b.phase, Location: location, FieldPath: readable, Family: family, Condition: condition, AppliesWhen: "containing protobuf message present; selected oneof member; each retained collection item", State: "not_covered", Feasibility: "known"}
	b.spec.Targets = append(b.spec.Targets, t)
	return t
}

func (b *grpcConditionBuilder) limit(location, readable string) *ContractTarget {
	for _, target := range b.spec.Targets {
		if target.Family == "unsupported" && target.Condition == "condition inventory limit" {
			return target
		}
	}
	t := &ContractTarget{Key: contractDigest([]string{ProtobufRulesVersion, b.spec.Revision, "condition inventory limit"}), OperationKey: b.opKey, Operation: b.procedure, Phase: b.phase, Location: location, FieldPath: readable, Family: "unsupported", Condition: "condition inventory limit", State: "unsupported", Feasibility: "unknown", Reason: "The bounded condition inventory was exhausted; additional fields or methods are unavailable"}
	b.spec.Targets = append(b.spec.Targets, t)
	return t
}

func (b *grpcConditionBuilder) node(desc protoreflect.MessageDescriptor, loc, name string, stack map[protoreflect.FullName]bool) *grpcConditions {
	n := &grpcConditions{}
	start := len(b.spec.Targets)
	if len(b.spec.Targets) >= grpcMaxConditionTargets-1 {
		n.targets = []*ContractTarget{b.limit(loc, name)}
		return n
	}
	if stack[desc.FullName()] || len(stack) >= 16 || desc.FullName() == "google.protobuf.Any" {
		t := b.add(loc, name, "unsupported", "message expansion")
		t.State, t.Feasibility, t.Reason = "unsupported", "unknown", "Recursive, dynamic Any, or bounded descriptor expansion requires additional verification"
		n.targets = append(n.targets, t)
		return n
	}
	stack[desc.FullName()] = true
	defer delete(stack, desc.FullName())
	unsafe := grpcUnknownOptions(desc.Options()) || grpcUnknownOptions(desc.ParentFile().Options())
	for i := 0; i < desc.Oneofs().Len(); i++ {
		if len(b.spec.Targets) >= grpcMaxConditionTargets-1 {
			b.limit(loc, name)
			break
		}
		o := desc.Oneofs().Get(i)
		if o.IsSynthetic() {
			continue
		}
		unsafe = unsafe || grpcUnknownOptions(o.Options())
		// Use sorted field numbers to identify a oneof rather than its name/index.
		numbers := []int{}
		for j := 0; j < o.Fields().Len(); j++ {
			numbers = append(numbers, int(o.Fields().Get(j).Number()))
		}
		sort.Ints(numbers)
		parts := []string{}
		for _, number := range numbers {
			parts = append(parts, strconv.Itoa(number))
		}
		oloc := loc + "/oneof:" + strings.Join(parts, ",")
		oneof := grpcOneofConditions{desc: o, targets: map[protoreflect.FieldNumber]*ContractTarget{}}
		oneof.targets[0] = b.add(oloc, name+"."+string(o.Name()), "oneof", "unset")
		for _, number := range numbers {
			if len(b.spec.Targets) >= grpcMaxConditionTargets-1 {
				b.limit(loc, name)
				break
			}
			oneof.targets[protoreflect.FieldNumber(number)] = b.add(oloc, name+"."+string(o.Name()), "oneof", "member:"+strconv.Itoa(number))
		}
		n.oneofs = append(n.oneofs, oneof)
	}
	for i := 0; i < desc.Fields().Len(); i++ {
		if len(b.spec.Targets) >= grpcMaxConditionTargets-1 {
			b.limit(loc, name)
			break
		}
		f := desc.Fields().Get(i)
		unsafe = unsafe || grpcUnknownOptions(f.Options())
		field := grpcFieldConditions{desc: f}
		floc, fname := loc+"/"+strconv.Itoa(int(f.Number())), name+"."+string(f.Name())
		if f.HasPresence() {
			field.presence = append(field.presence, b.add(floc, fname, "presence", "present"))
			if f.Cardinality() != protoreflect.Required {
				field.presence = append(field.presence, b.add(floc, fname, "presence", "absent"))
			}
		}
		valueDesc := f
		if f.IsList() || f.IsMap() {
			field.collection = append(field.collection, b.add(floc, fname, "collection", "empty"), b.add(floc, fname, "collection", "nonempty"))
			if f.IsMap() {
				valueDesc = f.MapValue()
				floc += "/value"
			} else {
				floc += "/item"
			}
		}
		if valueDesc.Kind() == protoreflect.BoolKind {
			field.values = append(field.values, b.add(floc, fname, "boolean", "false"), b.add(floc, fname, "boolean", "true"))
		}
		if valueDesc.Kind() == protoreflect.EnumKind {
			enum := valueDesc.Enum()
			unsafe = unsafe || grpcUnknownOptions(enum.Options())
			seen := map[protoreflect.EnumNumber]bool{}
			for j := 0; j < enum.Values().Len(); j++ {
				if len(b.spec.Targets) >= grpcMaxConditionTargets-1 {
					b.limit(loc, name)
					break
				}
				v := enum.Values().Get(j)
				unsafe = unsafe || grpcUnknownOptions(v.Options())
				if !seen[v.Number()] {
					field.values = append(field.values, b.add(floc, fname, "enum", strconv.Itoa(int(v.Number()))))
					seen[v.Number()] = true
				}
			}
		}
		if valueDesc.Message() != nil {
			field.child = b.node(valueDesc.Message(), floc, fname, stack)
		}
		n.fields = append(n.fields, field)
	}
	if unsafe {
		t := b.add(loc, name, "unsupported", "custom option semantics")
		t.State, t.Feasibility, t.Reason = "unsupported", "unknown", "Custom validation options are preserved but are not measured by structural coverage"
		for _, target := range b.spec.Targets[start:] {
			if target.State != "unsupported" {
				uncertain(target, "Custom options may constrain representable field values")
			}
		}
	}
	n.targets = b.spec.Targets[start:]
	return n
}

func grpcUnknownOptions(options protoreflect.ProtoMessage) bool {
	if options == nil {
		return false
	}
	m := options.ProtoReflect()
	if len(m.GetUnknown()) > 0 {
		return true
	}
	unknown := false
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.IsExtension() || f.Name() == "uninterpreted_option" {
			unknown = true
		}
		return !unknown
	})
	return unknown
}

func (n *grpcConditions) measure(msg protoreflect.Message, path string, e ContractEvidence, hits *[]ConditionHit) {
	if len(*hits) >= grpcMaxConditionTargets {
		return
	}
	observe := func(t *ContractTarget, location string) {
		if len(*hits) >= grpcMaxConditionTargets {
			return
		}
		local := e
		local.InstanceLocation = location
		if t.Feasibility == "unknown" {
			local.Result, local.Validation, local.Reason = "unknown", "unknown", t.Reason
		}
		recordHit(t, local, hits)
	}
	for _, o := range n.oneofs {
		member := protoreflect.FieldNumber(0)
		if selected := msg.WhichOneof(o.desc); selected != nil {
			member = selected.Number()
		}
		if target := o.targets[member]; target != nil {
			observe(target, path+"/"+string(o.desc.Name()))
		}
	}
	for _, f := range n.fields {
		if len(*hits) >= grpcMaxConditionTargets {
			return
		}
		present := msg.Has(f.desc)
		fpath := path + "/" + pointerToken(string(f.desc.Name()))
		for _, t := range f.presence {
			if (t.Condition == "present") == present {
				observe(t, fpath)
			}
		}
		if f.desc.HasPresence() && !present {
			continue
		}
		value := msg.Get(f.desc)
		measureValue := func(v protoreflect.Value, location string) {
			for _, t := range f.values {
				condition := ""
				if t.Family == "boolean" {
					condition = strconv.FormatBool(v.Bool())
				} else {
					condition = strconv.Itoa(int(v.Enum()))
				}
				if t.Condition == condition {
					observe(t, location)
				}
			}
			if f.child != nil {
				f.child.measure(v.Message(), location, e, hits)
			}
		}
		length := -1
		if f.desc.IsMap() {
			m := value.Map()
			length = m.Len()
			keys := []protoreflect.MapKey{}
			m.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool { keys = append(keys, k); return true })
			sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface()) })
			for _, k := range keys {
				if len(*hits) >= grpcMaxConditionTargets {
					break
				}
				measureValue(m.Get(k), fpath+"/"+pointerToken(fmt.Sprint(k.Interface())))
			}
		} else if f.desc.IsList() {
			list := value.List()
			length = list.Len()
			for i := 0; i < list.Len() && len(*hits) < grpcMaxConditionTargets; i++ {
				measureValue(list.Get(i), fpath+"/"+strconv.Itoa(i))
			}
		} else {
			measureValue(value, fpath)
		}
		for _, t := range f.collection {
			if (t.Condition == "empty") == (length == 0) {
				observe(t, fpath)
			}
		}
	}
}

func decodeGRPCDirection(side *artifact.GrpcMessage, desc protoreflect.MessageDescriptor, contract *grpccontract.Contract, streaming, required, observationRequired bool) ([]protoreflect.Message, error) {
	if !artifact.GRPCDirectionComplete(side, observationRequired) {
		return nil, fmt.Errorf("message direction is incomplete, omitted or unavailable")
	}
	if !streaming && (len(side.Messages) > 1 || required && len(side.Messages) != 1) {
		return nil, fmt.Errorf("invalid unary-side message cardinality")
	}
	messages := make([]protoreflect.Message, 0, len(side.Messages))
	for _, payload := range side.Messages {
		msg, err := decodeGRPCPayloadCoverage(side, payload, desc, contract)
		if err != nil {
			return messages, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func decodeGRPCPayloadCoverage(side *artifact.GrpcMessage, p artifact.GrpcPayload, desc protoreflect.MessageDescriptor, contract *grpccontract.Contract) (protoreflect.Message, error) {
	if p.Redacted || p.DecodeError != "" || p.Wire == nil || p.Wire.Truncated {
		return nil, fmt.Errorf("payload is redacted, undecodable or unavailable")
	}
	raw, err := base64.StdEncoding.DecodeString(p.Wire.Bytes)
	if err != nil || len(raw) > 16<<20 {
		return nil, fmt.Errorf("invalid or oversized protobuf bytes")
	}
	if p.Compressed {
		encoding := ""
		for key, values := range side.Metadata {
			if strings.EqualFold(key, "grpc-encoding") && len(values) > 0 {
				encoding = values[0]
			}
		}
		if encoding != "gzip" {
			return nil, fmt.Errorf("unsupported compression")
		}
		r, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		raw, err = io.ReadAll(io.LimitReader(r, (16<<20)+1))
		_ = r.Close()
		if err != nil || len(raw) > 16<<20 {
			return nil, fmt.Errorf("invalid or oversized gzip payload")
		}
	}
	m := dynamicpb.NewMessage(desc)
	if err := (proto.UnmarshalOptions{Resolver: dynamicpb.NewTypes(contract.Files), RecursionLimit: 100}).Unmarshal(raw, m); err != nil {
		return nil, err
	}
	return m, nil
}

func grpcMessageUnknown(msg protoreflect.Message) bool {
	if len(msg.GetUnknown()) > 0 {
		return true
	}
	unknown := false
	msg.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.IsExtension() {
			unknown = true
			return false
		}
		valueDesc := f
		if f.IsMap() {
			valueDesc = f.MapValue()
		}
		unknownValue := func(value protoreflect.Value) bool {
			if valueDesc.Kind() == protoreflect.EnumKind {
				return valueDesc.Enum().Values().ByNumber(value.Enum()) == nil
			}
			if valueDesc.Message() != nil {
				return grpcMessageUnknown(value.Message())
			}
			return false
		}
		if f.IsMap() {
			v.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
				unknown = unknownValue(item)
				return !unknown
			})
			return !unknown
		}
		if f.IsList() {
			for i := 0; i < v.List().Len() && !unknown; i++ {
				unknown = unknownValue(v.List().Get(i))
			}
			return !unknown
		}
		unknown = unknownValue(v)
		return !unknown

	})
	return unknown
}

func measureGRPCContract(spec *ContractSpec, ref artifact.SchemaRef, run *bundle.Run, captures []*artifact.Artifact, origin, id string) {
	spec.RulesVersion, spec.Dialect = ProtobufRulesVersion, "protobuf"
	raw, err := os.ReadFile(ref.Source.Path)
	if err != nil {
		spec.Notes = append(spec.Notes, "Descriptor unavailable: "+err.Error())
		return
	}
	contract, err := grpccontract.Parse(raw, ref.Services)
	if err != nil {
		spec.Notes = append(spec.Notes, "Invalid descriptor: "+err.Error())
		return
	}
	spec.Notes = append(spec.Notes, "Structural Protobuf conditions use field presence and values, not JSON defaults. Statuses are observations, not a required set. Semantic annotations and cross-message business invariants are not inferred. Stream length is not a coverage denominator.")
	for _, procedure := range sortedMapKeys(contract.Methods) {
		if len(spec.Targets) >= grpcMaxConditionTargets-1 {
			(&grpcConditionBuilder{spec: spec, procedure: procedure, phase: "method"}).limit(procedure, "")
			break
		}
		method := contract.Methods[procedure]
		b := &grpcConditionBuilder{spec: spec, procedure: procedure, phase: "method", opKey: contractDigest([]string{ProtobufRulesVersion, "grpc", id, procedure})}
		op := b.add(procedure, "", "operation", "exercised")
		b.phase = "request"
		request := b.node(method.Input(), "/fields", string(method.Input().FullName()), map[protoreflect.FullName]bool{})
		b.phase = "response"
		response := b.node(method.Output(), "/fields", string(method.Output().FullName()), map[protoreflect.FullName]bool{})
		if grpcUnknownOptions(method.Options()) || grpcUnknownOptions(method.Parent().Options()) {
			for _, target := range append(append([]*ContractTarget{}, request.targets...), response.targets...) {
				if target.State != "unsupported" {
					uncertain(target, "Custom service/method options have unsupported semantics")
				}
			}
			t := b.add(procedure, "", "unsupported", "service/method option semantics")
			t.State, t.Feasibility = "unsupported", "unknown"
		}
		for index, a := range captures {
			applicable := false
			for _, ar := range a.Schemas {
				if ar.Role == ref.Role && ar.AssetID == ref.AssetID && ar.ID == ref.ID && schemaRevision(ar) == spec.Revision {
					applicable = true
					break
				}
			}
			if !applicable {
				continue
			}
			for _, f := range a.Traffic {
				if !flowMatchesSchemaBoundary(f, ref) {
					continue
				}
				if f.Protocol != "grpc" || f.Request.GRPC == nil || f.Request.GRPC.Procedure != procedure {
					continue
				}
				if origin == "provided" && f.Channel != "test_to_sut" || origin == "dependency" && (f.Channel != "sut_to_dependency" || f.AssetID != id) {
					continue
				}
				e := ContractEvidence{RunID: run.RunID, AttemptID: a.TestCase.ID, FlowID: f.ID, TestName: a.TestCase.Name, Outcome: a.TestCase.Status, CaptureIncomplete: a.CaptureIncomplete, TransportError: f.TransportError, Phase: "method", Validation: "valid", Result: "covered"}
				if index < len(run.Attempts) {
					e.Path = run.Attempts[index].Path
				}
				if a.TestCase.Status != "pass" || a.CaptureIncomplete || f.TransportError != "" {
					e.Result, e.Reason = "unknown", "Failed, incomplete or transport-failed attempt cannot earn coverage"
				}
				requireObservation := method.IsStreamingClient() || method.IsStreamingServer()
				in, inErr := decodeGRPCDirection(f.Request.GRPC, method.Input(), contract, method.IsStreamingClient(), true, requireObservation)
				var out []protoreflect.Message
				var outErr error
				res := f.Response.GRPC
				validStatus := res != nil && res.HTTPStatus == 200 && res.StatusCode >= 0 && res.StatusCode <= 16 && !res.CaptureTruncated
				if validStatus {
					out, outErr = decodeGRPCDirection(res, method.Output(), contract, method.IsStreamingServer(), res.StatusCode == 0, requireObservation)
				}
				if !validStatus || inErr != nil || outErr != nil {
					e.Result, e.Validation, e.Reason = "unknown", "unknown", "Incomplete terminal status or invalid/unavailable protobuf message evidence"
				}
				op.observe(e)
				for _, side := range []struct {
					phase    string
					node     *grpcConditions
					messages []protoreflect.Message
					err      error
				}{{"request", request, in, inErr}, {"response", response, out, outErr}} {
					ce := e
					ce.Phase = side.phase

					if len(side.messages) == 0 {
						// A legitimate non-OK response without a result does not exercise output fields.
						if side.err == nil && validStatus {
							continue
						}
						for _, target := range side.node.targets {
							target.observe(ce)
						}
						continue
					}
					for messageIndex, message := range side.messages {
						ce := ce
						ce.MessageIndex = &messageIndex
						if grpcMessageUnknown(message) {
							ce.Result, ce.Validation, ce.Reason = "unknown", "unknown", "Unknown protobuf wire fields, enum numbers or extensions limit payload assurance"
							for _, target := range side.node.targets {
								target.observe(ce)
							}
						}
						hits := []ConditionHit{}
						side.node.measure(message, fmt.Sprintf("/messages/%d/body", messageIndex), ce, &hits)
						if len(hits) >= grpcMaxConditionTargets {
							unavailable := ce
							unavailable.Result, unavailable.Validation, unavailable.Reason = "unknown", "unknown", "Per-message condition observation limit reached"
							for _, target := range side.node.targets {
								if target.State != "covered" {
									target.observe(unavailable)
								}
							}
						}
						if len(hits) > 0 {
							sort.Slice(hits, func(i, j int) bool {
								return hits[i].Key+hits[i].InstanceLocation < hits[j].Key+hits[j].InstanceLocation
							})
							spec.Combinations = append(spec.Combinations, ContractCombination{Fingerprint: contractDigest(struct {
								Profile, Operation, Phase string
								Hits                      []ConditionHit
							}{ProtobufRulesVersion, b.opKey, side.phase, hits}), OperationKey: b.opKey, Phase: side.phase, Hits: hits, Evidence: ce})
						}
					}
				}
			}
		}
	}
}
