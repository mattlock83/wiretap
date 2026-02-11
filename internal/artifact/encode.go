package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MarshalJSON ensures the message is encoded as HTTP payload.
func (m Message) MarshalJSON() ([]byte, error) {
	if m.HTTP != nil {
		return json.Marshal(m.HTTP)
	}
	return []byte("null"), nil
}

// UnmarshalJSON decodes HTTP message payloads.
func (m *Message) UnmarshalJSON(data []byte) error {
	if m == nil {
		return nil
	}
	*m = Message{}
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) || len(trimmed) == 0 {
		return nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if _, ok := raw["method"]; ok || raw["url"] != nil || raw["status"] != nil {
		var msg HttpMessage
		if err := json.Unmarshal(data, &msg); err == nil {
			m.HTTP = &msg
			return nil
		}
	}

	var httpMsg HttpMessage
	if err := json.Unmarshal(data, &httpMsg); err == nil {
		if httpMsg.Method != "" || httpMsg.URL != "" || httpMsg.Status != 0 || httpMsg.Headers != nil || httpMsg.Body != nil {
			m.HTTP = &httpMsg
			return nil
		}
	}

	return nil
}

// UnmarshalYAML ensures YAML (including JSON-as-YAML) decodes into HTTP messages.
func (m *Message) UnmarshalYAML(value *yaml.Node) error {
	if m == nil {
		return nil
	}
	var raw any
	if err := value.Decode(&raw); err != nil {
		return err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return m.UnmarshalJSON(data)
}

// MarshalJSON ensures header keys are encoded in a deterministic order.
func (h Headers) MarshalJSON() ([]byte, error) {
	if h == nil {
		return []byte("null"), nil
	}
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		valJSON, err := json.Marshal(h[key])
		if err != nil {
			return nil, err
		}
		b.Write(keyJSON)
		b.WriteByte(':')
		b.Write(valJSON)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// Encode writes the artifact as JSON (valid YAML 1.2) with stable ordering.
func Encode(w io.Writer, artifact *Artifact) error {
	if artifact == nil {
		return errors.New("artifact is nil")
	}
	artifact.Normalize()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(artifact)
}

// EncodeBytes returns the encoded artifact bytes.
func EncodeBytes(artifact *Artifact) ([]byte, error) {
	var buf bytes.Buffer
	if err := Encode(&buf, artifact); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
