package redact

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
)

const defaultMask = "****"

// Apply applies redaction rules to the artifact in-place.
func Apply(art *artifact.Artifact, rules []artifact.Redaction) error {
	if art == nil {
		return errors.New("artifact is nil")
	}
	if len(rules) == 0 {
		return nil
	}
	for i := range art.Traffic {
		flow := &art.Traffic[i]
		for _, rule := range rules {
			applyRule(flow, rule)
		}
	}
	return nil
}

func applyRule(flow *artifact.Flow, rule artifact.Redaction) {
	field := strings.TrimSpace(rule.Field)
	if field == "" {
		return
	}

	parts := strings.Split(field, ".")
	if len(parts) < 3 {
		return
	}

	section := strings.ToLower(parts[0])
	target := strings.ToLower(parts[1])
	path := strings.Join(parts[2:], ".")

	var msg *artifact.HttpMessage
	switch section {
	case "request":
		if flow.Request.HTTP == nil {
			return
		}
		msg = flow.Request.HTTP
	case "response":
		if flow.Response.HTTP == nil {
			return
		}
		msg = flow.Response.HTTP
	default:
		return
	}

	switch target {
	case "headers":
		applyHeaderRule(msg, path, rule)
	case "body":
		applyBodyRule(msg, path, rule)
	}
}

func applyHeaderRule(msg *artifact.HttpMessage, headerKey string, rule artifact.Redaction) {
	if msg == nil || msg.Headers == nil {
		return
	}
	for key := range msg.Headers {
		if !strings.EqualFold(key, headerKey) {
			continue
		}
		switch strings.ToLower(rule.Rule) {
		case "remove":
			delete(msg.Headers, key)
		default:
			replacement := rule.Replacement
			if replacement == "" {
				replacement = defaultMask
			}
			values := msg.Headers[key]
			for i := range values {
				values[i] = replacement
			}
			msg.Headers[key] = values
		}
	}
}

func applyBodyRule(msg *artifact.HttpMessage, jsonPath string, rule artifact.Redaction) {
	if msg == nil || msg.Body == nil {
		return
	}
	if msg.Body.Text == "" || msg.Body.Bytes != "" {
		return
	}

	trimmed := strings.TrimSpace(msg.Body.Text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return
	}

	var payload any
	if err := json.Unmarshal([]byte(msg.Body.Text), &payload); err != nil {
		return
	}

	tokens := parsePath(jsonPath)
	if len(tokens) == 0 {
		return
	}
	changed := applyJSONRule(&payload, tokens, rule)
	if !changed {
		return
	}

	updated, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg.Body.Text = string(updated)
}

type pathToken struct {
	key   string
	index *int
}

func parsePath(path string) []pathToken {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	var tokens []pathToken
	for i := 0; i < len(path); {
		switch path[i] {
		case '.':
			i++
			continue
		case '[':
			i++
			start := i
			for i < len(path) && path[i] != ']' {
				i++
			}
			if i >= len(path) {
				return tokens
			}
			idx, err := strconv.Atoi(path[start:i])
			if err == nil {
				idxCopy := idx
				tokens = append(tokens, pathToken{index: &idxCopy})
			}
			i++
		default:
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start < i {
				tokens = append(tokens, pathToken{key: path[start:i]})
			}
		}
	}

	return tokens
}

func applyJSONRule(payload *any, tokens []pathToken, rule artifact.Redaction) bool {
	if payload == nil || len(tokens) == 0 {
		return false
	}

	current := *payload
	for i := 0; i < len(tokens)-1; i++ {
		tok := tokens[i]
		switch node := current.(type) {
		case map[string]any:
			if tok.key == "" {
				return false
			}
			next, ok := node[tok.key]
			if !ok {
				return false
			}
			current = next
		case []any:
			if tok.index == nil || *tok.index < 0 || *tok.index >= len(node) {
				return false
			}
			current = node[*tok.index]
		default:
			return false
		}
	}

	last := tokens[len(tokens)-1]
	replacement := rule.Replacement
	if replacement == "" {
		replacement = defaultMask
	}

	switch node := current.(type) {
	case map[string]any:
		if last.key == "" {
			return false
		}
		if _, ok := node[last.key]; !ok {
			return false
		}
		switch strings.ToLower(rule.Rule) {
		case "remove":
			delete(node, last.key)
		default:
			node[last.key] = replacement
		}
		return true
	case []any:
		if last.index == nil || *last.index < 0 || *last.index >= len(node) {
			return false
		}
		switch strings.ToLower(rule.Rule) {
		case "remove":
			node[*last.index] = nil
		default:
			node[*last.index] = replacement
		}
		return true
	default:
		return false
	}
}
