package coverage

import (
	"context"
	"encoding/base64"
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
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/phrond/evermock/internal/artifact"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"gopkg.in/yaml.v3"
)

type Report struct {
	GeneratedAt   time.Time         `json:"generated_at" yaml:"generated_at"`
	SUTName       string            `json:"sut_name" yaml:"sut_name"`
	ArtifactCount int               `json:"artifact_count" yaml:"artifact_count"`
	Artifacts     []string          `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	Provided      SpecCoverageGroup `json:"provided" yaml:"provided"`
	Dependent     SpecCoverageGroup `json:"dependent" yaml:"dependent"`
	Notes         []string          `json:"notes,omitempty" yaml:"notes,omitempty"`
}

type SpecCoverageGroup struct {
	Specs          []SpecCoverage  `json:"specs" yaml:"specs"`
	Exercised      []string        `json:"exercised,omitempty" yaml:"exercised,omitempty"`
	NotCovered     []string        `json:"not_covered,omitempty" yaml:"not_covered,omitempty"`
	Faked          []string        `json:"faked,omitempty" yaml:"faked,omitempty"`
	AssertedFields []AssertedField `json:"asserted_fields,omitempty" yaml:"asserted_fields,omitempty"`
}

type SpecCoverage struct {
	SchemaID            string                `json:"schema_id" yaml:"schema_id"`
	Role                string                `json:"role" yaml:"role"`
	Type                string                `json:"type" yaml:"type"`
	Source              string                `json:"source" yaml:"source"`
	Exercised           []string              `json:"exercised,omitempty" yaml:"exercised,omitempty"`
	NotCovered          []string              `json:"not_covered,omitempty" yaml:"not_covered,omitempty"`
	Faked               []string              `json:"faked,omitempty" yaml:"faked,omitempty"`
	AssertedByOperation []OperationAssertions `json:"asserted_by_operation,omitempty" yaml:"asserted_by_operation,omitempty"`
	Interactions        []OperationSamples    `json:"interactions,omitempty" yaml:"interactions,omitempty"`
}

type AssertedField struct {
	FlowID   string `json:"flow_id,omitempty" yaml:"flow_id,omitempty"`
	Channel  string `json:"channel,omitempty" yaml:"channel,omitempty"`
	Phase    string `json:"phase,omitempty" yaml:"phase,omitempty"`
	PathLang string `json:"path_lang,omitempty" yaml:"path_lang,omitempty"`
	Field    string `json:"field,omitempty" yaml:"field,omitempty"`
	Operator string `json:"operator,omitempty" yaml:"operator,omitempty"`
}

type OperationAssertions struct {
	Operation string          `json:"operation" yaml:"operation"`
	Fields    []AssertedField `json:"fields,omitempty" yaml:"fields,omitempty"`
}

type OperationSamples struct {
	Operation string       `json:"operation" yaml:"operation"`
	Samples   []FlowSample `json:"samples,omitempty" yaml:"samples,omitempty"`
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
	Method      string           `json:"method,omitempty" yaml:"method,omitempty"`
	URL         string           `json:"url,omitempty" yaml:"url,omitempty"`
	Status      int              `json:"status,omitempty" yaml:"status,omitempty"`
	Headers     artifact.Headers `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body        string           `json:"body,omitempty" yaml:"body,omitempty"`
	ContentType string           `json:"content_type,omitempty" yaml:"content_type,omitempty"`
	GRPCService string           `json:"grpc_service,omitempty" yaml:"grpc_service,omitempty"`
	GRPCMethod  string           `json:"grpc_method,omitempty" yaml:"grpc_method,omitempty"`
	GRPCStatus  int              `json:"grpc_status,omitempty" yaml:"grpc_status,omitempty"`
	GRPCMessage string           `json:"grpc_message,omitempty" yaml:"grpc_message,omitempty"`
}

type loadedSchema struct {
	Ref        artifact.SchemaRef
	Source     string
	Operations []string
	OpenAPI    *openapiSpec
	Proto      *protoSpec
	Exercised  map[string]struct{}
	Faked      map[string]struct{}
}

type openapiSpec struct {
	Doc    *openapi3.T
	Router routers.Router
}

type protoSpec struct {
	Services map[string]*desc.ServiceDescriptor
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

	provided, dependent, notes := buildCoverage(entries)

	report := &Report{
		GeneratedAt:   time.Now().UTC(),
		SUTName:       sutName,
		ArtifactCount: len(entries),
		Provided:      provided,
		Dependent:     dependent,
		Notes:         notes,
	}
	if includeArtifacts {
		report.Artifacts = paths
	}
	return report, nil
}

// WriteReport writes the report to the writer in the requested format (json or yaml).
func WriteReport(w io.Writer, report *Report, format string) error {
	if report == nil {
		return errors.New("report is nil")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
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
		return writeHTMLReport(w, report)
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
	})
	parsed, err := tpl.Parse(reportHTMLTemplate)
	if err != nil {
		return err
	}
	return parsed.Execute(w, report)
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
                  {{ $samples := samplesForOp $spec.Interactions . }}
                  <details class="op">
                    <summary>{{ . }}</summary>
                    {{ if $samples }}
                      {{ range $samples }}
                        <div class="sample">
                          <div class="sample-title">Test Case: {{ .TestCase }}</div>
                          <div class="sample-body">
                            {{ if eq .Protocol "grpc" }}
                              <div><strong>Request:</strong> {{ .Request.GRPCService }}/{{ .Request.GRPCMethod }}</div>
                              {{ if .Request.Body }}<pre class="code-block">{{ .Request.Body }}</pre>{{ end }}
                              <div><strong>Response:</strong> status {{ .Response.GRPCStatus }}</div>
                              {{ if .Response.Body }}<pre class="code-block">{{ .Response.Body }}</pre>{{ end }}
                            {{ else }}
                              <div><strong>Request:</strong> {{ .Request.Method }} {{ .Request.URL }}</div>
                              {{ if .Request.Body }}<pre class="code-block">{{ .Request.Body }}</pre>{{ end }}
                              <div><strong>Response:</strong> {{ .Response.Status }}</div>
                              {{ if .Response.Body }}<pre class="code-block">{{ .Response.Body }}</pre>{{ end }}
                            {{ end }}
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
                    <summary>{{ . }}</summary>
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
                  {{ $samples := samplesForOp $spec.Interactions . }}
                  <details class="op">
                    <summary>{{ . }}</summary>
                    {{ if $samples }}
                      {{ range $samples }}
                        <div class="sample">
                          <div class="sample-title">Test Case: {{ .TestCase }}</div>
                          <div class="sample-body">
                            {{ if eq .Protocol "grpc" }}
                              <div><strong>Request:</strong> {{ .Request.GRPCService }}/{{ .Request.GRPCMethod }}</div>
                              {{ if .Request.Body }}<pre class="code-block">{{ .Request.Body }}</pre>{{ end }}
                              <div><strong>Response:</strong> status {{ .Response.GRPCStatus }}</div>
                              {{ if .Response.Body }}<pre class="code-block">{{ .Response.Body }}</pre>{{ end }}
                            {{ else }}
                              <div><strong>Request:</strong> {{ .Request.Method }} {{ .Request.URL }}</div>
                              {{ if .Request.Body }}<pre class="code-block">{{ .Request.Body }}</pre>{{ end }}
                              <div><strong>Response:</strong> {{ .Response.Status }}</div>
                              {{ if .Response.Body }}<pre class="code-block">{{ .Response.Body }}</pre>{{ end }}
                            {{ end }}
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
  </main>
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

func buildCoverage(entries []artifactEntry) (SpecCoverageGroup, SpecCoverageGroup, []string) {
	var notes []string
	flows := []artifact.Flow{}
	flowEntries := []flowEntry{}
	assertions := []artifact.Assertion{}
	schemas := []artifact.SchemaRef{}
	flowIndex := map[string]artifact.Flow{}

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
		assertions = append(assertions, entry.Doc.Assertions...)
		schemas = append(schemas, entry.Doc.Schemas...)
	}
	for _, flow := range flows {
		if flow.ID != "" {
			flowIndex[flow.ID] = flow
		}
		if flow.MatchedStubID != "" {
			flowIndex[flow.MatchedStubID] = flow
		}
	}

	providedSchemas, providedNotes := loadSchemas(filterSchemas(schemas, "sut"))
	dependentSchemas, dependentNotes := loadSchemas(filterSchemas(schemas, "fake"))
	notes = append(notes, providedNotes...)
	notes = append(notes, dependentNotes...)

	markCoverage(flows, providedSchemas, "test_to_sut")
	markCoverage(flows, dependentSchemas, "sut_to_mock")

	providedGroup := SpecCoverageGroup{Specs: exportSpecs(providedSchemas, true)}
	dependentGroup := SpecCoverageGroup{Specs: exportSpecs(dependentSchemas, false)}

	providedInteractions := buildOperationSamples(flowEntries, providedSchemas, "test_to_sut")
	dependentInteractions := buildOperationSamples(flowEntries, dependentSchemas, "sut_to_mock")
	for i := range providedGroup.Specs {
		specID := providedGroup.Specs[i].SchemaID
		if ops, ok := providedInteractions[specID]; ok {
			providedGroup.Specs[i].Interactions = ops
		}
	}
	for i := range dependentGroup.Specs {
		specID := dependentGroup.Specs[i].SchemaID
		if ops, ok := dependentInteractions[specID]; ok {
			dependentGroup.Specs[i].Interactions = ops
		}
	}

	providedGroup.Exercised = uniqueSorted(collectOps(providedSchemas, "exercised"))
	providedGroup.NotCovered = uniqueSorted(collectOps(providedSchemas, "not_covered"))

	dependentGroup.Faked = uniqueSorted(collectOps(dependentSchemas, "faked"))
	dependentGroup.AssertedFields = buildAssertedFields(assertions, "sut_to_mock")
	assertedBySpec, assertedNotes := buildAssertedFieldsBySpec(assertions, flowIndex, dependentSchemas, "sut_to_mock")
	notes = append(notes, assertedNotes...)
	if len(assertedBySpec) > 0 {
		for i := range dependentGroup.Specs {
			specID := dependentGroup.Specs[i].SchemaID
			if ops, ok := assertedBySpec[specID]; ok {
				dependentGroup.Specs[i].AssertedByOperation = ops
			}
		}
	}

	if len(dependentGroup.AssertedFields) == 0 {
		notes = append(notes, "No field-level assertions were recorded for sut_to_mock traffic.")
	}
	return providedGroup, dependentGroup, notes
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
		}
		if strings.EqualFold(schema.Type, "protobuf") {
			if spec, err := loadProtoSpec(schema); err == nil {
				entry.Proto = spec
				entry.Operations = protoOperations(spec)
			} else {
				notes = append(notes, fmt.Sprintf("Protobuf schema %q failed to load: %v", schema.ID, err))
			}
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
			if schema.Proto != nil && flow.Protocol == "grpc" {
				if op := grpcOperationFromFlow(flow); op != "" {
					if !containsOp(schema.Operations, op) {
						continue
					}
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

func exportSpecs(schemas []*loadedSchema, includeNotCovered bool) []SpecCoverage {
	var specs []SpecCoverage
	for _, schema := range schemas {
		coverage := SpecCoverage{
			SchemaID:  schema.Ref.ID,
			Role:      schema.Ref.Role,
			Type:      schema.Ref.Type,
			Source:    schema.Source,
			Exercised: uniqueSorted(setToList(schema.Exercised)),
			Faked:     uniqueSorted(setToList(schema.Faked)),
		}
		if includeNotCovered {
			coverage.NotCovered = uniqueSorted(diffOps(schema.Operations, schema.Exercised))
		}
		specs = append(specs, coverage)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].SchemaID < specs[j].SchemaID })
	return specs
}

func collectOps(schemas []*loadedSchema, kind string) []string {
	var ops []string
	for _, schema := range schemas {
		switch kind {
		case "exercised":
			ops = append(ops, setToList(schema.Exercised)...)
		case "faked":
			ops = append(ops, setToList(schema.Faked)...)
		case "not_covered":
			ops = append(ops, diffOps(schema.Operations, schema.Exercised)...)
		}
	}
	return ops
}

func buildAssertedFields(assertions []artifact.Assertion, channel string) []AssertedField {
	var fields []AssertedField
	for _, assertion := range assertions {
		if assertion.Channel != channel {
			continue
		}
		fields = append(fields, AssertedField{
			FlowID:   assertion.FlowID,
			Channel:  assertion.Channel,
			Phase:    assertion.Phase,
			PathLang: assertion.PathLang,
			Field:    assertion.Field,
			Operator: assertion.Operator,
		})
	}
	return fields
}

func buildOperationSamples(entries []flowEntry, schemas []*loadedSchema, channel string) map[string][]OperationSamples {
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
			case schema.Proto != nil && flow.Protocol == "grpc":
				op := grpcOperationFromFlow(flow)
				if op == "" {
					continue
				}
				if !containsOp(schema.Operations, op) {
					continue
				}
				raw = addSample(raw, schema.Ref.ID, op, flowSampleFromFlow(entry))
			}
		}
	}
	return normalizeOperationSamples(raw)
}

func addSample(target map[string]map[string][]FlowSample, schemaID, operation string, sample FlowSample) map[string]map[string][]FlowSample {
	if _, ok := target[schemaID]; !ok {
		target[schemaID] = map[string][]FlowSample{}
	}
	target[schemaID][operation] = append(target[schemaID][operation], sample)
	return target
}

func normalizeOperationSamples(raw map[string]map[string][]FlowSample) map[string][]OperationSamples {
	out := map[string][]OperationSamples{}
	for schemaID, opMap := range raw {
		var ops []OperationSamples
		for op, samples := range opMap {
			sort.Slice(samples, func(i, j int) bool {
				if samples[i].TestCase == samples[j].TestCase {
					return samples[i].FlowID < samples[j].FlowID
				}
				return samples[i].TestCase < samples[j].TestCase
			})
			ops = append(ops, OperationSamples{Operation: op, Samples: samples})
		}
		sort.Slice(ops, func(i, j int) bool { return ops[i].Operation < ops[j].Operation })
		out[schemaID] = ops
	}
	return out
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
		return sample
	}
	if flow.Protocol == "grpc" {
		if flow.Request.GRPC != nil {
			sample.Request = grpcMessageSample(flow.GRPC, flow.Request.GRPC)
		} else {
			sample.Request = grpcMessageSample(flow.GRPC, nil)
		}
		if flow.Response.GRPC != nil {
			sample.Response = grpcMessageSample(flow.GRPC, flow.Response.GRPC)
		} else {
			sample.Response = grpcMessageSample(flow.GRPC, nil)
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

func grpcMessageSample(meta *artifact.GrpcMeta, msg *artifact.GrpcMessage) InteractionMessage {
	out := InteractionMessage{}
	if meta != nil {
		out.GRPCService = meta.Service
		out.GRPCMethod = meta.Method
		out.GRPCStatus = meta.Status
		out.GRPCMessage = meta.StatusMessage
	}
	if msg != nil {
		out.ContentType = msg.ContentType
		out.Headers = msg.Headers
		out.Body = bodyPreview(msg.Body)
	}
	return out
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

func buildAssertedFieldsBySpec(assertions []artifact.Assertion, flows map[string]artifact.Flow, schemas []*loadedSchema, channel string) (map[string][]OperationAssertions, []string) {
	out := map[string]map[string][]AssertedField{}
	var notes []string
	if len(assertions) == 0 || len(schemas) == 0 {
		return map[string][]OperationAssertions{}, notes
	}
	for _, assertion := range assertions {
		if assertion.Channel != channel {
			continue
		}
		if assertion.FlowID == "" {
			notes = append(notes, fmt.Sprintf("Assertion %q missing flow_id; cannot map to operation.", assertion.ID))
			continue
		}
		flow, ok := flows[assertion.FlowID]
		if !ok {
			notes = append(notes, fmt.Sprintf("Assertion %q references unknown flow %q.", assertion.ID, assertion.FlowID))
			continue
		}
		for _, schema := range schemas {
			switch {
			case schema.OpenAPI != nil && flow.Protocol == "http":
				if op, ok := openapiOperationFromFlow(flow, schema.OpenAPI); ok {
					if !containsOp(schema.Operations, op) {
						continue
					}
					out = addOperationAssertion(out, schema.Ref.ID, op, assertion)
				}
			case schema.Proto != nil && flow.Protocol == "grpc":
				op := grpcOperationFromFlow(flow)
				if op == "" {
					continue
				}
				if !containsOp(schema.Operations, op) {
					continue
				}
				out = addOperationAssertion(out, schema.Ref.ID, op, assertion)
			}
		}
	}
	return normalizeOperationAssertions(out), notes
}

func addOperationAssertion(target map[string]map[string][]AssertedField, schemaID, operation string, assertion artifact.Assertion) map[string]map[string][]AssertedField {
	if _, ok := target[schemaID]; !ok {
		target[schemaID] = map[string][]AssertedField{}
	}
	target[schemaID][operation] = append(target[schemaID][operation], AssertedField{
		FlowID:   assertion.FlowID,
		Channel:  assertion.Channel,
		Phase:    assertion.Phase,
		PathLang: assertion.PathLang,
		Field:    assertion.Field,
		Operator: assertion.Operator,
	})
	return target
}

func normalizeOperationAssertions(raw map[string]map[string][]AssertedField) map[string][]OperationAssertions {
	out := map[string][]OperationAssertions{}
	for schemaID, opMap := range raw {
		var ops []OperationAssertions
		for op, fields := range opMap {
			sort.Slice(fields, func(i, j int) bool {
				if fields[i].Field == fields[j].Field {
					return fields[i].FlowID < fields[j].FlowID
				}
				return fields[i].Field < fields[j].Field
			})
			ops = append(ops, OperationAssertions{Operation: op, Fields: fields})
		}
		sort.Slice(ops, func(i, j int) bool { return ops[i].Operation < ops[j].Operation })
		out[schemaID] = ops
	}
	return out
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

func grpcOperationFromFlow(flow artifact.Flow) string {
	if flow.GRPC == nil {
		return ""
	}
	service := flow.GRPC.Service
	method := flow.GRPC.Method
	if service == "" || method == "" {
		return ""
	}
	return service + "/" + method
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

func protoOperations(spec *protoSpec) []string {
	if spec == nil {
		return nil
	}
	var ops []string
	for _, service := range spec.Services {
		for _, method := range service.GetMethods() {
			ops = append(ops, service.GetFullyQualifiedName()+"/"+method.GetName())
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

func loadProtoSpec(ref artifact.SchemaRef) (*protoSpec, error) {
	path := strings.TrimSpace(ref.Source.Path)
	urlStr := strings.TrimSpace(ref.Source.URL)
	content := strings.TrimSpace(ref.Source.Content)
	format := strings.ToLower(strings.TrimSpace(ref.Source.Format))
	if format == "" {
		format = inferProtoFormat(path, urlStr)
	}

	cleanup := func() {}
	var err error

	switch strings.ToLower(ref.Source.Type) {
	case "file":
		if path == "" {
			return nil, errors.New("protobuf schema path required")
		}
		path = resolvePath(path)
		if format == "descriptor" {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, readErr
			}
			return loadProtoDescriptor(data)
		}
	case "url":
		if urlStr == "" {
			return nil, errors.New("protobuf schema url required")
		}
		path, cleanup, err = downloadToTemp(urlStr, "schema-*")
		if err != nil {
			return nil, err
		}
		if format == "descriptor" {
			defer cleanup()
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, readErr
			}
			return loadProtoDescriptor(data)
		}
	case "inline":
		if content == "" {
			return nil, errors.New("protobuf schema content required")
		}
		if format == "descriptor" {
			raw, decodeErr := decodeBase64(content)
			if decodeErr != nil {
				return nil, errors.New("descriptor content must be base64")
			}
			return loadProtoDescriptor(raw)
		}
		path, cleanup, err = writeTempContent([]byte(content), "schema-*.proto")
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unsupported protobuf source type")
	}
	defer cleanup()

	importPaths := resolveImportPaths(ref.Source.ImportPaths)
	importPaths = append([]string{filepath.Dir(path)}, importPaths...)
	parser := protoparse.Parser{ImportPaths: importPaths}
	files, err := parser.ParseFiles(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	services := map[string]*desc.ServiceDescriptor{}
	for _, file := range files {
		for _, service := range file.GetServices() {
			services[service.GetFullyQualifiedName()] = service
		}
	}
	if len(services) == 0 {
		return nil, errors.New("no services found in proto schema")
	}
	return &protoSpec{Services: services}, nil
}

func loadProtoDescriptor(data []byte) (*protoSpec, error) {
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, set); err != nil {
		return nil, fmt.Errorf("descriptor set decode failed: %w", err)
	}
	files, err := desc.CreateFileDescriptorsFromSet(set)
	if err != nil {
		return nil, err
	}
	services := map[string]*desc.ServiceDescriptor{}
	for _, file := range files {
		for _, service := range file.GetServices() {
			services[service.GetFullyQualifiedName()] = service
		}
	}
	if len(services) == 0 {
		return nil, errors.New("no services found in descriptor set")
	}
	return &protoSpec{Services: services}, nil
}

func downloadToTemp(urlStr, pattern string) (string, func(), error) {
	resp, err := http.Get(urlStr)
	if err != nil {
		return "", func() {}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", func() {}, fmt.Errorf("download failed: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", func() {}, err
	}
	return writeTempContent(data, pattern)
}

func writeTempContent(data []byte, pattern string) (string, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", func() {}, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	return file.Name(), cleanup, nil
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

func resolveImportPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if filepath.IsAbs(path) {
			out = append(out, path)
		} else {
			out = append(out, resolvePath(path))
		}
	}
	return out
}

func inferProtoFormat(path, urlStr string) string {
	candidate := path
	if candidate == "" {
		candidate = urlStr
	}
	candidate = strings.ToLower(candidate)
	if strings.HasSuffix(candidate, ".desc") || strings.HasSuffix(candidate, ".pb") || strings.HasSuffix(candidate, ".protoset") || strings.HasSuffix(candidate, ".bin") {
		return "descriptor"
	}
	return "proto"
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

func decodeBase64(raw string) ([]byte, error) {
	buf := make([]byte, len(raw))
	decodedLen, err := base64.StdEncoding.Decode(buf, []byte(raw))
	if err != nil {
		return nil, err
	}
	return buf[:decodedLen], nil
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
