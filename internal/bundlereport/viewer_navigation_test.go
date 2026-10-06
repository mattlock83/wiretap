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

// Repeated test executions must keep distinct evidence and navigation anchors.
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
	for _, item := range []struct{ id, name string }{
		{"attempt-one", "create order"}, {"attempt-two", "create order"}, {"attempt-three", "create another order"},
	} {
		tc := artifact.TestCase{ID: item.id, Name: item.name, Status: "pass", StartedAt: time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)}
		frozen, err := w.Register(tc, refs)
		if err != nil {
			t.Fatal(err)
		}
		a := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: frozen, SchemaValidation: &artifact.SchemaValidation{Status: "pass"}, Traffic: []artifact.Flow{{
			ID: "request", Protocol: "http", Channel: "test_to_sut", StartedAt: tc.StartedAt,
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://orders/orders", Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"amount":25}`}}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 201}},
		}}}
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

func TestViewerNavigationGroupsRepeatedExecutions(t *testing.T) {
	root := viewerNavigationFixture(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/viewer.html"))
	if err != nil {
		t.Fatal(err)
	}
	elements := viewerNavigationElements(t, string(data))
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
			t.Errorf("details expanded by default: %s", n.attrs)
		}
	}
	if len(groups) != 2 || len(attempts) != 3 {
		t.Fatalf("test grouping lost executions: %d groups, %d attempts", len(groups), len(attempts))
	}
	one := attempts["attempt-"+shortHash("attempt-one")]
	two := attempts["attempt-"+shortHash("attempt-two")]
	three := attempts["attempt-"+shortHash("attempt-three")]
	if one == nil || two == nil || three == nil || one.parent != two.parent || one.parent == three.parent {
		t.Fatal("repeated executions were not grouped by test identity")
	}
}

func TestViewerNavigationMismatchCountsVisibleBeforeExpansion(t *testing.T) {
	for _, unexpected := range []bool{false, true} {
		name := "no mismatches"
		if unexpected {
			name = "response mismatches"
		}
		t.Run(name, func(t *testing.T) {
			spec := &coverage.ContractSpec{APIID: "orders", SchemaID: "orders", Origin: "provided", TargetKind: "service", Revision: "revision", Targets: []*coverage.ContractTarget{{
				Key: "covered-status", Operation: "POST /orders", Phase: "response", Family: "response_status", Condition: "201", State: "covered",
			}}}
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
						t.Errorf("%s hides mismatches or counts unrelated observations: %s", name, summary)
					}
				} else if len(counts) != 0 {
					t.Errorf("%s labels a valid contract as contract mismatches", name)
				}
			}
		})
	}
}

func TestViewerNavigationIncompleteCaptureDoesNotClaimResponseValidation(t *testing.T) {
	tpl, err := template.New("viewer").Parse(viewerHTML + viewerSections)
	if err != nil {
		t.Fatal(err)
	}
	attempt := viewerAttempt{ID: "transport-failure", Anchor: "attempt-transport-failure", Status: "error", ValidationStatus: "unknown", Incomplete: true, Attention: true}
	var rendered strings.Builder
	if err := tpl.ExecuteTemplate(&rendered, "testRun", attempt); err != nil {
		t.Fatal(err)
	}
	page := rendered.String()
	if !strings.Contains(page, "Capture is incomplete") {
		t.Fatal("incomplete evidence is not explained")
	}
	if strings.Contains(page, "response was checked") || strings.Contains(page, "Contract mismatch") {
		t.Fatal("incomplete capture invents completed validation or a contract mismatch")
	}
}

func TestViewerNavigationLinksResolveAndRebuildPreservesEvidence(t *testing.T) {
	for _, fixture := range []struct {
		name string
		make func(*testing.T) string
	}{{"repeated test executions", viewerNavigationFixture}, {"provider and dependency", knowledgeFixture}} {
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
			data, err := os.ReadFile(filepath.Join(root, "docs/viewer.html"))
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
				for _, kind := range []string{"attempt-", "cov-"} {
					if strings.HasPrefix(id, kind) {
						kinds[kind]++
					}
				}
			}
			if kinds["attempt-"] == 0 || kinds["cov-"] == 0 {
				t.Fatalf("fixture lacks reciprocal test/coverage links: %v", kinds)
			}
		})
	}
}
