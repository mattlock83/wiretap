package assumptions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/fileuri"
	"github.com/mattlock83/wiretap/internal/openapivalidation"
)

var errHTTPContractUnavailable = errors.New("HTTP contract validation is unavailable")

const (
	maxHTTPContractFiles     = 128
	maxHTTPContractFileBytes = 16 << 20
	maxHTTPContractBytes     = 64 << 20
)

type httpContractVerifier struct{ cache map[string]*httpContract }
type httpContract struct {
	doc    *openapi3.T
	router routers.Router
}

type httpContractManifest struct {
	SetHash string `json:"set_hash"`
	Root    string `json:"root"`
	Profile string `json:"profile"`
	Type    string `json:"type"`
	Files   []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func (v *httpContractVerifier) load(ctx context.Context, ref *artifact.SchemaRef) (*httpContract, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref == nil || ref.Type != "openapi" || ref.Source.Type != "file" || ref.Source.Path == "" || len(ref.Source.SHA256) != 64 {
		return nil, fmt.Errorf("%w: a hash-pinned frozen OpenAPI file is required", errHTTPContractUnavailable)
	}
	key := ref.Source.Path + "\x00" + ref.Source.SHA256
	if contract := v.cache[key]; contract != nil {
		return contract, nil
	}
	files, values, root, err := loadHTTPContractFiles(ctx, ref)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", errHTTPContractUnavailable, err)
	}
	if err := preciseHTTPRules(ctx, values, root); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", errHTTPContractUnavailable, err)
	}
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true // The reader below is the complete access policy.
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, uri *url.URL) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if uri.Scheme != "file" || uri.RawQuery != "" {
			return nil, errors.New("OpenAPI references must stay in the frozen local contract manifest")
		}
		name, err := fileuri.Path(uri)
		if err != nil {
			return nil, err
		}
		data, found := files[filepath.Clean(name)]
		if !found {
			return nil, fmt.Errorf("OpenAPI reference %q is not in the frozen contract manifest", uri.String())
		}
		return data, nil
	}
	uri, err := fileuri.FromPath(root)
	if err != nil {
		return nil, err
	}
	doc, err := loader.LoadFromURI(uri)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: load frozen OpenAPI: %v", errHTTPContractUnavailable, err)
	}
	if !openAPI30(doc.OpenAPI) {
		return nil, fmt.Errorf("%w: only OpenAPI 3.0.x is supported (recorded %q)", errHTTPContractUnavailable, doc.OpenAPI)
	}
	if err := doc.Validate(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: invalid frozen OpenAPI: %v", errHTTPContractUnavailable, err)
	}
	// Hosts identify local test deployments. Retain declared server base paths
	// while routing all offline examples against one inert origin.
	routerDoc := *doc
	routerDoc.Servers = make(openapi3.Servers, 0, len(doc.Servers))
	for _, server := range doc.Servers {
		copy := *server
		path := server.URL
		if index := strings.Index(path, "://"); index >= 0 {
			path = path[index+3:]
			if index := strings.IndexByte(path, '/'); index >= 0 {
				path = path[index:]
			} else {
				path = ""
			}
		}
		if path != "" && !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("%w: unsupported relative OpenAPI server URL %q", errHTTPContractUnavailable, server.URL)
		}
		copy.URL = "http://wiretap.invalid" + path
		routerDoc.Servers = append(routerDoc.Servers, &copy)
	}
	router, err := legacy.NewRouter(&routerDoc)
	if err != nil {
		return nil, fmt.Errorf("%w: build OpenAPI router: %v", errHTTPContractUnavailable, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contract := &httpContract{doc: doc, router: router}
	if v.cache == nil {
		v.cache = map[string]*httpContract{}
	}
	v.cache[key] = contract
	return contract, nil
}

func openAPI30(version string) bool {
	if !strings.HasPrefix(version, "3.0.") || len(version) == 4 {
		return false
	}
	for _, digit := range version[4:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func loadHTTPContractFiles(ctx context.Context, ref *artifact.SchemaRef) (map[string][]byte, map[string]any, string, error) {
	root, err := filepath.Abs(ref.Source.Path)
	if err != nil {
		return nil, nil, "", err
	}
	setDir := filepath.Dir(root)
	manifestData, err := readHTTPContractFile(filepath.Join(setDir, "manifest.json"), 1<<20)
	if err != nil {
		return nil, nil, "", err
	}
	if _, err := decodeCollectionJSON(manifestData); err != nil {
		return nil, nil, "", fmt.Errorf("invalid contract manifest: %w", err)
	}
	var manifest httpContractManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, nil, "", err
	}
	if manifest.SetHash != filepath.Base(setDir) || manifest.Profile != "" || manifest.Type != "" || len(manifest.Files) == 0 || len(manifest.Files) > maxHTTPContractFiles {
		return nil, nil, "", errors.New("invalid or oversized frozen OpenAPI manifest")
	}
	bundleRoot := filepath.Dir(filepath.Dir(setDir))
	relRoot, err := filepath.Rel(bundleRoot, root)
	if err != nil || filepath.ToSlash(relRoot) != manifest.Root {
		return nil, nil, "", errors.New("OpenAPI manifest root does not match selected frozen schema")
	}
	files := map[string][]byte{}
	values := map[string]any{}
	var total int
	prefix := "contracts/" + manifest.SetHash + "/"
	for _, entry := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return nil, nil, "", err
		}
		if !strings.HasPrefix(entry.Path, prefix) || filepath.ToSlash(filepath.Clean(entry.Path)) != entry.Path || strings.Contains(entry.Path, "\\") || len(entry.SHA256) != 64 {
			return nil, nil, "", errors.New("invalid frozen OpenAPI manifest file path or hash")
		}
		name := filepath.Join(bundleRoot, filepath.FromSlash(entry.Path))
		if _, duplicate := files[name]; duplicate {
			return nil, nil, "", errors.New("duplicate frozen OpenAPI manifest file")
		}
		physical, err := filepath.EvalSymlinks(name)
		if err != nil {
			return nil, nil, "", err
		}
		physicalSet, err := filepath.EvalSymlinks(setDir)
		if err != nil {
			return nil, nil, "", err
		}
		rel, err := filepath.Rel(physicalSet, physical)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, nil, "", errors.New("OpenAPI manifest file escapes its frozen contract directory")
		}
		data, err := readHTTPContractFile(name, maxHTTPContractFileBytes)
		if err != nil {
			return nil, nil, "", err
		}
		total += len(data)
		if total > maxHTTPContractBytes {
			return nil, nil, "", errors.New("frozen OpenAPI contract exceeds 64 MiB")
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != entry.SHA256 {
			return nil, nil, "", fmt.Errorf("frozen OpenAPI file %q hash mismatch", entry.Path)
		}
		value, err := decodeCollectionJSON(data)
		if err != nil {
			return nil, nil, "", fmt.Errorf("invalid frozen OpenAPI JSON %q: %w", entry.Path, err)
		}
		files[name], values[name] = data, value
	}
	data, found := files[root]
	if !found {
		return nil, nil, "", errors.New("selected OpenAPI root is absent from frozen manifest")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != ref.Source.SHA256 {
		return nil, nil, "", errors.New("selected frozen OpenAPI root hash mismatch")
	}
	return files, values, root, nil
}

func readHTTPContractFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("frozen OpenAPI file %q is not a bounded regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err == nil && int64(len(data)) > maxBytes {
		err = errors.New("frozen OpenAPI file exceeds read limit")
	}
	return data, err
}

func (c *httpContract) input(flow artifact.Flow) (*openapi3filter.RequestValidationInput, error) {
	message := flow.Request.HTTP
	if message == nil || c == nil || c.router == nil {
		return nil, errors.New("HTTP request and frozen contract router are required")
	}
	var body []byte
	var err error
	if message.Body != nil {
		body, err = retainedBodyBytes(message.Body)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: request body: %v", errHTTPContractUnavailable, err)
	}
	request, err := http.NewRequest(message.Method, message.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.URL.Scheme, request.URL.Host = "http", "wiretap.invalid"
	for name, values := range message.Headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	route, params, err := c.router.FindRoute(request)
	if err != nil {
		return nil, fmt.Errorf("request does not match a declared OpenAPI operation: %w", err)
	}
	if route.PathItem == nil || route.Operation == nil {
		return nil, fmt.Errorf("%w: declared OpenAPI route is incomplete", errHTTPContractUnavailable)
	}
	if len(route.PathItem.Servers) > 0 || route.Operation.Servers != nil {
		return nil, fmt.Errorf("%w: operation or path server overrides are unsupported", errHTTPContractUnavailable)
	}
	return &openapi3filter.RequestValidationInput{Request: request, PathParams: params, Route: route,
		Options: &openapi3filter.Options{MultiError: true, SkipSettingDefaults: true, AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}, nil
}

func (c *httpContract) validate(ctx context.Context, flow artifact.Flow) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := c.input(flow)
	if err != nil {
		return err
	}
	response := flow.Response.HTTP
	if response == nil {
		return errors.New("HTTP response evidence is required")
	}
	if response.Status < 400 {
		if err := preciseHTTPBody(flow.Request.HTTP); err != nil {
			return fmt.Errorf("request: %w", err)
		}
		if err := preciseHTTPParameters(input); err != nil {
			return fmt.Errorf("request: %w", err)
		}
		if err := openapivalidation.ValidateRequest(ctx, input); err != nil {
			return fmt.Errorf("request violates OpenAPI: %w", err)
		}
	}
	if err := preciseHTTPBody(response); err != nil {
		return fmt.Errorf("response: %w", err)
	}
	if err := preciseHTTPResponseHeaders(input, response); err != nil {
		return fmt.Errorf("response: %w", err)
	}
	if response.Body != nil {
		if _, err := retainedBodyBytes(response.Body); err != nil {
			return fmt.Errorf("%w: response body: %v", errHTTPContractUnavailable, err)
		}
	}
	if err := coverage.ValidateReplayResponse(input, response); err != nil {
		return fmt.Errorf("response violates OpenAPI: %w", err)
	}
	return ctx.Err()
}

func preciseHTTPBody(message *artifact.HttpMessage) error {
	if message == nil || message.Body == nil {
		return nil
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(toContractHTTPHeader(message.Headers).Get("Content-Type"), ";", 2)[0]))
	if contentType != "application/json" && !strings.HasSuffix(contentType, "+json") {
		return nil
	}
	raw, err := retainedBodyBytes(message.Body)
	if err != nil {
		return fmt.Errorf("%w: %v", errHTTPContractUnavailable, err)
	}
	if len(raw) == 0 {
		return nil
	}
	value, err := decodeCollectionJSON(raw)
	if err != nil {
		// The validator reports ordinary JSON syntax errors. Ambiguity and
		// unsupported normalization limits cannot earn validation assurance.
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "Unicode") || strings.Contains(err.Error(), "limits") {
			return fmt.Errorf("%w: %v", errHTTPContractUnavailable, err)
		}
		return nil
	}
	return preciseHTTPNumbers(value)
}

func preciseHTTPNumbers(value any) error {
	switch value := value.(type) {
	case json.Number:
		return preciseHTTPNumber(string(value))
	case map[string]any:
		for _, child := range value {
			if err := preciseHTTPNumbers(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := preciseHTTPNumbers(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func preciseHTTPNumber(raw string) error {
	floating, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(floating, 0) || math.IsNaN(floating) {
		return fmt.Errorf("%w: numeric value exceeds the validator's reliable precision", errHTTPContractUnavailable)
	}
	original, err := canonicalNumber(strings.TrimPrefix(raw, "+"))
	if err != nil {
		return fmt.Errorf("%w: numeric value exceeds normalization limits", errHTTPContractUnavailable)
	}
	converted, err := canonicalNumber(strconv.FormatFloat(floating, 'g', -1, 64))
	if err != nil || original != converted {
		return fmt.Errorf("%w: numeric value exceeds the validator's reliable precision", errHTTPContractUnavailable)
	}
	return nil
}

func preciseHTTPRules(ctx context.Context, documents map[string]any, root string) error {
	seen := map[string]bool{}
	var walk func(any, string, string, string) error
	walk = func(value any, kind, file, location string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := file + "\x00" + location + "\x00" + kind
		if seen[key] {
			return nil
		}
		seen[key] = true
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		if reference, ok := object["$ref"].(string); ok && reference != "" {
			base, err := fileuri.FromPath(file)
			if err != nil {
				return err
			}
			ref, err := url.Parse(reference)
			if err != nil {
				return err
			}
			resolved := base.ResolveReference(ref)
			if resolved.Scheme != "file" || resolved.RawQuery != "" {
				return errors.New("OpenAPI reference is outside the frozen local contract")
			}
			name, err := fileuri.Path(resolved)
			if err != nil {
				return err
			}
			document, found := documents[filepath.Clean(name)]
			if !found {
				return errors.New("OpenAPI reference is absent from the frozen manifest")
			}
			target, err := httpContractPointer(document, resolved.Fragment)
			if err != nil {
				return err
			}
			return walk(target, kind, name, resolved.Fragment)
		}
		child := func(name, childKind string) error { return walk(object[name], childKind, file, location+"/"+name) }
		named := func(name, childKind string) error {
			entries, _ := object[name].(map[string]any)
			for key, value := range entries {
				if err := walk(value, childKind, file, location+"/"+name+"/"+httpContractPointerToken(key)); err != nil {
					return err
				}
			}
			return nil
		}
		array := func(name, childKind string) error {
			entries, _ := object[name].([]any)
			for index, value := range entries {
				if err := walk(value, childKind, file, location+"/"+name+"/"+strconv.Itoa(index)); err != nil {
					return err
				}
			}
			return nil
		}
		switch kind {
		case "document":
			if err := named("paths", "path"); err != nil {
				return err
			}
			return child("components", "components")
		case "components":
			for name, childKind := range map[string]string{"schemas": "schema", "parameters": "parameter", "headers": "parameter", "responses": "response", "requestBodies": "body"} {
				if err := named(name, childKind); err != nil {
					return err
				}
			}
		case "path":
			if err := array("parameters", "parameter"); err != nil {
				return err
			}
			for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
				if err := child(method, "operation"); err != nil {
					return err
				}
			}
		case "operation":
			if err := array("parameters", "parameter"); err != nil {
				return err
			}
			if err := child("requestBody", "body"); err != nil {
				return err
			}
			return named("responses", "response")
		case "parameter":
			if err := child("schema", "schema"); err != nil {
				return err
			}
			return child("content", "content")
		case "body":
			return child("content", "content")
		case "response":
			if err := named("headers", "parameter"); err != nil {
				return err
			}
			return child("content", "content")
		case "content":
			for media, value := range object {
				if err := walk(value, "media", file, location+"/"+httpContractPointerToken(media)); err != nil {
					return err
				}
			}
		case "media":
			return child("schema", "schema")
		case "schema":
			if nullable, _ := object["nullable"].(bool); nullable {
				for _, name := range []string{"enum", "not", "allOf", "anyOf", "oneOf"} {
					if _, present := object[name]; present {
						return errors.New("nullable schemas with enum, not or composition have unsupported null-validation semantics")
					}
				}
			}
			for _, name := range []string{"enum", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties"} {
				if err := preciseHTTPNumbers(object[name]); err != nil {
					return fmt.Errorf("OpenAPI %s constraint: %w", name, err)
				}
			}
			if err := named("properties", "schema"); err != nil {
				return err
			}
			for _, name := range []string{"items", "additionalProperties", "not"} {
				if err := child(name, "schema"); err != nil {
					return err
				}
			}
			for _, name := range []string{"allOf", "anyOf", "oneOf"} {
				if err := array(name, "schema"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(documents[root], "document", root, "")
}

func httpContractPointerToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func httpContractPointer(value any, fragment string) (any, error) {
	if fragment == "" {
		return value, nil
	}
	if !strings.HasPrefix(fragment, "/") {
		return nil, errors.New("OpenAPI reference has an unsupported fragment")
	}
	for _, token := range strings.Split(fragment[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch object := value.(type) {
		case map[string]any:
			var found bool
			value, found = object[token]
			if !found {
				return nil, errors.New("OpenAPI reference points to an unavailable value")
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(object) {
				return nil, errors.New("OpenAPI reference points to an unavailable array item")
			}
			value = object[index]
		default:
			return nil, errors.New("OpenAPI reference points through a scalar")
		}
	}
	return value, nil
}

func preciseHTTPParameters(input *openapi3filter.RequestValidationInput) error {
	parameters := map[string]*openapi3.Parameter{}
	for _, list := range []openapi3.Parameters{input.Route.PathItem.Parameters, input.Route.Operation.Parameters} {
		for _, ref := range list {
			if ref != nil && ref.Value != nil {
				parameter := ref.Value
				parameters[parameter.In+"/"+parameter.Name] = parameter
			}
		}
	}
	for _, parameter := range parameters {
		if len(parameter.Content) > 0 {
			return fmt.Errorf("%w: content-encoded parameter %q is unsupported", errHTTPContractUnavailable, parameter.Name)
		}
		if !httpNumericParameter(parameter.Schema, map[*openapi3.Schema]bool{}) {
			continue
		}
		if httpObjectParameter(parameter.Schema, map[*openapi3.Schema]bool{}) {
			return fmt.Errorf("%w: complex numeric parameter %q serialization is unsupported", errHTTPContractUnavailable, parameter.Name)
		}
		var values []string
		switch parameter.In {
		case "query":
			values = input.Request.URL.Query()[parameter.Name]
		case "header":
			values = input.Request.Header.Values(parameter.Name)
		case "path":
			values = []string{input.PathParams[parameter.Name]}
		case "cookie":
			for _, cookie := range input.Request.Cookies() {
				if cookie.Name == parameter.Name {
					values = append(values, cookie.Value)
				}
			}
		}
		for _, value := range values {
			for _, scalar := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '|' || r == '=' || r == ' ' }) {
				if _, err := strconv.ParseFloat(scalar, 64); err == nil || errors.Is(err, strconv.ErrRange) {
					if err := preciseHTTPNumber(scalar); err != nil {
						return fmt.Errorf("parameter %q: %w", parameter.Name, err)
					}
				}
			}
		}
	}
	return nil
}

func preciseHTTPResponseHeaders(input *openapi3filter.RequestValidationInput, message *artifact.HttpMessage) error {
	responses := input.Route.Operation.Responses
	if responses == nil {
		return nil
	}
	ref := responses.Status(message.Status)
	if ref == nil {
		ref = responses.Default()
	}
	if ref == nil || ref.Value == nil {
		return nil
	}
	headers := toContractHTTPHeader(message.Headers)
	for name, ref := range ref.Value.Headers {
		if ref == nil || ref.Value == nil {
			continue
		}
		if len(ref.Value.Content) > 0 {
			return fmt.Errorf("%w: content-encoded response header %q is unsupported", errHTTPContractUnavailable, name)
		}
		if !httpNumericParameter(ref.Value.Schema, map[*openapi3.Schema]bool{}) {
			continue
		}
		if httpObjectParameter(ref.Value.Schema, map[*openapi3.Schema]bool{}) {
			return fmt.Errorf("%w: complex numeric response header %q serialization is unsupported", errHTTPContractUnavailable, name)
		}
		for _, value := range headers.Values(name) {
			for _, scalar := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '|' || r == '=' || r == ' ' }) {
				if _, err := strconv.ParseFloat(scalar, 64); err == nil || errors.Is(err, strconv.ErrRange) {
					if err := preciseHTTPNumber(scalar); err != nil {
						return fmt.Errorf("header %q: %w", name, err)
					}
				}
			}
		}
	}
	return nil
}

func httpNumericParameter(ref *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) bool {
	if ref == nil || ref.Value == nil || seen[ref.Value] {
		return false
	}
	schema := ref.Value
	seen[schema] = true
	if schema.Type.Includes("number") || schema.Type.Includes("integer") {
		return true
	}
	for _, value := range schema.Enum {
		if _, ok := value.(float64); ok {
			return true
		}
	}
	for _, list := range []openapi3.SchemaRefs{schema.AllOf, schema.AnyOf, schema.OneOf} {
		for _, ref := range list {
			if httpNumericParameter(ref, seen) {
				return true
			}
		}
	}
	if httpNumericParameter(schema.Items, seen) {
		return true
	}
	for _, ref := range schema.Properties {
		if httpNumericParameter(ref, seen) {
			return true
		}
	}
	return false
}

func httpObjectParameter(ref *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) bool {
	if ref == nil || ref.Value == nil || seen[ref.Value] {
		return false
	}
	schema := ref.Value
	seen[schema] = true
	if schema.Type.Includes("object") || len(schema.Properties) > 0 {
		return true
	}
	for _, list := range []openapi3.SchemaRefs{schema.AllOf, schema.AnyOf, schema.OneOf} {
		for _, ref := range list {
			if httpObjectParameter(ref, seen) {
				return true
			}
		}
	}
	return httpObjectParameter(schema.Items, seen)
}

func toContractHTTPHeader(headers artifact.Headers) http.Header {
	result := http.Header{}
	for name, values := range headers {
		for _, value := range values {
			result.Add(name, value)
		}
	}
	return result
}
