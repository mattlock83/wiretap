package grpcwire

import (
	"encoding/binary"
	"errors"
	"strings"
)

// Frame represents a single gRPC message frame.
type Frame struct {
	Compressed bool
	Message    []byte
}

// DecodeFrames parses gRPC frames from the provided payload.
func DecodeFrames(data []byte) ([]Frame, error) {
	var frames []Frame
	offset := 0
	for offset < len(data) {
		if len(data)-offset < 5 {
			return frames, errors.New("grpc frame header truncated")
		}
		flag := data[offset]
		length := int(binary.BigEndian.Uint32(data[offset+1 : offset+5]))
		offset += 5
		if length < 0 || len(data)-offset < length {
			return frames, errors.New("grpc frame length exceeds payload")
		}
		msg := data[offset : offset+length]
		frames = append(frames, Frame{Compressed: flag == 1, Message: msg})
		offset += length
	}
	return frames, nil
}

// EncodeFrame wraps a single message into a gRPC frame.
func EncodeFrame(message []byte) []byte {
	frame := make([]byte, 5+len(message))
	frame[0] = 0
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(message)))
	copy(frame[5:], message)
	return frame
}

// JoinMessages concatenates all decoded frame payloads.
func JoinMessages(frames []Frame) []byte {
	if len(frames) == 0 {
		return nil
	}
	size := 0
	for _, frame := range frames {
		size += len(frame.Message)
	}
	buf := make([]byte, 0, size)
	for _, frame := range frames {
		buf = append(buf, frame.Message...)
	}
	return buf
}

// ParsePath extracts service and method from a gRPC path.
func ParsePath(path string) (service string, method string) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) >= 2 {
		service = parts[0]
		method = parts[1]
	}
	return service, method
}
