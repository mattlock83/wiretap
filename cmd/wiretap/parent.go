package main

import (
	"context"
	"io"
)

// SDK-owned runtimes keep a pipe open. EOF also works when the test process
// crashes or is killed, without depending on platform-specific parent signals.
func watchParent(input io.Reader, cancel context.CancelFunc) {
	_, _ = io.Copy(io.Discard, input)
	cancel()
}
