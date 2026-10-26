package bundle

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestPreparationFreezesExplicitSelfReferences(t *testing.T) {
	for _, source := range []string{"file-relative", "file-absolute", "url"} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			rootName, companionName := "root.yaml", "companion.yaml"
			var root, companion string
			var server *httptest.Server
			reads := map[string]int{}
			if source == "file-absolute" {
				rootName = (&url.URL{Scheme: "file", Path: filepath.Join(dir, rootName)}).String()
				companionName = (&url.URL{Scheme: "file", Path: filepath.Join(dir, companionName)}).String()
			}
			if source == "url" {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reads[r.URL.Path]++
					if r.URL.Path == "/root.yaml" {
						_, _ = w.Write([]byte(root))
					} else {
						_, _ = w.Write([]byte(companion))
					}
				}))
				defer server.Close()
				rootName, companionName = server.URL+"/root.yaml", server.URL+"/companion.yaml"
			}
			root = fmt.Sprintf(`openapi: 3.0.3
info: {title: Self references, version: '1'}
paths:
  /items:
    get:
      responses:
        '200':
          description: items
          content:
            application/json:
              schema:
                type: object
                properties:
                  local: {$ref: '%s#/components/schemas/Value'}
                  external: {$ref: '%s#/schemas/Item'}
components:
  schemas:
    Value: {type: string}
`, rootName, companionName)
			companion = fmt.Sprintf(`schemas:
  Item:
    type: object
    properties:
      value: {$ref: '%s#/schemas/Value'}
  Value: {type: integer}
`, companionName)
			ref := artifact.SchemaRef{ID: "items", Role: "sut", Type: "openapi"}
			if server != nil {
				ref.Source = artifact.SchemaSource{Type: "url", URL: rootName}
			} else {
				for name, data := range map[string]string{"root.yaml": root, "companion.yaml": companion} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
				ref.Source = artifact.SchemaSource{Type: "file", Path: filepath.Join(dir, "root.yaml")}
			}
			writer, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := writer.RegisterPrepared(artifact.TestCase{ID: "attempt"}, []artifact.SchemaRef{ref})
			if err != nil {
				t.Fatal(err)
			}
			if server != nil {
				server.Close()
				if reads["/root.yaml"] != 1 || reads["/companion.yaml"] != 1 {
					t.Fatalf("reloaded source documents: %v", reads)
				}
			} else {
				for _, name := range []string{"root.yaml", "companion.yaml"} {
					if err := os.Remove(filepath.Join(dir, name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			request, err := http.NewRequest(http.MethodGet, "http://provider/items", nil)
			if err != nil {
				t.Fatal(err)
			}
			route, _, err := prepared[0].Router.FindRoute(request)
			if err != nil {
				t.Fatal(err)
			}
			body := route.Operation.Responses.Status(200).Value.Content["application/json"].Schema.Value
			if !body.Properties["local"].Value.Type.Includes("string") || !body.Properties["external"].Value.Properties["value"].Value.Type.Includes("integer") {
				t.Fatal("prepared router lost resolved self references")
			}
			name, files, err := OpenAPIFiles(prepared[0].Ref)
			if err != nil {
				t.Fatal(err)
			}
			for _, data := range files {
				if strings.Contains(string(data), rootName+"#") || strings.Contains(string(data), companionName+"#") {
					t.Fatal("frozen references still point to source documents")
				}
			}
			doc, err := ParseOpenAPI(t.Context(), name, files)
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInvalidContractDoesNotRegisterAttempt(t *testing.T) {
	writer, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.RegisterPrepared(artifact.TestCase{ID: "invalid"}, []artifact.SchemaRef{{ID: "bad", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: "openapi: 3.0.3\npaths: {}\n"}}})
	if !errors.Is(err, ErrSchema) {
		t.Fatalf("invalid contract accepted: %v", err)
	}
	run := writer.Snapshot()
	if len(run.Pending) != 0 || len(run.Schemas) != 0 {
		t.Fatal("invalid contract published an attempt or inventory")
	}
}
