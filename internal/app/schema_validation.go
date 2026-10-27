package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/openapivalidation"
)

func applySchemaValidation(art *artifact.Artifact, specs []bundle.PreparedSchema) {
	if art == nil {
		return
	}
	if len(art.Schemas) == 0 {
		// Direct store callers without schemas retain a truthful skipped result.
		art.SchemaValidation = &artifact.SchemaValidation{Status: "skipped"}
		return
	}
	issues := validateSchemas(art, specs)
	result := &artifact.SchemaValidation{Status: "pass"}
	for _, schema := range art.Schemas {
		if schema.Source.Type == "omitted" {
			result.Skipped = append(result.Skipped, schema.ID)
		}
	}
	if len(result.Skipped) > 0 {
		result.Status = "partial"
		if len(result.Skipped) == len(art.Schemas) {
			result.Status = "skipped"
		}
	}

	if len(issues) > 0 {
		result.Status = "fail"
		result.Errors = issues
	}
	// Validation is evidence; the framework alone owns the test outcome.
	art.SchemaValidation = result
}

func validateSchemaSetup(schemas []artifact.SchemaRef, allowMissing bool) error {
	if len(schemas) == 0 {
		if allowMissing {
			return nil
		}
		return errors.New("an OpenAPI schema is required; supply schemas or explicitly set schema_required: false for this API")
	}
	for index, schema := range schemas {
		if !strings.EqualFold(strings.TrimSpace(schema.Type), "openapi") {
			return fmt.Errorf("schema %d (%q) must have type openapi", index+1, schema.ID)
		}
	}
	return nil
}

func validateSchemas(art *artifact.Artifact, specs []bundle.PreparedSchema) []artifact.SchemaValidationError {
	var issues []artifact.SchemaValidationError
	openapiByRole := map[string][]bundle.PreparedSchema{}
	for _, spec := range specs {
		if spec.Router == nil {
			continue
		}
		role := spec.Ref.Role
		if role == "" {
			role = "sut"
		}
		openapiByRole[role] = append(openapiByRole[role], spec)
	}

	for _, flow := range art.Traffic {
		if !strings.EqualFold(flow.Protocol, "http") {
			continue
		}
		if flow.Request == nil || flow.Response == nil {
			continue
		}
		role := "sut"
		if flow.Channel == "sut_to_dependency" {
			role = "dependency"
		}
		var candidates []bundle.PreparedSchema
		for _, spec := range openapiByRole[role] {
			if role == "dependency" && spec.Ref.AssetID != "" && spec.Ref.AssetID != flow.AssetID {
				continue
			}
			candidates = append(candidates, spec)
		}
		if len(candidates) == 0 {
			if omittedForFlow(art.Schemas, role, flow.AssetID) {
				continue
			}
			issues = append(issues, artifact.SchemaValidationError{
				FlowID:   flow.ID,
				Channel:  flow.Channel,
				Protocol: "http",
				Phase:    "schema",
				Message:  fmt.Sprintf("no openapi schema registered for role %s", role),
			})
			continue
		}
		issues = append(issues, validateHTTPFlow(flow, candidates)...)
	}

	return issues
}

func validateHTTPFlow(flow artifact.Flow, specs []bundle.PreparedSchema) []artifact.SchemaValidationError {
	var issues []artifact.SchemaValidationError
	var reqErrs []string
	var respErrs []string
	for _, spec := range specs {
		reqErr, respErr := validateHTTPAgainstSpec(flow, spec)
		if reqErr == nil && respErr == nil {
			return nil
		}
		if reqErr != nil {
			reqErrs = append(reqErrs, reqErr.Error())
		}
		if respErr != nil {
			respErrs = append(respErrs, respErr.Error())
		}
	}
	if len(reqErrs) > 0 {
		issues = append(issues, artifact.SchemaValidationError{
			FlowID:   flow.ID,
			Channel:  flow.Channel,
			Protocol: "http",
			Phase:    "request",
			Message:  strings.Join(reqErrs, "; "),
		})
	}
	if len(respErrs) > 0 {
		issues = append(issues, artifact.SchemaValidationError{
			FlowID:   flow.ID,
			Channel:  flow.Channel,
			Protocol: "http",
			Phase:    "response",
			Message:  strings.Join(respErrs, "; "),
		})
	}
	return issues
}

func validateHTTPAgainstSpec(flow artifact.Flow, spec bundle.PreparedSchema) (error, error) {
	msg := flow.Request
	respMsg := flow.Response
	if msg == nil || respMsg == nil {
		return errors.New("missing http messages"), nil
	}
	requestURL := msg.URL
	if requestURL == "" {
		return errors.New("missing request url"), nil
	}
	parsed, err := url.Parse(requestURL)
	if err != nil || parsed.Scheme == "" {
		parsed, err = url.Parse("http://" + strings.TrimPrefix(requestURL, "//"))
	}
	if err != nil {
		return fmt.Errorf("invalid request url: %v", err), nil
	}

	reqBody := bodyBytes(msg.Body)
	req, err := http.NewRequest(msg.Method, parsed.String(), io.NopCloser(bytes.NewReader(reqBody)))
	if err != nil {
		return err, nil
	}
	for key, values := range msg.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	router := spec.Router
	if router == nil {
		return errors.New("openapi router not initialized"), nil
	}
	route, params, err := router.FindRoute(req)
	if err != nil {
		return err, nil
	}

	input := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: params,
		Route:      route,
		Options: &openapi3filter.Options{
			// Credentials are verified by the provider. Schema conformance must
			// not fail merely because Wiretap has no authentication backend.
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			MultiError:         true,
			ExcludeRequestBody: msg.Body != nil && msg.Body.Truncated,
		},
	}
	requestErr := openapivalidation.ValidateRequest(context.Background(), input)

	respBody := bodyBytes(respMsg.Body)
	resp := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input,
		Status:                 respMsg.Status,
		Header:                 toHTTPHeader(respMsg.Headers),
		Body:                   io.NopCloser(bytes.NewReader(respBody)),
		Options: &openapi3filter.Options{
			MultiError:            true,
			IncludeResponseStatus: true,
			ExcludeResponseBody:   respMsg.Body != nil && respMsg.Body.Truncated,
		},
	}
	return requestErr, openapivalidation.ValidateResponse(context.Background(), resp)
}

func bodyBytes(body *artifact.Body) []byte {
	if body == nil {
		return nil
	}
	if body.Encoding == "base64" && body.Bytes != "" {
		decoded, err := base64.StdEncoding.DecodeString(body.Bytes)
		if err == nil {
			return decoded
		}
	}
	if body.Text != "" {
		return []byte(body.Text)
	}
	if body.Bytes != "" {
		decoded, err := base64.StdEncoding.DecodeString(body.Bytes)
		if err == nil {
			return decoded
		}
	}
	return nil
}

func toHTTPHeader(headers artifact.Headers) http.Header {
	out := http.Header{}
	for key, values := range headers {
		for _, value := range values {
			out.Add(key, value)
		}
	}
	return out
}
