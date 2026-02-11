package app

import (
	"sync"

	"github.com/phrond/evermock/internal/proxy"
)

type proxyRecorder interface {
	RecordHTTP(exchange proxy.HTTPExchange)
}

type ProxyFanoutRecorder struct {
	recorders []proxyRecorder
}

func (f *ProxyFanoutRecorder) RecordHTTP(exchange proxy.HTTPExchange) {
	if f == nil {
		return
	}
	for _, recorder := range f.recorders {
		if recorder == nil {
			continue
		}
		recorder.RecordHTTP(exchange)
	}
}

type SessionProxyRecorder struct {
	mu  sync.RWMutex
	app *App
}

func NewSessionProxyRecorder() *SessionProxyRecorder {
	return &SessionProxyRecorder{}
}

func (r *SessionProxyRecorder) SetApp(app *App) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.app = app
}

func (r *SessionProxyRecorder) RecordHTTP(exchange proxy.HTTPExchange) {
	r.mu.RLock()
	app := r.app
	r.mu.RUnlock()
	if app == nil {
		return
	}
	app.recordProxyForSession(exchange)
}
