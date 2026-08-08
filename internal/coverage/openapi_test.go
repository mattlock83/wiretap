package coverage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/phrond/evermock/internal/artifact"
	"gopkg.in/yaml.v3"
)

func TestWriteGeneratedOpenAPIProducesDeterministicStrictSchemaForCapturedTraffic(t *testing.T) {
	report := generatedSchemaTestReport()
	var first bytes.Buffer
	if err := WriteGeneratedOpenAPI(&first, report, GeneratedOpenAPIOptions{Title: "Orders Service", Version: "1.0.0"}); err != nil {
		t.Fatalf("generate OpenAPI: %v", err)
	}
	var second bytes.Buffer
	if err := WriteGeneratedOpenAPI(&second, report, GeneratedOpenAPIOptions{Title: "Orders Service", Version: "1.0.0"}); err != nil {
		t.Fatalf("generate OpenAPI again: %v", err)
	}
	if first.String() != second.String() {
		t.Fatal("generated OpenAPI is not deterministic")
	}
	if strings.Contains(first.String(), "secret-product-value") || strings.Contains(first.String(), "gift message") {
		t.Fatal("generated OpenAPI leaked captured values as examples")
	}
	if strings.Contains(first.String(), "failed_only_field") {
		t.Fatal("generated OpenAPI learned from a failing test")
	}

	var document map[string]any
	if err := yaml.Unmarshal(first.Bytes(), &document); err != nil {
		t.Fatalf("decode generated OpenAPI: %v", err)
	}
	paths := document["paths"].(map[string]any)
	orders := paths["/orders"].(map[string]any)
	post := orders["post"].(map[string]any)
	requestBody := post["requestBody"].(map[string]any)
	content := requestBody["content"].(map[string]any)
	media := content["application/json"].(map[string]any)
	schema := media["schema"].(map[string]any)
	required := schema["required"].([]any)
	if got := strings.Join([]string{required[0].(string), required[1].(string)}, ","); got != "product_id,quantity" {
		t.Fatalf("required properties = %s", got)
	}
	if schema["additionalProperties"] != false {
		t.Fatal("generated object schema should reject unobserved properties")
	}

	orderByID := paths["/orders/{orderId}"].(map[string]any)
	get := orderByID["get"].(map[string]any)
	parameters := get["parameters"].([]any)
	pathParameter := parameters[0].(map[string]any)
	if pathParameter["name"] != "orderId" || pathParameter["in"] != "path" || pathParameter["required"] != true {
		t.Fatalf("unexpected path parameter: %#v", pathParameter)
	}
	pathSchema := pathParameter["schema"].(map[string]any)
	if pathSchema["format"] != "uuid" {
		t.Fatalf("path parameter schema = %#v", pathSchema)
	}

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(first.Bytes())
	if err != nil {
		t.Fatalf("load generated OpenAPI: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate generated OpenAPI: %v", err)
	}
	validateGeneratedCaptures(t, doc, report)
}

func TestWriteGeneratedOpenAPIRequiresCapturedHTTP(t *testing.T) {
	var output bytes.Buffer
	err := WriteGeneratedOpenAPI(&output, &Report{SUTName: "Empty"}, GeneratedOpenAPIOptions{})
	if err == nil || !strings.Contains(err.Error(), "no captured") {
		t.Fatalf("error = %v", err)
	}
}

func generatedSchemaTestReport() *Report {
	jsonHeaders := artifact.Headers{"Content-Type": {"application/json; charset=utf-8"}}
	created := `{"id":"90d8ef0a-f789-4a56-9abc-1234567890ab","status":"created"}`
	return &Report{
		SUTName:       "Orders Service",
		ArtifactCount: 3,
		TestCases: []TestCaseCoverage{
			{Name: "creates an order with note", Traffic: []FlowSample{{
				Protocol: "http", Channel: "test_to_sut",
				Request: InteractionMessage{
					Method: "POST", URL: "http://orders.test/orders", Headers: jsonHeaders,
					ArtifactBody: &artifact.Body{Text: `{"product_id":"secret-product-value","quantity":2,"note":"gift message"}`},
				},
				Response: InteractionMessage{Status: 201, Headers: jsonHeaders, ArtifactBody: &artifact.Body{Text: created}},
			}}},
			{Name: "creates an order without note", Traffic: []FlowSample{{
				Protocol: "http", Channel: "test_to_sut",
				Request: InteractionMessage{
					Method: "POST", URL: "http://orders.test/orders", Headers: jsonHeaders,
					ArtifactBody: &artifact.Body{Text: `{"product_id":"another-product","quantity":1}`},
				},
				Response: InteractionMessage{Status: 201, Headers: jsonHeaders, ArtifactBody: &artifact.Body{Text: created}},
			}}},
			{Name: "gets an order", Traffic: []FlowSample{{
				Protocol: "http", Channel: "test_to_sut",
				Request:  InteractionMessage{Method: "GET", URL: "http://orders.test/orders/90d8ef0a-f789-4a56-9abc-1234567890ab?expand=true"},
				Response: InteractionMessage{Status: 200, Headers: jsonHeaders, ArtifactBody: &artifact.Body{Text: created}},
			}}},
			{Name: "failed test is not a contract", Traffic: []FlowSample{{
				TestStatus: "fail", Protocol: "http", Channel: "test_to_sut",
				Request: InteractionMessage{
					Method: "POST", URL: "http://orders.test/orders", Headers: jsonHeaders,
					ArtifactBody: &artifact.Body{Text: `{"product_id":"bad","quantity":1,"failed_only_field":true}`},
				},
				Response: InteractionMessage{Status: 500, Headers: jsonHeaders, ArtifactBody: &artifact.Body{Text: `{"error":"bad"}`}},
			}}},
		},
	}
}

func validateGeneratedCaptures(t *testing.T, doc *openapi3.T, report *Report) {
	t.Helper()
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("create generated OpenAPI router: %v", err)
	}
	for _, testcase := range report.TestCases {
		for _, flow := range testcase.Traffic {
			if flow.TestStatus == "fail" {
				continue
			}
			requestBody, _ := artifactBodyBytes(flow.Request.ArtifactBody)
			request, err := http.NewRequest(flow.Request.Method, flow.Request.URL, bytes.NewReader(requestBody))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			request.Header = toHTTPHeader(flow.Request.Headers)
			route, params, err := router.FindRoute(request)
			if err != nil {
				t.Fatalf("find generated route for %s: %v", flow.Request.URL, err)
			}
			input := &openapi3filter.RequestValidationInput{Request: request, PathParams: params, Route: route}
			if err := openapi3filter.ValidateRequest(context.Background(), input); err != nil {
				t.Fatalf("generated schema rejected captured request %s: %v", flow.Request.URL, err)
			}
			responseBody, _ := artifactBodyBytes(flow.Response.ArtifactBody)
			response := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: input,
				Status:                 flow.Response.Status,
				Header:                 toHTTPHeader(flow.Response.Headers),
				Body:                   io.NopCloser(bytes.NewReader(responseBody)),
			}
			if err := openapi3filter.ValidateResponse(context.Background(), response); err != nil {
				t.Fatalf("generated schema rejected captured response %s: %v", flow.Request.URL, err)
			}
		}
	}
}

func toHTTPHeader(headers artifact.Headers) http.Header {
	result := http.Header{}
	for key, values := range headers {
		for _, value := range values {
			result.Add(key, value)
		}
	}
	return result
}
