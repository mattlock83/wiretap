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
	"os"
	"path/filepath"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/phrond/evermock/internal/artifact"
)

type openapiSpec struct {
	ref    artifact.SchemaRef
	doc    *openapi3.T
	router routers.Router
}

func applySchemaValidation(art *artifact.Artifact) {
	if art == nil {
		return
	}
	if len(art.Schemas) == 0 {
		// Public APIs permit this only for explicit schema generation. Direct
		// store callers retain a truthful skipped result.
		art.SchemaValidation = &artifact.SchemaValidation{Status: "skipped"}
		if art.Meta == nil {
			art.Meta = map[string]any{}
		}
		art.Meta["schema_validation"] = map[string]any{"status": "skipped"}
		return
	}
	issues := validateSchemas(art)
	result := &artifact.SchemaValidation{Status: "pass"}
	if len(issues) > 0 {
		result.Status = "fail"
		result.Errors = issues
	}
	if art.Meta == nil {
		art.Meta = map[string]any{}
	}
	art.Meta["schema_validation"] = map[string]any{
		"status": result.Status,
		"errors": result.Errors,
	}
	art.SchemaValidation = result

	if len(issues) > 0 {
		for i, issue := range issues {
			assertion := artifact.Assertion{
				ID:       fmt.Sprintf("schema-%d", i+1),
				Channel:  issue.Channel,
				Phase:    issue.Phase,
				FlowID:   issue.FlowID,
				PathLang: issue.Protocol,
				Operator: "schema",
				Result:   "fail",
				Message:  issue.Message,
			}
			if issue.SchemaID != "" {
				assertion.Field = issue.SchemaID
			}
			art.Assertions = append(art.Assertions, assertion)
		}
		if strings.EqualFold(art.TestCase.Status, "pass") || art.TestCase.Status == "" {
			art.TestCase.Status = "fail"
		}
	}
}

func validateSchemaSetup(schemas []artifact.SchemaRef, generate bool) error {
	if len(schemas) == 0 {
		if generate {
			return nil
		}
		return errors.New("an OpenAPI schema is required; supply schemas or explicitly opt in with generate_schema")
	}
	if generate {
		return errors.New("schemas and generate_schema cannot be used together")
	}
	for index, schema := range schemas {
		if !strings.EqualFold(strings.TrimSpace(schema.Type), "openapi") {
			return fmt.Errorf("schema %d (%q) must have type openapi", index+1, schema.ID)
		}
		if _, err := loadOpenAPISpec(schema); err != nil {
			return fmt.Errorf("OpenAPI schema %q is invalid or unreadable: %w", schema.ID, err)
		}
	}
	return nil
}

func validateSchemas(art *artifact.Artifact) []artifact.SchemaValidationError {
	if art == nil {
		return nil
	}
	roleSchemas := map[string][]artifact.SchemaRef{}
	for _, schema := range art.Schemas {
		role := strings.TrimSpace(schema.Role)
		if role == "" {
			role = "sut"
		}
		roleSchemas[role] = append(roleSchemas[role], schema)
	}

	openapiByRole := map[string][]openapiSpec{}
	var issues []artifact.SchemaValidationError

	for role, schemas := range roleSchemas {
		for _, schema := range schemas {
			typ := strings.ToLower(strings.TrimSpace(schema.Type))
			switch typ {
			case "openapi":
				spec, err := loadOpenAPISpec(schema)
				if err != nil {
					issues = append(issues, artifact.SchemaValidationError{
						Protocol: "http",
						Channel:  roleToChannel(role),
						Phase:    "schema",
						SchemaID: schema.ID,
						Message:  err.Error(),
					})
					continue
				}
				openapiByRole[role] = append(openapiByRole[role], spec)
			case "":
				issues = append(issues, artifact.SchemaValidationError{
					Protocol: typ,
					Channel:  roleToChannel(role),
					Phase:    "schema",
					SchemaID: schema.ID,
					Message:  fmt.Sprintf("schema type %q is not supported; use openapi", schema.Type),
				})
			default:
				issues = append(issues, artifact.SchemaValidationError{
					Protocol: typ,
					Channel:  roleToChannel(role),
					Phase:    "schema",
					SchemaID: schema.ID,
					Message:  fmt.Sprintf("unknown schema type %q", schema.Type),
				})
			}
		}
	}

	for _, flow := range art.Traffic {
		if !strings.EqualFold(flow.Protocol, "http") {
			continue
		}
		if flow.Request.HTTP == nil || flow.Response.HTTP == nil {
			continue
		}
		const role = "sut"
		candidates := openapiByRole[role]
		if len(candidates) == 0 {
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

func validateHTTPFlow(flow artifact.Flow, specs []openapiSpec) []artifact.SchemaValidationError {
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

func validateHTTPAgainstSpec(flow artifact.Flow, spec openapiSpec) (error, error) {
	msg := flow.Request.HTTP
	respMsg := flow.Response.HTTP
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

	router := spec.router
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
			MultiError:         true,
			ExcludeRequestBody: msg.Body != nil && msg.Body.Truncated,
		},
	}
	if err := openapi3filter.ValidateRequest(context.Background(), input); err != nil {
		return err, nil
	}

	respBody := bodyBytes(respMsg.Body)
	resp := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input,
		Status:                 respMsg.Status,
		Header:                 toHTTPHeader(respMsg.Headers),
		Body:                   io.NopCloser(bytes.NewReader(respBody)),
		Options: &openapi3filter.Options{
			MultiError:          true,
			ExcludeResponseBody: respMsg.Body != nil && respMsg.Body.Truncated,
		},
	}
	if err := openapi3filter.ValidateResponse(context.Background(), resp); err != nil {
		return nil, err
	}
	return nil, nil
}

func loadOpenAPISpec(ref artifact.SchemaRef) (openapiSpec, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	var doc *openapi3.T
	var err error
	path := strings.TrimSpace(ref.Source.Path)
	urlStr := strings.TrimSpace(ref.Source.URL)
	content := strings.TrimSpace(ref.Source.Content)

	switch strings.ToLower(ref.Source.Type) {
	case "file":
		if path == "" {
			return openapiSpec{}, errors.New("openapi schema path required")
		}
		path = resolvePath(path)
		doc, err = loader.LoadFromFile(path)
	case "url":
		if urlStr == "" {
			return openapiSpec{}, errors.New("openapi schema url required")
		}
		uri, parseErr := url.Parse(urlStr)
		if parseErr != nil {
			return openapiSpec{}, parseErr
		}
		doc, err = loader.LoadFromURI(uri)
	case "inline":
		if content == "" {
			return openapiSpec{}, errors.New("openapi schema content required")
		}
		doc, err = loader.LoadFromData([]byte(content))
	default:
		return openapiSpec{}, errors.New("unsupported openapi source type")
	}
	if err != nil {
		return openapiSpec{}, err
	}
	if err := doc.Validate(context.Background()); err != nil {
		return openapiSpec{}, err
	}

	router, err := legacy.NewRouter(doc)
	if err != nil {
		return openapiSpec{}, err
	}
	return openapiSpec{ref: ref, doc: doc, router: router}, nil
}

func resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	return filepath.Join(cwd, path)
}

func roleToChannel(role string) string {
	return "test_to_sut"
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
