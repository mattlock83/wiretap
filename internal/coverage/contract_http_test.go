package coverage

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestContractRedirectBodiesMustValidateBeforeCoverage(t *testing.T) {
	for _, status := range []int{301, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			schema := strings.Replace(simpleResponseSpec(`{type: string, enum: [expected]}`), "'200':", fmt.Sprintf("'%d':", status), 1)
			run, capture := contractFixture(schema, `"wrong"`)
			capture.Traffic[0].Response.HTTP.Status = status
			spec := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			requireTargetState(t, spec, "response", "/schema", "type", "string", "not_covered")
			if len(spec.Violations) == 0 || spec.Violations[0].Evidence.Validation != "invalid" {
				t.Fatal("redirect response body bypassed validation")
			}
			capture.Traffic[0].Response.HTTP.Body.Text = `"expected"`
			spec = measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			requireTargetState(t, spec, "response", "/schema", "enum", `"expected"`, "covered")
			if capture.Traffic[0].Response.HTTP.Status != status || capture.Traffic[0].Request.HTTP.Method != "POST" {
				t.Fatal("measurement mutated captured evidence")
			}
		})
	}
}

func TestContractBodylessResponsesValidateStatusAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		method string
		status int
	}{{"HEAD", 200}, {"POST", 204}, {"POST", 304}} {
		t.Run(fmt.Sprintf("%s-%d", tc.method, tc.status), func(t *testing.T) {
			schema := fmt.Sprintf(`openapi: 3.0.3
info: {title: Headers, version: "1"}
paths:
  /orders:
    %s:
      responses:
        '%d':
          description: documented
          headers:
            X-Result: {required: true, schema: {type: string, enum: [expected]}}
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: string}}}
`, strings.ToLower(tc.method), tc.status)
			run, capture := contractFixture(schema, "")
			capture.Traffic[0].Request.HTTP = &artifact.HttpMessage{Method: tc.method, URL: "http://provider/orders"}
			capture.Traffic[0].Response.HTTP = &artifact.HttpMessage{Status: tc.status, Headers: artifact.Headers{"X-Result": {"wrong"}}}
			spec := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			requireTargetState(t, spec, "response", "/headers/X-Result/schema", "type", "string", "not_covered")
			if len(spec.Violations) == 0 {
				t.Fatal("invalid response header was accepted")
			}
			if spec.Violations[0].Evidence.InstanceLocation != "/header/X-Result" || !strings.HasSuffix(spec.Violations[0].SchemaLocation, "/headers/X-Result/schema") {
				t.Fatalf("response header violation has incorrect evidence location: %+v", spec.Violations[0])
			}
			delete(capture.Traffic[0].Response.HTTP.Headers, "X-Result")
			spec = measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			if len(spec.Violations) == 0 || spec.Violations[0].Evidence.InstanceLocation != "/header/X-Result" {
				t.Fatal("missing response header violation has incorrect evidence location")
			}
			capture.Traffic[0].Response.HTTP.Headers["X-Result"] = []string{"expected"}
			spec = measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			requireTargetState(t, spec, "response", "/headers/X-Result/schema", "enum", `"expected"`, "covered")
			requireTargetState(t, spec, "response", "/properties/id", "presence", "present", "excluded")
			if len(spec.Violations) != 0 {
				t.Fatalf("bodyless response reported a body violation: %+v", spec.Violations)
			}
			capture.Traffic[0].Response.HTTP.Status = 308
			spec = measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			if len(spec.Violations) == 0 {
				t.Fatal("undeclared response status was accepted")
			}
		})
	}
}

func TestContractUnknownEvidenceDoesNotSelectOtherResponses(t *testing.T) {
	run, capture := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
	capture.Traffic[0].Response.HTTP.Body.Truncated = true
	spec := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
	requireTargetState(t, spec, "response", "/responses/200", "response_status", "200", "unknown")
	for _, alternative := range []string{"4XX", "default"} {
		target := requireTargetState(t, spec, "response", "/responses/"+alternative, "response_status", alternative, "not_covered")
		if len(target.Evidence) != 0 {
			t.Fatal("unselected response received unknown evidence")
		}
	}
}

func TestHTTPBodyAvailabilityFlagsPreventCoverageWithoutPolicyRules(t *testing.T) {
	for _, phase := range []string{"request", "response"} {
		for _, flag := range []string{"omitted", "redacted"} {
			t.Run(phase+"/"+flag, func(t *testing.T) {
				run, capture := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
				baseline := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
				requireTargetState(t, baseline, "request", "/properties/name", "presence", "present", "covered")
				requireTargetState(t, baseline, "response", "/properties/status", "enum", `"pending"`, "covered")
				body := capture.Traffic[0].Request.HTTP.Body
				if phase == "response" {
					body = capture.Traffic[0].Response.HTTP.Body
				}
				if flag == "omitted" {
					body.Omitted, body.Text = true, ""
				} else {
					body.Redacted = true
				}
				if body.Truncated || len(capture.Redactions) != 0 {
					t.Fatal("fixture must rely on availability flags alone")
				}
				spec := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
				var target *ContractTarget
				if phase == "request" {
					target = requireTargetState(t, spec, "request", "/properties/name", "presence", "present", "unknown")
					requireTargetState(t, spec, "response", "/properties/status", "enum", `"pending"`, "covered")
				} else {
					target = requireTargetState(t, spec, "response", "/properties/status", "enum", `"pending"`, "unknown")
					requireTargetState(t, spec, "request", "/properties/name", "presence", "present", "covered")
				}
				if len(target.Evidence) != 1 || target.Evidence[0].Validation != "unknown" || !strings.Contains(target.Evidence[0].Reason, flag) {
					t.Fatalf("availability limit lost from evidence: %+v", target.Evidence)
				}
				if len(spec.Violations) != 0 {
					t.Fatalf("unavailable body treated as a contract mismatch: %+v", spec.Violations)
				}
				for _, target := range spec.Targets {
					if target.Phase == phase && target.State == "covered" {
						t.Fatalf("unavailable message earned original-condition coverage: %+v", target)
					}
				}
			})
		}
	}
}

func TestContractScalarMeasurementMatchesValidatorDecoding(t *testing.T) {
	for _, tc := range []struct{ schema, raw, condition string }{
		{`{type: integer, enum: [8]}`, "010", "8"},
		{`{type: integer, enum: [16]}`, "0x10", "16"},
		{`{type: boolean, enum: [true]}`, "TRUE", "true"},
		{`{type: boolean, enum: [false]}`, "0", "false"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			schema := strings.Replace(conditionsSpec, "{type: string, enum: [fast, slow], default: fast}", tc.schema, 1)
			run, capture := contractFixture(schema, `{"status":"pending","items":[],"reference":"ORD-123"}`)
			capture.Traffic[0].Request.HTTP.URL += "?mode=" + tc.raw
			spec := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			requireTargetState(t, spec, "request", "/parameters/query/mode/schema", "enum", tc.condition, "covered")
			if len(spec.Violations) != 0 {
				t.Fatalf("valid scalar generated a violation: %+v", spec.Violations)
			}
		})
	}
}

func TestContractAllowedEmptyParameterDoesNotBypassConstraintMeasurement(t *testing.T) {
	schema := strings.Replace(conditionsSpec, "in: query, schema:", "in: query, allowEmptyValue: true, schema:", 1)
	run, capture := contractFixture(schema, `{"status":"pending","items":[],"reference":"ORD-123"}`)
	capture.Traffic[0].Request.HTTP.URL += "?mode="
	spec := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
	target := requireTargetState(t, spec, "request", "/parameters/query/mode/schema", "type", "string", "unknown")
	if len(target.Evidence) != 1 || target.Evidence[0].Validation != "unknown" {
		t.Fatal("empty parameter was reported as validated")
	}
}

func TestContractBodylessResponsesIgnoreUnsupportedBodyMeasurement(t *testing.T) {
	for _, tc := range []struct {
		method string
		status int
	}{{"HEAD", 200}, {"POST", 204}, {"POST", 304}} {
		t.Run(fmt.Sprintf("%s-%d", tc.method, tc.status), func(t *testing.T) {
			schema := fmt.Sprintf(`openapi: 3.0.3
info: {title: Headers, version: "1"}
paths:
  /orders:
    %s:
      responses:
        '%d':
          description: documented
          headers:
            X-Result: {required: true, schema: {type: string, enum: [expected]}}
          content:
            application/octet-stream:
              schema: {type: string}
`, strings.ToLower(tc.method), tc.status)
			run, capture := contractFixture(schema, "")
			capture.Traffic[0].Request.HTTP = &artifact.HttpMessage{Method: tc.method, URL: "http://provider/orders"}
			capture.Traffic[0].Response.HTTP = &artifact.HttpMessage{Status: tc.status, Headers: artifact.Headers{"Content-Type": {"application/octet-stream"}, "X-Result": {"expected"}}}
			spec := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			requireTargetState(t, spec, "response", "/responses/"+fmt.Sprint(tc.status), "response_status", fmt.Sprint(tc.status), "covered")
			requireTargetState(t, spec, "response", "/headers/X-Result/schema", "enum", `"expected"`, "covered")
			requireTargetState(t, spec, "response", "/content/application~1octet-stream", "media_type", "application/octet-stream", "excluded")
			requireTargetState(t, spec, "response", "/content/application~1octet-stream", "unsupported", "Only JSON body condition measurement is supported", "excluded")
			if len(spec.Violations) != 0 {
				t.Fatalf("bodyless response inspected unsupported body: %+v", spec.Violations)
			}
		})
	}
}

func TestContractDefault304DoesNotMeasureForbiddenBody(t *testing.T) {
	for _, alternative := range []string{"default", "3XX"} {
		t.Run(alternative, func(t *testing.T) {
			schema := strings.Replace(simpleResponseSpec(`{type: string, enum: [expected]}`), "'200':", alternative+":", 1)
			run, capture := contractFixture(schema, `"expected"`)
			capture.Traffic[0].Response.HTTP.Status = 304
			spec := measureContractFixture(t, run, []*artifact.Artifact{capture}).Specs[0]
			target := requireTargetState(t, spec, "response", "/schema", "enum", `"expected"`, "not_covered")
			if len(target.Evidence) != 0 || len(spec.Violations) == 0 {
				t.Fatal("forbidden 304 response body was measured or was not a violation")
			}
		})
	}
}
