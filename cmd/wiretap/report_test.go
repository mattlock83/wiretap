package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/postman"
)

func TestCLIPostmanExport(t *testing.T) {
	bundlePath := postmanBundleFixture(t)
	for _, command := range []string{"export", "report"} {
		t.Run(command, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "collection.postman_collection.json")
			if code := execute(context.Background(), []string{command, "--bundle", bundlePath, "--format", "postman", "--sut-name", "Inventory API", "--output", output}); code != 0 {
				t.Fatalf("%s Postman export exit = %d", command, code)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			assertPostmanExport(t, data)
		})
	}

	t.Run("stdout", func(t *testing.T) {
		code, data := executeCapturingStdout(t, []string{"export", "--bundle", bundlePath, "--format", "postman", "--sut-name", "Inventory API"})
		if code != 0 {
			t.Fatalf("stdout Postman export exit = %d", code)
		}
		assertPostmanExport(t, data)
	})
}

func TestCLIPostmanExportPreservesOutputOnCorruptBundle(t *testing.T) {
	bundlePath := postmanBundleFixture(t)
	// Changing immutable evidence must fail bundle verification before the output
	// is opened, even when the replacement evidence would decode successfully.
	if err := os.WriteFile(filepath.Join(bundlePath, "tests", "read-items.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "existing.json")
	if err := os.WriteFile(output, []byte("keep this export\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if code := execute(context.Background(), []string{"export", "--bundle", bundlePath, "--format", "postman", "--output", output}); code != 1 {
		t.Fatalf("corrupt bundle export exit = %d, want 1", code)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "keep this export\n" {
		t.Fatalf("failed export changed existing output: %q, %v", data, err)
	}
}

func postmanBundleFixture(t *testing.T) string {
	t.Helper()
	writer, err := bundle.NewAt(filepath.Join(t.TempDir(), "capture"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC)
	flow := func(id, method, url string, status int, offset time.Duration) artifact.Flow {
		return artifact.Flow{
			ID: id, Protocol: "http", Channel: "test_to_sut", StartedAt: start.Add(offset),
			Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: method, URL: url}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{
				Status: status, Headers: artifact.Headers{"Content-Type": {"application/json"}},
				Body: &artifact.Body{Text: `{"ok":true}`},
			}},
		}
	}
	dependency := flow("dependency", "GET", "http://dependency/private", 200, 2*time.Second)
	dependency.Channel = "sut_to_dependency"
	dependency.AssetID = "inventory"
	for _, capture := range []*artifact.Artifact{
		{Version: 1, TestCase: artifact.TestCase{ID: "read-items", Name: "reads items", StartedAt: start, Status: "pass"}, Traffic: []artifact.Flow{
			flow("a-later", "POST", "http://sut/items", 201, time.Second),
			dependency,
			flow("z-earlier", "GET", "http://sut/items?limit=1", 200, 0),
		}},
		{Version: 1, TestCase: artifact.TestCase{ID: "missing-item", Name: "missing item", StartedAt: start.Add(time.Minute), Status: "pass"}, Traffic: []artifact.Flow{
			flow("missing", "GET", "http://sut/items/missing", 404, time.Minute),
		}},
	} {
		if _, err := writer.Register(capture.TestCase, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}

func assertPostmanExport(t *testing.T, data []byte) {
	t.Helper()
	var collection postman.Collection
	if err := json.Unmarshal(data, &collection); err != nil {
		t.Fatalf("invalid Postman JSON: %v\n%s", err, data)
	}
	if collection.Info.Name != "Inventory API" || !strings.Contains(collection.Info.Schema, "v2.1.0") {
		t.Fatalf("unexpected collection metadata: %+v", collection.Info)
	}
	if len(collection.Items) != 2 || collection.Items[0].Name != "reads items" || collection.Items[1].Name != "missing item" {
		t.Fatalf("expected a folder per test: %+v", collection.Items)
	}
	requests := collection.Items[0].Items
	if len(requests) != 2 || requests[0].Request.Method != "GET" || requests[1].Request.Method != "POST" {
		t.Fatalf("expected only inbound requests in start-time order: %+v", requests)
	}
	if !strings.Contains(requests[0].Request.URL, "items") || len(collection.Items[1].Items) != 1 {
		t.Fatalf("missing captured request: %+v", collection.Items)
	}
	for index, expected := range []int{200, 201} {
		item := requests[index]
		if len(item.Responses) != 1 || item.Responses[0].Code != expected || item.Responses[0].Body != `{"ok":true}` {
			t.Fatalf("missing response example for request %d: %+v", index, item.Responses)
		}
		if len(item.Events) != 1 || item.Events[0].Listen != "test" || !strings.Contains(strings.Join(item.Events[0].Script.Exec, "\n"), "pm.response.to.have.status(") {
			t.Fatalf("missing basic status check for request %d: %+v", index, item.Events)
		}
	}
	if item := collection.Items[1].Items[0]; len(item.Responses) != 1 || item.Responses[0].Code != 404 {
		t.Fatalf("missing second test response: %+v", item.Responses)
	}
}

func executeCapturingStdout(t *testing.T, args []string) (int, []byte) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	original := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = original }()
	code := execute(context.Background(), args)
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, data
}
