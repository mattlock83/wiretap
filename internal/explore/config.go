// Package explore generates and runs negative provider tests from verified
// capture evidence. Test-code hooks supply fixtures; bundles remain build artifacts.
package explore

import (
	"fmt"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
)

const Version = "wiretap-negative-1"

type Config struct {
	HookScopes           []HookScope          `json:"hooks,omitempty"`
	HookTimeout          string               `json:"hook_timeout,omitempty"`
	ExpectedGRPCStatuses []int                `json:"expected_grpc_statuses,omitempty"`
	ExpectedStatuses     []int                `json:"expected_statuses"`
	MaxCases             int                  `json:"max_cases"`
	RequestTimeout       string               `json:"request_timeout"`
	Bindings             []Binding            `json:"bindings"`
	Extract              []Extraction         `json:"extract"`
	Redactions           []artifact.Redaction `json:"redactions"`
}

// Bindings reference runtime values returned by hooks or extracted from earlier
// responses. Body paths are JSON pointers (ProtoJSON field names for gRPC).
// A nil gRPC MessageIndex binds every request message; HTTP path names refer to
// OpenAPI parameters.
type Binding struct {
	MessageIndex *int   `json:"message_index,omitempty"`
	Name         string `json:"name"`
	FlowID       string `json:"flow_id"`
	In           string `json:"in"` // body; HTTP query/header/path/cookie; gRPC metadata
	Path         string `json:"path"`
}

// Extraction reads an earlier response. A nil gRPC MessageIndex selects the
// first response message; metadata and trailers select one named value.
type Extraction struct {
	MessageIndex *int   `json:"message_index,omitempty"`
	Name         string `json:"name"`
	FlowID       string `json:"flow_id"`
	In           string `json:"in"` // body; HTTP header; gRPC metadata/trailers
	Path         string `json:"path"`
}

func (c *Config) Defaults() {
	if len(c.ExpectedGRPCStatuses) == 0 {
		c.ExpectedGRPCStatuses = []int{3}
	}
	if len(c.ExpectedStatuses) == 0 {
		c.ExpectedStatuses = []int{400, 422}
	}
	if c.HookTimeout == "" {
		c.HookTimeout = "30s"
	}
	if c.RequestTimeout == "" {
		c.RequestTimeout = "15s"
	}
}
func (c Config) Validate() error {
	if c.MaxCases < 0 || c.MaxCases > 10000 {
		return fmt.Errorf("explore.max_cases must be zero (all cases) or between 1 and 10000")
	}
	if d, err := time.ParseDuration(c.RequestTimeout); err != nil || d <= 0 {
		return fmt.Errorf("explore.request_timeout must be a positive duration")
	}
	if d, err := time.ParseDuration(c.HookTimeout); err != nil || d <= 0 {
		return fmt.Errorf("explore.hook_timeout must be a positive duration")
	}
	seenHookIDs := map[string]bool{}
	for _, scope := range c.HookScopes {
		if scope.ID == "" || seenHookIDs[scope.ID] {
			return fmt.Errorf("hooks require unique nonempty ids")
		}
		seenHookIDs[scope.ID] = true
		if !validHookEvent(scope.Event) {
			return fmt.Errorf("unknown exploration hook %q", scope.Event)
		}
		for _, operation := range scope.Operations {
			if _, err := normalizeOperation(operation); err != nil {
				return err
			}
		}
	}
	for _, status := range c.ExpectedGRPCStatuses {
		if status != 3 && status != 11 {
			return fmt.Errorf("explore.expected_grpc_statuses must contain INVALID_ARGUMENT (3) or OUT_OF_RANGE (11)")
		}
	}
	for _, status := range c.ExpectedStatuses {
		if status < 400 || status > 499 || status == 401 || status == 403 || status == 429 {
			return fmt.Errorf("explore.expected_statuses must be validation 4xx responses, excluding 401, 403 and 429")
		}
	}
	for _, b := range c.Bindings {
		if b.MessageIndex != nil && *b.MessageIndex < 0 {
			return fmt.Errorf("binding message_index must not be negative")
		}
		if b.Name == "" || (b.In != "body" && b.In != "header" && b.In != "query" && b.In != "path" && b.In != "cookie" && b.In != "metadata") {
			return fmt.Errorf("invalid exploration binding")
		}
		if b.In == "body" {
			if _, err := pointerTokens(b.Path); err != nil {
				return err
			}
		} else if b.Path == "" {
			return fmt.Errorf("binding path is required")
		}
	}
	for _, e := range c.Extract {
		if e.MessageIndex != nil && *e.MessageIndex < 0 {
			return fmt.Errorf("extraction message_index must not be negative")
		}
		if e.Name == "" || e.FlowID == "" || (e.In != "body" && e.In != "header" && e.In != "metadata" && e.In != "trailers") {
			return fmt.Errorf("invalid exploration extraction")
		}
		if e.In == "body" {
			if _, err := pointerTokens(e.Path); err != nil {
				return err
			}
		} else if e.Path == "" {
			return fmt.Errorf("extraction path is required")
		}
	}
	return nil
}
