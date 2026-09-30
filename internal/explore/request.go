package explore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/coverage"
)

type request struct {
	Method     string          `json:"method"`
	URL        string          `json:"url"`
	Headers    http.Header     `json:"headers"`
	Body       json.RawMessage `json:"body,omitempty"`
	BodyBase64 string          `json:"body_base64,omitempty"`
}

func seedRequest(msg *artifact.HttpMessage) (request, error) {
	if msg == nil {
		return request{}, fmt.Errorf("missing HTTP request")
	}
	r := request{Method: msg.Method, URL: msg.URL, Headers: http.Header{}}
	for key, values := range msg.Headers {
		r.Headers[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	for _, h := range []string{"Host", "Content-Length", "Transfer-Encoding", "Connection", "Proxy-Authorization", "X-Wiretap-Session-Id", "Traceparent", "Tracestate"} {
		r.Headers.Del(h)
	}
	if msg.Body != nil {
		if msg.Body.Truncated {
			return r, fmt.Errorf("request body is truncated")
		}
		if msg.Body.ContentEncoding != "" || r.Headers.Get("Content-Encoding") != "" {
			return r, fmt.Errorf("compressed requests are not supported by exploration")
		}
		body := []byte(msg.Body.Text)
		if msg.Body.Bytes != "" {
			var err error
			body, err = base64.StdEncoding.DecodeString(msg.Body.Bytes)
			if err != nil {
				return r, err
			}
		}
		if len(body) > 0 {
			if !json.Valid(body) {
				return r, fmt.Errorf("exploration supports JSON request bodies only")
			}
			r.Body = body
		}
	}
	return r, nil
}

func (r request) clone() request {
	r.Headers = r.Headers.Clone()
	r.Body = append(json.RawMessage(nil), r.Body...)
	return r
}

func (r request) http(ctx context.Context) (*http.Request, error) {
	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return nil, err
	}
	req.Header = r.Headers.Clone()
	return req, nil
}

func (r request) message() *artifact.HttpMessage {
	m := &artifact.HttpMessage{Method: r.Method, URL: r.URL, Headers: artifact.Headers(r.Headers.Clone())}
	if len(r.Body) > 0 {
		m.Body = &artifact.Body{Encoding: "utf-8", Text: string(r.Body), OriginalBytes: int64(len(r.Body))}
	}
	return m
}

func decodeJSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("expected one JSON value")
	}
	return v, nil
}

func token(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func pointerTokens(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("JSON pointer must be empty or start with /: %q", path)
	}
	parts := strings.Split(path[1:], "/")
	for i, s := range parts {
		for j := 0; j < len(s); j++ {
			if s[j] == '~' {
				if j+1 >= len(s) || (s[j+1] != '0' && s[j+1] != '1') {
					return nil, fmt.Errorf("invalid JSON pointer %q", path)
				}
				j++
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func jsonAt(value any, path string) (any, error) {
	parts, err := pointerTokens(path)
	if err != nil {
		return nil, err
	}
	for _, p := range parts {
		switch node := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = node[p]
			if !ok {
				return nil, fmt.Errorf("JSON pointer %q is missing", path)
			}
		case []any:
			i, e := strconv.Atoi(p)
			if e != nil || i < 0 || i >= len(node) {
				return nil, fmt.Errorf("JSON pointer %q is missing", path)
			}
			value = node[i]
		default:
			return nil, fmt.Errorf("JSON pointer %q has no parent", path)
		}
	}
	return value, nil
}

func editJSON(raw []byte, path string, value any, remove bool) ([]byte, error) {
	root, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	parts, err := pointerTokens(path)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		if remove {
			return nil, nil
		}
		return json.Marshal(value)
	}
	parentPath := ""
	for _, p := range parts[:len(parts)-1] {
		parentPath += "/" + token(p)
	}
	parent, err := jsonAt(root, parentPath)
	if err != nil {
		return nil, err
	}
	last := parts[len(parts)-1]
	switch node := parent.(type) {
	case map[string]any:
		if remove {
			if _, ok := node[last]; !ok {
				return nil, fmt.Errorf("mutation target %q missing", path)
			}
			delete(node, last)
		} else {
			node[last] = value
		}
	case []any:
		i, e := strconv.Atoi(last)
		if e != nil || i < 0 || i >= len(node) || remove {
			return nil, fmt.Errorf("unsupported array edit %q", path)
		}
		node[i] = value
	default:
		return nil, fmt.Errorf("JSON pointer %q has no editable parent", path)
	}
	return json.Marshal(root)
}

type spec struct {
	ref    artifact.SchemaRef
	doc    *openapi3.T
	router routers.Router
}

func loadSpec(ref artifact.SchemaRef) (*spec, error) {
	l := openapi3.NewLoader()
	l.IsExternalRefsAllowed = true
	doc, err := l.LoadFromFile(ref.Source.Path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.0.") {
		return nil, fmt.Errorf("exploration supports OpenAPI 3.0 only")
	}
	if err = doc.Validate(context.Background()); err != nil {
		return nil, err
	}
	// Keep server base paths while making the captured host independent of the
	// live target. The original frozen schema bytes are never changed.
	remap := func(servers openapi3.Servers) error {
		for _, server := range servers {
			u, e := url.Parse(server.URL)
			if e != nil {
				return e
			}
			u.Scheme = "http"
			u.Host = "wiretap.invalid"
			u.User = nil
			server.URL = u.String()
		}
		return nil
	}
	if err = remap(doc.Servers); err != nil {
		return nil, err
	}
	for _, item := range doc.Paths.Map() {
		if err = remap(item.Servers); err != nil {
			return nil, err
		}
		for _, op := range item.Operations() {
			if op.Servers != nil {
				if err = remap(*op.Servers); err != nil {
					return nil, err
				}
			}
		}
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		return nil, err
	}
	return &spec{ref: ref, doc: doc, router: router}, nil
}

func (s *spec) input(r request) (*openapi3filter.RequestValidationInput, error) {
	req, err := r.http(context.Background())
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = "http"
	req.URL.Host = "wiretap.invalid"
	route, params, err := s.router.FindRoute(req)
	if err != nil {
		return nil, fmt.Errorf("request does not match frozen provider spec: %w", err)
	}
	return &openapi3filter.RequestValidationInput{Request: req, Route: route, PathParams: params, Options: &openapi3filter.Options{MultiError: true, SkipSettingDefaults: true, AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, IncludeResponseStatus: true}}, nil
}

func (s *spec) validate(r request) error {
	in, err := s.input(r)
	if err != nil {
		return err
	}
	return openapi3filter.ValidateRequest(context.Background(), in)
}

func (s *spec) response(r request, m *artifact.HttpMessage) error {
	in, err := s.input(r)
	if err != nil {
		return err
	}
	return coverage.ValidateReplayResponse(in, m)
}

func bodyText(m *artifact.HttpMessage) string {
	if m == nil || m.Body == nil {
		return ""
	}
	if m.Body.Bytes != "" {
		raw, _ := base64.StdEncoding.DecodeString(m.Body.Bytes)
		return string(raw)
	}
	return m.Body.Text
}

func setValue(r *request, in, path string, value any, remove bool, input *openapi3filter.RequestValidationInput) error {
	if in == "body" {
		raw, err := editJSON(r.Body, path, value, remove)
		if err != nil {
			return err
		}
		r.Body = raw
		return nil
	}
	text := fmt.Sprint(value)
	if in == "header" {
		if remove {
			r.Headers.Del(path)
		} else {
			r.Headers.Set(path, text)
		}
		return nil
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return err
	}
	switch in {
	case "query":
		q := u.Query()
		if remove {
			q.Del(path)
		} else {
			q.Set(path, text)
		}
		u.RawQuery = q.Encode()
	case "cookie":
		req, _ := r.http(context.Background())
		r.Headers.Del("Cookie")
		for _, c := range req.Cookies() {
			if c.Name != path {
				tmp := &http.Request{Header: r.Headers}
				tmp.AddCookie(c)
			}
		}
		if !remove {
			tmp := &http.Request{Header: r.Headers}
			tmp.AddCookie(&http.Cookie{Name: path, Value: text})
		}
		return nil
	case "path":
		if remove || input == nil {
			return fmt.Errorf("cannot remove a path parameter")
		}
		// Match the route template segment, including a declared server base path.
		segments := strings.Split(input.Route.Path, "/")
		actual := strings.Split(u.EscapedPath(), "/")
		offset := len(actual) - len(segments)
		if offset < 0 {
			return fmt.Errorf("path template mismatch")
		}
		found := false
		for i, p := range segments {
			if p == "{"+path+"}" {
				actual[offset+i] = url.PathEscape(text)
				found = true
			}
		}
		if !found {
			return fmt.Errorf("path binding %q requires a whole parameter segment", path)
		}
		u.RawPath = strings.Join(actual, "/")
		u.Path, err = url.PathUnescape(u.RawPath)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported value location %q", in)
	}
	r.URL = u.String()
	return nil
}
