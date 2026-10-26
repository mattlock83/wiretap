package app

import (
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestSecurityDeclarationDoesNotRequireWiretapAuthenticationBackend(t *testing.T) {
	ref := artifact.SchemaRef{ID: "secured", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: Secure API, version: '1'}
paths:
  /orders:
    post:
      security: [{bearer: []}]
      requestBody:
        required: true
        content: {application/json: {schema: {type: object, required: [quantity], properties: {quantity: {type: integer, minimum: 1}}}}}
      responses:
        '200': {description: accepted}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
`}}
	for _, tc := range []struct{ body, want string }{{`{"quantity":1}`, "pass"}, {`{"quantity":0}`, "fail"}} {
		a := &artifact.Artifact{TestCase: artifact.TestCase{Status: "pass"}, Schemas: []artifact.SchemaRef{ref}, Traffic: []artifact.Flow{{ID: "flow", Protocol: "http", Channel: "test_to_sut", Request: &artifact.HttpMessage{Method: "POST", URL: "http://provider/orders", Headers: artifact.Headers{"Content-Type": {"application/json"}, "Authorization": {"Bearer example"}}, Body: &artifact.Body{Text: tc.body}}, Response: &artifact.HttpMessage{Status: 200}}}}
		validateTestCapture(t, a)
		if a.TestCase.Status != "pass" || a.SchemaValidation.Status != tc.want {
			t.Fatalf("body=%s status=%s validation=%+v", tc.body, a.TestCase.Status, a.SchemaValidation)
		}
	}
}
