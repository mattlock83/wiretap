package capture

import (
	"bytes"
	"io"
	"testing"
)

func TestWriterRetainsPrefixAndCountsCompleteBody(t *testing.T) {
	writer := NewWriter(4)
	if n, err := writer.Write([]byte("abcdefgh")); err != nil || n != 8 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	body := writer.Body()
	if got := string(body.Bytes); got != "abcd" {
		t.Fatalf("captured bytes = %q", got)
	}
	if body.OriginalBytes != 8 || !body.Truncated {
		t.Fatalf("capture metadata = %#v", body)
	}
}

func TestWrapReadCloserForwardsCompleteBody(t *testing.T) {
	wrapped, writer := WrapReadCloser(io.NopCloser(bytes.NewReader([]byte("abcdefgh"))), 3)
	got, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("read wrapped body: %v", err)
	}
	if string(got) != "abcdefgh" {
		t.Fatalf("forwarded body = %q", got)
	}
	body := writer.Body()
	if string(body.Bytes) != "abc" || body.OriginalBytes != 8 || !body.Truncated {
		t.Fatalf("capture = %#v", body)
	}
}

func TestZeroLimitIsUnlimited(t *testing.T) {
	body := FromBytes([]byte("abcdefgh"), 0)
	if string(body.Bytes) != "abcdefgh" || body.OriginalBytes != 8 || body.Truncated {
		t.Fatalf("capture = %#v", body)
	}
}
