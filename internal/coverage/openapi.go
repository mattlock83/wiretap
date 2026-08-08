package coverage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/phrond/evermock/internal/artifact"
	"gopkg.in/yaml.v3"
)

// GeneratedOpenAPIOptions controls the standard OpenAPI document produced
// from captured traffic. Captured values are used only to infer schemas; they
// are never emitted as examples.
type GeneratedOpenAPIOptions struct {
	Title   string
	Version string
}

type generatedOpenAPIDocument struct {
	OpenAPI string                        `yaml:"openapi"`
	Info    generatedOpenAPIInfo          `yaml:"info"`
	Paths   map[string]*generatedPathItem `yaml:"paths"`
}

type generatedOpenAPIInfo struct {
	Title       string `yaml:"title"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

type generatedPathItem struct {
	Get     *generatedOperation `yaml:"get,omitempty"`
	Put     *generatedOperation `yaml:"put,omitempty"`
	Post    *generatedOperation `yaml:"post,omitempty"`
	Delete  *generatedOperation `yaml:"delete,omitempty"`
	Options *generatedOperation `yaml:"options,omitempty"`
	Head    *generatedOperation `yaml:"head,omitempty"`
	Patch   *generatedOperation `yaml:"patch,omitempty"`
	Trace   *generatedOperation `yaml:"trace,omitempty"`
}

type generatedOperation struct {
	OperationID string                       `yaml:"operationId"`
	Summary     string                       `yaml:"summary,omitempty"`
	Parameters  []generatedParameter         `yaml:"parameters,omitempty"`
	RequestBody *generatedRequestBody        `yaml:"requestBody,omitempty"`
	Responses   map[string]generatedResponse `yaml:"responses"`
}

type generatedParameter struct {
	Name     string         `yaml:"name"`
	In       string         `yaml:"in"`
	Required bool           `yaml:"required,omitempty"`
	Schema   map[string]any `yaml:"schema"`
}

type generatedRequestBody struct {
	Required bool                          `yaml:"required,omitempty"`
	Content  map[string]generatedMediaType `yaml:"content"`
}

type generatedResponse struct {
	Description string                        `yaml:"description"`
	Content     map[string]generatedMediaType `yaml:"content,omitempty"`
}

type generatedMediaType struct {
	Schema map[string]any `yaml:"schema"`
}

type operationObservation struct {
	method          string
	path            string
	total           int
	parameters      map[string]*parameterObservation
	requestBodies   map[string]*mediaObservation
	requestBodySeen int
	responses       map[int]*responseObservation
}

type parameterObservation struct {
	name     string
	in       string
	position int
	present  int
	multiple bool
	stats    *schemaStats
}

type responseObservation struct {
	content map[string]*mediaObservation
}

type mediaObservation struct {
	stats         *schemaStats
	unconstrained bool
	binary        bool
}

type schemaStats struct {
	total       int
	types       map[string]int
	objectCount int
	properties  map[string]*schemaStats
	items       *schemaStats
	stringCount int
	formats     map[string]int
}

type capturedParameter struct {
	name     string
	value    any
	position int
}

var (
	integerPathSegment = regexp.MustCompile(`^[0-9]+$`)
	uuidPathSegment    = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// WriteGeneratedOpenAPI writes a deterministic OpenAPI 3.1 document inferred
// from the report's captured test-to-SUT HTTP exchanges.
func WriteGeneratedOpenAPI(w io.Writer, report *Report, options GeneratedOpenAPIOptions) error {
	if w == nil {
		return errors.New("openapi output writer is nil")
	}
	doc, err := generatedOpenAPIFromReport(report, options)
	if err != nil {
		return err
	}
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(doc)
}

func generatedOpenAPIFromReport(report *Report, options GeneratedOpenAPIOptions) (*generatedOpenAPIDocument, error) {
	if report == nil {
		return nil, errors.New("report is nil")
	}
	observations := map[string]*operationObservation{}
	for _, testcase := range report.TestCases {
		for _, flow := range testcase.Traffic {
			observeFlow(observations, flow)
		}
	}
	if len(observations) == 0 {
		return nil, errors.New("no captured test-to-SUT HTTP traffic found")
	}

	title := strings.TrimSpace(options.Title)
	if title == "" {
		title = strings.TrimSpace(report.SUTName)
	}
	if title == "" {
		title = "Generated API"
	}
	version := strings.TrimSpace(options.Version)
	if version == "" {
		version = "0.1.0"
	}
	doc := &generatedOpenAPIDocument{
		OpenAPI: "3.1.0",
		Info: generatedOpenAPIInfo{
			Title:       title,
			Version:     version,
			Description: "Generated from captured test traffic. Review before using as an enforced contract.",
		},
		Paths: map[string]*generatedPathItem{},
	}

	keys := make([]string, 0, len(observations))
	for key := range observations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		observation := observations[key]
		item := doc.Paths[observation.path]
		if item == nil {
			item = &generatedPathItem{}
			doc.Paths[observation.path] = item
		}
		operation := observation.operation()
		if !item.set(observation.method, operation) {
			continue
		}
	}
	if len(doc.Paths) == 0 {
		return nil, errors.New("captured traffic did not contain supported OpenAPI HTTP methods")
	}
	return doc, nil
}

func observeFlow(observations map[string]*operationObservation, flow FlowSample) {
	if !strings.EqualFold(flow.Protocol, "http") || !strings.EqualFold(flow.Channel, "test_to_sut") {
		return
	}
	if strings.EqualFold(flow.TestStatus, "fail") {
		return
	}
	method := strings.ToUpper(strings.TrimSpace(flow.Request.Method))
	if !supportedOpenAPIMethod(method) {
		return
	}
	parsed, err := url.Parse(strings.TrimSpace(flow.Request.URL))
	if err != nil {
		return
	}
	path, capturedParams := generatedPath(parsed.EscapedPath())
	key := method + " " + path
	observation := observations[key]
	if observation == nil {
		observation = &operationObservation{
			method:        method,
			path:          path,
			parameters:    map[string]*parameterObservation{},
			requestBodies: map[string]*mediaObservation{},
			responses:     map[int]*responseObservation{},
		}
		observations[key] = observation
	}
	observation.total++

	for _, parameter := range capturedParams {
		observation.observeParameter(parameter.name, "path", parameter.position, []any{parameter.value})
	}
	queryKeys := make([]string, 0, len(parsed.Query()))
	for name := range parsed.Query() {
		queryKeys = append(queryKeys, name)
	}
	sort.Strings(queryKeys)
	for _, name := range queryKeys {
		values := parsed.Query()[name]
		observed := make([]any, 0, len(values))
		for _, value := range values {
			observed = append(observed, queryScalar(value))
		}
		observation.observeParameter(name, "query", len(capturedParams)+len(observation.parameters), observed)
	}

	if bodyPresent(flow.Request.ArtifactBody) {
		observation.requestBodySeen++
		contentType := bodyContentType(flow.Request.Headers, flow.Request.ArtifactBody)
		observation.observeBody(observation.requestBodies, contentType, flow.Request.ArtifactBody)
	}
	status := flow.Response.Status
	if status <= 0 {
		status = 0
	}
	response := observation.responses[status]
	if response == nil {
		response = &responseObservation{content: map[string]*mediaObservation{}}
		observation.responses[status] = response
	}
	if bodyPresent(flow.Response.ArtifactBody) {
		contentType := bodyContentType(flow.Response.Headers, flow.Response.ArtifactBody)
		observation.observeBody(response.content, contentType, flow.Response.ArtifactBody)
	}
}

func (o *operationObservation) observeParameter(name, location string, position int, values []any) {
	key := location + ":" + name
	parameter := o.parameters[key]
	if parameter == nil {
		parameter = &parameterObservation{name: name, in: location, position: position, stats: newSchemaStats()}
		o.parameters[key] = parameter
	}
	parameter.present++
	if len(values) > 1 {
		parameter.multiple = true
	}
	for _, value := range values {
		parameter.stats.observe(value)
	}
}

func (o *operationObservation) observeBody(target map[string]*mediaObservation, contentType string, body *artifact.Body) {
	media := target[contentType]
	if media == nil {
		media = &mediaObservation{stats: newSchemaStats()}
		target[contentType] = media
	}
	if body == nil || body.Truncated {
		media.unconstrained = true
		return
	}
	raw, binary := artifactBodyBytes(body)
	if binary || strings.EqualFold(contentType, "application/octet-stream") {
		media.binary = true
		return
	}
	if isJSONContentType(contentType) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			media.unconstrained = true
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			media.unconstrained = true
			return
		}
		media.stats.observe(value)
		return
	}
	media.stats.observe(string(raw))
}

func (o *operationObservation) operation() *generatedOperation {
	operation := &generatedOperation{
		OperationID: generatedOperationID(o.method, o.path),
		Summary:     "Captured " + o.method + " " + o.path,
		Responses:   map[string]generatedResponse{},
	}
	parameters := make([]*parameterObservation, 0, len(o.parameters))
	for _, parameter := range o.parameters {
		parameters = append(parameters, parameter)
	}
	sort.Slice(parameters, func(i, j int) bool {
		if parameters[i].in != parameters[j].in {
			return parameters[i].in == "path"
		}
		if parameters[i].position != parameters[j].position {
			return parameters[i].position < parameters[j].position
		}
		return parameters[i].name < parameters[j].name
	})
	for _, parameter := range parameters {
		schema := parameter.stats.schema()
		if parameter.multiple {
			schema = map[string]any{"type": "array", "items": schema}
		}
		operation.Parameters = append(operation.Parameters, generatedParameter{
			Name:     parameter.name,
			In:       parameter.in,
			Required: parameter.in == "path" || parameter.present == o.total,
			Schema:   schema,
		})
	}
	if len(o.requestBodies) > 0 {
		operation.RequestBody = &generatedRequestBody{
			Required: o.requestBodySeen == o.total,
			Content:  generatedContent(o.requestBodies),
		}
	}
	statuses := make([]int, 0, len(o.responses))
	for status := range o.responses {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	for _, status := range statuses {
		key := "default"
		if status > 0 {
			key = strconv.Itoa(status)
		}
		response := generatedResponse{Description: "Captured response"}
		if len(o.responses[status].content) > 0 {
			response.Content = generatedContent(o.responses[status].content)
		}
		operation.Responses[key] = response
	}
	if len(operation.Responses) == 0 {
		operation.Responses["default"] = generatedResponse{Description: "Captured response"}
	}
	return operation
}

func generatedContent(observations map[string]*mediaObservation) map[string]generatedMediaType {
	content := make(map[string]generatedMediaType, len(observations))
	for contentType, observation := range observations {
		schema := map[string]any{}
		switch {
		case observation.unconstrained:
		case observation.binary:
			schema = map[string]any{"type": "string", "format": "binary"}
		default:
			schema = observation.stats.schema()
		}
		content[contentType] = generatedMediaType{Schema: schema}
	}
	return content
}

func (p *generatedPathItem) set(method string, operation *generatedOperation) bool {
	switch method {
	case "GET":
		p.Get = operation
	case "PUT":
		p.Put = operation
	case "POST":
		p.Post = operation
	case "DELETE":
		p.Delete = operation
	case "OPTIONS":
		p.Options = operation
	case "HEAD":
		p.Head = operation
	case "PATCH":
		p.Patch = operation
	case "TRACE":
		p.Trace = operation
	default:
		return false
	}
	return true
}

func supportedOpenAPIMethod(method string) bool {
	switch method {
	case "GET", "PUT", "POST", "DELETE", "OPTIONS", "HEAD", "PATCH", "TRACE":
		return true
	default:
		return false
	}
}

func generatedPath(escapedPath string) (string, []capturedParameter) {
	if escapedPath == "" {
		return "/", nil
	}
	segments := strings.Split(strings.TrimPrefix(escapedPath, "/"), "/")
	result := make([]string, 0, len(segments))
	parameters := []capturedParameter{}
	usedNames := map[string]int{}
	previousStatic := ""
	for position, escaped := range segments {
		segment, err := url.PathUnescape(escaped)
		if err != nil {
			segment = escaped
		}
		value, dynamic := pathScalar(segment)
		if !dynamic {
			result = append(result, escaped)
			if segment != "" {
				previousStatic = segment
			}
			continue
		}
		name := pathParameterName(previousStatic)
		usedNames[name]++
		if usedNames[name] > 1 {
			name += strconv.Itoa(usedNames[name])
		}
		result = append(result, "{"+name+"}")
		parameters = append(parameters, capturedParameter{name: name, value: value, position: position})
	}
	return "/" + strings.Join(result, "/"), parameters
}

func pathScalar(segment string) (any, bool) {
	if integerPathSegment.MatchString(segment) {
		return json.Number(segment), true
	}
	if uuidPathSegment.MatchString(segment) {
		return segment, true
	}
	return nil, false
}

func queryScalar(raw string) any {
	if raw == "true" || raw == "false" {
		return raw == "true"
	}
	if integerPathSegment.MatchString(raw) {
		return json.Number(raw)
	}
	if _, err := strconv.ParseFloat(raw, 64); err == nil && strings.ContainsAny(raw, ".eE") {
		return json.Number(raw)
	}
	return raw
}

func pathParameterName(previous string) string {
	name := identifier(previous)
	if name == "" {
		return "id"
	}
	switch {
	case strings.HasSuffix(name, "ies") && len(name) > 3:
		name = strings.TrimSuffix(name, "ies") + "y"
	case strings.HasSuffix(name, "ses"):
		name = strings.TrimSuffix(name, "es")
	case strings.HasSuffix(name, "s") && !strings.HasSuffix(name, "ss"):
		name = strings.TrimSuffix(name, "s")
	}
	return name + "Id"
}

func identifier(raw string) string {
	var result strings.Builder
	upperNext := false
	for _, r := range raw {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upperNext = result.Len() > 0
			continue
		}
		if result.Len() == 0 {
			result.WriteRune(unicode.ToLower(r))
			continue
		}
		if upperNext {
			result.WriteRune(unicode.ToUpper(r))
			upperNext = false
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func generatedOperationID(method, path string) string {
	parts := []string{strings.ToLower(method)}
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		segment = strings.Trim(segment, "{}")
		if value := identifier(segment); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "_")
}

func bodyPresent(body *artifact.Body) bool {
	if body == nil {
		return false
	}
	return body.Text != "" || body.Bytes != "" || body.OriginalBytes > 0 || body.Decoded != nil && body.Decoded.Text != ""
}

func artifactBodyBytes(body *artifact.Body) ([]byte, bool) {
	if body == nil {
		return nil, false
	}
	if body.Text != "" {
		return []byte(body.Text), false
	}
	if body.Decoded != nil && body.Decoded.Text != "" {
		return []byte(body.Decoded.Text), false
	}
	if body.Bytes != "" {
		decoded, err := base64.StdEncoding.DecodeString(body.Bytes)
		if err == nil {
			return decoded, true
		}
		return []byte(body.Bytes), true
	}
	return nil, false
}

func bodyContentType(headers artifact.Headers, body *artifact.Body) string {
	for key, values := range headers {
		if !strings.EqualFold(key, "Content-Type") || len(values) == 0 {
			continue
		}
		if value := strings.TrimSpace(strings.SplitN(values[0], ";", 2)[0]); value != "" {
			return strings.ToLower(value)
		}
	}
	if body != nil && body.Encoding == "base64" {
		return "application/octet-stream"
	}
	if body != nil {
		raw, binary := artifactBodyBytes(body)
		if !binary && json.Valid(raw) {
			return "application/json"
		}
	}
	return "text/plain"
}

func isJSONContentType(contentType string) bool {
	return contentType == "application/json" || strings.HasSuffix(contentType, "+json")
}

func newSchemaStats() *schemaStats {
	return &schemaStats{types: map[string]int{}, properties: map[string]*schemaStats{}, formats: map[string]int{}}
}

func (s *schemaStats) observe(value any) {
	if s == nil {
		return
	}
	s.total++
	switch typed := value.(type) {
	case nil:
		s.types["null"]++
	case bool:
		s.types["boolean"]++
	case json.Number:
		if _, err := typed.Int64(); err == nil {
			s.types["integer"]++
		} else {
			s.types["number"]++
		}
	case float64:
		if typed == float64(int64(typed)) {
			s.types["integer"]++
		} else {
			s.types["number"]++
		}
	case string:
		s.types["string"]++
		s.stringCount++
		if format := stringFormat(typed); format != "" {
			s.formats[format]++
		}
	case []any:
		s.types["array"]++
		if s.items == nil {
			s.items = newSchemaStats()
		}
		for _, item := range typed {
			s.items.observe(item)
		}
	case map[string]any:
		s.types["object"]++
		s.objectCount++
		for name, property := range typed {
			child := s.properties[name]
			if child == nil {
				child = newSchemaStats()
				s.properties[name] = child
			}
			child.observe(property)
		}
	default:
		s.types["string"]++
	}
}

func (s *schemaStats) schema() map[string]any {
	if s == nil || s.total == 0 || len(s.types) == 0 {
		return map[string]any{}
	}
	types := make([]string, 0, len(s.types))
	for typ := range s.types {
		types = append(types, typ)
	}
	sort.Strings(types)
	result := map[string]any{}
	if len(types) == 1 {
		result["type"] = types[0]
	} else {
		result["type"] = types
	}
	if s.types["object"] > 0 {
		properties := map[string]any{}
		required := []string{}
		names := make([]string, 0, len(s.properties))
		for name := range s.properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			properties[name] = s.properties[name].schema()
			if s.properties[name].total == s.objectCount {
				required = append(required, name)
			}
		}
		result["properties"] = properties
		result["additionalProperties"] = false
		if len(required) > 0 {
			result["required"] = required
		}
	}
	if s.types["array"] > 0 {
		if s.items == nil {
			result["items"] = map[string]any{}
		} else {
			result["items"] = s.items.schema()
		}
	}
	if s.types["string"] > 0 && s.stringCount > 0 {
		for format, count := range s.formats {
			if count == s.stringCount {
				result["format"] = format
				break
			}
		}
	}
	return result
}

func stringFormat(value string) string {
	if uuidPathSegment.MatchString(value) {
		return "uuid"
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return "date-time"
	}
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return "date"
	}
	return ""
}
