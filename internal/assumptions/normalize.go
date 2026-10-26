package assumptions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mattlock83/wiretap/internal/artifact"
)

// IgnoredHeaders are transport, framing and tracing fields excluded from
// scenario comparisons. Application headers, including authorization and
// arbitrary x-* fields, remain significant unless explicitly ignored.
var IgnoredHeaders = []string{
	"accept-encoding", "baggage", "connection", "content-length", "date",
	"host", "keep-alive",
	"postman-token", "proxy-connection", "server", "te", "trailer", "traceparent",
	"tracestate", "transfer-encoding", "upgrade", "user-agent", "via",
}

// normalizeFlow describes retained application semantics, independently of local
// boundary IDs, endpoints, test names and recording timestamps. The signatures
// preserve complete examples; they do not infer which fields a consumer uses.
func normalizeFlow(flow artifact.Flow, ignoreHeaders []string) (request, response string, err error) {
	if flow.TransportError != "" {
		return "", "", fmt.Errorf("transport failure: %s", flow.TransportError)
	}
	ignored := map[string]bool{}
	for _, names := range [][]string{IgnoredHeaders, ignoreHeaders} {
		for _, name := range names {
			ignored[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	var req, res any
	switch flow.Protocol {
	case "http":
		req, res, err = normalizeHTTP(flow, ignored)
	default:
		err = fmt.Errorf("unsupported protocol %q", flow.Protocol)
	}
	if err != nil {
		return "", "", err
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return "", "", err
	}
	resJSON, err := json.Marshal(res)
	if err != nil {
		return "", "", err
	}
	return string(reqJSON), string(resJSON), nil
}

func normalizeHTTP(flow artifact.Flow, ignored map[string]bool) (any, any, error) {
	req, res := flow.Request.HTTP, flow.Response.HTTP
	if req == nil || res == nil {
		return nil, nil, fmt.Errorf("complete HTTP request and response are required")
	}
	if !headerName(strings.ToLower(req.Method)) {
		return nil, nil, fmt.Errorf("a valid HTTP request method is required")
	}
	if res.Status < 100 || res.Status > 999 {
		return nil, nil, fmt.Errorf("an observed HTTP response status is required")
	}
	u, err := url.Parse(req.URL)
	if err != nil || req.URL == "" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.Scheme != "" && (u.Host == "" || u.Scheme != "http" && u.Scheme != "https") {
		return nil, nil, fmt.Errorf("invalid HTTP request URL %q", req.URL)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid HTTP query: %w", err)
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	reqBody, err := normalizeHTTPBody(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request body: %w", err)
	}
	resBody, err := normalizeHTTPBody(res)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP response body: %w", err)
	}
	return map[string]any{
			"protocol": "http", "method": req.Method, "path": path,
			"query": query, "headers": normalizeHeaders(req.Headers, ignored, req.Body),
			"trailers": normalizeHeaders(req.Trailers, ignored, nil), "body": reqBody,
		}, map[string]any{
			"protocol": "http", "status": res.Status,
			"headers":  normalizeHeaders(res.Headers, ignored, res.Body),
			"trailers": normalizeHeaders(res.Trailers, ignored, nil), "body": resBody,
		}, nil
}

func normalizeHeaders(headers artifact.Headers, ignored map[string]bool, body *artifact.Body) map[string][]string {
	// Connection can nominate further hop-by-hop headers. Their meaning is
	// confined to this transport connection, independently of the API example.
	omit := map[string]bool{}
	for name, values := range headers {
		if strings.EqualFold(name, "connection") {
			for _, value := range values {
				for _, token := range strings.Split(value, ",") {
					omit[strings.ToLower(strings.TrimSpace(token))] = true
				}
			}
		}
	}
	// Captures retain decoded bytes when HTTP content encoding was removed.
	if body != nil && body.ContentEncoding != "" {
		omit["content-encoding"] = true
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := map[string][]string{}
	for _, name := range names {
		key := strings.ToLower(name)
		if !ignored[key] && !omit[key] {
			result[key] = append(result[key], headers[name]...)
		}
	}
	return result
}

type normalizedBody struct {
	Representation string `json:"representation"`
	Value          any    `json:"value"`
}

func normalizeHTTPBody(message *artifact.HttpMessage) (normalizedBody, error) {
	var contentTypes []string
	for name, values := range message.Headers {
		if strings.EqualFold(name, "Content-Type") {
			contentTypes = append(contentTypes, values...)
		}
	}
	if message.Body == nil || httpBodyIsJSON(contentTypes) {
		return normalizeBody(message.Body)
	}
	return normalizeWireBody(message.Body)
}

// Honor a declared HTTP representation before interpreting JSON-looking bytes.
// Missing Content-Type retains legacy JSON sniffing; malformed, conflicting or
// non-JSON declarations compare bytes exactly. Storage encoding is irrelevant.
func httpBodyIsJSON(contentTypes []string) bool {
	if len(contentTypes) == 0 {
		return true
	}
	if len(contentTypes) != 1 {
		return false
	}
	media, _, err := mime.ParseMediaType(contentTypes[0])
	return err == nil && (media == "application/json" || strings.HasSuffix(media, "+json"))
}

func normalizeBody(body *artifact.Body) (normalizedBody, error) {
	if body == nil {
		return normalizedBody{Representation: "absent"}, nil
	}
	raw, err := retainedBodyBytes(body)
	if err != nil {
		return normalizedBody{}, err
	}
	if !validJSONUnicode(raw) {
		return normalizedBody{Representation: "bytes", Value: base64.StdEncoding.EncodeToString(raw)}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := normalizeJSONValue(decoder, 0)
	if err == nil {
		if _, trailing := decoder.Token(); trailing == io.EOF {
			return normalizedBody{Representation: "json", Value: value}, nil
		}
	}
	// Non-JSON (including invalid or duplicate-key JSON negative examples) is
	// compared byte for byte, without guessing a parser's interpretation.
	return normalizedBody{Representation: "bytes", Value: base64.StdEncoding.EncodeToString(raw)}, nil
}

// encoding/json replaces invalid UTF-8 and unpaired surrogate escapes with the
// replacement character. Keep those negative examples as exact retained bytes
// rather than allowing distinct malformed strings to become equal examples.
// JSON grammar is still checked by the caller's ordinary decoder.
func validJSONUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	codeUnit := func(raw []byte) (uint16, bool) {
		var result uint16
		for _, c := range raw {
			result <<= 4
			switch {
			case c >= '0' && c <= '9':
				result += uint16(c - '0')
			case c >= 'a' && c <= 'f':
				result += uint16(c-'a') + 10
			case c >= 'A' && c <= 'F':
				result += uint16(c-'A') + 10
			default:
				return 0, false
			}
		}
		return result, true
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		value, valid := codeUnit(raw[i+1 : i+5])
		if !valid {
			return false
		}
		i += 4
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, valid := codeUnit(raw[i+3 : i+7])
			if !valid || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		} else if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
	}
	return true
}

func retainedBodyBytes(body *artifact.Body) ([]byte, error) {
	if body.Truncated || body.Omitted || body.Redacted {
		return nil, fmt.Errorf("complete retained, unredacted bytes are required")
	}
	raw := []byte(body.Text)
	if body.Bytes != "" {
		var err error
		raw, err = base64.StdEncoding.DecodeString(body.Bytes)
		if err != nil {
			return nil, fmt.Errorf("invalid retained base64: %w", err)
		}
	}
	if len(raw) == 0 && body.OriginalBytes > 0 {
		return nil, fmt.Errorf("observed body bytes are unavailable")
	}
	return raw, nil
}

func normalizeWireBody(body *artifact.Body) (normalizedBody, error) {
	if body == nil {
		return normalizedBody{}, fmt.Errorf("retained wire bytes are required")
	}
	raw, err := retainedBodyBytes(body)
	if err != nil {
		return normalizedBody{}, err
	}
	return normalizedBody{Representation: "bytes", Value: base64.StdEncoding.EncodeToString(raw)}, nil
}

func normalizeJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 256 {
		return nil, fmt.Errorf("JSON nesting exceeds normalization limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Number:
		return normalizeNumber(token), nil
	case json.Delim:
		switch token {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				name, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := name.(string)
				if !ok {
					return nil, fmt.Errorf("invalid JSON object key")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate JSON object key")
				}
				value, err := normalizeJSONValue(decoder, depth+1)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("incomplete JSON object")
			}
			return object, nil
		case '[':
			array := []any{}
			for decoder.More() {
				value, err := normalizeJSONValue(decoder, depth+1)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("incomplete JSON array")
			}
			return array, nil
		}
		return nil, fmt.Errorf("unexpected JSON delimiter")
	default:
		return token, nil
	}
}

// Normalize decimal notation without float conversion or exponent expansion.
// For example 100, 100.0 and 1e2 all become 1e2, including arbitrarily precise
// integers and exponents that cannot safely be represented by float64.
func normalizeNumber(number json.Number) json.Number {
	text := string(number)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	exponent := new(big.Int)
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent.SetString(text[i+1:], 10)
		text = text[:i]
	}
	if i := strings.IndexByte(text, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(text)-i-1)))
		text = text[:i] + text[i+1:]
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		return json.Number("0")
	}
	trimmed := strings.TrimRight(text, "0")
	exponent.Add(exponent, big.NewInt(int64(len(text)-len(trimmed))))
	if exponent.Sign() == 0 {
		return json.Number(sign + trimmed)
	}
	return json.Number(sign + trimmed + "e" + exponent.String())
}
