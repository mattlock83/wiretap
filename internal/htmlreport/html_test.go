package htmlreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/coverage"
	"golang.org/x/net/html"
)

func rendererFixture() Inputs {
	zero, one := 0, 1
	samples := []coverage.FlowSample{
		{TestCaseID: "attempt-failed", TestCase: "creates an order", TestStatus: "fail", FlowID: "same-flow", Protocol: "http", Channel: "test_to_sut", Request: coverage.InteractionMessage{Method: "POST", URL: "https://orders.example/orders", Headers: artifact.Headers{"X-B": []string{"two"}, "X-A": []string{"one"}}, Body: `{"item":"book"}`}, Response: coverage.InteractionMessage{Status: 503, Trailers: artifact.Headers{"X-Diagnostic": []string{"unavailable"}}, Body: `{"error":"unavailable"}`}},
		{TestCaseID: "attempt-passed", TestCase: "creates an order", TestStatus: "pass", FlowID: "same-flow", Protocol: "http", Channel: "test_to_sut", Request: coverage.InteractionMessage{Method: "POST", URL: "https://orders.example/orders", Body: `{"item":"book"}`}, Response: coverage.InteractionMessage{Status: 201, Body: `{"id":"order-1"}`, BodyInfo: &coverage.BodyAvailability{Present: true, RetainedBytes: 16, OriginalBytes: 16}}},
	}
	report := &coverage.Report{ReportVersion: "1.0", SUTName: "Order service", GeneratedAt: time.Date(2026, 10, 26, 1, 2, 3, 0, time.UTC), Capture: &coverage.CaptureSource{RunID: "run-example", State: "complete", Revision: 7}, TestCases: []coverage.TestCaseCoverage{{Name: "creates an order", Attempts: []artifact.TestCase{{ID: "attempt-failed", Name: "creates an order", Status: "fail", Identity: &artifact.TestIdentity{ID: "order.test:4", Variant: "guest", AttemptOrdinal: &zero}}, {ID: "attempt-passed", Name: "creates an order", Status: "pass", Identity: &artifact.TestIdentity{ID: "order.test:4", Variant: "guest", AttemptOrdinal: &one}}}, Traffic: samples}}, Provided: coverage.SpecCoverageGroup{Specs: []coverage.SpecCoverage{{SchemaID: "orders", AssetID: "order-api", DenominatorKnown: true, Source: "orders.yaml", Exercised: []coverage.OperationSamples{{Operation: "POST /orders", Samples: samples}}, NotCovered: []coverage.OperationSamples{{Operation: "GET /orders/{id}"}}}}}}
	report.Contracts = &coverage.ContractCoverage{Complete: true, Scope: "registered supplied contracts only", Specs: []*coverage.ContractSpec{{SchemaID: "orders", APIID: "order-api", DenominatorKnown: true, Origin: "provided", TargetKind: "service", Revision: "sha256-example", Counts: map[string]coverage.ContractCounts{"property": {Covered: 1, NotCovered: 1}, "operation": {Covered: 1}}, Targets: []*coverage.ContractTarget{{Key: "condition-id", Operation: "POST /orders", Phase: "response", Location: "/properties/id", FieldPath: "$.id", Family: "property", Condition: "present", State: "covered", Evidence: []coverage.ContractEvidence{{AttemptID: "attempt-passed", FlowID: "same-flow", TestName: "creates an order", Outcome: "pass", Validation: "valid", Result: "covered"}}}, {Key: "condition-gap", Operation: "GET /orders/{id}", State: "not_covered", Condition: "exercised"}, {Key: "condition-unknown", State: "unknown", Reason: "Incomplete body"}}}}}
	return Inputs{Report: report, WiretapName: "wiretap.json", Specs: []Document{{Name: "orders.yaml", Content: "openapi: 3.0.3\ninfo:\n  title: Orders"}}, ArazzoName: "arazzo.json", Arazzo: &arazzo.Document{Arazzo: "1.0.1", Info: arazzo.Info{Title: "Captured orders", Version: "1"}, Sources: []arazzo.Source{{Name: "orders", URL: "orders.yaml", Type: "openapi"}}, Workflows: []arazzo.Workflow{{ID: "workflow_1", Summary: "creates an order", AttemptID: "attempt-passed", Boundary: "sut", Steps: []arazzo.Step{{ID: "step_1", FlowID: "same-flow", URL: "https://orders.example/orders", OperationPath: "$sourceDescriptions.orders#/paths/~1orders/post", SuccessCriteria: []arazzo.Criterion{{Condition: "$statusCode == 201"}}}}}}}}
}

func renderFixture(t *testing.T, in Inputs) string {
	t.Helper()
	var out bytes.Buffer
	if err := Write(&out, in); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func parsed(t *testing.T, text string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func walk(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walk(child, visit)
	}
}
func attr(node *html.Node, key string) string {
	for _, a := range node.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func content(node *html.Node) string {
	var out strings.Builder
	walk(node, func(n *html.Node) {
		if n.Type == html.TextNode {
			out.WriteString(n.Data)
		}
	})
	return out.String()
}

func TestRendererLinksCoverageAttemptsAndWorkflows(t *testing.T) {
	in := rendererFixture()
	operation := &in.Report.Provided.Specs[0].Exercised[0]
	operation.Samples = append(operation.Samples, operation.Samples[0])
	target := in.Report.Contracts.Specs[0].Targets[0]
	target.Evidence = append(target.Evidence, target.Evidence[0])
	model, err := makePage(in)
	if err != nil {
		t.Fatal(err)
	}
	if model.Attempts != 2 || model.Passed != 1 || model.Other != 1 {
		t.Fatalf("real pass/fail statuses produced wrong metrics: attempts=%d passed=%d other=%d", model.Attempts, model.Passed, model.Other)
	}
	for _, attempt := range model.Tests {
		if len(attempt.Coverage) != 1 || len(attempt.Flows) != 1 || len(attempt.Flows[0].Conditions) > 1 {
			t.Fatalf("repeated evidence duplicated links or flows: %+v", attempt)
		}
	}
	output := renderFixture(t, in)
	for _, want := range []string{`data-wiretap-report="coverage"`, "Provided APIs", "Contract coverage", "Test runs", "Arazzo workflows", "Sources", "retry ordinal 0", "retry ordinal 1", "Stable test identity:", "Measured contract conditions (1)", "order.test:4", "variant guest", "X-Diagnostic: unavailable", "HTTP 503", "HTTP 201", "$statusCode == 201", "not covered", "unknown", "wiretap.json", "orders.yaml", "arazzo.json"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q", want)
		}
	}
	doc := parsed(t, output)
	ids := map[string]bool{}
	refs := map[string]int{}
	var flowIDs []string
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		id := attr(n, "id")
		if id != "" {
			if ids[id] {
				t.Errorf("duplicate id %q", id)
			}
			ids[id] = true
			if strings.HasPrefix(id, "flow-") {
				flowIDs = append(flowIDs, id)
			}
		}
		if href := attr(n, "href"); strings.HasPrefix(href, "#") {
			refs[href[1:]]++
		}
	})
	for id := range refs {
		if !ids[id] {
			t.Errorf("dangling internal link #%s", id)
		}
	}
	if len(flowIDs) != 2 {
		t.Fatalf("retries sharing a flow id must remain separate: %v", flowIDs)
	}
	for _, id := range flowIDs {
		if refs[id] == 0 {
			t.Errorf("no coverage link to flow %s", id)
		}
	}
	if refs[flowIDs[1]] < 2 && refs[flowIDs[0]] < 2 {
		t.Error("workflow and contract evidence did not link to captured flow")
	}
	if strings.Count(output, `class="attempt filter-item"`) != 2 {
		t.Error("retry attempts merged")
	}
}

func TestBodyFormattingPreservesCapturedJSONValues(t *testing.T) {
	for _, body := range []string{`{"big":9007199254740993,"exponent":1e1000}`, `{"duplicate":1,"duplicate":2}`, `{"markup":"<>&","escaped":"\u003c"}`, `null`, `[true,1.00]`, `broken {`, ""} {
		got := message(coverage.InteractionMessage{Body: body}).Body
		if !json.Valid([]byte(body)) {
			if got != body {
				t.Fatalf("non-JSON body changed: %q", got)
			}
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(got)); err != nil || compact.String() != body {
			t.Fatalf("JSON values changed: %q -> %q (%v)", body, got, err)
		}
	}
}

func TestRendererEscapesAllEvidenceAndDoesNotLinkSourceURLs(t *testing.T) {
	in := rendererFixture()
	attack := `</pre></script><script data-injected="yes">alert("capture")</script><img src="https://evil.example/x" onerror="alert(1)">`
	in.Report.SUTName = attack
	in.Report.Notes = []string{attack}
	in.Report.TestCases[0].Name = attack
	in.Report.TestCases[0].Attempts[0].Name = attack
	in.Report.TestCases[0].Traffic[0].Request.Body = attack
	in.Report.TestCases[0].Traffic[0].Request.URL = "javascript:alert(1)"
	in.Report.TestCases[0].Traffic[0].Request.Headers = artifact.Headers{attack: []string{attack}}
	in.Specs = []Document{{Name: attack, Content: attack}}
	in.Arazzo.Sources[0].URL = "https://evil.example/source.yaml"
	in.Arazzo.Workflows[0].Summary = attack
	in.Arazzo.Workflows[0].Steps[0].SuccessCriteria[0].Condition = attack
	output := renderFixture(t, in)
	doc := parsed(t, output)
	scripts := 0
	sourceFound := false
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.Data == "script" {
			scripts++
			if strings.Contains(content(n), "capture") {
				t.Error("captured content entered executable script")
			}
		}
		if n.Data == "img" {
			t.Error("captured HTML produced an image element")
		}
		for _, a := range n.Attr {
			if strings.HasPrefix(a.Key, "on") {
				t.Errorf("captured event handler attribute %s", a.Key)
			}
			if a.Key == "href" && !strings.HasPrefix(a.Val, "#") {
				t.Errorf("external or active link %q", a.Val)
			}
		}
		if n.Data == "pre" && content(n) == attack {
			sourceFound = true
		}
	})
	if scripts != 1 {
		t.Errorf("got %d scripts, want one fixed local script", scripts)
	}
	if !sourceFound {
		t.Error("escaped source no longer preserves original text")
	}
	if !strings.Contains(output, "connect-src 'none'") {
		t.Error("missing offline CSP")
	}
}

func TestRendererIsDeterministicAndDoesNotMutateInputs(t *testing.T) {
	in := rendererFixture()
	before := prettyJSON(in)
	first := renderFixture(t, in)
	for i := 0; i < 15; i++ {
		if next := renderFixture(t, in); next != first {
			t.Fatal("render changed for identical input")
		}
	}
	if prettyJSON(in) != before {
		t.Fatal("render mutated evidence")
	}
	// Coverage JSON with an absent retry ordinal and explicit ordinal zero must
	// have a total ordering, independent of map iteration during model assembly.
	in.Report.TestCases[0].Attempts[1].Identity.AttemptOrdinal = nil
	for i := 0; i < 15; i++ {
		if i == 0 {
			first = renderFixture(t, in)
		} else if renderFixture(t, in) != first {
			t.Fatal("missing and zero retry ordinals are nondeterministic")
		}
	}
}

func TestRendererShowsMissingMeasurementsAndRetainedBodyLimits(t *testing.T) {
	in := rendererFixture()
	in.Report.Contracts = nil
	in.Arazzo = nil
	in.Report.Provided.Specs[0].DenominatorKnown = false
	in.Report.TestCases[0].Traffic[0].CaptureIncomplete = true
	in.Report.TestCases[0].Traffic[0].Response.BodyInfo = &coverage.BodyAvailability{Present: true, Truncated: true, RetainedBytes: 4, OriginalBytes: 200}
	in.Report.TestCases[0].Traffic[0].Response.Body = "part"
	output := renderFixture(t, in)
	for _, want := range []string{"denominator unknown", "No contract condition measurement", "No Arazzo workflows were supplied", "Capture incomplete", "Body truncated", "4 retained bytes; 200 original bytes", "This report does not run a compatibility check"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestRendererFindsEvidenceFromOperationOnlyJSON(t *testing.T) {
	in := rendererFixture()
	in.Report.TestCases = nil
	in.Report.Provided.Specs[0].Exercised[0].Samples[0].TestIdentity = &artifact.TestIdentity{ID: "fallback-identity", Variant: "fallback-variant"}
	output := renderFixture(t, in)
	if !strings.Contains(output, "fallback-identity") || !strings.Contains(output, "variant fallback-variant") {
		t.Error("sample identity was not retained for older JSON")
	}
	doc := parsed(t, output)
	ids := map[string]bool{}
	var hrefs []string
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode {
			ids[attr(n, "id")] = true
			if href := attr(n, "href"); strings.HasPrefix(href, "#flow-") {
				hrefs = append(hrefs, href)
			}
		}
	})
	if len(hrefs) == 0 {
		t.Fatal("no legacy operation evidence linked")
	}
	for _, href := range hrefs {
		if !ids[href[1:]] {
			t.Errorf("missing evidence target %s", href)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestRendererRejectsMissingDataAndPropagatesWriteErrors(t *testing.T) {
	if err := Write(io.Discard, Inputs{}); err == nil {
		t.Error("missing JSON accepted")
	}
	if err := Write(brokenWriter{}, rendererFixture()); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("got %v, want write error", err)
	}
}

func TestRendererNormalizesArtifactOutcomes(t *testing.T) {
	for input, want := range map[string]string{"pass": "passed", "fail": "failed", "skip": "skipped", "passed": "passed", "failed": "failed", "skipped": "skipped", "": "unknown", " pass ": "passed"} {
		if got := state(input); got != want {
			t.Errorf("state(%q)=%q, want %q", input, got, want)
		}
	}
	in := rendererFixture()
	in.Report.TestCases[0].Attempts = append(in.Report.TestCases[0].Attempts, artifact.TestCase{ID: "skipped-attempt", Name: "skipped test", Status: "skip"})
	output := renderFixture(t, in)
	doc := parsed(t, output)
	states := map[string]int{}
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && attr(n, "class") == "attempt filter-item" {
			states[attr(n, "data-state")]++
		}
	})
	for _, outcome := range []string{"passed", "failed", "skipped"} {
		if states[outcome] != 1 {
			t.Errorf("outcome %q count=%d, want 1", outcome, states[outcome])
		}
	}
}

func TestRendererPreservesRetainedFlowOrderAndUnknownOriginalSize(t *testing.T) {
	in := rendererFixture()
	first := in.Report.TestCases[0].Traffic[0]
	first.FlowID = "flow-2"
	first.Response.BodyInfo = &coverage.BodyAvailability{Present: true, RetainedBytes: 79}
	second := first
	second.FlowID = "flow-10"
	in.Report.TestCases[0].Traffic = []coverage.FlowSample{first, second}
	// Keep this test focused on the raw traffic inventory, independently of
	// operation samples, which can contain duplicate or differently grouped flows.
	in.Report.Provided.Specs = nil
	in.Report.Contracts = nil
	in.Arazzo = nil
	model, err := makePage(in)
	if err != nil {
		t.Fatal(err)
	}
	var attempt *attemptView
	for _, item := range model.Tests {
		if item.ID == first.TestCaseID {
			attempt = item
		}
	}
	if attempt == nil || len(attempt.Flows) != 2 {
		t.Fatalf("missing flow inventory: %+v", attempt)
	}
	if attempt.Flows[0].ID != "flow-2" || attempt.Flows[1].ID != "flow-10" {
		t.Fatalf("flow order changed: %+v", attempt.Flows)
	}
	note := attempt.Flows[0].Response.BodyNote
	if note != "79 retained bytes" {
		t.Fatalf("missing original size should stay unknown: %q", note)
	}
	if got := message(coverage.InteractionMessage{BodyInfo: &coverage.BodyAvailability{Present: true, RetainedBytes: 4, OriginalBytes: 200}}).BodyNote; got != "4 retained bytes; 200 original bytes" {
		t.Errorf("recorded original size was lost: %s", got)
	}
}

func TestRendererPreservesOriginalSourceExtensionsAndFormatting(t *testing.T) {
	in := rendererFixture()
	in.WiretapJSON = "\n{\r\n  \"custom-field\": \"wiretap original\"\r\n}\r\n"
	in.ArazzoJSON = "{\r\n\t\"arazzo\": \"1.0.1\",\r\n\t\"x-custom-step\": \"</pre><script>unsafe()</script>\"\r\n}\r\n"
	model, err := makePage(in)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"source-wiretap": in.WiretapJSON, "source-arazzo": in.ArazzoJSON}
	for _, s := range model.Sources {
		if want, ok := expected[s.ID]; ok && s.Content != want {
			t.Errorf("source %s lost original formatting or extensions", s.ID)
		}
	}
	doc := parsed(t, renderFixture(t, in))
	seen := map[string]bool{}
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		id := attr(n, "id")
		want, ok := expected[id]
		if !ok {
			return
		}
		seen[id] = true
		// Match the safe local download path. A JSON attribute retains CRLF and
		// the leading newline that HTML parsing would otherwise normalize.
		got := content(n)
		if original := attr(n, "data-original-json"); original != "" {
			if err := json.Unmarshal([]byte(original), &got); err != nil {
				t.Fatal(err)
			}
		}
		if got != want {
			t.Errorf("download source %s changed: got %q, want %q", id, got, want)
		}
	})
	for id := range expected {
		if !seen[id] {
			t.Errorf("missing original source %s", id)
		}
	}
}
