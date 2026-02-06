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
	SchemaID   string   `json:"schema_id" yaml:"schema_id"`
	Role       string   `json:"role" yaml:"role"`
	Type       string   `json:"type" yaml:"type"`
	Source     string   `json:"source" yaml:"source"`
	Exercised  []string `json:"exercised,omitempty" yaml:"exercised,omitempty"`
	NotCovered []string `json:"not_covered,omitempty" yaml:"not_covered,omitempty"`
	Faked      []string `json:"faked,omitempty" yaml:"faked,omitempty"`
}

type AssertedField struct {
	FlowID   string `json:"flow_id,omitempty" yaml:"flow_id,omitempty"`
	Channel  string `json:"channel,omitempty" yaml:"channel,omitempty"`
	Phase    string `json:"phase,omitempty" yaml:"phase,omitempty"`
	PathLang string `json:"path_lang,omitempty" yaml:"path_lang,omitempty"`
	Field    string `json:"field,omitempty" yaml:"field,omitempty"`
	Operator string `json:"operator,omitempty" yaml:"operator,omitempty"`
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
    .spec {
      border-top: 1px dashed var(--border);
      padding-top: 16px;
      margin-top: 16px;
    }
    .spec h3 {
      margin: 0 0 4px;
      font-size: 16px;
      color: var(--accent);
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
      {{ if .Provided.Exercised }}
      <p class="meta">Exercised: {{ join .Provided.Exercised }}</p>
      {{ end }}
      {{ if .Provided.NotCovered }}
      <p class="meta">Not Covered: {{ join .Provided.NotCovered }}</p>
      {{ end }}
      {{ range .Provided.Specs }}
        <div class="spec">
          <h3>{{ .SchemaID }}</h3>
          <div class="meta">Type: {{ .Type }} · Source: {{ .Source }}</div>
          {{ if .Exercised }}
            <strong>Exercised</strong>
            <ul>
              {{ range .Exercised }}<li>{{ . }}</li>{{ end }}
            </ul>
          {{ end }}
          {{ if .NotCovered }}
            <strong>Not Covered</strong>
            <ul>
              {{ range .NotCovered }}<li>{{ . }}</li>{{ end }}
            </ul>
          {{ end }}
        </div>
      {{ end }}
    </section>

    <section class="panel">
      <h2>Dependent API Specs</h2>
      {{ if .Dependent.Faked }}
      <p class="meta">Faked: {{ join .Dependent.Faked }}</p>
      {{ end }}
      {{ range .Dependent.Specs }}
        <div class="spec">
          <h3>{{ .SchemaID }}</h3>
          <div class="meta">Type: {{ .Type }} · Source: {{ .Source }}</div>
          {{ if .Faked }}
            <strong>Faked</strong>
            <ul>
              {{ range .Faked }}<li>{{ . }}</li>{{ end }}
            </ul>
          {{ end }}
        </div>
      {{ end }}

      {{ if .Dependent.AssertedFields }}
      <div class="spec">
        <h3>Asserted Fields</h3>
        <table>
          <thead>
            <tr>
              <th>Flow</th>
              <th>Channel</th>
              <th>Phase</th>
              <th>Path Lang</th>
              <th>Field</th>
              <th>Operator</th>
            </tr>
          </thead>
          <tbody>
            {{ range .Dependent.AssertedFields }}
              <tr>
                <td>{{ .FlowID }}</td>
                <td>{{ .Channel }}</td>
                <td>{{ .Phase }}</td>
                <td>{{ .PathLang }}</td>
                <td>{{ .Field }}</td>
                <td>{{ .Operator }}</td>
              </tr>
            {{ end }}
          </tbody>
        </table>
      </div>
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
	assertions := []artifact.Assertion{}
	schemas := []artifact.SchemaRef{}

	for _, entry := range entries {
		if entry.Doc == nil {
			continue
		}
		flows = append(flows, entry.Doc.Traffic...)
		assertions = append(assertions, entry.Doc.Assertions...)
		schemas = append(schemas, entry.Doc.Schemas...)
	}

	providedSchemas, providedNotes := loadSchemas(filterSchemas(schemas, "sut"))
	dependentSchemas, dependentNotes := loadSchemas(filterSchemas(schemas, "fake"))
	notes = append(notes, providedNotes...)
	notes = append(notes, dependentNotes...)

	markCoverage(flows, providedSchemas, "test_to_sut")
	markCoverage(flows, dependentSchemas, "sut_to_mock")

	providedGroup := SpecCoverageGroup{Specs: exportSpecs(providedSchemas, true)}
	dependentGroup := SpecCoverageGroup{Specs: exportSpecs(dependentSchemas, false)}

	providedGroup.Exercised = uniqueSorted(collectOps(providedSchemas, "exercised"))
	providedGroup.NotCovered = uniqueSorted(collectOps(providedSchemas, "not_covered"))

	dependentGroup.Faked = uniqueSorted(collectOps(dependentSchemas, "faked"))
	dependentGroup.AssertedFields = buildAssertedFields(assertions, "sut_to_mock")

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
