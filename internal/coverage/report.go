package coverage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/version"
)

const ReportVersion = "1.0"

// Report envelopes and raw capture formats evolve independently. The recording
// remains authoritative for every body preview and comparison observation.
type CaptureSource struct {
	ProviderMappings map[string]bundle.ProviderRef `json:"provider_mappings,omitempty"`
	FormatVersion    string                        `json:"format_version"`
	RunID            string                        `json:"run_id"`
	Revision         uint64                        `json:"revision"`
	State            string                        `json:"state"`
	Producer         *version.Producer             `json:"producer,omitempty"`
	Application      *bundle.Application           `json:"application,omitempty"`
	TestSource       map[string]any                `json:"test_source,omitempty"`
	SUTSource        map[string]any                `json:"sut_source,omitempty"`
}
type Report struct {
	ReportVersion string             `json:"report_version"`
	Producer      version.Producer   `json:"producer"`
	Capture       *CaptureSource     `json:"capture"`
	Contracts     *ContractCoverage  `json:"contract_coverage,omitempty"`
	GeneratedAt   time.Time          `json:"generated_at"`
	SUTName       string             `json:"sut_name"`
	ArtifactCount int                `json:"artifact_count"`
	Artifacts     []string           `json:"artifacts,omitempty"`
	Provided      SpecCoverageGroup  `json:"provided"`
	Dependent     SpecCoverageGroup  `json:"dependent"`
	TestCases     []TestCaseCoverage `json:"testcases,omitempty"`
	Notes         []string           `json:"notes,omitempty"`
}

type SpecCoverageGroup struct {
	Specs []SpecCoverage `json:"specs"`
}

type SpecCoverage struct {
	Protocol         string             `json:"protocol,omitempty"`
	DenominatorKnown bool               `json:"denominator_known"`
	SchemaSet        string             `json:"schema_set,omitempty"`
	SchemaID         string             `json:"schema_id"`
	Role             string             `json:"role"`
	AssetID          string             `json:"asset_id,omitempty"`
	AssetName        string             `json:"asset_name,omitempty"`
	AssetSHA         string             `json:"asset_sha,omitempty"`
	Type             string             `json:"type"`
	Source           string             `json:"source"`
	Exercised        []OperationSamples `json:"exercised,omitempty"`
	NotCovered       []OperationSamples `json:"not_covered,omitempty"`
}

type OperationSamples struct {
	Operation string       `json:"operation"`
	Samples   []FlowSample `json:"samples,omitempty"`
}

type TestCaseCoverage struct {
	Attempts  []artifact.TestCase    `json:"attempts,omitempty"`
	Name      string                 `json:"name"`
	Traffic   []FlowSample           `json:"traffic,omitempty"`
	Provided  []SpecOperationSamples `json:"provided,omitempty"`
	Dependent []SpecOperationSamples `json:"dependent,omitempty"`
}

type SpecOperationSamples struct {
	SchemaID   string             `json:"schema_id"`
	SchemaSet  string             `json:"schema_set,omitempty"`
	AssetID    string             `json:"asset_id,omitempty"`
	Type       string             `json:"type"`
	Source     string             `json:"source"`
	Operations []OperationSamples `json:"operations,omitempty"`
}

type FlowSample struct {
	TestIdentity       *artifact.TestIdentity `json:"test_identity,omitempty"`
	RedactionRules     []artifact.Redaction   `json:"redaction_rules,omitempty"`
	CaptureIncomplete  bool                   `json:"capture_incomplete,omitempty"`
	RequestSpanID      string                 `json:"request_span_id,omitempty"`
	TraceContextSource string                 `json:"trace_context_source,omitempty"`
	TestCorrelation    string                 `json:"test_correlation,omitempty"`
	ParentCorrelation  string                 `json:"parent_correlation,omitempty"`
	TestCaseID         string                 `json:"testcase_id,omitempty"`
	TestTraceID        string                 `json:"test_trace_id,omitempty"`
	RootSpanID         string                 `json:"root_span_id,omitempty"`

	TestCase       string             `json:"testcase"`
	TestStatus     string             `json:"test_status,omitempty"`
	FlowID         string             `json:"flow_id,omitempty"`
	Protocol       string             `json:"protocol,omitempty"`
	Channel        string             `json:"channel,omitempty"`
	AssetID        string             `json:"asset_id,omitempty"`
	TraceID        string             `json:"trace_id,omitempty"`
	ParentFlowID   string             `json:"parent_flow_id,omitempty"`
	TransportError string             `json:"transport_error,omitempty"`
	Request        InteractionMessage `json:"request,omitempty"`
	Response       InteractionMessage `json:"response,omitempty"`
}

type BodyAvailability struct {
	Present         bool   `json:"present"`
	Omitted         bool   `json:"omitted"`
	Redacted        bool   `json:"redacted"`
	Encoding        string `json:"encoding,omitempty"`
	ContentEncoding string `json:"content_encoding,omitempty"`
	RetainedBytes   int    `json:"retained_bytes"`
	OriginalBytes   int64  `json:"original_bytes"`
	Truncated       bool   `json:"truncated"`
}
type InteractionMessage struct {
	BodyInfo     *BodyAvailability `json:"body_info,omitempty"`
	Method       string            `json:"method,omitempty"`
	URL          string            `json:"url,omitempty"`
	Status       int               `json:"status,omitempty"`
	Headers      artifact.Headers  `json:"headers,omitempty"`
	Trailers     artifact.Headers  `json:"trailers,omitempty"`
	Body         string            `json:"body,omitempty"`
	ArtifactBody *artifact.Body    `json:"-"`
}

type loadedSchema struct {
	Ref        artifact.SchemaRef
	Source     string
	Operations []string
	OpenAPI    *openapiSpec
	Samples    map[string][]FlowSample
}

type openapiSpec struct {
	Doc    *openapi3.T
	Router routers.Router
}

type flowEntry struct {
	RedactionRules    []artifact.Redaction
	Case              artifact.TestCase
	Flow              artifact.Flow
	SchemaKeys        map[string]bool
	TestCase          string
	Status            string
	CaptureIncomplete bool
}

// Generate derives coverage from verified bundle evidence.
func Generate(ctx context.Context, bundlePath, sutName string, includeArtifacts bool) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, captures, err := bundle.Load(bundlePath)
	if err != nil {
		return nil, err
	}
	return GenerateBundleSnapshot(bundlePath, run, captures, sutName, includeArtifacts)
}

// GenerateBundleSnapshot derives coverage from one already-verified inventory
// snapshot. View refresh uses this to avoid loading payloads twice.
func GenerateBundleSnapshot(bundlePath string, run *bundle.Run, captures []*artifact.Artifact, sutName string, includeArtifacts bool) (*Report, error) {
	if run == nil {
		return nil, errors.New("bundle inventory is nil")
	}
	if len(run.Attempts) != len(captures) {
		return nil, errors.New("bundle inventory and captures disagree")
	}
	entries := make([]artifactEntry, 0, len(captures))
	if sutName == "" && run.Application != nil {
		sutName = run.Application.Name
	}
	for i, capture := range captures {
		entries = append(entries, artifactEntry{Path: filepath.Join(bundlePath, filepath.FromSlash(run.Attempts[i].Path)), Doc: capture})
	}
	report, err := generateFromEntries(entries, run.Schemas, sutName, includeArtifacts, run.UpdatedAt)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(bundlePath)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	portable := func(value string) string {
		if !filepath.IsAbs(value) {
			return value
		}
		rel, err := filepath.Rel(root, value)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return value
		}
		return filepath.ToSlash(rel)
	}
	for _, group := range []*SpecCoverageGroup{&report.Provided, &report.Dependent} {
		for i := range group.Specs {
			group.Specs[i].Source = portable(group.Specs[i].Source)
		}
	}
	for i := range report.TestCases {
		for j := range report.TestCases[i].Provided {
			report.TestCases[i].Provided[j].Source = portable(report.TestCases[i].Provided[j].Source)
		}
		for j := range report.TestCases[i].Dependent {
			report.TestCases[i].Dependent[j].Source = portable(report.TestCases[i].Dependent[j].Source)
		}
	}
	for i := range report.Artifacts {
		report.Artifacts[i] = portable(report.Artifacts[i])
	}
	report.Capture = &CaptureSource{ProviderMappings: run.ProviderMappings, FormatVersion: run.EvidenceVersion, RunID: run.RunID, Revision: run.Revision, State: run.RunState, Producer: run.Producer, Application: run.Application, TestSource: run.TestSource, SUTSource: run.SUTSource}
	report.Contracts = buildContractCoverage(run, captures)
	return report, nil
}

func generateFromEntries(entries []artifactEntry, extraSchemas []artifact.SchemaRef, sutName string, includeArtifacts bool, generatedAt time.Time) (*Report, error) {
	if sutName == "" {
		sutName = inferSUTName(entries)
	}

	provided, dependent, testcases, notes := buildCoverageWithSchemas(entries, extraSchemas)

	report := &Report{ReportVersion: ReportVersion, Producer: version.Current(),
		GeneratedAt:   generatedAt,
		SUTName:       sutName,
		ArtifactCount: len(entries),
		Provided:      provided,
		Dependent:     dependent,
		TestCases:     testcases,
		Notes:         notes,
	}
	if includeArtifacts {
		for _, entry := range entries {
			report.Artifacts = append(report.Artifacts, entry.Path)
		}
	}
	return report, nil
}

// WriteReport writes the report to the writer in the requested format.
func WriteReport(w io.Writer, report *Report, format string) error {
	if report == nil {
		return errors.New("report is nil")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "text":
		return writeTextReport(w, report)
	case "json", "":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

type artifactEntry struct {
	Path string
	Doc  *artifact.Artifact
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

func buildCoverageWithSchemas(entries []artifactEntry, extraSchemas []artifact.SchemaRef) (SpecCoverageGroup, SpecCoverageGroup, []TestCaseCoverage, []string) {
	var notes []string
	flowEntries := []flowEntry{}
	schemas := append([]artifact.SchemaRef{}, extraSchemas...)

	for _, entry := range entries {
		if entry.Doc == nil {
			continue
		}
		tcName := strings.TrimSpace(entry.Doc.TestCase.Name)
		if tcName == "" {
			tcName = entry.Doc.TestCase.ID
		}
		keys := make(map[string]bool, len(entry.Doc.Schemas))
		for _, schema := range entry.Doc.Schemas {
			keys[schemaKey(schema)] = true
		}
		for _, flow := range entry.Doc.Traffic {
			flowEntries = append(flowEntries, flowEntry{RedactionRules: entry.Doc.Redactions, Case: entry.Doc.TestCase, Flow: flow, SchemaKeys: keys, TestCase: tcName, Status: entry.Doc.TestCase.Status, CaptureIncomplete: entry.Doc.CaptureIncomplete})
		}
		schemas = append(schemas, entry.Doc.Schemas...)
	}

	providedSchemas, providedNotes := loadSchemas(filterSchemas(schemas, "sut"))
	notes = append(notes, providedNotes...)
	dependentSchemas, dependentNotes := loadSchemas(filterSchemas(schemas, "dependency"))
	notes = append(notes, dependentNotes...)

	testcases := indexCoverage(flowEntries, providedSchemas, dependentSchemas)
	providedGroup := SpecCoverageGroup{Specs: exportSpecs(providedSchemas)}
	dependentGroup := SpecCoverageGroup{Specs: exportSpecs(dependentSchemas)}
	// Retain attempts with no traffic (e.g. early failure or drain timeout).
	for _, entry := range entries {
		if entry.Doc == nil {
			continue
		}
		name := strings.TrimSpace(entry.Doc.TestCase.Name)
		if name == "" {
			name = "unknown"
		}
		index := -1
		for i := range testcases {
			if testcases[i].Name == name {
				index = i
				break
			}
		}
		if index == -1 {
			testcases = append(testcases, TestCaseCoverage{Name: name})
			index = len(testcases) - 1
		}
		tc := &testcases[index]
		found := false
		for _, attempt := range tc.Attempts {
			if attempt.ID == entry.Doc.TestCase.ID {
				found = true
				break
			}
		}
		if !found {
			tc.Attempts = append(tc.Attempts, entry.Doc.TestCase)
		}
		for _, diagnostic := range entry.Doc.CaptureDiagnostics {
			notes = append(notes, entry.Doc.TestCase.ID+": "+diagnostic)
		}
	}
	sort.Slice(testcases, func(i, j int) bool { return testcases[i].Name < testcases[j].Name })

	return providedGroup, dependentGroup, testcases, notes
}

func filterSchemas(schemas []artifact.SchemaRef, role string) []artifact.SchemaRef {
	var filtered []artifact.SchemaRef
	for _, schema := range schemas {
		if coverageRole(schema.Role) == coverageRole(role) {
			filtered = append(filtered, schema)
		}
	}
	return filtered
}

func coverageRole(role string) string {
	if strings.EqualFold(role, "sut") || strings.EqualFold(role, "provided") {
		return "provided"
	}
	return strings.ToLower(role)
}

func flowMatchesSchemaBoundary(flow artifact.Flow, schema artifact.SchemaRef) bool {
	if schema.Role == "provided" {
		// Explicit provider boundaries must not inherit the legacy single-SUT
		// association or measure another provider's request.
		return flow.Channel == "test_to_sut" && schema.AssetID != "" && flow.AssetID == schema.AssetID
	}
	return schema.AssetID == "" || flow.AssetID == "" || schema.AssetID == flow.AssetID
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
			Ref:     schema,
			Source:  schemaSourceLabel(schema),
			Samples: map[string][]FlowSample{},
		}
		if schema.Source.Type == "omitted" {
			notes = append(notes, fmt.Sprintf("API %q explicitly omits its OpenAPI schema; validation is skipped and coverage is unknown.", schema.ID))
		} else if strings.EqualFold(schema.Type, "openapi") {
			if spec, err := loadOpenAPISpec(schema); err == nil {
				entry.OpenAPI = spec
				entry.Operations = openapiOperations(spec)
			} else {
				notes = append(notes, fmt.Sprintf("OpenAPI schema %q failed to load: %v", schema.ID, err))
			}
		} else {
			notes = append(notes, fmt.Sprintf("Schema %q has unsupported measurement type %q; only OpenAPI coverage is supported. Its coverage denominator is unknown.", schema.ID, schema.Type))
		}
		seen[key] = entry
	}
	var out []*loadedSchema
	for _, entry := range seen {
		out = append(out, entry)
	}
	return out, notes
}

func exportSpecs(schemas []*loadedSchema) []SpecCoverage {
	var specs []SpecCoverage
	for _, schema := range schemas {
		exercisedOps := sortedMapKeys(schema.Samples)
		notCoveredOps := diffOps(schema.Operations, schema.Samples)
		coverage := SpecCoverage{
			Protocol:         schema.Ref.Protocol,
			DenominatorKnown: schema.OpenAPI != nil,
			SchemaSet:        schemaRevision(schema.Ref),
			SchemaID:         schema.Ref.ID,
			Role:             schema.Ref.Role,
			AssetID:          schema.Ref.AssetID,
			AssetName:        schema.Ref.AssetName,
			AssetSHA:         schema.Ref.AssetSHA,
			Type:             schema.Ref.Type,
			Source:           schema.Source,
		}
		if len(exercisedOps) > 0 {
			coverage.Exercised = operationSamplesList(exercisedOps, schema.Samples)
		}
		if len(notCoveredOps) > 0 {
			coverage.NotCovered = operationSamplesList(notCoveredOps, schema.Samples)
		}
		specs = append(specs, coverage)
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].SchemaID != specs[j].SchemaID {
			return specs[i].SchemaID < specs[j].SchemaID
		}
		if specs[i].Source != specs[j].Source {
			return specs[i].Source < specs[j].Source
		}
		return specs[i].SchemaSet < specs[j].SchemaSet
	})
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

// indexCoverage matches each flow against each applicable schema once. The same
// sample feeds the per-operation inventory and per-test view, so the two views
// cannot disagree about a route, schema revision, or boundary.
func indexCoverage(entries []flowEntry, providedSchemas, dependentSchemas []*loadedSchema) []TestCaseCoverage {
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
		foundAttempt := false
		for _, attempt := range tc.Attempts {
			if attempt.ID == entry.Case.ID {
				foundAttempt = true
				break
			}
		}
		if !foundAttempt && entry.Case.ID != "" {
			tc.Attempts = append(tc.Attempts, entry.Case)
		}
		sample := flowSampleFromFlow(entry)
		tc.Traffic = append(tc.Traffic, sample)

		var schemas []*loadedSchema
		var target *[]SpecOperationSamples
		switch flow.Channel {
		case "test_to_sut":
			schemas, target = providedSchemas, &tc.Provided
		case "sut_to_dependency":
			schemas, target = dependentSchemas, &tc.Dependent
		default:
			continue
		}

		for _, schema := range schemas {
			if !entry.SchemaKeys[schemaKey(schema.Ref)] {
				continue
			}
			if !flowMatchesSchemaBoundary(flow, schema.Ref) {
				continue
			}
			op := ""
			if schema.Ref.Source.Type == "omitted" {
				op = observedOperation(flow)
			}
			if schema.OpenAPI != nil && flow.Protocol == "http" {
				if matched, ok := openapiOperationFromFlow(flow, schema.OpenAPI); ok {
					op = matched
				}
			}
			if op == "" {
				continue
			}

			schema.Samples[op] = append(schema.Samples[op], sample)
			spec := findOrCreateSpec(target, schema)
			spec.Operations = appendOperationSample(spec.Operations, op, sample)
		}
	}

	var out []TestCaseCoverage
	for _, tc := range buckets {
		if len(tc.Traffic) > 1 {
			sortSamples(tc.Traffic)
		}
		sort.Slice(tc.Provided, func(i, j int) bool {
			if tc.Provided[i].SchemaID != tc.Provided[j].SchemaID {
				return tc.Provided[i].SchemaID < tc.Provided[j].SchemaID
			}
			return tc.Provided[i].SchemaSet < tc.Provided[j].SchemaSet
		})
		sort.Slice(tc.Dependent, func(i, j int) bool {
			if tc.Dependent[i].SchemaID != tc.Dependent[j].SchemaID {
				return tc.Dependent[i].SchemaID < tc.Dependent[j].SchemaID
			}
			return tc.Dependent[i].SchemaSet < tc.Dependent[j].SchemaSet
		})
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
		if (*target)[i].SchemaID == schema.Ref.ID && (*target)[i].SchemaSet == schemaRevision(schema.Ref) {
			return &(*target)[i]
		}
	}
	entry := SpecOperationSamples{
		SchemaID:  schema.Ref.ID,
		SchemaSet: schemaRevision(schema.Ref),
		AssetID:   schema.Ref.AssetID,
		Type:      schema.Ref.Type,
		Source:    schema.Source,
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
		CaptureIncomplete:  entry.CaptureIncomplete,
		TestCase:           entry.TestCase,
		TestStatus:         entry.Status,
		FlowID:             flow.ID,
		Protocol:           flow.Protocol,
		Channel:            flow.Channel,
		AssetID:            flow.AssetID,
		TraceID:            flow.TraceID,
		RequestSpanID:      flow.RequestSpanID,
		TraceContextSource: flow.TraceContextSource,
		TestCorrelation:    flow.TestCorrelation,
		ParentCorrelation:  flow.ParentCorrelation,
		RedactionRules:     entry.RedactionRules, TestIdentity: entry.Case.Identity,
		TestCaseID: entry.Case.ID, TestTraceID: entry.Case.TraceID, RootSpanID: entry.Case.RootSpanID,
		ParentFlowID:   flow.ParentFlowID,
		TransportError: flow.TransportError,
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
		Method:       msg.Method,
		URL:          msg.URL,
		Status:       msg.Status,
		Headers:      msg.Headers,
		Trailers:     msg.Trailers,
		Body:         bodyPreview(msg.Body),
		ArtifactBody: msg.Body,
		BodyInfo:     bodyAvailability(msg.Body),
	}
}

func bodyPreview(body *artifact.Body) string {
	if body == nil {
		return ""
	}
	if body.Text != "" {
		return body.Text
	}
	if body.Decoded != nil && body.Decoded.Text != "" {
		return body.Decoded.Text
	}
	if body.Bytes != "" {
		return "[base64]\n" + body.Bytes
	}
	return ""
}

func schemaKey(schema artifact.SchemaRef) string {
	contentHash := sha256.Sum256([]byte(schema.Source.Content))
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s|%x", schema.ID, schema.Role, schema.AssetID, schema.Type, schema.Source.Path, schema.Source.URL, schema.Source.Format, schema.Source.SHA256, schema.AssetSHA, contentHash)
}

func schemaRevision(schema artifact.SchemaRef) string {
	if schema.Source.Path != "" {
		parent := filepath.Base(filepath.Dir(schema.Source.Path))
		if len(parent) == 64 {
			return parent
		}
	}
	if schema.Source.Content != "" {
		sum := sha256.Sum256([]byte(schema.Source.Content))
		return fmt.Sprintf("%x", sum)
	}
	if schema.Source.SHA256 != "" {
		return schema.Source.SHA256
	}
	return schema.AssetSHA
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
		for method := range item.Operations() {
			ops = append(ops, strings.ToUpper(method)+" "+path)
		}
	}
	sort.Strings(ops)
	return ops
}

func loadOpenAPISpec(ref artifact.SchemaRef) (*openapiSpec, error) {
	root, files, err := bundle.OpenAPIFiles(ref)
	if err != nil {
		return nil, err
	}
	doc, err := bundle.ParseOpenAPI(context.Background(), root, files)
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

func diffOps(allOps []string, covered map[string][]FlowSample) []string {
	var out []string
	for _, op := range allOps {
		if _, ok := covered[op]; !ok {
			out = append(out, op)
		}
	}
	return out
}

// Without a schema these are observed concrete paths, never inferred contract operations.
func observedOperation(flow artifact.Flow) string {
	if flow.Protocol != "http" || flow.Request.HTTP == nil {
		return ""
	}
	req := flow.Request.HTTP
	uri, err := url.Parse(req.URL)
	if err != nil || req.Method == "" {
		return ""
	}
	path := uri.EscapedPath()
	if path == "" {
		path = "/"
	}
	return strings.ToUpper(req.Method) + " " + path
}

func bodyAvailability(body *artifact.Body) *BodyAvailability {
	info := &BodyAvailability{}
	if body != nil {
		info.Present = true
		info.Omitted = body.Omitted
		info.Redacted = body.Redacted
		info.Encoding = body.Encoding
		info.ContentEncoding = body.ContentEncoding
		info.RetainedBytes = len(body.Text)
		if body.Bytes != "" {
			if raw, err := base64.StdEncoding.DecodeString(body.Bytes); err == nil {
				info.RetainedBytes = len(raw)
			}
		}
		info.OriginalBytes = body.OriginalBytes
		info.Truncated = body.Truncated
	}
	return info
}
