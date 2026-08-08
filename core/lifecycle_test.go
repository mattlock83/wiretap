package core

import (
	"context"
	"sync"
	"testing"
)

type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

type spyComponent struct{ rec *recorder }

func (s *spyComponent) Start(context.Context) error { s.rec.add("proxy:start"); return nil }
func (s *spyComponent) Stop(context.Context) error  { s.rec.add("proxy:stop"); return nil }

func TestLifecycleStartsAndStopsCaptureProxy(t *testing.T) {
	rec := &recorder{}
	l := &Lifecycle{Proxy: &spyComponent{rec: rec}}
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := l.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rec.calls; len(got) != 2 || got[0] != "proxy:start" || got[1] != "proxy:stop" {
		t.Fatalf("unexpected calls: %v", got)
	}
}
