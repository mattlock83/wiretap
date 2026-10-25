package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/assumptions"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/postman"
)

func cliCheckArchive(t *testing.T, consumer bool, path string, status int, response string, states ...string) string {
	t.Helper()
	return cliCheckArchiveWithContracts(t, consumer, path, status, response, nil, states...)
}

func cliCheckArchiveWithContracts(t *testing.T, consumer bool, path string, status int, response string, schemas []artifact.SchemaRef, states ...string) string {
	t.Helper()
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity := bundle.Identity{Application: &bundle.Application{ID: "inventory-service"}, APIID: "inventory", SUTTargetKind: "service"}
	channel, asset := "test_to_sut", ""
	if consumer {
		identity = bundle.Identity{Application: &bundle.Application{ID: "orders"}, APIID: "orders", SUTTargetKind: "service", DependencyKinds: map[string]string{"stock": "mock"}, ProviderMappings: map[string]bundle.ProviderRef{"stock": {ApplicationID: "inventory-service", APIID: "inventory"}}}
		channel, asset = "sut_to_dependency", "stock"
	}
	if err := writer.Configure(identity); err != nil {
		t.Fatal(err)
	}
	capture := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "attempt", Name: "checks inventory", Status: "pass", StartedAt: time.Now().UTC()}, Traffic: []artifact.Flow{{
		ID: "call", Protocol: "http", Channel: channel, AssetID: asset,
		Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://local.test" + path, Headers: artifact.Headers{"Accept": {"application/json"}}}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: status, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: response}}},
	}}}
	capture.Schemas, err = writer.Register(capture.TestCase, schemas)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	state := "complete"
	if len(states) > 0 {
		state = states[0]
	}
	if err := writer.Finalize(state); err != nil {
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
	return writer.Path()
}

func TestCLIAssumptionCheckRejectsDeclaredRuleDifferences(t *testing.T) {
	for _, scenario := range []struct {
		name, consumer, provider, value, keyword string
	}{
		{"type", `{"type":"integer"}`, `{"type":"number"}`, `7`, "type"},
		{"enum", `{"type":"string","enum":["ready","done"]}`, `{"type":"string","enum":["ready"]}`, `"ready"`, "enum"},
		{"length", `{"type":"string","maxLength":10}`, `{"type":"string","maxLength":20}`, `"ready"`, "maxLength"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			archive := func(consumer bool, schema string) string {
				role, asset := "sut", ""
				if consumer {
					role, asset = "dependency", "stock"
				}
				spec := fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"Inventory","version":"1"},"paths":{"/items":{"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"object","properties":{"value":%s}}}}}}}}}}`, schema)
				return cliCheckArchiveWithContracts(t, consumer, "/items", 200, `{"value":`+scenario.value+`}`, []artifact.SchemaRef{{ID: "inventory", Role: role, AssetID: asset, Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: spec}}})
			}
			consumer, provider := archive(true, scenario.consumer), archive(false, scenario.provider)
			for _, mode := range []string{"consumer", "provider"} {
				args := []string{"check", mode, "--consumer", consumer, "--provider", provider}
				code, output := executeCapturingStdout(t, append(args, "--format", "json"))
				var report assumptions.Report
				if err := json.Unmarshal(output, &report); err != nil {
					t.Fatalf("invalid report: %v\n%s", err, output)
				}
				if code != 1 || report.Compatible || len(report.Findings) != 1 || report.Findings[0].Code != "http_contract_mismatch" || !strings.Contains(strings.Join(report.Findings[0].Differences, " "), scenario.keyword) {
					t.Fatalf("expected %s error in %s mode, exit %d: %s", scenario.keyword, mode, code, output)
				}
				code, output = executeCapturingStdout(t, args)
				if code != 1 || !strings.Contains(string(output), "validation rules differ") || !strings.Contains(string(output), scenario.keyword) {
					t.Fatalf("missing useful text diagnostic, exit %d: %s", code, output)
				}
			}
		})
	}
}

func TestCLIAssumptionCheckUsesProviderSpecWithoutConsumerCopy(t *testing.T) {
	spec := `openapi: 3.0.3
info: {title: Inventory, version: '1'}
paths:
  /items:
    get:
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [state, count, name, tags]
                properties:
                  state: {type: string, enum: [ready, pending]}
                  count: {type: integer, minimum: 1, maximum: 5}
                  name: {type: string, minLength: 2, maxLength: 5}
                  tags: {type: array, minItems: 1, maxItems: 2, items: {type: string}}
`
	for _, scenario := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"state":"ready","count":3,"name":"item","tags":["a"]}`, true},
		{"wrong type", `{"state":"ready","count":"3","name":"item","tags":["a"]}`, false},
		{"enum", `{"state":"unknown","count":3,"name":"item","tags":["a"]}`, false},
		{"string length", `{"state":"ready","count":3,"name":"toolong","tags":["a"]}`, false},
		{"array length", `{"state":"ready","count":3,"name":"item","tags":[]}`, false},
		{"numeric bound", `{"state":"ready","count":6,"name":"item","tags":["a"]}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			consumer := cliCheckArchive(t, true, "/items", 200, scenario.body)
			provider := cliCheckArchiveWithContracts(t, false, "/items", 200, scenario.body, []artifact.SchemaRef{{ID: "inventory", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: spec}}})
			for _, mode := range []string{"consumer", "provider"} {
				args := []string{"check", mode, "--consumer", consumer, "--provider", provider}
				code, output := executeCapturingStdout(t, append(args, "--format", "json"))
				var report assumptions.Report
				if err := json.Unmarshal(output, &report); err != nil {
					t.Fatalf("invalid report: %v\n%s", err, output)
				}
				if scenario.valid {
					if code != 0 || !report.Compatible || len(report.Matches) != 1 || report.Matches[0].ContractCheck != "openapi_3_0_provider_contract" || report.RulesVersion != assumptions.RulesVersion {
						t.Fatalf("provider-owned spec was not used, exit %d: %s", code, output)
					}
					code, output = executeCapturingStdout(t, args)
					if code != 0 || !strings.Contains(string(output), "1 HTTP provider-spec checks") {
						t.Fatalf("text report hides provider-spec assurance, exit %d: %s", code, output)
					}
				} else if code != 1 || report.Compatible || len(report.Findings) != 1 || report.Findings[0].Code != "http_contract_violation" {
					t.Fatalf("provider contract violation passed in %s mode, exit %d: %s", mode, code, output)
				}
			}
		})
	}
}

func TestCLIAssumptionCheckBothViewpoints(t *testing.T) {
	consumer := cliCheckArchive(t, true, "/items?limit=1", 200, `{"stock":3}`)
	provider := cliCheckArchive(t, false, "/items?limit=1", 200, `{"stock":3.0}`)
	for _, mode := range []string{"consumer", "provider"} {
		t.Run(mode, func(t *testing.T) {
			code, data := executeCapturingStdout(t, []string{"check", mode, "--consumer", filepath.Join(consumer, "bundle.json"), "--provider", provider, "--format", "json"})
			var report assumptions.Report
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatalf("invalid report: %v\n%s", err, data)
			}
			if code != 0 || !report.Compatible || report.Mode != mode || report.Counts.Verified != 1 || report.Binding != "declared_mapping" {
				t.Fatalf("check returned %d: %+v", code, report)
			}
		})
	}
}

func TestCLIAssumptionCheckReportsFailureAndWritesJSON(t *testing.T) {
	consumer := cliCheckArchive(t, true, "/items?limit=1", 200, `{"stock":3}`)
	for _, scenario := range []struct {
		name, path     string
		status         int
		response, kind string
	}{
		{"response mismatch", "/items?limit=1", 200, `{"stock":"three"}`, "mismatch"},
		{"missing coverage", "/items?limit=2", 200, `{"stock":3}`, "uncovered"},
		{"status mismatch", "/items?limit=1", 503, `{"stock":3}`, "mismatch"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider := cliCheckArchive(t, false, scenario.path, scenario.status, scenario.response)
			args := []string{"check", "consumer", "--consumer", consumer, "--provider", provider}
			code, text := executeCapturingStdout(t, args)
			if code != 1 || !strings.Contains(string(text), "["+scenario.kind+"]") || !strings.Contains(string(text), "tests/attempt.json#/capture/traffic/0") {
				t.Fatalf("check exit %d: %s", code, text)
			}
			output := filepath.Join(t.TempDir(), "check.json")
			if err := os.WriteFile(output, []byte("old report"), 0600); err != nil {
				t.Fatal(err)
			}
			code = execute(context.Background(), append(args, "--format", "json", "--output", output))
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var report assumptions.Report
			if code != 1 || json.Unmarshal(data, &report) != nil || report.Compatible || len(report.Findings) != 1 || report.Findings[0].Kind != scenario.kind {
				t.Fatalf("file report exit %d: %s", code, data)
			}
		})
	}
}

func TestCLIAssumptionCheckProtectsInputsAndExistingReports(t *testing.T) {
	consumer := cliCheckArchive(t, true, "/items", 200, `{"ok":true}`)
	provider := cliCheckArchive(t, false, "/items", 200, `{"ok":true}`)
	args := []string{"check", "consumer", "--consumer", consumer, "--provider", provider}
	manifest := filepath.Join(consumer, "bundle.json")
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if code := execute(context.Background(), append(args, "--output", manifest)); code != 2 {
		t.Fatalf("input overwrite exit %d", code)
	}
	after, err := os.ReadFile(manifest)
	if err != nil || string(after) != string(before) {
		t.Fatal("check changed its source archive")
	}
	output := filepath.Join(t.TempDir(), "existing.json")
	if err := os.WriteFile(output, []byte("keep existing"), 0600); err != nil {
		t.Fatal(err)
	}
	badCollection := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badCollection, []byte(`{"info":{},"item":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := execute(context.Background(), append(args, "--consumer-collection", badCollection, "--output", output)); code != 2 {
		t.Fatalf("invalid collection exit %d", code)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "keep existing" {
		t.Fatal("invalid input changed existing report")
	}
}

func TestCLIAssumptionCheckOutputRespectsUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX file permissions")
	}
	if os.Getenv("WIRETAP_TEST_CHECK_UMASK") != "child" {
		command := exec.Command("sh", "-c", `umask 077; exec "$@"`, "sh", os.Args[0], "-test.run=^TestCLIAssumptionCheckOutputRespectsUmask$")
		command.Env = append(os.Environ(), "WIRETAP_TEST_CHECK_UMASK=child")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("check with restrictive umask: %v\n%s", err, output)
		}
		return
	}
	consumer := cliCheckArchive(t, true, "/items", 200, `{"ok":true}`)
	provider := cliCheckArchive(t, false, "/items", 200, `{"ok":true}`)
	for _, format := range []string{"json", "text"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", format, existing), func(t *testing.T) {
				output := filepath.Join(t.TempDir(), "report")
				mode := os.FileMode(0600)
				if existing {
					if err := os.WriteFile(output, []byte("old report"), 0600); err != nil {
						t.Fatal(err)
					}
					mode = 0640
					if err := os.Chmod(output, mode); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"check", "consumer", "--consumer", consumer, "--provider", provider, "--format", format, "--output", output}
				if code := execute(context.Background(), args); code != 0 {
					t.Fatalf("check exit %d", code)
				}
				info, err := os.Stat(output)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("wrong check report permissions: %v, %v; want %v", info, err, mode)
				}
				data, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				if format == "json" {
					var report assumptions.Report
					if err := json.Unmarshal(data, &report); err != nil || !report.Compatible {
						t.Fatalf("invalid check report: %s, %v", data, err)
					}
				} else if !strings.HasPrefix(string(data), "PASS: check consumer") {
					t.Fatalf("invalid check report: %s", data)
				}
			})
		}
	}
}

func TestCLIAssumptionCheckUsage(t *testing.T) {
	for _, args := range [][]string{
		{"check", "--help"}, {"check", "consumer", "--help"}, {"check", "provider", "--help"},
	} {
		if code := execute(context.Background(), args); code != 0 {
			t.Fatalf("help %v: exit %d", args, code)
		}
	}
	for _, args := range [][]string{
		{"check"}, {"check", "other"}, {"check", "consumer"}, {"check", "provider", "--consumer", "x"},
		{"check", "consumer", "--consumer", "x", "--provider", "y", "--format", "yaml"},
		{"check", "consumer", "--consumer", "x", "--provider", "y", "extra"},
	} {
		if code := execute(context.Background(), args); code != 2 {
			t.Fatalf("usage %v: exit %d", args, code)
		}
	}
}

func TestCLIAssumptionCheckProtectsCaseAliases(t *testing.T) {
	consumer := cliCheckArchive(t, true, "/items", 200, `{"ok":true}`)
	provider := cliCheckArchive(t, false, "/items", 200, `{"ok":true}`)
	alias := filepath.Join(filepath.Dir(consumer), strings.ToUpper(filepath.Base(consumer)))
	if !sameCheckFile(alias, consumer) {
		t.Skip("filesystem distinguishes directory casing")
	}
	manifest := filepath.Join(consumer, "bundle.json")
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	code := execute(context.Background(), []string{"check", "consumer", "--consumer", consumer, "--provider", provider, "--output", filepath.Join(alias, "bundle.json")})
	after, err := os.ReadFile(manifest)
	if code != 2 || err != nil || string(before) != string(after) {
		t.Fatalf("case alias bypassed input protection: exit %d error %v", code, err)
	}
}

func TestCLIAssumptionCheckSavesUnverifiedReportWithoutExports(t *testing.T) {
	consumer := cliCheckArchive(t, true, "/items", 200, `{"ok":true}`)
	provider := cliCheckArchive(t, false, "/items", 200, `{"ok":true}`, "interrupted")
	if err := os.RemoveAll(filepath.Join(provider, "exports")); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "unverified.json")
	code := execute(context.Background(), []string{"check", "provider", "--consumer", consumer, "--provider", provider, "--format", "json", "--output", output})
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report assumptions.Report
	if code != 1 || json.Unmarshal(data, &report) != nil || report.Compatible || len(report.Findings) == 0 || report.Findings[0].Code != "provider_run_incomplete" {
		t.Fatalf("missing unverified result: exit %d, %s", code, data)
	}
}
