package arazzo

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestOperationPathResolvesEscapedURIFragments(t *testing.T) {
	for _, path := range []string{"/orders/{id}", "/orders/%2F", "/a~b/c#d?e ü"} {
		t.Run(path, func(t *testing.T) {
			reference := operationPath("api", path, "GET")
			uri, err := url.Parse(strings.Replace(reference, "{$sourceDescriptions.api.url}", "https://example.test/openapi.json", 1))
			if err != nil {
				t.Fatal(err)
			}
			document := map[string]any{"paths": map[string]any{path: map[string]any{"get": "target"}}}
			var node any = document
			for _, part := range strings.Split(strings.TrimPrefix(uri.Fragment, "/"), "/") {
				key := strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
				object, ok := node.(map[string]any)
				if !ok {
					t.Fatalf("operation reference %q does not point into the document", reference)
				}
				node = object[key]
			}
			if node != "target" {
				t.Fatalf("operation reference %q did not resolve to its target", reference)
			}
		})
	}
}

func TestJSONBodyPreservesNumbersAndLiteralExpressions(t *testing.T) {
	value, err := jsonBody(&artifact.Body{Text: `{"id":18446744073709551615,"literal":"$inputs.value","nested":[true,null,{}]}`})
	if err != nil {
		t.Fatal(err)
	}
	object := value.(map[string]any)
	if object["id"] != json.Number("18446744073709551615") || object["literal"] != "$inputs.value" {
		t.Fatalf("captured values changed: %#v", object)
	}
}

func TestJSONBodyRejectsUnrepresentableJSON(t *testing.T) {
	for _, raw := range []string{`{"id":1,"id":2}`, `{"nested":[{"id":null,"id":2}]}`, `{"a":1,"\u0061":2}`, `{"missing":`, `1 2`, "{\"x\":\"\xff\"}"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := jsonBody(&artifact.Body{Bytes: base64.StdEncoding.EncodeToString([]byte(raw))}); err == nil {
				t.Fatal("accepted JSON that cannot be represented without changing captured values")
			}
		})
	}
}

func TestObservedSourceRetainsRequestMediaTypes(t *testing.T) {
	source := &sourceSpec{name: "api", observed: map[string]any{"paths": map[string]any{}}}
	requestURL, err := url.Parse("https://example.test/orders")
	if err != nil {
		t.Fatal(err)
	}
	for _, media := range []string{"application/json", "application/vnd.orders+json"} {
		flow := artifact.Flow{Request: &artifact.HttpMessage{Method: "POST", Headers: artifact.Headers{"Content-Type": {media}}, Body: &artifact.Body{Text: `{}`}}, Response: &artifact.HttpMessage{Status: 200}}
		if err := observedStep(source, flow, requestURL, &Step{}, func(any) string { return "$inputs.body" }); err != nil {
			t.Fatal(err)
		}
	}
	paths := source.observed["paths"].(map[string]any)
	operation := paths["/orders"].(map[string]any)["post"].(map[string]any)
	content := operation["requestBody"].(map[string]any)["content"].(map[string]any)
	if len(content) != 2 || content["application/json"] == nil || content["application/vnd.orders+json"] == nil {
		t.Fatalf("a captured media type was lost: %#v", content)
	}
}

func TestStepRejectsAmbiguousContentType(t *testing.T) {
	flow := artifact.Flow{Request: &artifact.HttpMessage{URL: "https://example.test/orders", Headers: artifact.Headers{"Content-Type": {"application/json"}, "content-type": {"text/plain"}}}}
	_, _, err := makeStep(nil, flow, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Content-Type") {
		t.Fatalf("wanted ambiguous Content-Type failure, got %v", err)
	}
}
