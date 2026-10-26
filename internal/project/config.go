// Package project loads the single project configuration used by the runtime.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/runoutput"

	"gopkg.in/yaml.v3"
)

type API struct {
	URL            string `yaml:"url"`
	ID             string `yaml:"id"`
	Name           string `yaml:"name"`
	Kind           string `yaml:"kind"`
	Schema         string `yaml:"schema"`
	SchemaRequired *bool  `yaml:"schema_required"`
}

func (a *API) UnmarshalYAML(node *yaml.Node) error {
	allowed := map[string]bool{"url": true, "id": true, "name": true, "kind": true, "schema": true, "schema_required": true}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !allowed[key] {
			return fmt.Errorf("field %s not found in project API", key)
		}
		if key == "schema" && node.Content[i+1].Tag == "!!null" {
			return errors.New("schema: explicit null is not supported")
		}
	}
	type plainAPI API
	return node.Decode((*plainAPI)(a))
}

func (a API) RequiresSchema() bool { return a.SchemaRequired == nil || *a.SchemaRequired }
func (a API) Schemas(id, role string) []artifact.SchemaRef {
	if a.Schema == "" {
		return nil
	}
	return []artifact.SchemaRef{{ID: id, Role: role, Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: a.Schema}}}
}

type Config struct {
	ProviderMappings map[string]bundle.ProviderRef `yaml:"provider_mappings,omitempty"`
	TestSource       map[string]any                `yaml:"test_source,omitempty"`
	SUTSource        map[string]any                `yaml:"sut_source,omitempty"`
	Version          int                           `yaml:"version"`
	Application      *bundle.Application           `yaml:"application,omitempty"`
	HTTPListen       string                        `yaml:"http_listen"`
	ProxyListen      string                        `yaml:"proxy_listen"`
	OutputDir        string                        `yaml:"output_dir"`
	CADir            string                        `yaml:"ca_dir"`
	SUT              API                           `yaml:"sut"`
	Dependencies     map[string]API                `yaml:"dependencies"`
	Path             string                        `yaml:"-"`
	Outputs          *runoutput.Config             `yaml:"outputs,omitempty"`
}

// Load discovers .wiretap.yaml in the working directory or its ancestors.
// An explicitly requested path must exist; no discovered file is also valid.
func Load(path string) (*Config, error) {
	if path == "" {
		dir, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		for {
			candidate := filepath.Join(dir, ".wiretap.yaml")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return nil, nil
			}
			dir = parent
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(raw, path)
}

// Decode validates a project document. path defines the base directory for local inputs.
func Decode(raw []byte, path string) (*Config, error) {
	var err error
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%s: expected one YAML document", path)
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("%s: version must be 1", path)
	}
	if cfg.Outputs != nil {
		if err := cfg.Outputs.Validate(); err != nil {
			return nil, err
		}
	}
	if cfg.Application != nil && strings.TrimSpace(cfg.Application.Name) == "" && strings.TrimSpace(cfg.Application.ID) == "" {
		return nil, fmt.Errorf("%s: application requires a name or ID", path)
	}
	for id, provider := range cfg.ProviderMappings {
		if _, ok := cfg.Dependencies[id]; !ok {
			return nil, fmt.Errorf("provider mapping %q is not a configured dependency", id)
		}
		if err := provider.Validate(); err != nil {
			return nil, err
		}
	}
	cfg.Path = path
	base := filepath.Dir(path)
	expand := func(value string) (string, error) {
		var missing string
		result := os.Expand(value, func(key string) string {
			v, ok := os.LookupEnv(key)
			if !ok {
				missing = key
			}
			return v
		})
		if missing != "" {
			return "", fmt.Errorf("%s: environment variable %s is not set", path, missing)
		}
		return result, nil
	}
	for _, source := range []map[string]any{cfg.TestSource, cfg.SUTSource} {
		for key, value := range source {
			if text, ok := value.(string); ok {
				expanded, err := expand(text)
				if err != nil {
					return nil, err
				}
				source[key] = expanded
			}
		}
	}
	resolve := func(value string) (string, error) {
		v, err := expand(value)
		if err != nil || v == "" {
			return v, err
		}
		if !filepath.IsAbs(v) {
			v = filepath.Join(base, v)
		}
		return filepath.Clean(v), nil
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "artifacts"
	}
	for _, field := range []*string{&cfg.OutputDir, &cfg.CADir} {
		*field, err = resolve(*field)
		if err != nil {
			return nil, err
		}
	}
	for _, field := range []*string{&cfg.HTTPListen, &cfg.ProxyListen} {
		*field, err = expand(*field)
		if err != nil {
			return nil, err
		}
	}
	resolveAPI := func(id string, a API, dependency bool) (API, error) {
		a.URL, err = expand(a.URL)
		if err != nil {
			return a, err
		}
		// The provider owns the HTTP contract. Consumer captures can record
		// mocks without copying it; the SUT's own specification stays required.
		if dependency && a.SchemaRequired == nil {
			a.SchemaRequired = new(bool)
		}
		a.Schema, err = resolve(a.Schema)
		if err != nil {
			return a, err
		}
		if a.Schema == "" && a.RequiresSchema() {
			return a, fmt.Errorf("%s: schema is required; set schema_required: false explicitly for this API to omit it", id)
		}

		if a.Kind != "" && a.Kind != "mock" && a.Kind != "service" && a.Kind != "unknown" {
			return a, fmt.Errorf("%s: kind must be mock, service, or unknown", id)
		}
		return a, nil
	}
	cfg.SUT, err = resolveAPI("sut", cfg.SUT, false)
	if err != nil {
		return nil, err
	}
	for _, id := range cfg.DependencyIDs() {
		a, e := resolveAPI(id, cfg.Dependencies[id], true)
		if e != nil {
			return nil, e
		}
		cfg.Dependencies[id] = a
	}
	return &cfg, nil
}
func (c *Config) DependencyIDs() []string {
	var ids []string
	for id := range c.Dependencies {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ResolveURL is kept deliberately small: endpoint configuration uses ordinary URLs.
func (a API) TargetURL() string { return strings.TrimRight(a.URL, "/") }
