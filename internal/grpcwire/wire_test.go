package grpcwire

import "testing"

func TestEncodeDecodeFrame(t *testing.T) {
	msg := []byte("hello")
	frame := EncodeFrame(msg)
	frames, err := DecodeFrames(frame)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if string(frames[0].Message) != "hello" {
		t.Fatalf("unexpected message: %s", string(frames[0].Message))
	}
}

func TestParsePath(t *testing.T) {
	service, method := ParsePath("/pkg.Service/Do")
	if service != "pkg.Service" || method != "Do" {
		t.Fatalf("unexpected parse: %s %s", service, method)
	}
}
