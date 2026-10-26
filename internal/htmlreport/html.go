// Package htmlreport renders a disposable, self-contained view of Wiretap JSON,
// OpenAPI sources, and Arazzo workflows. It never executes captured content.
package htmlreport

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/coverage"
)

// Document is a supplied source, retained as text for inspection and download.
type Document struct{ Name, Content string }

// Inputs contains all evidence used by the dashboard. No source URLs are fetched.
type Inputs struct {
	Report                  *coverage.Report
	Specs                   []Document
	Arazzo                  *arazzo.Document
	WiretapName, ArazzoName string
	// Original standalone source text is retained for inspection and downloads.
	// Empty values fall back to serialization of the typed evidence above.
	WiretapJSON, ArazzoJSON string
}

//go:embed report.html.tmpl
var reportTemplate string

var page = template.Must(template.New("report").Funcs(template.FuncMap{
	"label": func(s string) string { return strings.ReplaceAll(s, "_", " ") },
	"json":  prettyJSON,
}).Parse(reportTemplate))

// Write renders an offline HTML dashboard. All source values are escaped by
// html/template, including source documents and HTTP message bodies.
func Write(w io.Writer, in Inputs) error {
	if in.Report == nil {
		return errors.New("HTML report requires Wiretap JSON coverage")
	}
	model, err := makePage(in)
	if err != nil {
		return err
	}
	return page.Execute(w, model)
}

type pageModel struct {
	Name, RunID, RunState, GeneratedAt, Producer              string
	Revision                                                  uint64
	Attempts, Passed, Other, Flows, Operations, Gaps, Unknown int
	Provided, Dependent                                       []specView
	Tests                                                     []*attemptView
	Contracts                                                 []contractView
	ContractScope                                             string
	ContractComplete                                          bool
	HasContracts                                              bool
	Workflows                                                 []workflowView
	ArazzoSources                                             []arazzo.Source
	Sources                                                   []sourceView
	Notes                                                     []string
}
type evidenceLink struct{ Name, AttemptID, FlowID, State, Href string }
type specView struct {
	ID, Name, Source, Asset, Protocol, Anchor string
	Known                                     bool
	Observed, Total, Percent                  int
	Operations                                []operationView
}
type operationView struct {
	Name, State, Anchor string
	Samples             []evidenceLink
}
type attemptView struct {
	Anchor, ID, Name, State, Identity, Variant, Ordinal, Trace, Started, Finished string
	Tags                                                                          []string
	Flows                                                                         []flowView
	Coverage                                                                      []coverageLink
}
type coverageLink struct{ Name, Href string }
type flowView struct {
	Anchor, ID, Protocol, Channel, Asset, Trace, Parent, Correlation, ParentCorrelation, ContextSource, Error string
	Incomplete                                                                                                bool
	Request, Response                                                                                         messageView
	Redactions                                                                                                []artifact.Redaction
	Conditions                                                                                                []conditionLink
}
type conditionLink struct{ Name, Href, State string }
type messageView struct{ Method, URL, Status, Headers, Trailers, Body, BodyNote string }
type contractView struct {
	ID, APIID, Origin, Kind, Revision string
	Known                             bool
	Notes                             []string
	Targets                           []targetView
	Violations                        []violationView
	Counts                            []countView
}
type countView struct {
	Family string
	coverage.ContractCounts
}
type targetView struct {
	Anchor string
	*coverage.ContractTarget
	Evidence []evidenceView
}
type evidenceView struct {
	Href string
	coverage.ContractEvidence
}
type violationView struct {
	coverage.ContractViolation
	Href string
}
type workflowView struct {
	ID, Summary, Description, AttemptID, Boundary, AttemptHref, Inputs string
	Steps                                                              []stepView
}
type stepView struct {
	ID, Operation, URL, FlowID, Href, Body, Headers string
	Parameters                                      []arazzo.Parameter
	Criteria                                        []arazzo.Criterion
}
type sourceView struct{ ID, Name, Kind, Content, OriginalJSON string }

func source(id, name, kind, content string) sourceView {
	out := sourceView{ID: id, Name: name, Kind: kind, Content: content}
	// HTML parsers normalize CRLF and discard the first newline after <pre>.
	// Preserve those source bytes separately as an escaped JSON string so local
	// downloads remain exact while the readable preview stays ordinary text.
	if strings.Contains(content, "\r") || strings.HasPrefix(content, "\n") {
		data, _ := json.Marshal(content)
		out.OriginalJSON = string(data)
	}
	return out
}

func anchor(kind string, values ...string) string {
	b, _ := json.Marshal(values)
	return fmt.Sprintf("%s-%x", kind, sha256.Sum256(b))
}
func flowKey(attempt, flow string) string { return attempt + "\x00" + flow }
func state(s string) string {
	switch value := strings.ToLower(strings.TrimSpace(s)); value {
	case "":
		return "unknown"
	case "pass":
		return "passed"
	case "fail":
		return "failed"
	case "skip":
		return "skipped"
	default:
		return value
	}
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "not recorded"
	}
	return t.UTC().Format(time.RFC3339)
}
func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "Unavailable: " + err.Error()
	}
	return string(b)
}
func headers(h artifact.Headers) string {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var lines []string
	for _, key := range keys {
		for _, value := range h[key] {
			lines = append(lines, key+": "+value)
		}
	}
	return strings.Join(lines, "\n")
}
func message(m coverage.InteractionMessage) messageView {
	out := messageView{Method: m.Method, URL: m.URL, Headers: headers(m.Headers), Trailers: headers(m.Trailers), Body: m.Body}
	if m.Status != 0 {
		out.Status = strconv.Itoa(m.Status)
	}
	if b := m.BodyInfo; b != nil {
		var notes []string
		if b.Omitted {
			notes = append(notes, "Body omitted from capture")
		} else if !b.Present {
			notes = append(notes, "No retained body")
		}
		if b.Redacted {
			notes = append(notes, "Body redacted")
		}
		if b.Truncated {
			notes = append(notes, "Body truncated")
		}
		if b.Encoding != "" {
			notes = append(notes, "encoding: "+b.Encoding)
		}
		if b.ContentEncoding != "" {
			notes = append(notes, "content encoding: "+b.ContentEncoding)
		}
		size := fmt.Sprintf("%d retained bytes", b.RetainedBytes)
		if b.OriginalBytes > 0 {
			size += fmt.Sprintf("; %d original bytes", b.OriginalBytes)
		}
		notes = append(notes, size)
		out.BodyNote = strings.Join(notes, " · ")
	} else if m.Body == "" {
		out.BodyNote = "No body recorded"
	}
	var compact any
	if json.Unmarshal([]byte(m.Body), &compact) == nil {
		var raw json.RawMessage = []byte(m.Body)
		out.Body = prettyJSON(raw)
	}
	return out
}
func makePage(in Inputs) (*pageModel, error) {
	r := in.Report
	m := &pageModel{Name: r.SUTName, RunState: "unknown", GeneratedAt: stamp(r.GeneratedAt), Producer: r.Producer.Name, Notes: append([]string(nil), r.Notes...)}
	if m.Name == "" {
		m.Name = "Wiretap API report"
	}
	if r.Capture != nil {
		m.RunID = r.Capture.RunID
		m.RunState = state(r.Capture.State)
		m.Revision = r.Capture.Revision
	}
	attempts := map[string]*attemptView{}
	links := map[string]string{}
	attemptLinks := map[string]string{}
	seenFlows := map[string]bool{}
	conditionLinks := map[string][]conditionLink{}
	ensure := func(id, name string) *attemptView {
		key := id
		if key == "" {
			key = "name:" + name
		}
		if t := attempts[key]; t != nil {
			return t
		}
		t := &attemptView{Anchor: anchor("attempt", key), ID: id, Name: name, State: "unknown", Started: "not recorded", Finished: "not recorded"}
		attempts[key] = t
		if id != "" {
			attemptLinks[id] = "#" + t.Anchor
		}
		return t
	}
	addFlow := func(s coverage.FlowSample) {
		t := ensure(s.TestCaseID, s.TestCase)
		if t.State == "unknown" {
			t.State = state(s.TestStatus)
		}
		if t.Identity == "" && s.TestIdentity != nil {
			t.Identity = s.TestIdentity.ID
			t.Variant = s.TestIdentity.Variant
			if s.TestIdentity.AttemptOrdinal != nil {
				t.Ordinal = strconv.Itoa(*s.TestIdentity.AttemptOrdinal)
			}
		}
		if t.Trace == "" {
			t.Trace = s.TestTraceID
		}
		key := flowKey(s.TestCaseID, s.FlowID)
		if s.FlowID == "" {
			key = flowKey(s.TestCaseID, prettyJSON(s))
		}
		if seenFlows[key] {
			return
		}
		seenFlows[key] = true
		f := flowView{Anchor: anchor("flow", key), ID: s.FlowID, Protocol: s.Protocol, Channel: s.Channel, Asset: s.AssetID, Trace: s.TraceID, Parent: s.ParentFlowID, Correlation: s.TestCorrelation, ParentCorrelation: s.ParentCorrelation, ContextSource: s.TraceContextSource, Error: s.TransportError, Incomplete: s.CaptureIncomplete, Request: message(s.Request), Response: message(s.Response), Redactions: s.RedactionRules}
		t.Flows = append(t.Flows, f)
		links[flowKey(s.TestCaseID, s.FlowID)] = "#" + f.Anchor
	}
	for _, tc := range r.TestCases {
		for _, a := range tc.Attempts {
			t := ensure(a.ID, tc.Name)
			if a.Name != "" {
				t.Name = a.Name
			}
			t.State = state(a.Status)
			t.Trace = a.TraceID
			t.Started = stamp(a.StartedAt)
			t.Finished = stamp(a.FinishedAt)
			t.Tags = append([]string(nil), a.Tags...)
			if a.Identity != nil {
				t.Identity = a.Identity.ID
				t.Variant = a.Identity.Variant
				if a.Identity.AttemptOrdinal != nil {
					t.Ordinal = strconv.Itoa(*a.Identity.AttemptOrdinal)
				}
			}
		}
		for _, s := range tc.Traffic {
			addFlow(s)
		}
		for _, group := range [][]coverage.SpecOperationSamples{tc.Provided, tc.Dependent} {
			for _, spec := range group {
				for _, op := range spec.Operations {
					for _, s := range op.Samples {
						addFlow(s)
					}
				}
			}
		}
		if len(tc.Attempts) == 0 && len(tc.Traffic) == 0 && len(tc.Provided) == 0 && len(tc.Dependent) == 0 {
			ensure("", tc.Name)
		}
	}
	// Older coverage JSON can lack the per-test traffic inventory. Operation
	// samples still provide useful evidence and must have real link targets.
	for _, g := range []coverage.SpecCoverageGroup{r.Provided, r.Dependent} {
		for _, s := range g.Specs {
			for _, op := range s.Exercised {
				for _, sample := range op.Samples {
					addFlow(sample)
				}
			}
		}
	}
	evidence := func(attempt, flow, name, outcome string) evidenceLink {
		href := links[flowKey(attempt, flow)]
		if href == "" {
			href = attemptLinks[attempt]
		}
		return evidenceLink{Name: name, AttemptID: attempt, FlowID: flow, State: state(outcome), Href: href}
	}
	buildSpecs := func(g coverage.SpecCoverageGroup, role string) []specView {
		var specs []specView
		for _, s := range g.Specs {
			v := specView{ID: s.SchemaID, Name: s.AssetName, Source: s.Source, Asset: s.AssetID, Protocol: s.Protocol, Known: s.DenominatorKnown, Anchor: anchor("spec", role, s.SchemaID, s.SchemaSet, s.AssetID, s.Source, s.Type)}
			if v.Name == "" {
				v.Name = s.SchemaID
			}
			for _, set := range []struct {
				ops   []coverage.OperationSamples
				state string
			}{{s.Exercised, "covered"}, {s.NotCovered, "not_covered"}} {
				for _, op := range set.ops {
					o := operationView{Name: op.Operation, State: set.state, Anchor: anchor("operation", role, s.SchemaID, s.SchemaSet, s.AssetID, s.Source, s.Type, op.Operation)}
					for _, sample := range op.Samples {
						o.Samples = append(o.Samples, evidence(sample.TestCaseID, sample.FlowID, sample.TestCase, sample.TestStatus))
						t := ensure(sample.TestCaseID, sample.TestCase)
						link := coverageLink{Name: v.Name + " · " + op.Operation, Href: "#" + o.Anchor}
						found := false
						for _, existing := range t.Coverage {
							found = found || existing.Href == link.Href
						}
						if !found {
							t.Coverage = append(t.Coverage, link)
						}
					}
					v.Operations = append(v.Operations, o)
					if set.state == "covered" {
						v.Observed++
						m.Operations++
					} else {
						m.Gaps++
					}
				}
			}
			sort.SliceStable(v.Operations, func(i, j int) bool { return v.Operations[i].Name < v.Operations[j].Name })
			v.Total = len(v.Operations)
			if v.Known && v.Total > 0 {
				v.Percent = v.Observed * 100 / v.Total
			}
			if !v.Known {
				m.Unknown++
			}
			specs = append(specs, v)
		}
		sort.SliceStable(specs, func(i, j int) bool { return specs[i].Name+specs[i].Anchor < specs[j].Name+specs[j].Anchor })
		return specs
	}
	m.Provided = buildSpecs(r.Provided, "provided")
	m.Dependent = buildSpecs(r.Dependent, "dependency")
	for _, t := range attempts {
		sort.SliceStable(t.Coverage, func(i, j int) bool { return t.Coverage[i].Name < t.Coverage[j].Name })
		m.Tests = append(m.Tests, t)
		m.Attempts++
		m.Flows += len(t.Flows)
		if t.State == "passed" {
			m.Passed++
		} else {
			m.Other++
		}
	}
	sort.SliceStable(m.Tests, func(i, j int) bool {
		a, b := m.Tests[i], m.Tests[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Identity != b.Identity {
			return a.Identity < b.Identity
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.Ordinal != b.Ordinal {
			ai, _ := strconv.Atoi(a.Ordinal)
			bi, _ := strconv.Atoi(b.Ordinal)
			if ai != bi {
				return ai < bi
			}
			return a.Ordinal < b.Ordinal
		}
		return a.ID < b.ID
	})
	if r.Contracts != nil {
		m.HasContracts = true
		m.ContractScope = r.Contracts.Scope
		m.ContractComplete = r.Contracts.Complete
		for _, s := range r.Contracts.Specs {
			if s == nil {
				continue
			}
			v := contractView{ID: s.SchemaID, APIID: s.APIID, Origin: s.Origin, Kind: s.TargetKind, Revision: s.Revision, Known: s.DenominatorKnown, Notes: s.Notes}
			for family, c := range s.Counts {
				v.Counts = append(v.Counts, countView{Family: family, ContractCounts: c})
			}
			sort.Slice(v.Counts, func(i, j int) bool { return v.Counts[i].Family < v.Counts[j].Family })
			for _, t := range s.Targets {
				if t == nil {
					continue
				}
				tv := targetView{Anchor: anchor("condition", s.SchemaID, s.APIID, s.Origin, s.TargetKind, s.Revision, t.Key), ContractTarget: t}
				for _, e := range t.Evidence {
					tv.Evidence = append(tv.Evidence, evidenceView{Href: evidence(e.AttemptID, e.FlowID, e.TestName, e.Outcome).Href, ContractEvidence: e})
					key := flowKey(e.AttemptID, e.FlowID)
					link := conditionLink{Name: t.Phase + " · " + t.Family + " · " + t.Condition, Href: "#" + tv.Anchor, State: t.State}
					found := false
					for _, existing := range conditionLinks[key] {
						found = found || existing.Href == link.Href
					}
					if !found {
						conditionLinks[key] = append(conditionLinks[key], link)
					}
				}
				v.Targets = append(v.Targets, tv)
			}
			sort.SliceStable(v.Targets, func(i, j int) bool {
				return v.Targets[i].Operation+v.Targets[i].Location+v.Targets[i].Condition < v.Targets[j].Operation+v.Targets[j].Location+v.Targets[j].Condition
			})
			for _, violation := range s.Violations {
				v.Violations = append(v.Violations, violationView{ContractViolation: violation, Href: evidence(violation.Evidence.AttemptID, violation.Evidence.FlowID, violation.Evidence.TestName, violation.Evidence.Outcome).Href})
			}
			m.Contracts = append(m.Contracts, v)
		}
		sort.SliceStable(m.Contracts, func(i, j int) bool {
			return m.Contracts[i].ID+m.Contracts[i].APIID+m.Contracts[i].Revision < m.Contracts[j].ID+m.Contracts[j].APIID+m.Contracts[j].Revision
		})
	}

	for _, test := range m.Tests {
		for i := range test.Flows {
			test.Flows[i].Conditions = conditionLinks[flowKey(test.ID, test.Flows[i].ID)]
			sort.SliceStable(test.Flows[i].Conditions, func(a, b int) bool {
				return test.Flows[i].Conditions[a].Name+test.Flows[i].Conditions[a].Href < test.Flows[i].Conditions[b].Name+test.Flows[i].Conditions[b].Href
			})
		}
	}
	if in.Arazzo != nil {
		m.ArazzoSources = in.Arazzo.Sources
		for _, w := range in.Arazzo.Workflows {
			v := workflowView{ID: w.ID, Summary: w.Summary, Description: w.Description, AttemptID: w.AttemptID, Boundary: w.Boundary, AttemptHref: attemptLinks[w.AttemptID], Inputs: prettyJSON(w.Inputs)}
			for _, s := range w.Steps {
				sv := stepView{ID: s.ID, Operation: s.OperationPath, URL: s.URL, FlowID: s.FlowID, Href: links[flowKey(w.AttemptID, s.FlowID)], Headers: headers(s.Headers), Parameters: s.Parameters, Criteria: s.SuccessCriteria}
				if s.RequestBody != nil {
					sv.Body = prettyJSON(s.RequestBody)
				}
				v.Steps = append(v.Steps, sv)
			}
			m.Workflows = append(m.Workflows, v)
		}
	}
	wiretapText := in.WiretapJSON
	if wiretapText == "" {
		raw, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("HTML report Wiretap JSON: %w", err)
		}
		wiretapText = string(raw)
	}
	wiretapName := in.WiretapName
	if wiretapName == "" {
		wiretapName = "wiretap.coverage.json"
	}
	m.Sources = append(m.Sources, source("source-wiretap", wiretapName, "Wiretap JSON", wiretapText))
	specs := append([]Document(nil), in.Specs...)
	sort.SliceStable(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	for i, s := range specs {
		m.Sources = append(m.Sources, source(anchor("source", s.Name, strconv.Itoa(i)), s.Name, "OpenAPI source", s.Content))
	}
	if in.Arazzo != nil {
		arazzoText := in.ArazzoJSON
		if arazzoText == "" {
			raw, err := json.MarshalIndent(in.Arazzo, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("HTML report Arazzo: %w", err)
			}
			arazzoText = string(raw)
		}
		name := in.ArazzoName
		if name == "" {
			name = "arazzo.json"
		}
		m.Sources = append(m.Sources, source("source-arazzo", name, "Arazzo", arazzoText))
	}
	return m, nil
}
