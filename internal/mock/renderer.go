package mock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

type renderData struct {
	Request renderRequest
	Now     time.Time
}

type renderRequest struct {
	Method  string
	Path    string
	URL     string
	Headers map[string]string
	Query   map[string]string
	Body    string
}

// Renderer applies response templates using request data.
type Renderer struct {
	env      *cel.Env
	envErr   error
	mu       sync.RWMutex
	programs map[string]cel.Program
}

func NewRenderer() *Renderer {
	env, err := cel.NewEnv(
		cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("method", cel.StringType),
		cel.Variable("path", cel.StringType),
		cel.Variable("url", cel.StringType),
		cel.Variable("body", cel.StringType),
		cel.Variable("headers", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("query", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("now", cel.TimestampType),
	)
	return &Renderer{
		env:      env,
		envErr:   err,
		programs: map[string]cel.Program{},
	}
}

// RenderResponse returns the rendered response based on stub templates.
func (r *Renderer) RenderResponse(stub Stub, req RecordedRequest) (RecordedResponse, error) {
	data := renderData{
		Request: renderRequest{
			Method:  req.Method,
			Path:    req.Path,
			URL:     req.URL,
			Headers: flattenHeader(req.Headers),
			Query:   flattenQuery(req.Query),
			Body:    req.Body,
		},
		Now: time.Now().UTC(),
	}

	body, err := r.renderTemplate(stub.Response.Body, data)
	if err != nil {
		return RecordedResponse{}, err
	}

	headers := make(map[string][]string, len(stub.Response.Headers))
	for key, value := range stub.Response.Headers {
		rendered, err := r.renderTemplate(value, data)
		if err != nil {
			return RecordedResponse{}, err
		}
		headers[key] = []string{rendered}
	}

	status := stub.Response.Status
	if status == 0 {
		status = 200
	}

	return RecordedResponse{
		Status:  status,
		DelayMS: stub.Response.DelayMS,
		Headers: headers,
		Body:    body,
		Fault:   stub.Response.Fault,
	}, nil
}

func (r *Renderer) renderTemplate(raw string, data renderData) (string, error) {
	if raw == "" {
		return "", nil
	}
	tmpl, err := template.New("response").Funcs(template.FuncMap{
		"cel": func(expr string) (string, error) {
			return r.evalCELString(expr, data)
		},
		"celJSON": func(expr string) (string, error) {
			return r.evalCELJSON(expr, data)
		},
	}).Option("missingkey=zero").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

func (r *Renderer) evalCELString(expr string, data renderData) (string, error) {
	value, err := r.evalCEL(expr, data)
	if err != nil {
		return "", err
	}
	return formatCELValue(value)
}

func (r *Renderer) evalCELJSON(expr string, data renderData) (string, error) {
	value, err := r.evalCEL(expr, data)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("cel json encode: %w", err)
	}
	return string(encoded), nil
}

func (r *Renderer) evalCEL(expr string, data renderData) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("renderer not configured")
	}
	if r.envErr != nil {
		return nil, fmt.Errorf("cel env init: %w", r.envErr)
	}

	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return nil, fmt.Errorf("cel expression is required")
	}

	program, err := r.lookupProgram(trimmed)
	if err != nil {
		return nil, err
	}

	headers := data.Request.Headers
	query := data.Request.Query
	activation := map[string]any{
		"request": map[string]any{
			"method":  data.Request.Method,
			"path":    data.Request.Path,
			"url":     data.Request.URL,
			"headers": headers,
			"query":   query,
			"body":    data.Request.Body,
		},
		"method":  data.Request.Method,
		"path":    data.Request.Path,
		"url":     data.Request.URL,
		"body":    data.Request.Body,
		"headers": headers,
		"query":   query,
		"now":     data.Now,
	}

	out, _, err := program.Eval(activation)
	if err != nil {
		return nil, fmt.Errorf("cel eval: %w", err)
	}

	if out == nil {
		return nil, nil
	}

	return celValueToNative(out)
}

func (r *Renderer) lookupProgram(expr string) (cel.Program, error) {
	r.mu.RLock()
	program, ok := r.programs[expr]
	r.mu.RUnlock()
	if ok {
		return program, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, found := r.programs[expr]; found {
		return cached, nil
	}

	ast, issues := r.env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("cel compile: %w", issues.Err())
	}

	compiled, err := r.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel program: %w", err)
	}

	r.programs[expr] = compiled
	return compiled, nil
}

func celValueToNative(value ref.Val) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch typed := value.(type) {
	case types.String:
		return string(typed), nil
	case types.Bytes:
		return []byte(typed), nil
	case types.Int:
		return int64(typed), nil
	case types.Uint:
		return uint64(typed), nil
	case types.Double:
		return float64(typed), nil
	case types.Bool:
		return bool(typed), nil
	case types.Null:
		return nil, nil
	case types.Timestamp:
		return typed.Time, nil
	case types.Duration:
		return typed.Duration, nil
	}

	if mapper, ok := value.(traits.Mapper); ok {
		out := map[string]any{}
		it := mapper.Iterator()
		for it.HasNext() == types.True {
			keyVal := it.Next()
			nativeKey, err := celValueToNative(keyVal)
			if err != nil {
				return nil, err
			}
			mapped := mapper.Get(keyVal)
			nativeValue, err := celValueToNative(mapped)
			if err != nil {
				return nil, err
			}
			out[fmt.Sprint(nativeKey)] = nativeValue
		}
		return out, nil
	}

	if lister, ok := value.(traits.Lister); ok {
		out := []any{}
		it := lister.Iterator()
		for it.HasNext() == types.True {
			item := it.Next()
			native, err := celValueToNative(item)
			if err != nil {
				return nil, err
			}
			out = append(out, native)
		}
		return out, nil
	}

	if types.IsError(value) {
		return nil, fmt.Errorf("%v", value)
	}
	return value.Value(), nil
}

func formatCELValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano), nil
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", fmt.Errorf("cel value encode: %w", err)
		}
		return string(encoded), nil
	default:
		return fmt.Sprint(typed), nil
	}
}

func flattenHeader(headers map[string][]string) map[string]string {
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		out[key] = values[0]
	}
	return out
}

func flattenQuery(query map[string][]string) map[string]string {
	out := make(map[string]string, len(query))
	for key, values := range query {
		if len(values) == 0 {
			continue
		}
		out[key] = values[0]
	}
	return out
}
