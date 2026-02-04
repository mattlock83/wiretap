package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
)

func evaluateAssertions(art *artifact.Artifact) {
	if art == nil || len(art.Assertions) == 0 {
		return
	}

	for i := range art.Assertions {
		assertion := &art.Assertions[i]
		if assertion.Result != "" && assertion.Result != "recorded" {
			continue
		}
		flow := findFlow(art.Traffic, assertion)
		if flow == nil {
			assertion.Result = "error"
			assertion.Message = "flow not found"
			continue
		}
		msg := messageForAssertion(flow, assertion)
		if msg == nil {
			assertion.Result = "error"
			assertion.Message = "message not found"
			continue
		}

		if err := evaluateAssertion(assertion, msg); err != nil {
			assertion.Result = "error"
			assertion.Message = err.Error()
			continue
		}
	}
}

func findFlow(flows []artifact.Flow, assertion *artifact.Assertion) *artifact.Flow {
	if assertion == nil {
		return nil
	}
	if assertion.FlowID != "" {
		for i := range flows {
			if flows[i].ID == assertion.FlowID {
				return &flows[i]
			}
		}
	}
	if assertion.Channel != "" {
		for i := range flows {
			if flows[i].Channel == assertion.Channel {
				return &flows[i]
			}
		}
	}
	if len(flows) > 0 {
		return &flows[0]
	}
	return nil
}

func messageForAssertion(flow *artifact.Flow, assertion *artifact.Assertion) *artifact.HttpMessage {
	if flow == nil || assertion == nil {
		return nil
	}
	phase := strings.ToLower(assertion.Phase)
	switch phase {
	case "request":
		if flow.Request.HTTP != nil {
			return flow.Request.HTTP
		}
	case "response":
		if flow.Response.HTTP != nil {
			return flow.Response.HTTP
		}
	}
	return nil
}

func evaluateAssertion(assertion *artifact.Assertion, msg *artifact.HttpMessage) error {
	pathLang := strings.ToLower(assertion.PathLang)
	operator := strings.ToLower(assertion.Operator)
	if operator == "" {
		operator = "equals"
	}

	var actual any
	switch pathLang {
	case "status":
		if msg.Status == 0 {
			return fmt.Errorf("status not available")
		}
		actual = msg.Status
	case "header":
		if msg.Headers == nil {
			return fmt.Errorf("headers not available")
		}
		headerKey := assertion.Field
		if headerKey == "" {
			return fmt.Errorf("header field required")
		}
		for key, values := range msg.Headers {
			if strings.EqualFold(key, headerKey) {
				if len(values) == 1 {
					actual = values[0]
				} else {
					actual = values
				}
				break
			}
		}
		if actual == nil {
			return fmt.Errorf("header not found")
		}
	default:
		bodyText := ""
		if msg.Body != nil {
			bodyText = msg.Body.Text
		}
		if bodyText == "" {
			return fmt.Errorf("body not available")
		}
		var payload any
		if err := json.Unmarshal([]byte(bodyText), &payload); err != nil {
			return fmt.Errorf("invalid json body")
		}
		actual = lookupJSON(payload, assertion.Field)
	}

	assertion.Actual = actual
	pass := false

	switch operator {
	case "equals":
		pass = reflect.DeepEqual(actual, assertion.Expected)
	case "exists":
		pass = actual != nil
	default:
		return fmt.Errorf("unsupported operator")
	}

	if pass {
		assertion.Result = "pass"
	} else {
		assertion.Result = "fail"
	}
	return nil
}

type pathToken struct {
	key   string
	index *int
}

func lookupJSON(payload any, path string) any {
	tokens := parsePath(path)
	if len(tokens) == 0 {
		return nil
	}
	current := payload
	for _, tok := range tokens {
		switch node := current.(type) {
		case map[string]any:
			if tok.key == "" {
				return nil
			}
			next, ok := node[tok.key]
			if !ok {
				return nil
			}
			current = next
		case []any:
			if tok.index == nil || *tok.index < 0 || *tok.index >= len(node) {
				return nil
			}
			current = node[*tok.index]
		default:
			return nil
		}
	}
	return current
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
