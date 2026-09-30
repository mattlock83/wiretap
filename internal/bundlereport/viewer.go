package bundlereport

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
)

// The HTML dashboard is a disposable view. It uses the same verified snapshot
// and coverage calculation as the OKF pages, and never becomes input to either.
type viewerData struct {
	Contracts                 *coverage.ContractCoverage
	ContractSpecs             []viewerContractSpec
	RejectionOperations       []viewerRejectionOperation
	TestGroups                []viewerTestGroup
	RunID, State, GeneratedAt string
	APIID, TargetKind         string
	Revision                  uint64
	Attempts, Passed, Failed  int
	Flows, ObservedOperations int
	Pending                   []bundle.PendingAttempt
	Provided, Dependent       []viewerSpec
	MockDrafts                []viewerMockDraft
	Tests                     []viewerAttempt
	Notes                     []string
}

type viewerMockDraft struct {
	ID, URL               string
	Operations, Exchanges int
}

type viewerSpec struct {
	Name, ID, AssetID, Kind, TargetKind, SourceURL, Search string
	EvidenceLabel, Interpretation                          string
	DenominatorKnown                                       bool
	Observed, Passing, Total                               int
	Percent                                                int
	Operations                                             []viewerOperation
}

type viewerOperation struct {
	Name, State string
	Samples     []viewerSample
}

type viewerSample struct {
	TestName, AttemptID, FlowID, Status, Request, Response, EvidenceURL, TestURL string
	ValidationStatus                                                             string
}

type viewerAttempt struct {
	Coverage                                                                      []viewerCoverageLink
	Exploration                                                                   *artifact.Exploration
	Assertions                                                                    []artifact.Assertion
	ID, Name, Status, TraceID, EvidenceURL, MarkdownURL, ValidationStatus, Search string
	Incomplete                                                                    bool
	Chronological                                                                 bool
	Diagnostics                                                                   []string
	Flows                                                                         []viewerFlow
	Anchor, DisplayName, OriginLabel, Operation, Summary, GroupKey, GroupName     string
	Attention                                                                     bool
	ChecksPassed, ChecksOther                                                     int
}

type viewerFlow struct {
	ID, Channel, AssetID, TraceID, ParentID, Correlation, ParentCorrelation, ContextSource, Error string
	Sequence                                                                                      int
	Direction, EvidenceLabel                                                                      string
	Operation                                                                                     string
	Request, Response                                                                             viewerMessage
}

type viewerMessage struct {
	Method, URL, Status, Headers, Trailers string
	Body, AttachmentURL, BodyNote          string
}

func writeViewer(root string, run *bundle.Run, captures []*artifact.Artifact, report *coverage.Report, drafts []mockDraft) error {
	data := viewerData{
		Contracts: report.Contracts,
		RunID:     run.RunID, State: run.RunState, Revision: run.Revision,
		APIID: run.APIID, TargetKind: run.SUTTargetKind,
		GeneratedAt: run.UpdatedAt.Format("2006-01-02 15:04:05 UTC"),
		Pending:     run.Pending,
		Notes:       report.Notes,
	}
	data.ContractSpecs = buildViewerCoverage(report.Contracts)
	if report.Contracts != nil {
		data.RejectionOperations = buildViewerRejections(report.Contracts.Rejections)
	}
	coverageLinks := viewerCoverageLinks(data.ContractSpecs, data.RejectionOperations)
	if data.APIID == "" {
		data.APIID = "Unidentified system"
	}
	if data.TargetKind == "" {
		data.TargetKind = "unknown"
	}
	byID := make(map[string]*artifact.Artifact, len(captures))
	for _, capture := range captures {
		byID[capture.TestCase.ID] = capture
	}
	for _, draft := range drafts {
		data.MockDrafts = append(data.MockDrafts, viewerMockDraft{ID: draft.AssetID, URL: "../" + draft.Path, Operations: draft.Operations, Exchanges: draft.Exchanges})
	}
	paths := make(map[string]string, len(run.Attempts))
	for _, ref := range run.Attempts {
		paths[ref.AttemptID] = "../" + ref.Path
	}
	makeSpecs := func(specs []coverage.SpecCoverage, kind string) []viewerSpec {
		out := make([]viewerSpec, 0, len(specs))
		for _, spec := range specs {
			name := spec.AssetName
			if name == "" {
				name = spec.SchemaID
			}
			item := viewerSpec{Name: name, ID: spec.SchemaID, AssetID: spec.AssetID, Kind: kind, DenominatorKnown: spec.DenominatorKnown, Observed: len(spec.Exercised), Total: len(spec.Exercised) + len(spec.NotCovered)}
			if kind == "provided" {
				if item.AssetID == "" {
					item.AssetID = run.APIID
				}
				item.TargetKind = run.SUTTargetKind
			} else {
				item.TargetKind = run.DependencyKinds[spec.AssetID]
			}
			if item.TargetKind == "" {
				item.TargetKind = "unknown"
			}
			item.EvidenceLabel, item.Interpretation = viewerEvidence(kind, item.TargetKind)
			if item.Total > 0 {
				item.Percent = 100 * item.Observed / item.Total
			}
			if strings.HasPrefix(spec.Source, "schemas/") {
				item.SourceURL = "../" + spec.Source
			}
			for _, operation := range spec.Exercised {
				entry := viewerOperation{Name: operation.Operation, State: "observed"}
				if passingObservation(operation.Samples) {
					entry.State = "passing"
					item.Passing++
				}
				for _, sample := range operation.Samples {
					validation := "not recorded"
					if capture := byID[sample.TestCaseID]; capture != nil && capture.SchemaValidation != nil {
						validation = capture.SchemaValidation.Status
					}
					entry.Samples = append(entry.Samples, viewerSample{
						TestName: sample.TestCase, AttemptID: sample.TestCaseID, FlowID: sample.FlowID, Status: sample.TestStatus,
						Request: strings.TrimSpace(sample.Request.Method + " " + sample.Request.URL), Response: fmt.Sprint(sample.Response.Status),
						EvidenceURL: paths[sample.TestCaseID], TestURL: "#attempt-" + shortHash(sample.TestCaseID),
						ValidationStatus: validation,
					})
				}
				item.Operations = append(item.Operations, entry)
			}
			for _, operation := range spec.NotCovered {
				item.Operations = append(item.Operations, viewerOperation{Name: operation.Operation, State: "unobserved"})
			}
			sort.Slice(item.Operations, func(i, j int) bool { return item.Operations[i].Name < item.Operations[j].Name })
			item.Search = strings.Join([]string{item.Name, item.ID, item.AssetID, item.Kind, item.TargetKind}, " ")
			for _, operation := range item.Operations {
				item.Search += " " + operation.Name
			}
			data.ObservedOperations += item.Observed
			out = append(out, item)
		}
		return out
	}
	data.Provided = makeSpecs(report.Provided.Specs, "provided")
	data.Dependent = makeSpecs(report.Dependent.Specs, "dependency")
	for _, ref := range run.Attempts {
		capture := byID[ref.AttemptID]
		if capture == nil {
			return fmt.Errorf("viewer: missing attempt %s", ref.AttemptID)
		}
		attempt := viewerAttempt{
			Exploration: capture.Exploration, Assertions: capture.Assertions,
			ID: ref.AttemptID, Name: capture.TestCase.Name, Status: capture.TestCase.Status,
			TraceID: capture.TestCase.TraceID, Incomplete: capture.CaptureIncomplete,
			EvidenceURL: "../" + ref.Path, MarkdownURL: "../tests/" + ref.AttemptID + ".md",
		}
		attempt.Anchor = "attempt-" + shortHash(ref.AttemptID)
		attempt.Coverage = coverageLinks[ref.AttemptID]
		attempt.DisplayName = attemptName(capture)
		attempt.OriginLabel = "Authored test"
		attempt.GroupKey = "authored"
		attempt.GroupName = "Authored tests"
		if x := capture.Exploration; x != nil {
			attempt.GroupKey = "generated|" + x.SourceRunID + "|" + x.SourceAttemptID + "|" + x.SourceSHA256
			attempt.GroupName = capture.TestCase.Name
			if i := strings.LastIndex(attempt.GroupName, " ["); i >= 0 {
				attempt.GroupName = attempt.GroupName[:i]
			}
			attempt.OriginLabel = "Generated input check"
			if x.Mode == "baseline" {
				attempt.OriginLabel = "Baseline replay"
				attempt.DisplayName = "Happy-path baseline"
			}
			if x.Target != nil {
				attempt.Operation = x.Target.Operation
				attempt.DisplayName = viewerMutationLabel(x.Target.Location, x.Target.Constraint)
			}
		}
		attempt.Diagnostics = append(attempt.Diagnostics, capture.CaptureDiagnostics...)
		if capture.SchemaValidation != nil {
			attempt.ValidationStatus = capture.SchemaValidation.Status
			for _, id := range capture.SchemaValidation.Skipped {
				attempt.Diagnostics = append(attempt.Diagnostics, "Validation skipped for API "+id+": schema explicitly omitted.")
			}
			for _, issue := range capture.SchemaValidation.Errors {
				attempt.Diagnostics = append(attempt.Diagnostics, issue.Message)
			}
		}
		attempt.Attention = capture.TestCase.Status != "pass" || capture.CaptureIncomplete || attempt.ValidationStatus == "fail"
		for _, check := range capture.Assertions {
			if check.Result == "pass" {
				attempt.ChecksPassed++
			} else {
				attempt.ChecksOther++
				attempt.Attention = true
			}
		}
		if capture.Exploration != nil && capture.Exploration.Target != nil {
			target := capture.Exploration.Target
			for _, flow := range capture.Traffic {
				if flow.ID == target.FlowID && flow.Response.HTTP != nil {
					attempt.Summary = fmt.Sprintf("Expected %s · received HTTP %d", knowledgeStatuses(target.ExpectedStatuses), flow.Response.HTTP.Status)
				}
			}
		}
		data.Attempts++
		switch capture.TestCase.Status {
		case "pass":
			data.Passed++
		default:
			data.Failed++
		}
		flows, chronological := orderedFlows(capture.Traffic)
		attempt.Chronological = chronological
		for index, flow := range flows {
			direction, evidenceLabel := flow.Channel, "Unclassified observation"
			switch flow.Channel {
			case "test_to_sut":
				direction = "Tests → " + data.APIID
				evidenceLabel, _ = viewerEvidence("provided", data.TargetKind)
			case "sut_to_dependency":
				direction = data.APIID + " → " + flow.AssetID
				evidenceLabel, _ = viewerEvidence("dependency", run.DependencyKinds[flow.AssetID])
			}
			attempt.Flows = append(attempt.Flows, viewerFlow{
				Sequence: index + 1, Direction: direction, EvidenceLabel: evidenceLabel,
				Operation: flowOperation(flow),
				ID:        flow.ID, Channel: flow.Channel, AssetID: flow.AssetID, TraceID: flow.TraceID,
				ParentID: flow.ParentFlowID, Correlation: correlationLabel(flow.TestCorrelation), ParentCorrelation: correlationLabel(flow.ParentCorrelation),
				ContextSource: flow.TraceContextSource, Error: flow.TransportError,
				Request: viewerHTTP(flow.Request.HTTP), Response: viewerHTTP(flow.Response.HTTP),
			})
			data.Flows++
		}
		attempt.Search = strings.Join([]string{attempt.Name, attempt.ID, attempt.Status, attempt.OriginLabel, attempt.DisplayName, attempt.Summary, attempt.Operation, strings.Join(attempt.Diagnostics, " ")}, " ")
		for _, flow := range attempt.Flows {
			attempt.Search += " " + flow.Request.Method + " " + flow.Request.URL + " " + flow.AssetID + " " + flow.Response.Status
		}
		data.Tests = append(data.Tests, attempt)
	}
	data.TestGroups = groupViewerAttempts(data.Tests)
	tpl, err := template.New("viewer").Parse(viewerHTML + viewerSections)
	if err != nil {
		return err
	}
	var rendered strings.Builder
	if err := tpl.Execute(&rendered, data); err != nil {
		return err
	}
	return writePage(filepath.Join(root, "viewer", "index.html"), []byte(rendered.String()))
}

func viewerEvidence(kind, targetKind string) (string, string) {
	switch targetKind {
	case "mock":
		return "Mock assumption", "Traffic describes mock behavior used in these tests. Agreement with the real provider and which response fields the consumer used are unknown."
	case "service":
		if kind == "provided" {
			return "Provider observation", "Traffic was observed against this service in the recorded test environment. Passing tests and schema checks are separate evidence; neither establishes behavior for untested inputs."
		}
		return "Dependency observation", "Traffic was observed against a service dependency in this test environment. Compatibility with other versions or environments and which response fields the consumer used remain unknown."
	default:
		return "Unclassified observation", "The target was not identified as a service or mock. Classify it before treating these observations as provider evidence or mock assumptions."
	}
}

func viewerHTTP(message *artifact.HttpMessage) viewerMessage {
	if message == nil {
		return viewerMessage{}
	}
	view := viewerMessage{
		Method: message.Method, URL: message.URL, Status: fmt.Sprint(message.Status),
		Headers: viewerHeaders(message.Headers), Trailers: viewerHeaders(message.Trailers),
	}
	if message.Body == nil {
		return view
	}
	body := message.Body
	if body.Truncated {
		view.BodyNote = fmt.Sprintf("Capture truncated from %d bytes", body.OriginalBytes)
	}
	var raw []byte
	if body.Bytes != "" {
		raw, _ = base64.StdEncoding.DecodeString(body.Bytes)
		if view.BodyNote != "" {
			view.BodyNote += "; "
		}
		view.BodyNote += "Binary body"
	} else {
		raw = []byte(body.Text)
		view.Body = viewerPreview(body.Text)
	}
	if len(raw) > 0 {
		hash := sha256.Sum256(raw)
		view.AttachmentURL = "../payloads/" + hex.EncodeToString(hash[:]) + ".bin"
	}
	return view
}

func viewerHeaders(headers artifact.Headers) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		for _, value := range headers[name] {
			lines = append(lines, name+": "+value)
		}
	}
	return strings.Join(lines, "\n")
}

func viewerPreview(value string) string {
	const limit = 4096
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + "\n… preview limited to 4 KiB; open the attachment for retained bytes"
}

var viewerLink = regexp.MustCompile(`href="([^"]+)"`)
var viewerAnchor = regexp.MustCompile(`\sid="([^"]+)"`)

// checkViewer verifies the generated entry point and its local links. It is
// separate from bundle.Load so damaged views never block evidence reads.
func checkViewer(root string, run *bundle.Run) error {
	path := filepath.Join(root, "viewer", "index.html")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("viewer: %w", err)
	}
	if !strings.Contains(string(data), fmt.Sprintf(`data-run-id="%s" data-revision="%d"`, run.RunID, run.Revision)) {
		return fmt.Errorf("viewer: stale or invalid run identity")
	}
	anchors := map[string]bool{}
	for _, match := range viewerAnchor.FindAllSubmatch(data, -1) {
		id := html.UnescapeString(string(match[1]))
		if anchors[id] {
			return fmt.Errorf("viewer: duplicate navigation anchor %q", id)
		}
		anchors[id] = true
	}
	for _, match := range viewerLink.FindAllSubmatch(data, -1) {
		link := html.UnescapeString(string(match[1]))
		if strings.HasPrefix(link, "#") {
			if !anchors[strings.TrimPrefix(link, "#")] {
				return fmt.Errorf("viewer link %q: missing navigation target", link)
			}
			continue
		}
		if strings.Contains(link, "://") {
			continue
		}
		if err := localReference(root, path, link); err != nil {
			return fmt.Errorf("viewer link %q: %w", link, err)
		}
	}
	return nil
}

//go:embed viewer.html.tmpl
var viewerHTML string

//go:embed viewer_sections.html.tmpl
var viewerSections string
