package artifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

func (m GrpcMessage) MarshalJSON() ([]byte, error) {
	type alias GrpcMessage
	raw, err := json.Marshal(alias(m))
	if err != nil {
		return nil, err
	}
	if m.HTTPStatus == 0 {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields["grpc_status"] = json.RawMessage(strconv.Itoa(m.StatusCode))
	return json.Marshal(fields)
}

func (m *GrpcMessage) UnmarshalJSON(data []byte) error {
	type alias GrpcMessage
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.HTTPStatus != 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if status, ok := fields["grpc_status"]; !ok || bytes.Equal(bytes.TrimSpace(status), []byte("null")) {
			return fmt.Errorf("gRPC response requires explicit grpc_status")
		}
	}
	*m = GrpcMessage(decoded)
	return nil
}

// MarshalJSON encodes the protocol-specific message.
func (m Message) MarshalJSON() ([]byte, error) {
	if m.HTTP != nil && m.GRPC != nil {
		return nil, fmt.Errorf("message contains both HTTP and gRPC payloads")
	}
	if m.HTTP != nil {
		return json.Marshal(m.HTTP)
	}
	if m.GRPC != nil {
		if m.GRPC.Kind != "grpc" {
			return nil, fmt.Errorf("invalid gRPC message kind %q", m.GRPC.Kind)
		}
		return json.Marshal(m.GRPC)
	}
	return []byte("null"), nil
}

// UnmarshalJSON requires an explicit tag for gRPC; untagged messages are HTTP.
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
	if _, ok := probe["kind"]; ok {
		var msg GrpcMessage
		if err := json.Unmarshal(trimmed, &msg); err != nil {
			return err
		}
		if msg.Kind != "grpc" {
			return fmt.Errorf("unknown message kind %q", msg.Kind)
		}
		m.GRPC = &msg
		return nil
	}
	for _, key := range []string{"procedure", "grpc_status", "messages"} {
		if _, ok := probe[key]; ok {
			return fmt.Errorf("gRPC message requires explicit kind tag")
		}
	}
	var msg HttpMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return err
	}
	m.HTTP = &msg
	return nil
}
