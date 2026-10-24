package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
)

const defaultMask = "****"

// Validate rejects unsupported policies instead of silently retaining secrets.
func Validate(rules []artifact.Redaction) error {
	for _, rule := range rules {
		parts := strings.SplitN(strings.TrimSpace(rule.Field), ".", 3)
		if len(parts) < 2 || parts[0] != "request" && parts[0] != "response" {
			return fmt.Errorf("invalid redaction field %q", rule.Field)
		}
		target := parts[1]
		if target != "body" && target != "headers" && target != "metadata" && target != "trailers" && target != "url" && target != "query" && target != "messages" && !strings.HasPrefix(target, "messages[") && target != "status_details" && !strings.HasPrefix(target, "status_details[") {
			return fmt.Errorf("unsupported redaction field %q", rule.Field)
		}
		if target == "url" && len(parts) == 3 && parts[2] != "userinfo" && parts[2] != "fragment" && parts[2] != "path" && !strings.HasPrefix(parts[2], "query.") {
			return fmt.Errorf("unsupported URL redaction field %q", rule.Field)
		}
		if target == "url" && len(parts) == 3 && parts[2] == "query." {
			return fmt.Errorf("URL query redaction needs a parameter name")
		}
		for _, prefix := range []string{"messages", "status_details"} {
			if strings.HasPrefix(target, prefix+"[") {
				selector := parsePath(strings.TrimPrefix(target, prefix))
				if len(selector) != 1 || selector[0].index == nil && !selector[0].all {
					return fmt.Errorf("invalid redaction selector %q", target)
				}
			}
		}
		if target != "url" && target != "body" && (len(parts) < 3 || parts[2] == "") {
			return fmt.Errorf("redaction field %q needs a selector", rule.Field)
		}
		if rule.Rule != "" && rule.Rule != "mask" && rule.Rule != "remove" {
			return fmt.Errorf("unsupported redaction rule %q", rule.Rule)
		}
	}
	return nil
}

// Apply applies redaction rules to the artifact in-place.
func Apply(art *artifact.Artifact, rules []artifact.Redaction) error {
	if art == nil {
		return errors.New("artifact is nil")
	}
	if len(rules) == 0 {
		return nil
	}
	if err := Validate(rules); err != nil {
		return err
	}
	for i := range art.Traffic {
		flow := &art.Traffic[i]
		for _, rule := range rules {
			applyRule(flow, rule)
		}
		// Validation ran against the original traffic. Its errors may embed an
		// entire body, parameter, or header, so never retain those diagnostic copies
		// after applying a rule to that message.
		for _, phase := range []string{"request", "response"} {
			for _, rule := range rules {
				if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(rule.Field)), phase+".") {
					continue
				}
				if art.SchemaValidation != nil {
					for j := range art.SchemaValidation.Errors {
						e := &art.SchemaValidation.Errors[j]
						if e.FlowID == flow.ID && e.Phase == phase {
							e.Message = "Schema validation failed; details omitted by redaction policy"
						}
					}
					for j := range art.SchemaValidation.Unavailable {
						e := &art.SchemaValidation.Unavailable[j]
						if e.FlowID == flow.ID && e.Phase == phase {
							e.Message = "Validation evidence unavailable; details omitted by redaction policy"
						}
					}
				}
				for j := range art.Assertions {
					e := &art.Assertions[j]
					if e.FlowID == flow.ID && e.Phase == phase {
						e.Message = "Assertion details omitted by redaction policy"
						e.Expected, e.Actual = nil, nil
					}
				}
				if flow.TransportError != "" {
					flow.TransportError = "Transport error; details omitted by redaction policy"
				}
				break
			}
		}
	}
	if art.SchemaValidation != nil && art.Meta != nil {
		art.Meta["schema_validation"] = map[string]any{"status": art.SchemaValidation.Status, "errors": art.SchemaValidation.Errors, "unavailable": art.SchemaValidation.Unavailable}
	}
	if art.Env != nil {
		for _, rule := range rules {
			field := strings.ToLower(strings.TrimSpace(rule.Field))
			if strings.HasPrefix(field, "request.query.") || strings.HasPrefix(field, "request.url") {
				msg := &artifact.HttpMessage{URL: art.Env.SUTBaseURL}
				applyRule(&artifact.Flow{Request: artifact.Message{HTTP: msg}}, rule)
				art.Env.SUTBaseURL = msg.URL
			}
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
	if len(parts) < 2 {
		return
	}

	section := strings.ToLower(parts[0])
	target := strings.ToLower(parts[1])
	path := strings.Join(parts[2:], ".")

	var msg *artifact.HttpMessage
	var grpc *artifact.GrpcMessage
	switch section {
	case "request":
		if flow.Request.HTTP == nil && flow.Request.GRPC == nil {
			return
		}
		msg = flow.Request.HTTP
		grpc = flow.Request.GRPC
	case "response":
		if flow.Response.HTTP == nil && flow.Response.GRPC == nil {
			return
		}
		msg = flow.Response.HTTP
		grpc = flow.Response.GRPC
	default:
		return
	}
	if grpc != nil {
		var messageIndex *int
		if target == "status_details" || strings.HasPrefix(target, "status_details[") {
			redactStatusDetails(grpc, target, path, rule)
			return
		}
		if strings.HasPrefix(target, "messages[") {
			// Indices identify positions on the original wire stream. Retention
			// omissions can leave gaps in the stored message slice.
			selectors := parsePath(strings.TrimPrefix(target, "messages"))
			if len(selectors) != 1 || !selectors[0].all && (selectors[0].index == nil || *selectors[0].index < 0) {
				return
			}
			messageIndex = selectors[0].index
			if path == "body" {
				path = ""
			} else {
				path = strings.TrimPrefix(path, "body.")
			}
			target = "body"
		}
		switch target {
		case "headers", "metadata":
			applyHeaderRule(grpc.Metadata, path, rule)
			redactGRPCStatus(grpc, path, rule)
		case "trailers":
			applyHeaderRule(grpc.Trailers, path, rule)
			redactGRPCStatus(grpc, path, rule)
		case "body", "messages":
			if target == "messages" {
				if path == "body" {
					path = ""
				} else {
					path = strings.TrimPrefix(path, "body.")
				}
			}
			path = strings.TrimPrefix(path, "[*].body.")
			for i := range grpc.Messages {
				payload := &grpc.Messages[i]
				if messageIndex != nil && payload.Index != *messageIndex {
					continue
				}
				payload.Wire = nil
				payload.Redacted = true
				if payload.Body == nil {
					payload.DecodeError = "payload omitted: configured redaction could not be applied"
					continue
				}
				var projection any
				decoder := json.NewDecoder(strings.NewReader(payload.Body.Text))
				decoder.UseNumber()
				if payload.Body.Bytes != "" || decoder.Decode(&projection) != nil {
					payload.Body = nil
					payload.DecodeError = "payload omitted: configured redaction could not be applied"
					continue
				}
				switch projection.(type) {
				case map[string]any, []any:
				default:
					payload.Body = nil
					payload.DecodeError = "payload omitted: field redaction cannot select a scalar ProtoJSON value"
					continue
				}
				applyBodyRule(&artifact.HttpMessage{Body: payload.Body}, path, rule)
				if payload.Body.Omitted {
					payload.Body = nil
					payload.DecodeError = "payload omitted: configured redaction could not be applied"
				}
			}
		}
		return
	}

	switch target {
	case "headers":
		applyHeaderRule(msg.Headers, path, rule)
	case "trailers":
		applyHeaderRule(msg.Trailers, path, rule)
	case "body":
		applyBodyRule(msg, path, rule)
	case "query":
		applyURLRule(msg, "query."+path, rule)
	case "url":
		applyURLRule(msg, path, rule)
	}
}

func applyURLRule(msg *artifact.HttpMessage, path string, rule artifact.Redaction) {
	if msg == nil || msg.URL == "" {
		return
	}
	replacement := rule.Replacement
	if replacement == "" {
		replacement = defaultMask
	}
	if path == "" {
		msg.URL = ""
		return
	}
	u, err := url.Parse(msg.URL)
	if err != nil || u.Opaque != "" {
		msg.URL = ""
		return
	}
	switch {
	case strings.HasPrefix(path, "query."):
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			u.RawQuery = ""
			break
		}
		key := strings.TrimPrefix(path, "query.")
		for name := range values {
			if name != key && key != "*" {
				continue
			}
			if strings.EqualFold(rule.Rule, "remove") {
				values.Del(name)
			} else {
				for i := range values[name] {
					values[name][i] = replacement
				}
			}
		}
		u.RawQuery = values.Encode()
	case path == "userinfo":
		u.User = nil
	case path == "fragment":
		u.Fragment, u.RawFragment = "", ""
	case path == "path":
		u.Path, u.RawPath = "/"+replacement, ""
	default:
		return
	}
	msg.URL = u.String()
}

func redactGRPCStatus(msg *artifact.GrpcMessage, key string, rule artifact.Redaction) {
	if !strings.EqualFold(key, "grpc-message") && !strings.EqualFold(key, "grpc-status-details-bin") {
		return
	}
	if strings.EqualFold(key, "grpc-status-details-bin") {
		redactWholeStatusDetails(msg)
		return
	}
	removeStatusEnvelope(msg)
	// Status may have arrived in headers or trailers. Redact every persisted
	// copy, including the convenience status-message field used by viewers.
	applyHeaderRule(msg.Metadata, key, rule)
	applyHeaderRule(msg.Trailers, key, rule)
	if msg.StatusDetails != nil {
		if strings.EqualFold(rule.Rule, "remove") {
			msg.StatusDetails.Message = ""
		} else if rule.Replacement != "" {
			msg.StatusDetails.Message = rule.Replacement
		} else {
			msg.StatusDetails.Message = defaultMask
		}
	}
	if strings.EqualFold(key, "grpc-message") && msg.StatusMessage != "" {
		if strings.EqualFold(rule.Rule, "remove") {
			msg.StatusMessage = ""
		} else if rule.Replacement != "" {
			msg.StatusMessage = rule.Replacement
		} else {
			msg.StatusMessage = defaultMask
		}
	}
}

func applyHeaderRule(headers artifact.Headers, headerKey string, rule artifact.Redaction) {
	if headers == nil {
		return
	}
	for key := range headers {
		if !strings.EqualFold(key, headerKey) {
			continue
		}
		switch strings.ToLower(rule.Rule) {
		case "remove":
			delete(headers, key)
		default:
			replacement := rule.Replacement
			if replacement == "" {
				replacement = defaultMask
			}
			values := headers[key]
			for i := range values {
				values[i] = replacement
			}
			headers[key] = values
		}
	}
}

func applyBodyRule(msg *artifact.HttpMessage, jsonPath string, rule artifact.Redaction) {
	if msg == nil || msg.Body == nil {
		return
	}
	if msg.Body.Decoded != nil {
		msg.Body.Redacted = true
	}
	msg.Body.Decoded = nil
	// A partial or undecodable representation cannot prove a selected secret is
	// absent. Remove all retained representations and mark the bytes unavailable.
	if msg.Body.Truncated || msg.Body.Bytes != "" || jsonPath == "" {
		omitBody(msg.Body)
		return
	}

	trimmed := strings.TrimSpace(msg.Body.Text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		omitBody(msg.Body)
		return
	}
	if !uniqueJSONKeys([]byte(msg.Body.Text)) {
		omitBody(msg.Body)
		return
	}

	var payload any
	decoder := json.NewDecoder(bytes.NewReader([]byte(msg.Body.Text)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		omitBody(msg.Body)
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		omitBody(msg.Body)
		return
	}

	tokens := parsePath(jsonPath)
	if len(tokens) == 0 {
		omitBody(msg.Body)
		return
	}
	changed := applyJSONRule(&payload, tokens, rule)
	if !changed {
		return
	}

	updated, err := json.Marshal(payload)
	if err != nil {
		omitBody(msg.Body)
		return
	}
	msg.Body.Text = string(updated)
	msg.Body.Redacted = true
	msg.Body.Decoded = nil
}

func omitBody(body *artifact.Body) {
	if body.OriginalBytes == 0 {
		body.OriginalBytes = int64(len(body.Text))
	}
	body.Text, body.Bytes, body.Decoded = "", "", nil
	if body.Encoding != "json" {
		body.Encoding = "utf8"
	}
	body.Omitted, body.Redacted = true, true
}

// Duplicate ancestor keys can hide a selected secret from a map decoder. Reject
// that ambiguity before retaining a body, including when the selector is absent
// from the final occurrence. Bound recursion while inspecting untrusted payloads.
func uniqueJSONKeys(data []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 128 {
			return false
		}
		token, err := dec.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !walk(depth + 1) {
					return false
				}
			}
		case '[':
			for dec.More() {
				if !walk(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		_, err = dec.Token()
		return err == nil
	}
	if !walk(0) {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF
}

type pathToken struct {
	key   string
	index *int
	all   bool
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
				return nil
			}
			idx, err := strconv.Atoi(path[start:i])
			if path[start:i] == "*" {
				tokens = append(tokens, pathToken{all: true})
			} else if err == nil && idx >= 0 {
				idxCopy := idx
				tokens = append(tokens, pathToken{index: &idxCopy})
			} else {
				return nil
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
	for i, token := range tokens {
		if !token.all {
			continue
		}
		parent := current
		for _, prefix := range tokens[:i] {
			switch node := parent.(type) {
			case map[string]any:
				parent = node[prefix.key]
			case []any:
				if prefix.index == nil || *prefix.index < 0 || *prefix.index >= len(node) {
					return false
				}
				parent = node[*prefix.index]
			default:
				return false
			}
		}
		changed := false
		switch node := parent.(type) {
		case []any:
			for index := range node {
				index := index
				replaced := append(append([]pathToken{}, tokens[:i]...), pathToken{index: &index})
				replaced = append(replaced, tokens[i+1:]...)
				changed = applyJSONRule(payload, replaced, rule) || changed
			}
		case map[string]any:
			for key := range node {
				replaced := append(append([]pathToken{}, tokens[:i]...), pathToken{key: key})
				replaced = append(replaced, tokens[i+1:]...)
				changed = applyJSONRule(payload, replaced, rule) || changed
			}
		}
		return changed
	}
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
