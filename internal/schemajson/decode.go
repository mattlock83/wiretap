// Package schemajson decodes YAML/JSON contract documents without rounding numbers.
package schemajson

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func Decode(data []byte) (any, error) {
	var syntax yaml.Node
	if err := yaml.Unmarshal(data, &syntax); err != nil {
		return nil, err
	}
	return DecodeNode(&syntax)
}

// DecodeNode retains the parser's normal string/boolean/null semantics, while
// restoring numeric scalars from their original spelling before JSON encoding.
func DecodeNode(syntax *yaml.Node) (any, error) {
	var value any
	if err := syntax.Decode(&value); err != nil {
		return nil, err
	}
	var restore func(*yaml.Node, any) (any, error)
	restore = func(n *yaml.Node, value any) (any, error) {
		switch n.Kind {
		case yaml.DocumentNode:
			if len(n.Content) > 0 {
				return restore(n.Content[0], value)
			}
		case yaml.AliasNode:
			return restore(n.Alias, value)
		case yaml.MappingNode:
			object, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("JSON contract object keys must be strings")
			}
			original := make(map[string]any, len(object))
			for name, v := range object {
				original[name] = v
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Tag == "!!merge" {
					// Resolve merged entries with their original scalar nodes too.
					var merge func(*yaml.Node) error
					merge = func(source *yaml.Node) error {
						if source.Kind == yaml.AliasNode {
							return merge(source.Alias)
						}
						if source.Kind == yaml.SequenceNode {
							for j := len(source.Content) - 1; j >= 0; j-- {
								if err := merge(source.Content[j]); err != nil {
									return err
								}
							}
							return nil
						}
						merged, err := DecodeNode(source)
						if err != nil {
							return err
						}
						for name, v := range merged.(map[string]any) {
							object[name] = v
						}
						return nil
					}
					if err := merge(n.Content[i+1]); err != nil {
						return nil, err
					}
				}
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Tag == "!!merge" {
					continue
				}
				child, err := restore(n.Content[i+1], original[key.Value])
				if err != nil {
					return nil, err
				}
				object[key.Value] = child
			}
		case yaml.SequenceNode:
			items := value.([]any)
			for i, child := range n.Content {
				v, err := restore(child, items[i])
				if err != nil {
					return nil, err
				}
				items[i] = v
			}
		case yaml.ScalarNode:
			if n.Tag == "!!int" || n.Tag == "!!float" {
				return exactNumber(n.Value, n.Tag)
			}
		}
		return value, nil
	}
	return restore(syntax, value)
}

func exactNumber(raw, tag string) (json.Number, error) {
	s := strings.ReplaceAll(raw, "_", "")
	if tag == "!!int" {
		value, ok := new(big.Int).SetString(s, 0)
		if !ok {
			return "", fmt.Errorf("unsupported YAML integer %q", raw)
		}
		return json.Number(value.String()), nil
	}
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	parts := strings.Split(strings.ToLower(s), "e")
	if len(parts) > 2 {
		return "", fmt.Errorf("unsupported YAML number %q", raw)
	}
	exponent := 0
	if len(parts) == 2 {
		parsed, err := strconv.ParseInt(parts[1], 10, 32)
		if err != nil || parsed < -10000 || parsed > 10000 {
			return "", fmt.Errorf("unsupported numeric exponent in %q", raw)
		}
		exponent = int(parsed)
	}
	mantissa := parts[0]
	if point := strings.IndexByte(mantissa, '.'); point >= 0 {
		exponent -= len(mantissa) - point - 1
		mantissa = mantissa[:point] + mantissa[point+1:]
	}
	if mantissa == "" || strings.IndexFunc(mantissa, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", fmt.Errorf("non-finite or unsupported YAML number %q", raw)
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return "0", nil
	}
	trimmed := strings.TrimRight(mantissa, "0")
	exponent += len(mantissa) - len(trimmed)
	mantissa = trimmed
	if exponent >= 0 {
		s = mantissa + strings.Repeat("0", exponent)
	} else if point := len(mantissa) + exponent; point > 0 {
		s = mantissa[:point] + "." + mantissa[point:]
	} else {
		s = "0." + strings.Repeat("0", -point) + mantissa
	}
	if negative {
		s = "-" + s
	}
	return json.Number(s), nil
}
