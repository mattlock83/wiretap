package bundlereport

import (
	"encoding/json"
	"html/template"
	"reflect"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/coverage"
)

func TestViewerCombinationsPreserveMessageWitnessesAndCounts(t *testing.T) {
	first := &coverage.ContractTarget{Key: "enum", OperationKey: "op", Operation: "/orders.Orders/Create", Phase: "request", FieldPath: "Order.state", Family: "enum", Condition: "PENDING", State: "covered"}
	second := &coverage.ContractTarget{Key: "presence", OperationKey: "op", Operation: first.Operation, Phase: "request", FieldPath: "Order.note", Family: "presence", Condition: "absent", State: "not_covered"}
	index := 2
	evidence := coverage.ContractEvidence{AttemptID: "failed-attempt", TestName: "create order", Path: "tests/failed-attempt.json", FlowID: "stream", Phase: "request", MessageIndex: &index, InstanceLocation: "/messages/2/body", Outcome: "fail", Validation: "valid", Result: "observed"}
	spec := &coverage.ContractSpec{APIID: "orders", Targets: []*coverage.ContractTarget{first, second}, Combinations: []coverage.ContractCombination{{Fingerprint: "fingerprint", OperationKey: "op", Phase: "request", Hits: []coverage.ConditionHit{{Key: first.Key, InstanceLocation: "/messages/2/body/state"}, {Key: second.Key, InstanceLocation: "/messages/2/body/note"}}, Evidence: evidence}}}
	report := &coverage.ContractCoverage{Specs: []*coverage.ContractSpec{spec}}
	before, _ := json.Marshal(report)
	views := buildViewerCoverage(report)
	after, _ := json.Marshal(report)
	if string(before) != string(after) || views[0].Counts.Covered != 1 || views[0].Counts.NotCovered != 1 {
		t.Fatal("presenting observed combinations changed measured coverage")
	}
	combination := views[0].Operations[0].Combinations[0]
	if combination.Evidence.Outcome != "fail" || combination.Evidence.MessageIndex == nil || *combination.Evidence.MessageIndex != 2 || combination.Hits[0].Location != "/messages/2/body/note" {
		t.Fatal("combination lost its failed attempt or exact stream witness")
	}
	for _, term := range []string{"fingerprint", "create order", "Order.note", "omitted", "/messages/2/body/note"} {
		if !strings.Contains(combination.Search, term) {
			t.Errorf("combination search missing %q", term)
		}
	}
	tpl, err := template.New("viewer").Parse(viewerHTML + viewerSections)
	if err != nil {
		t.Fatal(err)
	}
	var rendered strings.Builder
	if err := tpl.ExecuteTemplate(&rendered, "contractPane", viewerData{Contracts: report, ContractSpecs: views}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), `class="technical filter-leaf" data-state="observed" data-attention="true" data-search=`) {
		t.Fatal("combination is not connected to search and coverage filters")
	}
	for _, output := range []string{rendered.String(), contractCoverageMarkdown(report)} {
		for _, expected := range []string{"Observed case combinations", "Order.note", "omitted", "/messages/2/body/note", "fingerprint", "fail"} {
			if !strings.Contains(output, expected) {
				t.Errorf("combination view missing %q", expected)
			}
		}
	}
	// A passing observation elsewhere may cover every individual condition;
	// that must not promote this failed message to a covered combination.
	second.State = "covered"
	passing := spec.Combinations[0]
	passing.Evidence.AttemptID = "passing-attempt"
	passing.Evidence.Outcome, passing.Evidence.Result = "pass", "covered"
	spec.Combinations = append(spec.Combinations, passing)
	view := buildViewerCoverage(report)[0]
	op := view.Operations[0]
	if view.Counts.Covered != 2 || !view.Attention || !op.Attention || len(op.Combinations) != 2 || !op.Combinations[0].Attention || op.Combinations[1].Attention {
		t.Fatal("observations were merged or their outcomes were inferred from aggregate coverage")
	}
}

func TestViewerCoverageGroupsWithoutChangingMeasurement(t *testing.T) {
	const operation = "POST /transactions"
	evidence := coverage.ContractEvidence{AttemptID: "attempt-one", TestName: "withdrawal succeeds", FlowID: "exchange-one", Outcome: "pass", Path: "evidence/attempt-one.json", InstanceLocation: "/body/amount"}
	makeTarget := func(key, state, phase, family, condition string) *coverage.ContractTarget {
		return &coverage.ContractTarget{Key: key, OperationKey: "operation-key", Operation: operation, Phase: phase, Location: "/paths/~1transactions/post/requestBody/content/application~1json/schema/properties/amount", Family: family, Condition: condition, State: state, Evidence: []coverage.ContractEvidence{evidence}}
	}
	spec := &coverage.ContractSpec{
		APIID: "banking", SchemaID: "banking-spec", Revision: "revision-one", Origin: "provided", TargetKind: "service", DenominatorKnown: false,
		Targets: []*coverage.ContractTarget{
			makeTarget("covered-key", "covered", "request", "number", "interior"),
			makeTarget("missing-key", "not_covered", "request", "nullable", "null"),
			makeTarget("unknown-key", "unknown", "response", "enum", `"PENDING"`),
			makeTarget("unsupported-key", "unsupported", "response", "unsupported", "format has no measurement"),
			makeTarget("excluded-key", "excluded", "response", "array_length", "interior"),
		},
		// Real coverage counts index each target under both phase and family.
		// Presentation must not sum these duplicated aggregates.
		Counts: map[string]coverage.ContractCounts{"phase:request": {Covered: 1, NotCovered: 1}, "family:number": {Covered: 1}, "family:nullable": {NotCovered: 1}},
		Violations: []coverage.ContractViolation{
			{Operation: operation, Reason: "unexpected response mismatch", Evidence: evidence},
			{Operation: "GET /undeclared", Reason: "undeclared operation", Evidence: evidence},
		},
	}
	report := &coverage.ContractCoverage{Specs: []*coverage.ContractSpec{spec}}
	before, _ := json.Marshal(report)
	views := buildViewerCoverage(report)
	after, _ := json.Marshal(report)
	if string(before) != string(after) {
		t.Fatal("viewer modified canonical coverage")
	}
	if len(views) != 1 || len(views[0].Operations) != 2 {
		t.Fatalf("operation grouping lost evidence: %+v", views)
	}
	view := views[0]
	if view.Counts != (coverage.ContractCounts{Covered: 1, NotCovered: 1, Unknown: 1, Unsupported: 1, Excluded: 1}) {
		t.Fatalf("targets counted incorrectly: %+v", view.Counts)
	}
	if view.ViolationCount != 2 || !view.Attention {
		t.Fatalf("contract mismatch count is incorrect: %+v", view)
	}
	op := view.Operations[1]
	if op.Name != operation || len(op.Groups) != 3 || op.Groups[0].Label != "Request" || op.Groups[1].Label != "Response" || !op.Groups[2].Limits {
		t.Fatalf("unexpected groups: %+v", op)
	}
	if len(op.Violations) != 1 || !view.Operations[0].Attention {
		t.Fatalf("mismatch-only operation or evidence lost: %+v", view.Operations)
	}
	if op.Violations[0].Evidence.TestURL != "#attempt-"+shortHash(evidence.AttemptID) {
		t.Fatalf("mismatch has no linked test: %+v", op.Violations)
	}
	seen := map[string]bool{}
	for _, group := range op.Groups {
		for _, row := range group.Rows {
			seen[row.Key] = true
			if row.Attention != (row.State != "covered" && row.State != "excluded") {
				t.Fatalf("incorrect attention state: %+v", row)
			}
			if len(row.Evidence) != 1 || row.Evidence[0].TestURL != "#attempt-"+shortHash(evidence.AttemptID) {
				t.Fatalf("condition lost test evidence: %+v", row)
			}
		}
	}
	if len(seen) != len(spec.Targets) {
		t.Fatalf("target lost in grouping: %v", seen)
	}
	for _, term := range []string{"banking", "PENDING", "amount", "exchange-one", "withdrawal succeeds", "undeclared operation", "covered-key"} {
		if !strings.Contains(view.Search, term) {
			t.Fatalf("deep search lost %q", term)
		}
	}
	// IDs describe the API operation, rather than its order or current results.
	stableID := op.ID
	spec.Targets[0].State = "not_covered"
	spec.Targets[0], spec.Targets[4] = spec.Targets[4], spec.Targets[0]
	if next := buildViewerCoverage(report)[0].Operations[1].ID; next != stableID {
		t.Fatalf("operation link changed with evidence/order: %s != %s", stableID, next)
	}
}

func TestViewerConditionLabels(t *testing.T) {
	for _, tc := range []struct{ location, phase, expected string }{
		{"/paths/~1transactions/post", "operation", "Operation"},
		{"/paths/~1transactions/post/requestBody", "request", "Body"},
		{"/paths/~1transactions/post/requestBody/content/application~1json/schema/properties/amount", "request", "Body · amount"},
		{"/paths/~1transactions/get/parameters/query/accountId/schema", "request", "Query · accountId"},
		{"/paths/~1transactions~1{id}/get/parameters/path/id", "request", "Path · id"},
		{"/paths/~1transactions/get/responses/200", "response", "200 response"},
		{"/paths/~1transactions/get/responses/200/headers/X-Result/schema", "response", "200 header · X-Result"},
		{"/paths/~1transactions/get/responses/200/content/application~1json/schema/items/properties/account/properties/name", "response", "200 body · [].account.name"},
		{"/paths/~1transactions/post/requestBody/content/application~1json/schema/properties/accounts/items/properties/name", "request", "Body · accounts[].name"},
		{"/paths/~1transactions/post/requestBody/content/application~1json/schema/properties/fee~1rate~0/oneOf/0", "request", "Body · fee/rate~ (oneOf branch 1)"},
		{"/paths/~1transactions/post/security", "request", "Authentication"},
		{"/openapi", "", "Contract"},
	} {
		t.Run(tc.location, func(t *testing.T) {
			if actual := viewerConditionField(tc.location, tc.phase); actual != tc.expected {
				t.Errorf("got %q, want %q", actual, tc.expected)
			}
		})
	}
	for _, tc := range []struct{ family, condition, expected string }{
		{"presence", "absent", "omitted"},
		{"enum", `"EUR"`, `equals "EUR"`},
		{"number", "interior", "inside numeric bounds"},
		{"string_length", "equal:0", "length 0"},
		{"nullable", "non_null", "non-null value"},
		{"response_status", "400", "HTTP 400 observed"},
	} {
		if actual := viewerConditionLabel(&coverage.ContractTarget{Family: tc.family, Condition: tc.condition}); actual != tc.expected {
			t.Errorf("%s/%s label = %q, want %q", tc.family, tc.condition, actual, tc.expected)
		}
	}
}

func TestViewerCoverageEmptyReports(t *testing.T) {
	if buildViewerCoverage(nil) != nil {
		t.Fatal("nil coverage should produce no view")
	}
	view := buildViewerCoverage(&coverage.ContractCoverage{Specs: []*coverage.ContractSpec{nil, {APIID: "missing-spec", Notes: []string{"No supplied contract"}}}})
	if len(view) != 1 || len(view[0].Operations) != 0 || view[0].DenominatorKnown || !reflect.DeepEqual(view[0].Counts, coverage.ContractCounts{}) {
		t.Fatalf("unknown denominator was presented as measured coverage: %+v", view)
	}
}
