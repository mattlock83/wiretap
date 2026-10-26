package coverage

import (
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestResponseValidatesHTMLStrings(t *testing.T) {
	schema := strings.Replace(simpleResponseSpec(`{type: string, enum: [Property deleted]}`), "application/json:", "text/html:", 1)
	spec, err := loadOpenAPISpec(freezeCoverageSchema(t, artifact.SchemaRef{Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: schema}}))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", "http://provider/orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	route, params, err := spec.Router.FindRoute(req)
	if err != nil {
		t.Fatal(err)
	}
	input := &openapi3filter.RequestValidationInput{Request: req, Route: route, PathParams: params}
	for _, body := range []string{"Property deleted", "Tenant deleted"} {
		t.Run(body, func(t *testing.T) {
			msg := &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"text/html; charset=utf-8"}}, Body: &artifact.Body{Text: body}}
			err := ValidateResponse(input, msg)
			if body == "Property deleted" && err != nil {
				t.Fatalf("valid HTML string rejected: %v", err)
			}
			if body == "Tenant deleted" && (err == nil || !strings.Contains(err.Error(), "enum")) {
				t.Fatalf("expected an enum violation, got %v", err)
			}
		})
	}
}
