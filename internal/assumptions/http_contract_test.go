package assumptions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

const validatingHTTPBodySchema = `{"type":"object","required":["name","state","count","tags"],"additionalProperties":false,"properties":{"name":{"type":"string","minLength":2,"maxLength":5,"pattern":"^[a-z]+$"},"state":{"type":"string","enum":["open","closed"]},"count":{"type":"integer","minimum":1,"maximum":5},"tags":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"string"}}}}`
const validContractHTTPBody = `{"name":"alpha","state":"open","count":3,"tags":["a"]}`

func validatingHTTPDocument(schema string) string {
	return fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"HTTP check","version":"1"},"servers":[{"url":"https://provider.example:8443/api"}],"paths":{"/orders/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"post":{"requestBody":{"required":true,"content":{"application/json":{"schema":%s}}},"responses":{"200":{"description":"OK","headers":{"X-Rate":{"schema":{"type":"integer","minimum":0}}},"content":{"application/json":{"schema":%s}}},"400":{"description":"Bad request","content":{"application/json":{"schema":{"type":"object","required":["error"],"properties":{"error":{"type":"string"}}}}}}}}}}}`, schema, schema)
}

func freezeAssumptionHTTPContract(t *testing.T, source artifact.SchemaSource) *artifact.SchemaRef {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(providerCheckIdentity()); err != nil {
		t.Fatal(err)
	}
	refs, err := writer.Register(artifact.TestCase{ID: "contract-example", Name: "contract example"}, []artifact.SchemaRef{{ID: "provider", Role: "sut", Type: "openapi", Source: source}})
	if err != nil {
		t.Fatal(err)
	}
	return &refs[0]
}

func loadAssumptionHTTPContract(t *testing.T, document string) *httpContract {
	t.Helper()
	ref := freezeAssumptionHTTPContract(t, artifact.SchemaSource{Type: "inline", Content: document})
	contract, err := new(httpContractVerifier).load(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func validatingHTTPFlow() artifact.Flow {
	return artifact.Flow{Protocol: "http", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://127.0.0.1:45678/api/orders/one", Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: validContractHTTPBody}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: validContractHTTPBody}}}}
}

func TestHTTPContractRoutesDifferentOriginsAndKeepsServerBasePaths(t *testing.T) {
	contract := loadAssumptionHTTPContract(t, validatingHTTPDocument(validatingHTTPBodySchema))
	flow := validatingHTTPFlow()
	input, err := contract.input(flow)
	if err != nil || input.Route.Path != "/orders/{id}" || input.PathParams["id"] != "one" {
		t.Fatalf("base-path route changed: %+v / %v", input, err)
	}
	if err := contract.validate(t.Context(), flow); err != nil {
		t.Fatal(err)
	}
	if contract.doc.Servers[0].URL != "https://provider.example:8443/api" {
		t.Fatal("routing mutated the frozen document")
	}
	flow.Request.HTTP.URL = "http://other.local/orders/one"
	if err := contract.validate(t.Context(), flow); err == nil {
		t.Fatal("declared base path was silently removed")
	}
}

func TestHTTPContractRejectsObservedTypeEnumLengthPatternAndBoundsViolations(t *testing.T) {
	contract := loadAssumptionHTTPContract(t, validatingHTTPDocument(validatingHTTPBodySchema))
	invalid := map[string]string{
		"type":                `{"name":"alpha","state":"open","count":"3","tags":["a"]}`,
		"integer":             `{"name":"alpha","state":"open","count":3.5,"tags":["a"]}`,
		"enum":                `{"name":"alpha","state":"other","count":3,"tags":["a"]}`,
		"minLength":           `{"name":"a","state":"open","count":3,"tags":["a"]}`,
		"maxLength":           `{"name":"toolong","state":"open","count":3,"tags":["a"]}`,
		"pattern":             `{"name":"ALPHA","state":"open","count":3,"tags":["a"]}`,
		"minimum":             `{"name":"alpha","state":"open","count":0,"tags":["a"]}`,
		"maximum":             `{"name":"alpha","state":"open","count":6,"tags":["a"]}`,
		"minItems":            `{"name":"alpha","state":"open","count":3,"tags":[]}`,
		"maxItems":            `{"name":"alpha","state":"open","count":3,"tags":["a","b","c"]}`,
		"array item type":     `{"name":"alpha","state":"open","count":3,"tags":[1]}`,
		"required":            `{"name":"alpha","count":3,"tags":["a"]}`,
		"additional property": `{"name":"alpha","state":"open","count":3,"tags":["a"],"extra":true}`,
	}
	for name, body := range invalid {
		for _, phase := range []string{"request", "response"} {
			t.Run(name+"/"+phase, func(t *testing.T) {
				flow := validatingHTTPFlow()
				message := flow.Request.HTTP
				if phase == "response" {
					message = flow.Response.HTTP
				}
				message.Body.Text = body
				err := contract.validate(t.Context(), flow)
				if err == nil || errors.Is(err, errHTTPContractUnavailable) || !strings.Contains(err.Error(), phase+" violates OpenAPI") {
					t.Fatalf("wanted actual %s violation, got %v", phase, err)
				}
			})
		}
	}
}

func TestHTTPContractPreservesNegativeRequestExamplesAndValidatesErrorResponses(t *testing.T) {
	contract := loadAssumptionHTTPContract(t, validatingHTTPDocument(validatingHTTPBodySchema))
	flow := validatingHTTPFlow()
	flow.Request.HTTP.Body.Text = `{"invalid":`
	flow.Response.HTTP.Status, flow.Response.HTTP.Body.Text = 400, `{"error":"invalid request"}`
	if err := contract.validate(t.Context(), flow); err != nil {
		t.Fatalf("deliberately invalid 400 request was rejected: %v", err)
	}
	flow.Response.HTTP.Body.Text = `{"error":123}`
	if err := contract.validate(t.Context(), flow); err == nil || !strings.Contains(err.Error(), "response violates") {
		t.Fatalf("error response escaped validation: %v", err)
	}
}

func TestHTTPContractNoBodyGETHEADAnd204(t *testing.T) {
	doc := `{"openapi":"3.0.3","info":{"title":"Health","version":"1"},"paths":{"/health":{"get":{"responses":{"200":{"description":"OK"},"304":{"description":"Cached"}}},"head":{"responses":{"200":{"description":"OK"}}},"delete":{"responses":{"204":{"description":"Gone"}}}}}}`
	contract := loadAssumptionHTTPContract(t, doc)
	for _, entry := range []struct {
		method string
		status int
	}{{"GET", 200}, {"GET", 304}, {"HEAD", 200}, {"DELETE", 204}} {
		t.Run(fmt.Sprintf("%s/%d", entry.method, entry.status), func(t *testing.T) {
			flow := artifact.Flow{Protocol: "http", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: entry.method, URL: "http://local:3000/health"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: entry.status}}}
			if err := contract.validate(t.Context(), flow); err != nil {
				t.Fatal(err)
			}
			if entry.method == "HEAD" || entry.status == 204 || entry.status == 304 {
				flow.Response.HTTP.Body = &artifact.Body{Text: "unexpected"}
				if err := contract.validate(t.Context(), flow); err == nil {
					t.Fatal("forbidden response body passed")
				}
			}
		})
	}
}

func TestHTTPContractUsesSharedHTMLResponseDecoder(t *testing.T) {
	doc := `{"openapi":"3.0.3","info":{"title":"HTML","version":"1"},"paths":{"/":{"delete":{"responses":{"200":{"description":"OK","content":{"text/html":{"schema":{"type":"string","enum":["Property deleted"]}}}}}}}}}`
	contract := loadAssumptionHTTPContract(t, doc)
	flow := artifact.Flow{Protocol: "http", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "DELETE", URL: "http://local/"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"text/html; charset=utf-8"}}, Body: &artifact.Body{Text: "Property deleted"}}}}
	if err := contract.validate(t.Context(), flow); err != nil {
		t.Fatal(err)
	}
	flow.Response.HTTP.Body.Text = "Tenant deleted"
	if err := contract.validate(t.Context(), flow); err == nil {
		t.Fatal("wrong HTML enum value passed")
	}
}

func TestHTTPContractLoadsFrozenExternalReferencesWithoutSourceWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload.json"), []byte(validatingHTTPBodySchema), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "openapi.json"), []byte(validatingHTTPDocument(`{"$ref":"payload.json"}`)), 0600); err != nil {
		t.Fatal(err)
	}
	ref := freezeAssumptionHTTPContract(t, artifact.SchemaSource{Type: "file", Path: filepath.Join(root, "openapi.json")})
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	verifier := new(httpContractVerifier)
	contract, err := verifier.load(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.validate(t.Context(), validatingHTTPFlow()); err != nil {
		t.Fatal(err)
	}
	again, err := verifier.load(t.Context(), ref)
	if err != nil || again != contract {
		t.Fatalf("frozen contract cache failed: %v", err)
	}
}

func mutateFrozenHTTPDocument(t *testing.T, ref *artifact.SchemaRef, change func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(ref.Source.Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	change(doc)
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	ref.Source.SHA256 = hex.EncodeToString(digest[:])
	if err := os.WriteFile(ref.Source.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(filepath.Dir(ref.Source.Path), "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, value := range manifest["files"].([]any) {
		entry := value.(map[string]any)
		if filepath.Base(entry["path"].(string)) == filepath.Base(ref.Source.Path) {
			entry["sha256"] = ref.Source.SHA256
		}
	}
	manifestData, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestData, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPContractRejectsUnfrozenReferencesAndNeverFetchesNetwork(t *testing.T) {
	var requests atomic.Int32
	original := http.DefaultTransport
	http.DefaultTransport = httpContractCountingTransport{requests: &requests}
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, reference := range []string{"http://wiretap-network.invalid/schema.json", "not-listed.json"} {
		t.Run(reference, func(t *testing.T) {
			ref := freezeAssumptionHTTPContract(t, artifact.SchemaSource{Type: "inline", Content: validatingHTTPDocument(validatingHTTPBodySchema)})
			mutateFrozenHTTPDocument(t, ref, func(doc map[string]any) {
				operation := doc["paths"].(map[string]any)["/orders/{id}"].(map[string]any)["post"].(map[string]any)
				operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"] = map[string]any{"$ref": reference}
			})
			if _, err := new(httpContractVerifier).load(t.Context(), ref); !errors.Is(err, errHTTPContractUnavailable) {
				t.Fatalf("unfrozen reference passed: %v", err)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("offline contract validation made a network request")
	}
}

type httpContractCountingTransport struct{ requests *atomic.Int32 }

func (transport httpContractCountingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.requests.Add(1)
	return nil, errors.New("offline checker attempted a network request")
}

func TestHTTPContractRejectsUnsupportedVersionOverridesAndCorruptFiles(t *testing.T) {
	for _, kind := range []string{"3.1", "hash", "operation server"} {
		t.Run(kind, func(t *testing.T) {
			ref := freezeAssumptionHTTPContract(t, artifact.SchemaSource{Type: "inline", Content: validatingHTTPDocument(validatingHTTPBodySchema)})
			switch kind {
			case "3.1":
				mutateFrozenHTTPDocument(t, ref, func(doc map[string]any) { doc["openapi"] = "3.1.0" })
			case "hash":
				if err := os.WriteFile(ref.Source.Path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "operation server":
				mutateFrozenHTTPDocument(t, ref, func(doc map[string]any) {
					doc["paths"].(map[string]any)["/orders/{id}"].(map[string]any)["post"].(map[string]any)["servers"] = []any{map[string]any{"url": "https://other.example"}}
				})
			}
			contract, err := new(httpContractVerifier).load(t.Context(), ref)
			if err == nil {
				err = contract.validate(t.Context(), validatingHTTPFlow())
			}
			if !errors.Is(err, errHTTPContractUnavailable) {
				t.Fatalf("unsafe contract did not fail unavailable: %v", err)
			}
		})
	}
}

func TestHTTPContractRefusesRoundedRulesAndObservedNumbers(t *testing.T) {
	for _, schema := range []string{`{"type":"integer","minimum":9007199254740993}`, `{"type":"integer","enum":[9007199254740993]}`, `{"maxLength":9007199254740993}`, `{"type":"object","properties":{"description":{"type":"integer","maximum":9007199254740993}}}`, `{"type":"object","properties":{"default":{"type":"integer","maximum":9007199254740993}}}`, `{"type":"object","enum":[{"default":9007199254740993}]}`} {
		ref := freezeAssumptionHTTPContract(t, artifact.SchemaSource{Type: "inline", Content: validatingHTTPDocument(schema)})
		if _, err := new(httpContractVerifier).load(t.Context(), ref); !errors.Is(err, errHTTPContractUnavailable) {
			t.Fatalf("rounded rule passed: %v", err)
		}
	}
	contract := loadAssumptionHTTPContract(t, validatingHTTPDocument(`{"type":"integer"}`))
	flow := validatingHTTPFlow()
	flow.Request.HTTP.Body.Text, flow.Response.HTTP.Body.Text = "9007199254740993", "1"
	if err := contract.validate(t.Context(), flow); !errors.Is(err, errHTTPContractUnavailable) {
		t.Fatalf("rounded request passed: %v", err)
	}
	flow.Request.HTTP.Body.Text, flow.Response.HTTP.Body.Text = "1", "9007199254740993"
	if err := contract.validate(t.Context(), flow); !errors.Is(err, errHTTPContractUnavailable) {
		t.Fatalf("rounded response passed: %v", err)
	}
	flow.Response.HTTP.Body.Text = "1"
	flow.Response.HTTP.Headers["X-Rate"] = []string{"9007199254740993"}
	if err := contract.validate(t.Context(), flow); !errors.Is(err, errHTTPContractUnavailable) {
		t.Fatalf("rounded response header passed: %v", err)
	}
	document := strings.Replace(validatingHTTPDocument(`{"type":"integer"}`), `"post":{"requestBody"`, `"post":{"parameters":[{"name":"n","in":"query","schema":{"type":"integer"}}],"requestBody"`, 1)
	contract = loadAssumptionHTTPContract(t, document)
	flow = validatingHTTPFlow()
	flow.Request.HTTP.Body.Text, flow.Response.HTTP.Body.Text = "1", "1"
	for _, number := range []string{"9007199254740993", "1e999"} {
		flow.Request.HTTP.URL = "http://local/api/orders/one?n=" + number
		if err := contract.validate(t.Context(), flow); !errors.Is(err, errHTTPContractUnavailable) {
			t.Fatalf("rounded query parameter passed: %v", err)
		}
	}
}

func TestHTTPContractPrecisionUsesSchemaContextAndIgnoresAnnotations(t *testing.T) {
	document := validatingHTTPDocument(`{"type":"object","description":"Human documentation","example":{"maximum":9007199254740993},"default":{"maximum":9007199254740993},"properties":{"id":{"type":"integer"}}}`)
	loadAssumptionHTTPContract(t, document)
	for _, name := range []string{"description", "default"} {
		document := validatingHTTPDocument(`{"$ref":"#/components/schemas/` + name + `"}`)
		document = strings.Replace(document, `"paths":`, `"components":{"schemas":{"`+name+`":{"type":"integer","maximum":9007199254740993}}},"paths":`, 1)
		ref := freezeAssumptionHTTPContract(t, artifact.SchemaSource{Type: "inline", Content: document})
		if _, err := new(httpContractVerifier).load(t.Context(), ref); !errors.Is(err, errHTTPContractUnavailable) {
			t.Fatalf("rounded component named %q escaped guard: %v", name, err)
		}
	}
}

func TestHTTPContractRefusesUnguardedComplexAndContentNumericParameters(t *testing.T) {
	for _, parameter := range []string{
		`{"name":"filter","in":"query","style":"deepObject","explode":true,"schema":{"type":"object","properties":{"count":{"type":"integer","maximum":9007199254740992}}}}`,
		`{"name":"filter","in":"query","content":{"application/json":{"schema":{"type":"object","properties":{"count":{"type":"integer"}}}}}}`,
	} {
		document := strings.Replace(validatingHTTPDocument(`{"type":"integer"}`), `"post":{"requestBody"`, `"post":{"parameters":[`+parameter+`],"requestBody"`, 1)
		contract := loadAssumptionHTTPContract(t, document)
		flow := validatingHTTPFlow()
		flow.Request.HTTP.Body.Text, flow.Response.HTTP.Body.Text = "1", "1"
		flow.Request.HTTP.URL = "http://local/api/orders/one?filter%5Bcount%5D=9007199254740993&filter=%7B%22count%22%3A9007199254740993%7D"
		if err := contract.validate(t.Context(), flow); !errors.Is(err, errHTTPContractUnavailable) {
			t.Fatalf("complex parameter acquired numeric assurance: %v", err)
		}
	}
	document := strings.Replace(validatingHTTPDocument(`{"type":"integer"}`), `"X-Rate":{"schema":{"type":"integer","minimum":0}}`, `"X-Rate":{"content":{"application/json":{"schema":{"type":"integer"}}}}`, 1)
	contract := loadAssumptionHTTPContract(t, document)
	flow := validatingHTTPFlow()
	flow.Request.HTTP.Body.Text, flow.Response.HTTP.Body.Text = "1", "1"
	flow.Response.HTTP.Headers["X-Rate"] = []string{"9007199254740993"}
	if err := contract.validate(t.Context(), flow); !errors.Is(err, errHTTPContractUnavailable) {
		t.Fatalf("content response header acquired numeric assurance: %v", err)
	}
}

func TestHTTPContractDoesNotCertifyAmbiguousNullableConstraints(t *testing.T) {
	for _, schema := range []string{`{"type":"string","nullable":true,"enum":["active"]}`, `{"type":"string","nullable":true,"not":{"enum":["blocked"]}}`, `{"nullable":true,"oneOf":[{"type":"string"},{"type":"number"}]}`} {
		ref := freezeAssumptionHTTPContract(t, artifact.SchemaSource{Type: "inline", Content: validatingHTTPDocument(schema)})
		contract, err := new(httpContractVerifier).load(t.Context(), ref)
		if err == nil {
			flow := validatingHTTPFlow()
			flow.Request.HTTP.Body.Text, flow.Response.HTTP.Body.Text = "null", "null"
			err = contract.validate(t.Context(), flow)
		}
		if !errors.Is(err, errHTTPContractUnavailable) || !strings.Contains(err.Error(), "null-validation") {
			t.Fatalf("ambiguous nullable constraints acquired assurance: %v", err)
		}
	}
}
