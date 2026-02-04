package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// MarshalJSON ensures the message is encoded as HTTP or gRPC payload.
func (m Message) MarshalJSON() ([]byte, error) {
	if m.HTTP != nil {
		return json.Marshal(m.HTTP)
	}
	if m.GRPC != nil {
		return json.Marshal(m.GRPC)
	}
	return []byte("null"), nil
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
