package bundlereport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/coverage"
)

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
			{Operation: operation, Expected: true, Reason: "deliberately invalid request", Evidence: evidence},
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
	if view.ExpectedViolations != 1 || view.UnexpectedViolations != 2 || !view.Attention {
		t.Fatalf("expected negative inputs were conflated with mismatches: %+v", view)
	}
	op := view.Operations[1]
	if op.Name != operation || len(op.Groups) != 3 || op.Groups[0].Label != "Request" || op.Groups[1].Label != "Response" || !op.Groups[2].Limits {
		t.Fatalf("unexpected groups: %+v", op)
	}
	if len(op.Violations) != 1 || len(op.ExpectedViolations) != 1 || !view.Operations[0].Attention {
		t.Fatalf("mismatch-only operation or expected input lost: %+v", view.Operations)
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

func TestViewerRejectionsKeepExecutionFailuresSeparate(t *testing.T) {
	evidence := coverage.ContractEvidence{AttemptID: "case-one", TestName: "withdrawal invalid amount", Outcome: "fail", Reason: "Generated case did not satisfy rejection checks (HTTP 401)"}
	report := &coverage.RejectionCoverage{Targets: []*coverage.RejectionCoverageTarget{
		{RejectionTarget: artifact.RejectionTarget{Key: "a", SchemaID: "banking", Operation: "POST /withdraw", Location: "/body/amount", Constraint: "minimum", ExpectedStatuses: []int{400}}, State: "covered"},
		{RejectionTarget: artifact.RejectionTarget{Key: "b", SchemaID: "banking", Operation: "POST /withdraw", Location: "/body/accountId", Constraint: "type", ExpectedStatuses: []int{400}}, State: "failed", Evidence: []coverage.ContractEvidence{evidence}},
		{RejectionTarget: artifact.RejectionTarget{Key: "c", SchemaID: "banking", Operation: "POST /withdraw", Location: "/body", Constraint: "required", ExpectedStatuses: []int{400}}, State: "not_covered"},
		{RejectionTarget: artifact.RejectionTarget{Key: "d", SchemaID: "other", Operation: "POST /withdraw", Location: "/query/id", Constraint: "type", ExpectedStatuses: []int{422}}, State: "covered"},
	}}
	before, _ := json.Marshal(report)
	views := buildViewerRejections(report)
	after, _ := json.Marshal(report)
	if string(before) != string(after) {
		t.Fatal("viewer changed rejection evidence")
	}
	if len(views) != 2 {
		t.Fatalf("distinct API schemas merged: %+v", views)
	}
	op := views[0]
	if op.Covered != 1 || op.Failed != 1 || op.NotCovered != 1 || !op.Attention {
		t.Fatalf("failed authentication counted as rejection coverage: %+v", op)
	}
	if !strings.Contains(op.Search, "HTTP 401") || !strings.Contains(op.Search, "withdrawal invalid amount") {
		t.Fatalf("execution evidence not searchable: %s", op.Search)
	}
	for _, row := range op.Rows {
		if row.Key == "b" && (row.Label != "wrong value type" || row.Field != "Body · accountId" || row.Evidence[0].TestURL != "#attempt-"+shortHash("case-one")) {
			t.Fatalf("invalid input description/link lost: %+v", row)
		}
	}
	if views[1].Attention || !strings.HasPrefix(op.ID, "rejection-") {
		t.Fatalf("unexpected attention or unstable ID: %+v", views)
	}
}

func TestViewerCoverageEmptyReports(t *testing.T) {
	if buildViewerCoverage(nil) != nil || buildViewerRejections(nil) != nil {
		t.Fatal("nil coverage should produce no view")
	}
	view := buildViewerCoverage(&coverage.ContractCoverage{Specs: []*coverage.ContractSpec{nil, {APIID: "missing-spec", Notes: []string{"No supplied contract"}}}})
	if len(view) != 1 || len(view[0].Operations) != 0 || view[0].DenominatorKnown || !reflect.DeepEqual(view[0].Counts, coverage.ContractCounts{}) {
		t.Fatalf("unknown denominator was presented as measured coverage: %+v", view)
	}
}

func TestNativeRejectionViewsUseApplicationStatusAndExactWitnesses(t *testing.T) {
	index := 0
	target := artifact.RejectionTarget{Key: "native-target", Protocol: "grpc", SchemaID: "orders", Operation: "/orders.v1.Orders/CreateOrder", Location: "/messages/0/body/sku", MessageIndex: &index, Constraint: "string.min_len", ExpectedStatuses: []int{3, 11}}
	evidence := coverage.ContractEvidence{AttemptID: "case-two", InstanceLocation: "/messages/1/body/sku", Reason: "Intended request violation verified (gRPC status 3)"}
	report := &coverage.RejectionCoverage{Covered: 1, Targets: []*coverage.RejectionCoverageTarget{{RejectionTarget: target, State: "covered", Evidence: []coverage.ContractEvidence{evidence}}}}
	view := buildViewerRejections(report)
	row := view[0].Rows[0]
	if row.StatusLabel != "gRPC INVALID_ARGUMENT (3) or OUT_OF_RANGE (11)" || row.Field != "Message 1 · sku" || row.Label != "string too short" {
		t.Fatalf("native target lost its meaning: %+v", row)
	}
	if row.Evidence[0].InstanceLocation != evidence.InstanceLocation {
		t.Fatal("later stream witness replaced with catalogue representative")
	}
	markdown := contractCoverageMarkdown(&coverage.ContractCoverage{Rejections: report})
	if !strings.Contains(markdown, row.StatusLabel) || !strings.Contains(markdown, "/messages/1/body/sku") || strings.Contains(markdown, "HTTP [3") {
		t.Fatalf("native markdown status or witness is incorrect: %s", markdown)
	}
}
