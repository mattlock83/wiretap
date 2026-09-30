package coverage

import (
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
)

func TestContractUnboundedMultipleOfStaysExplicit(t *testing.T) {
	run, a := contractFixture(simpleResponseSpec(`{type: integer, multipleOf: 2}`), `2`)
	s := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	if s.DenominatorKnown || s.Counts["family:unsupported"].Unsupported != 1 {
		t.Fatalf("unmeasured multipleOf omitted from denominator: %+v", s.Counts)
	}
	requireTargetState(t, s, "response", "/schema", "type", "integer", "covered")
}

func TestContractNullableIntersectionsCannotEarnCoverage(t *testing.T) {
	for _, schema := range []string{
		`{type: string, nullable: true, enum: [a]}`,
		`{type: string, nullable: true, not: {type: string, nullable: true}}`,
		`{type: string, nullable: true, oneOf: [{type: string}]}`,
		`{type: string, nullable: true, anyOf: [{type: string}]}`,
		`{type: string, nullable: true, allOf: [{type: string}]}`,
	} {
		t.Run(schema, func(t *testing.T) {
			run, a := contractFixture(simpleResponseSpec(schema), `null`)
			s := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
			if s.DenominatorKnown {
				t.Fatal("nullable validator limitation was hidden")
			}
			foundUnsupported := false
			for _, target := range s.Targets {
				if target.Phase != "response" {
					continue
				}
				if target.State == "covered" {
					t.Fatalf("incompletely validated message earned credit: %+v", target)
				}
				foundUnsupported = foundUnsupported || target.State == "unsupported" && strings.Contains(target.Reason, "complete null validation")
			}
			if !foundUnsupported {
				t.Fatal("missing explicit nullable limitation")
			}
		})
	}
}

func TestContractRequiredNamesWithoutPropertySchemas(t *testing.T) {
	for _, tc := range []struct {
		name, body, state string
	}{
		{"present", `{"id":"123"}`, "covered"},
		{"absent", `{}`, "not_covered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, a := contractFixture(simpleResponseSpec(`{type: object, required: [id]}`), tc.body)
			s := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
			requireTargetState(t, s, "response", "/schema/properties/id", "presence", "present", tc.state)
			if tc.name == "absent" && len(s.Violations) == 0 {
				t.Fatal("missing required presence violation")
			}
		})
	}
}

func TestContractNestedFeasibilityRemainsUnknown(t *testing.T) {
	for _, schema := range []string{
		`{type: object, required: [x], properties: {x: {type: string, enum: [a], pattern: '^b$'}}}`,
		`{type: object, required: [x], properties: {x: {type: string, pattern: '^b$'}}}`,
		`{type: array, minItems: 1, items: {type: string, enum: [a], pattern: '^b$'}}`,
	} {
		t.Run(schema, func(t *testing.T) {
			run, _ := contractFixture(simpleResponseSpec(schema), `{}`)
			s := buildContractCoverage(run, nil).Specs[0]
			if s.DenominatorKnown {
				t.Fatal("nested schema feasibility was incorrectly treated as known")
			}
			typ := "object"
			if strings.Contains(schema, "type: array") {
				typ = "array"
			}
			requireTargetState(t, s, "response", "/schema", "type", typ, "unknown")
		})
	}
}

func TestContractImpossibleScalarDomainsAreExcluded(t *testing.T) {
	for _, schema := range []string{
		`{type: string, minLength: 2, maxLength: 1}`,
		`{type: number, minimum: 3, maximum: 2}`,
		`{type: integer, minimum: 0.1, maximum: 0.9}`,
		`{type: integer, minimum: -0.9, maximum: -0.1}`,
		`{type: number, minimum: 0, maximum: 0, exclusiveMinimum: true}`,
	} {
		t.Run(schema, func(t *testing.T) {
			run, _ := contractFixture(simpleResponseSpec(schema), `{}`)
			s := buildContractCoverage(run, nil).Specs[0]
			for _, target := range s.Targets {
				if strings.HasSuffix(target.Location, "/schema") && target.State != "excluded" {
					t.Fatalf("impossible scalar target stayed in denominator: %+v", target)
				}
			}
		})
	}
}

func TestContractNullSurvivesImpossibleNonNullBounds(t *testing.T) {
	run, a := contractFixture(simpleResponseSpec(`{type: string, nullable: true, minLength: 2, maxLength: 1}`), `null`)
	s := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	requireTargetState(t, s, "response", "/schema", "nullable", "null", "covered")
	requireTargetState(t, s, "response", "/schema", "nullable", "non_null", "excluded")
	requireTargetState(t, s, "response", "/schema", "type", "string", "excluded")
}

func TestContractUnmeasuredSchemasCannotHideValidationGaps(t *testing.T) {
	for _, schema := range []string{
		`{type: object, additionalProperties: {type: string, nullable: true, enum: [a]}}`,
		`{type: object, not: {type: object, properties: {x: {type: string, nullable: true, enum: [a]}}}}`,
		`{type: object, additionalProperties: {type: object, properties: {x: {type: string, nullable: true, enum: [a]}}}}`,
	} {
		t.Run(schema, func(t *testing.T) {
			run, a := contractFixture(simpleResponseSpec(schema), `{"x":null}`)
			s := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
			for _, target := range s.Targets {
				if target.Phase == "response" && target.State == "covered" {
					t.Fatalf("hidden validation gap earned coverage: %+v", target)
				}
			}
			requireTargetState(t, s, "response", "/schema", "type", "object", "unknown")
		})
	}
}

func TestContractSafeUnmeasuredSchemasRetainSupportedCoverage(t *testing.T) {
	for _, schema := range []string{
		`{type: object, additionalProperties: {type: integer}}`,
		`{type: object, not: {type: string}}`,
	} {
		t.Run(schema, func(t *testing.T) {
			run, a := contractFixture(simpleResponseSpec(schema), `{"x":1}`)
			s := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
			requireTargetState(t, s, "response", "/schema", "type", "object", "covered")
			if s.Counts["family:unsupported"].Unsupported != 1 {
				t.Fatal("safe unmeasured schema received extra unsupported rules")
			}
		})
	}
}

func TestContractUnmeasuredRecursiveSchemaSafetyIsBounded(t *testing.T) {
	schema := simpleResponseSpec(`{$ref: '#/components/schemas/Node'}`) + `components:
  schemas:
    Node:
      type: object
      additionalProperties: {$ref: '#/components/schemas/Node'}
`
	run, a := contractFixture(schema, `{"child":{}}`)
	s := buildContractCoverage(run, []*artifact.Artifact{a}).Specs[0]
	requireTargetState(t, s, "response", "/schema", "type", "object", "unknown")
	if len(s.Targets) > 12 {
		t.Fatal("safety inspection expanded dynamic coverage targets")
	}
}
