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

func (r *Registry) RemoveIDs(ids []string) int {
	if len(ids) == 0 {
		return 0
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		idSet[trimmed] = struct{}{}
	}
	if len(idSet) == 0 {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	kept := r.stubs[:0]
	removed := 0
	for _, stub := range r.stubs {
		if _, found := idSet[stub.ID]; found {
			removed++
			continue
		}
		kept = append(kept, stub)
	}
	r.stubs = kept
	return removed
}

func (r *Registry) List() []Stub {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Stub, len(r.stubs))
	copy(out, r.stubs)
	return out
}

// MatchTarget describes an incoming request after routing prefixes
// (session/dependency) have been stripped from the path.
type MatchTarget struct {
	SessionID string
	AssetID   string
	Path      string
}

func (r *Registry) Match(req *http.Request, body []byte, target MatchTarget, bodyComplete ...bool) (Stub, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	complete := true
	if len(bodyComplete) > 0 {
		complete = bodyComplete[0]
	}

	// Stack semantics: newest non-default matching response is consumed first.
	for i := len(r.stubs) - 1; i >= 0; i-- {
		stub := r.stubs[i]
		if stub.Default {
			continue
		}
		if !matchStub(stub, req, body, target, complete) {
			continue
		}
		r.stubs = append(r.stubs[:i], r.stubs[i+1:]...)
		return stub, true
	}

	// Default responses are a fallback and are not consumed on match.
	for i := len(r.stubs) - 1; i >= 0; i-- {
		stub := r.stubs[i]
		if !stub.Default {
			continue
		}
		if !matchStub(stub, req, body, target, complete) {
			continue
		}
		return stub, true
	}
	return Stub{}, false
}

func matchStub(stub Stub, req *http.Request, body []byte, target MatchTarget, bodyComplete bool) bool {
	// Scope: a session-bound stub only serves its session; an asset-bound stub
	// only serves requests routed to that dependency (and vice versa).
	if stub.SessionID != "" && stub.SessionID != target.SessionID {
		return false
	}
	if stub.AssetID != target.AssetID {
		return false
	}
	match := stub.Match
	if match.Method != "" && !strings.EqualFold(match.Method, req.Method) {
		return false
	}
	if match.Path != "" && match.Path != target.Path {
		return false
	}
	if match.Body != "" {
		if !bodyComplete || match.Body != string(body) {
			return false
		}
	}
	if match.BodyContains != "" && !strings.Contains(string(body), match.BodyContains) {
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
