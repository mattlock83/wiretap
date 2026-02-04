package mock

import (
	"net/http"
	"strings"
	"sync"
)

// Registry stores stubs in registration order.
type Registry struct {
	mu    sync.RWMutex
	stubs []Stub
}

func NewRegistry() *Registry {
	return &Registry{stubs: make([]Stub, 0)}
}

func (r *Registry) Register(stub Stub) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs = append(r.stubs, stub)
}

func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs = r.stubs[:0]
}

func (r *Registry) Match(req *http.Request, body []byte) (Stub, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, stub := range r.stubs {
		if matchStub(stub, req, body) {
			return stub, true
		}
	}
	return Stub{}, false
}

func matchStub(stub Stub, req *http.Request, body []byte) bool {
	match := stub.Match
	if match.Method != "" && !strings.EqualFold(match.Method, req.Method) {
		return false
	}
	if match.Path != "" && match.Path != req.URL.Path {
		return false
	}
	if match.Body != "" && match.Body != string(body) {
		return false
	}
	if len(match.Headers) > 0 {
		for key, expected := range match.Headers {
			actual := req.Header.Get(key)
			if actual != expected {
				return false
			}
		}
	}
	if len(match.Query) > 0 {
		values := req.URL.Query()
		for key, expected := range match.Query {
			actual := values.Get(key)
			if actual != expected {
				return false
			}
		}
	}
	return true
}
