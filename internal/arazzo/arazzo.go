// Package arazzo exports captured JSON/REST test attempts as Arazzo workflows.
package arazzo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/fileuri"
)

type Document struct {
	Arazzo    string     `json:"arazzo"`
	Info      Info       `json:"info"`
	Sources   []Source   `json:"sourceDescriptions"`
	Workflows []Workflow `json:"workflows"`
}
type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}
type Source struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Type string `json:"type"`
}
type Workflow struct {
	ID          string         `json:"workflowId"`
	Summary     string         `json:"summary,omitempty"`
	Description string         `json:"description,omitempty"`
	Inputs      map[string]any `json:"inputs,omitempty"`
	Steps       []Step         `json:"steps"`
	AttemptID   string         `json:"x-wiretap-attempt-id"`
	Boundary    string         `json:"x-wiretap-boundary"`
}
type Step struct {
	ID              string           `json:"stepId"`
	OperationPath   string           `json:"operationPath"`
	Parameters      []Parameter      `json:"parameters,omitempty"`
	RequestBody     *RequestBody     `json:"requestBody,omitempty"`
	SuccessCriteria []Criterion      `json:"successCriteria"`
	FlowID          string           `json:"x-wiretap-flow-id"`
	URL             string           `json:"x-wiretap-url"`
	Headers         artifact.Headers `json:"x-wiretap-request-headers,omitempty"`
}
type Parameter struct {
	Name  string `json:"name"`
	In    string `json:"in"`
	Value any    `json:"value"`
}
type RequestBody struct {
	ContentType string `json:"contentType"`
	Payload     any    `json:"payload"`
}
type Criterion struct {
	Condition string `json:"condition"`
}
type File struct {
	Path string
	Data []byte
}
type Export struct {
	Document *Document
	Files    []File
}

type sourceSpec struct {
	name, root, key string
	doc             *openapi3.T
	router          routers.Router
	observed        map[string]any
}

// The legacy router uses only document-level servers. Group operations by their
// effective servers so path and operation overrides retain their base paths.
type sourceRouter struct{ groups []routers.Router }

func (r *sourceRouter) FindRoute(request *http.Request) (*routers.Route, map[string]string, error) {
	var selected *routers.Route
	var selectedParams map[string]string
	for _, group := range r.groups {
		route, params, err := group.FindRoute(request)
		if err != nil {
			continue
		}
		if selected == nil || strings.Count(route.Path, "{") < strings.Count(selected.Path, "{") {
			selected, selectedParams = route, params
		}
	}
	if selected == nil {
		return nil, nil, errors.New("request does not match frozen OpenAPI paths and servers")
	}
	return selected, selectedParams, nil
}

// Generate verifies the archive before deriving any output. A nil Document
// means the run contains no eligible JSON/REST interactions.
func Generate(archive, sutName string) (*Export, error) {
	run, captures, err := bundle.Load(archive)
	if err != nil {
		return nil, fmt.Errorf("export Arazzo: %w", err)
	}
	return GenerateSnapshot(run, captures, sutName)
}

// GenerateSnapshot derives workflows from an already-verified archive snapshot.
// It preserves the caller's capture order so the same snapshot can feed coverage.
func GenerateSnapshot(run *bundle.Run, captures []*artifact.Artifact, sutName string) (*Export, error) {
	if run == nil || len(run.Attempts) != len(captures) {
		return nil, errors.New("export Arazzo: bundle inventory and captures disagree")
	}
	captures = append([]*artifact.Artifact(nil), captures...)
	if sutName == "" {
		sutName = "Wiretap"
		if run.Application != nil {
			sutName = run.Application.Name
		}
	}
	if sutName == "" {
		sutName = "Wiretap"
	}
	doc := &Document{Arazzo: "1.0.1", Info: Info{Title: sutName + " captured workflows", Version: "1.0.0", Description: "Run: " + run.RunID + ". One workflow per test attempt and API boundary, in request start order. Captured values are workflow input defaults; no response-to-request dependencies are inferred. Mock dependency workflows are separate from SUT workflows. Sources without a frozen contract describe observed requests only."}}
	result := &Export{}
	sources := map[string]*sourceSpec{}
	files := map[string][]byte{}
	allStarted := true
	for _, capture := range captures {
		allStarted = allStarted && !capture.TestCase.StartedAt.IsZero()
	}
	if allStarted {
		sort.SliceStable(captures, func(i, j int) bool { return captures[i].TestCase.StartedAt.Before(captures[j].TestCase.StartedAt) })
	}
	for _, capture := range captures {
		groups := map[string][]artifact.Flow{}
		for _, flow := range capture.Traffic {
			if flow.Protocol != "http" || flow.Request.HTTP == nil {
				continue
			}
			boundary := "sut"
			if flow.Channel == "sut_to_dependency" {
				if run.DependencyKinds[flow.AssetID] != "mock" {
					continue
				}
				boundary = "mock:" + flow.AssetID
			} else if flow.Channel != "test_to_sut" {
				continue
			}
			groups[boundary] = append(groups[boundary], flow)
		}
		boundaries := sortedKeys(groups)
		for _, boundary := range boundaries {
			flows := groups[boundary]
			chronological := true
			for _, flow := range flows {
				chronological = chronological && !flow.StartedAt.IsZero()
			}
			if chronological {
				sort.SliceStable(flows, func(i, j int) bool { return flows[i].StartedAt.Before(flows[j].StartedAt) })
			}
			workflow := Workflow{ID: fmt.Sprintf("workflow_%d", len(doc.Workflows)+1), Summary: capture.TestCase.Name, AttemptID: capture.TestCase.ID, Boundary: boundary, Description: "Captured attempt status: " + capture.TestCase.Status + "."}
			if !chronological {
				workflow.Description += " Request start timestamps are missing; retained capture order is used."
			}
			properties := map[string]any{}
			workflow.Inputs = map[string]any{"type": "object", "properties": properties}
			input := func(value any) string {
				name := fmt.Sprintf("value_%d", len(properties)+1)
				properties[name] = map[string]any{"default": value}
				return "$inputs." + name
			}
			for _, flow := range flows {
				step, eligible, err := makeStep(run, capture, flow, sources, files, input)
				if err != nil {
					return nil, fmt.Errorf("export Arazzo test %q flow %q: %w", capture.TestCase.ID, flow.ID, err)
				}
				if !eligible {
					continue
				}
				if capture.CaptureIncomplete || len(capture.Redactions) > 0 {
					return nil, fmt.Errorf("export Arazzo test %q: incomplete or redacted capture", capture.TestCase.ID)
				}
				step.ID = fmt.Sprintf("step_%d", len(workflow.Steps)+1)
				workflow.Steps = append(workflow.Steps, step)
			}
			if len(workflow.Steps) > 0 {
				doc.Workflows = append(doc.Workflows, workflow)
			}
		}
	}
	if len(doc.Workflows) == 0 {
		return result, nil
	}
	ordered := make([]*sourceSpec, 0, len(sources))
	for _, source := range sources {
		ordered = append(ordered, source)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].name < ordered[j].name })
	for _, source := range ordered {
		if source.observed != nil {
			data, err := json.MarshalIndent(source.observed, "", "  ")
			if err != nil {
				return nil, err
			}
			files[source.root] = append(data, '\n')
		}
		doc.Sources = append(doc.Sources, Source{Name: source.name, URL: source.root, Type: "openapi"})
	}
	for _, path := range sortedKeys(files) {
		result.Files = append(result.Files, File{Path: path, Data: files[path]})
	}
	result.Document = doc
	return result, nil
}

func makeStep(run *bundle.Run, capture *artifact.Artifact, flow artifact.Flow, sources map[string]*sourceSpec, files map[string][]byte, input func(any) string) (Step, bool, error) {
	step := Step{FlowID: flow.ID, URL: flow.Request.HTTP.URL, Headers: flow.Request.HTTP.Headers}
	req, res := flow.Request.HTTP, flow.Response.HTTP
	// A non-JSON representation is outside the requested JSON/REST export.
	jsonEvidence := false
	for _, message := range []*artifact.HttpMessage{req, res} {
		if message == nil {
			continue
		}
		if len(headerValues(message.Headers, "Content-Type")) > 1 {
			return step, false, errors.New("multiple captured Content-Type values are ambiguous")
		}
		media := contentType(message.Headers)
		if media != "" && !jsonMedia(media) {
			return step, false, nil
		}
		jsonEvidence = jsonEvidence || jsonMedia(media)
		if bodyPresent(message.Body) {
			if media == "" {
				return step, false, nil
			}
		}
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return step, false, errors.New("request requires an absolute HTTP URL without credentials or fragment")
	}
	if u.Path == "" {
		u.Path = "/"
	}
	var source *sourceSpec
	var route *routers.Route
	var pathParams map[string]string
	for _, ref := range capture.Schemas {
		if !schemaMatches(ref, flow) || ref.Type != "openapi" || ref.Source.Type == "omitted" {
			continue
		}
		key := "frozen:" + ref.Source.Path
		spec := sources[key]
		if spec == nil {
			spec, err = loadSource(ref, fmt.Sprintf("api_%d", len(sources)+1), files)
			if err != nil {
				return step, false, err
			}
		}
		requestURL := *u
		requestURL.Scheme = "http"
		requestURL.Host = "wiretap.invalid"
		request, err := http.NewRequest(req.Method, requestURL.String(), nil)
		if err != nil {
			return step, false, err
		}
		matched, params, err := spec.router.FindRoute(request)
		if err != nil {
			continue
		}
		if source != nil {
			if source.key == spec.key {
				continue
			}
			return step, false, errors.New("multiple frozen OpenAPI operations match the request")
		}
		source, route, pathParams = spec, matched, params
		// Cache only sources actually used by a matching JSON operation below.
	}
	if source != nil {
		jsonEvidence = jsonEvidence || operationJSON(route.Operation) || (!bodyPresent(req.Body) && res != nil && !bodyPresent(res.Body))
	}
	if !jsonEvidence {
		return step, false, nil
	}
	if flow.TransportError != "" || res == nil || res.Status < 100 || res.Status > 599 {
		return step, false, errors.New("a complete HTTP response is required")
	}
	for _, message := range []*artifact.HttpMessage{req, res} {
		if len(message.Trailers) > 0 {
			return step, false, errors.New("HTTP trailers cannot be represented")
		}
		if message.Body != nil && (message.Body.Truncated || message.Body.Omitted || message.Body.Redacted) {
			return step, false, errors.New("truncated, omitted or redacted body cannot be represented")
		}
		if bodyPresent(message.Body) {
			if _, err := jsonBody(message.Body); err != nil {
				return step, false, err
			}
		}
	}
	if source == nil {
		// Do not disguise a missing operation in a declared frozen contract.
		for _, ref := range capture.Schemas {
			if schemaMatches(ref, flow) && ref.Type == "openapi" && ref.Source.Type != "omitted" {
				return step, false, errors.New("request does not match its frozen OpenAPI contract")
			}
		}
		key := "observed:" + flow.Channel + ":" + flow.AssetID + ":" + u.Scheme + "://" + u.Host
		source = sources[key]
		if source == nil {
			name := fmt.Sprintf("api_%d", len(sources)+1)
			source = &sourceSpec{name: name, root: "observed/" + name + ".openapi.json", observed: map[string]any{"openapi": "3.0.3", "info": map[string]any{"title": "Wiretap observed JSON/REST requests", "version": "1.0.0", "description": "Observed requests only; this is not a declared or inferred API contract."}, "servers": []any{map[string]any{"url": u.Scheme + "://" + u.Host}}, "paths": map[string]any{}}}
			sources[key] = source
		}
		if err := observedStep(source, flow, u, &step, input); err != nil {
			return step, false, err
		}
	} else {
		sources[source.key] = source
		step.OperationPath = operationPath(source.name, route.Path, req.Method)
		parameters := map[string]*openapi3.Parameter{}
		for _, refs := range []openapi3.Parameters{route.PathItem.Parameters, route.Operation.Parameters} {
			for _, ref := range refs {
				if ref.Value != nil {
					parameters[ref.Value.In+":"+ref.Value.Name] = ref.Value
				}
			}
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return step, false, err
		}
		for name := range query {
			if parameters["query:"+name] == nil {
				return step, false, fmt.Errorf("query parameter %q is absent from the frozen OpenAPI operation", name)
			}
		}
		for _, key := range sortedKeys(parameters) {
			p := parameters[key]
			var values []string
			switch p.In {
			case "path":
				if value, ok := pathParams[p.Name]; ok {
					values = []string{value}
				}
			case "query":
				values = u.Query()[p.Name]
			case "header":
				values = headerValues(req.Headers, p.Name)
			case "cookie":
				request := http.Request{Header: http.Header{"Cookie": headerValues(req.Headers, "Cookie")}}
				for _, cookie := range request.Cookies() {
					if cookie.Name == p.Name {
						values = append(values, cookie.Value)
					}
				}
			}
			if len(values) == 0 {
				continue
			}
			if len(values) > 1 {
				return step, false, fmt.Errorf("repeated %s parameter %q cannot be represented without changing serialization", p.In, p.Name)
			}
			if p.Schema != nil && p.Schema.Value != nil && (p.Schema.Value.Type.Is("array") || p.Schema.Value.Type.Is("object")) {
				return step, false, fmt.Errorf("structured %s parameter %q is unsupported", p.In, p.Name)
			}
			// OpenAPI reserves these header names for content negotiation and security.
			// The complete captured header set remains available in the step extension.
			if p.In == "header" && reservedHeader(p.Name) {
				continue
			}
			step.Parameters = append(step.Parameters, Parameter{Name: p.Name, In: p.In, Value: input(values[0])})
		}
	}
	if bodyPresent(req.Body) {
		value, err := jsonBody(req.Body)
		if err != nil {
			return step, false, err
		}
		step.RequestBody = &RequestBody{ContentType: contentType(req.Headers), Payload: input(value)}
	}
	step.SuccessCriteria = []Criterion{{Condition: "$statusCode == " + strconv.Itoa(res.Status)}}
	return step, true, nil
}

func schemaMatches(ref artifact.SchemaRef, flow artifact.Flow) bool {
	if flow.Channel == "sut_to_dependency" {
		return ref.Role == "dependency" && ref.AssetID == flow.AssetID
	}
	if ref.Role == "provided" {
		return ref.AssetID != "" && ref.AssetID == flow.AssetID
	}
	return ref.Role == "sut" && (ref.AssetID == "" || flow.AssetID == "" || ref.AssetID == flow.AssetID)
}

func loadSource(ref artifact.SchemaRef, name string, files map[string][]byte) (*sourceSpec, error) {
	root, members, err := bundle.OpenAPIFiles(ref)
	if err != nil {
		return nil, err
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, uri *url.URL) ([]byte, error) {
		if uri.Scheme != "file" || uri.RawQuery != "" {
			return nil, errors.New("OpenAPI reference leaves frozen contract")
		}
		path, err := fileuri.Path(uri)
		if err != nil {
			return nil, err
		}
		base := filepath.Dir(filepath.Dir(filepath.Dir(ref.Source.Path)))
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return nil, err
		}
		data, found := members[filepath.ToSlash(relative)]
		if !found {
			return nil, errors.New("OpenAPI reference is absent from frozen manifest")
		}
		return data, nil
	}
	uri, err := fileuri.FromPath(ref.Source.Path)
	if err != nil {
		return nil, err
	}
	doc, err := loader.LoadFromURI(uri)
	if err != nil {
		return nil, err
	}
	groups := map[string]*openapi3.T{}
	for _, path := range sortedKeys(doc.Paths.Map()) {
		item := doc.Paths.Value(path)
		for _, method := range sortedKeys(item.Operations()) {
			operation := item.GetOperation(method)
			servers := doc.Servers
			if len(item.Servers) > 0 {
				servers = item.Servers
			}
			if operation.Servers != nil && len(*operation.Servers) > 0 {
				servers = *operation.Servers
			}
			servers, err = localServers(servers)
			if err != nil {
				return nil, err
			}
			if len(servers) == 0 {
				servers = openapi3.Servers{&openapi3.Server{URL: "http://wiretap.invalid"}}
			}
			identity, err := json.Marshal(servers)
			if err != nil {
				return nil, err
			}
			group := groups[string(identity)]
			if group == nil {
				copy := *doc
				copy.Servers, copy.Paths = servers, openapi3.NewPaths()
				group = &copy
				groups[string(identity)] = group
			}
			pathItem := group.Paths.Value(path)
			if pathItem == nil {
				pathItem = &openapi3.PathItem{Parameters: item.Parameters}
				group.Paths.Set(path, pathItem)
			}
			copy := *operation
			copy.Servers = nil
			pathItem.SetOperation(method, &copy)
		}
	}
	router := &sourceRouter{}
	for _, key := range sortedKeys(groups) {
		group, err := legacy.NewRouter(groups[key])
		if err != nil {
			return nil, err
		}
		router.groups = append(router.groups, group)
	}
	for path, data := range members {
		files[path] = data
	}
	return &sourceSpec{name: name, root: root, key: "frozen:" + ref.Source.Path, doc: doc, router: router}, nil
}

func localServers(servers openapi3.Servers) (openapi3.Servers, error) {
	var result openapi3.Servers
	for _, server := range servers {
		copy := *server
		address := copy.URL
		for name, variable := range copy.Variables {
			address = strings.ReplaceAll(address, "{"+name+"}", variable.Default)
		}
		parsed, err := url.Parse(address)
		if err != nil {
			return nil, err
		}
		copy.URL = "http://wiretap.invalid" + parsed.EscapedPath()
		copy.Variables = nil
		result = append(result, &copy)
	}
	return result, nil
}

func reservedHeader(name string) bool {
	return strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Accept") || strings.EqualFold(name, "Content-Type")
}

func observedStep(source *sourceSpec, flow artifact.Flow, u *url.URL, step *Step, input func(any) string) error {
	paths := source.observed["paths"].(map[string]any)
	path := u.EscapedPath()
	if strings.ContainsAny(path, "{}") {
		return errors.New("observed path contains OpenAPI template delimiters")
	}
	item, ok := paths[path].(map[string]any)
	if !ok {
		item = map[string]any{}
		paths[path] = item
	}
	method := strings.ToLower(flow.Request.HTTP.Method)
	switch method {
	case "get", "post", "put", "patch", "delete", "head", "options", "trace":
	default:
		return fmt.Errorf("unsupported REST method %q", method)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		op = map[string]any{"responses": map[string]any{}}
		item[method] = op
	}
	responses := op["responses"].(map[string]any)
	responses[strconv.Itoa(flow.Response.HTTP.Status)] = map[string]any{"description": "Captured response"}
	params := map[string]any{}
	if existing, ok := op["parameters"].([]any); ok {
		for _, entry := range existing {
			p := entry.(map[string]any)
			params[p["in"].(string)+":"+p["name"].(string)] = p
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(query) {
		values := query[name]
		if len(values) != 1 {
			return fmt.Errorf("repeated query parameter %q is unsupported", name)
		}
		params["query:"+name] = map[string]any{"name": name, "in": "query", "schema": map[string]any{"type": "string"}}
		step.Parameters = append(step.Parameters, Parameter{Name: name, In: "query", Value: input(values[0])})
	}
	for _, name := range sortedKeys(flow.Request.HTTP.Headers) {
		if omitHeader(name, flow.Request.HTTP) {
			continue
		}
		values := headerValues(flow.Request.HTTP.Headers, name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 {
			return fmt.Errorf("repeated header %q is unsupported", name)
		}
		name = http.CanonicalHeaderKey(name)
		params["header:"+name] = map[string]any{"name": name, "in": "header", "schema": map[string]any{"type": "string"}}
		step.Parameters = append(step.Parameters, Parameter{Name: name, In: "header", Value: input(values[0])})
	}
	ordered := []any{}
	for _, key := range sortedKeys(params) {
		ordered = append(ordered, params[key])
	}
	op["parameters"] = ordered
	if bodyPresent(flow.Request.HTTP.Body) {
		body, ok := op["requestBody"].(map[string]any)
		if !ok {
			body = map[string]any{"content": map[string]any{}}
			op["requestBody"] = body
		}
		body["content"].(map[string]any)[contentType(flow.Request.HTTP.Headers)] = map[string]any{"schema": map[string]any{}}
	}
	step.OperationPath = operationPath(source.name, path, method)
	return nil
}

func operationPath(source, path, method string) string {
	pointer := strings.NewReplacer("~", "~0", "/", "~1").Replace(path)
	fragment := &url.URL{Fragment: "/paths/" + pointer + "/" + strings.ToLower(method)}
	return "{$sourceDescriptions." + source + ".url}#" + fragment.EscapedFragment()
}

func omitHeader(name string, req *artifact.HttpMessage) bool {
	if reservedHeader(name) {
		return true
	}
	lower := strings.ToLower(name)
	switch lower {
	case "host", "content-length", "transfer-encoding", "connection", "proxy-connection", "trailer", "te", "upgrade", "content-type":
		return true
	case "content-encoding":
		if req.Body != nil && req.Body.ContentEncoding != "" {
			return true
		}
	}
	for _, value := range headerValues(req.Headers, "Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), name) {
				return true
			}
		}
	}
	return false
}
func operationJSON(op *openapi3.Operation) bool {
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		for media := range op.RequestBody.Value.Content {
			if jsonMedia(media) {
				return true
			}
		}
	}
	if op.Responses != nil {
		for _, ref := range op.Responses.Map() {
			if ref.Value != nil {
				for media := range ref.Value.Content {
					if jsonMedia(media) {
						return true
					}
				}
			}
		}
	}
	return false
}
func headerValues(headers artifact.Headers, name string) []string {
	var result []string
	for _, key := range sortedKeys(headers) {
		if strings.EqualFold(key, name) {
			result = append(result, headers[key]...)
		}
	}
	return result
}
func contentType(headers artifact.Headers) string {
	values := headerValues(headers, "Content-Type")
	if len(values) == 0 {
		return ""
	}
	media, _, err := mime.ParseMediaType(values[0])
	if err != nil {
		return values[0]
	}
	return strings.ToLower(media)
}
func jsonMedia(media string) bool {
	return media == "application/json" || (strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))
}
func bodyPresent(body *artifact.Body) bool {
	return body != nil && (body.Text != "" || body.Bytes != "" || body.OriginalBytes > 0 || body.Omitted || body.Truncated || body.Redacted)
}
func jsonBody(body *artifact.Body) (any, error) {
	raw := []byte(body.Text)
	if body.Bytes != "" {
		var err error
		raw, err = base64.StdEncoding.DecodeString(body.Bytes)
		if err != nil {
			return nil, err
		}
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("captured JSON body contains invalid UTF-8")
	}
	// Validate syntax and the standard decoder's nesting limit before walking
	// tokens, which also guards the recursive duplicate-member check below.
	if !json.Valid(raw) {
		return nil, errors.New("invalid captured JSON body: expected exactly one JSON value")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	value, err := jsonValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("invalid captured JSON body: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("captured body must contain exactly one JSON value")
	}
	return value, nil
}

// JSON objects with duplicate names have no lossless map representation. Refuse
// those captures instead of silently replacing the earlier member's value.
func jsonValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("JSON object key must be a string")
			}
			if _, exists := object[name]; exists {
				return nil, fmt.Errorf("duplicate JSON object key %q", name)
			}
			value, err := jsonValue(decoder)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("JSON object is not closed")
		}
		return object, nil
	case '[':
		array := []any{}
		for decoder.More() {
			value, err := jsonValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("JSON array is not closed")
		}
		return array, nil
	default:
		return nil, errors.New("unexpected JSON closing delimiter")
	}
}
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func Write(w io.Writer, doc *Document) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(doc)
}
