package assumptions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/postman"
)

const (
	maxCollectionBytes  = 64 << 20
	maxCollectionsBytes = 256 << 20
	maxCollectionFiles  = 1024
	maxCollectionDepth  = 64
	maxCollectionNodes  = 100000
)

type collectionInput struct {
	path   string
	direct bool
}

// VerifyCollections checks that supplied Postman examples describe the selected
// verified bundle exports. It never executes scripts, resolves variables or reads
// request body files. Directory inputs may also contain other API boundaries.
func VerifyCollections(inputs []string, expected []postman.Export) error {
	if len(inputs) == 0 {
		return errors.New("at least one Postman collection file or directory is required")
	}
	files, err := discoverCollections(inputs)
	if err != nil {
		return err
	}
	wanted := make([]string, len(expected))
	labels := make([]string, len(expected))
	attemptIDs := map[string]bool{}
	for _, export := range expected {
		if export.Collection != nil {
			for _, folder := range export.Collection.Items {
				attemptIDs[folder.ID] = true
			}
		}
		if export.GRPC != nil {
			for _, folder := range export.GRPC.Items {
				attemptIDs[folder.ID] = true
			}
		}
	}
	binaryResponses, err := expectedBinaryResponses(expected)
	if err != nil {
		return err
	}
	for i, export := range expected {
		var data bytes.Buffer
		if err := postman.WriteExport(&data, export); err != nil {
			return fmt.Errorf("expected Postman export %q: %w", export.Filename, err)
		}
		wanted[i], err = normalizeCollection(data.Bytes(), attemptIDs, binaryResponses)
		if err != nil {
			return fmt.Errorf("expected Postman export %q: %w", export.Filename, err)
		}
		labels[i] = export.Filename
	}
	matched := make([]string, len(expected))
	var rejected []string
	var total int64
	for _, input := range files {
		data, err := readCollection(input.path)
		if err != nil {
			return fmt.Errorf("Postman collection %q: %w", input.path, err)
		}
		total += int64(len(data))
		if total > maxCollectionsBytes {
			return fmt.Errorf("Postman collections exceed %d MiB in total", maxCollectionsBytes>>20)
		}
		actual, err := normalizeCollection(data, attemptIDs, binaryResponses)
		if err != nil {
			if input.direct {
				return fmt.Errorf("Postman collection %q: %w", input.path, err)
			}
			if len(rejected) < 3 {
				rejected = append(rejected, fmt.Sprintf("%q: %s", input.path, err))
			}
			continue
		}
		found := -1
		for i, signature := range wanted {
			if signature == actual && matched[i] == "" {
				found = i
				break
			}
		}
		if found < 0 {
			if input.direct {
				return fmt.Errorf("Postman collection %q does not match a selected bundle export: examples or attempt IDs are missing, extra, stale or edited", input.path)
			}
			continue
		}
		matched[found] = input.path
	}
	for i, input := range matched {
		if input == "" {
			detail := ""
			if len(rejected) != 0 {
				detail = "; rejected directory files: " + strings.Join(rejected, "; ")
			}
			return fmt.Errorf("no supplied Postman collection in %q matches selected bundle export %q: examples or attempt IDs are missing, extra, stale or edited%s", strings.Join(inputs, ", "), labels[i], detail)
		}
	}
	return nil
}

func discoverCollections(inputs []string) ([]collectionInput, error) {
	byPath := map[string]bool{}
	add := func(path string, direct bool) error {
		byPath[path] = byPath[path] || direct
		if len(byPath) > maxCollectionFiles {
			return fmt.Errorf("Postman inputs exceed %d collection files", maxCollectionFiles)
		}
		return nil
	}
	for _, input := range inputs {
		path, err := filepath.Abs(input)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("Postman input %q: %w", input, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Postman input %q must not be a symbolic link", input)
		}
		if info.Mode().IsRegular() {
			if err := add(path, true); err != nil {
				return nil, err
			}
			continue
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("Postman input %q must be a regular file or directory", input)
		}
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(path, current)
			if err != nil {
				return err
			}
			if len(strings.Split(filepath.ToSlash(rel), "/")) > maxCollectionDepth {
				return fmt.Errorf("directory nesting exceeds %d levels", maxCollectionDepth)
			}
			if entry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(entry.Name()), ".postman_collection.json") {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("collection %q must be a regular file", current)
			}
			return add(current, false)
		})
		if err != nil {
			return nil, fmt.Errorf("Postman input %q: %w", input, err)
		}
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]collectionInput, 0, len(paths))
	for _, path := range paths {
		files = append(files, collectionInput{path, byPath[path]})
	}
	return files, nil
}

func readCollection(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("must be a regular file")
	}
	if info.Size() > maxCollectionBytes {
		return nil, fmt.Errorf("exceeds %d MiB", maxCollectionBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCollectionBytes+1))
	if err == nil && len(data) > maxCollectionBytes {
		err = fmt.Errorf("exceeds %d MiB", maxCollectionBytes>>20)
	}
	return data, err
}

type normalizedCollection struct {
	Protocol string
	Attempts map[string][]string
}

func normalizeCollection(data []byte, knownAttempts map[string]bool, binaryResponses map[string]bool) (string, error) {
	value, err := decodeCollectionJSON(data)
	if err != nil {
		return "", err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return "", errors.New("collection must be one JSON object")
	}
	if err := supportedAuth(root); err != nil {
		return "", err
	}
	protocol, field := "http", "item"
	if root["$kind"] == "collection" {
		protocol, field = "grpc", "items"
	} else {
		info, _ := root["info"].(map[string]any)
		schema, _ := info["schema"].(string)
		if schema != "https://schema.getpostman.com/json/collection/v2.1.0/collection.json" && schema != "http://schema.getpostman.com/json/collection/v2.1.0/collection.json" {
			return "", errors.New("only Postman HTTP v2.1 and gRPC v3 collections are supported")
		}
	}
	items, ok := root[field].([]any)
	if !ok {
		return "", fmt.Errorf("collection %s must be an array", field)
	}
	result := normalizedCollection{Protocol: protocol, Attempts: map[string][]string{}}
	seenAttempts := map[string]bool{}
	var walk func([]any, string) error
	walk = func(items []any, attempt string) error {
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok {
				return errors.New("collection items must be objects")
			}
			if err := supportedAuth(item); err != nil {
				return err
			}
			children, folder := item[field]
			if folder {
				if _, hasRequest := item["request"]; hasRequest {
					return errors.New("an item cannot contain both a request and a folder")
				}
				if protocol == "grpc" && item["$kind"] != "collection" {
					return errors.New("gRPC folders need $kind collection")
				}
				children, ok := children.([]any)
				if !ok {
					return errors.New("folder items must be an array")
				}
				id, _ := item["id"].(string)
				next := attempt
				if knownAttempts[id] {
					if seenAttempts[id] {
						return errors.New("duplicate recorded test attempt ID folder")
					}
					seenAttempts[id] = true
					if attempt != "" && attempt != id {
						return errors.New("test attempt folders cannot be nested inside a different attempt")
					}
					next = id
					if _, seen := result.Attempts[id]; !seen {
						result.Attempts[id] = []string{}
					}
				}
				if next == "" && len(children) == 0 && id != "" {
					result.Attempts[id] = []string{}
				}
				if err := walk(children, next); err != nil {
					return err
				}
				continue
			}
			if attempt == "" {
				return errors.New("each example must be inside its recorded test attempt ID folder")
			}
			var normalized any
			var err error
			if protocol == "http" {
				normalized, err = normalizeHTTPItem(item, binaryResponses)
			} else {
				normalized, err = normalizeGRPCItem(item)
			}
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(normalized)
			if err != nil {
				return err
			}
			result.Attempts[attempt] = append(result.Attempts[attempt], string(encoded))
		}
		return nil
	}
	if err := walk(items, ""); err != nil {
		return "", err
	}
	for _, exchanges := range result.Attempts {
		sort.Strings(exchanges)
	}
	encoded, err := json.Marshal(result)
	return string(encoded), err
}

func supportedAuth(object map[string]any) error {
	value, exists := object["auth"]
	if !exists || value == nil {
		return nil
	}
	auth, ok := value.(map[string]any)
	if !ok || auth["type"] != "noauth" {
		return errors.New("request authentication transformations are unsupported; use explicit captured headers")
	}
	return nil
}

type normalizedHTTPRequest struct {
	Method, URL string
	Headers     [][2]string
	Body        *normalizedCollectionBody
}
type normalizedHTTPResponse struct {
	Code            int
	Headers         [][2]string
	Body            normalizedCollectionBody
	OriginalRequest normalizedHTTPRequest
}
type normalizedHTTPItem struct {
	Request   normalizedHTTPRequest
	Responses []normalizedHTTPResponse
}
type normalizedCollectionBody struct{ Kind, Content string }

func normalizeHTTPItem(item map[string]any, binaryResponses map[string]bool) (normalizedHTTPItem, error) {
	request, err := normalizeHTTPRequest(item["request"])
	if err != nil {
		return normalizedHTTPItem{}, err
	}
	responses, ok := item["response"].([]any)
	if !ok || len(responses) == 0 {
		return normalizedHTTPItem{}, errors.New("each HTTP request needs captured response examples")
	}
	result := normalizedHTTPItem{Request: request, Responses: []normalizedHTTPResponse{}}
	for _, value := range responses {
		response, ok := value.(map[string]any)
		if !ok {
			return result, errors.New("HTTP responses must be objects")
		}
		code, err := integer(response["code"], 100, 999)
		if err != nil {
			return result, errors.New("HTTP response needs an observed status code")
		}
		headers, err := normalizeCollectionHeaders(response["header"])
		if err != nil {
			return result, err
		}
		body, ok := response["body"].(string)
		if !ok {
			return result, errors.New("HTTP response body must be a string")
		}
		original, err := normalizeHTTPRequest(response["originalRequest"])
		if err != nil {
			return result, fmt.Errorf("HTTP response originalRequest: %w", err)
		}
		normalizedBody := normalizeHTTPCollectionBody(body, headers)
		if binaryResponses[binaryResponseKey(original, code, headers, body)] {
			decoded, err := base64.StdEncoding.DecodeString(body)
			if err != nil {
				return result, errors.New("binary HTTP response body must contain base64")
			}
			normalizedBody = normalizedCollectionBody{Kind: "binary", Content: base64.StdEncoding.EncodeToString(decoded)}
		}
		result.Responses = append(result.Responses, normalizedHTTPResponse{code, headers, normalizedBody, original})
	}
	// Saved response names and ordering are presentation details.
	sort.Slice(result.Responses, func(i, j int) bool {
		a, _ := json.Marshal(result.Responses[i])
		b, _ := json.Marshal(result.Responses[j])
		return string(a) < string(b)
	})
	return result, nil
}

func normalizeHTTPRequest(value any) (normalizedHTTPRequest, error) {
	request, ok := value.(map[string]any)
	if !ok {
		return normalizedHTTPRequest{}, errors.New("HTTP request must be an object")
	}
	if err := supportedAuth(request); err != nil {
		return normalizedHTTPRequest{}, err
	}
	method, ok := request["method"].(string)
	if !ok || method == "" {
		return normalizedHTTPRequest{}, errors.New("HTTP request needs a method")
	}
	target, err := normalizeHTTPURL(request["url"])
	if err != nil {
		return normalizedHTTPRequest{}, err
	}
	headers, err := normalizeCollectionHeaders(request["header"])
	if err != nil {
		return normalizedHTTPRequest{}, err
	}
	result := normalizedHTTPRequest{Method: method, URL: target, Headers: headers}
	value, exists := request["body"]
	if !exists || value == nil {
		return result, nil
	}
	body, ok := value.(map[string]any)
	if !ok {
		return result, errors.New("HTTP request body must be an object")
	}
	if disabled, _ := body["disabled"].(bool); disabled {
		return result, nil
	}
	switch body["mode"] {
	case "raw":
		raw, ok := body["raw"].(string)
		if !ok {
			return result, errors.New("raw request body must be a string")
		}
		if options, ok := body["options"].(map[string]any); ok {
			if rawOptions, ok := options["raw"].(map[string]any); ok {
				language, _ := rawOptions["language"].(string)
				if language != "" && language != "text" && language != "json" {
					return result, errors.New("raw request body transformations are unsupported")
				}
				if language == "json" {
					if _, err := decodeCollectionJSON([]byte(raw)); err != nil {
						return result, errors.New("JSON-mode request body must be valid JSON without transformations")
					}
				}
			}
		}
		normalized := normalizeHTTPCollectionBody(raw, headers)
		result.Body = &normalized
	case "file":
		file, ok := body["file"].(map[string]any)
		if !ok {
			return result, errors.New("file request body needs inline content")
		}
		if source, exists := file["src"]; exists && source != nil && source != "" {
			return result, errors.New("external request body files are unsupported")
		}
		content, ok := file["content"].(string)
		if !ok {
			return result, errors.New("file request body needs inline base64 content")
		}
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return result, errors.New("inline file request body is not base64")
		}
		result.Body = &normalizedCollectionBody{Kind: "binary", Content: base64.StdEncoding.EncodeToString(decoded)}
	default:
		return result, errors.New("only raw and inline file request bodies are supported")
	}
	return result, nil
}

func normalizeHTTPURL(value any) (string, error) {
	if raw, ok := value.(string); ok && raw != "" {
		return raw, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "", errors.New("HTTP request URL must be a string or URL object with raw")
	}
	raw, ok := object["raw"].(string)
	if !ok || raw == "" {
		return "", errors.New("HTTP URL object needs raw")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid HTTP URL: %w", err)
	}
	if value, exists := object["protocol"]; exists {
		protocol, ok := value.(string)
		if !ok || protocol != parsed.Scheme {
			return "", errors.New("HTTP URL object protocol disagrees with raw")
		}
	}
	if host, exists := object["host"]; exists {
		value, err := urlComponent(host, ".")
		if err != nil || !strings.EqualFold(value, parsed.Hostname()) {
			return "", errors.New("HTTP URL object host disagrees with raw")
		}
	}
	if value, exists := object["port"]; exists {
		port, ok := value.(string)
		if !ok || port != parsed.Port() {
			return "", errors.New("HTTP URL object port disagrees with raw")
		}
	}
	if path, exists := object["path"]; exists {
		value, err := urlComponent(path, "/")
		if err != nil || value != strings.TrimPrefix(parsed.EscapedPath(), "/") {
			return "", errors.New("HTTP URL object path disagrees with raw")
		}
	}
	if value, exists := object["hash"]; exists {
		fragment, ok := value.(string)
		if !ok || fragment != parsed.Fragment {
			return "", errors.New("HTTP URL object fragment disagrees with raw")
		}
	}
	if entries, ok := object["query"].([]any); ok {
		query := url.Values{}
		for _, value := range entries {
			entry, ok := value.(map[string]any)
			if !ok {
				return "", errors.New("HTTP URL query entries must be objects")
			}
			if disabled, _ := entry["disabled"].(bool); disabled {
				continue
			}
			key, keyOK := entry["key"].(string)
			value, valueOK := entry["value"].(string)
			if entry["value"] == nil {
				valueOK = true
			}
			if !keyOK || !valueOK {
				return "", errors.New("HTTP URL query entries need string keys and values")
			}
			query.Add(key, value)
		}
		if !reflect.DeepEqual(query, parsed.Query()) {
			return "", errors.New("HTTP URL object query disagrees with raw")
		}
	}
	return raw, nil
}

func urlComponent(value any, separator string) (string, error) {
	if value, ok := value.(string); ok {
		return value, nil
	}
	values, ok := value.([]any)
	if !ok {
		return "", errors.New("URL component must be a string or string array")
	}
	parts, err := stringArray(values)
	return strings.Join(parts, separator), err
}

func stringArray(values []any) ([]string, error) {
	result := make([]string, len(values))
	for i, value := range values {
		var ok bool
		result[i], ok = value.(string)
		if !ok {
			return nil, errors.New("expected strings")
		}
	}
	return result, nil
}

func normalizeCollectionHeaders(value any) ([][2]string, error) {
	result := [][2]string{}
	if value == nil {
		return result, nil
	}
	entries, ok := value.([]any)
	if !ok {
		return nil, errors.New("headers or metadata must be an array")
	}
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("headers or metadata must contain objects")
		}
		if disabled, _ := entry["disabled"].(bool); disabled {
			continue
		}
		key, keyOK := entry["key"].(string)
		value, valueOK := entry["value"].(string)
		if !keyOK || key == "" || !valueOK {
			return nil, errors.New("headers or metadata need string keys and values")
		}
		result = append(result, [2]string{strings.ToLower(key), value})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i][0] < result[j][0] })
	return result, nil
}

type normalizedGRPCCall struct {
	Method   string
	Metadata [][2]string
	Message  *normalizedCollectionBody
}
type normalizedGRPCMessage struct {
	Direction string
	Body      normalizedCollectionBody
}
type normalizedGRPCResponse struct {
	Code               int
	Metadata, Trailers [][2]string
	Messages           []normalizedGRPCMessage
}
type normalizedGRPCExample struct {
	Request  normalizedGRPCCall
	Response normalizedGRPCResponse
}
type normalizedGRPCItem struct {
	Request  normalizedGRPCCall
	Examples []normalizedGRPCExample
}

func normalizeGRPCItem(item map[string]any) (normalizedGRPCItem, error) {
	if item["$kind"] != "grpc-request" {
		return normalizedGRPCItem{}, errors.New("gRPC items need $kind grpc-request")
	}
	request, err := normalizeGRPCCall(item)
	if err != nil {
		return normalizedGRPCItem{}, err
	}
	result := normalizedGRPCItem{Request: request, Examples: []normalizedGRPCExample{}}
	examples, ok := item["examples"].([]any)
	if !ok || len(examples) == 0 {
		return result, errors.New("gRPC requests need captured examples")
	}
	for _, value := range examples {
		example, ok := value.(map[string]any)
		if !ok || example["$kind"] != "grpc-example" {
			return result, errors.New("gRPC examples need $kind grpc-example")
		}
		call, ok := example["request"].(map[string]any)
		if !ok {
			return result, errors.New("gRPC example request must be an object")
		}
		request, err := normalizeGRPCCall(call)
		if err != nil {
			return result, err
		}
		response, ok := example["response"].(map[string]any)
		if !ok {
			return result, errors.New("gRPC example response must be an object")
		}
		code, err := integer(response["code"], 0, 16)
		if err != nil {
			return result, errors.New("gRPC response needs an observed status code")
		}
		metadata, err := normalizeCollectionHeaders(response["metadata"])
		if err != nil {
			return result, err
		}
		trailers, err := normalizeCollectionHeaders(response["trailers"])
		if err != nil {
			return result, err
		}
		messages, ok := response["messages"].([]any)
		if !ok {
			return result, errors.New("gRPC response messages must be an array")
		}
		normalized := normalizedGRPCResponse{Code: code, Metadata: metadata, Trailers: trailers, Messages: []normalizedGRPCMessage{}}
		for _, value := range messages {
			message, ok := value.(map[string]any)
			if !ok {
				return result, errors.New("gRPC messages must be objects")
			}
			direction, _ := message["type"].(string)
			if direction != "incoming" && direction != "outgoing" {
				return result, errors.New("gRPC messages need incoming or outgoing direction")
			}
			body, err := grpcContent(message["content"])
			if err != nil {
				return result, err
			}
			normalized.Messages = append(normalized.Messages, normalizedGRPCMessage{direction, body})
		}
		result.Examples = append(result.Examples, normalizedGRPCExample{request, normalized})
	}
	sort.Slice(result.Examples, func(i, j int) bool {
		a, _ := json.Marshal(result.Examples[i])
		b, _ := json.Marshal(result.Examples[j])
		return string(a) < string(b)
	})
	return result, nil
}

func normalizeGRPCCall(call map[string]any) (normalizedGRPCCall, error) {
	if err := supportedAuth(call); err != nil {
		return normalizedGRPCCall{}, err
	}
	method, ok := call["methodPath"].(string)
	if !ok || method == "" || strings.Count(method, "/") != 1 || strings.HasPrefix(method, "/") {
		return normalizedGRPCCall{}, errors.New("gRPC calls need a fully qualified methodPath")
	}
	metadata, err := normalizeCollectionHeaders(call["metadata"])
	if err != nil {
		return normalizedGRPCCall{}, err
	}
	result := normalizedGRPCCall{Method: method, Metadata: metadata}
	if value, exists := call["message"]; exists && value != nil {
		message, ok := value.(map[string]any)
		if !ok {
			return result, errors.New("gRPC message must contain JSON content")
		}
		body, err := grpcContent(message["content"])
		if err != nil {
			return result, err
		}
		result.Message = &body
	}
	return result, nil
}

func grpcContent(value any) (normalizedCollectionBody, error) {
	content, ok := value.(string)
	if !ok {
		return normalizedCollectionBody{}, errors.New("gRPC message content must be a JSON string")
	}
	result := normalizeCollectionBody(content)
	if result.Kind != "json" {
		return result, errors.New("gRPC message content must contain valid JSON")
	}
	return result, nil
}

func integer(value any, min, max int) (int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("expected an integer")
	}
	i, err := number.Int64()
	if err != nil || i < int64(min) || i > int64(max) {
		return 0, errors.New("integer outside range")
	}
	return int(i), nil
}

func normalizeCollectionBody(raw string) normalizedCollectionBody {
	value, err := decodeCollectionJSON([]byte(raw))
	if err != nil {
		return normalizedCollectionBody{Kind: "text", Content: raw}
	}
	var canonical bytes.Buffer
	if err := writeCanonicalJSON(&canonical, value); err != nil {
		return normalizedCollectionBody{Kind: "text", Content: raw}
	}
	return normalizedCollectionBody{Kind: "json", Content: canonical.String()}
}

func normalizeHTTPCollectionBody(raw string, headers [][2]string) normalizedCollectionBody {
	var contentTypes []string
	for _, header := range headers {
		if header[0] == "content-type" {
			contentTypes = append(contentTypes, header[1])
		}
	}
	if httpBodyIsJSON(contentTypes) {
		return normalizeCollectionBody(raw)
	}
	return normalizedCollectionBody{Kind: "text", Content: raw}
}

// Decode tokens rather than unmarshalling so ambiguous duplicate keys and deeply
// nested inputs cannot hide edits or exhaust the verification process.
func decodeCollectionJSON(data []byte) (any, error) {
	if !validJSONUnicode(data) {
		return nil, errors.New("JSON must contain valid UTF-8 and paired Unicode surrogate escapes")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		nodes++
		if depth > maxCollectionDepth || nodes > maxCollectionNodes {
			return nil, errors.New("JSON exceeds nesting or item limits")
		}
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return token, nil
		}
		switch delim {
		case '{':
			object := map[string]any{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("JSON object key must be a string")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate JSON key %q", key)
				}
				object[key], err = read(depth + 1)
				if err != nil {
					return nil, err
				}
			}
			if token, err := d.Token(); err != nil || token != json.Delim('}') {
				return nil, errors.New("invalid JSON object")
			}
			return object, nil
		case '[':
			array := []any{}
			for d.More() {
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if token, err := d.Token(); err != nil || token != json.Delim(']') {
				return nil, errors.New("invalid JSON array")
			}
			return array, nil
		default:
			return nil, errors.New("invalid JSON delimiter")
		}
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("collection must contain one JSON value")
	}
	return value, nil
}

func binaryResponseKey(request normalizedHTTPRequest, code int, headers [][2]string, body string) string {
	data, _ := json.Marshal(struct {
		Request normalizedHTTPRequest
		Code    int
		Headers [][2]string
		Body    string
	}{request, code, headers, body})
	return string(data)
}

// The raw archive decides whether an exported response string represents JSON,
// text or base64. Names in supplied files remain presentation details. In
// particular, base64 such as "null" must never acquire JSON normalization.
func expectedBinaryResponses(exports []postman.Export) (map[string]bool, error) {
	result := map[string]bool{}
	for _, export := range exports {
		if export.Collection == nil {
			continue
		}
		for _, folder := range export.Collection.Items {
			for _, item := range folder.Items {
				for _, response := range item.Responses {
					if !strings.Contains(response.Name, "(body is base64)") {
						continue
					}
					data, err := json.Marshal(response)
					if err != nil {
						return nil, err
					}
					value, err := decodeCollectionJSON(data)
					if err != nil {
						return nil, err
					}
					object := value.(map[string]any)
					request, err := normalizeHTTPRequest(object["originalRequest"])
					if err != nil {
						return nil, err
					}
					headers, err := normalizeCollectionHeaders(object["header"])
					if err != nil {
						return nil, err
					}
					result[binaryResponseKey(request, response.Code, headers, response.Body)] = true
				}
			}
		}
	}
	return result, nil
}

func writeCanonicalJSON(out *bytes.Buffer, value any) error {
	switch value := value.(type) {
	case json.Number:
		number, err := canonicalNumber(string(value))
		if err != nil {
			return err
		}
		out.WriteString(number)
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i != 0 {
				out.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			out.Write(encoded)
			out.WriteByte(':')
			if err := writeCanonicalJSON(out, value[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, item := range value {
			if i != 0 {
				out.WriteByte(',')
			}
			if err := writeCanonicalJSON(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out.Write(encoded)
	}
	return nil
}

// Keep numeric precision and normalize equivalent decimal spellings without
// expanding exponents (including deliberately huge exponents in test data).
func canonicalNumber(raw string) (string, error) {
	if len(raw) > 1024 {
		return "", errors.New("JSON number exceeds normalization limit")
	}
	sign := ""
	if strings.HasPrefix(raw, "-") {
		sign, raw = "-", raw[1:]
	}
	exponent := new(big.Int)
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		if _, ok := exponent.SetString(raw[index+1:], 10); !ok {
			return "", errors.New("invalid JSON exponent")
		}
		raw = raw[:index]
	}
	if index := strings.IndexByte(raw, '.'); index >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(raw)-index-1)))
		raw = raw[:index] + raw[index+1:]
	}
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		return "0", nil
	}
	trimmed := strings.TrimRight(raw, "0")
	exponent.Add(exponent, big.NewInt(int64(len(raw)-len(trimmed))))
	return sign + trimmed + "e" + exponent.String(), nil
}
