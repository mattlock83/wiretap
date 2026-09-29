package bundlereport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/bundle"
	"gopkg.in/yaml.v3"
)

// Mock drafts are disposable observations, never validation inputs or evidence.
// Group by dependency identity, not host/port, which can change between tests.
type mockDraft struct {
	AssetID, Path         string
	Operations, Exchanges int
}

type mockEvidence struct {
	AttemptID         string `yaml:"attempt_id"`
	FlowID            string `yaml:"flow_id"`
	Path              string `yaml:"path"`
	TestStatus        string `yaml:"test_status"`
	CaptureIncomplete bool   `yaml:"capture_incomplete,omitempty"`
	ExcludedReason    string `yaml:"excluded_reason,omitempty"`
}

type mockOperation struct {
	pathParams []string
	query      map[string]bool
	request    map[string]*mockBody
	responses  map[string]map[string]*mockBody
	evidence   []mockEvidence
}

type mockBody struct {
	unknown, binary bool
	shape           mockShape
}

type mockShape struct {
	types      map[string]bool
	properties map[string]*mockShape
	items      *mockShape
}

func mockDraftPath(assetID string) string {
	return "dependencies/mocks/" + shortHash(assetID) + ".openapi.yaml"
}

func mockIDs(run *bundle.Run) []string {
	var ids []string
	for id, kind := range run.DependencyKinds {
		if kind == "mock" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func writeMockDrafts(root string, run *bundle.Run, captures []*artifact.Artifact) ([]mockDraft, error) {
	var drafts []mockDraft
	for _, id := range mockIDs(run) {
		doc, draft := observedMockAPI(id, run, captures)
		data, err := yaml.Marshal(doc)
		if err != nil {
			return nil, err
		}
		if err := writePage(filepath.Join(root, filepath.FromSlash(draft.Path)), data); err != nil {
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, nil
}

func observedMockAPI(assetID string, run *bundle.Run, captures []*artifact.Artifact) (map[string]any, mockDraft) {
	draft := mockDraft{AssetID: assetID, Path: mockDraftPath(assetID)}
	attemptPaths := map[string]string{}
	for _, ref := range run.Attempts {
		attemptPaths[ref.AttemptID] = "../../" + ref.Path
	}
	operations := map[string]*mockOperation{}
	evidence := []mockEvidence{}
	for _, capture := range captures {
		for _, flow := range capture.Traffic {
			if flow.Channel != "sut_to_dependency" || flow.AssetID != assetID {
				continue
			}
			ref := mockEvidence{AttemptID: capture.TestCase.ID, FlowID: flow.ID, Path: attemptPaths[capture.TestCase.ID], TestStatus: capture.TestCase.Status, CaptureIncomplete: capture.CaptureIncomplete}
			req, res := flow.Request.HTTP, flow.Response.HTTP
			switch {
			case flow.Protocol != "http" || req == nil || res == nil:
				ref.ExcludedReason = "no complete HTTP exchange"
			case flow.TransportError != "":
				ref.ExcludedReason = "transport error"
			case res.Status < 100 || res.Status > 599:
				ref.ExcludedReason = "no valid HTTP response status"
			}
			if ref.ExcludedReason != "" {
				evidence = append(evidence, ref)
				continue
			}
			method := strings.ToLower(req.Method)
			parsed, err := url.Parse(req.URL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || !mockHTTPMethod(method) {
				ref.ExcludedReason = "unsupported HTTP method or URL"
				evidence = append(evidence, ref)
				continue
			}
			path, params := mockPath(parsed.EscapedPath())
			key := method + " " + path
			op := operations[key]
			if op == nil {
				op = &mockOperation{pathParams: params, query: map[string]bool{}, request: map[string]*mockBody{}, responses: map[string]map[string]*mockBody{}}
				operations[key] = op
			}
			for name, values := range parsed.Query() {
				if name == "" {
					continue
				}
				op.query[name] = op.query[name] || len(values) > 1
			}
			observeMockBody(op.request, req)
			status := strconv.Itoa(res.Status)
			if op.responses[status] == nil {
				op.responses[status] = map[string]*mockBody{}
			}
			observeMockBody(op.responses[status], res)
			op.evidence = append(op.evidence, ref)
			evidence = append(evidence, ref)
			draft.Exchanges++
		}
	}
	paths := map[string]any{}
	for key, observation := range operations {
		parts := strings.SplitN(key, " ", 2)
		method, path := parts[0], parts[1]
		item, ok := paths[path].(map[string]any)
		if !ok {
			item = map[string]any{}
			paths[path] = item
		}
		item[method] = observation.document()
	}
	draft.Operations = len(operations)
	sortMockEvidence(evidence)
	description := "Observed mock requests and responses, including failed test attempts. This draft is not a provider contract and is not used for validation or coverage. Fields are observed shapes, not proven consumer requirements. Additional properties remain allowed. Numeric and UUID path segments are inferred parameters; other segments remain literal. Payload values are not emitted as examples. Redaction and capture limits can affect inferred types."
	if draft.Exchanges == 0 {
		description += " No complete HTTP exchanges have been captured for this mock."
	}
	return map[string]any{
		"openapi":  "3.1.0",
		"info":     map[string]any{"title": assetID + " — inferred mock assumptions", "version": "0.1.0", "description": description},
		"paths":    paths,
		"x-phrond": map[string]any{"kind": "inferred_mock_assumptions", "api_id": assetID, "run_id": run.RunID, "snapshot_revision": run.Revision, "inventory": "../../evidence/run.json", "used_for_validation": false, "observed_exchanges": draft.Exchanges, "excluded_exchanges": len(evidence) - draft.Exchanges, "evidence": evidence},
	}, draft
}

func sortMockEvidence(refs []mockEvidence) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].AttemptID != refs[j].AttemptID {
			return refs[i].AttemptID < refs[j].AttemptID
		}
		return refs[i].FlowID < refs[j].FlowID
	})
}

func (o *mockOperation) document() map[string]any {
	sortMockEvidence(o.evidence)
	doc := map[string]any{"responses": map[string]any{}, "x-phrond-evidence": o.evidence}
	parameters := []map[string]any{}
	for _, name := range o.pathParams {
		parameters = append(parameters, map[string]any{"name": name, "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
	}
	names := make([]string, 0, len(o.query))
	for name := range o.query {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		schema := map[string]any{"type": "string"}
		if o.query[name] {
			schema = map[string]any{"type": "array", "items": schema}
		}
		parameters = append(parameters, map[string]any{"name": name, "in": "query", "schema": schema})
	}
	if len(parameters) > 0 {
		doc["parameters"] = parameters
	}
	if len(o.request) > 0 {
		doc["requestBody"] = map[string]any{"content": mockContent(o.request)}
	}
	responses := doc["responses"].(map[string]any)
	for status, content := range o.responses {
		response := map[string]any{"description": "Observed mock response"}
		if len(content) > 0 {
			response["content"] = mockContent(content)
		}
		responses[status] = response
	}
	return doc
}

var mockDynamicSegment = regexp.MustCompile(`(?i)^([0-9]+|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

func mockPath(path string) (string, []string) {
	if path == "" {
		return "/", nil
	}
	segments := strings.Split(path, "/")
	var params []string
	for i, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err == nil && mockDynamicSegment.MatchString(decoded) {
			name := fmt.Sprintf("param%d", i)
			segments[i] = "{" + name + "}"
			params = append(params, name)
		}
	}
	return strings.Join(segments, "/"), params
}

func mockHTTPMethod(method string) bool {
	switch method {
	case "get", "post", "put", "patch", "delete", "head", "options", "trace":
		return true
	}
	return false
}

func observeMockBody(content map[string]*mockBody, msg *artifact.HttpMessage) {
	body := msg.Body
	if body == nil || (body.Text == "" && body.Bytes == "" && body.Decoded == nil && body.OriginalBytes == 0 && !body.Truncated) {
		return
	}
	var raw []byte
	binary := false
	switch {
	case body.Decoded != nil:
		raw = []byte(body.Decoded.Text)
	case body.Bytes != "":
		raw, _ = base64.StdEncoding.DecodeString(body.Bytes)
		binary = true
	default:
		raw = []byte(body.Text)
	}
	media := ""
	for name, values := range msg.Headers {
		if strings.EqualFold(name, "Content-Type") && len(values) > 0 {
			media = strings.ToLower(strings.TrimSpace(strings.SplitN(values[0], ";", 2)[0]))
			break
		}
	}
	if media != "" {
		if _, _, err := mime.ParseMediaType(media); err != nil || !strings.Contains(media, "/") {
			media = ""
		}
	}
	if media == "" {
		switch {
		case json.Valid(raw):
			media = "application/json"
		case binary:
			media = "application/octet-stream"
		default:
			media = "text/plain"
		}
	}
	observed := content[media]
	if observed == nil {
		observed = &mockBody{}
		content[media] = observed
	}
	if body.Truncated {
		observed.unknown = true
		return
	}
	if media == "application/json" || strings.HasSuffix(media, "+json") {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value, trailing any
		if decoder.Decode(&value) != nil || decoder.Decode(&trailing) != io.EOF {
			observed.unknown = true
			return
		}
		observed.shape.observe(value)
	} else if binary || media == "application/octet-stream" {
		observed.binary = true
	} else {
		observed.shape.observe(string(raw))
	}
}

func mockContent(observed map[string]*mockBody) map[string]any {
	content := map[string]any{}
	for media, body := range observed {
		schema := map[string]any{}
		switch {
		case body.unknown:
		case body.binary:
			schema = map[string]any{"type": "string", "format": "binary"}
		default:
			schema = body.shape.schema()
		}
		content[media] = map[string]any{"schema": schema}
	}
	return content
}

func (s *mockShape) observe(value any) {
	if s.types == nil {
		s.types = map[string]bool{}
	}
	switch v := value.(type) {
	case nil:
		s.types["null"] = true
	case bool:
		s.types["boolean"] = true
	case string:
		s.types["string"] = true
	case json.Number:
		if _, err := v.Int64(); err == nil {
			s.types["integer"] = true
		} else {
			s.types["number"] = true
		}
	case []any:
		s.types["array"] = true
		if s.items == nil {
			s.items = &mockShape{}
		}
		for _, item := range v {
			s.items.observe(item)
		}
	case map[string]any:
		s.types["object"] = true
		if s.properties == nil {
			s.properties = map[string]*mockShape{}
		}
		for name, value := range v {
			if s.properties[name] == nil {
				s.properties[name] = &mockShape{}
			}
			s.properties[name].observe(value)
		}
	}
}

func (s *mockShape) schema() map[string]any {
	schema := map[string]any{}
	var types []string
	for typ := range s.types {
		if typ == "null" || (typ == "integer" && s.types["number"]) {
			continue
		}
		types = append(types, typ)
	}
	sort.Strings(types)
	if len(types) == 1 {
		schema["type"] = types[0]
	} else if len(types) > 1 {
		schema["type"] = types
	}
	if s.types["object"] {
		properties := map[string]any{}
		for name, property := range s.properties {
			properties[name] = property.schema()
		}
		schema["properties"] = properties
	}
	if s.types["array"] {
		schema["items"] = s.items.schema()
	}
	if s.types["null"] {
		null := map[string]any{"enum": []any{nil}}
		if len(types) == 0 {
			return null
		}
		return map[string]any{"anyOf": []any{schema, null}}
	}
	return schema
}
