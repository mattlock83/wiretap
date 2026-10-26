package assumptions

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func httpRulesDocument(phase string, rule map[string]any) map[string]any {
	requestSchema, responseSchema := map[string]any{"type": "object"}, map[string]any{"type": "object"}
	selected := map[string]any{"type": "object", "required": []any{"value"}, "properties": map[string]any{"value": rule}}
	if phase == "request" {
		requestSchema = selected
	} else {
		responseSchema = selected
	}
	return map[string]any{
		"openapi": "3.0.3", "info": map[string]any{"title": "Inventory", "version": "1"},
		"paths": map[string]any{"/items": map[string]any{"post": map[string]any{
			"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": requestSchema}}},
			"responses":   map[string]any{"200": map[string]any{"description": "ok", "content": map[string]any{"application/json": map[string]any{"schema": responseSchema}}}},
		}}},
	}
}

func httpRulesOperation(document map[string]any) map[string]any {
	return document["paths"].(map[string]any)["/items"].(map[string]any)["post"].(map[string]any)
}

func httpRulesResponse(document map[string]any) map[string]any {
	return httpRulesOperation(document)["responses"].(map[string]any)["200"].(map[string]any)
}

func httpRulesCapture(t *testing.T, provider bool, document map[string]any, value string) *artifact.Artifact {
	t.Helper()
	flow := checkFixtureFlow()
	flow.Request.HTTP.URL = "http://consumer.test/items?tenant=shop"
	if provider {
		flow.Request.HTTP.URL = "https://provider.test/items?tenant=shop"
	}
	flow.Request.HTTP.Headers["X-Tenant"] = []string{"shop"}
	flow.Response.HTTP.Headers["X-Rule"] = []string{"shared"}
	flow.Request.HTTP.Body.Text, flow.Response.HTTP.Body.Text = `{"value":`+value+`}`, `{"value":`+value+`}`
	capture := checkCapture(provider, flow)
	if document != nil {
		operation := httpRulesOperation(document)
		parameters, _ := operation["parameters"].([]any)
		declared := false
		for _, value := range parameters {
			parameter := value.(map[string]any)
			if parameter["name"] == "tenant" && parameter["in"] == "query" {
				declared = true
			}
		}
		if !declared {
			operation["parameters"] = append(parameters, map[string]any{"name": "tenant", "in": "query", "schema": map[string]any{"type": "string"}})
		}
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		ref := artifact.SchemaRef{ID: "inventory", Type: "openapi", Role: "dependency", AssetID: "stock", Source: artifact.SchemaSource{Type: "inline", Content: string(data)}}
		if provider {
			ref.Role, ref.AssetID = "sut", "inventory-api"
		}
		capture.Schemas = []artifact.SchemaRef{ref}
	}
	return capture
}

func httpRulesPair(t *testing.T, consumer, provider map[string]any, value string) Options {
	t.Helper()
	return checkHTTPPair(t, []*artifact.Artifact{httpRulesCapture(t, false, consumer, value)}, []*artifact.Artifact{httpRulesCapture(t, true, provider, value)})
}

func assertHTTPRulesMismatch(t *testing.T, options Options) {
	t.Helper()
	for _, mode := range []string{"consumer", "provider"} {
		t.Run(mode, func(t *testing.T) {
			options.Mode = mode
			report := mustCheck(t, options)
			if report.Compatible || report.Counts.Verified != 0 || report.Counts.Mismatched != 1 || !findingCode(report, "http_contract_mismatch") {
				t.Fatalf("declared rule drift with shared valid values was certified in %s mode: %+v", mode, report)
			}
			if len(report.Findings) != 1 || len(report.Findings[0].Differences) == 0 || !strings.HasPrefix(report.Findings[0].Differences[0], "contract/") {
				t.Fatalf("rule drift has no field-level contract diagnostic: %+v", report.Findings)
			}
		})
	}
}

func TestCheckHTTPValidationRuleDriftWithSharedValidValues(t *testing.T) {
	for _, tc := range []struct {
		name, value        string
		consumer, provider map[string]any
	}{
		{"integer versus number", `3`, map[string]any{"type": "integer"}, map[string]any{"type": "number"}},
		{"int32 versus int64 format", `3`, map[string]any{"type": "integer", "format": "int32"}, map[string]any{"type": "integer", "format": "int64"}},
		{"enum addition", `"ready"`, map[string]any{"type": "string", "enum": []any{"ready", "pending"}}, map[string]any{"type": "string", "enum": []any{"ready", "pending", "cancelled"}}},
		{"enum removal", `"ready"`, map[string]any{"type": "string", "enum": []any{"ready", "pending"}}, map[string]any{"type": "string", "enum": []any{"ready"}}},
		{"minimum string length", `"shared"`, map[string]any{"type": "string", "minLength": 1}, map[string]any{"type": "string", "minLength": 2}},
		{"maximum string length", `"shared"`, map[string]any{"type": "string", "maxLength": 8}, map[string]any{"type": "string", "maxLength": 10}},
		{"minimum array count", `[1,2,3]`, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 1}, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 2}},
		{"maximum array count", `[1,2,3]`, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "maxItems": 3}, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "maxItems": 4}},
		{"numeric minimum", `3`, map[string]any{"type": "number", "minimum": 1}, map[string]any{"type": "number", "minimum": 2}},
		{"numeric maximum", `3`, map[string]any{"type": "number", "maximum": 10}, map[string]any{"type": "number", "maximum": 20}},
		{"exclusive minimum", `3`, map[string]any{"type": "number", "minimum": 1, "exclusiveMinimum": false}, map[string]any{"type": "number", "minimum": 1, "exclusiveMinimum": true}},
		{"exclusive maximum", `3`, map[string]any{"type": "number", "maximum": 10, "exclusiveMaximum": false}, map[string]any{"type": "number", "maximum": 10, "exclusiveMaximum": true}},
		{"numeric multiple", `6`, map[string]any{"type": "number", "multipleOf": 1}, map[string]any{"type": "number", "multipleOf": 3}},
		{"required nested property", `{"id":"shared"}`, map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}, map[string]any{"type": "object", "required": []any{"id"}, "properties": map[string]any{"id": map[string]any{"type": "string"}}}},
		{"nullable string", `"shared"`, map[string]any{"type": "string", "nullable": false}, map[string]any{"type": "string", "nullable": true}},
		{"unique array items", `[1,2]`, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "uniqueItems": false}, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "uniqueItems": true}},
		{"additional object properties", `{"id":3}`, map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}, "additionalProperties": true}, map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}, "additionalProperties": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, phase := range []string{"request", "response"} {
				t.Run(phase, func(t *testing.T) {
					assertHTTPRulesMismatch(t, httpRulesPair(t, httpRulesDocument(phase, tc.consumer), httpRulesDocument(phase, tc.provider), tc.value))
				})
			}
		})
	}
}

func TestCheckHTTPParameterAndHeaderRuleDrift(t *testing.T) {
	for _, kind := range []string{"query parameter", "request header", "response header"} {
		t.Run(kind, func(t *testing.T) {
			consumer, provider := httpRulesDocument("response", map[string]any{"type": "string"}), httpRulesDocument("response", map[string]any{"type": "string"})
			for i, document := range []map[string]any{consumer, provider} {
				rule := map[string]any{"type": "string", "minLength": i + 1}
				if kind == "response header" {
					httpRulesResponse(document)["headers"] = map[string]any{"X-Rule": map[string]any{"schema": rule}}
				} else {
					name, in := "tenant", "query"
					if kind == "request header" {
						name, in = "X-Tenant", "header"
					}
					httpRulesOperation(document)["parameters"] = []any{map[string]any{"name": name, "in": in, "required": true, "schema": rule}}
				}
			}
			assertHTTPRulesMismatch(t, httpRulesPair(t, consumer, provider, `"shared"`))
		})
	}
}

func TestCheckHTTPRulesIgnoreAnnotationsRepresentationsAndUnusedAlternatives(t *testing.T) {
	for _, kind := range []string{"enum order", "annotations defaults examples", "references versus inline", "unrelated route status media"} {
		t.Run(kind, func(t *testing.T) {
			consumerRule := map[string]any{"type": "string", "enum": []any{"ready", "pending"}}
			providerRule := map[string]any{"type": "string", "enum": []any{"ready", "pending"}}
			consumer, provider := httpRulesDocument("response", consumerRule), httpRulesDocument("response", providerRule)
			switch kind {
			case "enum order":
				providerRule["enum"] = []any{"pending", "ready"}
			case "annotations defaults examples":
				consumerRule["title"], consumerRule["description"], consumerRule["example"], consumerRule["default"] = "First", "first description", "ready", "ready"
				providerRule["title"], providerRule["description"], providerRule["example"], providerRule["default"] = "Second", "different description", "pending", "pending"
				provider["info"] = map[string]any{"title": "Renamed inventory", "version": "2"}
			case "references versus inline":
				provider["components"] = map[string]any{"schemas": map[string]any{"State": providerRule}}
				selected := httpRulesResponse(provider)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
				selected["properties"].(map[string]any)["value"] = map[string]any{"$ref": "#/components/schemas/State"}
			case "unrelated route status media":
				provider["paths"].(map[string]any)["/unrelated"] = map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "unrelated", "content": map[string]any{"text/plain": map[string]any{"schema": map[string]any{"type": "string"}}}}}}}
				httpRulesOperation(provider)["responses"].(map[string]any)["404"] = map[string]any{"description": "unused status", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "integer"}}}}
				httpRulesResponse(provider)["content"].(map[string]any)["application/xml"] = map[string]any{"schema": map[string]any{"type": "integer"}}
			}
			options := httpRulesPair(t, consumer, provider, `"ready"`)
			for _, mode := range []string{"consumer", "provider"} {
				t.Run(mode, func(t *testing.T) {
					options.Mode = mode
					report := mustCheck(t, options)
					if !report.Compatible || report.Counts.Verified != 1 || len(report.Matches) != 1 || report.Matches[0].ContractCheck != "openapi_3_0_validation_rules" {
						t.Fatalf("equivalent exercised rules were rejected due to annotations, representation or unrelated rules: %+v", report)
					}
				})
			}
		})
	}
}

func TestCheckHTTPContractAvailability(t *testing.T) {
	for _, kind := range []string{"both absent", "consumer absent", "provider absent", "OpenAPI 3.1"} {
		t.Run(kind, func(t *testing.T) {
			consumer, provider := httpRulesDocument("response", map[string]any{"type": "string"}), httpRulesDocument("response", map[string]any{"type": "string"})
			switch kind {
			case "both absent":
				consumer, provider = nil, nil
			case "consumer absent":
				consumer = nil
			case "provider absent":
				provider = nil
			case "OpenAPI 3.1":
				consumer["openapi"], provider["openapi"] = "3.1.0", "3.1.0"
			}
			options := httpRulesPair(t, consumer, provider, `"shared"`)
			for _, mode := range []string{"consumer", "provider"} {
				t.Run(mode, func(t *testing.T) {
					options.Mode = mode
					report := mustCheck(t, options)
					if kind == "both absent" || kind == "consumer absent" {
						marker := "unavailable"
						if kind == "consumer absent" {
							marker = "openapi_3_0_provider_contract"
						}
						if !report.Compatible || len(report.Matches) != 1 || report.Matches[0].ContractCheck != marker {
							t.Fatalf("example match failed or claimed the wrong contract assurance: %+v", report)
						}
					} else if report.Compatible || report.Counts.Unverified != 1 || !findingCode(report, "http_contract_unavailable") {
						t.Fatalf("unavailable contract comparison was certified: %+v", report)
					}
				})
			}
		})
	}
}

func TestCheckHTTPAmbiguousCapturedMediaTypesPreventArazzoVerification(t *testing.T) {
	for _, phase := range []string{"request", "response"} {
		for _, variant := range []string{"repeated", "case variants"} {
			t.Run(phase+"/"+variant, func(t *testing.T) {
				document := httpRulesDocument("response", map[string]any{"type": "string"})
				consumer := httpRulesCapture(t, false, nil, `"shared"`)
				provider := httpRulesCapture(t, true, document, `"shared"`)
				for _, capture := range []*artifact.Artifact{consumer, provider} {
					message := capture.Traffic[0].Request.HTTP
					if phase == "response" {
						message = capture.Traffic[0].Response.HTTP
					}
					if variant == "case variants" {
						message.Headers["content-type"] = []string{"text/plain"}
					} else {
						message.Headers["Content-Type"] = []string{"application/json", "text/plain"}
					}
				}
				options := checkHTTPPair(t, []*artifact.Artifact{consumer}, []*artifact.Artifact{provider})
				for _, mode := range []string{"consumer", "provider"} {
					options.Mode = mode
					if _, err := Check(t.Context(), options); !errors.Is(err, ErrInput) {
						t.Fatalf("ambiguous Arazzo media accepted: %v", err)
					}
				}
			})
		}
	}
}

func TestCheckHTTPProviderContractSupportsAbsentAndOmittedConsumerSchema(t *testing.T) {
	for _, omitted := range []bool{false, true} {
		name := "absent"
		if omitted {
			name = "explicitly omitted"
		}
		t.Run(name, func(t *testing.T) {
			consumer := httpRulesCapture(t, false, nil, `"shared"`)
			if omitted {
				consumer.Schemas = []artifact.SchemaRef{{ID: "stock", Type: "openapi", Role: "dependency", AssetID: "stock", Source: artifact.SchemaSource{Type: "omitted"}}}
			}
			provider := httpRulesCapture(t, true, httpRulesDocument("response", map[string]any{"type": "string", "enum": []any{"shared", "other"}, "minLength": 2, "maxLength": 10}), `"shared"`)
			options := checkHTTPPair(t, []*artifact.Artifact{consumer}, []*artifact.Artifact{provider})
			for _, mode := range []string{"consumer", "provider"} {
				t.Run(mode, func(t *testing.T) {
					options.Mode = mode
					report := mustCheck(t, options)
					if !report.Compatible || report.Counts.Verified != 1 || len(report.Matches) != 1 || report.Matches[0].ContractCheck != "openapi_3_0_provider_contract" {
						t.Fatalf("optional consumer schema prevented provider-backed comparison or claimed independent consumer rules: %+v", report)
					}
				})
			}
		})
	}
}

func TestCheckHTTPProviderOnlyContractRejectsObservedConstraintViolations(t *testing.T) {
	for _, tc := range []struct {
		name, phase, value string
		rule               map[string]any
	}{
		{"response type", "response", `"3"`, map[string]any{"type": "integer"}},
		{"response enum", "response", `"other"`, map[string]any{"type": "string", "enum": []any{"ready"}}},
		{"response length", "response", `"long"`, map[string]any{"type": "string", "maxLength": 2}},
		{"response array count", "response", `[1,2]`, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "maxItems": 1}},
		{"request type", "request", `"3"`, map[string]any{"type": "integer"}},
		{"request enum", "request", `"other"`, map[string]any{"type": "string", "enum": []any{"ready"}}},
		{"request length", "request", `"x"`, map[string]any{"type": "string", "minLength": 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := httpRulesPair(t, nil, httpRulesDocument(tc.phase, tc.rule), tc.value)
			for _, mode := range []string{"consumer", "provider"} {
				t.Run(mode, func(t *testing.T) {
					options.Mode = mode
					report := mustCheck(t, options)
					if report.Compatible || report.Counts.Verified != 0 || report.Counts.Mismatched != 1 || !findingCode(report, "http_contract_violation") {
						t.Fatalf("equal captured values violating provider constraints were certified: %+v", report)
					}
				})
			}
		})
	}
}

func TestCheckHTTPProviderContractValidatesEachActualExchange(t *testing.T) {
	for _, invalidSide := range []string{"consumer", "provider"} {
		t.Run(invalidSide, func(t *testing.T) {
			document := httpRulesDocument("response", map[string]any{"type": "string"})
			httpRulesOperation(document)["parameters"] = []any{map[string]any{"name": "X-Tenant", "in": "header", "required": true, "schema": map[string]any{"type": "string", "minLength": 3}}}
			consumer, provider := httpRulesCapture(t, false, nil, `"shared"`), httpRulesCapture(t, true, document, `"shared"`)
			invalid := consumer
			if invalidSide == "provider" {
				invalid = provider
			}
			invalid.Traffic[0].Request.HTTP.Headers["X-Tenant"] = []string{"x"}
			options := checkHTTPPair(t, []*artifact.Artifact{consumer}, []*artifact.Artifact{provider})
			// Explicit normalization exceptions do not excuse a declared rule
			// violation in either original, complete recorded exchange.
			options.IgnoreHeaders = []string{"x-tenant"}
			for _, mode := range []string{"consumer", "provider"} {
				t.Run(mode, func(t *testing.T) {
					options.Mode = mode
					report := mustCheck(t, options)
					if report.Compatible || report.Counts.Mismatched != 1 || !findingCode(report, "http_contract_violation") || !strings.HasPrefix(report.Findings[0].Message, invalidSide+" captured exchange") {
						t.Fatalf("%s values were not independently checked against provider rules: %+v", invalidSide, report)
					}
				})
			}
		})
	}
}

func TestCheckHTTPProviderOnlyContractAllowsIntentionalInvalidRequestError(t *testing.T) {
	document := httpRulesDocument("request", map[string]any{"type": "integer"})
	responses := httpRulesOperation(document)["responses"].(map[string]any)
	responses["400"] = responses["200"]
	delete(responses, "200")
	consumer, provider := httpRulesCapture(t, false, nil, `"invalid request"`), httpRulesCapture(t, true, document, `"invalid request"`)
	consumer.Traffic[0].Response.HTTP.Status, provider.Traffic[0].Response.HTTP.Status = 400, 400
	options := checkHTTPPair(t, []*artifact.Artifact{consumer}, []*artifact.Artifact{provider})
	for _, mode := range []string{"consumer", "provider"} {
		options.Mode = mode
		report := mustCheck(t, options)
		if !report.Compatible || len(report.Matches) != 1 || report.Matches[0].ContractCheck != "openapi_3_0_provider_contract" {
			t.Fatalf("provider-only fallback rejected a valid intentional 400 exchange: %+v", report)
		}
	}
}

func TestCheckHTTPHeaderCaseAmbiguityCannotHideRuleDrift(t *testing.T) {
	for _, kind := range []string{"request headers", "path operation headers", "response headers"} {
		t.Run(kind, func(t *testing.T) {
			consumer, provider := httpRulesDocument("response", map[string]any{"type": "string"}), httpRulesDocument("response", map[string]any{"type": "string"})
			for i, document := range []map[string]any{consumer, provider} {
				parameter := func(name string, minimum int) map[string]any {
					return map[string]any{"name": name, "in": "header", "required": true, "schema": map[string]any{"type": "string", "minLength": minimum}}
				}
				switch kind {
				case "request headers":
					httpRulesOperation(document)["parameters"] = []any{parameter("X-Tenant", i+1), parameter("x-tenant", 1)}
				case "path operation headers":
					document["paths"].(map[string]any)["/items"].(map[string]any)["parameters"] = []any{parameter("X-Tenant", i+1)}
					httpRulesOperation(document)["parameters"] = []any{parameter("x-tenant", 1)}
				case "response headers":
					httpRulesResponse(document)["headers"] = map[string]any{
						"X-Rule": map[string]any{"schema": map[string]any{"type": "string", "minLength": i + 1}},
						"x-rule": map[string]any{"schema": map[string]any{"type": "string", "minLength": 1}},
					}
				}
			}
			options := httpRulesPair(t, consumer, provider, `"shared"`)
			for _, mode := range []string{"consumer", "provider"} {
				t.Run(mode, func(t *testing.T) {
					options.Mode = mode
					report := mustCheck(t, options)
					if report.Compatible || report.Counts.Verified != 0 || report.Counts.Unverified != 1 || !findingCode(report, "http_contract_unavailable") {
						t.Fatalf("case-insensitive projection silently overwrote a different header rule: %+v", report)
					}
				})
			}
		})
	}
}

func TestCheckHTTPExactHeaderOverrideUsesEffectiveOperationRule(t *testing.T) {
	consumer, provider := httpRulesDocument("response", map[string]any{"type": "string"}), httpRulesDocument("response", map[string]any{"type": "string"})
	for i, document := range []map[string]any{consumer, provider} {
		parameter := func(minimum int) map[string]any {
			return map[string]any{"name": "X-Tenant", "in": "header", "required": true, "schema": map[string]any{"type": "string", "minLength": minimum}}
		}
		document["paths"].(map[string]any)["/items"].(map[string]any)["parameters"] = []any{parameter(i + 1)}
		httpRulesOperation(document)["parameters"] = []any{parameter(3)}
	}
	options := httpRulesPair(t, consumer, provider, `"shared"`)
	for _, mode := range []string{"consumer", "provider"} {
		options.Mode = mode
		if report := mustCheck(t, options); !report.Compatible || report.Counts.Verified != 1 {
			t.Fatalf("effective exact-name parameter override was confused with case ambiguity: %+v", report)
		}
	}
}
