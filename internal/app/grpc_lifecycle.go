package app

import (
	"context"
	"fmt"
	"time"
)

type activeGRPCCall struct {
	sessionID string
	cancel    context.CancelFunc
	snapshot  func()
	done      chan struct{}
	ready     chan struct{}
}

func (a *App) trackGRPCCall(sessionID string) func(context.CancelFunc, func()) func() {
	// Reserve under admission's ownership lock, before teardown can snapshot.
	call := &activeGRPCCall{sessionID: sessionID, done: make(chan struct{}), ready: make(chan struct{})}
	a.grpcCallsMu.Lock()
	if a.grpcCalls == nil {
		a.grpcCalls = map[*activeGRPCCall]struct{}{}
	}
	a.grpcCalls[call] = struct{}{}
	a.grpcCallsMu.Unlock()
	return func(cancel context.CancelFunc, snapshot func()) func() {
		call.cancel, call.snapshot = cancel, snapshot
		close(call.ready)
		return func() {
			a.grpcCallsMu.Lock()
			delete(a.grpcCalls, call)
			close(call.done)
			a.grpcCallsMu.Unlock()
		}
	}
}
func (a *App) snapshotGRPCCalls(sessionID string) {
	a.grpcCallsMu.Lock()
	var calls []*activeGRPCCall
	for call := range a.grpcCalls {
		if call.sessionID == sessionID {
			calls = append(calls, call)
		}
	}
	a.grpcCallsMu.Unlock()
	for _, call := range calls {
		<-call.ready
		call.snapshot()
	}
}
func (a *App) stopGRPCCalls(ctx context.Context) error {
	a.grpcCallsMu.Lock()
	var calls []*activeGRPCCall
	for call := range a.grpcCalls {
		calls = append(calls, call)
	}
	a.grpcCallsMu.Unlock()
	for _, call := range calls {
		select {
		case <-call.ready:
		case <-ctx.Done():
			return fmt.Errorf("prepare gRPC termination: %w", ctx.Err())
		}
		call.snapshot()
		call.cancel()
	}
	for _, call := range calls {
		select {
		case <-call.done:
		case <-ctx.Done():
			return fmt.Errorf("stop gRPC calls: %w", ctx.Err())
		}
	}
	// The transport callback returns immediately before the admission releases.
	// Wait for those releases so a completed run cannot race retired ownership.
	if len(calls) > 0 {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			a.sessionsMu.Lock()
			pending := a.inFlight
			a.sessionsMu.Unlock()
			if pending == 0 {
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return fmt.Errorf("release gRPC admissions: %w", ctx.Err())
			}
		}
	}
	return nil
}
