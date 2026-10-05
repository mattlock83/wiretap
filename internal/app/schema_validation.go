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
	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/grpccontract"
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
		// Direct store callers without schemas retain a truthful skipped result.
		art.SchemaValidation = &artifact.SchemaValidation{Status: "skipped"}
		if art.Meta == nil {
			art.Meta = map[string]any{}
		}
		art.Meta["schema_validation"] = map[string]any{"status": "skipped"}
		return
	}
	issues := validateSchemas(art)
	result := &artifact.SchemaValidation{Status: "pass", Unavailable: grpcUnavailableValidation(art)}
	if len(result.Unavailable) > 0 {
		result.Status = "partial"
	}
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
	if art.Meta == nil {
		art.Meta = map[string]any{}
	}
	art.Meta["schema_validation"] = map[string]any{
		"status":      result.Status,
		"errors":      result.Errors,
		"unavailable": result.Unavailable,
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
	protobufByRole := map[string][]artifact.SchemaRef{}
	var issues []artifact.SchemaValidationError

	for role, schemas := range roleSchemas {
		for _, schema := range schemas {
			if schema.Source.Type == "omitted" {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(schema.Type))
			switch typ {
			case "protobuf":
				protobufByRole[role] = append(protobufByRole[role], schema)
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
		if flow.Protocol == "grpc" {
			issues = append(issues, validateGRPCFlow(flow, protobufByRole)...)
			continue
		}
		if !strings.EqualFold(flow.Protocol, "http") {
			continue
		}
		if flow.Request.HTTP == nil || flow.Response.HTTP == nil {
			continue
		}
		role := "sut"
		if flow.Channel == "sut_to_dependency" {
			role = "dependency"
		}
		var candidates []openapiSpec
		for _, spec := range openapiByRole[role] {
			if role == "dependency" && spec.ref.AssetID != "" && spec.ref.AssetID != flow.AssetID {
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

func validateGRPCFlow(flow artifact.Flow, byRole map[string][]artifact.SchemaRef) []artifact.SchemaValidationError {
	role := "sut"
	if flow.Channel == "sut_to_dependency" {
		role = "dependency"
	}
	issue := func(phase, id, message string) artifact.SchemaValidationError {
		return artifact.SchemaValidationError{FlowID: flow.ID, Channel: flow.Channel, Protocol: "grpc", Phase: phase, SchemaID: id, Message: message}
	}
	if flow.Request.GRPC == nil || flow.Response.GRPC == nil {
		return []artifact.SchemaValidationError{issue("capture", "", "missing gRPC request or response evidence")}
	}
	var schema *artifact.SchemaRef
	for i := range byRole[role] {
		candidate := &byRole[role][i]
		if role == "sut" || candidate.AssetID == flow.AssetID {
			schema = candidate
			break
		}
	}
	if schema == nil {
		return []artifact.SchemaValidationError{issue("schema", "", "no protobuf contract for boundary")}
	}
	data, err := os.ReadFile(schema.Source.Path)
	if err != nil {
		return []artifact.SchemaValidationError{issue("schema", schema.ID, err.Error())}
	}
	contract, err := grpccontract.Parse(data, schema.Services)
	if err != nil {
		return []artifact.SchemaValidationError{issue("schema", schema.ID, err.Error())}
	}
	method := contract.Methods[flow.Request.GRPC.Procedure]
	var issues []artifact.SchemaValidationError
	if method == nil {
		issues = append(issues, issue("request", schema.ID, "procedure is not in selected service inventory"))
	}
	for _, side := range []struct {
		name string
		msg  *artifact.GrpcMessage
	}{{"request", flow.Request.GRPC}, {"response", flow.Response.GRPC}} {
		if o := side.msg.Observation; o != nil && !o.CountsComplete && o.Error != "" {
			issues = append(issues, issue(side.name, schema.ID, o.Error))
		}
		for _, payload := range side.msg.Messages {
			if payload.DecodeError != "" && payload.DecodeState != "unavailable" {
				issues = append(issues, issue(side.name, schema.ID, fmt.Sprintf("message %d: %s", payload.Index, payload.DecodeError)))
			}
		}
	}
	if flow.Response.GRPC.HTTPStatus != http.StatusOK {
		issues = append(issues, issue("response", schema.ID, fmt.Sprintf("gRPC transport returned HTTP %d", flow.Response.GRPC.HTTPStatus)))
	}
	if flow.Response.GRPC.StatusCode < 0 {
		issues = append(issues, issue("response", schema.ID, "gRPC status is missing"))
	}
	if method != nil {
		requestCount, requestKnown := grpcObservedCount(flow.Request.GRPC)
		responseCount, responseKnown := grpcObservedCount(flow.Response.GRPC)
		if !method.IsStreamingClient() && requestKnown && requestCount != 1 {
			issues = append(issues, issue("request", schema.ID, "unary request must contain exactly one message"))
		}
		if !method.IsStreamingServer() && responseKnown && (responseCount > 1 || flow.Response.GRPC.StatusCode == 0 && responseCount != 1) {
			issues = append(issues, issue("response", schema.ID, "unary response permits at most one message and requires one on OK"))
		}
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
			// Credentials are verified by the provider. Schema conformance must
			// not fail merely because Wiretap has no authentication backend.
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
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
			MultiError:            true,
			IncludeResponseStatus: true,
			ExcludeResponseBody:   respMsg.Body != nil && respMsg.Body.Truncated,
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
	if strings.EqualFold(role, "dependency") {
		return "sut_to_dependency"
	}
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

func grpcObservedCount(msg *artifact.GrpcMessage) (uint64, bool) {
	if msg.Observation != nil {
		return msg.Observation.CompleteFrames, msg.Observation.CountsComplete && msg.Observation.EOF && msg.Observation.Partial == nil
	}
	return uint64(len(msg.Messages)), !msg.CaptureTruncated
}

func grpcUnavailableValidation(art *artifact.Artifact) []artifact.SchemaValidationError {
	var result []artifact.SchemaValidationError
	for _, flow := range art.Traffic {
		if flow.Protocol != "grpc" || flow.Interaction != nil {
			continue
		}
		for _, side := range []struct {
			name string
			msg  *artifact.GrpcMessage
		}{{"request", flow.Request.GRPC}, {"response", flow.Response.GRPC}} {
			if side.msg == nil {
				continue
			}
			add := func(reason string) {
				result = append(result, artifact.SchemaValidationError{FlowID: flow.ID, Channel: flow.Channel, Protocol: "grpc", Phase: side.name, SchemaID: side.msg.SchemaRef, Message: reason})
			}
			if !artifact.GRPCDirectionComplete(side.msg, false) {
				add("Full message validation is unavailable: direction incomplete, framing unknown, metadata limited or messages omitted")
			}
			for _, p := range side.msg.Messages {
				if p.DecodeState == "unavailable" {
					add(fmt.Sprintf("message %d: %s", p.Index, p.DecodeError))
				}
			}
		}
	}
	return result
}
