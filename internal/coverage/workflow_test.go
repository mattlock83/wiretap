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

}

func TestWriteServerlessWorkflowIncludesCorrelatedExternalDependencyCalls(t *testing.T) {
	var output bytes.Buffer
	if err := WriteServerlessWorkflow(&output, sampleDualCaptureReport()); err != nil {
		t.Fatal(err)
	}
	var workflow ServerlessWorkflow
	if err := yaml.Unmarshal(output.Bytes(), &workflow); err != nil {
		t.Fatal(err)
	}
	dependency := findWorkflowFlow(t, workflow.Do, "flow-2")
	if dependency.With == nil || dependency.With.Method != "post" || dependency.With.Endpoint != "http://wiremock.test/inventory" {
		t.Fatalf("unexpected dependency HTTP task: %#v", dependency)
	}
	if dependency.Metadata == nil || dependency.Metadata.Phrond.AssetID != "inventory-service" || dependency.Metadata.Phrond.ParentFlowID != "flow-1" {
		t.Fatalf("dependency identity/correlation missing: %#v", dependency.Metadata)
	}
	if !strings.Contains(output.String(), "dependency-traffic:") {
		t.Fatalf("unexpected workflow channels:\n%s", output.String())
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
