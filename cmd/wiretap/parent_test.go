package main

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestParentPipeCancelsRuntimeOnEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go watchParent(reader, cancel)
	if _, err := writer.Write([]byte("alive")); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("runtime stopped while its parent pipe remained open")
	}
	writer.Close()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after parent pipe closed")
	}
}
