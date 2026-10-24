package app

import (
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestSchemaValidationHTMLStringResponses(t *testing.T) {
	ref := artifact.SchemaRef{ID: "properties", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: Properties, version: '1'}
paths:
  /properties/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: string}}
    delete:
      responses:
        '200':
          description: Deleted
          content:
            text/html:
              schema: {type: string, enum: [Property deleted, '<p>Property deleted</p>']}
`}}
	for _, tc := range []struct{ name, body, want string }{
		{"express string", "Property deleted", "pass"},
		{"html markup", "<p>Property deleted</p>", "pass"},
		{"invalid enum", "Tenant deleted", "fail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &artifact.Artifact{
				TestCase: artifact.TestCase{Status: "pass"},
				Schemas:  []artifact.SchemaRef{ref},
				Traffic: []artifact.Flow{{
					ID: "flow", Protocol: "http", Channel: "test_to_sut",
					Request: artifact.Message{HTTP: &artifact.HttpMessage{
						Method: "DELETE", URL: "http://provider/properties/example",
					}},
					Response: artifact.Message{HTTP: &artifact.HttpMessage{
						Status: 200, Headers: artifact.Headers{"Content-Type": {"text/html; charset=utf-8"}},
						Body: &artifact.Body{Text: tc.body},
					}},
				}},
			}
			applySchemaValidation(capture)
			if capture.TestCase.Status != "pass" || capture.SchemaValidation.Status != tc.want {
				t.Fatalf("test status=%s validation=%+v", capture.TestCase.Status, capture.SchemaValidation)
			}
			if tc.want == "fail" {
				issues := capture.SchemaValidation.Errors
				if len(issues) != 1 || issues[0].Phase != "response" || !strings.Contains(issues[0].Message, "enum") {
					t.Fatalf("expected a response schema violation, got %+v", issues)
				}
			}
		})
	}
}
