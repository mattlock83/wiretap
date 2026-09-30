// Package explore generates and runs negative provider tests from verified
// capture evidence. Project hooks supply fixtures; bundles remain build artifacts.
package explore

import (
	"fmt"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/hooks"
)

const Version = "wiretap-negative-1"

type Config struct {
	Hooks            hooks.Lifecycle      `yaml:"hooks"`
	ExpectedStatuses []int                `yaml:"expected_statuses"`
	MaxCases         int                  `yaml:"max_cases"`
	RequestTimeout   string               `yaml:"request_timeout"`
	Bindings         []Binding            `yaml:"bindings"`
	Extract          []Extraction         `yaml:"extract"`
	Redactions       []artifact.Redaction `yaml:"redactions"`
	Dir              string               `yaml:"-"`
}

// Bindings reference runtime values returned by hooks or extracted from earlier
// responses. Body paths are JSON pointers; path names are OpenAPI parameters.
type Binding struct {
	Name   string `yaml:"name"`
	FlowID string `yaml:"flow_id"`
	In     string `yaml:"in"` // body, query, header, path, cookie
	Path   string `yaml:"path"`
}
type Extraction struct {
	Name   string `yaml:"name"`
	FlowID string `yaml:"flow_id"`
	In     string `yaml:"in"` // body or header
	Path   string `yaml:"path"`
}

func (c *Config) Defaults() {
	if len(c.ExpectedStatuses) == 0 {
		c.ExpectedStatuses = []int{400, 422}
	}
	if c.MaxCases == 0 {
		c.MaxCases = 100
	}
	if c.RequestTimeout == "" {
		c.RequestTimeout = "15s"
	}
}
func (c Config) Validate() error {
	if c.MaxCases < 1 || c.MaxCases > 10000 {
		return fmt.Errorf("explore.max_cases must be between 1 and 10000")
	}
	if d, err := time.ParseDuration(c.RequestTimeout); err != nil || d <= 0 {
		return fmt.Errorf("explore.request_timeout must be a positive duration")
	}
	for _, status := range c.ExpectedStatuses {
		if status < 400 || status > 499 || status == 401 || status == 403 || status == 429 {
			return fmt.Errorf("explore.expected_statuses must be validation 4xx responses, excluding 401, 403 and 429")
		}
	}
	for _, b := range c.Bindings {
		if b.Name == "" || (b.In != "body" && b.In != "header" && b.In != "query" && b.In != "path" && b.In != "cookie") {
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
		if e.Name == "" || e.FlowID == "" || (e.In != "body" && e.In != "header") {
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
	return c.Hooks.Validate()
}
