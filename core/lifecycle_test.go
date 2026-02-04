package core

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(call string) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

type spyComponent struct {
	name     string
	rec      *recorder
	startErr error
	stopErr  error
}

func (s *spyComponent) Start(_ context.Context) error {
	s.rec.add(s.name + ":start")
	return s.startErr
}

func (s *spyComponent) Stop(_ context.Context) error {
	s.rec.add(s.name + ":stop")
	return s.stopErr
}

func TestLifecycleStartStopOrder(t *testing.T) {
	rec := &recorder{}
	proxy := &spyComponent{name: "proxy", rec: rec}
	mock := &spyComponent{name: "mock", rec: rec}
	l := &Lifecycle{Proxy: proxy, Mock: mock}

	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := l.Stop(context.Background()); err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	want := []string{"proxy:start", "mock:start", "mock:stop", "proxy:stop"}
	rec.mu.Lock()
	calls := append([]string{}, rec.calls...)
	rec.mu.Unlock()

	if len(calls) != len(want) {
		t.Fatalf("unexpected calls length: got %v want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d: got %q want %q", i, calls[i], want[i])
		}
	}
}

func TestLifecycleStartRollback(t *testing.T) {
	rec := &recorder{}
	proxy := &spyComponent{name: "proxy", rec: rec}
	mock := &spyComponent{name: "mock", rec: rec, startErr: errors.New("boom")}
	l := &Lifecycle{Proxy: proxy, Mock: mock}

	err := l.Start(context.Background())
	if err == nil {
		t.Fatal("expected start error")
	}

	want := []string{"proxy:start", "mock:start", "proxy:stop"}
	rec.mu.Lock()
	calls := append([]string{}, rec.calls...)
	rec.mu.Unlock()

	if len(calls) != len(want) {
		t.Fatalf("unexpected calls: %v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d: got %q want %q", i, calls[i], want[i])
		}
	}
}
