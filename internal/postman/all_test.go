package postman

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

func TestGenerateSeparatesCapturedMocksByAssetIdentity(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	inventoryEarly := dependencyFlow("flow-2", "inventory", "http://shared-mock/first", 201, started.Add(time.Second), "inventory first")
	inventoryEarly.Request.HTTP.Method = "POST"
	inventoryEarly.Request.HTTP.Body = &artifact.Body{Text: "{\"sku\":\"one\"}\n"}
	inventoryEarly.Response.HTTP.Headers = artifact.Headers{"X-Result": {"one", "two"}}
	inventoryLate := dependencyFlow("flow-10", "inventory", "http://shared-mock/second", 200, started.Add(2*time.Second), "inventory second")
	paymentEarly := dependencyFlow("payment", "payments", "http://shared-mock/first", 202, started, "payment first")
	service := dependencyFlow("service", "live-service", "http://shared-mock/first", 200, time.Time{}, "service response")
	unknown := dependencyFlow("unknown", "unknown", "http://shared-mock/first", 200, time.Time{}, "unknown response")
	rawDescriptor, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(grpc_health_v1.File_grpc_health_v1_health_proto)}})
	if err != nil {
		t.Fatal(err)
	}
	grpcSchema := artifact.SchemaRef{ID: "health", AssetID: "grpc-only", Role: "dependency", Type: "protobuf", Services: []string{"grpc.health.v1.Health"}, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(rawDescriptor)}}
	grpc := artifact.Flow{
		ID: "grpc", Protocol: "grpc", Channel: "sut_to_dependency", AssetID: "grpc-only",
		Request:  artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health"}},
		Response: artifact.Message{GRPC: &artifact.GrpcMessage{Kind: "grpc", Procedure: "/grpc.health.v1.Health/Check", Service: "grpc.health.v1.Health", Method: "Check", SchemaRef: "health", HTTPStatus: 200}},
	}
	first := capture("test-one", "first test", started, []artifact.Flow{inventoryLate, service, paymentEarly, grpc, unknown, inventoryEarly, httpFlow("sut", "http://sut/orders", 200, started)})
	first.Schemas = []artifact.SchemaRef{dependencySchema("inventory", "Shared API"), grpcSchema}
	second := capture("test-two", "second test", started.Add(time.Minute), []artifact.Flow{
		dependencyFlow("inventory", "inventory", "http://shared-mock/third", 200, started.Add(time.Minute), "inventory third"),
		dependencyFlow("payment", "payments", "http://shared-mock/second", 200, started.Add(time.Minute), "payment second"),
	})
	second.Schemas = []artifact.SchemaRef{dependencySchema("payments", "Shared API")}
	root := configuredBundle(t, bundle.Identity{
		Application: &bundle.Application{Name: "Orders", ID: "orders"},
		DependencyKinds: map[string]string{
			"inventory": "mock", "payments": "mock", "live-service": "service", "unknown": "unknown", "grpc-only": "mock", "unused": "mock",
		},
	}, first, second, capture("test-empty", "empty test", started.Add(2*time.Minute), nil))
	before := bundleFiles(t, root)
	exports := generateAll(t, root, "")
	if len(exports) != 3 {
		t.Fatalf("wanted SUT and two captured HTTP mocks, got %d exports", len(exports))
	}
	wantFilenames := []string{"sut-orders.postman_collection.json", "mock-inventory.postman_collection.json", "mock-payments.postman_collection.json"}
	wantNames := []string{"Orders - orders", "Shared API - inventory", "Shared API - payments"}
	for i, export := range exports {
		if export.Filename != wantFilenames[i] {
			t.Errorf("export %d filename: got %q, want %q", i, export.Filename, wantFilenames[i])
		}
		collection := collectionJSON(t, export.Collection)
		if got := object(t, collection["info"])["name"]; got != wantNames[i] {
			t.Errorf("export %d name: got %q, want %q", i, got, wantNames[i])
		}
		folders := array(t, collection["item"])
		if len(folders) != 3 {
			t.Fatalf("export %d did not retain all test folders: %#v", i, folders)
		}
		for j, name := range []string{"first test", "second test", "empty test"} {
			if object(t, folders[j])["name"] != name {
				t.Errorf("export %d folder %d: %#v", i, j, folders[j])
			}
		}
		if len(array(t, object(t, folders[2])["item"])) != 0 {
			t.Fatalf("export %d invented traffic for an empty test", i)
		}
	}
	sut := collectionJSON(t, exports[0].Collection)
	if got := len(array(t, object(t, array(t, sut["item"])[0])["item"])); got != 1 {
		t.Fatalf("SUT collection contains dependency traffic: %d requests", got)
	}
	inventory := collectionJSON(t, exports[1].Collection)
	folders := array(t, inventory["item"])
	folder := object(t, folders[0])
	if description, _ := folder["description"].(string); strings.Contains(description, "unavailable") {
		t.Fatalf("an unrelated dependency's missing timestamps affected mock chronology: %q", description)
	}
	items := array(t, folder["item"])
	if len(items) != 2 {
		t.Fatalf("inventory collection merged another dependency: %#v", items)
	}
	for i, want := range []string{"http://shared-mock/first", "http://shared-mock/second"} {
		if got := object(t, object(t, items[i])["request"])["url"]; got != want {
			t.Errorf("mock request %d is not in request-start order: got %v, want %s", i, got, want)
		}
	}
	item := object(t, items[0])
	request := object(t, item["request"])
	if body := object(t, request["body"]); body["raw"] != inventoryEarly.Request.HTTP.Body.Text {
		t.Fatalf("mock request body changed: %#v", body)
	}
	response := object(t, array(t, item["response"])[0])
	if response["body"] != "inventory first" || response["code"] != float64(201) || response["status"] != "Created" || !reflect.DeepEqual(response["originalRequest"], request) {
		t.Fatalf("mock response or original request was lost: %#v", response)
	}
	if got := jsonHeaders(t, response["header"]); !reflect.DeepEqual(got, map[string][]string{"X-Result": {"one", "two"}}) {
		t.Fatalf("mock response repeated headers changed: %#v", got)
	}
	assertStatusTest(t, item, 201)
	assertResponseBodies(t, exports[1].Collection, [][]string{{"inventory first", "inventory second"}, {"inventory third"}, {}})
	assertResponseBodies(t, exports[2].Collection, [][]string{{"payment first"}, {"payment second"}, {}})
	if again := generateAll(t, root, ""); !reflect.DeepEqual(exports, again) {
		t.Fatal("repeated exports differ")
	}
	if !reflect.DeepEqual(before, bundleFiles(t, root)) {
		t.Fatal("export mutated bundle evidence")
	}
	override := generateAll(t, root, "Explicit SUT name")
	if override[0].Collection.Info.Name != "Explicit SUT name" {
		t.Fatal("SUT name override was not applied")
	}
	if !reflect.DeepEqual(exports[1:], override[1:]) {
		t.Fatal("SUT name override changed mock exports")
	}
	hash := sha256.Sum256([]byte("inventory first"))
	if err := os.WriteFile(filepath.Join(root, "payloads", hex.EncodeToString(hash[:])+".bin"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, ""); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("wanted verified mock payload failure, got %v", err)
	}
}

func TestGenerateMockNameFallbacks(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	for _, name := range []string{"", "inventory"} {
		t.Run("name="+name, func(t *testing.T) {
			art := capture("test", "test", started, []artifact.Flow{dependencyFlow("flow", "inventory", "http://mock/items", 200, started, "ok")})
			art.Schemas = []artifact.SchemaRef{dependencySchema("inventory", name)}
			root := configuredBundle(t, bundle.Identity{DependencyKinds: map[string]string{"inventory": "mock"}}, art)
			exports := generateAll(t, root, "")
			if len(exports) != 2 || exports[1].Collection.Info.Name != "inventory" {
				t.Fatalf("mock name should fall back to asset ID: %#v", exports)
			}
		})
	}
}

func TestGenerateFilenamesAreSafeAndUniqueAcrossIdentities(t *testing.T) {
	started := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	ids := []string{"Inventory", "inventory", "a.b", "a_b", strings.Repeat("a", 240) + "-one", strings.Repeat("a", 240) + "-two"}
	kinds := make(map[string]string, len(ids))
	flows := make([]artifact.Flow, 0, len(ids))
	for i, id := range ids {
		kinds[id] = "mock"
		flows = append(flows, dependencyFlow("flow-"+jsonNumber(i), id, "http://same-host/same-path", 200, started, id))
	}
	root := configuredBundle(t, bundle.Identity{Application: &bundle.Application{Name: "Orders", ID: "../../orders/unsafe"}, DependencyKinds: kinds}, capture("test", "test", started, flows))
	exports := generateAll(t, root, "")
	if len(exports) != len(ids)+1 {
		t.Fatalf("filename sanitization merged identities: got %d exports", len(exports))
	}
	seen := map[string]bool{}
	for _, export := range exports {
		filename := export.Filename
		folded := strings.ToLower(filename)
		if filename != filepath.Base(filename) || strings.ContainsAny(filename, `/\\`) || !strings.HasSuffix(filename, ".postman_collection.json") || len(filename) > 255 {
			t.Errorf("unsafe collection filename %q", filename)
		}
		if seen[folded] {
			t.Errorf("collection filename collides on case-insensitive filesystems: %q", filename)
		}
		seen[folded] = true
	}
	if again := generateAll(t, root, ""); !reflect.DeepEqual(exports, again) {
		t.Fatal("safe filenames are not deterministic")
	}
}

func configuredBundle(t *testing.T, identity bundle.Identity, captures ...*artifact.Artifact) string {
	t.Helper()
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(identity); err != nil {
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

func dependencySchema(id, name string) artifact.SchemaRef {
	return artifact.SchemaRef{ID: id + "-contract", AssetID: id, AssetName: name, Role: "dependency", Type: "openapi", Source: artifact.SchemaSource{Type: "omitted"}}
}

func dependencyFlow(id, assetID, url string, status int, started time.Time, response string) artifact.Flow {
	flow := httpFlow(id, url, status, started)
	flow.Channel, flow.AssetID = "sut_to_dependency", assetID
	flow.Response.HTTP.Body = &artifact.Body{Text: response}
	return flow
}

func generateAll(t *testing.T, root, name string) []Export {
	t.Helper()
	exports, err := Generate(root, name)
	if err != nil {
		t.Fatal(err)
	}
	return exports
}

func collectionJSON(t *testing.T, collection *Collection) map[string]any {
	t.Helper()
	var output bytes.Buffer
	if err := Write(&output, collection); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertResponseBodies(t *testing.T, collection *Collection, want [][]string) {
	t.Helper()
	root := collectionJSON(t, collection)
	folders := array(t, root["item"])
	if len(folders) != len(want) {
		t.Fatalf("wanted %d folders, got %d", len(want), len(folders))
	}
	for i, folder := range folders {
		items := array(t, object(t, folder)["item"])
		got := []string{}
		for _, entry := range items {
			response := object(t, array(t, object(t, entry)["response"])[0])
			got = append(got, response["body"].(string))
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("folder %d response bodies: got %#v, want %#v", i, got, want[i])
		}
	}
}
