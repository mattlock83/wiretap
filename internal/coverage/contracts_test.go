package coverage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

const conditionsSpec = `openapi: 3.0.3
info: {title: Orders, version: "1"}
paths:
  /orders:
    post:
      parameters:
        - {name: mode, in: query, schema: {type: string, enum: [fast, slow], default: fast}}
      requestBody:
        required: true
        content:
          application/json:
            schema: {type: object, required: [name], properties: {name: {type: string, minLength: 1}}}
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [status, items, reference]
                properties:
                  status: {type: string, enum: [pending, shipped, cancelled]}
                  items: {type: array, minItems: 0, maxItems: 2, items: {type: string, enum: [a, b]}}
                  reference: {type: string, pattern: '^ORD-[0-9]{3}$'}
                  note: {type: string, minLength: 1, maxLength: 3}
                  parent: {type: object, properties: {child: {type: string}}}
        '4XX': {description: rejected}
        default: {description: fallback}
  /untested:
    get:
      responses:
        '204': {description: empty}
`

func contractFixture(schema, body string) (*bundle.Run, *artifact.Artifact) {
	ref := artifact.SchemaRef{ID: "orders-schema", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: schema}}
	a := &artifact.Artifact{Version: 1, TestCase: artifact.TestCase{ID: "attempt", Name: "creates order", Status: "pass"}, Schemas: []artifact.SchemaRef{ref}, Traffic: []artifact.Flow{{ID: "flow", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://provider/orders", Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: `{"name":"example"}`}}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Headers: artifact.Headers{"Content-Type": {"application/json"}}, Body: &artifact.Body{Text: body}}}}}}
	run := &bundle.Run{RunID: "run", APIID: "orders", RunState: "complete", Schemas: []artifact.SchemaRef{ref}, Attempts: []bundle.AttemptRef{{AttemptID: "attempt", FileRef: bundle.FileRef{Path: "tests/attempt.json"}}}}
	return run, a
}

// Freeze mutable inline fixtures at the same boundary used by production reports.
// Copies let tests change the next observation without mutating prior snapshots.
func measureContractFixture(t *testing.T, run *bundle.Run, captures []*artifact.Artifact) *ContractCoverage {
	t.Helper()
	frozen := map[string]artifact.SchemaRef{}
	freeze := func(refs []artifact.SchemaRef) []artifact.SchemaRef {
		out := append([]artifact.SchemaRef(nil), refs...)
		for i, ref := range out {
			if ref.Source.Type != "inline" {
				continue
			}
			key := schemaKey(ref)
			if _, ok := frozen[key]; !ok {
				frozen[key] = freezeCoverageSchema(t, ref)
			}
			out[i] = frozen[key]
		}
		return out
	}
	copyRun := *run
	copyRun.Schemas = freeze(run.Schemas)
	copyCaptures := make([]*artifact.Artifact, len(captures))
	for i, capture := range captures {
		copyCapture := *capture
		copyCapture.Schemas = freeze(capture.Schemas)
		copyCaptures[i] = &copyCapture
	}
	return buildContractCoverage(&copyRun, copyCaptures)
}

func freezeCoverageSchema(t *testing.T, ref artifact.SchemaRef) artifact.SchemaRef {
	t.Helper()
	if ref.ID == "" {
		ref.ID = "contract"
	}
	if ref.Type == "" {
		ref.Type = "openapi"
	}
	if ref.Role == "" {
		ref.Role = "sut"
	}
	writer, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Register(artifact.TestCase{ID: "fixture"}, []artifact.SchemaRef{ref}); err != nil {
		t.Fatal(err)
	}
	run, _, err := bundle.Load(writer.Path())
	if err != nil {
		t.Fatal(err)
	}
	return run.Schemas[0]
}

func contractTarget(t *testing.T, s *ContractSpec, phase, location, family, condition string) *ContractTarget {
	t.Helper()
	for _, target := range s.Targets {
		if target.Phase == phase && strings.HasSuffix(target.Location, location) && target.Family == family && target.Condition == condition {
			return target
		}
	}
	t.Fatalf("missing %s %s %s %s", phase, location, family, condition)
	return nil
}
func requireTargetState(t *testing.T, s *ContractSpec, phase, location, family, condition, state string) *ContractTarget {
	t.Helper()
	target := contractTarget(t, s, phase, location, family, condition)
	if target.State != state {
		t.Fatalf("%s %s: got %s want %s (%s)", location, condition, target.State, state, target.Reason)
	}
	return target
}
func TestContractConditionsCatalogueAndEvidence(t *testing.T) {
	run, a := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
	report := measureContractFixture(t, run, []*artifact.Artifact{a})
	s := report.Specs[0]
	if !report.Complete || len(s.Violations) != 0 {
		t.Fatalf("unexpected report: %+v", s.Violations)
	}
	requireTargetState(t, s, "response", "/properties/status", "enum", `"pending"`, "covered")
	requireTargetState(t, s, "response", "/properties/status", "enum", `"shipped"`, "not_covered")
	requireTargetState(t, s, "response", "/properties/items", "array_length", "equal:0", "covered")
	requireTargetState(t, s, "response", "/properties/note", "presence", "absent", "covered")
	requireTargetState(t, s, "response", "/properties/note", "string_length", "equal:1", "not_covered")
	child := requireTargetState(t, s, "response", "/properties/parent/properties/child", "presence", "absent", "not_covered")
	if len(child.Evidence) != 0 {
		t.Fatal("absent parent credited its child")
	}
	requireTargetState(t, s, "operation", "/paths/~1untested/get", "operation", "exercised", "not_covered")
	requireTargetState(t, s, "request", "/parameters/query/mode", "presence", "absent", "covered")
	requireTargetState(t, s, "request", "/parameters/query/mode/schema", "enum", `"fast"`, "not_covered")
	requireTargetState(t, s, "response", "/properties/reference", "pattern", "match:^ORD-[0-9]{3}$", "covered")
	for _, target := range s.Targets {
		if target.State == "covered" && (len(target.Evidence) == 0 || target.Evidence[0].FlowID != "flow") {
			t.Fatalf("covered condition lost its evidence: %+v", target)
		}
	}
	first, _ := json.Marshal(report)
	if strings.Contains(string(first), `"combinations"`) || strings.Contains(string(first), `"fingerprint"`) {
		t.Fatal("removed combination data is still emitted")
	}
	second, _ := json.Marshal(measureContractFixture(t, run, []*artifact.Artifact{a}))
	if string(first) != string(second) {
		t.Fatal("catalogue is nondeterministic")
	}
}

func TestContractArrayItemsRetainInstanceEvidence(t *testing.T) {
	run, a := contractFixture(conditionsSpec, `{"status":"pending","items":["a","b","a"],"reference":"ORD-123"}`)
	// Make this a valid array while keeping repeated hits on one condition.
	run.Schemas[0].Source.Content = strings.ReplaceAll(conditionsSpec, "maxItems: 2", "maxItems: 3")
	a.Schemas = run.Schemas
	s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	ta := requireTargetState(t, s, "response", "/items", "enum", `"a"`, "covered")
	tb := requireTargetState(t, s, "response", "/items", "enum", `"b"`, "covered")
	if len(ta.Evidence) != 2 || len(tb.Evidence) != 1 {
		t.Fatal("did not examine every array item")
	}
	if ta.Evidence[0].InstanceLocation != "/body/items/0" || ta.Evidence[1].InstanceLocation != "/body/items/2" || tb.Evidence[0].InstanceLocation != "/body/items/1" {
		t.Fatal("lost array instance relationships")
	}
}

func TestContractQualityAndOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		mutate      func(*bundle.Run, *artifact.Artifact)
	}{
		{"failed", "not_covered", func(_ *bundle.Run, a *artifact.Artifact) { a.TestCase.Status = "fail" }},
		{"incomplete", "not_covered", func(_ *bundle.Run, a *artifact.Artifact) { a.CaptureIncomplete = true }},
		{"transport", "not_covered", func(_ *bundle.Run, a *artifact.Artifact) { a.Traffic[0].TransportError = "connection failed" }},
		{"truncated", "unknown", func(_ *bundle.Run, a *artifact.Artifact) { a.Traffic[0].Response.HTTP.Body.Truncated = true }},
		{"redacted", "unknown", func(_ *bundle.Run, a *artifact.Artifact) {
			a.Redactions = []artifact.Redaction{{Field: "response.body.status", Rule: "mask", Replacement: "pending"}}
		}},
		{"invalid", "not_covered", func(_ *bundle.Run, a *artifact.Artifact) {
			a.Traffic[0].Response.HTTP.Body.Text = `{"status":"pending","items":[],"reference":"invalid"}`
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, a := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
			tc.mutate(run, a)
			s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
			target := requireTargetState(t, s, "response", "/properties/status", "enum", `"pending"`, tc.state)
			if len(target.Evidence) != 1 {
				t.Fatal("missing evidence of observation or unknown")
			}
			if tc.name == "invalid" && len(s.Violations) == 0 {
				t.Fatal("missing violation")
			}
		})
	}
}

func TestContractIndependentPhasesAndResponseAlternatives(t *testing.T) {
	run, a := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
	a.Traffic[0].Request.HTTP.Body.Text = `{}`
	s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	requireTargetState(t, s, "response", "/properties/status", "enum", `"pending"`, "covered")
	requireTargetState(t, s, "request", "/requestBody", "presence", "present", "not_covered")
	if len(s.Violations) != 1 || s.Violations[0].Evidence.Phase != "request" {
		t.Fatalf("expected request violation: %+v", s.Violations)
	}
	for _, status := range []int{404, 503} {
		a.Traffic[0].Response.HTTP = &artifact.HttpMessage{Status: status}
		s = measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
		alternative := "4XX"
		if status == 503 {
			alternative = "default"
		}
		requireTargetState(t, s, "response", "/responses/"+alternative, "response_status", alternative, "covered")
	}
}

func TestContractProviderMockKeysAndContractRevisions(t *testing.T) {
	run, a := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
	provider := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	run.APIID = "consumer"
	run.DependencyKinds = map[string]string{"orders": "mock"}
	run.Schemas[0].Role = "dependency"
	run.Schemas[0].AssetID = "orders"
	a.Schemas = run.Schemas
	a.Traffic[0].Channel = "sut_to_dependency"
	a.Traffic[0].AssetID = "orders"
	consumer := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	if len(provider.Targets) != len(consumer.Targets) || consumer.TargetKind != "mock" {
		t.Fatal("different catalogue")
	}
	for i, target := range provider.Targets {
		if target.Key != consumer.Targets[i].Key || target.OperationKey != consumer.Targets[i].OperationKey {
			t.Fatal("capture origin leaked into shared keys")
		}
	}
	run.Schemas[0].Source.Content = strings.ReplaceAll(conditionsSpec, "pending, shipped, cancelled", "pending, shipped, cancelled, returned")
	a.Schemas = run.Schemas
	changed := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	x := contractTarget(t, consumer, "response", "/properties/status", "enum", `"pending"`)
	y := contractTarget(t, changed, "response", "/properties/status", "enum", `"pending"`)
	if x.Key == y.Key || x.OperationKey != y.OperationKey {
		t.Fatal("revision should change condition identity, not operation identity")
	}
}

func TestContractOmittedSpecsAndOpenRuns(t *testing.T) {
	run, a := contractFixture(conditionsSpec, `{}`)
	run.Schemas[0].Source = artifact.SchemaSource{Type: "omitted"}
	a.Schemas = run.Schemas
	run.RunState = "open"
	report := measureContractFixture(t, run, []*artifact.Artifact{a})
	if report.Complete || report.Specs[0].DenominatorKnown || len(report.Specs[0].Targets) > 0 {
		t.Fatal("invented coverage denominator")
	}
}

func simpleResponseSpec(schema string) string {
	return `openapi: 3.0.3
info: {title: Test, version: "1"}
paths:
  /orders:
    post:
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: ` + schema + "\n"
}
func TestContractBoundariesAndEnumFeasibility(t *testing.T) {
	for _, tc := range []struct{ schema, body, family, condition, state string }{
		{`{type: integer, minimum: 0, maximum: 1}`, `0`, "number", "interior", "excluded"},
		{`{type: number, minimum: 0.1, maximum: 1}`, `0.1`, "number", "equal:1/10", "covered"},
		{`{type: number, minimum: 0.1, exclusiveMinimum: true}`, `0.1`, "number", "interior", "not_covered"},
		{`{type: string, minLength: 1, maxLength: 1}`, `"é"`, "string_length", "equal:1", "covered"},
		{`{type: integer, minimum: 0, maximum: 10, enum: [5]}`, `5`, "number", "equal:0", "excluded"},
		{`{type: number, minimum: 0}`, `0.10000000000000001`, "number", "interior", "unknown"},
	} {
		t.Run(tc.schema+tc.body, func(t *testing.T) {
			run, a := contractFixture(simpleResponseSpec(tc.schema), tc.body)
			s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
			target := requireTargetState(t, s, "response", "/schema", tc.family, tc.condition, tc.state)
			if tc.state == "covered" && len(target.Evidence) != 1 {
				t.Fatalf("duplicate boundary hit: %d", len(target.Evidence))
			}
		})
	}
}

func TestContractBranchesAndUnsupportedRules(t *testing.T) {
	for _, tc := range []struct {
		schema, body string
		covered      int
	}{
		{`{oneOf: [{type: string}, {type: integer}]}`, `"a"`, 1},
		{`{oneOf: [{type: number}, {type: integer}]}`, `1`, 0},
		{`{anyOf: [{type: number}, {type: integer}]}`, `1`, 2},
		{`{allOf: [{type: integer, minimum: 0}, {type: integer, maximum: 3}]}`, `1`, 2},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			run, a := contractFixture(simpleResponseSpec(tc.schema), tc.body)
			s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
			got := 0
			for _, target := range s.Targets {
				if target.Family == "branch" && target.State == "covered" {
					got++
				}
			}
			if got != tc.covered {
				t.Fatalf("covered branches: got %d want %d", got, tc.covered)
			}
		})
	}
	run, a := contractFixture(simpleResponseSpec(`{type: array, uniqueItems: true, items: {type: string, format: uuid}}`), `[]`)
	s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	if s.DenominatorKnown || s.Counts["family:unsupported"].Unsupported != 2 {
		t.Fatal("unsupported constraints hidden")
	}
}

func TestContractNoTrafficInventoryAndRetries(t *testing.T) {
	run, a := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
	run.Attempts = nil
	empty := measureContractFixture(t, run, nil)
	requireTargetState(t, empty.Specs[0], "response", "/properties/status", "enum", `"pending"`, "not_covered")
	run.Attempts = []bundle.AttemptRef{{AttemptID: "attempt"}, {AttemptID: "retry"}}
	retry := *a
	retry.TestCase = a.TestCase
	retry.TestCase.ID = "retry"
	s := measureContractFixture(t, run, []*artifact.Artifact{a, &retry}).Specs[0]
	target := requireTargetState(t, s, "response", "/properties/status", "enum", `"pending"`, "covered")
	if len(target.Evidence) != 2 || target.Evidence[1].AttemptID != "retry" {
		t.Fatal("retry evidence lost")
	}
}

func TestContractDirectionalFieldsAndNullBranches(t *testing.T) {
	schema := simpleResponseSpec(`{type: object, properties: {secret: {type: string, writeOnly: true}, id: {type: string, readOnly: true}, value: {type: string, nullable: true}}}`)
	run, a := contractFixture(schema, `{"id":"1","value":null}`)
	s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	requireTargetState(t, s, "response", "/properties/value", "nullable", "null", "covered")
	requireTargetState(t, s, "response", "/properties/id", "presence", "present", "covered")
	for _, target := range s.Targets {
		if strings.Contains(target.Location, "/properties/secret") {
			t.Fatal("writeOnly field entered response denominator")
		}
	}
	run, a = contractFixture(simpleResponseSpec(`{anyOf: [{type: string, nullable: true}, {type: integer}]}`), `null`)
	s = measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	requireTargetState(t, s, "response", "/anyOf/0", "branch", "anyOf:matched", "covered")
}

func TestContractRecursiveSchemaIsExplicitAndBounded(t *testing.T) {
	schema := simpleResponseSpec(`{$ref: '#/components/schemas/Node'}`) + `components:
  schemas:
    Node:
      type: object
      properties:
        child: {$ref: '#/components/schemas/Node'}
`
	run, a := contractFixture(schema, `{"child":{}}`)
	s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	if s.DenominatorKnown || len(s.Targets) > 20 || s.Counts["family:unsupported"].Unsupported != 1 {
		t.Fatalf("recursion was not bounded: %+v", s.Counts)
	}
	for _, target := range s.Targets {
		if target.Phase == "response" && target.State == "covered" {
			t.Fatal("unsupported recursive validation earned coverage")
		}
	}
}

func TestFrozenContractKeysSurviveDifferentRepoPaths(t *testing.T) {
	makeReport := func() *ContractSpec {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "model.yaml"), []byte("type: string\nenum: [a, b]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "api.yaml")
		if err := os.WriteFile(path, []byte(simpleResponseSpec(`{$ref: 'model.yaml'}`)), 0600); err != nil {
			t.Fatal(err)
		}
		w, err := bundle.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Configure(bundle.Identity{APIID: "orders"}); err != nil {
			t.Fatal(err)
		}
		tc := artifact.TestCase{ID: "attempt", Name: "test", Status: "pass"}
		_, err = w.Register(tc, []artifact.SchemaRef{{ID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "file", Path: path}}})
		if err != nil {
			t.Fatal(err)
		}
		run, captures, err := bundle.Load(w.Path())
		if err != nil {
			t.Fatal(err)
		}
		return measureContractFixture(t, run, captures).Specs[0]
	}
	a, b := makeReport(), makeReport()
	if len(a.Targets) == 0 || len(a.Targets) != len(b.Targets) || a.Revision != b.Revision {
		t.Fatal("same frozen contract has different identity in another repo")
	}
	for i, target := range a.Targets {
		if target.Key != b.Targets[i].Key {
			t.Fatal("repository path leaked into coverage key")
		}
	}
}

func TestContractRepeatedParametersAndPrecisionAreUnknown(t *testing.T) {
	run, a := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"ORD-123"}`)
	a.Traffic[0].Request.HTTP.URL += "?mode=fast&mode=slow"
	s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	requireTargetState(t, s, "request", "/parameters/query/mode/schema", "enum", `"fast"`, "unknown")
	run, a = contractFixture(simpleResponseSpec(`{type: integer, minimum: 9007199254740993}`), `9007199254740994`)
	s = measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	if s.DenominatorKnown || s.Counts["family:unsupported"].Unsupported == 0 {
		t.Fatal("lossy schema precision was hidden")
	}
}

func TestContractPathParametersAreDecodedOnlyOnce(t *testing.T) {
	schema := `openapi: 3.0.3
info: {title: Test, version: "1"}
paths:
  /orders/{id}:
    get:
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: string, enum: ['a%2Fb']}
      responses:
        '204': {description: empty}
`
	run, a := contractFixture(schema, "")
	a.Traffic[0].Request.HTTP = &artifact.HttpMessage{Method: "GET", URL: "http://provider/orders/a%252Fb"}
	a.Traffic[0].Response.HTTP = &artifact.HttpMessage{Status: 204}
	s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	requireTargetState(t, s, "request", "/parameters/path/id/schema", "enum", `"a%2Fb"`, "covered")
	if len(s.Violations) != 0 {
		t.Fatal("valid escaped path generated a violation")
	}
}

func TestContractStructuredViolationsKeepConstraintAndInstance(t *testing.T) {
	run, a := contractFixture(conditionsSpec, `{"status":"pending","items":[],"reference":"invalid"}`)
	s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
	if len(s.Violations) != 1 {
		t.Fatalf("expected one violated constraint, got %v", s.Violations)
	}
	v := s.Violations[0]
	if v.Constraint != "pattern" || !strings.HasSuffix(v.SchemaLocation, "/properties/reference") || v.Evidence.InstanceLocation != "/body/reference" {
		t.Fatalf("missing structured violation: %+v", v)
	}
}
