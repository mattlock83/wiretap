package coverage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/phrond/evermock/internal/artifact"
)

const (
	postmanCollectionSchema = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
	postmanMetaPrefix       = "phrond-meta:"
)

// PostmanCollection is a minimal Postman v2.1 collection model used for coverage export.
type PostmanCollection struct {
	Info     PostmanInfo       `json:"info"`
	Item     []PostmanItem     `json:"item,omitempty"`
	Variable []PostmanVariable `json:"variable,omitempty"`
}

// PostmanInfo stores collection-level metadata.
type PostmanInfo struct {
	Name        string `json:"name"`
	Schema      string `json:"schema"`
	Description string `json:"description,omitempty"`
}

// PostmanItem represents either a folder (Item set) or request (Request set).
type PostmanItem struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Item        []PostmanItem     `json:"item,omitempty"`
	Request     *PostmanRequest   `json:"request,omitempty"`
	Response    []PostmanResponse `json:"response,omitempty"`
}

// PostmanRequest is a request shape for captured samples.
type PostmanRequest struct {
	Method string          `json:"method,omitempty"`
	Header []PostmanHeader `json:"header,omitempty"`
	URL    PostmanURL      `json:"url,omitempty"`
	Body   *PostmanBody    `json:"body,omitempty"`
}

// PostmanURL stores the raw URL string.
type PostmanURL struct {
	Raw      string              `json:"raw,omitempty"`
	Protocol string              `json:"protocol,omitempty"`
	Host     []string            `json:"host,omitempty"`
	Path     []string            `json:"path,omitempty"`
	Query    []PostmanQueryParam `json:"query,omitempty"`
}

// PostmanQueryParam stores one query key/value pair.
type PostmanQueryParam struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// UnmarshalJSON supports both string and object URL shapes.
func (u *PostmanURL) UnmarshalJSON(data []byte) error {
	if u == nil {
		return nil
	}
	if len(data) == 0 || string(data) == "null" {
		u.Raw = ""
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		u.Raw = asString
		return nil
	}
	var asObject struct {
		Raw      string              `json:"raw"`
		Protocol string              `json:"protocol"`
		Host     []string            `json:"host"`
		Path     []string            `json:"path"`
		Query    []PostmanQueryParam `json:"query"`
	}
	if err := json.Unmarshal(data, &asObject); err == nil {
		u.Raw = asObject.Raw
		u.Protocol = asObject.Protocol
		u.Host = asObject.Host
		u.Path = asObject.Path
		u.Query = asObject.Query
		return nil
	}
	return errors.New("unsupported postman url shape")
}

// PostmanBody stores a raw body payload.
type PostmanBody struct {
	Mode string `json:"mode,omitempty"`
	Raw  string `json:"raw,omitempty"`
}

// PostmanHeader stores one header key/value pair.
type PostmanHeader struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// PostmanResponse stores one captured response sample.
type PostmanResponse struct {
	Name            string          `json:"name,omitempty"`
	OriginalRequest *PostmanRequest `json:"originalRequest,omitempty"`
	Status          string          `json:"status,omitempty"`
	Code            int             `json:"code,omitempty"`
	Header          []PostmanHeader `json:"header,omitempty"`
	Body            string          `json:"body,omitempty"`
}

// PostmanVariable defines a collection-level variable.
type PostmanVariable struct {
	Key         string `json:"key"`
	Value       string `json:"value,omitempty"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

type postmanCollectionMeta struct {
	Kind          string    `json:"kind"`
	GeneratedAt   time.Time `json:"generated_at,omitempty"`
	SUTName       string    `json:"sut_name,omitempty"`
	ArtifactCount int       `json:"artifact_count,omitempty"`
	Artifacts     []string  `json:"artifacts,omitempty"`
	Notes         []string  `json:"notes,omitempty"`
}

type postmanTestCaseMeta struct {
	Kind     string              `json:"kind"`
	Attempts []artifact.TestCase `json:"attempts,omitempty"`
}

type postmanSpecMeta struct {
	Kind      string                `json:"kind"`
	SchemaID  string                `json:"schema_id"`
	Role      string                `json:"role"`
	Type      string                `json:"type"`
	Source    artifact.SchemaSource `json:"source,omitempty"`
	AssetID   string                `json:"asset_id,omitempty"`
	AssetName string                `json:"asset_name,omitempty"`
	AssetSHA  string                `json:"asset_sha,omitempty"`
}

type postmanSampleMeta struct {
	RequestHeaders     artifact.Headers `json:"request_headers,omitempty"`
	RequestSpanID      string           `json:"request_span_id,omitempty" yaml:"request_span_id,omitempty"`
	TraceContextSource string           `json:"trace_context_source,omitempty" yaml:"trace_context_source,omitempty"`
	TestCorrelation    string           `json:"test_correlation,omitempty" yaml:"test_correlation,omitempty"`
	ParentCorrelation  string           `json:"parent_correlation,omitempty" yaml:"parent_correlation,omitempty"`
	TestCaseID         string           `json:"testcase_id,omitempty" yaml:"testcase_id,omitempty"`
	TestStatus         string           `json:"test_status,omitempty"`
	TestTraceID        string           `json:"test_trace_id,omitempty" yaml:"test_trace_id,omitempty"`
	RootSpanID         string           `json:"root_span_id,omitempty" yaml:"root_span_id,omitempty"`

	Kind         string `json:"kind"`
	TestCase     string `json:"testcase,omitempty"`
	FlowID       string `json:"flow_id,omitempty"`
	Protocol     string `json:"protocol,omitempty"`
	Channel      string `json:"channel,omitempty"`
	AssetID      string `json:"asset_id,omitempty"`
	ParentFlowID string `json:"parent_flow_id,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
	Operation    string `json:"operation,omitempty"`
	SchemaID     string `json:"schema_id,omitempty"`
	Role         string `json:"role,omitempty"`
	RequestURL   string `json:"request_url,omitempty"`
}

// CollectionFromReport converts a coverage report into a Postman collection.
func CollectionFromReport(report *Report) (*PostmanCollection, error) {
	if report == nil {
		return nil, errors.New("report is nil")
	}
	name := strings.TrimSpace(report.SUTName)
	if name == "" {
		name = "SUT"
	}
	collection := &PostmanCollection{
		Info: PostmanInfo{
			Name:   fmt.Sprintf("Phrond Coverage - %s", name),
			Schema: postmanCollectionSchema,
		},
	}
	collection.Info.Description = encodePostmanMeta(postmanCollectionMeta{
		Kind:          "coverage_collection",
		GeneratedAt:   report.GeneratedAt,
		SUTName:       name,
		ArtifactCount: report.ArtifactCount,
		Artifacts:     report.Artifacts,
		Notes:         report.Notes,
	})

	specLookup := map[string]SpecCoverage{}
	for _, spec := range report.Provided.Specs {
		specLookup[specLookupKey("sut", spec.SchemaID)] = spec
	}
	for _, spec := range report.Dependent.Specs {
		specLookup[specLookupKey("dependency", spec.SchemaID)] = spec
	}
	specFolderNames := buildSpecFolderNames(report)
	specVariables := buildSpecVariables(report, specFolderNames)
	collection.Variable = sortPostmanVariables(specVariables)

	for _, tc := range report.TestCases {
		tcName := strings.TrimSpace(tc.Name)
		if tcName == "" {
			tcName = "unnamed-test"
		}

		testFolder := PostmanItem{Name: tcName, Description: encodePostmanMeta(postmanTestCaseMeta{Kind: "testcase", Attempts: tc.Attempts})}
		testTraffic := PostmanItem{Name: "test traffic"}
		appendPostmanSpecFolders(&testTraffic, "sut", tc.Provided, specLookup, specFolderNames, specVariables)
		if len(testTraffic.Item) > 0 {
			testFolder.Item = append(testFolder.Item, testTraffic)
		}
		dependencyTraffic := PostmanItem{Name: "dependency traffic"}
		appendPostmanSpecFolders(&dependencyTraffic, "dependency", tc.Dependent, specLookup, specFolderNames, specVariables)
		if len(dependencyTraffic.Item) > 0 {
			testFolder.Item = append(testFolder.Item, dependencyTraffic)
		}

		if len(testFolder.Item) == 0 && len(tc.Attempts) == 0 {
			continue
		}
		collection.Item = append(collection.Item, testFolder)
	}
	return collection, nil
}

// WritePostmanCollection writes an indented Postman v2.1 collection JSON payload.
func WritePostmanCollection(w io.Writer, report *Report) error {
	collection, err := CollectionFromReport(report)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(collection)
}

func appendPostmanSpecFolders(
	parent *PostmanItem,
	role string,
	specs []SpecOperationSamples,
	lookup map[string]SpecCoverage,
	folderNames map[string]string,
	variables map[string]PostmanVariable,
) {
	if parent == nil {
		return
	}
	for _, spec := range specs {
		lookupKey := specLookupKey(role, spec.SchemaID)
		lookupSpec, hasLookup := lookup[lookupKey]
		sourceLabel := strings.TrimSpace(spec.Source)
		if sourceLabel == "" && hasLookup {
			sourceLabel = strings.TrimSpace(lookupSpec.Source)
		}
		typeLabel := strings.TrimSpace(spec.Type)
		if typeLabel == "" && hasLookup {
			typeLabel = strings.TrimSpace(lookupSpec.Type)
		}
		if typeLabel == "" {
			typeLabel = "openapi"
		}
		folderName := strings.TrimSpace(folderNames[lookupKey])
		if folderName == "" || strings.EqualFold(folderName, "unknown") {
			folderName = spec.SchemaID
		}
		variable := variables[lookupKey]

		specMeta := postmanSpecMeta{
			Kind:     "spec",
			SchemaID: spec.SchemaID,
			Role:     role,
			Type:     typeLabel,
			Source:   inferSchemaSource(sourceLabel),
			AssetID:  spec.AssetID,
		}
		if hasLookup {
			specMeta.AssetName = lookupSpec.AssetName
			specMeta.AssetSHA = lookupSpec.AssetSHA
		}
		specFolder := PostmanItem{
			Name:        folderName,
			Description: encodePostmanMeta(specMeta),
		}

		for _, op := range spec.Operations {
			for _, sample := range op.Samples {
				req := sampleToPostmanRequest(sample.Request, variable.Key)
				item := PostmanItem{
					Name:     sampleItemName(op.Operation, sample.FlowID),
					Request:  req,
					Response: sampleToPostmanResponses(sample.Response, req),
					Description: encodePostmanMeta(postmanSampleMeta{
						Kind:               "sample",
						TestCase:           sample.TestCase,
						FlowID:             sample.FlowID,
						Protocol:           sample.Protocol,
						Channel:            sample.Channel,
						AssetID:            sample.AssetID,
						ParentFlowID:       sample.ParentFlowID,
						TraceID:            sample.TraceID,
						RequestSpanID:      sample.RequestSpanID,
						TraceContextSource: sample.TraceContextSource,
						TestCorrelation:    sample.TestCorrelation,
						ParentCorrelation:  sample.ParentCorrelation,
						TestCaseID:         sample.TestCaseID,
						TestStatus:         sample.TestStatus,
						TestTraceID:        sample.TestTraceID,
						RootSpanID:         sample.RootSpanID,

						Operation:      op.Operation,
						SchemaID:       spec.SchemaID,
						Role:           role,
						RequestURL:     strings.TrimSpace(sample.Request.URL),
						RequestHeaders: sample.Request.Headers,
					}),
				}
				specFolder.Item = append(specFolder.Item, item)
			}
		}

		sort.Slice(specFolder.Item, func(i, j int) bool {
			return specFolder.Item[i].Name < specFolder.Item[j].Name
		})
		parent.Item = append(parent.Item, specFolder)
	}
	sort.Slice(parent.Item, func(i, j int) bool {
		return parent.Item[i].Name < parent.Item[j].Name
	})
}

func sampleItemName(operation, flowID string) string {
	op := strings.TrimSpace(operation)
	id := strings.TrimSpace(flowID)
	if op == "" {
		op = "captured-flow"
	}
	if id == "" {
		return op
	}
	return fmt.Sprintf("%s [%s]", op, id)
}

func sampleToPostmanRequest(msg InteractionMessage, variableKey string) *PostmanRequest {
	postmanURL := buildPostmanURL(msg.URL, variableKey)
	req := &PostmanRequest{
		Method: strings.TrimSpace(msg.Method),
		Header: headersToPostman(replayHeaders(msg.Headers)),
		URL:    postmanURL,
	}
	if body := prettyJSONBody(msg.Body); body != "" {
		req.Body = &PostmanBody{
			Mode: "raw",
			Raw:  body,
		}
	}
	return req
}

func sampleToPostmanResponses(msg InteractionMessage, req *PostmanRequest) []PostmanResponse {
	if msg.Status == 0 && len(msg.Headers) == 0 && strings.TrimSpace(msg.Body) == "" {
		return nil
	}
	status := http.StatusText(msg.Status)
	if status == "" && msg.Status > 0 {
		status = fmt.Sprintf("%d", msg.Status)
	}
	response := PostmanResponse{
		Name:            "captured response",
		OriginalRequest: req,
		Status:          status,
		Code:            msg.Status,
		Header:          headersToPostman(msg.Headers),
		Body:            prettyJSONBody(msg.Body),
	}
	return []PostmanResponse{response}
}

func headersToPostman(headers artifact.Headers) []PostmanHeader {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out []PostmanHeader
	for _, key := range keys {
		values := headers[key]
		if len(values) == 0 {
			out = append(out, PostmanHeader{Key: key, Value: ""})
			continue
		}
		for _, value := range values {
			out = append(out, PostmanHeader{Key: key, Value: value})
		}
	}
	return out
}

func headersFromPostman(headers []PostmanHeader) artifact.Headers {
	if len(headers) == 0 {
		return nil
	}
	out := artifact.Headers{}
	for _, header := range headers {
		key := strings.TrimSpace(header.Key)
		if key == "" {
			continue
		}
		out[key] = append(out[key], header.Value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func encodePostmanMeta(meta any) string {
	data, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return postmanMetaPrefix + string(data)
}

func decodePostmanMeta(raw string, target any) (bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, nil
	}
	if !strings.HasPrefix(trimmed, postmanMetaPrefix) {
		return false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, postmanMetaPrefix))
	if payload == "" {
		return true, errors.New("empty metadata payload")
	}
	if err := json.Unmarshal([]byte(payload), target); err != nil {
		return true, err
	}
	return true, nil
}

func specLookupKey(role, id string) string {
	return strings.ToLower(strings.TrimSpace(role)) + "|" + strings.TrimSpace(id)
}

func inferSchemaSource(label string) artifact.SchemaSource {
	value := strings.TrimSpace(label)
	if value == "" || strings.EqualFold(value, "unknown") {
		return artifact.SchemaSource{}
	}
	if strings.EqualFold(value, "inline") {
		return artifact.SchemaSource{Type: "inline"}
	}
	if strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://") {
		return artifact.SchemaSource{Type: "url", URL: value}
	}
	return artifact.SchemaSource{Type: "file", Path: value}
}

func buildSpecFolderNames(report *Report) map[string]string {
	out := map[string]string{}
	titleCache := map[string]string{}
	build := func(role string, specs []SpecCoverage) {
		for _, spec := range specs {
			key := specLookupKey(role, spec.SchemaID)
			if _, exists := out[key]; exists {
				continue
			}
			sourceLabel := strings.TrimSpace(spec.Source)
			fileName := schemaSourceBaseName(sourceLabel)

			title := titleCache[sourceLabel]
			if title == "" && sourceLabel != "" {
				title = strings.TrimSpace(loadOpenAPITitleFromSourceLabel(sourceLabel))
				if title != "" {
					titleCache[sourceLabel] = title
				}
			}

			switch {
			case fileName != "" && title != "":
				out[key] = fmt.Sprintf("%s (%s)", fileName, title)
			case fileName != "":
				out[key] = fileName
			case title != "":
				out[key] = title
			default:
				out[key] = spec.SchemaID
			}
		}
	}
	build("sut", report.Provided.Specs)
	build("dependency", report.Dependent.Specs)
	return out
}

func schemaSourceBaseName(sourceLabel string) string {
	value := strings.TrimSpace(sourceLabel)
	if value == "" || strings.EqualFold(value, "inline") || strings.EqualFold(value, "unknown") {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		base := filepath.Base(parsed.Path)
		if base != "" && base != "." && base != "/" {
			return base
		}
		return parsed.Host
	}
	base := filepath.Base(value)
	if base == "" || base == "." || base == "/" {
		return ""
	}
	return base
}

func loadOpenAPITitleFromSourceLabel(sourceLabel string) string {
	source := inferSchemaSource(sourceLabel)
	if strings.TrimSpace(source.Type) == "" {
		return ""
	}
	ref := artifact.SchemaRef{
		Type:   "openapi",
		Source: source,
	}
	spec, err := loadOpenAPISpec(ref)
	if err != nil || spec == nil || spec.Doc == nil || spec.Doc.Info == nil {
		return ""
	}
	return strings.TrimSpace(spec.Doc.Info.Title)
}

func buildSpecVariables(report *Report, folderNames map[string]string) map[string]PostmanVariable {
	out := map[string]PostmanVariable{}
	usedNames := map[string]int{}

	addSpecs := func(role string, specs []SpecCoverage) {
		for _, spec := range specs {
			lookupKey := specLookupKey(role, spec.SchemaID)
			if _, exists := out[lookupKey]; exists {
				continue
			}
			sourceLabel := strings.TrimSpace(spec.Source)
			displayName := strings.TrimSpace(folderNames[lookupKey])
			varName := nextVariableName(displayName, sourceLabel, spec.SchemaID, usedNames)
			baseURL := inferBaseURLForSpec(report.TestCases, role, spec.SchemaID)
			if baseURL == "" {
				baseURL = "http://localhost"
			}
			description := fmt.Sprintf("Base URL for %s", spec.SchemaID)
			if displayName != "" {
				description = fmt.Sprintf("Base URL for %s", displayName)
			}
			if sourceLabel != "" {
				description += fmt.Sprintf(" (%s)", sourceLabel)
			}
			out[lookupKey] = PostmanVariable{
				Key:         varName,
				Value:       baseURL,
				Type:        "string",
				Description: description,
			}
		}
	}

	addSpecs("sut", report.Provided.Specs)
	addSpecs("dependency", report.Dependent.Specs)
	return out
}

func nextVariableName(displayName, sourceLabel, schemaID string, used map[string]int) string {
	base := sanitizeVariableName(displayName)
	if base == "" {
		base = sanitizeVariableName(sourceLabel)
	}
	if base == "" {
		base = sanitizeVariableName(schemaID)
	}
	if base == "" {
		base = "spec"
	}
	name := base + "_base_url"
	if _, exists := used[name]; !exists {
		used[name] = 1
		return name
	}
	index := used[name]
	used[name] = index + 1
	return fmt.Sprintf("%s_%d", name, index+1)
}

func sanitizeVariableName(input string) string {
	raw := strings.ToLower(strings.TrimSpace(input))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	return name
}

func sortPostmanVariables(vars map[string]PostmanVariable) []PostmanVariable {
	if len(vars) == 0 {
		return nil
	}
	out := make([]PostmanVariable, 0, len(vars))
	for _, variable := range vars {
		out = append(out, variable)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func inferBaseURLForSpec(testcases []TestCaseCoverage, role, schemaID string) string {
	for _, tc := range testcases {
		var specs []SpecOperationSamples
		if strings.EqualFold(role, "dependency") || strings.EqualFold(role, "fake") {
			specs = tc.Dependent
		} else {
			specs = tc.Provided
		}
		for _, spec := range specs {
			if spec.SchemaID != schemaID {
				continue
			}
			for _, op := range spec.Operations {
				for _, sample := range op.Samples {
					if base := extractBaseURL(sample.Request.URL); base != "" {
						return base
					}
				}
			}
		}
	}
	return ""
}

func extractBaseURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func requestPathAndQuery(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err == nil {
		if parsed.Scheme != "" {
			path := parsed.EscapedPath()
			if path == "" {
				path = "/"
			}
			if parsed.RawQuery != "" {
				path += "?" + parsed.RawQuery
			}
			return path
		}
		if strings.HasPrefix(value, "/") {
			return value
		}
		if parsed.Path != "" {
			path := parsed.Path
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			if parsed.RawQuery != "" {
				path += "?" + parsed.RawQuery
			}
			return path
		}
	}
	if strings.HasPrefix(value, "/") {
		return value
	}
	return "/" + value
}

func buildPostmanURL(originalURL, variableKey string) PostmanURL {
	parsed, _ := parseURLLoose(originalURL)

	path := "/"
	if parsed != nil {
		path = parsed.EscapedPath()
		if path == "" {
			path = "/"
		}
	} else {
		path = requestPathAndQuery(originalURL)
		if path == "" {
			path = "/"
		}
	}

	raw := path
	if parsed != nil && parsed.RawQuery != "" {
		raw += "?" + parsed.RawQuery
	}

	urlOut := PostmanURL{
		Path: splitPostmanPath(path),
	}
	if parsed != nil {
		for key, values := range parsed.Query() {
			if len(values) == 0 {
				urlOut.Query = append(urlOut.Query, PostmanQueryParam{Key: key})
				continue
			}
			for _, value := range values {
				urlOut.Query = append(urlOut.Query, PostmanQueryParam{Key: key, Value: value})
			}
		}
		sort.Slice(urlOut.Query, func(i, j int) bool {
			if urlOut.Query[i].Key == urlOut.Query[j].Key {
				return urlOut.Query[i].Value < urlOut.Query[j].Value
			}
			return urlOut.Query[i].Key < urlOut.Query[j].Key
		})
	}

	if variableKey != "" {
		if !strings.HasPrefix(raw, "/") {
			raw = "/" + raw
		}
		urlOut.Raw = fmt.Sprintf("{{%s}}%s", variableKey, raw)
		urlOut.Host = []string{fmt.Sprintf("{{%s}}", variableKey)}
		return urlOut
	}

	urlOut.Raw = raw
	if parsed != nil {
		urlOut.Protocol = parsed.Scheme
		if parsed.Host != "" {
			urlOut.Host = splitPostmanHost(parsed.Host)
		}
	}
	return urlOut
}

func splitPostmanPath(path string) []string {
	value := strings.TrimSpace(path)
	if value == "" || value == "/" {
		return []string{}
	}
	value = strings.TrimPrefix(value, "/")
	parts := strings.Split(value, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func splitPostmanHost(host string) []string {
	value := strings.TrimSpace(host)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return []string{value}
	}
	return out
}

func parseURLLoose(raw string) (*url.URL, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, errors.New("empty url")
	}
	if strings.HasPrefix(value, "/") {
		parsed, err := url.Parse("http://placeholder.local" + value)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" {
		return parsed, nil
	}
	if err == nil && parsed.Path != "" {
		parsed2, err2 := url.Parse("http://placeholder.local/" + strings.TrimPrefix(parsed.Path, "/"))
		if err2 == nil {
			parsed2.RawQuery = parsed.RawQuery
			return parsed2, nil
		}
	}
	return nil, errors.New("unparseable url")
}

func prettyJSONBody(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return value
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return value
	}
	return string(data)
}

// ReportFromPostmanCollection rebuilds the internal report model by parsing the collection structure.
func ReportFromPostmanCollection(collection *PostmanCollection) (*Report, error) {
	if collection == nil {
		return nil, errors.New("collection is nil")
	}

	var meta postmanCollectionMeta
	var notes []string
	if ok, err := decodePostmanMeta(collection.Info.Description, &meta); err != nil && ok {
		notes = append(notes, fmt.Sprintf("collection metadata parse error: %v", err))
	}

	entries, parseNotes := postmanCollectionToArtifacts(collection)
	notes = append(notes, meta.Notes...)
	notes = append(notes, parseNotes...)

	sutName := strings.TrimSpace(meta.SUTName)
	if sutName == "" {
		sutName = inferSUTNameFromCollectionName(collection.Info.Name)
	}
	if sutName == "" {
		sutName = "SUT"
	}

	report := &Report{
		GeneratedAt:   time.Now().UTC(),
		SUTName:       sutName,
		ArtifactCount: len(entries),
		Provided:      SpecCoverageGroup{},
		Dependent:     SpecCoverageGroup{},
		TestCases:     nil,
		Notes:         uniqueSorted(notes),
	}
	if len(entries) > 0 {
		provided, dependent, testcases, coverageNotes := buildCoverage(entries)
		report.Provided = provided
		report.Dependent = dependent
		report.TestCases = testcases
		report.Notes = uniqueSorted(append(report.Notes, coverageNotes...))
	}
	if !meta.GeneratedAt.IsZero() {
		report.GeneratedAt = meta.GeneratedAt
	}
	if meta.ArtifactCount > 0 {
		report.ArtifactCount = meta.ArtifactCount
	}
	if len(meta.Artifacts) > 0 {
		report.Artifacts = append([]string{}, meta.Artifacts...)
	}
	return report, nil
}

// ReportFromPostmanJSON decodes a Postman collection payload and rebuilds the coverage report.
func ReportFromPostmanJSON(data []byte) (*Report, error) {
	if len(data) == 0 {
		return nil, errors.New("postman collection payload is empty")
	}
	var collection PostmanCollection
	if err := json.Unmarshal(data, &collection); err != nil {
		return nil, fmt.Errorf("decode postman collection: %w", err)
	}
	return ReportFromPostmanCollection(&collection)
}

func postmanCollectionToArtifacts(collection *PostmanCollection) ([]artifactEntry, []string) {
	var (
		entries []artifactEntry
		notes   []string
	)

	for idx, testFolder := range collection.Item {
		var testMeta postmanTestCaseMeta
		if parsed, err := decodePostmanMeta(testFolder.Description, &testMeta); err != nil && parsed {
			notes = append(notes, fmt.Sprintf("testcase metadata parse error in %q: %v", testFolder.Name, err))
		}
		if len(testFolder.Item) == 0 && len(testMeta.Attempts) == 0 {
			continue
		}
		testName := strings.TrimSpace(testFolder.Name)
		if testName == "" {
			testName = fmt.Sprintf("testcase-%d", idx+1)
		}
		// A folder groups a display name, but every retry keeps its own artifact
		// identity. Flow IDs are only unique within that attempt.
		documents := map[string]*artifact.Artifact{}
		var documentIDs []string
		ensureDocument := func(testCase artifact.TestCase) *artifact.Artifact {
			if testCase.ID == "" {
				testCase.ID = testName
			}
			if existing := documents[testCase.ID]; existing != nil {
				return existing
			}
			if testCase.Name == "" {
				testCase.Name = testName
			}
			doc := &artifact.Artifact{Version: 1, TestCase: testCase, Traffic: []artifact.Flow{}}
			documents[testCase.ID] = doc
			documentIDs = append(documentIDs, testCase.ID)
			return doc
		}
		for _, attempt := range testMeta.Attempts {
			ensureDocument(attempt)
		}
		var schemas []artifact.SchemaRef

		seenSchemas := map[string]struct{}{}
		flowCounter := 0
		for _, trafficFolder := range testFolder.Item {
			defaultChannel, defaultRole, ok := detectTrafficFolder(trafficFolder.Name)
			if !ok {
				continue
			}
			for _, specFolder := range trafficFolder.Item {
				schemaRef := artifact.SchemaRef{
					ID:   strings.TrimSpace(specFolder.Name),
					Role: defaultRole,
					Type: "openapi",
				}

				var specMeta postmanSpecMeta
				if parsed, err := decodePostmanMeta(specFolder.Description, &specMeta); err != nil && parsed {
					notes = append(notes, fmt.Sprintf("spec metadata parse error in %q/%q: %v", testName, specFolder.Name, err))
				} else if parsed {
					if id := strings.TrimSpace(specMeta.SchemaID); id != "" {
						schemaRef.ID = id
					}
					if role := strings.TrimSpace(specMeta.Role); role != "" {
						if role == "fake" {
							role = "dependency"
						}
						schemaRef.Role = role
					}
					schemaRef.AssetID = specMeta.AssetID
					schemaRef.AssetName = specMeta.AssetName
					schemaRef.AssetSHA = specMeta.AssetSHA
					if typ := strings.TrimSpace(specMeta.Type); typ != "" {
						schemaRef.Type = typ
					}
					schemaRef.Source = specMeta.Source
				}
				if schemaRef.ID == "" {
					notes = append(notes, fmt.Sprintf("skipping unnamed spec folder under testcase %q", testName))
					continue
				}

				key := schemaKey(schemaRef)
				if _, exists := seenSchemas[key]; !exists {
					schemas = append(schemas, schemaRef)
					seenSchemas[key] = struct{}{}
				}

				for _, sampleItem := range specFolder.Item {
					if sampleItem.Request == nil {
						continue
					}
					flowCounter++
					flowID := fmt.Sprintf("flow-%d", flowCounter)
					channel := defaultChannel
					protocol := "http"

					var sampleMeta postmanSampleMeta
					requestURL := ""
					if parsed, err := decodePostmanMeta(sampleItem.Description, &sampleMeta); err != nil && parsed {
						notes = append(notes, fmt.Sprintf("sample metadata parse error in testcase %q item %q: %v", testName, sampleItem.Name, err))
					} else if parsed {
						if id := strings.TrimSpace(sampleMeta.FlowID); id != "" {
							flowID = id
						}
						if ch := strings.TrimSpace(sampleMeta.Channel); ch != "" {
							if ch == "sut_to_mock" {
								ch = "sut_to_dependency"
							}
							channel = ch
						}
						if proto := strings.TrimSpace(sampleMeta.Protocol); proto != "" {
							protocol = proto
						}
						requestURL = strings.TrimSpace(sampleMeta.RequestURL)
					}

					flow := artifact.Flow{
						ID:                 flowID,
						Protocol:           protocol,
						Channel:            channel,
						AssetID:            sampleMeta.AssetID,
						ParentFlowID:       sampleMeta.ParentFlowID,
						TraceID:            sampleMeta.TraceID,
						RequestSpanID:      sampleMeta.RequestSpanID,
						TraceContextSource: sampleMeta.TraceContextSource,
						TestCorrelation:    sampleMeta.TestCorrelation,
						ParentCorrelation:  sampleMeta.ParentCorrelation,

						Request: artifact.Message{
							HTTP: postmanRequestToHTTP(sampleItem.Request, requestURL),
						},
						Response: artifact.Message{
							HTTP: postmanResponseToHTTP(sampleItem.Response),
						},
					}
					if sampleMeta.RequestHeaders != nil && flow.Request.HTTP != nil {
						flow.Request.HTTP.Headers = sampleMeta.RequestHeaders
					}
					caseID := sampleMeta.TestCaseID
					if caseID == "" && len(testMeta.Attempts) == 1 {
						caseID = testMeta.Attempts[0].ID
					}
					doc := ensureDocument(artifact.TestCase{
						ID: caseID, Name: testName, Status: sampleMeta.TestStatus,
						TraceID: sampleMeta.TestTraceID, RootSpanID: sampleMeta.RootSpanID,
					})
					doc.Traffic = append(doc.Traffic, flow)
				}
			}
		}
		if len(documentIDs) == 0 {
			ensureDocument(artifact.TestCase{ID: testName, Name: testName})
		}
		for _, id := range documentIDs {
			doc := documents[id]
			doc.Schemas = append([]artifact.SchemaRef(nil), schemas...)
			entries = append(entries, artifactEntry{
				Path: fmt.Sprintf("postman://%s/%s", testName, id),
				Doc:  doc,
			})
		}
	}
	return entries, notes
}

func detectTrafficFolder(name string) (channel string, role string, ok bool) {
	label := strings.ToLower(strings.TrimSpace(name))
	switch label {
	case "test traffic", "provided traffic":
		return "test_to_sut", "sut", true
	case "dependency traffic", "mock traffic", "dependent traffic", "fake traffic":
		return "sut_to_dependency", "dependency", true
	default:
		return "", "", false
	}
}

func postmanRequestToHTTP(req *PostmanRequest, explicitURL string) *artifact.HttpMessage {
	if req == nil {
		return &artifact.HttpMessage{}
	}
	rawURL := strings.TrimSpace(explicitURL)
	if rawURL == "" {
		rawURL = normalizePostmanRequestURL(strings.TrimSpace(req.URL.Raw))
	}
	msg := &artifact.HttpMessage{
		Method:  strings.TrimSpace(req.Method),
		URL:     rawURL,
		Headers: headersFromPostman(req.Header),
	}
	if req.Body != nil && strings.TrimSpace(req.Body.Raw) != "" {
		msg.Body = &artifact.Body{
			Encoding: "utf-8",
			Text:     req.Body.Raw,
		}
	}
	return msg
}

func normalizePostmanRequestURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "{{") {
		if idx := strings.Index(value, "}}"); idx >= 0 {
			rest := strings.TrimSpace(value[idx+2:])
			if rest == "" {
				return "/"
			}
			if !strings.HasPrefix(rest, "/") {
				return "/" + rest
			}
			return rest
		}
	}
	if strings.Contains(value, "{{") {
		if idx := strings.Index(value, "}}"); idx >= 0 {
			rest := strings.TrimSpace(value[idx+2:])
			if strings.HasPrefix(rest, "/") {
				return rest
			}
		}
	}
	return value
}

func postmanResponseToHTTP(responses []PostmanResponse) *artifact.HttpMessage {
	if len(responses) == 0 {
		return &artifact.HttpMessage{}
	}
	resp := responses[0]
	msg := &artifact.HttpMessage{
		Status:  resp.Code,
		Headers: headersFromPostman(resp.Header),
	}
	if strings.TrimSpace(resp.Body) != "" {
		msg.Body = &artifact.Body{
			Encoding: "utf-8",
			Text:     resp.Body,
		}
	}
	return msg
}

func inferSUTNameFromCollectionName(name string) string {
	value := strings.TrimSpace(name)
	if value == "" {
		return ""
	}
	const prefix = "Phrond Coverage - "
	if strings.HasPrefix(value, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	return value
}
