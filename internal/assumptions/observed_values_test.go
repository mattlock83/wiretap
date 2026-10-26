package assumptions

import (
	"errors"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

// These are concrete retained values, independent of any optional schema. A
// contract can explain a value's type or enum, but is not needed to detect that
// the consumer's actual request or mocked reply differs from provider evidence.
func TestCheckObservedTypesEnumsAndLengthsFailInBothModes(t *testing.T) {
	for _, tc := range []struct {
		name, provider, consumer string
	}{
		{"number to string", `3`, `"3"`},
		{"number to boolean", `1`, `true`},
		{"boolean to string", `true`, `"true"`},
		{"boolean to null", `true`, `null`},
		{"null to object", `null`, `{}`},
		{"null to array", `null`, `[]`},
		{"object to array", `{"id":1}`, `[{"id":1}]`},
		{"object to string", `{"id":1}`, `"{\"id\":1}"`},
		{"array to number", `[1]`, `1`},
		{"nested property type", `{"nested":3}`, `{"nested":"3"}`},
		{"array element type", `[1,2]`, `[1,"2"]`},
		{"string enum literal", `"ready"`, `"pending"`},
		{"numeric enum literal", `1`, `2`},
		{"empty string length", `""`, `"a"`},
		{"nonempty string length", `"four"`, `"five!"`},
		{"empty array count", `[]`, `[1]`},
		{"nonempty array count", `[1]`, `[1,2]`},
		{"missing versus null property", `{}`, `{"value":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, phase := range []string{"request", "response"} {
				t.Run(phase, func(t *testing.T) {
					consumer, provider := checkFixtureFlow(), checkFixtureFlow()
					consumerMessage, providerMessage := consumer.Request.HTTP, provider.Request.HTTP
					if phase == "response" {
						consumerMessage, providerMessage = consumer.Response.HTTP, provider.Response.HTTP
					}
					consumerMessage.Body.Text = `{"field":` + tc.consumer + `}`
					providerMessage.Body.Text = `{"field":` + tc.provider + `}`
					options := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, provider)})
					for _, mode := range []string{"consumer", "provider"} {
						t.Run(mode, func(t *testing.T) {
							options.Mode = mode
							report := mustCheck(t, options)
							if report.Compatible || report.Counts.Assumptions != 1 || report.Counts.Verified != 0 || len(report.Findings) != 1 {
								t.Fatalf("different %s values were certified by %s mode: %+v", phase, mode, report)
							}
							if phase == "request" {
								if report.Counts.Uncovered != 1 || report.Counts.Mismatched != 0 || !findingCode(report, "provider_request_uncovered") {
									t.Fatalf("changed request needs its own provider test example: %+v", report)
								}
							} else {
								if report.Counts.Mismatched != 1 || report.Counts.Uncovered != 0 || !findingCode(report, "provider_response_mismatch") {
									t.Fatalf("changed mock reply must disagree with the matching request's provider response: %+v", report)
								}
								if len(report.Findings[0].Differences) == 0 || !strings.HasPrefix(report.Findings[0].Differences[0], "response/body/value/field") {
									t.Fatalf("changed value has no field-level diagnostic: %+v", report.Findings[0])
								}
							}
							if report.Findings[0].Consumer == nil || len(report.Findings[0].Provider) != 1 {
								t.Fatalf("type, literal or size disagreement lost concrete source evidence: %+v", report.Findings[0])
							}
						})
					}
				})
			}
		})
	}
}

func TestCheckRejectsNonJSONBodies(t *testing.T) {
	for _, media := range []string{"text/plain", "application/octet-stream"} {
		for _, phase := range []string{"request", "response"} {
			t.Run(media+"/"+phase, func(t *testing.T) {
				consumer, provider := checkFixtureFlow(), checkFixtureFlow()
				cm, pm := consumer.Request.HTTP, provider.Request.HTTP
				if phase == "response" {
					cm, pm = consumer.Response.HTTP, provider.Response.HTTP
				}
				cm.Headers["Content-Type"], pm.Headers["Content-Type"] = []string{media}, []string{media}
				cm.Body.Text, pm.Body.Text = "1", "1.0"
				options := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, consumer)}, []*artifact.Artifact{checkCapture(true, provider)})
				for _, mode := range []string{"consumer", "provider"} {
					options.Mode = mode
					if _, err := Check(t.Context(), options); !errors.Is(err, ErrInput) {
						t.Fatalf("unsupported representation certified: %v", err)
					}
				}
			})
		}
	}
}
