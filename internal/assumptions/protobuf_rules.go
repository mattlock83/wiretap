package assumptions

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// rulesMatch compares declarations reachable from the selected RPC, separately
// from the observed-value wire check. Enum additions and changes to optional
// fields therefore remain visible even when this particular message is valid.
func (v *wireVerifier) rulesMatch(consumer, provider sample) ([]string, error) {
	var rules [2]string
	for i, entry := range []sample{consumer, provider} {
		if entry.flow.Request.GRPC == nil {
			return nil, fmt.Errorf("a native gRPC request is required for protobuf rule comparison")
		}
		contract, err := v.load(entry.schema)
		if err != nil {
			return nil, err
		}
		procedure := "/" + strings.TrimPrefix(entry.flow.Request.GRPC.Procedure, "/")
		method := contract.contract.Methods[procedure]
		if method == nil {
			return nil, fmt.Errorf("selected protobuf method %q is absent from the frozen contract", procedure)
		}
		projector := protobufRuleProjector{messages: map[string]any{}, enums: map[string]any{}, files: map[string]string{}}
		input, err := projector.message(method.Input(), 0)
		if err != nil {
			return nil, err
		}
		output, err := projector.message(method.Output(), 0)
		if err != nil {
			return nil, err
		}
		methodOptions, err := protobufOptionSignature(method.Options())
		if err != nil {
			return nil, err
		}
		serviceOptions, err := protobufOptionSignature(method.Parent().Options())
		if err != nil {
			return nil, err
		}
		fileOptions, err := projector.fileOptions(method.ParentFile())
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(map[string]any{
			"procedure": procedure, "client_streaming": method.IsStreamingClient(), "server_streaming": method.IsStreamingServer(),
			"request": input, "response": output, "messages": projector.messages, "enums": projector.enums,
			"method_options": methodOptions, "service_options": serviceOptions, "file_options": fileOptions,
		})
		if err != nil {
			return nil, err
		}
		rules[i] = string(data)
	}
	if rules[0] == rules[1] {
		return nil, nil
	}
	return jsonDifferencePaths(rules[0], rules[1], "protobuf/contract"), nil
}

type protobufRuleProjector struct {
	messages, enums map[string]any
	files           map[string]string
	nodes           int
}

func (p *protobufRuleProjector) admit(depth int) error {
	p.nodes++
	if depth > 128 || p.nodes > 50000 {
		return fmt.Errorf("protobuf rule graph exceeds its nesting or node limit")
	}
	return nil
}

func (p *protobufRuleProjector) message(message protoreflect.MessageDescriptor, depth int) (string, error) {
	name := string(message.FullName())
	if _, seen := p.messages[name]; seen {
		return name, nil
	}
	if err := p.admit(depth); err != nil {
		return "", err
	}
	// Register before traversal so recursive message graphs remain finite.
	p.messages[name] = nil
	if message.ExtensionRanges().Len() != 0 || message.Extensions().Len() != 0 {
		return "", fmt.Errorf("protobuf message %q uses extensions whose complete rule inventory is unavailable", name)
	}
	options, err := protobufOptionSignature(message.Options())
	if err != nil {
		return "", err
	}
	fileOptions, err := p.fileOptions(message.ParentFile())
	if err != nil {
		return "", err
	}
	fields := map[string]any{}
	for i := 0; i < message.Fields().Len(); i++ {
		field := message.Fields().Get(i)
		if err := p.admit(depth + 1); err != nil {
			return "", err
		}
		options, err := protobufOptionSignature(field.Options())
		if err != nil {
			return "", err
		}
		rule := map[string]any{
			"name": string(field.Name()), "json_name": field.JSONName(), "kind": field.Kind().String(),
			"cardinality": field.Cardinality().String(), "presence": field.HasPresence(),
			"optional_keyword": field.HasOptionalKeyword(), "packed": field.IsPacked(), "map": field.IsMap(), "options": options,
		}
		if oneof := field.ContainingOneof(); oneof != nil {
			rule["oneof"] = string(oneof.Name())
		}
		if child := field.Message(); child != nil {
			name, err := p.message(child, depth+1)
			if err != nil {
				return "", err
			}
			rule["message"] = name
		} else if enum := field.Enum(); enum != nil {
			name, err := p.enum(enum, depth+1)
			if err != nil {
				return "", err
			}
			rule["enum"] = name
		}
		if !field.IsList() && !field.IsMap() && field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
			rule["explicit_default"] = field.HasDefault()
			rule["default"] = protobufFieldDefault(field)
		}
		fields[strconv.Itoa(int(field.Number()))] = rule
	}
	oneofs := map[string]any{}
	for i := 0; i < message.Oneofs().Len(); i++ {
		oneof := message.Oneofs().Get(i)
		options, err := protobufOptionSignature(oneof.Options())
		if err != nil {
			return "", err
		}
		oneofs[string(oneof.Name())] = map[string]any{"synthetic": oneof.IsSynthetic(), "options": options}
	}
	reservedNames := make([]string, message.ReservedNames().Len())
	for i := range reservedNames {
		reservedNames[i] = string(message.ReservedNames().Get(i))
	}
	sort.Strings(reservedNames)
	reservedRanges := make([][2]int32, message.ReservedRanges().Len())
	for i := range reservedRanges {
		rangeValue := message.ReservedRanges().Get(i)
		reservedRanges[i] = [2]int32{int32(rangeValue[0]), int32(rangeValue[1])}
	}
	sort.Slice(reservedRanges, func(i, j int) bool { return reservedRanges[i][0] < reservedRanges[j][0] })
	p.messages[name] = map[string]any{"syntax": message.ParentFile().Syntax().String(), "map_entry": message.IsMapEntry(), "fields": fields, "oneofs": oneofs, "reserved_names": reservedNames, "reserved_ranges": reservedRanges, "options": options, "file_options": fileOptions}
	return name, nil
}

func (p *protobufRuleProjector) enum(enum protoreflect.EnumDescriptor, depth int) (string, error) {
	name := string(enum.FullName())
	if _, seen := p.enums[name]; seen {
		return name, nil
	}
	if err := p.admit(depth); err != nil {
		return "", err
	}
	options, err := protobufOptionSignature(enum.Options())
	if err != nil {
		return "", err
	}
	fileOptions, err := p.fileOptions(enum.ParentFile())
	if err != nil {
		return "", err
	}
	values := map[string]any{}
	jsonNames := map[string]string{}
	for i := 0; i < enum.Values().Len(); i++ {
		value := enum.Values().Get(i)
		if err := p.admit(depth + 1); err != nil {
			return "", err
		}
		options, err := protobufOptionSignature(value.Options())
		if err != nil {
			return "", err
		}
		values[string(value.Name())] = map[string]any{"number": int32(value.Number()), "options": options}
		number := strconv.FormatInt(int64(value.Number()), 10)
		if _, seen := jsonNames[number]; !seen {
			// ProtoJSON emits the first declared label for an aliased number.
			// Other declaration ordering does not change the value-name mapping.
			jsonNames[number] = string(value.Name())
		}
	}
	p.enums[name] = map[string]any{"values": values, "json_names": jsonNames, "closed": enum.IsClosed(), "syntax": enum.ParentFile().Syntax().String(), "options": options, "file_options": fileOptions}
	return name, nil
}

func protobufFieldDefault(field protoreflect.FieldDescriptor) any {
	value := field.Default()
	switch field.Kind() {
	case protoreflect.BoolKind:
		return value.Bool()
	case protoreflect.StringKind:
		return value.String()
	case protoreflect.BytesKind:
		return base64.StdEncoding.EncodeToString(value.Bytes())
	case protoreflect.EnumKind:
		return strconv.FormatInt(int64(value.Enum()), 10)
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return strconv.FormatFloat(value.Float(), 'g', -1, 64)
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		return strconv.FormatUint(value.Uint(), 10)
	default:
		return strconv.FormatInt(value.Int(), 10)
	}
}

func (p *protobufRuleProjector) fileOptions(file protoreflect.FileDescriptor) (string, error) {
	if value, found := p.files[file.Path()]; found {
		return value, nil
	}
	value, err := protobufOptionSignature(file.Options())
	if err != nil {
		return "", err
	}
	p.files[file.Path()] = value
	return value, nil
}

// Retain custom/uninterpreted options and runtime features as opaque declarations.
// This detects annotation drift without claiming to execute a validation plugin.
// Language packaging, code generation and deprecation are not API data rules.
func protobufOptionSignature(options protoreflect.ProtoMessage) (string, error) {
	if options == nil || !options.ProtoReflect().IsValid() {
		return "", nil
	}
	copy := proto.Clone(options).ProtoReflect()
	ignored := map[protoreflect.Name]bool{}
	for _, name := range []string{
		"deprecated", "java_package", "java_outer_classname", "java_multiple_files", "java_generate_equals_and_hash",
		"optimize_for", "go_package", "cc_generic_services", "java_generic_services", "py_generic_services", "php_generic_services", "cc_enable_arenas",
		"objc_class_prefix", "csharp_namespace", "swift_prefix", "php_class_prefix", "php_namespace", "php_metadata_namespace", "ruby_package",
		"no_standard_descriptor_accessor", "ctype", "jstype", "lazy", "unverified_lazy", "debug_redact", "retention", "targets", "edition_defaults", "feature_support",
	} {
		ignored[protoreflect.Name(name)] = true
	}
	copy.Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if !field.IsExtension() && ignored[field.Name()] {
			copy.Clear(field)
		}
		return true
	})
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(copy.Interface())
	if err != nil {
		return "", err
	}
	if len(encoded) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}
