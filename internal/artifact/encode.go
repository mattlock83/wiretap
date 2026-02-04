package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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
