package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/assumptions"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/postman"
)

// Produce actual complete, portable archives with their own Postman exports.
func cliStoreArchive(t *testing.T, root, application string, mappings map[string]bundle.ProviderRef, response string) string {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	var dependencies []string
	for id := range mappings {
		kinds[id] = "mock"
		dependencies = append(dependencies, id)
	}
	sort.Strings(dependencies)
	if err := writer.Configure(bundle.Identity{
		Application: &bundle.Application{ID: application}, APIID: application + "-http", SUTTargetKind: "service",
		DependencyKinds: kinds, ProviderMappings: mappings,
	}); err != nil {
		t.Fatal(err)
	}
	if len(dependencies) == 0 {
		dependencies = []string{""}
	}
	capture := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "example", Name: "looks up inventory", Status: "pass", StartedAt: time.Now().UTC()}}
	for _, dependency := range dependencies {
		channel := "sut_to_dependency"
		if dependency == "" {
			channel = "test_to_sut"
		}
		capture.Traffic = append(capture.Traffic, artifact.Flow{
			ID: "request-" + dependency, Protocol: "http", Channel: channel, AssetID: dependency,
			Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://fixture.test/items"}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200,
				Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: response}}},
		})
	}
	if _, err := writer.Register(capture.TestCase, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	exports, err := postman.GenerateAll(writer.Path(), "")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(writer.Path(), "exports", "postman")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, export := range exports {
		file, err := os.Create(filepath.Join(directory, export.Filename))
		if err != nil {
			t.Fatal(err)
		}
		writeErr := postman.WriteExport(file, export)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("write collection: %v / %v", writeErr, closeErr)
		}
	}
	destination := filepath.Join(root, application, "tested-build", filepath.Base(writer.Path()))
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(writer.Path(), destination); err != nil {
		t.Fatal(err)
	}
	return destination
}

func TestCLISharedArtifactsChecksAllMappedBoundariesAndConsumers(t *testing.T) {
	root := t.TempDir()
	inventory := cliStoreArchive(t, root, "inventory", nil, `{"stock":3}`)
	cliStoreArchive(t, root, "pricing", nil, `{"stock":3}`)
	stock := bundle.ProviderRef{ApplicationID: "inventory", APIID: "inventory-http"}
	orders := cliStoreArchive(t, root, "orders", map[string]bundle.ProviderRef{
		"stock": stock, "prices": {ApplicationID: "pricing", APIID: "pricing-http"},
	}, `{"stock":3}`)
	cliStoreArchive(t, root, "billing", map[string]bundle.ProviderRef{"inventory-mock": stock}, `{"stock":"3"}`)
	for _, scenario := range []struct {
		mode, anchor               string
		exit, verified, mismatched int
	}{
		{"consumer", orders, 0, 2, 0},
		{"provider", inventory, 1, 1, 1},
	} {
		t.Run(scenario.mode, func(t *testing.T) {
			args := []string{"check", scenario.mode, "--" + scenario.mode, filepath.Join(scenario.anchor, "bundle.json"), "--artifacts", root}
			code, data := executeCapturingStdout(t, append(args, "--format", "json"))
			var report assumptions.StoreReport
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatalf("invalid report: %v\n%s", err, data)
			}
			if code != scenario.exit || report.Compatible != (scenario.exit == 0) || len(report.Comparisons) != 2 || report.Counts.Assumptions != 2 || report.Counts.Verified != scenario.verified || report.Counts.Mismatched != scenario.mismatched || report.Scope != "discovered_consumer_provider_relationships" || report.RulesVersion != assumptions.RulesVersion {
				t.Fatalf("exit %d: %+v", code, report)
			}
			for _, comparison := range report.Comparisons {
				if comparison.Binding != "declared_mapping" || comparison.Consumer.ManifestSHA256 == "" || comparison.Provider.ManifestSHA256 == "" || comparison.Mode != scenario.mode {
					t.Fatalf("missing pair provenance: %+v", comparison)
				}
			}
			code, data = executeCapturingStdout(t, args)
			if code != scenario.exit || !strings.Contains(string(data), "2 comparisons") || !strings.Contains(string(data), "Mock dependency:") || scenario.exit == 1 && !strings.Contains(string(data), "provider_response_mismatch") {
				t.Fatalf("unhelpful text report (exit %d): %s", code, data)
			}
		})
	}
	// Selecting one dependency retains an aggregate envelope with one pair.
	code, data := executeCapturingStdout(t, []string{"check", "consumer", "--consumer", orders, "--artifacts", root, "--dependency", "stock", "--format", "json"})
	var scoped assumptions.StoreReport
	if code != 0 || json.Unmarshal(data, &scoped) != nil || len(scoped.Comparisons) != 1 || scoped.Comparisons[0].Dependency != "stock" {
		t.Fatalf("scoped check exit %d: %s", code, data)
	}
}

func TestCLISharedArtifactsPreservesInputsAndReportsOnInvalidSelection(t *testing.T) {
	root := t.TempDir()
	provider := cliStoreArchive(t, root, "inventory", nil, `{"ok":true}`)
	consumer := cliStoreArchive(t, t.TempDir(), "orders", map[string]bundle.ProviderRef{"stock": {ApplicationID: "inventory", APIID: "inventory-http"}}, `{"ok":true}`)
	args := []string{"check", "consumer", "--consumer", consumer, "--artifacts", root}
	for _, output := range []string{filepath.Join(root, "report.json"), filepath.Join(provider, "bundle.json"), filepath.Join(consumer, "bundle.json")} {
		before, _ := os.ReadFile(output)
		code := execute(context.Background(), append(args, "--output", output))
		after, _ := os.ReadFile(output)
		if code != 2 || string(before) != string(after) {
			t.Fatalf("input overwritten at %s, exit %d", output, code)
		}
	}
	output := filepath.Join(t.TempDir(), "report.json")
	if code := execute(context.Background(), append(args, "--format", "json", "--output", output)); code != 0 {
		t.Fatalf("external report exit %d", code)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	// A second complete provider run is ambiguous, even if both would pass.
	cliStoreArchive(t, root, "inventory", nil, `{"ok":true}`)
	if code := execute(context.Background(), append(args, "--format", "json", "--output", output)); code != 2 {
		t.Fatalf("ambiguous selection exit %d", code)
	}
	after, err := os.ReadFile(output)
	if err != nil || string(before) != string(after) {
		t.Fatal("invalid selection replaced a previous report")
	}
}

func TestCLISharedArtifactsRejectsMixedInputModes(t *testing.T) {
	for _, args := range [][]string{
		{"consumer", "--consumer", "c", "--provider", "p", "--artifacts", "root"},
		{"provider", "--consumer", "c", "--provider", "p", "--artifacts", "root"},
		{"consumer", "--artifacts", "root"},
		{"provider", "--artifacts", "root"},
		{"consumer", "--consumer", "c", "--artifacts", "root", "--consumer-collection", "collection.json"},
		{"provider", "--provider", "p", "--artifacts", "root", "--provider-collection", "collection.json"},
		{"provider", "--provider", "p", "--artifacts", "root", "--dependency", "stock"},
	} {
		if code := execute(context.Background(), append([]string{"check"}, args...)); code != 2 {
			t.Fatalf("invalid flags %v: exit %d", args, code)
		}
	}
}

func TestCLISharedArtifactsRequiresEachSelectedRunsOwnPostmanExports(t *testing.T) {
	root := t.TempDir()
	provider := cliStoreArchive(t, root, "inventory", nil, `{"ok":true}`)
	consumer := cliStoreArchive(t, t.TempDir(), "orders", map[string]bundle.ProviderRef{"stock": {ApplicationID: "inventory", APIID: "inventory-http"}}, `{"ok":true}`)
	// A matching collection elsewhere in the store must not mask missing exports.
	if err := os.Rename(filepath.Join(provider, "exports", "postman"), filepath.Join(root, "old-collections")); err != nil {
		t.Fatal(err)
	}
	code, data := executeCapturingStdout(t, []string{"check", "consumer", "--consumer", consumer, "--artifacts", root, "--format", "json"})
	if code != 2 || len(data) != 0 {
		t.Fatalf("missing per-run exports accepted, exit %d: %s", code, data)
	}
}
