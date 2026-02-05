package grpcmock

import (
	"encoding/base64"
	"strings"
	"sync"
)

// Registry holds registered stubs.
type Registry struct {
	mu    sync.RWMutex
	stubs []Stub
}

// NewRegistry creates a new stub registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a stub to the registry.
func (r *Registry) Register(stub Stub) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs = append(r.stubs, stub)
}

// Clear removes all stubs.
func (r *Registry) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs = nil
}

// Match finds the first stub that matches the request.
func (r *Registry) Match(req RecordedRequest) (Stub, bool) {
	if r == nil {
		return Stub{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, stub := range r.stubs {
		if !pathMatches(req.Path, stub) {
			continue
		}
		if !headersMatch(req.Headers, stub.Match.Headers) {
			continue
		}
		if !bodyMatches(req.Body, stub.Match.BodyBase64) {
			continue
		}
		return stub, true
	}
	return Stub{}, false
}

func pathMatches(path string, stub Stub) bool {
	if stub.Path != "" {
		return stub.Path == path
	}
	if stub.Service != "" && stub.Method != "" {
		return "/"+stub.Service+"/"+stub.Method == path
	}
	return true
}

func headersMatch(headers map[string][]string, expected map[string][]string) bool {
	if len(expected) == 0 {
		return true
	}
	for key, values := range expected {
		actual := headerValues(headers, key)
		if len(actual) == 0 {
			return false
		}
		if len(values) == 0 {
			continue
		}
		matched := false
		for _, expect := range values {
			for _, got := range actual {
				if got == expect {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func headerValues(headers map[string][]string, key string) []string {
	for k, values := range headers {
		if strings.EqualFold(k, key) {
			return values
		}
	}
	return nil
}

func bodyMatches(body []byte, expectedBase64 string) bool {
	if expectedBase64 == "" {
		return true
	}
	actual := base64.StdEncoding.EncodeToString(body)
	return actual == expectedBase64
}
