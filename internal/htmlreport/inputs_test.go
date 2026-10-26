package htmlreport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/version"
)

func inputFile(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func inputCoverage(t *testing.T) string {
	t.Helper()
	report := coverage.Report{
		ReportVersion: coverage.ReportVersion, Producer: version.Producer{Name: "wiretap", Version: "test"},
		Capture:     &coverage.CaptureSource{FormatVersion: bundle.EvidenceVersion, RunID: "test-run", Revision: 1, State: "complete"},
		GeneratedAt: time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC), SUTName: "Items",
		Contracts: &coverage.ContractCoverage{RulesVersion: coverage.ContractRulesVersion, RunID: "test-run", RunState: "complete", Complete: true},
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const inputArazzo = `{"arazzo":"1.0.1","info":{"title":"Items","version":"1"},"sourceDescriptions":[{"name":"items","url":"missing-openapi.yaml","type":"openapi"}],"workflows":[{"workflowId":"read","steps":[{"stepId":"one","operationPath":"{$sourceDescriptions.items.url}#/paths/~1items/get","successCriteria":[{"condition":"$statusCode == 200"}]}]}]}`

func TestLoadUsesOnlyExplicitLocalInputs(t *testing.T) {
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer remote.Close()
	coveragePath := inputFile(t, "coverage.json", inputCoverage(t))
	jsonSpec := `{"openapi":"3.0.3","info":{"title":"Items","version":"1"},"paths":{},"components":{"schemas":{"Local":{"$ref":"file:///does/not/exist/secret.json"},"Remote":{"$ref":"` + remote.URL + `/schema.json"}}}}`
	yamlSpec := "components:\n  schemas:\n    Item:\n      type: object\n      properties:\n        id: {type: integer}\n"
	arazzoPath := inputFile(t, "arazzo.json", strings.Replace(inputArazzo, "missing-openapi.yaml", remote.URL+"/openapi.json", 1))
	specPath := inputFile(t, "openapi.json", jsonSpec)
	inputs, err := Load(Files{Coverage: coveragePath, Specs: []string{specPath, inputFile(t, "components.yaml", yamlSpec), specPath}, Arazzo: arazzoPath})
	if err != nil {
		t.Fatal(err)
	}
	if inputs.Report.SUTName != "Items" || inputs.WiretapName != "coverage.json" || len(inputs.Specs) != 2 || inputs.Specs[0].Content != jsonSpec || inputs.Specs[1].Content != yamlSpec {
		t.Fatalf("lost explicit report inputs: %#v", inputs)
	}
	if inputs.ArazzoName != "arazzo.json" || inputs.Arazzo.Workflows[0].ID != "read" || requests.Load() != 0 {
		t.Fatalf("loaded document references or lost workflows: %#v; requests %d", inputs, requests.Load())
	}
}

func TestLoadPreservesExactStandaloneJSONSources(t *testing.T) {
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILES", "2")
	wiretapJSON := "{\n  \"x-report-note\": {\"custom\": \"kept exactly\"},\n" + inputCoverage(t)[1:] + "\n"
	arazzoJSON := "{\n  \"x-custom-extension\": {\"formatting\": \"kept exactly\"},\n" + inputArazzo[1:] + "\n"
	inputs, err := Load(Files{Coverage: inputFile(t, "coverage.json", wiretapJSON), Arazzo: inputFile(t, "arazzo.json", arazzoJSON)})
	if err != nil {
		t.Fatal(err)
	}
	if inputs.WiretapJSON != wiretapJSON || inputs.ArazzoJSON != arazzoJSON {
		t.Fatal("standalone JSON source text was reformatted or unmodeled fields were lost")
	}
	if inputs.Report.SUTName != "Items" || inputs.Arazzo.Workflows[0].ID != "read" {
		t.Fatal("raw source retention replaced typed report data")
	}
}

func TestLoadRejectsWrongOrAmbiguousWiretapJSON(t *testing.T) {
	valid := inputCoverage(t)
	for name, data := range map[string]string{
		"raw recording":        `{"version":2,"testcase":{"id":"one"},"traffic":[]}`,
		"archive":              `{"format_version":"3.1","run_id":"test-run","tests":[]}`,
		"future version":       strings.Replace(valid, `"report_version":"1.0"`, `"report_version":"2.0"`, 1),
		"missing metadata":     `{"report_version":"1.0","producer":{},"capture":{},"provided":{"specs":null},"dependent":{"specs":null},"contract_coverage":{},"generated_at":"2026-10-26T01:00:00Z","sut_name":"Items","artifact_count":0}`,
		"duplicate nested key": strings.Replace(valid, `"producer":{`, `"producer":{"name":"overwritten",`, 1),
		"wrong group":          strings.Replace(valid, `"provided":{"specs":null}`, `"provided":{"specs":{}}`, 1),
		"trailing JSON":        valid + `{}`,
		"not object":           `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(Files{Coverage: inputFile(t, "coverage.json", data)}); err == nil {
				t.Fatal("accepted invalid Wiretap JSON")
			}
		})
	}
}

func TestLoadRejectsMalformedSpecsAndArazzo(t *testing.T) {
	coveragePath := inputFile(t, "coverage.json", inputCoverage(t))
	for name, data := range map[string]string{
		"json duplicate": `{"openapi":"3.0.3","info":{},"paths":{},"paths":{}}`,
		"yaml duplicate": "openapi: 3.0.3\ninfo: {}\npaths: {}\npaths: {}\n",
		"yaml documents": "openapi: 3.0.3\ninfo: {}\npaths: {}\n---\ncomponents: {}\n",
		"yaml alias":     "openapi: 3.0.3\ninfo: &info {title: Items, version: '1'}\npaths: {}\nx-copy: *info\n",
		"wrong version":  `{"openapi":"4.0.0","info":{},"paths":{}}`,
		"wrong format":   inputArazzo,
		"plain scalar":   `hello`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(Files{Coverage: coveragePath, Specs: []string{inputFile(t, "spec.yaml", data)}}); err == nil {
				t.Fatal("accepted invalid spec input")
			}
		})
	}
	for name, data := range map[string]string{
		"wrong version":     strings.Replace(inputArazzo, `"1.0.1"`, `"2.0.0"`, 1),
		"wrong format":      inputCoverage(t),
		"duplicate key":     strings.Replace(inputArazzo, `"stepId":"one"`, `"stepId":"one","stepId":"two"`, 1),
		"missing operation": strings.Replace(inputArazzo, `"operationPath":"{$sourceDescriptions.items.url}#/paths/~1items/get",`, "", 1),
		"empty workflows":   `{"arazzo":"1.0.1","info":{"title":"Items","version":"1"},"sourceDescriptions":[{"name":"items","url":"missing.yaml","type":"openapi"}],"workflows":[]}`,
	} {
		t.Run("arazzo "+name, func(t *testing.T) {
			if _, err := Load(Files{Coverage: coveragePath, Arazzo: inputFile(t, "arazzo.json", data)}); err == nil {
				t.Fatal("accepted invalid Arazzo input")
			}
		})
	}
}

func TestLoadBoundsLocalFileReads(t *testing.T) {
	path := inputFile(t, "coverage.json", inputCoverage(t))
	t.Run("bytes", func(t *testing.T) {
		t.Setenv("WIRETAP_MAX_ARCHIVE_FILE_BYTES", "16")
		if _, err := Load(Files{Coverage: path}); !errors.Is(err, bundle.ErrLoadLimit) {
			t.Fatalf("expected file size limit, got %v", err)
		}
	})
	t.Run("read count", func(t *testing.T) {
		t.Setenv("WIRETAP_MAX_ARCHIVE_FILES", "1")
		if _, err := Load(Files{Coverage: path, Arazzo: inputFile(t, "arazzo.json", inputArazzo)}); !errors.Is(err, bundle.ErrLoadLimit) {
			t.Fatalf("expected file read limit, got %v", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		if _, err := Load(Files{Coverage: t.TempDir()}); err == nil {
			t.Fatal("accepted directory input")
		}
	})
}

func htmlArchive(t *testing.T, incomplete, jsonResponse bool, refs []artifact.SchemaRef) string {
	t.Helper()
	writer, err := bundle.NewAt(filepath.Join(t.TempDir(), "capture"))
	if err != nil {
		t.Fatal(err)
	}
	testcase := artifact.TestCase{ID: "read-items", Name: "reads items", Status: "pass", StartedAt: time.Now().UTC()}
	frozen, err := writer.Register(testcase, refs)
	if err != nil {
		t.Fatal(err)
	}
	media, body := "application/json", `{"id":1}`
	if !jsonResponse {
		media, body = "text/plain", "ready"
	}
	capture := &artifact.Artifact{Version: 2, TestCase: testcase, Schemas: frozen, CaptureIncomplete: incomplete,
		Traffic: []artifact.Flow{{ID: "read", Protocol: "http", Channel: "test_to_sut", StartedAt: testcase.StartedAt,
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://sut/items"}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {media}}, Body: &artifact.Body{Text: body}}},
		}},
	}
	if _, err := writer.Commit(capture); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}

func TestArchiveUsesFrozenContractsAndDerivesWorkflows(t *testing.T) {
	source := inputFile(t, "openapi.yaml", "openapi: 3.0.3\ninfo: {title: Items, version: '1'}\nservers: [{url: 'http://sut'}]\npaths:\n  /items:\n    get:\n      responses:\n        '200': {description: ok, content: {application/json: {schema: {type: object}}}}\n")
	refs := []artifact.SchemaRef{{ID: "items", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: source}}}
	path := htmlArchive(t, false, true, refs)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	inputs, err := Archive(context.Background(), path, "Items", true)
	if err != nil {
		t.Fatal(err)
	}
	if inputs.Report.SUTName != "Items" || len(inputs.Report.Artifacts) != 1 || len(inputs.Report.TestCases) != 1 || len(inputs.Specs) != 1 || !strings.HasPrefix(inputs.Specs[0].Name, "contracts/") || !strings.Contains(inputs.Specs[0].Content, "openapi") {
		t.Fatalf("lost frozen contract or coverage: %#v", inputs)
	}
	if inputs.Arazzo == nil || len(inputs.Arazzo.Workflows) != 1 || inputs.Arazzo.Workflows[0].AttemptID != "read-items" {
		t.Fatalf("lost derived workflows: %#v", inputs.Arazzo)
	}
	withoutArtifacts, err := Archive(context.Background(), path, "Items", false)
	if err != nil || len(withoutArtifacts.Report.Artifacts) != 0 {
		t.Fatalf("includeArtifacts ignored: %#v, %v", withoutArtifacts.Report, err)
	}
}

func TestArchivePreservesCoverageWhenArazzoUnavailable(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		t.Run(map[bool]string{false: "non JSON", true: "incomplete"}[incomplete], func(t *testing.T) {
			inputs, err := Archive(context.Background(), htmlArchive(t, incomplete, incomplete, nil), "Items", false)
			if err != nil {
				t.Fatal(err)
			}
			if inputs.Report.ArtifactCount != 1 || len(inputs.Report.TestCases) != 1 || inputs.Arazzo != nil || len(inputs.Report.Notes) == 0 || !strings.Contains(strings.Join(inputs.Report.Notes, " "), "Arazzo workflows unavailable") {
				t.Fatalf("lost coverage or missing eligibility diagnostic: %#v", inputs)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Archive(ctx, "missing", "", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored cancellation: %v", err)
	}
}

func TestFromSnapshotKeepsWorkflowNotesOutOfSharedCoverage(t *testing.T) {
	path := htmlArchive(t, false, false, nil)
	run, captures, err := bundle.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := coverage.GenerateBundleSnapshot(path, run, captures, "Items", false)
	if err != nil {
		t.Fatal(err)
	}
	report.Notes = make([]string, 1, 4)
	report.Notes[0] = "original note"
	for _, workflowErr := range []error{nil, errors.New("incomplete workflow")} {
		inputs, err := FromSnapshot(context.Background(), run, report, nil, workflowErr)
		if err != nil {
			t.Fatal(err)
		}
		if len(inputs.Report.Notes) != 2 {
			t.Fatalf("missing workflow note: %v", inputs.Report.Notes)
		}
		inputs.Report.Notes[0] = "changed in HTML"
		if len(report.Notes) != 1 || report.Notes[0] != "original note" || report.Notes[:cap(report.Notes)][1] != "" {
			t.Fatalf("HTML changed shared coverage notes: %v", report.Notes[:cap(report.Notes)])
		}
	}
}

func TestArchiveKeepsUnknownCoverageAndObservedWorkflowsForOmittedSpec(t *testing.T) {
	refs := []artifact.SchemaRef{{ID: "items", AssetID: "items", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "omitted"}}}
	inputs, err := Archive(context.Background(), htmlArchive(t, false, true, refs), "Items", false)
	if err != nil {
		t.Fatal(err)
	}
	if inputs.Report.ArtifactCount != 1 || len(inputs.Report.TestCases) != 1 || len(inputs.Report.Provided.Specs) != 1 || inputs.Report.Provided.Specs[0].DenominatorKnown {
		t.Fatalf("lost schema-free capture or claimed a known denominator: %#v", inputs.Report)
	}
	if inputs.Arazzo == nil || len(inputs.Arazzo.Workflows) != 1 || len(inputs.Specs) != 1 || !strings.HasPrefix(inputs.Specs[0].Name, "observed/") {
		t.Fatalf("missing observed workflow sources: %#v", inputs)
	}
}
