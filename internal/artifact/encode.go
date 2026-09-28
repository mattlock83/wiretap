package artifact

import (
	"bytes"
	"encoding/json"
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

	var msg HttpMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return err
	}
	m.HTTP = &msg
	return nil
}
