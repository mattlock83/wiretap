package coverage

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWriteServerlessWorkflowEmitsOnlyCapturedSUTCalls(t *testing.T) {
	report := sampleCaptureReport()
	var out bytes.Buffer
	if err := WriteServerlessWorkflow(&out, report); err != nil {
		t.Fatal(err)
	}
	var workflow ServerlessWorkflow
	if err := yaml.Unmarshal(out.Bytes(), &workflow); err != nil {
		t.Fatal(err)
	}
	flow := findWorkflowFlow(t, workflow.Do, "flow-1")
	if flow.With == nil || flow.With.Method != "get" || flow.With.Endpoint != "http://sut.test/hello" {
		t.Fatalf("unexpected HTTP task: %#v", flow)
	}
	if strings.Contains(out.String(), "mock-traffic") || strings.Contains(out.String(), "fake") {
		t.Fatalf("workflow contains removed mock concepts:\n%s", out.String())
	}
}

func findWorkflowFlow(t *testing.T, tasks []WorkflowTask, flowID string) WorkflowTaskDefinition {
	t.Helper()
	for _, task := range tasks {
		for _, definition := range task {
			if definition.Metadata != nil && definition.Metadata.Phrond.FlowID == flowID {
				return definition
			}
			if len(definition.Do) > 0 {
				if found, ok := findWorkflowFlowOptional(definition.Do, flowID); ok {
					return found
				}
			}
		}
	}
	t.Fatalf("flow %q not found", flowID)
	return WorkflowTaskDefinition{}
}

func findWorkflowFlowOptional(tasks []WorkflowTask, flowID string) (WorkflowTaskDefinition, bool) {
	for _, task := range tasks {
		for _, definition := range task {
			if definition.Metadata != nil && definition.Metadata.Phrond.FlowID == flowID {
				return definition, true
			}
			if found, ok := findWorkflowFlowOptional(definition.Do, flowID); ok {
				return found, true
			}
		}
	}
	return WorkflowTaskDefinition{}, false
}
