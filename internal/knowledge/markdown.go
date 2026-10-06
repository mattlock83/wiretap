package knowledge

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func markdownRelative(from, to string) string {
	rel, err := filepath.Rel(filepath.Dir(filepath.FromSlash(from)), filepath.FromSlash(to))
	if err != nil {
		return to
	}
	return filepath.ToSlash(rel)
}

// Keep exact JSON numeric values numeric in YAML; yaml.Marshal otherwise treats
// json.Number's underlying string as a quoted string.
func marshalFrontmatter(v any) ([]byte, error) {
	var nodes func(any) any
	nodes = func(v any) any {
		switch v := v.(type) {
		case json.Number:
			tag := "!!int"
			if strings.ContainsAny(string(v), ".eE") {
				tag = "!!float"
			}
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(v)}
		case Object:
			m := Object{}
			for k, x := range v {
				m[k] = nodes(x)
			}
			return m
		case []any:
			a := make([]any, len(v))
			for i, x := range v {
				a[i] = nodes(x)
			}
			return a
		default:
			return v
		}
	}
	return yaml.Marshal(nodes(v))
}

// YAML exports the verified JSON-compatible records without turning exact
// numeric values into strings or rounding large captured numbers.
func YAML(v any) ([]byte, error) {
	b, err := Canonical(v)
	if err != nil {
		return nil, err
	}
	value, err := Decode(b)
	if err != nil {
		return nil, err
	}
	return marshalFrontmatter(value)
}
