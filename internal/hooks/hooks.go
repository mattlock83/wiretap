// Package hooks defines the data exchanged with test-owned lifecycle callbacks.
package hooks

const Version = 1

type Check struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Output contains runtime-only context, plus explicit assertion results. The
// caller decides which check evidence to persist; bindings/state are not logs.
type Output struct {
	Bindings map[string]any    `json:"bindings,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	State    map[string]any    `json:"state,omitempty"`
	Checks   []Check           `json:"checks,omitempty"`
}
