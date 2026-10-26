package coverage

import (
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestContractEvidencePreservesQualityAndValidation(t *testing.T) {
	for _, test := range []struct {
		name, validation string
		mutate           func(*artifact.Artifact)
	}{
		{"passing", "valid", func(a *artifact.Artifact) {}},
		{"failed", "valid", func(a *artifact.Artifact) { a.TestCase.Status = "fail" }},
		{"incomplete", "valid", func(a *artifact.Artifact) { a.CaptureIncomplete = true }},
		{"transport", "valid", func(a *artifact.Artifact) { a.Traffic[0].TransportError = "connection failed" }},
		{"invalid", "invalid", func(a *artifact.Artifact) {
			a.Traffic[0].Response.Body.Text = `{"status":"pending","items":[],"reference":"bad"}`
		}},
		{"truncated", "unknown", func(a *artifact.Artifact) { a.Traffic[0].Response.Body.Truncated = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			run, a := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
			test.mutate(a)
			spec := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
			target := contractTarget(t, spec, "response", "/properties/status", "enum", `"pending"`)
			if len(target.Evidence) != 1 {
				t.Fatalf("expected one evidence reference: %+v", target.Evidence)
			}
			e := target.Evidence[0]
			if e.Validation != test.validation || e.Outcome != a.TestCase.Status || e.CaptureIncomplete != a.CaptureIncomplete || e.TransportError != a.Traffic[0].TransportError {
				t.Fatalf("evidence lost message or attempt quality: %+v", e)
			}
		})
	}
}

func TestOperationInventoryIncludesTrace(t *testing.T) {
	schema := `openapi: 3.0.3
info: {title: Trace, version: "1"}
paths:
  /trace:
    trace:
      responses:
        '200': {description: OK}
`
	run, _ := contractFixture(schema, "")
	spec, err := loadOpenAPISpec(freezeCoverageSchema(t, run.Schemas[0]))
	if err != nil {
		t.Fatal(err)
	}
	operations := openapiOperations(spec)
	if len(operations) != 1 || operations[0] != "TRACE /trace" {
		t.Fatalf("lost declared operation: %v", operations)
	}
}
