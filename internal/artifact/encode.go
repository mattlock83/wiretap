package artifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// MarshalJSON encodes the protocol-specific message.
func (m Message) MarshalJSON() ([]byte, error) {
	if m.HTTP != nil {
		return json.Marshal(m.HTTP)
	}
	return []byte("null"), nil
}

// UnmarshalJSON accepts HTTP messages and rejects unsupported message kinds.
func (m *Message) UnmarshalJSON(data []byte) error {
	if m == nil {
		return nil
	}
	*m = Message{}
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) || len(trimmed) == 0 {
		return nil
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return err
	}
	for key := range probe {
		for _, removed := range []string{"kind", "procedure", "grpc_status", "messages"} {
			if strings.EqualFold(key, removed) {
				return fmt.Errorf("unsupported message field %q; only HTTP messages are supported", key)
			}
		}
	}
	var msg HttpMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return err
	}
	m.HTTP = &msg
	return nil
}

// UnmarshalJSON rejects unsupported evidence before legacy fields can be ignored.
func (f *Flow) UnmarshalJSON(data []byte) error {
	type plain Flow
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		if strings.EqualFold(key, "interaction") {
			return fmt.Errorf("event interaction evidence is unsupported; only HTTP request/response captures are supported")
		}
		if strings.EqualFold(key, "grpc_events") {
			return fmt.Errorf("native gRPC capture evidence is unsupported")
		}
		if strings.EqualFold(key, "protocol") {
			var protocol string
			if err := json.Unmarshal(raw, &protocol); err != nil {
				return err
			}
			if protocol != "" && protocol != "http" {
				return fmt.Errorf("unsupported capture protocol %q; only HTTP is supported", protocol)
			}
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Protocol != "" && value.Protocol != "http" {
		return fmt.Errorf("unsupported capture protocol %q; only HTTP is supported", value.Protocol)
	}
	*f = Flow(value)
	return nil
}

// UnmarshalJSON rejects removed RPC contracts and acquisition metadata instead
// of silently discarding them while decoding an HTTP contract.
func (ref *SchemaRef) UnmarshalJSON(data []byte) error {
	type plain SchemaRef
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		for _, removed := range []string{"services", "acquired_from", "acquisition"} {
			if strings.EqualFold(key, removed) {
				return fmt.Errorf("native gRPC contract field %q is unsupported", key)
			}
		}
		if strings.EqualFold(key, "type") || strings.EqualFold(key, "protocol") {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			if strings.EqualFold(key, "type") && strings.EqualFold(strings.TrimSpace(value), "protobuf") || strings.EqualFold(key, "protocol") && UnsupportedRPCProtocol(value) {
				return fmt.Errorf("native gRPC and protobuf contracts are unsupported")
			}
		}
	}
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.Type != "openapi" || value.Protocol != "" && value.Protocol != "http" {
		return fmt.Errorf("unsupported contract type/protocol %q/%q; only OpenAPI over HTTP is supported", value.Type, value.Protocol)
	}
	*ref = SchemaRef(value)
	return nil
}

// UnmarshalJSON accepts OpenAPI sources and rejects removed source metadata.
func (source *SchemaSource) UnmarshalJSON(data []byte) error {
	type plain SchemaSource
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		if strings.EqualFold(key, "files") {
			return fmt.Errorf("opaque contract packages are unsupported; use an OpenAPI source")
		}
		if strings.EqualFold(key, "import_paths") {
			return fmt.Errorf("protobuf import paths are unsupported")
		}
		if strings.EqualFold(key, "type") || strings.EqualFold(key, "format") {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "protobuf_inline", "proto", "protobuf", "descriptor_set", "file_descriptor_set", "reflection":
				return fmt.Errorf("protobuf contract sources are unsupported")
			}
		}
	}
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.Type == "package" {
		return fmt.Errorf("opaque contract packages are unsupported; use an OpenAPI source")
	}
	*source = SchemaSource(value)
	return nil
}
