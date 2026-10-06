// Package project loads the single project configuration used by the runtime.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"gopkg.in/yaml.v3"
)

type API struct {
	URL            string      `yaml:"url"`
	ID             string      `yaml:"id"`
	Kind           string      `yaml:"kind"`
	Schema         string      `yaml:"schema"`
	Protocol       string      `yaml:"protocol"`
	Services       []string    `yaml:"services"`
	CaptureListen  string      `yaml:"capture_listen"`
	GRPCSchema     *GRPCSchema `yaml:"-"`
	CaptureTLS     TLSConfig   `yaml:"capture_tls"`
	UpstreamTLS    TLSConfig   `yaml:"upstream_tls"`
	SchemaRequired *bool       `yaml:"schema_required"`
}

type TLSConfig struct {
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	CAFile     string `yaml:"ca_file"`
	ServerName string `yaml:"server_name"`
}

type GRPCSchema struct {
	Source      string            `yaml:"source"`
	Timeout     string            `yaml:"timeout"`
	Headers     map[string]string `yaml:"headers"`
	Files       []string          `yaml:"files"`
	ImportPaths []string          `yaml:"import_paths"`
	Path        string            `yaml:"path"`
}

func (a *API) UnmarshalYAML(node *yaml.Node) error {
	allowed := map[string]bool{"url": true, "id": true, "kind": true, "schema": true, "protocol": true, "services": true, "capture_listen": true, "schema_required": true, "capture_tls": true, "upstream_tls": true}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !allowed[key] {
			return fmt.Errorf("field %s not found in project API", key)
		}
		if key == "capture_tls" || key == "upstream_tls" {
			allowedTLS := map[string]bool{"cert_file": key == "capture_tls", "key_file": key == "capture_tls", "ca_file": key == "upstream_tls", "server_name": key == "upstream_tls"}
			fields := node.Content[i+1]
			if fields.Kind != yaml.MappingNode {
				return fmt.Errorf("%s must be an object", key)
			}
			for j := 0; j+1 < len(fields.Content); j += 2 {
				if !allowedTLS[fields.Content[j].Value] {
					return fmt.Errorf("field %s not found in %s", fields.Content[j].Value, key)
				}
			}
		}
	}
	type rawAPI struct {
		URL            string    `yaml:"url"`
		ID             string    `yaml:"id"`
		Kind           string    `yaml:"kind"`
		Schema         yaml.Node `yaml:"schema"`
		Protocol       string    `yaml:"protocol"`
		Services       []string  `yaml:"services"`
		CaptureListen  string    `yaml:"capture_listen"`
		CaptureTLS     TLSConfig `yaml:"capture_tls"`
		UpstreamTLS    TLSConfig `yaml:"upstream_tls"`
		SchemaRequired *bool     `yaml:"schema_required"`
	}
	var raw rawAPI
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*a = API{URL: raw.URL, ID: raw.ID, Kind: raw.Kind, Protocol: raw.Protocol, Services: raw.Services, CaptureListen: raw.CaptureListen, CaptureTLS: raw.CaptureTLS, UpstreamTLS: raw.UpstreamTLS, SchemaRequired: raw.SchemaRequired}
	if raw.Schema.Kind == 0 {
		return nil
	}
	switch raw.Schema.Kind {
	case yaml.ScalarNode:
		if raw.Schema.Tag == "!!null" {
			return errors.New("schema: explicit null is not supported")
		}
		if raw.Protocol == "grpc" {
			return errors.New("gRPC schema must be a source object, for example {source: reflection}")
		}
		return raw.Schema.Decode(&a.Schema)
	case yaml.MappingNode:
		allowedSchema := map[string]bool{"source": true, "timeout": true, "headers": true, "files": true, "import_paths": true, "path": true}
		for i := 0; i+1 < len(raw.Schema.Content); i += 2 {
			if !allowedSchema[raw.Schema.Content[i].Value] {
				return fmt.Errorf("field %s not found in gRPC schema", raw.Schema.Content[i].Value)
			}
		}
		var schema GRPCSchema
		if err := raw.Schema.Decode(&schema); err != nil {
			return err
		}
		a.GRPCSchema = &schema
		return nil
	default:
		return errors.New("schema must be a path or gRPC source object")
	}
}

func (a API) RequiresSchema() bool { return a.SchemaRequired == nil || *a.SchemaRequired }
func (a API) Schemas(id, role string) []artifact.SchemaRef {
	if a.Schema == "" {
		return nil
	}
	return []artifact.SchemaRef{{ID: id, Role: role, Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: a.Schema}}}
}

type Config struct {
	Version      int            `yaml:"version"`
	HTTPListen   string         `yaml:"http_listen"`
	ProxyListen  string         `yaml:"proxy_listen"`
	OutputDir    string         `yaml:"output_dir"`
	CADir        string         `yaml:"ca_dir"`
	SUT          API            `yaml:"sut"`
	Dependencies map[string]API `yaml:"dependencies"`
	Path         string         `yaml:"-"`
	Publication  *Publication   `yaml:"publication,omitempty"`
}

// Publication opts into maintaining a repository bundle when a run finishes.
type Publication struct {
	Config string `yaml:"config"`
	Output string `yaml:"output"`
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

// Decode validates a project document. path defines the base directory for local
// inputs; callers may supply a synthetic path when registering through the API.
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
	if cfg.Publication != nil {
		if cfg.Publication.Config == "" {
			return nil, errors.New("publication.config is required")
		}
		if cfg.Publication.Output == "" {
			cfg.Publication.Output = "docs/knowledge"
		}
		for _, field := range []*string{&cfg.Publication.Config, &cfg.Publication.Output} {
			*field, err = resolve(*field)
			if err != nil {
				return nil, err
			}
		}
	}
	for _, field := range []*string{&cfg.HTTPListen, &cfg.ProxyListen} {
		*field, err = expand(*field)
		if err != nil {
			return nil, err
		}
	}
	resolveAPI := func(id string, a API) (API, error) {
		a.URL, err = expand(a.URL)
		if err != nil {
			return a, err
		}
		if a.Protocol == "" {
			a.Protocol = "http"
		}
		if a.Protocol != "http" && a.Protocol != "grpc" {
			return a, fmt.Errorf("%s: protocol must be http or grpc", id)
		}
		a.CaptureListen, err = expand(a.CaptureListen)
		if err != nil {
			return a, err
		}
		a.Schema, err = resolve(a.Schema)
		if err != nil {
			return a, err
		}
		if a.Protocol == "grpc" {
			parsed, e := url.Parse(a.URL)
			if e != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
				return a, fmt.Errorf("%s: gRPC url must be an http or https origin without path or credentials", id)
			}
			if (a.CaptureTLS.CertFile == "") != (a.CaptureTLS.KeyFile == "") {
				return a, fmt.Errorf("%s: capture_tls requires both cert_file and key_file", id)
			}
			if a.CaptureTLS.CAFile != "" || a.CaptureTLS.ServerName != "" || a.UpstreamTLS.CertFile != "" || a.UpstreamTLS.KeyFile != "" {
				return a, fmt.Errorf("%s: unsupported TLS field for gRPC boundary", id)
			}
			for _, field := range []*string{&a.CaptureTLS.CertFile, &a.CaptureTLS.KeyFile, &a.UpstreamTLS.CAFile} {
				*field, err = resolve(*field)
				if err != nil {
					return a, err
				}
			}
			a.UpstreamTLS.ServerName, err = expand(a.UpstreamTLS.ServerName)
			if err != nil {
				return a, err
			}
			if a.Schema != "" {
				return a, fmt.Errorf("%s: gRPC schema must be a source object, not a path string", id)
			}
			if !a.RequiresSchema() {
				return a, fmt.Errorf("%s: gRPC requires a schema; schema_required: false is not supported", id)
			}
			if a.GRPCSchema != nil {
				s := a.GRPCSchema
				switch s.Source {
				case "reflection":
					if len(s.Files) > 0 || len(s.ImportPaths) > 0 || s.Path != "" {
						return a, fmt.Errorf("%s: reflection schema has local-source fields", id)
					}
					if s.Timeout != "" {
						duration, e := time.ParseDuration(s.Timeout)
						if e != nil || duration <= 0 || duration > time.Minute {
							return a, fmt.Errorf("%s: reflection timeout must be greater than zero and at most one minute", id)
						}
					}
				case "proto":
					if len(s.Files) == 0 || len(s.ImportPaths) == 0 || s.Path != "" || s.Timeout != "" || len(s.Headers) > 0 {
						return a, fmt.Errorf("%s: proto schema requires files and import_paths only", id)
					}
				case "descriptor_set":
					if s.Path == "" || len(s.Files) > 0 || len(s.ImportPaths) > 0 || s.Timeout != "" || len(s.Headers) > 0 {
						return a, fmt.Errorf("%s: descriptor_set schema requires path only", id)
					}
				default:
					return a, fmt.Errorf("%s: unsupported gRPC schema source %q", id, s.Source)
				}
				if s.Path != "" {
					s.Path, err = resolve(s.Path)
					if err != nil {
						return a, err
					}
				}
				for i := range s.ImportPaths {
					s.ImportPaths[i], err = resolve(s.ImportPaths[i])
					if err != nil {
						return a, err
					}
				}
				for key, value := range s.Headers {
					s.Headers[key], err = expand(value)
					if err != nil {
						return a, err
					}
				}
			}
			if a.GRPCSchema != nil && a.GRPCSchema.Source != "reflection" && len(a.Services) == 0 {
				return a, fmt.Errorf("%s: local gRPC schemas require services", id)
			}
		} else if a.GRPCSchema != nil || len(a.Services) > 0 || a.CaptureListen != "" || a.CaptureTLS != (TLSConfig{}) || a.UpstreamTLS != (TLSConfig{}) {
			return a, fmt.Errorf("%s: gRPC fields require protocol: grpc", id)
		} else if a.Schema == "" && a.RequiresSchema() {
			return a, fmt.Errorf("%s: schema is required; set schema_required: false explicitly for this API to omit it", id)
		}
		if a.Kind != "" && a.Kind != "mock" && a.Kind != "service" && a.Kind != "unknown" {
			return a, fmt.Errorf("%s: kind must be mock, service, or unknown", id)
		}
		return a, nil
	}
	cfg.SUT, err = resolveAPI("sut", cfg.SUT)
	if err != nil {
		return nil, err
	}
	for _, id := range cfg.DependencyIDs() {
		a, e := resolveAPI(id, cfg.Dependencies[id])
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
