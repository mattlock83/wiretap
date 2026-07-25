package coverage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
	"gopkg.in/yaml.v3"
)

const (
	serverlessWorkflowDSLVersion = "1.0.3"
	serverlessWorkflowVersion    = "1.0.0"
)

// ServerlessWorkflow is the Open Workflow Specification document emitted for
// import into workflow-aware API clients.
type ServerlessWorkflow struct {
	Document WorkflowDocument `yaml:"document" json:"document"`
	Do       []WorkflowTask   `yaml:"do" json:"do"`
}

// WorkflowDocument identifies the generated workflow.
type WorkflowDocument struct {
	DSL       string                    `yaml:"dsl" json:"dsl"`
	Namespace string                    `yaml:"namespace" json:"namespace"`
	Name      string                    `yaml:"name" json:"name"`
	Version   string                    `yaml:"version" json:"version"`
	Title     string                    `yaml:"title,omitempty" json:"title,omitempty"`
	Summary   string                    `yaml:"summary,omitempty" json:"summary,omitempty"`
	Metadata  *workflowDocumentMetadata `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// WorkflowTask is a one-entry named task map, as required by the Open
// Workflow Specification task-list shape.
type WorkflowTask map[string]WorkflowTaskDefinition

// WorkflowTaskDefinition represents either a composite do task or an HTTP
// call task.
type WorkflowTaskDefinition struct {
	Do       []WorkflowTask         `yaml:"do,omitempty" json:"do,omitempty"`
	Call     string                 `yaml:"call,omitempty" json:"call,omitempty"`
	With     *WorkflowHTTPArguments `yaml:"with,omitempty" json:"with,omitempty"`
	Metadata *workflowTaskMetadata  `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// WorkflowHTTPArguments contains arguments accepted by a Serverless Workflow
// HTTP call task.
type WorkflowHTTPArguments struct {
	Method   string            `yaml:"method" json:"method"`
	Endpoint string            `yaml:"endpoint" json:"endpoint"`
	Headers  map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body     *any              `yaml:"body,omitempty" json:"body,omitempty"`
	Output   string            `yaml:"output,omitempty" json:"output,omitempty"`
}

type workflowDocumentMetadata struct {
	Phrond workflowDocumentPhrondMetadata `yaml:"phrond" json:"phrond"`
}

type workflowDocumentPhrondMetadata struct {
	Kind          string   `yaml:"kind" json:"kind"`
	GeneratedAt   string   `yaml:"generated_at,omitempty" json:"generated_at,omitempty"`
	ArtifactCount int      `yaml:"artifact_count,omitempty" json:"artifact_count,omitempty"`
	Artifacts     []string `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	Notes         []string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

type workflowTaskMetadata struct {
	Phrond workflowTaskPhrondMetadata `yaml:"phrond" json:"phrond"`
}

type workflowTaskPhrondMetadata struct {
	Kind                   string                    `yaml:"kind" json:"kind"`
	TestCase               string                    `yaml:"testcase,omitempty" json:"testcase,omitempty"`
	FlowID                 string                    `yaml:"flow_id,omitempty" json:"flow_id,omitempty"`
	Protocol               string                    `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	Channel                string                    `yaml:"channel,omitempty" json:"channel,omitempty"`
	Operation              string                    `yaml:"operation,omitempty" json:"operation,omitempty"`
	SchemaID               string                    `yaml:"schema_id,omitempty" json:"schema_id,omitempty"`
	Role                   string                    `yaml:"role,omitempty" json:"role,omitempty"`
	AssetID                string                    `yaml:"asset_id,omitempty" json:"asset_id,omitempty"`
	TraceID                string                    `yaml:"trace_id,omitempty" json:"trace_id,omitempty"`
	ParentFlowID           string                    `yaml:"parent_flow_id,omitempty" json:"parent_flow_id,omitempty"`
	Fingerprint            string                    `yaml:"fingerprint,omitempty" json:"fingerprint,omitempty"`
	ConsumedFields         []string                  `yaml:"consumed_fields,omitempty" json:"consumed_fields,omitempty"`
	MockStub               *artifact.MockStub        `yaml:"mock_stub,omitempty" json:"mock_stub,omitempty"`
	MockMode               string                    `yaml:"mock_mode,omitempty" json:"mock_mode,omitempty"`
	TransportError         string                    `yaml:"transport_error,omitempty" json:"transport_error,omitempty"`
	Unmatched              bool                      `yaml:"unmatched,omitempty" json:"unmatched,omitempty"`
	RequestURL             string                    `yaml:"request_url,omitempty" json:"request_url,omitempty"`
	RequestHeaders         artifact.Headers          `yaml:"request_headers,omitempty" json:"request_headers,omitempty"`
	RequestTrailers        artifact.Headers          `yaml:"request_trailers,omitempty" json:"request_trailers,omitempty"`
	RequestEncoding        string                    `yaml:"request_encoding,omitempty" json:"request_encoding,omitempty"`
	RequestContentEncoding string                    `yaml:"request_content_encoding,omitempty" json:"request_content_encoding,omitempty"`
	RequestTruncated       bool                      `yaml:"request_truncated,omitempty" json:"request_truncated,omitempty"`
	RequestOriginalBytes   int64                     `yaml:"request_original_bytes,omitempty" json:"request_original_bytes,omitempty"`
	CapturedResponse       *workflowCapturedResponse `yaml:"captured_response,omitempty" json:"captured_response,omitempty"`
}

type workflowCapturedResponse struct {
	Status          int              `yaml:"status,omitempty" json:"status,omitempty"`
	Headers         artifact.Headers `yaml:"headers,omitempty" json:"headers,omitempty"`
	Trailers        artifact.Headers `yaml:"trailers,omitempty" json:"trailers,omitempty"`
	Body            *any             `yaml:"body,omitempty" json:"body,omitempty"`
	Encoding        string           `yaml:"encoding,omitempty" json:"encoding,omitempty"`
	ContentEncoding string           `yaml:"content_encoding,omitempty" json:"content_encoding,omitempty"`
	Truncated       bool             `yaml:"truncated,omitempty" json:"truncated,omitempty"`
	OriginalBytes   int64            `yaml:"original_bytes,omitempty" json:"original_bytes,omitempty"`
}

// ServerlessWorkflowFromReport converts a coverage report into one
// deterministic workflow. Test cases, traffic channels, and schemas are
// represented as nested do tasks; captured interactions become HTTP call
// tasks.
func ServerlessWorkflowFromReport(report *Report) (*ServerlessWorkflow, error) {
	if report == nil {
		return nil, errors.New("report is nil")
	}

	sutName := strings.TrimSpace(report.SUTName)
	if sutName == "" {
		sutName = "SUT"
	}
	workflow := &ServerlessWorkflow{
		Document: WorkflowDocument{
			DSL:       serverlessWorkflowDSLVersion,
			Namespace: "phrond",
			Name:      workflowName(sutName, "captured-traffic"),
			Version:   serverlessWorkflowVersion,
			Title:     fmt.Sprintf("Phrond Capture - %s", sutName),
			Summary:   "Captured API traffic exported by Phrond for replay and inspection.",
			Metadata: &workflowDocumentMetadata{
				Phrond: workflowDocumentPhrondMetadata{
					Kind:          "captured-traffic",
					ArtifactCount: report.ArtifactCount,
					Artifacts:     append([]string(nil), report.Artifacts...),
					Notes:         append([]string(nil), report.Notes...),
				},
			},
		},
	}
	if !report.GeneratedAt.IsZero() {
		workflow.Document.Metadata.Phrond.GeneratedAt = report.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}

	folderNames := buildSpecFolderNames(report)
	baseURLs := buildSpecVariables(report, folderNames)
	testNames := newWorkflowNameAllocator()
	for _, testCase := range report.TestCases {
		var groups []WorkflowTask
		if len(testCase.Traffic) > 0 {
			if tasks := workflowCapturedTrafficTasks(testCase, "test_to_sut", "sut", baseURLs); len(tasks) > 0 {
				groups = append(groups, namedWorkflowTask("test-traffic", WorkflowTaskDefinition{Do: tasks}))
			}
			if tasks := workflowCapturedTrafficTasks(testCase, "sut_to_mock", "fake", baseURLs); len(tasks) > 0 {
				groups = append(groups, namedWorkflowTask("mock-traffic", WorkflowTaskDefinition{Do: tasks}))
			}
		} else {
			// Reports reconstructed from older Postman collections do not have
			// raw traffic, so retain the schema-sample projection as a
			// compatibility fallback.
			if tasks := workflowTrafficTasks(testCase.Provided, "sut", baseURLs); len(tasks) > 0 {
				groups = append(groups, namedWorkflowTask("test-traffic", WorkflowTaskDefinition{Do: tasks}))
			}
			if tasks := workflowTrafficTasks(testCase.Dependent, "fake", baseURLs); len(tasks) > 0 {
				groups = append(groups, namedWorkflowTask("mock-traffic", WorkflowTaskDefinition{Do: tasks}))
			}
		}
		if len(groups) == 0 {
			continue
		}

		testCaseName := strings.TrimSpace(testCase.Name)
		if testCaseName == "" {
			testCaseName = "unnamed-test"
		}
		taskName := testNames.next(testCaseName, "testcase")
		workflow.Do = append(workflow.Do, namedWorkflowTask(taskName, WorkflowTaskDefinition{
			Do: groups,
			Metadata: &workflowTaskMetadata{Phrond: workflowTaskPhrondMetadata{
				Kind:     "testcase",
				TestCase: testCaseName,
			}},
		}))
	}

	return workflow, nil
}

// WriteServerlessWorkflow writes deterministic Open Workflow Specification
// YAML for the supplied report.
func WriteServerlessWorkflow(w io.Writer, report *Report) error {
	workflow, err := ServerlessWorkflowFromReport(report)
	if err != nil {
		return err
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(workflow)
}

type workflowCallGroup struct {
	name  string
	calls []WorkflowTask
	names *workflowNameAllocator
}

func workflowCapturedTrafficTasks(
	testCase TestCaseCoverage,
	channel string,
	role string,
	baseURLs map[string]PostmanVariable,
) []WorkflowTask {
	var groups []*workflowCallGroup
	groupIndex := map[string]int{}
	for _, sample := range testCase.Traffic {
		if sample.Channel != channel {
			continue
		}
		operation, schemaID := workflowSampleMatch(testCase, sample, role)
		groupName := schemaID
		if groupName == "" {
			groupName = "captured"
		}
		index, exists := groupIndex[groupName]
		if !exists {
			index = len(groups)
			groupIndex[groupName] = index
			groups = append(groups, &workflowCallGroup{
				name:  groupName,
				names: newWorkflowNameAllocator(),
			})
		}
		baseURL := baseURLs[specLookupKey(role, schemaID)].Value
		definition, ok := workflowHTTPTaskDefinition(sample, operation, schemaID, role, baseURL)
		if !ok {
			continue
		}
		nameSeed := operation
		if sample.FlowID != "" {
			nameSeed += "-" + sample.FlowID
		}
		taskName := groups[index].names.next(nameSeed, "captured-call")
		groups[index].calls = append(groups[index].calls, namedWorkflowTask(taskName, definition))
	}

	names := newWorkflowNameAllocator()
	var out []WorkflowTask
	for _, group := range groups {
		if len(group.calls) == 0 {
			continue
		}
		out = append(out, namedWorkflowTask(
			names.next(group.name, "captured"),
			WorkflowTaskDefinition{Do: group.calls},
		))
	}
	return out
}

func workflowSampleMatch(testCase TestCaseCoverage, sample FlowSample, role string) (string, string) {
	specs := testCase.Provided
	if role == "fake" {
		specs = testCase.Dependent
	}
	for _, spec := range specs {
		for _, operation := range spec.Operations {
			for _, candidate := range operation.Samples {
				if candidate.FlowID == sample.FlowID && candidate.TestCase == sample.TestCase {
					return operation.Operation, spec.SchemaID
				}
			}
		}
	}
	method := strings.ToUpper(strings.TrimSpace(sample.Request.Method))
	path := requestPathAndQuery(sample.Request.URL)
	return strings.TrimSpace(method + " " + path), ""
}

func workflowTrafficTasks(
	specs []SpecOperationSamples,
	role string,
	baseURLs map[string]PostmanVariable,
) []WorkflowTask {
	specNames := newWorkflowNameAllocator()
	var out []WorkflowTask
	for _, spec := range specs {
		var calls []WorkflowTask
		callNames := newWorkflowNameAllocator()
		baseURL := baseURLs[specLookupKey(role, spec.SchemaID)].Value
		for _, operation := range spec.Operations {
			for _, sample := range operation.Samples {
				definition, ok := workflowHTTPTaskDefinition(sample, operation.Operation, spec.SchemaID, role, baseURL)
				if !ok {
					continue
				}

				nameSeed := strings.TrimSpace(operation.Operation)
				if sample.FlowID != "" {
					nameSeed += "-" + sample.FlowID
				}
				taskName := callNames.next(nameSeed, "captured-call")
				calls = append(calls, namedWorkflowTask(taskName, definition))
			}
		}
		if len(calls) == 0 {
			continue
		}
		specName := specNames.next(spec.SchemaID, "schema")
		out = append(out, namedWorkflowTask(specName, WorkflowTaskDefinition{Do: calls}))
	}
	return out
}

func workflowHTTPTaskDefinition(
	sample FlowSample,
	operation string,
	schemaID string,
	role string,
	baseURL string,
) (WorkflowTaskDefinition, bool) {
	if !strings.EqualFold(sample.Protocol, "http") && strings.TrimSpace(sample.Protocol) != "" {
		return WorkflowTaskDefinition{}, false
	}
	if strings.TrimSpace(sample.Request.Method) == "" {
		return WorkflowTaskDefinition{}, false
	}
	requestBody, requestEncoding, contentEncoding := workflowInteractionBody(sample.Request)
	return WorkflowTaskDefinition{
		Call: "http",
		With: &WorkflowHTTPArguments{
			Method:   strings.ToLower(strings.TrimSpace(sample.Request.Method)),
			Endpoint: workflowEndpoint(sample.Request.URL, baseURL),
			Headers:  workflowHeaders(sample.Request.Headers),
			Body:     requestBody,
			Output:   "response",
		},
		Metadata: &workflowTaskMetadata{Phrond: workflowTaskPhrondMetadata{
			Kind:                   "captured-flow",
			TestCase:               sample.TestCase,
			FlowID:                 sample.FlowID,
			Protocol:               sample.Protocol,
			Channel:                sample.Channel,
			Operation:              operation,
			SchemaID:               schemaID,
			Role:                   role,
			AssetID:                sample.AssetID,
			TraceID:                sample.TraceID,
			ParentFlowID:           sample.ParentFlowID,
			Fingerprint:            sample.Fingerprint,
			ConsumedFields:         append([]string(nil), sample.ConsumedFields...),
			MockStub:               sample.MockStub,
			MockMode:               sample.MockMode,
			TransportError:         sample.TransportError,
			Unmatched:              sample.Unmatched,
			RequestURL:             sample.Request.URL,
			RequestHeaders:         cloneWorkflowHeaders(sample.Request.Headers),
			RequestTrailers:        cloneWorkflowHeaders(sample.Request.Trailers),
			RequestEncoding:        requestEncoding,
			RequestContentEncoding: contentEncoding,
			RequestTruncated:       sample.Request.ArtifactBody != nil && sample.Request.ArtifactBody.Truncated,
			RequestOriginalBytes:   workflowOriginalBytes(sample.Request.ArtifactBody),
			CapturedResponse:       capturedWorkflowResponse(sample.Response),
		}},
	}, true
}

func namedWorkflowTask(name string, definition WorkflowTaskDefinition) WorkflowTask {
	return WorkflowTask{name: definition}
}

func workflowEndpoint(raw, baseURL string) string {
	value := strings.TrimSpace(raw)
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return value
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "http://localhost"
	}
	return base + requestPathAndQuery(value)
}

func workflowHeaders(headers artifact.Headers) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(keys))
	for _, key := range keys {
		values := headers[key]
		separator := ", "
		if strings.EqualFold(key, "cookie") {
			separator = "; "
		}
		out[key] = strings.Join(values, separator)
	}
	return out
}

func cloneWorkflowHeaders(headers artifact.Headers) artifact.Headers {
	if len(headers) == 0 {
		return nil
	}
	out := make(artifact.Headers, len(headers))
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func capturedWorkflowResponse(msg InteractionMessage) *workflowCapturedResponse {
	if msg.Status == 0 && len(msg.Headers) == 0 && strings.TrimSpace(msg.Body) == "" && msg.ArtifactBody == nil {
		return nil
	}
	body, encoding, contentEncoding := workflowInteractionBody(msg)
	return &workflowCapturedResponse{
		Status:          msg.Status,
		Headers:         cloneWorkflowHeaders(msg.Headers),
		Trailers:        cloneWorkflowHeaders(msg.Trailers),
		Body:            body,
		Encoding:        encoding,
		ContentEncoding: contentEncoding,
		Truncated:       msg.ArtifactBody != nil && msg.ArtifactBody.Truncated,
		OriginalBytes:   workflowOriginalBytes(msg.ArtifactBody),
	}
}

func workflowOriginalBytes(body *artifact.Body) int64 {
	if body == nil || !body.Truncated {
		return 0
	}
	return body.OriginalBytes
}

func workflowInteractionBody(msg InteractionMessage) (*any, string, string) {
	if body := msg.ArtifactBody; body != nil {
		switch {
		case body.Text != "":
			return workflowBody(body.Text), body.Encoding, body.ContentEncoding
		case body.Decoded != nil && body.Decoded.Text != "":
			return workflowBody(body.Decoded.Text), body.Decoded.Encoding, body.ContentEncoding
		case body.Bytes != "":
			value := any(body.Bytes)
			encoding := body.Encoding
			if encoding == "" {
				encoding = "base64"
			}
			return &value, encoding, body.ContentEncoding
		}
	}
	return workflowBody(msg.Body), "", ""
}

func workflowBody(raw string) *any {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		decoded = raw
	}
	return &decoded
}

type workflowNameAllocator struct {
	used map[string]int
}

func newWorkflowNameAllocator() *workflowNameAllocator {
	return &workflowNameAllocator{used: map[string]int{}}
}

func (a *workflowNameAllocator) next(raw, fallback string) string {
	base := workflowName(raw, fallback)
	count := a.used[base]
	a.used[base] = count + 1
	if count == 0 {
		return base
	}
	suffix := fmt.Sprintf("-%d", count+1)
	maxBase := 63 - len(suffix)
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-")
	}
	return base + suffix
}

func workflowName(raw, fallback string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		isAlphaNumeric := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNumeric {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = strings.Trim(workflowName(fallback, "workflow"), "-")
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	if name == "" {
		return "workflow"
	}
	return name
}
