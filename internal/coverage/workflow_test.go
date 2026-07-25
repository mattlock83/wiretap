package coverage

import (
	"bytes"
	"strings"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
	"gopkg.in/yaml.v3"
)

func TestWriteServerlessWorkflow_EmitsCapturedHTTPCallsAndMetadata(t *testing.T) {
	report := sampleCoverageReport(t)

	var first bytes.Buffer
	if err := WriteServerlessWorkflow(&first, report); err != nil {
		t.Fatalf("WriteServerlessWorkflow() error = %v", err)
	}
	var second bytes.Buffer
	if err := WriteServerlessWorkflow(&second, report); err != nil {
		t.Fatalf("second WriteServerlessWorkflow() error = %v", err)
	}
	if first.String() != second.String() {
		t.Fatal("workflow output is not deterministic")
	}

	var workflow ServerlessWorkflow
	if err := yaml.Unmarshal(first.Bytes(), &workflow); err != nil {
		t.Fatalf("decode workflow YAML: %v", err)
	}
	if workflow.Document.DSL != serverlessWorkflowDSLVersion {
		t.Fatalf("dsl = %q, want %q", workflow.Document.DSL, serverlessWorkflowDSLVersion)
	}
	if workflow.Document.Namespace != "phrond" {
		t.Fatalf("namespace = %q, want phrond", workflow.Document.Namespace)
	}
	if workflow.Document.Name != "payments-sut" {
		t.Fatalf("name = %q, want payments-sut", workflow.Document.Name)
	}
	if workflow.Document.Version != serverlessWorkflowVersion {
		t.Fatalf("version = %q, want %q", workflow.Document.Version, serverlessWorkflowVersion)
	}
	if workflow.Document.Metadata == nil || workflow.Document.Metadata.Phrond.ArtifactCount != 1 {
		t.Fatalf("missing Phrond document metadata: %#v", workflow.Document.Metadata)
	}

	provided := findWorkflowFlow(t, workflow.Do, "flow-1")
	if provided.Call != "http" || provided.With == nil {
		t.Fatalf("provided flow is not an HTTP call: %#v", provided)
	}
	if provided.With.Method != "post" {
		t.Fatalf("provided method = %q, want post", provided.With.Method)
	}
	if provided.With.Endpoint != "http://127.0.0.1:18110/payments/submit" {
		t.Fatalf("provided endpoint = %q", provided.With.Endpoint)
	}
	if provided.With.Headers["Content-Type"] != "application/json" {
		t.Fatalf("provided headers = %#v", provided.With.Headers)
	}
	if provided.With.Output != "response" {
		t.Fatalf("provided output = %q, want response", provided.With.Output)
	}
	if provided.With.Body == nil {
		t.Fatal("provided request body is missing")
	}
	body, ok := (*provided.With.Body).(map[string]any)
	if !ok {
		t.Fatalf("provided request body type = %T, want map", *provided.With.Body)
	}
	if body["from_account"] != "acct-100" {
		t.Fatalf("provided request body = %#v", body)
	}
	if provided.Metadata.Phrond.CapturedResponse == nil ||
		provided.Metadata.Phrond.CapturedResponse.Status != 201 {
		t.Fatalf("provided captured response = %#v", provided.Metadata.Phrond.CapturedResponse)
	}

	dependent := findWorkflowFlow(t, workflow.Do, "flow-2")
	if dependent.With == nil {
		t.Fatal("dependent HTTP arguments are missing")
	}
	if dependent.With.Endpoint != "http://localhost/accounts/acct-100" {
		t.Fatalf("dependent endpoint = %q, want localhost fallback", dependent.With.Endpoint)
	}
	if dependent.Metadata.Phrond.Channel != "sut_to_mock" {
		t.Fatalf("dependent channel = %q", dependent.Metadata.Phrond.Channel)
	}
	if dependent.Metadata.Phrond.Role != "fake" {
		t.Fatalf("dependent role = %q", dependent.Metadata.Phrond.Role)
	}

	output := first.String()
	for _, expected := range []string{
		"document:",
		"dsl: 1.0.3",
		"do:",
		"call: http",
		"captured_response:",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("workflow YAML missing %q:\n%s", expected, output)
		}
	}
}

func TestWriteReport_ServerlessWorkflowAliases(t *testing.T) {
	report := sampleCoverageReport(t)
	for _, format := range []string{"workflow", "serverless-workflow", "serverless_workflow", "sw"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			if err := WriteReport(&out, report, format); err != nil {
				t.Fatalf("WriteReport(%q) error = %v", format, err)
			}
			var workflow ServerlessWorkflow
			if err := yaml.Unmarshal(out.Bytes(), &workflow); err != nil {
				t.Fatalf("decode %q output: %v", format, err)
			}
			if workflow.Document.DSL != serverlessWorkflowDSLVersion {
				t.Fatalf("dsl = %q", workflow.Document.DSL)
			}
		})
	}
}

func TestServerlessWorkflowFromReport_UsesFullArtifactBodies(t *testing.T) {
	report := sampleCoverageReport(t)
	payload := strings.Repeat("captured-", 80)
	sample := &report.TestCases[0].Provided[0].Operations[0].Samples[0]
	sample.Request.Body = `{"payload":"preview"}`
	sample.Request.ArtifactBody = &artifact.Body{
		Encoding:      "utf8",
		Text:          `{"payload":"` + payload + `"}`,
		Truncated:     true,
		OriginalBytes: 2048,
	}
	sample.Response.ArtifactBody = &artifact.Body{
		Encoding:        "base64",
		Bytes:           "AAEC",
		ContentEncoding: "gzip",
		Truncated:       true,
		OriginalBytes:   4096,
	}

	workflow, err := ServerlessWorkflowFromReport(report)
	if err != nil {
		t.Fatalf("ServerlessWorkflowFromReport() error = %v", err)
	}
	call := findWorkflowFlow(t, workflow.Do, "flow-1")
	body, ok := (*call.With.Body).(map[string]any)
	if !ok {
		t.Fatalf("request body type = %T, want map", *call.With.Body)
	}
	if body["payload"] != payload {
		t.Fatalf("workflow request body was truncated")
	}
	if call.Metadata.Phrond.RequestEncoding != "utf8" {
		t.Fatalf("request encoding = %q", call.Metadata.Phrond.RequestEncoding)
	}
	if !call.Metadata.Phrond.RequestTruncated || call.Metadata.Phrond.RequestOriginalBytes != 2048 {
		t.Fatalf("request capture metadata = %#v", call.Metadata.Phrond)
	}
	response := call.Metadata.Phrond.CapturedResponse
	if response == nil || response.Body == nil || *response.Body != "AAEC" {
		t.Fatalf("captured binary response = %#v", response)
	}
	if response.Encoding != "base64" || response.ContentEncoding != "gzip" {
		t.Fatalf("captured response encoding = %#v", response)
	}
	if !response.Truncated || response.OriginalBytes != 4096 {
		t.Fatalf("captured response limit metadata = %#v", response)
	}
}

func TestServerlessWorkflowFromReport_IncludesSchemaLessTrafficAndCorrelation(t *testing.T) {
	report := &Report{
		SUTName:       "Schema-less SUT",
		ArtifactCount: 1,
		TestCases: []TestCaseCoverage{{
			Name: "captures an undeclared call",
			Traffic: []FlowSample{{
				TestCase:       "captures an undeclared call",
				FlowID:         "flow-9",
				Protocol:       "http",
				Channel:        "sut_to_mock",
				AssetID:        "inventory-service",
				TraceID:        "phtr-9",
				ParentFlowID:   "flow-1",
				Fingerprint:    "abc123",
				ConsumedFields: []string{"body.available"},
				MockMode:       "external",
				TransportError: "dial tcp: connection refused",
				Request: InteractionMessage{
					Method:   "GET",
					URL:      "/inventory/SKU-1",
					Trailers: artifact.Headers{"X-Request-Checksum": {"abc"}},
				},
				Response: InteractionMessage{
					Status:   502,
					Body:     "proxy error: dial tcp: connection refused",
					Trailers: artifact.Headers{"X-Response-Checksum": {"def"}},
				},
			}},
		}},
	}

	workflow, err := ServerlessWorkflowFromReport(report)
	if err != nil {
		t.Fatalf("ServerlessWorkflowFromReport() error = %v", err)
	}
	call := findWorkflowFlow(t, workflow.Do, "flow-9")
	if call.With == nil || call.With.Endpoint != "http://localhost/inventory/SKU-1" {
		t.Fatalf("schema-less endpoint = %#v", call.With)
	}
	meta := call.Metadata.Phrond
	if meta.AssetID != "inventory-service" ||
		meta.TraceID != "phtr-9" ||
		meta.ParentFlowID != "flow-1" ||
		meta.Fingerprint != "abc123" ||
		meta.Unmatched {
		t.Fatalf("schema-less correlation metadata = %#v", meta)
	}
	if meta.MockMode != "external" ||
		meta.TransportError != "dial tcp: connection refused" ||
		meta.RequestTrailers["X-Request-Checksum"][0] != "abc" ||
		meta.CapturedResponse.Trailers["X-Response-Checksum"][0] != "def" {
		t.Fatalf("external mock metadata = %#v", meta)
	}
	if len(meta.ConsumedFields) != 1 || meta.ConsumedFields[0] != "body.available" {
		t.Fatalf("consumed fields = %#v", meta.ConsumedFields)
	}
}

func TestServerlessWorkflowFromReport_SanitizesAndDeduplicatesTaskNames(t *testing.T) {
	report := sampleCoverageReport(t)
	report.SUTName = "Payments / SUT 🚀"
	report.TestCases = append(report.TestCases, report.TestCases[0])

	workflow, err := ServerlessWorkflowFromReport(report)
	if err != nil {
		t.Fatalf("ServerlessWorkflowFromReport() error = %v", err)
	}
	if workflow.Document.Name != "payments-sut" {
		t.Fatalf("workflow name = %q, want payments-sut", workflow.Document.Name)
	}
	if len(workflow.Do) != 2 {
		t.Fatalf("top-level task count = %d, want 2", len(workflow.Do))
	}
	firstName, _ := onlyWorkflowTask(t, workflow.Do[0])
	secondName, _ := onlyWorkflowTask(t, workflow.Do[1])
	if firstName != "submit-payment" || secondName != "submit-payment-2" {
		t.Fatalf("task names = %q, %q", firstName, secondName)
	}
}

func findWorkflowFlow(t *testing.T, tasks []WorkflowTask, flowID string) WorkflowTaskDefinition {
	t.Helper()
	for _, task := range tasks {
		_, definition := onlyWorkflowTask(t, task)
		if definition.Metadata != nil && definition.Metadata.Phrond.FlowID == flowID {
			return definition
		}
		if len(definition.Do) > 0 {
			if found, ok := findWorkflowFlowOptional(t, definition.Do, flowID); ok {
				return found
			}
		}
	}
	t.Fatalf("workflow flow %q not found", flowID)
	return WorkflowTaskDefinition{}
}

func findWorkflowFlowOptional(t *testing.T, tasks []WorkflowTask, flowID string) (WorkflowTaskDefinition, bool) {
	t.Helper()
	for _, task := range tasks {
		_, definition := onlyWorkflowTask(t, task)
		if definition.Metadata != nil && definition.Metadata.Phrond.FlowID == flowID {
			return definition, true
		}
		if len(definition.Do) > 0 {
			if found, ok := findWorkflowFlowOptional(t, definition.Do, flowID); ok {
				return found, true
			}
		}
	}
	return WorkflowTaskDefinition{}, false
}

func onlyWorkflowTask(t *testing.T, task WorkflowTask) (string, WorkflowTaskDefinition) {
	t.Helper()
	if len(task) != 1 {
		t.Fatalf("workflow task has %d entries, want 1", len(task))
	}
	for name, definition := range task {
		return name, definition
	}
	return "", WorkflowTaskDefinition{}
}
