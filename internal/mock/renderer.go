package mock

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"
	"unicode/utf8"

	sprig "github.com/Masterminds/sprig/v3"
	gofakeit "github.com/brianvoe/gofakeit/v7"
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
	tmpl, err := template.New("response").
		Funcs(r.templateFuncMap(data)).
		Option("missingkey=zero").
		Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

func (r *Renderer) templateFuncMap(data renderData) template.FuncMap {
	funcs := sprig.TxtFuncMap()
	addGoFakeItFuncs(funcs, gofakeit.New(0))

	funcs["cel"] = func(expr string) (string, error) {
		return r.evalCELString(expr, data)
	}
	funcs["celJSON"] = func(expr string) (string, error) {
		return r.evalCELJSON(expr, data)
	}
	funcs["mustFromJSON"] = mustFromJSON
	funcs["mustToJSON"] = mustToJSON
	funcs["mustToPrettyJSON"] = mustToPrettyJSON
	funcs["nowUTC"] = func() time.Time {
		return data.Now
	}
	funcs["nowPlus"] = func(durationRaw string) (time.Time, error) {
		duration, err := time.ParseDuration(strings.TrimSpace(durationRaw))
		if err != nil {
			return time.Time{}, fmt.Errorf("parse duration: %w", err)
		}
		return data.Now.Add(duration), nil
	}
	funcs["formatTime"] = formatTimeValue
	funcs["uuid"] = newUUIDv4

	return funcs
}

func addGoFakeItFuncs(funcs template.FuncMap, faker *gofakeit.Faker) {
	if funcs == nil || faker == nil {
		return
	}
	fakerValue := reflect.ValueOf(faker)
	fakerType := fakerValue.Type()
	for i := 0; i < fakerType.NumMethod(); i++ {
		method := fakerType.Method(i)
		if method.PkgPath != "" {
			continue
		}
		invoker := makeReflectFuncInvoker(fakerValue.Method(i), method.Name)
		if _, exists := funcs[method.Name]; !exists {
			funcs[method.Name] = invoker
		}
		lowerAlias := lowerFirst(method.Name)
		if _, exists := funcs[lowerAlias]; !exists {
			funcs[lowerAlias] = invoker
		}
	}
}

func makeReflectFuncInvoker(method reflect.Value, name string) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		methodType := method.Type()
		prepared, err := buildReflectArgs(methodType, args)
		if err != nil {
			return nil, fmt.Errorf("faker %s: %w", name, err)
		}

		results := method.Call(prepared)
		switch len(results) {
		case 0:
			return "", nil
		case 1:
			if methodType.Out(0) == reflect.TypeOf((*error)(nil)).Elem() {
				if !results[0].IsNil() {
					return nil, results[0].Interface().(error)
				}
				return "", nil
			}
			return results[0].Interface(), nil
		case 2:
			if methodType.Out(1) == reflect.TypeOf((*error)(nil)).Elem() {
				if !results[1].IsNil() {
					return nil, results[1].Interface().(error)
				}
				return results[0].Interface(), nil
			}
			return results[0].Interface(), nil
		default:
			out := make([]any, 0, len(results))
			for _, result := range results {
				out = append(out, result.Interface())
			}
			return out, nil
		}
	}
}

func buildReflectArgs(methodType reflect.Type, args []any) ([]reflect.Value, error) {
	numIn := methodType.NumIn()
	isVariadic := methodType.IsVariadic()
	if !isVariadic {
		if len(args) != numIn {
			return nil, fmt.Errorf("expected %d args, got %d", numIn, len(args))
		}
		out := make([]reflect.Value, numIn)
		for i := 0; i < numIn; i++ {
			converted, err := coerceValue(args[i], methodType.In(i))
			if err != nil {
				return nil, fmt.Errorf("arg %d: %w", i+1, err)
			}
			out[i] = converted
		}
		return out, nil
	}

	if len(args) < numIn-1 {
		return nil, fmt.Errorf("expected at least %d args, got %d", numIn-1, len(args))
	}

	out := make([]reflect.Value, 0, len(args))
	for i := 0; i < numIn-1; i++ {
		converted, err := coerceValue(args[i], methodType.In(i))
		if err != nil {
			return nil, fmt.Errorf("arg %d: %w", i+1, err)
		}
		out = append(out, converted)
	}
	variadicType := methodType.In(numIn - 1).Elem()
	for i := numIn - 1; i < len(args); i++ {
		converted, err := coerceValue(args[i], variadicType)
		if err != nil {
			return nil, fmt.Errorf("arg %d: %w", i+1, err)
		}
		out = append(out, converted)
	}
	return out, nil
}

func coerceValue(value any, target reflect.Type) (reflect.Value, error) {
	if value == nil {
		if isNilableKind(target.Kind()) {
			return reflect.Zero(target), nil
		}
		return reflect.Zero(target), fmt.Errorf("nil not allowed for %s", target)
	}

	in := reflect.ValueOf(value)
	if in.Type().AssignableTo(target) {
		return in, nil
	}
	if in.Type().ConvertibleTo(target) {
		return in.Convert(target), nil
	}

	switch target.Kind() {
	case reflect.String:
		return reflect.ValueOf(fmt.Sprint(value)).Convert(target), nil
	case reflect.Bool:
		boolVal, err := toBool(value)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(boolVal).Convert(target), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		intVal, err := toInt64(value)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(target).Elem()
		if out.OverflowInt(intVal) {
			return reflect.Value{}, fmt.Errorf("value %d overflows %s", intVal, target)
		}
		out.SetInt(intVal)
		return out, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		uintVal, err := toUint64(value)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(target).Elem()
		if out.OverflowUint(uintVal) {
			return reflect.Value{}, fmt.Errorf("value %d overflows %s", uintVal, target)
		}
		out.SetUint(uintVal)
		return out, nil
	case reflect.Float32, reflect.Float64:
		floatVal, err := toFloat64(value)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(target).Elem()
		if out.OverflowFloat(floatVal) {
			return reflect.Value{}, fmt.Errorf("value %f overflows %s", floatVal, target)
		}
		out.SetFloat(floatVal)
		return out, nil
	case reflect.Ptr:
		if in.Type().AssignableTo(target) {
			return in, nil
		}
		elem, err := coerceValue(value, target.Elem())
		if err != nil {
			return reflect.Value{}, err
		}
		ptr := reflect.New(target.Elem())
		ptr.Elem().Set(elem)
		return ptr, nil
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
		encoded, err := json.Marshal(value)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("json marshal: %w", err)
		}
		out := reflect.New(target)
		if err := json.Unmarshal(encoded, out.Interface()); err != nil {
			return reflect.Value{}, fmt.Errorf("json unmarshal into %s: %w", target, err)
		}
		return out.Elem(), nil
	case reflect.Interface:
		if in.Type().Implements(target) {
			return in, nil
		}
		return in, nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported argument conversion %T -> %s", value, target)
	}
}

func isNilableKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Interface, reflect.Map, reflect.Slice, reflect.Ptr, reflect.Func:
		return true
	default:
		return false
	}
}

func toBool(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(typed))
		if trimmed == "true" {
			return true, nil
		}
		if trimmed == "false" {
			return false, nil
		}
		return false, fmt.Errorf("invalid bool %q", typed)
	default:
		return false, fmt.Errorf("unsupported bool value %T", value)
	}
}

func toInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int8:
		return int64(typed), nil
	case int16:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case uint:
		maxInt64 := uint64(^uint64(0) >> 1)
		if uint64(typed) > maxInt64 {
			return 0, fmt.Errorf("uint overflows int64")
		}
		return int64(typed), nil
	case uint8:
		return int64(typed), nil
	case uint16:
		return int64(typed), nil
	case uint32:
		return int64(typed), nil
	case uint64:
		maxInt64 := uint64(^uint64(0) >> 1)
		if typed > maxInt64 {
			return 0, fmt.Errorf("uint64 overflows int64")
		}
		return int64(typed), nil
	case float32:
		return int64(typed), nil
	case float64:
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported int value %T", value)
	}
}

func toUint64(value any) (uint64, error) {
	switch typed := value.(type) {
	case int:
		if typed < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(typed), nil
	case int8:
		if typed < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(typed), nil
	case int16:
		if typed < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(typed), nil
	case int32:
		if typed < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(typed), nil
	case int64:
		if typed < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(typed), nil
	case uint:
		return uint64(typed), nil
	case uint8:
		return uint64(typed), nil
	case uint16:
		return uint64(typed), nil
	case uint32:
		return uint64(typed), nil
	case uint64:
		return typed, nil
	case float32:
		if typed < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(typed), nil
	case float64:
		if typed < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(typed), nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, err
		}
		if parsed < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(parsed), nil
	case string:
		return strconv.ParseUint(strings.TrimSpace(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported uint value %T", value)
	}
}

func toFloat64(value any) (float64, error) {
	switch typed := value.(type) {
	case float32:
		return float64(typed), nil
	case float64:
		return typed, nil
	case int:
		return float64(typed), nil
	case int8:
		return float64(typed), nil
	case int16:
		return float64(typed), nil
	case int32:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case uint:
		return float64(typed), nil
	case uint8:
		return float64(typed), nil
	case uint16:
		return float64(typed), nil
	case uint32:
		return float64(typed), nil
	case uint64:
		return float64(typed), nil
	case json.Number:
		return typed.Float64()
	case string:
		return strconv.ParseFloat(strings.TrimSpace(typed), 64)
	default:
		return 0, fmt.Errorf("unsupported float value %T", value)
	}
}

func lowerFirst(value string) string {
	if value == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(value)
	if r == utf8.RuneError && size == 0 {
		return value
	}
	return string(unicode.ToLower(r)) + value[size:]
}

func mustFromJSON(raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	var out any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return out, nil
}

func mustToJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode json: %w", err)
	}
	return string(encoded), nil
}

func mustToPrettyJSON(value any) (string, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode pretty json: %w", err)
	}
	return string(encoded), nil
}

func formatTimeValue(layout string, value any) (string, error) {
	trimmedLayout := strings.TrimSpace(layout)
	if trimmedLayout == "" {
		trimmedLayout = time.RFC3339Nano
	}
	t, err := asTime(value)
	if err != nil {
		return "", err
	}
	return t.UTC().Format(trimmedLayout), nil
}

func asTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed, nil
	case *time.Time:
		if typed == nil {
			return time.Time{}, fmt.Errorf("time value is nil")
		}
		return *typed, nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return time.Time{}, fmt.Errorf("time string is empty")
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02",
		}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				return parsed, nil
			}
		}
		return time.Time{}, fmt.Errorf("unsupported time string %q", typed)
	case int64:
		return time.Unix(typed, 0), nil
	case int:
		return time.Unix(int64(typed), 0), nil
	case float64:
		return time.Unix(int64(typed), 0), nil
	case json.Number:
		seconds, err := typed.Int64()
		if err != nil {
			return time.Time{}, fmt.Errorf("parse json number as unix seconds: %w", err)
		}
		return time.Unix(seconds, 0), nil
	default:
		text := fmt.Sprint(value)
		if text == "" {
			return time.Time{}, fmt.Errorf("time value is empty")
		}
		if asInt, err := strconv.ParseInt(text, 10, 64); err == nil {
			return time.Unix(asInt, 0), nil
		}
		return time.Time{}, fmt.Errorf("unsupported time value type %T", value)
	}
}

func newUUIDv4() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("uuid-%d", time.Now().UTC().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80

	hex := fmt.Sprintf("%x", buf)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex[0:8], hex[8:12], hex[12:16], hex[16:20], hex[20:32])
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
