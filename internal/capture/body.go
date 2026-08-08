package capture

import (
	"bytes"
	"io"
)

const DefaultMaxBodyBytes int64 = 4 * 1024 * 1024

// Body is the bounded prefix retained while a complete body is transferred.
type Body struct {
	Bytes         []byte
	OriginalBytes int64
	Truncated     bool
}

// Writer counts every byte written while retaining at most maxBodyBytes.
// A zero limit explicitly means unlimited capture.
type Writer struct {
	maxBodyBytes int64
	total        int64
	buf          bytes.Buffer
}

func NewWriter(maxBodyBytes int64) *Writer {
	if maxBodyBytes < 0 {
		maxBodyBytes = 0
	}
	return &Writer{maxBodyBytes: maxBodyBytes}
}

func (w *Writer) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.total += int64(len(p))
	retain := len(p)
	if w.maxBodyBytes > 0 {
		remaining := w.maxBodyBytes - int64(w.buf.Len())
		if remaining <= 0 {
			return len(p), nil
		}
		if int64(retain) > remaining {
			retain = int(remaining)
		}
	}
	if retain > 0 {
		_, _ = w.buf.Write(p[:retain])
	}
	return len(p), nil
}

func (w *Writer) Body() Body {
	if w == nil {
		return Body{}
	}
	data := append([]byte(nil), w.buf.Bytes()...)
	return Body{
		Bytes:         data,
		OriginalBytes: w.total,
		Truncated:     int64(len(data)) < w.total,
	}
}

// WrapReadCloser captures bytes as the wrapped body is consumed.
func WrapReadCloser(body io.ReadCloser, maxBodyBytes int64) (io.ReadCloser, *Writer) {
	writer := NewWriter(maxBodyBytes)
	if body == nil {
		return io.NopCloser(bytes.NewReader(nil)), writer
	}
	return &capturingReadCloser{
		Reader: io.TeeReader(body, writer),
		Closer: body,
	}, writer
}

type capturingReadCloser struct {
	io.Reader
	io.Closer
}

// ReadAll consumes the complete reader while retaining only the configured
// prefix. It is useful when the body must be consumed before choosing a
// response but must not be buffered in full.
func ReadAll(reader io.Reader, maxBodyBytes int64) (Body, error) {
	writer := NewWriter(maxBodyBytes)
	_, err := io.Copy(writer, reader)
	return writer.Body(), err
}

// FromBytes applies the same bounded representation to an already materialized
// body, such as a generated proxy error response.
func FromBytes(data []byte, maxBodyBytes int64) Body {
	writer := NewWriter(maxBodyBytes)
	_, _ = writer.Write(data)
	return writer.Body()
}
