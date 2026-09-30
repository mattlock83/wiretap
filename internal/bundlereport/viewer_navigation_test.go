package bundlereport

import (
	"encoding/json"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
)

// This fixture has identically named seeds whose content hashes differ. They
// must remain separate exploration groups even when their source IDs match.
func viewerNavigationFixture(t *testing.T) string {
	t.Helper()
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(bundle.Identity{APIID: "orders", SUTTargetKind: "service"}); err != nil {
		t.Fatal(err)
	}
	refs := []artifact.SchemaRef{{ID: "orders", AssetID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: Orders, version: "1"}
paths:
  /orders:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [amount]
              properties:
                amount: {type: number, minimum: 0, exclusiveMinimum: true}
      responses:
        '201': {description: created}
        '400': {description: invalid input}
`}}}
	target := artifact.RejectionTarget{Key: strings.Repeat("c", 64), SchemaID: "orders", Operation: "POST /orders", FlowID: "request", Location: "/body/amount", Constraint: "nullable", ExpectedStatuses: []int{400}}
	for _, item := range []struct{ id, mode, hash string }{
		{"authored", "", ""},
		{"baseline-a", "baseline", strings.Repeat("a", 64)},
		{"case-a", "rejection", strings.Repeat("a", 64)},
		{"baseline-b", "baseline", strings.Repeat("b", 64)},
		{"case-b", "rejection", strings.Repeat("b", 64)},
	} {
		tc := artifact.TestCase{ID: item.id, Name: "create order", Status: "pass", StartedAt: time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)}
		if item.mode != "" {
			tc.Name += " [" + item.mode + "]"
		}
		frozen, err := w.Register(tc, refs)
		if err != nil {
			t.Fatal(err)
		}
		a := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen, SchemaValidation: &artifact.SchemaValidation{Status: "pass"}, Traffic: []artifact.Flow{{
			ID: "request", Protocol: "http", Channel: "test_to_sut", StartedAt: tc.StartedAt,
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://orders/orders", Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"amount":25}`}}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 201}},
		}}}
		if item.mode != "" {
			a.Exploration = &artifact.Exploration{Version: "wiretap-negative-1", Mode: item.mode, SourceRunID: "source-run", SourceAttemptID: "source-attempt", SourceSHA256: item.hash}
			if item.mode == "baseline" {
				a.Exploration.Catalogue = []artifact.RejectionTarget{target}
			} else {
				a.Exploration.Target = &target
				a.Exploration.RequestVerified = true
				a.SchemaValidation.Status = "expected_rejection"
				a.Traffic[0].Request.HTTP.Body.Text = `{"amount":null}`
				a.Traffic[0].Response.HTTP.Status = 400
				a.Assertions = []artifact.Assertion{
					{ID: "expected-rejection", FlowID: "request", Operator: "status_in", Result: "pass"},
					{ID: "response-schema-request", FlowID: "request", Operator: "schema", Result: "pass"},
				}
			}
		}
		if _, err := w.Commit(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(w.Path()); err != nil {
		t.Fatal(err)
	}
	return w.Path()
}

// Inspect the structural elements used by the viewer without introducing an
// HTML parser dependency. User content is escaped before reaching these tags.
type viewerNavigationElement struct {
	tag, attrs string
	start, end int
	parent     *viewerNavigationElement
}

func viewerNavigationElements(t *testing.T, page string) []*viewerNavigationElement {
	t.Helper()
	var all, stack []*viewerNavigationElement
	for _, match := range regexp.MustCompile(`</?(?:section|article|details)\b[^>]*>`).FindAllStringIndex(page, -1) {
		tag := page[match[0]:match[1]]
		if strings.HasPrefix(tag, "</") {
			if len(stack) == 0 {
				t.Fatalf("unbalanced viewer element: %s", tag)
			}
			n := stack[len(stack)-1]
			if tag != "</"+n.tag+">" {
				t.Fatalf("viewer closes %s with %s", n.tag, tag)
			}
			n.end = match[1]
			stack = stack[:len(stack)-1]
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">"), " ", 2)
		n := &viewerNavigationElement{tag: parts[0], start: match[0]}
		if len(parts) > 1 {
			n.attrs = parts[1]
		}
		if len(stack) > 0 {
			n.parent = stack[len(stack)-1]
		}
		all = append(all, n)
		stack = append(stack, n)
	}
	if len(stack) != 0 {
		t.Fatal("viewer has unclosed structural elements")
	}
	return all
}

func (n *viewerNavigationElement) attribute(name string) string {
	match := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(n.attrs)
	if len(match) == 0 {
		return ""
	}
	return html.UnescapeString(match[1])
}

func TestViewerNavigationGroupsExecutionsAndExplainsNegativeChecks(t *testing.T) {
	root := viewerNavigationFixture(t)
	data, err := os.ReadFile(filepath.Join(root, "viewer/index.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	elements := viewerNavigationElements(t, page)
	groups, attempts := map[string]*viewerNavigationElement{}, map[string]*viewerNavigationElement{}
	for _, n := range elements {
		id := n.attribute("id")
		if strings.HasPrefix(id, "test-group-") {
			groups[id] = n
		}
		if strings.HasPrefix(id, "attempt-") {
			attempts[id] = n
		}
		if n.tag == "details" && regexp.MustCompile(`(?:^|\s)open(?:\s|=|$)`).MatchString(n.attrs) {
			t.Errorf("viewer exposes expanded details by default: %s", n.attrs)
		}
	}
	if len(groups) != 3 || len(attempts) != 5 {
		t.Fatalf("authored runs or distinct source hashes were merged: %d groups, %d attempts", len(groups), len(attempts))
	}
	for _, seed := range []string{"a", "b"} {
		baseline := attempts["attempt-"+shortHash("baseline-"+seed)]
		negative := attempts["attempt-"+shortHash("case-"+seed)]
		if baseline == nil || negative == nil {
			t.Fatal("generated runs lost their stable navigation anchors")
		}
		// Baselines are direct members of the generated group. Mutations are
		// nested in a separate disclosure inside that same group.
		if baseline.parent == nil || baseline.parent.tag != "article" || negative.parent == nil || negative.parent.tag != "details" || negative.parent.parent != baseline.parent {
			t.Fatalf("baseline and invalid-input checks are not separate in source group %s", seed)
		}
		body := page[negative.start:negative.end]
		if !strings.Contains(body, "deliberately invalid") || strings.Contains(body, "Contract mismatch") {
			t.Fatal("an expected invalid request is presented as a contract defect")
		}
		if strings.Contains(body, "response was checked") {
			t.Fatal("request-validation status alone must not claim response checks completed")
		}
		if !strings.Contains(body, "Body · amount") || !strings.Contains(body, "null where disallowed") {
			t.Fatal("generated check lacks a readable field and mutation description")
		}
		if !strings.Contains(page[baseline.start:baseline.end], "Happy-path baseline") {
			t.Fatal("generated baseline is not identified separately")
		}
	}
	if authored := attempts["attempt-"+shortHash("authored")]; authored == nil || authored.parent == nil || !strings.Contains(page[authored.parent.start:authored.parent.end], "Authored tests") {
		t.Fatal("authored test does not have its own group")
	}
}

func TestViewerNavigationMismatchCountsVisibleBeforeExpansion(t *testing.T) {
	for _, unexpected := range []bool{false, true} {
		name := "expected inputs only"
		if unexpected {
			name = "unexpected responses as well"
		}
		t.Run(name, func(t *testing.T) {
			spec := &coverage.ContractSpec{APIID: "orders", SchemaID: "orders", Origin: "provided", TargetKind: "service", Revision: "revision", Targets: []*coverage.ContractTarget{{
				Key: "covered-status", Operation: "POST /orders", Phase: "response", Family: "response_status", Condition: "201", State: "covered",
			}}}
			for i := 0; i < 3; i++ {
				spec.Violations = append(spec.Violations, coverage.ContractViolation{Operation: "POST /orders", Expected: true, Reason: "intentionally invalid input"})
			}
			if unexpected {
				for i := 0; i < 2; i++ {
					spec.Violations = append(spec.Violations, coverage.ContractViolation{Operation: "POST /orders", Reason: "response does not match the contract"})
				}
			}
			contracts := &coverage.ContractCoverage{Complete: true, Specs: []*coverage.ContractSpec{spec}}
			view := viewerData{Contracts: contracts, ContractSpecs: buildViewerCoverage(contracts)}
			tpl, err := template.New("viewer").Parse(viewerHTML + viewerSections)
			if err != nil {
				t.Fatal(err)
			}
			var rendered strings.Builder
			if err := tpl.ExecuteTemplate(&rendered, "contractPane", view); err != nil {
				t.Fatal(err)
			}
			page := rendered.String()
			var cardHeader, operationSummary string
			for _, element := range viewerNavigationElements(t, page) {
				body := page[element.start:element.end]
				if element.tag == "article" && strings.HasPrefix(element.attribute("data-api-search"), "orders ") {
					cardHeader = strings.SplitN(body, "<details", 2)[0]
				}
				if element.attribute("id") == view.ContractSpecs[0].Operations[0].ID {
					operationSummary = strings.SplitN(body, "</summary>", 2)[0]
				}
			}
			if cardHeader == "" || operationSummary == "" {
				t.Fatal("API card or operation summary was not rendered")
			}
			for name, summary := range map[string]string{"API card": cardHeader, "operation": operationSummary} {
				counts := regexp.MustCompile(`>(\d+) contract mismatches<`).FindAllStringSubmatch(summary, -1)
				if unexpected {
					if len(counts) != 1 || counts[0][1] != "2" {
						t.Errorf("%s hides mismatches or counts expected negative inputs: %s", name, summary)
					}
				} else if len(counts) != 0 {
					t.Errorf("%s labels deliberately invalid inputs as contract mismatches", name)
				}
			}
		})
	}
}

func TestViewerNavigationIncompleteNegativeCaseDoesNotClaimResponseValidation(t *testing.T) {
	tpl, err := template.New("viewer").Parse(viewerHTML + viewerSections)
	if err != nil {
		t.Fatal(err)
	}
	// Exploration records expected_rejection after checking the mutated request,
	// before sending it. A transport failure can therefore have no response check.
	attempt := viewerAttempt{ID: "transport-failure", Anchor: "attempt-transport-failure", Status: "error", ValidationStatus: "expected_rejection", Incomplete: true, Attention: true}
	var rendered strings.Builder
	if err := tpl.ExecuteTemplate(&rendered, "testRun", attempt); err != nil {
		t.Fatal(err)
	}
	page := rendered.String()
	if !strings.Contains(page, "deliberately invalid") || !strings.Contains(page, "Capture is incomplete") {
		t.Fatal("negative request or incomplete evidence is not explained")
	}
	if strings.Contains(page, "response was checked") || strings.Contains(page, "Contract mismatch") {
		t.Fatal("incomplete negative case invents completed validation or a contract mismatch")
	}
}

func TestViewerNavigationLinksResolveAndRebuildPreservesEvidence(t *testing.T) {
	for _, fixture := range []struct {
		name string
		make func(*testing.T) string
	}{{"authored and generated", viewerNavigationFixture}, {"provider and dependency", knowledgeFixture}} {
		t.Run(fixture.name, func(t *testing.T) {
			root := fixture.make(t)
			run, captures, err := bundle.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal([]any{run, captures})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Rebuild(root); err != nil {
				t.Fatal(err)
			}
			afterRun, afterCaptures, err := bundle.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal([]any{afterRun, afterCaptures})
			if err != nil || string(before) != string(after) {
				t.Fatal("viewer regeneration changed canonical evidence")
			}
			data, err := os.ReadFile(filepath.Join(root, "viewer/index.html"))
			if err != nil {
				t.Fatal(err)
			}
			page := string(data)
			ids := map[string]int{}
			for _, match := range regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(page, -1) {
				ids[html.UnescapeString(match[1])]++
			}
			kinds := map[string]int{}
			for _, match := range regexp.MustCompile(`\bhref="#([^"]+)"`).FindAllStringSubmatch(page, -1) {
				id := html.UnescapeString(match[1])
				if ids[id] != 1 {
					t.Errorf("in-page link %q resolves to %d targets", id, ids[id])
				}
				for _, kind := range []string{"attempt-", "cov-", "rejection-"} {
					if strings.HasPrefix(id, kind) {
						kinds[kind]++
					}
				}
			}
			if kinds["attempt-"] == 0 || kinds["cov-"] == 0 || fixture.name == "authored and generated" && kinds["rejection-"] == 0 {
				t.Fatalf("fixture lacks reciprocal test/coverage links: %v", kinds)
			}
		})
	}
}
