package assumptions

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/postman"
)

func collectionHTTPFixture() postman.Export {
	raw := `{"id":9007199254740993,"name":"thing"}`
	request := postman.Request{Method: "POST", URL: "http://provider.test/orders?tag=a&tag=b", Headers: []postman.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "X-Tag", Value: "one"}, {Key: "X-Tag", Value: "two"}}, Body: &postman.RequestBody{Mode: "raw", Raw: &raw}}
	response := postman.Response{Code: 201, Headers: []postman.Header{{Key: "Content-Type", Value: "application/json"}}, Body: `{"ok":true,"values":[1,2]}`, OriginalRequest: request}
	return postman.Export{Filename: "mock-orders.postman_collection.json", BoundaryID: "orders", Collection: &postman.Collection{Info: postman.Info{Name: "orders", Schema: "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"}, Items: []postman.Folder{{ID: "attempt-1", Name: "creates order", Items: []postman.Item{{Name: "POST order", Request: request, Responses: []postman.Response{response}}}}, {ID: "attempt-empty", Name: "no request", Items: []postman.Item{}}}}}
}

func collectionGRPCFixture() postman.Export {
	call := postman.GRPCCall{MethodPath: "orders.v1.Orders/Watch", Metadata: []postman.Header{{Key: "X-Client", Value: "test"}}, Message: &postman.GRPCContent{Content: `{"id":"one"}`}}
	response := postman.GRPCResponse{Code: 0, Metadata: []postman.Header{{Key: "content-type", Value: "application/grpc"}}, Trailers: []postman.Header{{Key: "grpc-status", Value: "0"}}, Messages: []postman.GRPCMessage{{ID: "message-1", Type: "outgoing", Content: `{"id":"one"}`}, {ID: "message-2", Type: "incoming", Content: `{"id":"one","state":"OPEN"}`}, {ID: "message-3", Type: "incoming", Content: `{"id":"one","state":"CLOSED"}`}}}
	return postman.Export{Filename: "mock-orders.grpc.postman_collection.json", BoundaryID: "orders", GRPC: &postman.GRPCCollection{Kind: "collection", Name: "orders gRPC", Items: []postman.GRPCFolder{{Kind: "collection", ID: "attempt-1", Name: "watches order", Items: []postman.GRPCRequest{{Kind: "grpc-request", ID: "step-1", Name: "Watch", GRPCCall: call, Examples: []postman.GRPCExample{{Kind: "grpc-example", Name: "capture", Request: call, Response: response}}}}}}}}
}

func collectionObject(t *testing.T, export postman.Export) map[string]any {
	t.Helper()
	var data bytes.Buffer
	if err := postman.WriteExport(&data, export); err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func writeCollectionObject(t *testing.T, root, name string, object map[string]any) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func firstHTTPItem(object map[string]any) map[string]any {
	return object["item"].([]any)[0].(map[string]any)["item"].([]any)[0].(map[string]any)
}
func firstGRPCItem(object map[string]any) map[string]any {
	return object["items"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
}
func firstHTTPResponse(item map[string]any) map[string]any {
	return item["response"].([]any)[0].(map[string]any)
}
func firstGRPCResponse(item map[string]any) map[string]any {
	return item["examples"].([]any)[0].(map[string]any)["response"].(map[string]any)
}

func TestVerifyCollectionsAcceptsCompleteRESTAndGRPCExports(t *testing.T) {
	exports := []postman.Export{collectionHTTPFixture(), collectionGRPCFixture()}
	root := t.TempDir()
	var files []string
	for _, export := range exports {
		files = append(files, writeCollectionObject(t, root, export.Filename, collectionObject(t, export)))
	}
	if err := VerifyCollections(files, exports); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCollectionsNormalizesSDKURLsFoldersHeadersAndJSON(t *testing.T) {
	export := collectionHTTPFixture()
	object := collectionObject(t, export)
	item := firstHTTPItem(object)
	for _, request := range []map[string]any{item["request"].(map[string]any), firstHTTPResponse(item)["originalRequest"].(map[string]any)} {
		request["url"] = map[string]any{"raw": "http://provider.test/orders?tag=a&tag=b", "protocol": "http", "host": []string{"provider", "test"}, "path": []string{"orders"}, "query": []any{map[string]any{"key": "tag", "value": "a"}, map[string]any{"key": "tag", "value": "b"}, map[string]any{"key": "disabled", "value": "ignored", "disabled": true}}}
		request["body"].(map[string]any)["raw"] = "{\n \"name\": \"thing\", \"id\": 9007199254740993.0\n}"
		request["header"].([]any)[0].(map[string]any)["key"] = "content-type"
		request["header"] = append(request["header"].([]any), map[string]any{"key": "disabled", "value": "ignored", "disabled": true})
		request["auth"] = map[string]any{"type": "noauth"}
	}
	firstHTTPResponse(item)["body"] = `{ "values": [1.0, 2e0], "ok": true }`
	item["name"], item["event"] = "new label", []any{map[string]any{"listen": "test", "script": map[string]any{"exec": []string{"throw new Error('must never run')"}}}}
	folder := object["item"].([]any)[0].(map[string]any)
	folder["item"] = []any{map[string]any{"id": "presentation-folder", "name": "nested", "item": folder["item"]}}
	object["item"] = []any{map[string]any{"id": "presentation-wrapper", "name": "wrapper", "item": object["item"]}}
	file := writeCollectionObject(t, t.TempDir(), "sdk.json", object)
	if err := VerifyCollections([]string{file}, []postman.Export{export}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCollectionsRejectsEditedNonJSONBodyBytes(t *testing.T) {
	for _, media := range []string{"text/plain", "application/octet-stream"} {
		for _, phase := range []string{"request", "response"} {
			t.Run(media+"/"+phase, func(t *testing.T) {
				export := collectionHTTPFixture()
				item := &export.Collection.Items[0].Items[0]
				body := "1"
				item.Request.Headers[0].Value = media
				item.Request.Body.Raw = &body
				item.Responses[0].OriginalRequest = item.Request
				item.Responses[0].Headers[0].Value = media
				item.Responses[0].Body = body
				object := collectionObject(t, export)
				if phase == "request" {
					firstHTTPItem(object)["request"].(map[string]any)["body"].(map[string]any)["raw"] = "1.0"
				} else {
					firstHTTPResponse(firstHTTPItem(object))["body"] = "1.0"
				}
				file := writeCollectionObject(t, t.TempDir(), "edited.json", object)
				if err := VerifyCollections([]string{file}, []postman.Export{export}); err == nil {
					t.Fatalf("edited %s %s bytes were accepted as equivalent JSON", media, phase)
				}
			})
		}
	}
}

func TestVerifyCollectionsRejectsChangedHTTPAssumptions(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"request value": func(object map[string]any) {
			firstHTTPItem(object)["request"].(map[string]any)["body"].(map[string]any)["raw"] = `{"id":9007199254740992,"name":"thing"}`
		},
		"response value": func(object map[string]any) {
			firstHTTPResponse(firstHTTPItem(object))["body"] = `{"ok":false,"values":[1,2]}`
		},
		"array order": func(object map[string]any) {
			firstHTTPResponse(firstHTTPItem(object))["body"] = `{"ok":true,"values":[2,1]}`
		},
		"status": func(object map[string]any) { firstHTTPResponse(firstHTTPItem(object))["code"] = 200 },
		"response header": func(object map[string]any) {
			firstHTTPResponse(firstHTTPItem(object))["header"].([]any)[0].(map[string]any)["value"] = "text/plain"
		},
		"request header": func(object map[string]any) {
			firstHTTPItem(object)["request"].(map[string]any)["header"].([]any)[1].(map[string]any)["value"] = "changed"
		},
		"original request": func(object map[string]any) {
			firstHTTPResponse(firstHTTPItem(object))["originalRequest"].(map[string]any)["method"] = "GET"
		},
		"missing example": func(object map[string]any) { firstHTTPItem(object)["response"] = []any{} },
		"extra exchange": func(object map[string]any) {
			folder := object["item"].([]any)[0].(map[string]any)
			folder["item"] = append(folder["item"].([]any), firstHTTPItem(object))
		},
		"missing exchange":      func(object map[string]any) { object["item"].([]any)[0].(map[string]any)["item"] = []any{} },
		"missing empty attempt": func(object map[string]any) { object["item"] = object["item"].([]any)[:1] },
		"duplicate empty attempt": func(object map[string]any) {
			object["item"] = append(object["item"].([]any), object["item"].([]any)[1])
		},
		"stale attempt": func(object map[string]any) { object["item"].([]any)[0].(map[string]any)["id"] = "old-attempt" },
		"auth":          func(object map[string]any) { object["auth"] = map[string]any{"type": "bearer"} },
		"file body": func(object map[string]any) {
			firstHTTPItem(object)["request"].(map[string]any)["body"] = map[string]any{"mode": "file", "file": map[string]any{"src": "outside.json", "content": "e30="}}
		},
		"form transform": func(object map[string]any) {
			firstHTTPItem(object)["request"].(map[string]any)["body"] = map[string]any{"mode": "formdata", "formdata": []any{}}
		},
		"contradictory URL": func(object map[string]any) {
			firstHTTPItem(object)["request"].(map[string]any)["url"] = map[string]any{"raw": "http://provider.test/orders?tag=a&tag=b", "path": []string{"changed"}}
		},
		"contradictory URL string host": func(object map[string]any) {
			firstHTTPItem(object)["request"].(map[string]any)["url"] = map[string]any{"raw": "http://provider.test/orders?tag=a&tag=b", "host": "other.test"}
		},
		"method case": func(object map[string]any) {
			firstHTTPItem(object)["request"].(map[string]any)["method"] = "post"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			export := collectionHTTPFixture()
			object := collectionObject(t, export)
			mutate(object)
			file := writeCollectionObject(t, t.TempDir(), "edited.json", object)
			err := VerifyCollections([]string{file}, []postman.Export{export})
			if err == nil || !strings.Contains(err.Error(), file) {
				t.Fatalf("wanted path-qualified rejection, got %v", err)
			}
		})
	}
}

func TestVerifyCollectionsDirectoriesSelectExpectedBoundaryAndIgnoreCompanions(t *testing.T) {
	export := collectionHTTPFixture()
	root := t.TempDir()
	writeCollectionObject(t, root, "exports/nested/"+export.Filename, collectionObject(t, export))
	unrelated := collectionObject(t, export)
	firstHTTPItem(unrelated)["request"].(map[string]any)["url"] = "http://other.test/"
	unrelated["item"].([]any)[0].(map[string]any)["id"] = "unrelated-run-attempt"
	writeCollectionObject(t, root, "mock-other.postman_collection.json", unrelated)
	if err := os.WriteFile(filepath.Join(root, "diagnostics.json"), []byte("not a collection"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCollections([]string{root}, []postman.Export{export}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "exports/nested/", export.Filename)); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCollections([]string{root}, []postman.Export{export}); err == nil || !strings.Contains(err.Error(), export.Filename) {
		t.Fatalf("missing selected boundary passed: %v", err)
	}
	file := filepath.Join(root, "mock-other.postman_collection.json")
	if err := VerifyCollections([]string{file}, []postman.Export{export}); err == nil {
		t.Fatal("unrelated direct input passed")
	}
}

func TestVerifyCollectionsGRPCTargetOverridesAndCanonicalMessages(t *testing.T) {
	export := collectionGRPCFixture()
	object := collectionObject(t, export)
	item := firstGRPCItem(object)
	item["url"] = "other:443"
	item["schema"] = map[string]any{"source": "file", "location": "schemas/relocated.proto"}
	item["settings"] = map[string]any{"secureConnection": true}
	item["id"], item["name"] = "new-human-id", "renamed"
	item["examples"].([]any)[0].(map[string]any)["request"].(map[string]any)["url"] = "another:5000"
	firstGRPCResponse(item)["messages"].([]any)[1].(map[string]any)["content"] = `{ "state": "OPEN", "id": "one" }`
	file := writeCollectionObject(t, t.TempDir(), "grpc.json", object)
	if err := VerifyCollections([]string{file}, []postman.Export{export}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCollectionsRejectsGRPCStatusValuesMetadataAndStreamOrder(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"status":   func(item map[string]any) { firstGRPCResponse(item)["code"] = 5 },
		"metadata": func(item map[string]any) { item["metadata"].([]any)[0].(map[string]any)["value"] = "changed" },
		"trailers": func(item map[string]any) {
			firstGRPCResponse(item)["trailers"].([]any)[0].(map[string]any)["value"] = "5"
		},
		"message value": func(item map[string]any) {
			firstGRPCResponse(item)["messages"].([]any)[1].(map[string]any)["content"] = `{"id":"one","state":"OTHER"}`
		},
		"direction": func(item map[string]any) {
			firstGRPCResponse(item)["messages"].([]any)[0].(map[string]any)["type"] = "incoming"
		},
		"stream order": func(item map[string]any) {
			messages := firstGRPCResponse(item)["messages"].([]any)
			messages[1], messages[2] = messages[2], messages[1]
		},
		"missing message": func(item map[string]any) {
			response := firstGRPCResponse(item)
			response["messages"] = response["messages"].([]any)[:2]
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			export := collectionGRPCFixture()
			object := collectionObject(t, export)
			mutate(firstGRPCItem(object))
			file := writeCollectionObject(t, t.TempDir(), "changed-grpc.json", object)
			if err := VerifyCollections([]string{file}, []postman.Export{export}); err == nil {
				t.Fatal("changed gRPC example passed")
			}
		})
	}
}

func TestVerifyCollectionsRetainsBinaryAndMalformedNegativeBodies(t *testing.T) {
	for _, mode := range []string{"binary", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			export := collectionHTTPFixture()
			item := &export.Collection.Items[0].Items[0]
			if mode == "binary" {
				item.Request.Body = &postman.RequestBody{Mode: "file", File: &postman.File{Content: "AP+A"}}
			} else {
				raw := `{"invalid":`
				item.Request.Body = &postman.RequestBody{Mode: "raw", Raw: &raw}
			}
			item.Responses[0].OriginalRequest = item.Request
			file := writeCollectionObject(t, t.TempDir(), "negative.json", collectionObject(t, export))
			if err := VerifyCollections([]string{file}, []postman.Export{export}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVerifyCollectionsDoesNotInterpretBinaryResponsesAsJSON(t *testing.T) {
	export := collectionHTTPFixture()
	response := &export.Collection.Items[0].Items[0].Responses[0]
	// "null" is also base64 for non-UTF-8 bytes. JSON whitespace changes the
	// exported binary representation and must not be normalized away.
	response.Name, response.Body = "Captured response: 201 (body is base64)", "null"
	object := collectionObject(t, export)
	firstHTTPResponse(firstHTTPItem(object))["name"] = "a human can rename this"
	file := writeCollectionObject(t, t.TempDir(), "binary.json", object)
	if err := VerifyCollections([]string{file}, []postman.Export{export}); err != nil {
		t.Fatal(err)
	}
	firstHTTPResponse(firstHTTPItem(object))["body"] = " null "
	file = writeCollectionObject(t, t.TempDir(), "edited-binary.json", object)
	if err := VerifyCollections([]string{file}, []postman.Export{export}); err == nil {
		t.Fatal("binary response was normalized as JSON")
	}
}

func TestVerifyCollectionsPreservesInvalidUnicodeNegativeBodyBytes(t *testing.T) {
	export := collectionHTTPFixture()
	response := &export.Collection.Items[0].Items[0].Responses[0]
	response.Body = `{"value":"\ud800"}`
	object := collectionObject(t, export)
	file := writeCollectionObject(t, t.TempDir(), "negative-unicode.json", object)
	if err := VerifyCollections([]string{file}, []postman.Export{export}); err != nil {
		t.Fatal(err)
	}
	firstHTTPResponse(firstHTTPItem(object))["body"] = `{"value":"�"}`
	file = writeCollectionObject(t, t.TempDir(), "replacement-unicode.json", object)
	if err := VerifyCollections([]string{file}, []postman.Export{export}); err == nil {
		t.Fatal("invalid surrogate and replacement character were treated as equal")
	}
}

func TestVerifyCollectionsBoundsAndAmbiguousJSON(t *testing.T) {
	export := collectionHTTPFixture()
	for name, data := range map[string]string{"duplicate keys": `{"info":{},"info":{},"item":[]}`, "trailing JSON": `{} {}`, "depth": strings.Repeat("[", maxCollectionDepth+2) + strings.Repeat("]", maxCollectionDepth+2), "invalid surrogate": `{"info":{"name":"\ud800"},"item":[]}`} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "invalid.json")
			if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := VerifyCollections([]string{file}, []postman.Export{export}); err == nil || !strings.Contains(err.Error(), file) {
				t.Fatalf("wanted path-qualified limit error, got %v", err)
			}
		})
	}
	t.Run("file size", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "large.json")
		f, err := os.Create(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(maxCollectionBytes + 1); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
		if err := VerifyCollections([]string{file}, []postman.Export{export}); err == nil || !strings.Contains(err.Error(), "MiB") {
			t.Fatalf("wanted size error, got %v", err)
		}
	})
	if err := VerifyCollections(nil, []postman.Export{export}); err == nil {
		t.Fatal("missing inputs passed")
	}
}
