package coverage

import (
	"bytes"
	"strings"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
)

func TestHTMLReportIncludesCompleteCapturedRequestAndResponsePayloads(t *testing.T) {
	requestPayload := `{"request_payload":"` + strings.Repeat("from-test-", 50) + `END-OF-REQUEST"}`
	responsePayload := `{"response_payload":"` + strings.Repeat("from-sut-", 50) + `END-OF-RESPONSE"}`
	sample := FlowSample{
		TestCase: "payload happy flow",
		FlowID:   "flow-1",
		Protocol: "http",
		Channel:  "test_to_sut",
		TraceID:  "trace-1",
		Request: InteractionMessage{
			Method:       "POST",
			URL:          "http://sut.test/messages",
			Headers:      artifact.Headers{"X-Test-Request": {"request-header-value"}},
			Trailers:     artifact.Headers{"X-Test-Request-Trailer": {"request-trailer-value"}},
			Body:         requestPayload,
			ArtifactBody: &artifact.Body{Encoding: "utf8", Text: requestPayload},
		},
		Response: InteractionMessage{
			Status:       201,
			Headers:      artifact.Headers{"X-SUT-Response": {"response-header-value"}},
			Trailers:     artifact.Headers{"X-SUT-Response-Trailer": {"response-trailer-value"}},
			Body:         responsePayload,
			ArtifactBody: &artifact.Body{Encoding: "utf8", Text: responsePayload},
		},
	}
	report := &Report{
		SUTName:       "Payload SUT",
		ArtifactCount: 1,
		TestCases:     []TestCaseCoverage{{Name: sample.TestCase, Traffic: []FlowSample{sample}}},
	}

	var output bytes.Buffer
	if err := WriteReport(&output, report, "html"); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		"By API Spec",
		"By Test Case",
		"Captured Test → SUT and SUT → Dependency Traffic",
		"Test Case: payload happy flow",
		"POST http://sut.test/messages",
		"Request Headers:",
		"request-header-value",
		"Request Trailers:",
		"request-trailer-value",
		"Request Body:",
		"END-OF-REQUEST",
		"Response Headers:",
		"response-header-value",
		"Response Trailers:",
		"response-trailer-value",
		"Response Body:",
		"END-OF-RESPONSE",
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("HTML report missing %q:\n%s", expected, html)
		}
	}
	if strings.Contains(html, "Dependent API Specs") || strings.Contains(html, "mock traffic") {
		t.Fatalf("HTML report unexpectedly includes undeclared dependencies or mock-serving concepts:\n%s", html)
	}
}

func TestHTMLReportIncludesCorrelatedExternalDependencyPayloads(t *testing.T) {
	var output bytes.Buffer
	if err := WriteReport(&output, sampleDualCaptureReport(), "html"); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		"Dependent API Specs", "inventory-openapi", "sut_to_dependency", "dependency inventory-service",
		"parent flow-1", "POST http://wiremock.test/inventory", "widget", "available",
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("HTML report missing %q:\n%s", expected, html)
		}
	}
	if strings.Contains(html, ">Faked<") || strings.Contains(html, "mock traffic") {
		t.Fatalf("HTML report reintroduced mock-serving concepts:\n%s", html)
	}
}
