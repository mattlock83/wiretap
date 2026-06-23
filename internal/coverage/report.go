package coverage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/phrond/evermock/internal/artifact"
	"gopkg.in/yaml.v3"
)

type Report struct {
	GeneratedAt   time.Time          `json:"generated_at" yaml:"generated_at"`
	SUTName       string             `json:"sut_name" yaml:"sut_name"`
	ArtifactCount int                `json:"artifact_count" yaml:"artifact_count"`
	Artifacts     []string           `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	Provided      SpecCoverageGroup  `json:"provided" yaml:"provided"`
	Dependent     SpecCoverageGroup  `json:"dependent" yaml:"dependent"`
	TestCases     []TestCaseCoverage `json:"testcases,omitempty" yaml:"testcases,omitempty"`
	Notes         []string           `json:"notes,omitempty" yaml:"notes,omitempty"`
}

type SpecCoverageGroup struct {
	Specs []SpecCoverage `json:"specs" yaml:"specs"`
}

type SpecCoverage struct {
	SchemaID   string             `json:"schema_id" yaml:"schema_id"`
	Role       string             `json:"role" yaml:"role"`
	Type       string             `json:"type" yaml:"type"`
	Source     string             `json:"source" yaml:"source"`
	Exercised  []OperationSamples `json:"exercised,omitempty" yaml:"exercised,omitempty"`
	NotCovered []OperationSamples `json:"not_covered,omitempty" yaml:"not_covered,omitempty"`
	Faked      []OperationSamples `json:"faked,omitempty" yaml:"faked,omitempty"`
}

type OperationSamples struct {
	Operation string       `json:"operation" yaml:"operation"`
	Samples   []FlowSample `json:"samples,omitempty" yaml:"samples,omitempty"`
}

type TestCaseCoverage struct {
	Name      string                 `json:"name" yaml:"name"`
	Provided  []SpecOperationSamples `json:"provided,omitempty" yaml:"provided,omitempty"`
	Dependent []SpecOperationSamples `json:"dependent,omitempty" yaml:"dependent,omitempty"`
}

type SpecOperationSamples struct {
	SchemaID   string             `json:"schema_id" yaml:"schema_id"`
	Type       string             `json:"type" yaml:"type"`
	Source     string             `json:"source" yaml:"source"`
	Operations []OperationSamples `json:"operations,omitempty" yaml:"operations,omitempty"`
}

type FlowSample struct {
	TestCase string             `json:"testcase" yaml:"testcase"`
	FlowID   string             `json:"flow_id,omitempty" yaml:"flow_id,omitempty"`
	Protocol string             `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	Channel  string             `json:"channel,omitempty" yaml:"channel,omitempty"`
	Request  InteractionMessage `json:"request,omitempty" yaml:"request,omitempty"`
	Response InteractionMessage `json:"response,omitempty" yaml:"response,omitempty"`
}

type InteractionMessage struct {
	Method  string           `json:"method,omitempty" yaml:"method,omitempty"`
	URL     string           `json:"url,omitempty" yaml:"url,omitempty"`
	Status  int              `json:"status,omitempty" yaml:"status,omitempty"`
	Headers artifact.Headers `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body    string           `json:"body,omitempty" yaml:"body,omitempty"`
}

type loadedSchema struct {
	Ref        artifact.SchemaRef
	Source     string
	Operations []string
	OpenAPI    *openapiSpec
	Exercised  map[string]struct{}
	Faked      map[string]struct{}
}

type openapiSpec struct {
	Doc    *openapi3.T
	Router routers.Router
}

type flowEntry struct {
	Flow     artifact.Flow
	TestCase string
}

// Generate builds a coverage report from artifact files in a directory.
func Generate(ctx context.Context, artifactsDir, sutName string, includeArtifacts bool) (*Report, error) {
	_ = ctx
	if strings.TrimSpace(artifactsDir) == "" {
		artifactsDir = "artifacts"
	}
	paths, err := listArtifactFiles(artifactsDir)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("no artifact files found")
	}

	entries, err := loadArtifacts(paths)
	if err != nil {
		return nil, err
	}

	if sutName == "" {
		sutName = inferSUTName(entries)
	}

	provided, dependent, testcases, notes := buildCoverage(entries)

	report := &Report{
		GeneratedAt:   time.Now().UTC(),
		SUTName:       sutName,
		ArtifactCount: len(entries),
		Provided:      provided,
		Dependent:     dependent,
		TestCases:     testcases,
		Notes:         notes,
	}
	if includeArtifacts {
		report.Artifacts = paths
	}
	return report, nil
}

// WriteReport writes the report to the writer in the requested format.
func WriteReport(w io.Writer, report *Report, format string) error {
	if report == nil {
		return errors.New("report is nil")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "postman", "collection":
		return WritePostmanCollection(w, report)
	case "yaml", "yml":
		data, err := yaml.Marshal(report)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	case "json", "":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case "html", "htm":
		collection, err := CollectionFromReport(report)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(collection)
		if err != nil {
			return err
		}
		parsedReport, err := ReportFromPostmanJSON(payload)
		if err != nil {
			return err
		}
		return writeHTMLReport(w, parsedReport)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeHTMLReport(w io.Writer, report *Report) error {
	tpl := template.New("report").Funcs(template.FuncMap{
		"join": func(values []string) string {
			return strings.Join(values, ", ")
		},
		"samplesForOp": func(items []OperationSamples, op string) []FlowSample {
			for _, item := range items {
				if item.Operation == op {
					return item.Samples
				}
			}
			return nil
		},
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format(time.RFC3339)
		},
		"prettyJSON":    prettyJSON,
		"formatHeaders": formatHeaders,
	})
	parsed, err := tpl.Parse(reportHTMLTemplate)
	if err != nil {
		return err
	}
	return parsed.Execute(w, report)
}

func prettyJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return raw
	}
	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return raw
	}
	return string(pretty)
}

func formatHeaders(headers artifact.Headers) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(headers))
	for _, key := range keys {
		values := headers[key]
		if len(values) == 0 {
			lines = append(lines, key+":")
			continue
		}
		for _, value := range values {
			lines = append(lines, fmt.Sprintf("%s: %s", key, value))
		}
	}
	return strings.Join(lines, "\n")
}

const reportHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Phrond Coverage Report</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f7f8fb;
      --panel: #ffffff;
      --text: #0f172a;
      --muted: #64748b;
      --border: #e2e8f0;
      --accent: #0f766e;
      --warn: #b45309;
    }
    body {
      margin: 0;
      font-family: "IBM Plex Sans", "Helvetica Neue", Arial, sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    header {
      padding: 32px 48px 16px;
      background: linear-gradient(120deg, #0f766e, #0ea5e9);
      color: #fff;
    }
    header h1 {
      margin: 0 0 8px;
      font-size: 28px;
    }
    header p {
      margin: 0;
      font-size: 14px;
      opacity: 0.85;
    }
    main {
      padding: 24px 48px 48px;
      display: flex;
      flex-direction: column;
      gap: 24px;
    }
    .panel {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 12px;
      padding: 20px 24px;
      box-shadow: 0 10px 20px rgba(15, 23, 42, 0.05);
    }
    .panel h2 {
      margin: 0 0 12px;
      font-size: 18px;
    }
    .summary-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
      gap: 16px;
    }
    .summary-card {
      border: 1px solid var(--border);
      border-radius: 10px;
      padding: 12px 16px;
      background: #f8fafc;
    }
    .summary-card span {
      display: block;
      font-size: 12px;
      color: var(--muted);
      text-transform: uppercase;
      letter-spacing: 0.08em;
      margin-bottom: 6px;
    }
    .summary-card strong {
      font-size: 20px;
    }
    .tabs {
      display: flex;
      gap: 8px;
    }
    .tab-button {
      border: 1px solid var(--border);
      background: #f1f5f9;
      color: var(--text);
      padding: 8px 14px;
      border-radius: 999px;
      cursor: pointer;
      font-weight: 600;
    }
    .tab-button.active {
      background: var(--accent);
      color: #ffffff;
      border-color: var(--accent);
    }
    .tab-panel {
      display: none;
      gap: 24px;
      flex-direction: column;
    }
    .tab-panel.active {
      display: flex;
    }
    details {
      border: 1px solid var(--border);
      border-radius: 12px;
      padding: 10px 14px;
      background: #f8fafc;
      margin-top: 12px;
    }
    details[open] > summary {
      margin-bottom: 12px;
    }
    summary {
      cursor: pointer;
      display: flex;
      gap: 12px;
      align-items: center;
      justify-content: space-between;
      font-weight: 600;
      color: var(--text);
    }
    summary .meta {
      font-weight: 500;
      color: var(--muted);
      font-size: 12px;
    }
    .spec {
      background: #ffffff;
    }
    .spec-body {
      display: flex;
      flex-direction: column;
      gap: 12px;
    }
    .op-group h4 {
      margin: 0 0 8px;
      font-size: 14px;
      color: var(--accent);
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .op {
      background: #f1f5f9;
      margin-top: 8px;
    }
    .sample {
      border: 1px solid var(--border);
      border-radius: 10px;
      padding: 12px;
      margin-top: 10px;
      background: #ffffff;
    }
    .sample-title {
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--muted);
    }
    .sample-body {
      margin-top: 8px;
      display: grid;
      gap: 6px;
      font-size: 13px;
    }
    .code-block {
      margin: 0;
      padding: 8px 10px;
      background: #0f172a;
      color: #e2e8f0;
      border-radius: 8px;
      font-family: "IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
      font-size: 12px;
      white-space: pre-wrap;
      word-break: break-word;
    }
    .meta {
      color: var(--muted);
      font-size: 13px;
      margin-bottom: 8px;
    }
    ul {
      margin: 8px 0 0 18px;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 13px;
    }
    th, td {
      border: 1px solid var(--border);
      padding: 8px 10px;
      text-align: left;
    }
    th {
      background: #f1f5f9;
    }
    .notes {
      color: var(--warn);
      font-size: 13px;
    }
    @media (max-width: 700px) {
      header, main {
        padding-left: 20px;
        padding-right: 20px;
      }
    }
  </style>
</head>
<body>
  <header>
    <h1>Phrond Coverage Report</h1>
    <p>Generated {{ formatTime .GeneratedAt }} · SUT: {{ .SUTName }}</p>
  </header>
  <main>
    <div class="tabs">
      <button class="tab-button active" data-tab="tab-specs">By API Spec</button>
      <button class="tab-button" data-tab="tab-tests">By Test Case</button>
    </div>

    <div id="tab-specs" class="tab-panel active">
      <section class="panel">
      <h2>Summary</h2>
      <div class="summary-grid">
        <div class="summary-card">
          <span>Artifacts</span>
          <strong>{{ .ArtifactCount }}</strong>
        </div>
        <div class="summary-card">
          <span>Provided Specs</span>
          <strong>{{ len .Provided.Specs }}</strong>
        </div>
        <div class="summary-card">
          <span>Dependent Specs</span>
          <strong>{{ len .Dependent.Specs }}</strong>
        </div>
      </div>
      {{ if .Notes }}
      <div class="notes" style="margin-top: 16px;">
        <strong>Notes:</strong>
        <ul>
          {{ range .Notes }}<li>{{ . }}</li>{{ end }}
        </ul>
      </div>
      {{ end }}
    </section>

    <section class="panel">
      <h2>Provided API Specs</h2>
      {{ range .Provided.Specs }}
        {{ $spec := . }}
        <details class="spec" open>
          <summary>
            <span>{{ $spec.SchemaID }}</span>
            <span class="meta">Type: {{ $spec.Type }}</span>
          </summary>
          <div class="spec-body">
            <div class="meta">Source: {{ $spec.Source }}</div>
            <div class="op-group">
              <h4>Exercised</h4>
              {{ if $spec.Exercised }}
                {{ range $spec.Exercised }}
                  <details class="op">
                    <summary>{{ .Operation }}</summary>
                    {{ if .Samples }}
                      {{ range .Samples }}
                        <div class="sample">
                          <div class="sample-title">Test Case: {{ .TestCase }}</div>
                          <div class="sample-body">
                            <div><strong>Request:</strong> {{ .Request.Method }} {{ .Request.URL }}</div>
                            {{ if .Request.Headers }}<div><strong>Request Headers:</strong></div><pre class="code-block">{{ formatHeaders .Request.Headers }}</pre>{{ end }}
                            {{ if .Request.Body }}<pre class="code-block">{{ prettyJSON .Request.Body }}</pre>{{ end }}
                            <div><strong>Response:</strong> {{ .Response.Status }}</div>
                            {{ if .Response.Headers }}<div><strong>Response Headers:</strong></div><pre class="code-block">{{ formatHeaders .Response.Headers }}</pre>{{ end }}
                            {{ if .Response.Body }}<pre class="code-block">{{ prettyJSON .Response.Body }}</pre>{{ end }}
                          </div>
                        </div>
                      {{ end }}
                    {{ else }}
                      <div class="meta">No captured samples.</div>
                    {{ end }}
                  </details>
                {{ end }}
              {{ else }}
                <div class="meta">None recorded.</div>
              {{ end }}
            </div>
            <div class="op-group">
              <h4>Not Covered</h4>
              {{ if $spec.NotCovered }}
                {{ range $spec.NotCovered }}
                  <details class="op">
                    <summary>{{ .Operation }}</summary>
                    <div class="meta">Not exercised by tests.</div>
                  </details>
                {{ end }}
              {{ else }}
                <div class="meta">All operations exercised.</div>
              {{ end }}
            </div>
          </div>
        </details>
      {{ end }}
    </section>

    <section class="panel">
      <h2>Dependent API Specs</h2>
      {{ range .Dependent.Specs }}
        {{ $spec := . }}
        <details class="spec" open>
          <summary>
            <span>{{ $spec.SchemaID }}</span>
            <span class="meta">Type: {{ $spec.Type }}</span>
          </summary>
          <div class="spec-body">
            <div class="meta">Source: {{ $spec.Source }}</div>
            <div class="op-group">
              <h4>Faked</h4>
              {{ if $spec.Faked }}
                {{ range $spec.Faked }}
                  <details class="op">
                    <summary>{{ .Operation }}</summary>
                    {{ if .Samples }}
                      {{ range .Samples }}
                        <div class="sample">
                          <div class="sample-title">Test Case: {{ .TestCase }}</div>
                          <div class="sample-body">
                            <div><strong>Request:</strong> {{ .Request.Method }} {{ .Request.URL }}</div>
                            {{ if .Request.Headers }}<div><strong>Request Headers:</strong></div><pre class="code-block">{{ formatHeaders .Request.Headers }}</pre>{{ end }}
                            {{ if .Request.Body }}<pre class="code-block">{{ prettyJSON .Request.Body }}</pre>{{ end }}
                            <div><strong>Response:</strong> {{ .Response.Status }}</div>
                            {{ if .Response.Headers }}<div><strong>Response Headers:</strong></div><pre class="code-block">{{ formatHeaders .Response.Headers }}</pre>{{ end }}
                            {{ if .Response.Body }}<pre class="code-block">{{ prettyJSON .Response.Body }}</pre>{{ end }}
                          </div>
                        </div>
                      {{ end }}
                    {{ else }}
                      <div class="meta">No captured samples.</div>
                    {{ end }}
                  </details>
                {{ end }}
              {{ else }}
                <div class="meta">No faked operations recorded.</div>
              {{ end }}
            </div>
          </div>
        </details>
      {{ end }}
    </section>
    </div>

    <div id="tab-tests" class="tab-panel">
      {{ range .TestCases }}
        <section class="panel">
          <h2>Test Case: {{ .Name }}</h2>
          <div class="op-group">
            <h4>Provided Specs</h4>
            {{ if .Provided }}
              {{ range .Provided }}
                {{ $spec := . }}
                <details class="spec" open>
                  <summary>
                    <span>{{ $spec.SchemaID }}</span>
                    <span class="meta">Type: {{ $spec.Type }}</span>
                  </summary>
                  <div class="spec-body">
                    <div class="meta">Source: {{ $spec.Source }}</div>
                    {{ range $spec.Operations }}
                      <details class="op">
                        <summary>{{ .Operation }}</summary>
                        {{ if .Samples }}
                          {{ range .Samples }}
                            <div class="sample">
                              <div class="sample-title">Test Case: {{ .TestCase }}</div>
                              <div class="sample-body">
                                <div><strong>Request:</strong> {{ .Request.Method }} {{ .Request.URL }}</div>
                                {{ if .Request.Headers }}<div><strong>Request Headers:</strong></div><pre class="code-block">{{ formatHeaders .Request.Headers }}</pre>{{ end }}
                                {{ if .Request.Body }}<pre class="code-block">{{ prettyJSON .Request.Body }}</pre>{{ end }}
                                <div><strong>Response:</strong> {{ .Response.Status }}</div>
                                {{ if .Response.Headers }}<div><strong>Response Headers:</strong></div><pre class="code-block">{{ formatHeaders .Response.Headers }}</pre>{{ end }}
                                {{ if .Response.Body }}<pre class="code-block">{{ prettyJSON .Response.Body }}</pre>{{ end }}
                              </div>
                            </div>
                          {{ end }}
                        {{ else }}
                          <div class="meta">No captured samples.</div>
                        {{ end }}
                      </details>
                    {{ end }}
                  </div>
                </details>
              {{ end }}
            {{ else }}
              <div class="meta">No provided interactions.</div>
            {{ end }}
          </div>

          <div class="op-group">
            <h4>Dependent Specs</h4>
            {{ if .Dependent }}
              {{ range .Dependent }}
                {{ $spec := . }}
                <details class="spec" open>
                  <summary>
                    <span>{{ $spec.SchemaID }}</span>
                    <span class="meta">Type: {{ $spec.Type }}</span>
                  </summary>
                  <div class="spec-body">
                    <div class="meta">Source: {{ $spec.Source }}</div>
                    {{ range $spec.Operations }}
                      <details class="op">
                        <summary>{{ .Operation }}</summary>
                        {{ if .Samples }}
                          {{ range .Samples }}
                            <div class="sample">
                              <div class="sample-title">Test Case: {{ .TestCase }}</div>
                              <div class="sample-body">
                                <div><strong>Request:</strong> {{ .Request.Method }} {{ .Request.URL }}</div>
                                {{ if .Request.Headers }}<div><strong>Request Headers:</strong></div><pre class="code-block">{{ formatHeaders .Request.Headers }}</pre>{{ end }}
                                {{ if .Request.Body }}<pre class="code-block">{{ prettyJSON .Request.Body }}</pre>{{ end }}
                                <div><strong>Response:</strong> {{ .Response.Status }}</div>
                                {{ if .Response.Headers }}<div><strong>Response Headers:</strong></div><pre class="code-block">{{ formatHeaders .Response.Headers }}</pre>{{ end }}
                                {{ if .Response.Body }}<pre class="code-block">{{ prettyJSON .Response.Body }}</pre>{{ end }}
                              </div>
                            </div>
                          {{ end }}
                        {{ else }}
                          <div class="meta">No captured samples.</div>
                        {{ end }}
                      </details>
                    {{ end }}
                  </div>
                </details>
              {{ end }}
            {{ else }}
              <div class="meta">No dependent interactions.</div>
            {{ end }}
          </div>
        </section>
      {{ end }}
    </div>
  </main>
  <script>
    const tabs = document.querySelectorAll('.tab-button');
    const panels = document.querySelectorAll('.tab-panel');
    tabs.forEach((tab) => {
      tab.addEventListener('click', () => {
        tabs.forEach((btn) => btn.classList.remove('active'));
        panels.forEach((panel) => panel.classList.remove('active'));
        tab.classList.add('active');
        const panel = document.getElementById(tab.dataset.tab);
        if (panel) {
          panel.classList.add('active');
        }
      });
    });
  </script>
</body>
</html>`

type artifactEntry struct {
	Path string
	Doc  *artifact.Artifact
}

func listArtifactFiles(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("artifacts path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !isArtifactFile(path) {
			return nil, fmt.Errorf("unsupported artifact file: %s", path)
		}
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		candidate := filepath.Join(path, entry.Name())
		if isArtifactFile(candidate) {
			paths = append(paths, candidate)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func loadArtifacts(paths []string) ([]artifactEntry, error) {
	var entries []artifactEntry
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var doc artifact.Artifact
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".yaml", ".yml":
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return nil, fmt.Errorf("decode %s: %w", path, err)
			}
		case ".json":
			if err := json.Unmarshal(data, &doc); err != nil {
				return nil, fmt.Errorf("decode %s: %w", path, err)
			}
		default:
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return nil, fmt.Errorf("decode %s: %w", path, err)
			}
		}
		entries = append(entries, artifactEntry{Path: path, Doc: &doc})
	}
	return entries, nil
}

func isArtifactFile(path string) bool {
	name := strings.ToLower(filepath.Ext(path))
	return name == ".yaml" || name == ".yml" || name == ".json"
}

func inferSUTName(entries []artifactEntry) string {
	for _, entry := range entries {
		if entry.Doc != nil {
			if name := strings.TrimSpace(entry.Doc.TestCase.Name); name != "" {
				return name
			}
		}
	}
	return "SUT"
}

func buildCoverage(entries []artifactEntry) (SpecCoverageGroup, SpecCoverageGroup, []TestCaseCoverage, []string) {
	var notes []string
	flows := []artifact.Flow{}
	flowEntries := []flowEntry{}
	schemas := []artifact.SchemaRef{}

	for _, entry := range entries {
		if entry.Doc == nil {
			continue
		}
		tcName := strings.TrimSpace(entry.Doc.TestCase.Name)
		if tcName == "" {
			tcName = entry.Doc.TestCase.ID
		}
		for _, flow := range entry.Doc.Traffic {
			flows = append(flows, flow)
			flowEntries = append(flowEntries, flowEntry{Flow: flow, TestCase: tcName})
		}
		schemas = append(schemas, entry.Doc.Schemas...)
	}

	providedSchemas, providedNotes := loadSchemas(filterSchemas(schemas, "sut"))
	dependentSchemas, dependentNotes := loadSchemas(filterSchemas(schemas, "fake"))
	notes = append(notes, providedNotes...)
	notes = append(notes, dependentNotes...)

	markCoverage(flows, providedSchemas, "test_to_sut")
	markCoverage(flows, dependentSchemas, "sut_to_mock")

	providedSamples := buildOperationSamples(flowEntries, providedSchemas, "test_to_sut")
	dependentSamples := buildOperationSamples(flowEntries, dependentSchemas, "sut_to_mock")

	providedGroup := SpecCoverageGroup{Specs: exportSpecs(providedSchemas, true, providedSamples, false)}
	dependentGroup := SpecCoverageGroup{Specs: exportSpecs(dependentSchemas, false, dependentSamples, true)}
	testcases := buildTestCaseCoverage(flowEntries, providedSchemas, dependentSchemas)

	return providedGroup, dependentGroup, testcases, notes
}

func filterSchemas(schemas []artifact.SchemaRef, role string) []artifact.SchemaRef {
	var filtered []artifact.SchemaRef
	for _, schema := range schemas {
		if strings.EqualFold(schema.Role, role) {
			filtered = append(filtered, schema)
		}
	}
	return filtered
}

func loadSchemas(schemas []artifact.SchemaRef) ([]*loadedSchema, []string) {
	seen := map[string]*loadedSchema{}
	var notes []string
	for _, schema := range schemas {
		key := schemaKey(schema)
		if _, ok := seen[key]; ok {
			continue
		}
		entry := &loadedSchema{
			Ref:       schema,
			Source:    schemaSourceLabel(schema),
			Exercised: map[string]struct{}{},
			Faked:     map[string]struct{}{},
		}
		if strings.EqualFold(schema.Type, "openapi") {
			if spec, err := loadOpenAPISpec(schema); err == nil {
				entry.OpenAPI = spec
				entry.Operations = openapiOperations(spec)
			} else {
				notes = append(notes, fmt.Sprintf("OpenAPI schema %q failed to load: %v", schema.ID, err))
			}
		} else {
			notes = append(notes, fmt.Sprintf("Schema %q has unsupported type %q (only openapi is supported).", schema.ID, schema.Type))
		}
		seen[key] = entry
	}
	var out []*loadedSchema
	for _, entry := range seen {
		out = append(out, entry)
	}
	return out, notes
}

func markCoverage(flows []artifact.Flow, schemas []*loadedSchema, channel string) {
	if len(schemas) == 0 {
		return
	}
	for _, flow := range flows {
		if flow.Channel != channel {
			continue
		}
		for _, schema := range schemas {
			if schema.OpenAPI != nil && flow.Protocol == "http" {
				if op, ok := openapiOperationFromFlow(flow, schema.OpenAPI); ok {
					if channel == "sut_to_mock" {
						schema.Faked[op] = struct{}{}
					} else {
						schema.Exercised[op] = struct{}{}
					}
				}
			}
		}
	}
}

func exportSpecs(schemas []*loadedSchema, includeNotCovered bool, samples map[string]map[string][]FlowSample, includeFaked bool) []SpecCoverage {
	var specs []SpecCoverage
	for _, schema := range schemas {
		sampleMap := samples[schema.Ref.ID]
		exercisedOps := uniqueSorted(setToList(schema.Exercised))
		fakedOps := uniqueSorted(setToList(schema.Faked))
		notCoveredOps := []string{}
		if includeNotCovered {
			notCoveredOps = uniqueSorted(diffOps(schema.Operations, schema.Exercised))
		}
		coverage := SpecCoverage{
			SchemaID: schema.Ref.ID,
			Role:     schema.Ref.Role,
			Type:     schema.Ref.Type,
			Source:   schema.Source,
		}
		if len(exercisedOps) > 0 {
			coverage.Exercised = operationSamplesList(exercisedOps, sampleMap)
		}
		if len(notCoveredOps) > 0 {
			coverage.NotCovered = operationSamplesList(notCoveredOps, nil)
		}
		if includeFaked && len(fakedOps) > 0 {
			coverage.Faked = operationSamplesList(fakedOps, sampleMap)
		}
		specs = append(specs, coverage)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].SchemaID < specs[j].SchemaID })
	return specs
}

func operationSamplesList(ops []string, samples map[string][]FlowSample) []OperationSamples {
	if len(ops) == 0 {
		return nil
	}
	out := make([]OperationSamples, 0, len(ops))
	for _, op := range ops {
		list := samples[op]
		if len(list) > 1 {
			sortSamples(list)
		}
		out = append(out, OperationSamples{Operation: op, Samples: list})
	}
	return out
}

func sortSamples(samples []FlowSample) {
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].TestCase == samples[j].TestCase {
			return samples[i].FlowID < samples[j].FlowID
		}
		return samples[i].TestCase < samples[j].TestCase
	})
}

func buildOperationSamples(entries []flowEntry, schemas []*loadedSchema, channel string) map[string]map[string][]FlowSample {
	raw := map[string]map[string][]FlowSample{}
	for _, entry := range entries {
		flow := entry.Flow
		if flow.Channel != channel {
			continue
		}
		for _, schema := range schemas {
			switch {
			case schema.OpenAPI != nil && flow.Protocol == "http":
				op, ok := openapiOperationFromFlow(flow, schema.OpenAPI)
				if !ok {
					continue
				}
				if !containsOp(schema.Operations, op) {
					continue
				}
				raw = addSample(raw, schema.Ref.ID, op, flowSampleFromFlow(entry))
			}
		}
	}
	return raw
}

func addSample(target map[string]map[string][]FlowSample, schemaID, operation string, sample FlowSample) map[string]map[string][]FlowSample {
	if _, ok := target[schemaID]; !ok {
		target[schemaID] = map[string][]FlowSample{}
	}
	target[schemaID][operation] = append(target[schemaID][operation], sample)
	return target
}

func buildTestCaseCoverage(entries []flowEntry, providedSchemas, dependentSchemas []*loadedSchema) []TestCaseCoverage {
	buckets := map[string]*TestCaseCoverage{}
	for _, entry := range entries {
		flow := entry.Flow
		tcName := strings.TrimSpace(entry.TestCase)
		if tcName == "" {
			tcName = "unknown"
		}
		tc := buckets[tcName]
		if tc == nil {
			tc = &TestCaseCoverage{Name: tcName}
			buckets[tcName] = tc
		}

		var schemas []*loadedSchema
		var target *[]SpecOperationSamples
		switch flow.Channel {
		case "test_to_sut":
			schemas = providedSchemas
			target = &tc.Provided
		case "sut_to_mock":
			schemas = dependentSchemas
			target = &tc.Dependent
		default:
			continue
		}

		for _, schema := range schemas {
			op := ""
			if schema.OpenAPI != nil && flow.Protocol == "http" {
				if matched, ok := openapiOperationFromFlow(flow, schema.OpenAPI); ok {
					op = matched
				}
			}
			if op == "" {
				continue
			}

			spec := findOrCreateSpec(target, schema)
			spec.Operations = appendOperationSample(spec.Operations, op, flowSampleFromFlow(entry))
		}
	}

	var out []TestCaseCoverage
	for _, tc := range buckets {
		sort.Slice(tc.Provided, func(i, j int) bool { return tc.Provided[i].SchemaID < tc.Provided[j].SchemaID })
		sort.Slice(tc.Dependent, func(i, j int) bool { return tc.Dependent[i].SchemaID < tc.Dependent[j].SchemaID })
		for i := range tc.Provided {
			sortOperationSamples(tc.Provided[i].Operations)
		}
		for i := range tc.Dependent {
			sortOperationSamples(tc.Dependent[i].Operations)
		}
		out = append(out, *tc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func findOrCreateSpec(target *[]SpecOperationSamples, schema *loadedSchema) *SpecOperationSamples {
	for i := range *target {
		if (*target)[i].SchemaID == schema.Ref.ID {
			return &(*target)[i]
		}
	}
	entry := SpecOperationSamples{
		SchemaID: schema.Ref.ID,
		Type:     schema.Ref.Type,
		Source:   schema.Source,
	}
	*target = append(*target, entry)
	return &(*target)[len(*target)-1]
}

func appendOperationSample(ops []OperationSamples, operation string, sample FlowSample) []OperationSamples {
	for i := range ops {
		if ops[i].Operation == operation {
			ops[i].Samples = append(ops[i].Samples, sample)
			return ops
		}
	}
	ops = append(ops, OperationSamples{Operation: operation, Samples: []FlowSample{sample}})
	return ops
}

func sortOperationSamples(ops []OperationSamples) {
	sort.Slice(ops, func(i, j int) bool { return ops[i].Operation < ops[j].Operation })
	for i := range ops {
		if len(ops[i].Samples) > 1 {
			sortSamples(ops[i].Samples)
		}
	}
}

func flowSampleFromFlow(entry flowEntry) FlowSample {
	flow := entry.Flow
	sample := FlowSample{
		TestCase: entry.TestCase,
		FlowID:   flow.ID,
		Protocol: flow.Protocol,
		Channel:  flow.Channel,
	}
	if flow.Protocol == "http" {
		if flow.Request.HTTP != nil {
			sample.Request = httpMessageSample(flow.Request.HTTP)
		}
		if flow.Response.HTTP != nil {
			sample.Response = httpMessageSample(flow.Response.HTTP)
		}
	}
	return sample
}

func httpMessageSample(msg *artifact.HttpMessage) InteractionMessage {
	if msg == nil {
		return InteractionMessage{}
	}
	return InteractionMessage{
		Method:  msg.Method,
		URL:     msg.URL,
		Status:  msg.Status,
		Headers: msg.Headers,
		Body:    bodyPreview(msg.Body),
	}
}

func bodyPreview(body *artifact.Body) string {
	if body == nil {
		return ""
	}
	if body.Text != "" {
		return truncateBody(body.Text)
	}
	if body.Decoded != nil && body.Decoded.Text != "" {
		return truncateBody(body.Decoded.Text)
	}
	if body.Bytes != "" {
		return fmt.Sprintf("[binary payload %d bytes base64]", len(body.Bytes))
	}
	return ""
}

func truncateBody(text string) string {
	const limit = 360
	clean := strings.TrimSpace(text)
	if len(clean) <= limit {
		return clean
	}
	return clean[:limit] + "…"
}

func schemaKey(schema artifact.SchemaRef) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s", schema.ID, schema.Role, schema.Type, schema.Source.Path, schema.Source.URL, schema.Source.Format)
}

func schemaSourceLabel(schema artifact.SchemaRef) string {
	if schema.Source.Path != "" {
		return schema.Source.Path
	}
	if schema.Source.URL != "" {
		return schema.Source.URL
	}
	if schema.Source.Content != "" {
		return "inline"
	}
	return "unknown"
}

func openapiOperationFromFlow(flow artifact.Flow, spec *openapiSpec) (string, bool) {
	if spec == nil || spec.Router == nil {
		return "", false
	}
	msg := flow.Request.HTTP
	if msg == nil {
		return "", false
	}
	parsed, err := parseRequestURL(msg.URL)
	if err != nil {
		return "", false
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	request, err := http.NewRequest(msg.Method, parsed.String(), nil)
	if err != nil {
		return "", false
	}
	for key, values := range msg.Headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}

	route, _, err := spec.Router.FindRoute(request)
	if err != nil {
		return "", false
	}
	return strings.ToUpper(route.Method) + " " + route.Path, true
}

func parseRequestURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("empty url")
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Scheme != "" {
		return parsed, nil
	}
	parsed, err = url.Parse("http://" + strings.TrimPrefix(raw, "//"))
	if err != nil {
		return nil, err
	}
	return parsed, nil
}

func openapiOperations(spec *openapiSpec) []string {
	if spec == nil || spec.Doc == nil {
		return nil
	}
	var ops []string
	for path, item := range spec.Doc.Paths.Map() {
		if item.Get != nil {
			ops = append(ops, "GET "+path)
		}
		if item.Post != nil {
			ops = append(ops, "POST "+path)
		}
		if item.Put != nil {
			ops = append(ops, "PUT "+path)
		}
		if item.Patch != nil {
			ops = append(ops, "PATCH "+path)
		}
		if item.Delete != nil {
			ops = append(ops, "DELETE "+path)
		}
		if item.Head != nil {
			ops = append(ops, "HEAD "+path)
		}
		if item.Options != nil {
			ops = append(ops, "OPTIONS "+path)
		}
	}
	return ops
}

func loadOpenAPISpec(ref artifact.SchemaRef) (*openapiSpec, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	var doc *openapi3.T
	var err error
	path := strings.TrimSpace(ref.Source.Path)
	urlStr := strings.TrimSpace(ref.Source.URL)
	content := strings.TrimSpace(ref.Source.Content)

	switch strings.ToLower(ref.Source.Type) {
	case "file":
		if path == "" {
			return nil, errors.New("openapi schema path required")
		}
		path = resolvePath(path)
		doc, err = loader.LoadFromFile(path)
	case "url":
		if urlStr == "" {
			return nil, errors.New("openapi schema url required")
		}
		uri, parseErr := url.Parse(urlStr)
		if parseErr != nil {
			return nil, parseErr
		}
		doc, err = loader.LoadFromURI(uri)
	case "inline":
		if content == "" {
			return nil, errors.New("openapi schema content required")
		}
		doc, err = loader.LoadFromData([]byte(content))
	default:
		return nil, errors.New("unsupported openapi source type")
	}
	if err != nil {
		return nil, err
	}
	if err := doc.Validate(context.Background()); err != nil {
		return nil, err
	}

	router, err := legacy.NewRouter(doc)
	if err != nil {
		return nil, err
	}
	return &openapiSpec{Doc: doc, Router: router}, nil
}

func resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	return filepath.Join(cwd, path)
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{})
	for _, value := range values {
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func setToList(set map[string]struct{}) []string {
	var out []string
	for value := range set {
		out = append(out, value)
	}
	return out
}

func diffOps(allOps []string, covered map[string]struct{}) []string {
	var out []string
	for _, op := range allOps {
		if _, ok := covered[op]; !ok {
			out = append(out, op)
		}
	}
	return out
}

func containsOp(ops []string, target string) bool {
	for _, op := range ops {
		if op == target {
			return true
		}
	}
	return false
}
