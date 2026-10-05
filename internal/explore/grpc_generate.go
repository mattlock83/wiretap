package explore

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	validate "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"github.com/phrond/wiretap/internal/artifact"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

type grpcFieldStep struct {
	number protoreflect.FieldNumber
	index  int
}

func grpcFieldParent(message proto.Message, path []grpcFieldStep) (protoreflect.Message, protoreflect.FieldDescriptor, error) {
	parent := message.ProtoReflect()
	for i, part := range path {
		field := parent.Descriptor().Fields().ByNumber(part.number)
		if field == nil {
			return nil, nil, fmt.Errorf("mutation field is absent")
		}
		if i == len(path)-1 {
			return parent, field, nil
		}
		if part.index >= 0 {
			list := parent.Get(field).List()
			if part.index >= list.Len() {
				return nil, nil, fmt.Errorf("mutation message index is absent")
			}
			parent = list.Get(part.index).Message()
		} else {
			if !parent.Has(field) {
				return nil, nil, fmt.Errorf("mutation parent is absent")
			}
			parent = parent.Get(field).Message()
		}
	}
	return nil, nil, fmt.Errorf("mutation field path is empty")
}
func grpcViolationPath(violation *protovalidate.Violation) string {
	var out string
	for _, element := range violation.Proto.GetField().GetElements() {
		out += "/" + token(element.GetFieldName())
		ref := element.ProtoReflect()
		index := ref.Descriptor().Fields().ByName("index")
		if index != nil && ref.Has(index) {
			out += "/" + strconv.FormatUint(ref.Get(index).Uint(), 10)
		}
	}
	return out
}
func isolatedGRPCViolation(err error, path, rule string) bool {
	var validation *protovalidate.ValidationError
	if !errors.As(err, &validation) || len(validation.Violations) != 1 {
		return false
	}
	violation := validation.Violations[0]
	return grpcViolationPath(violation) == path && (rule == "" || violation.Proto.GetRuleId() == rule)
}
func fieldRules(field protoreflect.FieldDescriptor) *validate.FieldRules {
	raw, err := proto.Marshal(field.Options())
	if err != nil {
		return nil
	}
	var options descriptorpb.FieldOptions
	if proto.Unmarshal(raw, &options) != nil || !proto.HasExtension(&options, validate.E_Field) {
		return nil
	}
	rules, _ := proto.GetExtension(&options, validate.E_Field).(*validate.FieldRules)
	return rules
}
func matchingGRPCRule(actual *protovalidate.Violation, expected protoreflect.FieldDescriptor) bool {
	if actual.RuleDescriptor == nil || expected == nil {
		return false
	}
	if actual.RuleDescriptor.FullName() == expected.FullName() {
		return true
	}
	if actual.RuleDescriptor.Parent().FullName() != expected.Parent().FullName() {
		return false
	}
	name := string(expected.Name())
	if name != "gt" && name != "gte" && name != "lt" && name != "lte" {
		return false
	}
	id := actual.Proto.GetRuleId()
	dot := strings.LastIndex(id, ".")
	if dot < 0 {
		return false
	}
	for _, part := range strings.Split(id[dot+1:], "_") {
		if part == name {
			return true
		}
	}
	return false
}

func generateGRPC(steps []step, selected []int, apiID, revision string, statuses []int) ([]mutation, []string, error) {
	var out []mutation
	var notes []string
	seen := map[string]bool{}
	exceeded := false
	for _, stepIndex := range selected {
		st := steps[stepIndex]
		seed := st.GRPC
		for messageIndex, original := range seed.messages {
			if err := seed.validator.Validate(original); err != nil {
				return nil, nil, fmt.Errorf("seed message violates declared constraints: %w", err)
			}
			emit := func(path []grpcFieldStep, location string, ruleDescriptor protoreflect.FieldDescriptor, change func(protoreflect.Message, protoreflect.FieldDescriptor)) {
				if exceeded {
					return
				}
				copiedPath := append([]grpcFieldStep(nil), path...)
				apply := func(message proto.Message) error {
					parent, field, err := grpcFieldParent(message, copiedPath)
					if err != nil {
						return err
					}
					if err := grpcRepeatedGrowthBudget(message, parent, field, ruleDescriptor); err != nil {
						return err
					}
					change(parent, field)
					return nil
				}
				candidate := proto.Clone(original)
				if apply(candidate) != nil || proto.CheckInitialized(candidate) != nil {
					return
				}
				if proto.Size(candidate) > 16<<20 {
					notes = append(notes, "Skipped a gRPC mutation exceeding 16 MiB at "+st.Operation+" "+location)
					return
				}
				err := seed.validator.Validate(candidate)
				if !isolatedGRPCViolation(err, location, "") {
					notes = append(notes, "Skipped non-isolated gRPC candidate at "+st.Operation+" "+location)
					return
				}
				var validation *protovalidate.ValidationError
				errors.As(err, &validation)
				violation := validation.Violations[0]
				if !matchingGRPCRule(violation, ruleDescriptor) {
					notes = append(notes, "Skipped a candidate whose declared rule could not be isolated at "+st.Operation+" "+location)
					return
				}
				rule := violation.Proto.GetRuleId()
				constraint := rule
				if dot := strings.LastIndex(rule, "."); dot >= 0 && strings.Contains(rule[dot+1:], "_") && (ruleDescriptor.Name() == "gt" || ruleDescriptor.Name() == "gte" || ruleDescriptor.Name() == "lt" || ruleDescriptor.Name() == "lte") {
					constraint = rule[:dot+1] + string(ruleDescriptor.Name())
				}
				raw, _ := json.Marshal([]any{Version, "grpc", apiID, revision, st.Operation, location, constraint})
				key := fmt.Sprintf("%x", sha256.Sum256(raw))
				instance := fmt.Sprintf("%d/%d/%s", stepIndex, messageIndex, key)
				if seen[instance] {
					return
				}
				if len(out) >= 10000 {
					exceeded = true
					return
				}
				seen[instance] = true
				index := messageIndex
				target := artifact.RejectionTarget{Key: key, SchemaID: seed.schemaID, Protocol: "grpc", Operation: st.Operation, FlowID: st.ID, Location: fmt.Sprintf("/messages/%d/body%s", messageIndex, location), Constraint: constraint, ExpectedStatuses: append([]int(nil), statuses...), MessageIndex: &index}
				out = append(out, mutation{Target: target, Step: stepIndex, GRPC: &grpcMutation{index: messageIndex, path: location, rule: rule, apply: apply}})
			}
			var walk func(protoreflect.Message, []grpcFieldStep, string, int, map[protoreflect.FullName]bool)
			walk = func(message protoreflect.Message, path []grpcFieldStep, location string, depth int, ancestors map[protoreflect.FullName]bool) {
				if depth > 20 || ancestors[message.Descriptor().FullName()] {
					notes = append(notes, "Skipped recursive/deep gRPC message at "+st.Operation+" "+location)
					return
				}
				ancestors[message.Descriptor().FullName()] = true
				defer delete(ancestors, message.Descriptor().FullName())
				fields := message.Descriptor().Fields()
				for i := 0; i < fields.Len(); i++ {
					field := fields.Get(i)
					fieldPath := append(append([]grpcFieldStep(nil), path...), grpcFieldStep{number: field.Number(), index: -1})
					fieldLocation := location + "/" + token(string(field.Name()))
					rules := fieldRules(field)
					if rules != nil {
						reflected := rules.ProtoReflect()
						required := reflected.Descriptor().Fields().ByName("required")
						if rules.GetRequired() && message.Has(field) {
							emit(fieldPath, fieldLocation, required, func(parent protoreflect.Message, field protoreflect.FieldDescriptor) { parent.Clear(field) })
						}
						for _, typeName := range []protoreflect.Name{"string", "bytes", "bool", "enum", "int32", "int64", "uint32", "uint64", "sint32", "sint64", "fixed32", "fixed64", "sfixed32", "sfixed64", "float", "double", "repeated", "map"} {
							typed := reflected.Descriptor().Fields().ByName(typeName)
							if typed == nil || !reflected.Has(typed) {
								continue
							}
							typedRules := reflected.Get(typed).Message()
							for _, candidate := range grpcFieldCandidates(field, message.Get(field), typedRules, &notes, st.Operation+" "+fieldLocation, (16<<20)-proto.Size(original)) {
								emit(fieldPath, fieldLocation, candidate.rule, candidate.apply)
							}
						}
						if len(rules.GetCel()) > 0 {
							notes = append(notes, "Custom CEL rules are validated but do not generate witnesses at "+st.Operation+" "+fieldLocation)
						}
					}
					if field.IsMap() {
						continue
					}
					if field.Message() != nil {
						if field.IsList() {
							list := message.Get(field).List()
							for index := 0; index < list.Len() && index < 32; index++ {
								nested := append([]grpcFieldStep(nil), fieldPath...)
								nested[len(nested)-1].index = index
								walk(list.Get(index).Message(), nested, fieldLocation+"/"+strconv.Itoa(index), depth+1, ancestors)
							}
							if list.Len() > 32 {
								notes = append(notes, "Nested message generation limited to first 32 list elements at "+fieldLocation)
							}
						} else if message.Has(field) {
							walk(message.Get(field).Message(), fieldPath, fieldLocation, depth+1, ancestors)
						}
					}
				}
			}
			walk(original.ProtoReflect(), nil, "", 0, map[protoreflect.FullName]bool{})
		}
	}
	if exceeded {
		return nil, nil, fmt.Errorf("generation exceeds the 10000-target resource limit; narrow operations or flow selection")
	}
	sort.Strings(notes)
	return out, unique(notes), nil
}

type grpcCandidate struct {
	rule  protoreflect.FieldDescriptor
	apply func(protoreflect.Message, protoreflect.FieldDescriptor)
}

func grpcFieldCandidates(field protoreflect.FieldDescriptor, original protoreflect.Value, rules protoreflect.Message, notes *[]string, where string, growthBudget int) []grpcCandidate {
	var out []grpcCandidate
	supported := map[protoreflect.Name]bool{}
	for _, name := range []protoreflect.Name{"min_items", "max_items", "unique", "min_pairs", "max_pairs", "len", "min_len", "max_len", "len_bytes", "min_bytes", "max_bytes", "const", "in", "not_in", "defined_only", "gt", "gte", "lt", "lte"} {
		supported[name] = true
	}
	rules.Range(func(rule protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if !supported[rule.Name()] {
			*notes = append(*notes, "Declared rule "+string(rule.FullName())+" is validated but has no witness generator at "+where)
		}
		return true
	})
	add := func(name string, apply func(protoreflect.Message, protoreflect.FieldDescriptor)) {
		rule := rules.Descriptor().Fields().ByName(protoreflect.Name(name))
		if rule != nil && rules.Has(rule) {
			out = append(out, grpcCandidate{rule, apply})
		}
	}
	value := func(name string) (protoreflect.Value, bool) {
		rule := rules.Descriptor().Fields().ByName(protoreflect.Name(name))
		if rule == nil || !rules.Has(rule) {
			return protoreflect.Value{}, false
		}
		return rules.Get(rule), true
	}
	set := func(name string, v protoreflect.Value) {
		add(name, func(parent protoreflect.Message, field protoreflect.FieldDescriptor) { parent.Set(field, v) })
	}
	if field.IsList() {
		list := original.List()
		for _, name := range []string{"min_items", "max_items"} {
			if boundary, ok := value(name); ok {
				count := boundary.Uint()
				if name == "min_items" {
					if count == 0 {
						continue
					}
					count--
				} else {
					if count == math.MaxUint64 {
						*notes = append(*notes, "List boundary has no representable witness at "+where)
						continue
					}
					count++
				}
				if count > 1024 {
					*notes = append(*notes, "List boundary witness exceeds 1024 elements at "+where)
					continue
				}
				if int(count) > list.Len() && list.Len() == 0 {
					*notes = append(*notes, "Cannot grow an empty list without inventing a valid element at "+where)
					continue
				}
				targetCount := int(count)
				if targetCount > list.Len() {
					element := list.Get(0)
					size := 16
					switch field.Kind() {
					case protoreflect.MessageKind, protoreflect.GroupKind:
						size += proto.Size(element.Message().Interface())
					case protoreflect.StringKind:
						size += len(element.String())
					case protoreflect.BytesKind:
						size += len(element.Bytes())
					}
					if size > growthBudget/(targetCount-list.Len()) {
						*notes = append(*notes, "List boundary witness exceeds 16 MiB at "+where)
						continue
					}
				}
				add(name, func(parent protoreflect.Message, field protoreflect.FieldDescriptor) {
					current := parent.Mutable(field).List()
					for current.Len() > targetCount {
						current.Truncate(targetCount)
					}
					for current.Len() < targetCount {
						v := current.Get(0)
						if field.Message() != nil {
							v = protoreflect.ValueOfMessage(proto.Clone(v.Message().Interface()).ProtoReflect())
						}
						current.Append(v)
					}
				})
			}
		}
		if v, ok := value("unique"); ok && v.Bool() && list.Len() > 0 {
			add("unique", func(parent protoreflect.Message, field protoreflect.FieldDescriptor) {
				current := parent.Mutable(field).List()
				current.Append(current.Get(0))
			})
		}
		return out
	}
	if field.IsMap() {
		if boundary, ok := value("min_pairs"); ok && boundary.Uint() > 0 {
			if boundary.Uint() > uint64(original.Map().Len()) {
				return out
			}
			count := int(boundary.Uint() - 1)
			add("min_pairs", func(parent protoreflect.Message, field protoreflect.FieldDescriptor) {
				m := parent.Mutable(field).Map()
				keys := []protoreflect.MapKey{}
				m.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool { keys = append(keys, k); return true })
				sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface()) })
				for i := count; i < len(keys); i++ {
					m.Clear(keys[i])
				}
			})
		}
		if _, ok := value("max_pairs"); ok {
			*notes = append(*notes, "Map maximum-pair witness generation is unsupported at "+where)
		}
		return out
	}
	switch field.Kind() {
	case protoreflect.StringKind, protoreflect.BytesKind:
		for _, name := range []string{"len", "min_len", "max_len", "len_bytes", "min_bytes", "max_bytes"} {
			boundary, ok := value(name)
			if !ok {
				continue
			}
			size := boundary.Uint()
			if strings.HasPrefix(name, "min_") {
				if size == 0 {
					continue
				}
				size--
			} else {
				if size == math.MaxUint64 {
					*notes = append(*notes, "String/bytes boundary has no representable witness at "+where)
					continue
				}
				size++
			}
			if size > 4096 {
				*notes = append(*notes, "String/bytes boundary witness exceeds 4096 bytes at "+where)
				continue
			}
			text := strings.Repeat("x", int(size))
			if field.Kind() == protoreflect.BytesKind {
				set(name, protoreflect.ValueOfBytes([]byte(text)))
			} else {
				set(name, protoreflect.ValueOfString(text))
			}
		}
		if boundary, ok := value("const"); ok {
			if field.Kind() == protoreflect.StringKind {
				set("const", protoreflect.ValueOfString(boundary.String()+"x"))
			} else {
				set("const", protoreflect.ValueOfBytes(append(append([]byte(nil), boundary.Bytes()...), 0)))
			}
		}
		for _, name := range []string{"in", "not_in"} {
			boundary, ok := value(name)
			if !ok || boundary.List().Len() == 0 {
				continue
			}
			if name == "not_in" {
				set(name, boundary.List().Get(0))
			} else {
				if field.Kind() == protoreflect.StringKind {
					candidate := "wiretap-invalid"
					for containsProtoValue(boundary.List(), protoreflect.ValueOfString(candidate)) {
						candidate += "x"
					}
					set(name, protoreflect.ValueOfString(candidate))
				} else {
					candidate := []byte("wiretap-invalid")
					for containsProtoValue(boundary.List(), protoreflect.ValueOfBytes(candidate)) {
						candidate = append(candidate, 0)
					}
					set(name, protoreflect.ValueOfBytes(candidate))
				}
			}
		}
	case protoreflect.BoolKind:
		if boundary, ok := value("const"); ok {
			set("const", protoreflect.ValueOfBool(!boundary.Bool()))
		}
	case protoreflect.EnumKind:
		if boundary, ok := value("defined_only"); ok && boundary.Bool() {
			unknown := protoreflect.EnumNumber(math.MaxInt32)
			for field.Enum().Values().ByNumber(unknown) != nil {
				unknown--
			}
			set("defined_only", protoreflect.ValueOfEnum(unknown))
		}
		for _, name := range []string{"const", "in", "not_in"} {
			boundary, ok := value(name)
			if !ok {
				continue
			}
			if name == "not_in" && boundary.List().Len() > 0 {
				set(name, protoreflect.ValueOfEnum(protoreflect.EnumNumber(boundary.List().Get(0).Int())))
				continue
			}
			for i := 0; i < field.Enum().Values().Len(); i++ {
				number := field.Enum().Values().Get(i).Number()
				match := name == "const" && int64(number) != boundary.Int()
				if name == "in" {
					match = !containsProtoValue(boundary.List(), protoreflect.ValueOfInt32(int32(number)))
				}
				if match {
					set(name, protoreflect.ValueOfEnum(number))
					break
				}
			}
		}
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind, protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind, protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind, protoreflect.FloatKind, protoreflect.DoubleKind:
		for _, name := range []string{"gt", "gte", "lt", "lte", "const"} {
			boundary, ok := value(name)
			if !ok {
				continue
			}
			if v, ok := grpcNumericViolation(field.Kind(), name, boundary); ok {
				set(name, v)
			}
		}
		for _, name := range []string{"in", "not_in"} {
			boundary, ok := value(name)
			if !ok || boundary.List().Len() == 0 {
				continue
			}
			if name == "not_in" {
				set(name, boundary.List().Get(0))
				continue
			}
			for _, n := range []int64{0, 1, -1, 2, 127, 1024} {
				v, ok := grpcNumericFromInt(field.Kind(), n)
				if ok && !containsProtoValue(boundary.List(), v) {
					set(name, v)
					break
				}
			}
		}
	}
	return out
}

// Bindings may replace a small captured list element with a larger fixture.
// Recheck growth before cloning that element during execution as well as planning.
func grpcRepeatedGrowthBudget(message proto.Message, parent protoreflect.Message, field, rule protoreflect.FieldDescriptor) error {
	if !field.IsList() || rule == nil || rule.Name() != "max_items" && rule.Name() != "unique" {
		return nil
	}
	list := parent.Get(field).List()
	count := uint64(list.Len()) + 1
	if rule.Name() == "max_items" {
		limit := fieldRules(field).GetRepeated().GetMaxItems()
		if limit == math.MaxUint64 {
			return fmt.Errorf("gRPC repeated boundary has no representable witness")
		}
		count = limit + 1
	}
	if count <= uint64(list.Len()) {
		return nil
	}
	if list.Len() == 0 || count > 1024 {
		return fmt.Errorf("gRPC repeated witness cannot grow this bound request safely")
	}
	size := 16
	element := list.Get(0)
	switch field.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		size += proto.Size(element.Message().Interface())
	case protoreflect.StringKind:
		size += len(element.String())
	case protoreflect.BytesKind:
		size += len(element.Bytes())
	}
	budget := (16 << 20) - proto.Size(message)
	if budget < 0 || size > budget/int(count-uint64(list.Len())) {
		return fmt.Errorf("gRPC repeated witness exceeds 16 MiB after runtime bindings")
	}
	return nil
}
func containsProtoValue(list protoreflect.List, want protoreflect.Value) bool {
	for i := 0; i < list.Len(); i++ {
		if fmt.Sprint(list.Get(i).Interface()) == fmt.Sprint(want.Interface()) {
			return true
		}
	}
	return false
}
func grpcNumericViolation(kind protoreflect.Kind, name string, boundary protoreflect.Value) (protoreflect.Value, bool) {
	delta := int64(0)
	if name == "gte" {
		delta = -1
	}
	if name == "lte" || name == "const" {
		delta = 1
	}
	switch kind {
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		n := boundary.Float()
		if delta != 0 {
			direction := math.Inf(int(delta))
			if kind == protoreflect.FloatKind {
				n = float64(math.Nextafter32(float32(n), float32(direction)))
			} else {
				n = math.Nextafter(n, direction)
			}
		}
		if kind == protoreflect.FloatKind {
			return protoreflect.ValueOfFloat32(float32(n)), true
		}
		return protoreflect.ValueOfFloat64(n), true
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		n := boundary.Uint()
		maximum := uint64(math.MaxUint64)
		if kind == protoreflect.Uint32Kind || kind == protoreflect.Fixed32Kind {
			maximum = math.MaxUint32
		}
		if delta < 0 && n == 0 || delta > 0 && n == maximum {
			return protoreflect.Value{}, false
		}
		if delta < 0 {
			n--
		} else if delta > 0 {
			n++
		}
		if maximum == math.MaxUint32 {
			return protoreflect.ValueOfUint32(uint32(n)), true
		}
		return protoreflect.ValueOfUint64(n), true
	default:
		n := boundary.Int()
		minimum, maximum := int64(math.MinInt64), int64(math.MaxInt64)
		small := kind == protoreflect.Int32Kind || kind == protoreflect.Sint32Kind || kind == protoreflect.Sfixed32Kind
		if small {
			minimum, maximum = math.MinInt32, math.MaxInt32
		}
		if delta < 0 && n == minimum || delta > 0 && n == maximum {
			return protoreflect.Value{}, false
		}
		n += delta
		if small {
			return protoreflect.ValueOfInt32(int32(n)), true
		}
		return protoreflect.ValueOfInt64(n), true
	}
}
func grpcNumericFromInt(kind protoreflect.Kind, n int64) (protoreflect.Value, bool) {
	switch kind {
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if n < 0 {
			return protoreflect.Value{}, false
		}
		return protoreflect.ValueOfUint32(uint32(n)), true
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if n < 0 {
			return protoreflect.Value{}, false
		}
		return protoreflect.ValueOfUint64(uint64(n)), true
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(float32(n)), true
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(float64(n)), true
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(int32(n)), true
	default:
		return protoreflect.ValueOfInt64(n), true
	}
}
