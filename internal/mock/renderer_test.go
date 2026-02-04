package mock

import "testing"

func TestRendererTemplate(t *testing.T) {
	renderer := NewRenderer()
	stub := Stub{
		Response: StubResponse{
			Status: 200,
			Headers: map[string]string{
				"X-Path": "{{.Request.Path}}",
			},
			Body: "hello {{.Request.Query.name}}",
		},
	}

	req := RecordedRequest{
		Method: "GET",
		Path:   "/hello",
		URL:    "/hello?name=codex",
		Headers: map[string][]string{
			"X-Test": {"yes"},
		},
		Query: map[string][]string{
			"name": {"codex"},
		},
		Body: "",
	}

	resp, err := renderer.RenderResponse(stub, req)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}
	if resp.Body != "hello codex" {
		t.Fatalf("unexpected body: %q", resp.Body)
	}
	if resp.Headers["X-Path"][0] != "/hello" {
		t.Fatalf("unexpected header render: %q", resp.Headers["X-Path"][0])
	}
}
