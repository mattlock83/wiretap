package postman

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestGenerateCollectionFromVerifiedHTTPTestTraffic(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	url := "http://sut:8080/orders?tag=a&tag=b&space=a%20b&encoded=%2F&author=O'Reilly"
	requestBody := "{\n  \"name\": \"first\", \"count\": 1\n}\n"
	responseBody := "{\"id\":\"order-1\"}\n"
	first := httpFlow("flow-2", url, 201, started)
	first.DurationMS = 2000 // It starts first, but finishes after the other request.
	first.Request.HTTP.Method = "POST"
	first.Request.HTTP.Headers = artifact.Headers{
		"Content-Type": {"application/json"}, "Cookie": {"recorded-session=one"},
		"Accept": {"application/json"}, "Cache-Control": {"max-age=30"}, "X-Tag": {"one", "two"},
	}
	first.Request.HTTP.Body = &artifact.Body{Text: requestBody}
	first.Response.HTTP.Headers = artifact.Headers{"Content-Type": {"application/json"}, "Set-Cookie": {"a=1", "b=2"}}
	first.Response.HTTP.Body = &artifact.Body{Text: responseBody}
	second := httpFlow("flow-10", "http://sut:8080/orders/order-1", 200, started.Add(time.Second))
	second.DurationMS = 1
	dependency := httpFlow("dependency", "http://dependency/private", 200, started)
	dependency.Channel = "sut_to_dependency"
	grpc := artifact.Flow{
		ID: "grpc", Protocol: "grpc", Channel: "test_to_sut",
		Request:  artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health"}},
		Response: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health", HTTPStatus: 200}},
	}
	httpCapture := capture("test-http", "creates and reads an order", started, []artifact.Flow{second, dependency, grpc, first})
	grpcCapture := capture("test-grpc", "gRPC test", started, []artifact.Flow{grpc})
	rawDescriptor, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(grpc_health_v1.File_grpc_health_v1_health_proto)}})
	if err != nil {
		t.Fatal(err)
	}
	schema := artifact.SchemaRef{ID: "health", Role: "sut", Type: "protobuf", Services: []string{"grpc.health.v1.Health"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(rawDescriptor)}}
	httpCapture.Schemas, grpcCapture.Schemas = []artifact.SchemaRef{schema}, []artifact.SchemaRef{schema}
	root := fixtureBundle(t, httpCapture, capture("test-empty", "empty test", started, nil), grpcCapture)
	before := bundleFiles(t, root)
	collection, firstBytes := generateJSON(t, root)
	_, secondBytes := generateJSON(t, root)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("repeated exports differ")
	}
	if !reflect.DeepEqual(before, bundleFiles(t, root)) {
		t.Fatal("export mutated bundle evidence")
	}
	info := object(t, collection["info"])
	if info["name"] != "Captured tests" || info["schema"] != "https://schema.getpostman.com/json/collection/v2.1.0/collection.json" {
		t.Fatalf("unexpected collection info: %#v", info)
	}
	behavior := object(t, collection["protocolProfileBehavior"])
	if behavior["followRedirects"] != false || behavior["disableBodyPruning"] != true || behavior["disableCookies"] != true || behavior["disableUrlEncoding"] != true {
		t.Fatalf("Postman defaults will alter captured requests: %#v", behavior)
	}
	folders := array(t, collection["item"])
	if len(folders) != 3 {
		t.Fatalf("wanted one folder per test, got %d", len(folders))
	}
	byName := make(map[string]map[string]any)
	for _, value := range folders {
		folder := object(t, value)
		byName[folder["name"].(string)] = folder
	}
	for _, name := range []string{"empty test", "gRPC test"} {
		folder, ok := byName[name]
		if !ok || len(array(t, folder["item"])) != 0 {
			t.Fatalf("wanted an empty folder for %q: %#v", name, folder)
		}
	}
	items := array(t, byName["creates and reads an order"]["item"])
	if len(items) != 2 {
		t.Fatalf("wanted only HTTP test-to-SUT requests, got %d", len(items))
	}
	firstItem := object(t, items[0])
	request := object(t, firstItem["request"])
	if request["method"] != "POST" || request["url"] != url {
		t.Fatalf("first request was not preserved in request-start order: %#v", request)
	}
	if got := object(t, request["body"]); got["mode"] != "raw" || got["raw"] != requestBody {
		t.Fatalf("raw request body changed: %#v", got)
	}
	wantHeaders := map[string][]string{
		"Content-Type": {"application/json"}, "Cookie": {"recorded-session=one"},
		"Accept": {"application/json"}, "Cache-Control": {"max-age=30"}, "X-Tag": {"one", "two"},
	}
	if got := jsonHeaders(t, request["header"]); !reflect.DeepEqual(got, wantHeaders) {
		t.Fatalf("request headers changed: %#v", got)
	}
	responses := array(t, firstItem["response"])
	if len(responses) != 1 {
		t.Fatalf("wanted one saved response, got %d", len(responses))
	}
	response := object(t, responses[0])
	if response["code"] != float64(201) || response["status"] != "Created" || response["body"] != responseBody {
		t.Fatalf("saved response changed: %#v", response)
	}
	wantResponseHeaders := map[string][]string{"Content-Type": {"application/json"}, "Set-Cookie": {"a=1", "b=2"}}
	if got := jsonHeaders(t, response["header"]); !reflect.DeepEqual(got, wantResponseHeaders) {
		t.Fatalf("response headers changed: %#v", got)
	}
	if !reflect.DeepEqual(response["originalRequest"], request) {
		t.Fatalf("saved response originalRequest differs from request: %#v", response["originalRequest"])
	}
	assertStatusTest(t, firstItem, 201)
	secondItem := object(t, items[1])
	if got := object(t, secondItem["request"])["url"]; got != second.Request.HTTP.URL {
		t.Fatalf("second request was not preserved in request-start order: %v", got)
	}
	assertStatusTest(t, secondItem, 200)
}

func TestGenerateRetainsStoredOrderWhenRequestChronologyIsUnavailable(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	flows := []artifact.Flow{
		httpFlow("flow-z", "http://sut/later", 200, started.Add(time.Second)),
		httpFlow("flow-m", "http://sut/unknown", 200, time.Time{}),
		httpFlow("flow-a", "http://sut/earlier", 200, started),
	}
	root := fixtureBundle(t, capture("legacy", "legacy test", started, flows))
	collection, _ := generateJSON(t, root)
	folder := object(t, array(t, collection["item"])[0])
	note, _ := folder["description"].(string)
	if !strings.Contains(strings.ToLower(note), "chronolog") || !strings.Contains(strings.ToLower(note), "unavailable") {
		t.Fatalf("folder does not explain unavailable chronology: %q", note)
	}
	items := array(t, folder["item"])
	if len(items) != len(flows) {
		t.Fatalf("wanted %d requests, got %d", len(flows), len(items))
	}
	for i, item := range items {
		request := object(t, object(t, item)["request"])
		if got := request["url"]; got != flows[i].Request.HTTP.URL {
			t.Fatalf("stored request %d moved: got %v, want %q", i, got, flows[i].Request.HTTP.URL)
		}
	}
}

func TestGenerateEmbedsBinaryRequestPayloadsAsBase64(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	requestBase64 := base64.StdEncoding.EncodeToString([]byte{0, 255, 1, 128})
	responseBase64 := base64.StdEncoding.EncodeToString([]byte{255, 0, 128, 2})
	flow := httpFlow("binary", "http://sut/upload", 202, started)
	flow.Request.HTTP.Method = "POST"
	flow.Request.HTTP.Body = &artifact.Body{Encoding: "base64", Bytes: requestBase64}
	flow.Response.HTTP.Body = &artifact.Body{Encoding: "base64", Bytes: responseBase64}
	root := fixtureBundle(t, capture("upload", "binary upload", started, []artifact.Flow{flow}))
	collection, _ := generateJSON(t, root)
	folder := object(t, array(t, collection["item"])[0])
	item := object(t, array(t, folder["item"])[0])
	request := object(t, item["request"])
	body := object(t, request["body"])
	if body["mode"] != "file" {
		t.Fatalf("binary request is not a file body: %#v", body)
	}
	file := object(t, body["file"])
	if value, exists := file["src"]; !exists || value != nil {
		t.Fatalf("binary request unexpectedly refers to a local file: %#v", file)
	}
	if file["content"] != requestBase64 {
		t.Fatalf("binary request bytes were not embedded as base64: %#v", file)
	}
	response := object(t, array(t, item["response"])[0])
	name, _ := response["name"].(string)
	if response["body"] != responseBase64 || !strings.Contains(strings.ToLower(name), "base64") {
		t.Fatalf("binary response is not explicitly retained as base64: %#v", response)
	}
	assertStatusTest(t, item, 202)
}

func TestGenerateRejectsTamperedDetachedPayload(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	flow := httpFlow("request", "http://sut/orders", 200, started)
	flow.Response.HTTP.Body = &artifact.Body{Text: "verified payload"}
	root := fixtureBundle(t, capture("integrity", "integrity", started, []artifact.Flow{flow}))
	hash := sha256.Sum256([]byte("verified payload"))
	path := filepath.Join(root, "payloads", hex.EncodeToString(hash[:])+".bin")
	if err := os.WriteFile(path, []byte("tampered payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, "Captured tests"); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("wanted verified bundle payload failure, got %v", err)
	}
}

func TestGenerateKeepsPartialResponsesWithoutInventingStatusAssertions(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	missing := httpFlow("missing", "http://sut/no-response", 0, started)
	missing.Response.HTTP = nil
	partial := httpFlow("partial", "http://sut/partial", 0, started.Add(time.Second))
	partial.Response.HTTP.Body = &artifact.Body{Text: "retained partial response", Truncated: true}
	root := fixtureBundle(t, capture("incomplete", "incomplete", started, []artifact.Flow{missing, partial}))
	collection, _ := generateJSON(t, root)
	folder := object(t, array(t, collection["item"])[0])
	items := array(t, folder["item"])
	for i, entry := range items {
		item := object(t, entry)
		event := object(t, array(t, item["event"])[0])
		script := object(t, event["script"])
		lines := array(t, script["exec"])
		if len(lines) != 1 || !strings.Contains(lines[0].(string), "pm.test.skip(") {
			t.Fatalf("missing status was presented as a real assertion: %#v", script)
		}
		responses := array(t, item["response"])
		if i == 0 && len(responses) != 0 {
			t.Fatalf("invented response: %#v", responses)
		}
		if i == 1 {
			if len(responses) != 1 || object(t, responses[0])["body"] != "retained partial response" {
				t.Fatalf("lost partial response: %#v", responses)
			}
			if _, exists := object(t, responses[0])["code"]; exists {
				t.Fatalf("invented response status: %#v", responses)
			}
		}
	}
}

func TestCollectionNameUsesApplicationIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, override, want string
		identity             bundle.Identity
	}{
		{name: "application", identity: bundle.Identity{Application: &bundle.Application{Name: "Orders", ID: "orders-app"}, APIID: "orders-api"}, want: "Orders - orders-app"},
		{name: "legacy API identity", identity: bundle.Identity{Application: &bundle.Application{Name: "Orders"}, APIID: "orders-api"}, want: "Orders - orders-api"},
		{name: "name only", identity: bundle.Identity{Application: &bundle.Application{Name: "Orders"}}, want: "Orders"},
		{name: "application ID only", identity: bundle.Identity{Application: &bundle.Application{ID: "orders-app"}}, want: "orders-app"},
		{name: "explicit override", identity: bundle.Identity{Application: &bundle.Application{Name: "Orders", ID: "orders-app"}}, override: "Custom collection", want: "Custom collection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer, err := bundle.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.Configure(tc.identity); err != nil {
				t.Fatal(err)
			}
			exports, err := Generate(writer.Path(), tc.override)
			if err != nil {
				t.Fatal(err)
			}
			collection := exports[0].Collection
			if collection.Info.Name != tc.want {
				t.Fatalf("collection name = %q, want %q", collection.Info.Name, tc.want)
			}
		})
	}
}

func TestGenerateRequestHeadersMatchRetainedBody(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		body        artifact.Body
		keepEncoded bool
	}{
		{name: "decoded text", body: artifact.Body{Text: "decoded", ContentEncoding: "gzip"}},
		{name: "decoded binary", body: artifact.Body{Encoding: "base64", Bytes: "/wA=", ContentEncoding: "gzip"}},
		{name: "retained encoded bytes", body: artifact.Body{Encoding: "base64", Bytes: "H4v/AA=="}, keepEncoded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flow := httpFlow("request", "http://sut/body", 200, started)
			flow.Request.HTTP.Body = &tc.body
			flow.Request.HTTP.Headers = artifact.Headers{
				"Host": {"old-target"}, "Content-Length": {"999"}, "Transfer-Encoding": {"chunked"},
				"Connection": {"keep-alive, X-Hop"}, "X-Hop": {"connection-specific"},
				"Content-Encoding": {"gzip"}, "X-Keep": {"one", "two"},
			}
			root := fixtureBundle(t, capture("body", "body", started, []artifact.Flow{flow}))
			collection, _ := generateJSON(t, root)
			folder := object(t, array(t, collection["item"])[0])
			item := object(t, array(t, folder["item"])[0])
			request := object(t, item["request"])
			want := map[string][]string{"X-Keep": {"one", "two"}}
			if tc.keepEncoded {
				want["Content-Encoding"] = []string{"gzip"}
			}
			if got := jsonHeaders(t, request["header"]); !reflect.DeepEqual(got, want) {
				t.Fatalf("headers do not match retained body: got %#v, want %#v", got, want)
			}
		})
	}
}

func TestGenerateDoesNotInventOptionalHeadersForRawBodies(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	flow := httpFlow("request", "http://sut/body", 200, started)
	flow.Request.HTTP.Method = "POST"
	flow.Request.HTTP.Body = &artifact.Body{Text: "captured body"}
	root := fixtureBundle(t, capture("body", "body", started, []artifact.Flow{flow}))
	collection, _ := generateJSON(t, root)
	behavior := object(t, collection["protocolProfileBehavior"])
	disabled := object(t, behavior["disabledSystemHeaders"])
	for _, name := range []string{"content-type", "accept", "accept-encoding", "user-agent", "cache-control", "postman-token"} {
		if disabled[name] != true {
			t.Errorf("Postman will add an uncaptured %s header: %#v", name, disabled)
		}
	}
	folder := object(t, array(t, collection["item"])[0])
	request := object(t, object(t, array(t, folder["item"])[0])["request"])
	if len(jsonHeaders(t, request["header"])) != 0 || object(t, request["body"])["mode"] != "raw" {
		t.Fatalf("captured raw request changed: %#v", request)
	}
}

func TestGeneratePreservesInvalidJSONUsedByNegativeTests(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	flow := httpFlow("request", "http://sut/orders", 400, started)
	flow.Request.HTTP.Method = "POST"
	flow.Request.HTTP.Headers = artifact.Headers{"Content-Type": {"application/json"}}
	flow.Request.HTTP.Body = &artifact.Body{Text: `{"count":1/* invalid JSON */}`}
	root := fixtureBundle(t, capture("invalid-json", "rejects invalid JSON", started, []artifact.Flow{flow}))
	collection, _ := generateJSON(t, root)
	folder := object(t, array(t, collection["item"])[0])
	item := object(t, array(t, folder["item"])[0])
	request := object(t, item["request"])
	body := object(t, request["body"])
	if body["raw"] != flow.Request.HTTP.Body.Text || object(t, object(t, body["options"])["raw"])["language"] != "text" {
		t.Fatalf("Postman will strip comments from the captured negative-test body: %#v", body)
	}
	if got := jsonHeaders(t, request["header"]); !reflect.DeepEqual(got, map[string][]string{"Content-Type": {"application/json"}}) {
		t.Fatalf("negative-test Content-Type changed: %#v", got)
	}
	assertStatusTest(t, item, 400)
}

func capture(id, name string, started time.Time, flows []artifact.Flow) *artifact.Artifact {
	return &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: id, Name: name, StartedAt: started}, Traffic: flows}
}

func httpFlow(id, url string, status int, started time.Time) artifact.Flow {
	return artifact.Flow{
		ID: id, Protocol: "http", Channel: "test_to_sut", StartedAt: started,
		Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: url}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: status}},
	}
}

func fixtureBundle(t *testing.T, captures ...*artifact.Artifact) string {
	t.Helper()
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, capture := range captures {
		frozen, err := w.Register(capture.TestCase, capture.Schemas)
		if err != nil {
			t.Fatal(err)
		}
		capture.Schemas = frozen
		if _, err := w.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	return w.Path()
}

func generateJSON(t *testing.T, root string) (map[string]any, []byte) {
	t.Helper()
	exports, err := Generate(root, "Captured tests")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Write(&output, exports[0].Collection); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result, output.Bytes()
}

func object(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("wanted JSON object, got %#v", value)
	}
	return result
}

func array(t *testing.T, value any) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("wanted JSON array, got %#v", value)
	}
	return result
}

func jsonHeaders(t *testing.T, value any) map[string][]string {
	t.Helper()
	result := map[string][]string{}
	for _, entry := range array(t, value) {
		header := object(t, entry)
		key, keyOK := header["key"].(string)
		value, valueOK := header["value"].(string)
		if !keyOK || !valueOK {
			t.Fatalf("invalid Postman header: %#v", header)
		}
		result[key] = append(result[key], value)
	}
	return result
}

func assertStatusTest(t *testing.T, item map[string]any, code int) {
	t.Helper()
	want := "pm.response.to.have.status(" + jsonNumber(code) + ")"
	for _, entry := range array(t, item["event"]) {
		event := object(t, entry)
		if event["listen"] != "test" {
			continue
		}
		script := object(t, event["script"])
		if script["type"] != "text/javascript" {
			t.Fatalf("unexpected status test script type: %#v", script)
		}
		var lines []string
		for _, line := range array(t, script["exec"]) {
			lines = append(lines, line.(string))
		}
		if got := strings.Join(lines, "\n"); strings.Contains(got, "pm.test(") && strings.Contains(got, want) {
			return
		}
	}
	t.Fatalf("request has no status test asserting the recorded code %d: %#v", code, item["event"])
}

func jsonNumber(value int) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func bundleFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
