package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/postman"
)

func TestCLIPostmanMockCollections(t *testing.T) {
	bundlePath := postmanMockBundleFixture(t)
	for _, tc := range []struct{ command, option, name string }{
		{"export", "--output-dir", "Orders - orders-app"},
		{"report", "--output-dir", "Custom SUT"},
		{"export", "--output", "Custom SUT"},
	} {
		t.Run(tc.command+tc.option, func(t *testing.T) {
			dir := t.TempDir()
			output := dir
			if tc.option == "--output" {
				output = filepath.Join(dir, "my-sut.json")
				if err := os.WriteFile(output, []byte("old collection"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{tc.command, "--bundle", bundlePath, "--format", "postman", tc.option, output}
			if tc.name == "Custom SUT" {
				args = append(args, "--sut-name", tc.name)
			}
			if code := execute(context.Background(), args); code != 0 {
				t.Fatalf("Postman export = %d", code)
			}
			collections := readPostmanCollections(t, dir)
			if len(collections) != 3 {
				t.Fatalf("wanted SUT and two mock collections: %+v", collections)
			}
			for _, name := range []string{tc.name, "Catalog - catalog", "Inventory - inventory"} {
				collection, ok := collections[name]
				if !ok || !strings.Contains(collection.Info.Schema, "v2.1.0") || len(collection.Items) != 2 || collection.Items[0].Name != "first test" || collection.Items[1].Name != "second test" {
					t.Fatalf("missing collection or test folders for %q: %+v", name, collections)
				}
			}
			inventory := collections["Inventory - inventory"]
			if len(inventory.Items[0].Items) != 2 || len(inventory.Items[1].Items) != 0 {
				t.Fatalf("mock requests crossed test or dependency boundaries: %+v", inventory.Items)
			}
			for i, status := range []int{201, 202} {
				item := inventory.Items[0].Items[i]
				if !strings.Contains(item.Request.URL, []string{"/first", "/later"}[i]) || len(item.Responses) != 1 || item.Responses[0].Code != status || item.Responses[0].Body != `{"asset":"inventory"}` {
					t.Fatalf("mock order or response lost: %+v", item)
				}
				if len(item.Events) != 1 || item.Events[0].Listen != "test" || !strings.Contains(strings.Join(item.Events[0].Script.Exec, "\n"), "pm.response.to.have.status("+strconv.Itoa(status)+")") {
					t.Fatalf("missing mock status assertion: %+v", item.Events)
				}
			}
			if item := inventory.Items[0].Items[0]; item.Request.Body == nil || item.Request.Body.Raw == nil || *item.Request.Body.Raw != `{"sku":"one"}` {
				t.Fatalf("mock request body lost: %+v", item.Request)
			}
			catalog := collections["Catalog - catalog"]
			if len(catalog.Items[0].Items) != 1 || len(catalog.Items[1].Items) != 1 || len(catalog.Items[1].Items[0].Responses) != 1 || catalog.Items[1].Items[0].Responses[0].Code != 204 {
				t.Fatalf("catalog test requests or responses lost: %+v", catalog.Items)
			}
			if tc.option == "--output" {
				if info, err := os.Stat(output); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
					t.Fatalf("requested SUT filename or existing permissions lost: %v, %v", info, err)
				}
			}
		})
	}
	t.Run("no mocks", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "new", "collections")
		if code := execute(context.Background(), []string{"export", "--bundle", postmanBundleFixture(t), "--format", "postman", "--output-dir", dir}); code != 0 {
			t.Fatalf("SUT-only directory export = %d", code)
		}
		if collections := readPostmanCollections(t, dir); len(collections) != 1 {
			t.Fatalf("unexpected collections without mock traffic: %+v", collections)
		}
	})
}

func TestCLIPostmanMultipleCollectionsNeedDestination(t *testing.T) {
	code, data := executeCapturingStdout(t, []string{"export", "--bundle", postmanMockBundleFixture(t), "--format", "postman"})
	if code != 2 || len(data) != 0 {
		t.Fatalf("multi-collection stdout export = %d, %q", code, data)
	}
}

func TestCLIPostmanOutputValidation(t *testing.T) {
	bundlePath := postmanMockBundleFixture(t)
	for _, args := range [][]string{
		{"--format", "postman", "--output", "sut.json", "--output-dir", "collections"},
		{"--format", "json", "--output-dir", "collections"},
		{"--format", "yaml", "--output-dir", "collections"},
		{"--format", "okf", "--output-dir", "collections"},
	} {
		if code := execute(context.Background(), append([]string{"export", "--bundle", bundlePath}, args...)); code != 2 {
			t.Fatalf("accepted invalid output options %q: %d", args, code)
		}
	}
	exports, err := postman.Generate(bundlePath, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("case insensitive filename collision", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), strings.ToUpper(exports[1].Filename))
		if err := os.WriteFile(output, []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
		if code := execute(context.Background(), []string{"export", "--bundle", bundlePath, "--format", "postman", "--output", output}); code != 2 {
			t.Fatalf("accepted SUT/mock filename collision: %d", code)
		}
		if data, err := os.ReadFile(output); err != nil || string(data) != "keep" {
			t.Fatalf("filename collision changed existing output: %q, %v", data, err)
		}
	})
	t.Run("directory collision preserves all outputs", func(t *testing.T) {
		dir := t.TempDir()
		for _, export := range exports[:2] {
			if err := os.WriteFile(filepath.Join(dir, export.Filename), []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(filepath.Join(dir, exports[2].Filename), 0755); err != nil {
			t.Fatal(err)
		}
		if code := execute(context.Background(), []string{"export", "--bundle", bundlePath, "--format", "postman", "--output-dir", dir}); code != 1 {
			t.Fatalf("directory collision exit = %d", code)
		}
		for _, export := range exports[:2] {
			if data, err := os.ReadFile(filepath.Join(dir, export.Filename)); err != nil || string(data) != "keep" {
				t.Fatalf("directory collision changed earlier output: %q, %v", data, err)
			}
		}
	})
}

func TestCLIPostmanCorruptBundlePreservesCollectionSet(t *testing.T) {
	bundlePath := postmanMockBundleFixture(t)
	exports, err := postman.Generate(bundlePath, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, export := range exports {
		if err := os.WriteFile(filepath.Join(dir, export.Filename), []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bundlePath, "tests", "first.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{dir, filepath.Join(t.TempDir(), "new-collections")} {
		if code := execute(context.Background(), []string{"export", "--bundle", bundlePath, "--format", "postman", "--output-dir", output}); code != 1 {
			t.Fatalf("corrupt bundle export = %d", code)
		}
		if output != dir {
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("corrupt bundle created output directory: %v", err)
			}
		}
	}
	for _, export := range exports {
		if data, err := os.ReadFile(filepath.Join(dir, export.Filename)); err != nil || string(data) != "keep" {
			t.Fatalf("corrupt bundle changed collection output: %q, %v", data, err)
		}
	}
}

func TestCLIPostmanNewFilesRespectUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX file permissions")
	}
	if os.Getenv("WIRETAP_TEST_POSTMAN_UMASK") != "child" {
		// Set umask only in an isolated process, leaving other tests unaffected.
		command := exec.Command("sh", "-c", `umask 077; exec "$@"`, "sh", os.Args[0], "-test.run=^TestCLIPostmanNewFilesRespectUmask$")
		command.Env = append(os.Environ(), "WIRETAP_TEST_POSTMAN_UMASK=child")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("export with restrictive umask: %v\n%s", err, output)
		}
		return
	}
	dir := t.TempDir()
	sutPath := filepath.Join(dir, "sut.json")
	if err := os.WriteFile(sutPath, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sutPath, 0640); err != nil {
		t.Fatal(err)
	}
	if code := execute(context.Background(), []string{"export", "--bundle", postmanMockBundleFixture(t), "--format", "postman", "--output", sutPath}); code != 0 {
		t.Fatalf("export = %d", code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		wantMode := os.FileMode(0600)
		if entry.Name() == "sut.json" {
			wantMode = 0640
		}
		if err != nil || info.Mode().Perm() != wantMode {
			t.Fatalf("%s permissions = %v, want %v (error: %v)", entry.Name(), info, wantMode, err)
		}
	}
}

func readPostmanCollections(t *testing.T, dir string) map[string]postman.Collection {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	collections := map[string]postman.Collection{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var collection postman.Collection
		if err := json.Unmarshal(data, &collection); err != nil {
			t.Fatalf("invalid collection %s: %v", entry.Name(), err)
		}
		if _, exists := collections[collection.Info.Name]; exists {
			t.Fatalf("duplicate collection: %q", collection.Info.Name)
		}
		collections[collection.Info.Name] = collection
	}
	return collections
}

func postmanMockBundleFixture(t *testing.T) string {
	t.Helper()
	writer, err := bundle.NewAt(filepath.Join(t.TempDir(), "capture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(bundle.Identity{
		Application: &bundle.Application{Name: "Orders", ID: "orders-app"}, APIID: "orders-api",
		DependencyKinds: map[string]string{"catalog": "mock", "inventory": "mock", "unused": "mock", "billing": "service", "unknown": "unknown"},
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC)
	flow := func(id, asset, path string, status int, offset time.Duration) artifact.Flow {
		channel, host := "sut_to_dependency", asset
		if asset == "" {
			channel, host = "test_to_sut", "sut"
		}
		return artifact.Flow{ID: id, AssetID: asset, Protocol: "http", Channel: channel, StartedAt: start.Add(offset),
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://" + host + path}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: status, Body: &artifact.Body{Text: `{"asset":"` + asset + `"}`}}},
		}
	}
	first := flow("z-earlier", "inventory", "/first", 201, time.Second)
	first.Request.HTTP.Method = "POST"
	first.Request.HTTP.Body = &artifact.Body{Text: `{"sku":"one"}`}
	schemas := []artifact.SchemaRef{
		{ID: "inventory-api", Role: "dependency", AssetID: "inventory", AssetName: "Inventory", Type: "openapi", Source: artifact.SchemaSource{Type: "omitted"}},
		{ID: "catalog-api", Role: "dependency", AssetID: "catalog", AssetName: "Catalog", Type: "openapi", Source: artifact.SchemaSource{Type: "omitted"}},
	}
	for _, capture := range []*artifact.Artifact{
		{Version: 1, TestCase: artifact.TestCase{ID: "first", Name: "first test", StartedAt: start, Status: "pass"}, Traffic: []artifact.Flow{
			flow("sut-first", "", "/first", 200, 0), flow("a-later", "inventory", "/later", 202, 2*time.Second), first,
			flow("catalog-first", "catalog", "/first", 200, 3*time.Second), flow("service", "billing", "/private", 200, 4*time.Second),
			flow("unknown", "unknown", "/private", 200, 5*time.Second),
		}},
		{Version: 1, TestCase: artifact.TestCase{ID: "second", Name: "second test", StartedAt: start.Add(time.Minute), Status: "pass"}, Traffic: []artifact.Flow{
			flow("sut-second", "", "/second", 404, time.Minute), flow("catalog-second", "catalog", "/second", 204, time.Minute+time.Second),
		}},
	} {
		frozen, err := writer.Register(capture.TestCase, schemas)
		if err != nil {
			t.Fatal(err)
		}
		capture.Schemas = frozen
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}
