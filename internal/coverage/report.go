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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"github.com/mattlock83/wiretap/internal/version"

	"gopkg.in/yaml.v3"
)

const ReportVersion = "1.0"

// Report envelopes and raw capture formats evolve independently. The recording
// remains authoritative for every body preview and comparison observation.
type CaptureSource struct {
	ProviderMappings map[string]bundle.ProviderRef `json:"provider_mappings,omitempty" yaml:"provider_mappings,omitempty"`
	FormatVersion    string                        `json:"format_version" yaml:"format_version"`
	RunID            string                        `json:"run_id" yaml:"run_id"`
	Revision         uint64                        `json:"revision" yaml:"revision"`
	State            string                        `json:"state" yaml:"state"`
	Producer         *version.Producer             `json:"producer,omitempty" yaml:"producer,omitempty"`
	Application      *bundle.Application           `json:"application,omitempty" yaml:"application,omitempty"`
	TestSource       map[string]any                `json:"test_source,omitempty" yaml:"test_source,omitempty"`
	SUTSource        map[string]any                `json:"sut_source,omitempty" yaml:"sut_source,omitempty"`
}
type Report struct {
	ReportVersion string                  `json:"report_version" yaml:"report_version"`
	Producer      version.Producer        `json:"producer" yaml:"producer"`
	Capture       *CaptureSource          `json:"capture" yaml:"capture"`
	Unmeasured    []UnmeasuredInteraction `json:"unmeasured_interactions,omitempty" yaml:"unmeasured_interactions,omitempty"`
	Contracts     *ContractCoverage       `json:"contract_coverage,omitempty" yaml:"contract_coverage,omitempty"`
	GeneratedAt   time.Time               `json:"generated_at" yaml:"generated_at"`
	SUTName       string                  `json:"sut_name" yaml:"sut_name"`
	ArtifactCount int                     `json:"artifact_count" yaml:"artifact_count"`
	Artifacts     []string                `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	Provided      SpecCoverageGroup       `json:"provided" yaml:"provided"`
	Dependent     SpecCoverageGroup       `json:"dependent" yaml:"dependent"`
	TestCases     []TestCaseCoverage      `json:"testcases,omitempty" yaml:"testcases,omitempty"`
	Notes         []string                `json:"notes,omitempty" yaml:"notes,omitempty"`

	artifactEntries []artifactEntry
	explicitSUTName bool
}

// UnmeasuredInteraction preserves an explicit gap when the recording format
// supports an exchange that the current measurement engine cannot evaluate.
type UnmeasuredInteraction struct {
	AttemptID string `json:"attempt_id" yaml:"attempt_id"`
	FlowID    string `json:"flow_id" yaml:"flow_id"`
	Protocol  string `json:"protocol" yaml:"protocol"`
	Kind      string `json:"kind" yaml:"kind"`
	Reason    string `json:"reason" yaml:"reason"`
}

func unmeasuredInteractions(captures []*artifact.Artifact) []UnmeasuredInteraction {
	var out []UnmeasuredInteraction
	for _, capture := range captures {
		for _, flow := range capture.Traffic {
			if flow.Interaction != nil {
				out = append(out, UnmeasuredInteraction{AttemptID: capture.TestCase.ID, FlowID: flow.ID, Protocol: flow.Protocol, Kind: flow.Interaction.Kind, Reason: "Event interaction coverage is not supported; retained events are observations, not measured contract coverage."})
			}
		}
	}
	return out
}

type SpecCoverageGroup struct {
	Specs []SpecCoverage `json:"specs" yaml:"specs"`
}

type SpecCoverage struct {
	Protocol         string             `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	DenominatorKnown bool               `json:"denominator_known" yaml:"denominator_known"`
	SchemaSet        string             `json:"schema_set,omitempty" yaml:"schema_set,omitempty"`
	SchemaID         string             `json:"schema_id" yaml:"schema_id"`
	Role             string             `json:"role" yaml:"role"`
	AssetID          string             `json:"asset_id,omitempty" yaml:"asset_id,omitempty"`
	AssetName        string             `json:"asset_name,omitempty" yaml:"asset_name,omitempty"`
	AssetSHA         string             `json:"asset_sha,omitempty" yaml:"asset_sha,omitempty"`
	Type             string             `json:"type" yaml:"type"`
	Source           string             `json:"source" yaml:"source"`
	Exercised        []OperationSamples `json:"exercised,omitempty" yaml:"exercised,omitempty"`
	NotCovered       []OperationSamples `json:"not_covered,omitempty" yaml:"not_covered,omitempty"`
}

type OperationSamples struct {
	Operation string       `json:"operation" yaml:"operation"`
	Samples   []FlowSample `json:"samples,omitempty" yaml:"samples,omitempty"`
}

type TestCaseCoverage struct {
	Attempts  []artifact.TestCase    `json:"attempts,omitempty" yaml:"attempts,omitempty"`
	Name      string                 `json:"name" yaml:"name"`
	Traffic   []FlowSample           `json:"traffic,omitempty" yaml:"traffic,omitempty"`
	Provided  []SpecOperationSamples `json:"provided,omitempty" yaml:"provided,omitempty"`
	Dependent []SpecOperationSamples `json:"dependent,omitempty" yaml:"dependent,omitempty"`
}

type SpecOperationSamples struct {
	SchemaID   string             `json:"schema_id" yaml:"schema_id"`
	SchemaSet  string             `json:"schema_set,omitempty" yaml:"schema_set,omitempty"`
	AssetID    string             `json:"asset_id,omitempty" yaml:"asset_id,omitempty"`
	Type       string             `json:"type" yaml:"type"`
	Source     string             `json:"source" yaml:"source"`
	Operations []OperationSamples `json:"operations,omitempty" yaml:"operations,omitempty"`
}

type FlowSample struct {
	TestIdentity       *artifact.TestIdentity `json:"test_identity,omitempty" yaml:"test_identity,omitempty"`
	RedactionRules     []artifact.Redaction   `json:"redaction_rules,omitempty" yaml:"redaction_rules,omitempty"`
	GRPCEvents         []artifact.GrpcEvent   `json:"grpc_events,omitempty" yaml:"grpc_events,omitempty"`
	Interaction        *artifact.Interaction  `json:"interaction,omitempty" yaml:"interaction,omitempty"`
	CaptureIncomplete  bool                   `json:"capture_incomplete,omitempty" yaml:"capture_incomplete,omitempty"`
	RequestSpanID      string                 `json:"request_span_id,omitempty" yaml:"request_span_id,omitempty"`
	TraceContextSource string                 `json:"trace_context_source,omitempty" yaml:"trace_context_source,omitempty"`
	TestCorrelation    string                 `json:"test_correlation,omitempty" yaml:"test_correlation,omitempty"`
	ParentCorrelation  string                 `json:"parent_correlation,omitempty" yaml:"parent_correlation,omitempty"`
	TestCaseID         string                 `json:"testcase_id,omitempty" yaml:"testcase_id,omitempty"`
	TestTraceID        string                 `json:"test_trace_id,omitempty" yaml:"test_trace_id,omitempty"`
	RootSpanID         string                 `json:"root_span_id,omitempty" yaml:"root_span_id,omitempty"`

	TestCase       string             `json:"testcase" yaml:"testcase"`
	TestStatus     string             `json:"test_status,omitempty" yaml:"test_status,omitempty"`
	FlowID         string             `json:"flow_id,omitempty" yaml:"flow_id,omitempty"`
	Protocol       string             `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	Channel        string             `json:"channel,omitempty" yaml:"channel,omitempty"`
	AssetID        string             `json:"asset_id,omitempty" yaml:"asset_id,omitempty"`
	TraceID        string             `json:"trace_id,omitempty" yaml:"trace_id,omitempty"`
	ParentFlowID   string             `json:"parent_flow_id,omitempty" yaml:"parent_flow_id,omitempty"`
	Fingerprint    string             `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`
	TransportError string             `json:"transport_error,omitempty" yaml:"transport_error,omitempty"`
	Request        InteractionMessage `json:"request,omitempty" yaml:"request,omitempty"`
	Response       InteractionMessage `json:"response,omitempty" yaml:"response,omitempty"`
}

type BodyAvailability struct {
	Present         bool   `json:"present" yaml:"present"`
	Omitted         bool   `json:"omitted" yaml:"omitted"`
	Redacted        bool   `json:"redacted" yaml:"redacted"`
	Encoding        string `json:"encoding,omitempty" yaml:"encoding,omitempty"`
	ContentEncoding string `json:"content_encoding,omitempty" yaml:"content_encoding,omitempty"`
	RetainedBytes   int    `json:"retained_bytes" yaml:"retained_bytes"`
	OriginalBytes   int64  `json:"original_bytes" yaml:"original_bytes"`
	Truncated       bool   `json:"truncated" yaml:"truncated"`
}
type InteractionMessage struct {
	BodyInfo      *BodyAvailability           `json:"body_info,omitempty" yaml:"body_info,omitempty"`
	StreamKind    string                      `json:"stream_kind,omitempty" yaml:"stream_kind,omitempty"`
	Observation   *artifact.GrpcObservation   `json:"observation,omitempty" yaml:"observation,omitempty"`
	StatusDetails *artifact.GrpcStatusDetails `json:"status_details,omitempty" yaml:"status_details,omitempty"`
	Procedure     string                      `json:"procedure,omitempty" yaml:"procedure,omitempty"`
	GRPCStatus    *int                        `json:"grpc_status,omitempty" yaml:"grpc_status,omitempty"`
	HTTPStatus    int                         `json:"http_status,omitempty" yaml:"http_status,omitempty"`
	Messages      []artifact.GrpcPayload      `json:"messages,omitempty" yaml:"messages,omitempty"`
	Method        string                      `json:"method,omitempty" yaml:"method,omitempty"`
	URL           string                      `json:"url,omitempty" yaml:"url,omitempty"`
	Status        int                         `json:"status,omitempty" yaml:"status,omitempty"`
	Headers       artifact.Headers            `json:"headers,omitempty" yaml:"headers,omitempty"`
	Trailers      artifact.Headers            `json:"trailers,omitempty" yaml:"trailers,omitempty"`
	Body          string                      `json:"body,omitempty" yaml:"body,omitempty"`
	ArtifactBody  *artifact.Body              `json:"-" yaml:"-"`
}

type loadedSchema struct {
	Ref        artifact.SchemaRef
	Source     string
	Operations []string
	OpenAPI    *openapiSpec
	GRPC       *grpccontract.Contract
	Exercised  map[string]struct{}
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
	report.Unmeasured = unmeasuredInteractions(captures)
	for _, item := range report.Unmeasured {
		report.Notes = append(report.Notes, fmt.Sprintf("Attempt %s, interaction %s (%s %s): %s", item.AttemptID, item.FlowID, item.Protocol, item.Kind, item.Reason))
	}
	return report, nil
}

func generateFromEntries(entries []artifactEntry, extraSchemas []artifact.SchemaRef, sutName string, includeArtifacts bool, generatedAt time.Time) (*Report, error) {
	explicitSUTName := strings.TrimSpace(sutName) != ""
	if sutName == "" {
		sutName = inferSUTName(entries)
	}

	provided, dependent, testcases, notes := buildCoverageWithSchemas(entries, extraSchemas)

	report := &Report{ReportVersion: ReportVersion, Producer: version.Current(),
		GeneratedAt:     generatedAt,
		SUTName:         sutName,
		ArtifactCount:   len(entries),
		Provided:        provided,
		Dependent:       dependent,
		TestCases:       testcases,
		Notes:           notes,
		artifactEntries: entries,
		explicitSUTName: explicitSUTName,
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
	case "yaml":
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

func buildCoverage(entries []artifactEntry) (SpecCoverageGroup, SpecCoverageGroup, []TestCaseCoverage, []string) {
	return buildCoverageWithSchemas(entries, nil)
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
		for _, flow := range entry.Doc.Traffic {
			keys := make(map[string]bool, len(entry.Doc.Schemas))
			for _, schema := range entry.Doc.Schemas {
				keys[schemaKey(schema)] = true
			}
			flowEntries = append(flowEntries, flowEntry{RedactionRules: entry.Doc.Redactions, Case: entry.Doc.TestCase, Flow: flow, SchemaKeys: keys, TestCase: tcName, Status: entry.Doc.TestCase.Status, CaptureIncomplete: entry.Doc.CaptureIncomplete})
		}
		schemas = append(schemas, entry.Doc.Schemas...)
	}

	providedSchemas, providedNotes := loadSchemas(filterSchemas(schemas, "sut"))
	notes = append(notes, providedNotes...)
	dependentSchemas, dependentNotes := loadSchemas(filterSchemas(schemas, "dependency"))
	notes = append(notes, dependentNotes...)

	markCoverage(flowEntries, providedSchemas, "test_to_sut")
	markCoverage(flowEntries, dependentSchemas, "sut_to_dependency")

	providedSamples := buildOperationSamples(flowEntries, providedSchemas, "test_to_sut")
	dependentSamples := buildOperationSamples(flowEntries, dependentSchemas, "sut_to_dependency")

	providedGroup := SpecCoverageGroup{Specs: exportSpecs(providedSchemas, true, providedSamples)}
	dependentGroup := SpecCoverageGroup{Specs: exportSpecs(dependentSchemas, true, dependentSamples)}
	testcases := buildTestCaseCoverage(flowEntries, providedSchemas, dependentSchemas)
	// Retain attempts with no traffic (e.g. early failure or drain timeout).
	for _, entry := range entries {
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
	if flow.Interaction != nil {
		return false
	}
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
			Ref:       schema,
			Source:    schemaSourceLabel(schema),
			Exercised: map[string]struct{}{},
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
		} else if strings.EqualFold(schema.Type, "protobuf") {
			if raw, err := os.ReadFile(schema.Source.Path); err == nil {
				if contract, e := grpccontract.Parse(raw, schema.Services); e == nil {
					entry.GRPC = contract
					for method := range contract.Methods {
						entry.Operations = append(entry.Operations, method)
					}
					sort.Strings(entry.Operations)
				} else {
					notes = append(notes, fmt.Sprintf("Protobuf schema %q failed to load: %v", schema.ID, e))
				}
			} else {
				notes = append(notes, fmt.Sprintf("Protobuf schema %q failed to read: %v", schema.ID, err))
			}
		} else {
			notes = append(notes, fmt.Sprintf("Schema %q has unsupported measurement type %q; only OpenAPI and Protobuf RPC coverage are supported. Its coverage denominator is unknown.", schema.ID, schema.Type))
		}
		seen[key] = entry
	}
	var out []*loadedSchema
	for _, entry := range seen {
		out = append(out, entry)
	}
	return out, notes
}

func markCoverage(entries []flowEntry, schemas []*loadedSchema, channel string) {
	if len(schemas) == 0 {
		return
	}
	for _, entry := range entries {
		flow := entry.Flow
		if flow.Channel != channel {
			continue
		}
		for _, schema := range schemas {
			if !entry.SchemaKeys[schemaKey(schema.Ref)] {
				continue
			}
			if !flowMatchesSchemaBoundary(flow, schema.Ref) {
				continue
			}
			if schema.Ref.Source.Type == "omitted" {
				if op := observedOperation(flow); op != "" {
					schema.Exercised[op] = struct{}{}
				}
			} else if schema.OpenAPI != nil && flow.Protocol == "http" {
				if op, ok := openapiOperationFromFlow(flow, schema.OpenAPI); ok {
					schema.Exercised[op] = struct{}{}
				}
			} else if schema.GRPC != nil && flow.Protocol == "grpc" && grpcCoverageEligible(entry, schema) {
				schema.Exercised[flow.Request.GRPC.Procedure] = struct{}{}
			}
		}
	}
}

func exportSpecs(schemas []*loadedSchema, includeNotCovered bool, samples map[string]map[string][]FlowSample) []SpecCoverage {
	var specs []SpecCoverage
	for _, schema := range schemas {
		sampleMap := samples[schemaKey(schema.Ref)]
		exercisedOps := uniqueSorted(setToList(schema.Exercised))
		notCoveredOps := []string{}
		if includeNotCovered {
			notCoveredOps = uniqueSorted(diffOps(schema.Operations, schema.Exercised))
		}
		coverage := SpecCoverage{
			Protocol:         schema.Ref.Protocol,
			DenominatorKnown: schema.OpenAPI != nil || schema.GRPC != nil,
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
			coverage.Exercised = operationSamplesList(exercisedOps, sampleMap)
		}
		if len(notCoveredOps) > 0 {
			coverage.NotCovered = operationSamplesList(notCoveredOps, sampleMap)
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

func buildOperationSamples(entries []flowEntry, schemas []*loadedSchema, channel string) map[string]map[string][]FlowSample {
	raw := map[string]map[string][]FlowSample{}
	for _, entry := range entries {
		flow := entry.Flow
		if flow.Channel != channel {
			continue
		}
		for _, schema := range schemas {
			if !entry.SchemaKeys[schemaKey(schema.Ref)] {
				continue
			}
			if !flowMatchesSchemaBoundary(flow, schema.Ref) {
				continue
			}
			switch {
			case schema.GRPC != nil && flow.Protocol == "grpc" && flow.Request.GRPC != nil && schema.GRPC.Methods[flow.Request.GRPC.Procedure] != nil:
				raw = addSample(raw, schemaKey(schema.Ref), flow.Request.GRPC.Procedure, flowSampleFromFlow(entry))
			case schema.Ref.Source.Type == "omitted":
				if op := observedOperation(flow); op != "" {
					raw = addSample(raw, schemaKey(schema.Ref), op, flowSampleFromFlow(entry))
				}
			case schema.OpenAPI != nil && flow.Protocol == "http":
				op, ok := openapiOperationFromFlow(flow, schema.OpenAPI)
				if !ok {
					continue
				}
				if !containsOp(schema.Operations, op) {
					continue
				}
				raw = addSample(raw, schemaKey(schema.Ref), op, flowSampleFromFlow(entry))
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
		tc.Traffic = append(tc.Traffic, flowSampleFromFlow(entry))

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
			if schema.GRPC != nil && flow.Protocol == "grpc" && flow.Request.GRPC != nil && schema.GRPC.Methods[flow.Request.GRPC.Procedure] != nil {
				op = flow.Request.GRPC.Procedure
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
		Interaction:        flow.Interaction,
		GRPCEvents:         flow.GRPCEvents,
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
		Fingerprint:    flow.Fingerprint,
		TransportError: flow.TransportError,
	}
	if flow.Interaction != nil {
		sample.CaptureIncomplete = sample.CaptureIncomplete || flow.Interaction.CaptureState != "complete"
	}
	if flow.Protocol == "http" {
		if flow.Request.HTTP != nil {
			sample.Request = httpMessageSample(flow.Request.HTTP)
		}
		if flow.Response.HTTP != nil {
			sample.Response = httpMessageSample(flow.Response.HTTP)
		}
	}
	if flow.Protocol == "grpc" {
		if flow.Request.GRPC != nil {
			sample.Request = grpcMessageSample(flow.Request.GRPC)
		}
		if flow.Response.GRPC != nil {
			sample.Response = grpcMessageSample(flow.Response.GRPC)
		}
	}
	return sample
}

func grpcMessageSample(msg *artifact.GrpcMessage) InteractionMessage {
	if msg == nil {
		return InteractionMessage{}
	}
	result := InteractionMessage{StreamKind: msg.StreamKind, Observation: msg.Observation, StatusDetails: msg.StatusDetails, Procedure: msg.Procedure, Method: msg.Procedure, URL: msg.Procedure, Headers: msg.Metadata, Trailers: msg.Trailers, Messages: msg.Messages, HTTPStatus: msg.HTTPStatus}
	if msg.HTTPStatus != 0 {
		status := msg.StatusCode
		result.GRPCStatus = &status
		result.Status = msg.StatusCode
	}
	if msg.StreamKind == "unary" && len(msg.Messages) == 1 {
		result.Body = bodyPreview(msg.Messages[0].Body)
	}
	return result
}

func grpcCoverageEligible(entry flowEntry, schema *loadedSchema) bool {
	flow := entry.Flow
	if entry.Status != "pass" || entry.CaptureIncomplete || flow.TransportError != "" || flow.Request.GRPC == nil || flow.Response.GRPC == nil || flow.Request.GRPC.CaptureTruncated || flow.Response.GRPC.CaptureTruncated || flow.Response.GRPC.HTTPStatus != http.StatusOK || flow.Response.GRPC.StatusCode < 0 || flow.Response.GRPC.StatusCode > 16 {
		return false
	}
	method := schema.GRPC.Methods[flow.Request.GRPC.Procedure]
	if method == nil {
		return false
	}
	requireObservation := method.IsStreamingClient() || method.IsStreamingServer()
	if _, err := decodeGRPCDirection(flow.Request.GRPC, method.Input(), schema.GRPC, method.IsStreamingClient(), true, requireObservation); err != nil {
		return false
	}
	if _, err := decodeGRPCDirection(flow.Response.GRPC, method.Output(), schema.GRPC, method.IsStreamingServer(), flow.Response.GRPC.StatusCode == 0, requireObservation); err != nil {
		return false
	}
	return true
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
	if schema.Type == "protobuf" && schema.Source.Path != "" {
		if raw, err := os.ReadFile(schema.Source.Path); err == nil {
			if revision, err := grpccontract.SemanticRevision(raw, schema.Services); err == nil {
				return revision
			}
		}
	}
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
