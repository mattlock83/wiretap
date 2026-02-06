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
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/phrond/evermock/internal/artifact"
)

type schemaValidationIssue struct {
	FlowID   string `json:"flow_id"`
	Channel  string `json:"channel"`
	Protocol string `json:"protocol"`
	Phase    string `json:"phase"`
	SchemaID string `json:"schema_id,omitempty"`
	Message  string `json:"message"`
}

type openapiSpec struct {
	ref    artifact.SchemaRef
	doc    *openapi3.T
	router routers.Router
}

type protoSpec struct {
	ref      artifact.SchemaRef
	services map[string]*desc.ServiceDescriptor
}

func applySchemaValidation(art *artifact.Artifact) {
	if art == nil {
		return
	}
	issues := validateSchemas(art)
	meta := map[string]any{
		"enabled": true,
		"errors":  issues,
	}
	if len(issues) == 0 {
		meta["status"] = "pass"
	} else {
		meta["status"] = "fail"
	}
	if art.Meta == nil {
		art.Meta = map[string]any{}
	}
	art.Meta["schema_validation"] = meta

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

func validateSchemas(art *artifact.Artifact) []schemaValidationIssue {
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
	protoByRole := map[string][]protoSpec{}
	var issues []schemaValidationIssue

	for role, schemas := range roleSchemas {
		for _, schema := range schemas {
			switch strings.ToLower(schema.Type) {
			case "openapi":
				spec, err := loadOpenAPISpec(schema)
				if err != nil {
					issues = append(issues, schemaValidationIssue{Protocol: "http", Channel: roleToChannel(role), Phase: "schema", SchemaID: schema.ID, Message: err.Error()})
					continue
				}
				openapiByRole[role] = append(openapiByRole[role], spec)
			case "protobuf":
				spec, err := loadProtoSpec(schema)
				if err != nil {
					issues = append(issues, schemaValidationIssue{Protocol: "grpc", Channel: roleToChannel(role), Phase: "schema", SchemaID: schema.ID, Message: err.Error()})
					continue
				}
				protoByRole[role] = append(protoByRole[role], spec)
			}
		}
	}

	for _, flow := range art.Traffic {
		role := roleForChannel(flow.Channel)
		switch strings.ToLower(flow.Protocol) {
		case "http":
			if flow.Request.HTTP == nil || flow.Response.HTTP == nil {
				continue
			}
			candidates := openapiByRole[role]
			if len(candidates) == 0 {
				issues = append(issues, schemaValidationIssue{FlowID: flow.ID, Channel: flow.Channel, Protocol: "http", Phase: "schema", Message: fmt.Sprintf("no openapi schema registered for role %s", role)})
				continue
			}
			issues = append(issues, validateHTTPFlow(flow, candidates)...)
		case "grpc":
			if flow.Request.GRPC == nil || flow.Response.GRPC == nil {
				continue
			}
			candidates := protoByRole[role]
			if len(candidates) == 0 {
				issues = append(issues, schemaValidationIssue{FlowID: flow.ID, Channel: flow.Channel, Protocol: "grpc", Phase: "schema", Message: fmt.Sprintf("no protobuf schema registered for role %s", role)})
				continue
			}
			issues = append(issues, validateGRPCFlow(flow, candidates)...)
		}
	}

	return issues
}

func validateHTTPFlow(flow artifact.Flow, specs []openapiSpec) []schemaValidationIssue {
	var issues []schemaValidationIssue
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
		issues = append(issues, schemaValidationIssue{FlowID: flow.ID, Channel: flow.Channel, Protocol: "http", Phase: "request", Message: strings.Join(reqErrs, "; ")})
	}
	if len(respErrs) > 0 {
		issues = append(issues, schemaValidationIssue{FlowID: flow.ID, Channel: flow.Channel, Protocol: "http", Phase: "response", Message: strings.Join(respErrs, "; ")})
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
			MultiError: true,
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
			MultiError: true,
		},
	}
	if err := openapi3filter.ValidateResponse(context.Background(), resp); err != nil {
		return nil, err
	}
	return nil, nil
}

func validateGRPCFlow(flow artifact.Flow, specs []protoSpec) []schemaValidationIssue {
	var issues []schemaValidationIssue
	var errs []string
	for _, spec := range specs {
		if err := validateGRPCAgainstSpec(flow, spec); err == nil {
			return nil
		} else {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	message := strings.Join(errs, "; ")
	issues = append(issues, schemaValidationIssue{FlowID: flow.ID, Channel: flow.Channel, Protocol: "grpc", Phase: "request", Message: message})
	issues = append(issues, schemaValidationIssue{FlowID: flow.ID, Channel: flow.Channel, Protocol: "grpc", Phase: "response", Message: message})
	return issues
}

func validateGRPCAgainstSpec(flow artifact.Flow, spec protoSpec) error {
	if flow.GRPC == nil {
		return errors.New("missing grpc metadata")
	}
	service := flow.GRPC.Service
	method := flow.GRPC.Method
	if service == "" || method == "" {
		return errors.New("missing grpc service or method")
	}
	serviceDesc := spec.services[service]
	if serviceDesc == nil {
		return fmt.Errorf("service %s not found", service)
	}
	methodDesc := serviceDesc.FindMethodByName(method)
	if methodDesc == nil {
		return fmt.Errorf("method %s not found", method)
	}

	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
	reqBytes := bodyBytes(flow.Request.GRPC.Body)
	if len(reqBytes) > 0 {
		if err := reqMsg.Unmarshal(reqBytes); err != nil {
			return fmt.Errorf("request decode failed: %w", err)
		}
	}

	respBytes := bodyBytes(flow.Response.GRPC.Body)
	if flow.GRPC.Status == 0 && len(respBytes) > 0 {
		respMsg := dynamic.NewMessage(methodDesc.GetOutputType())
		if err := respMsg.Unmarshal(respBytes); err != nil {
			return fmt.Errorf("response decode failed: %w", err)
		}
	}
	return nil
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

func loadProtoSpec(ref artifact.SchemaRef) (protoSpec, error) {
	path := strings.TrimSpace(ref.Source.Path)
	urlStr := strings.TrimSpace(ref.Source.URL)
	content := strings.TrimSpace(ref.Source.Content)

	cleanup := func() {}
	var err error

	switch strings.ToLower(ref.Source.Type) {
	case "file":
		if path == "" {
			return protoSpec{}, errors.New("protobuf schema path required")
		}
		path = resolvePath(path)
	case "url":
		if urlStr == "" {
			return protoSpec{}, errors.New("protobuf schema url required")
		}
		path, cleanup, err = downloadToTemp(urlStr, "schema-*.proto")
		if err != nil {
			return protoSpec{}, err
		}
	case "inline":
		if content == "" {
			return protoSpec{}, errors.New("protobuf schema content required")
		}
		path, cleanup, err = writeTempContent([]byte(content), "schema-*.proto")
		if err != nil {
			return protoSpec{}, err
		}
	default:
		return protoSpec{}, errors.New("unsupported protobuf source type")
	}
	defer cleanup()

	parser := protoparse.Parser{ImportPaths: []string{filepath.Dir(path)}}
	files, err := parser.ParseFiles(filepath.Base(path))
	if err != nil {
		return protoSpec{}, err
	}
	services := map[string]*desc.ServiceDescriptor{}
	for _, file := range files {
		for _, service := range file.GetServices() {
			services[service.GetFullyQualifiedName()] = service
		}
	}
	if len(services) == 0 {
		return protoSpec{}, errors.New("no services found in proto schema")
	}
	return protoSpec{ref: ref, services: services}, nil
}

func downloadToTemp(urlStr, pattern string) (string, func(), error) {
	resp, err := http.Get(urlStr)
	if err != nil {
		return "", func() {}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", func() {}, fmt.Errorf("download failed: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", func() {}, err
	}
	return writeTempContent(data, pattern)
}

func writeTempContent(data []byte, pattern string) (string, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", func() {}, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	return file.Name(), cleanup, nil
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

func roleForChannel(channel string) string {
	switch channel {
	case "sut_to_mock":
		return "fake"
	default:
		return "sut"
	}
}

func roleToChannel(role string) string {
	if role == "fake" {
		return "sut_to_mock"
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
