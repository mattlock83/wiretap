package mock

import (
	"bytes"
	"fmt"
	"text/template"
)

type renderData struct {
	Request renderRequest
}

type renderRequest struct {
	Method  string
	Path    string
	URL     string
	Headers map[string]string
	Query   map[string]string
	Body    string
}

// Renderer applies response templates using request data.
type Renderer struct {
}

func NewRenderer() *Renderer {
	return &Renderer{}
}

// RenderResponse returns the rendered response based on stub templates.
func (r *Renderer) RenderResponse(stub Stub, req RecordedRequest) (RecordedResponse, error) {
	data := renderData{
		Request: renderRequest{
			Method:  req.Method,
			Path:    req.Path,
			URL:     req.URL,
			Headers: flattenHeader(req.Headers),
			Query:   flattenQuery(req.Query),
			Body:    req.Body,
		},
	}

	body, err := renderTemplate(stub.Response.Body, data)
	if err != nil {
		return RecordedResponse{}, err
	}

	headers := make(map[string][]string, len(stub.Response.Headers))
	for key, value := range stub.Response.Headers {
		rendered, err := renderTemplate(value, data)
		if err != nil {
			return RecordedResponse{}, err
		}
		headers[key] = []string{rendered}
	}

	status := stub.Response.Status
	if status == 0 {
		status = 200
	}

	return RecordedResponse{
		Status:  status,
		Headers: headers,
		Body:    body,
	}, nil
}

func renderTemplate(raw string, data renderData) (string, error) {
	if raw == "" {
		return "", nil
	}
	tmpl, err := template.New("response").Option("missingkey=zero").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

func flattenHeader(headers map[string][]string) map[string]string {
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		out[key] = values[0]
	}
	return out
}

func flattenQuery(query map[string][]string) map[string]string {
	out := make(map[string]string, len(query))
	for key, values := range query {
		if len(values) == 0 {
			continue
		}
		out[key] = values[0]
	}
	return out
}
