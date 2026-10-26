package coverage

import (
	"reflect"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestCoverageViewsShareRouteAndBoundaryEvidence(t *testing.T) {
	const document = `openapi: 3.0.3
info: {title: API, version: "1"}
servers: [{url: 'http://provider/v1'}]
paths:
  /orders/{id}:
    parameters: [{name: id, in: path, required: true, schema: {type: string}}]
    get:
      responses: {'200': {description: OK}}
  /unseen:
    get:
      responses: {'200': {description: OK}}
`
	provided := freezeCoverageSchema(t, artifact.SchemaRef{ID: "orders", Role: "provided", Type: "openapi", AssetID: "orders", Source: artifact.SchemaSource{Type: "inline", Content: document}})
	other := provided
	other.ID, other.AssetID = "payments", "payments"
	unregistered := provided
	unregistered.ID, unregistered.AssetID = "unused", "unused"
	omitted := artifact.SchemaRef{ID: "inventory", Role: "dependency", Type: "openapi", AssetID: "inventory", Source: artifact.SchemaSource{Type: "omitted"}}
	flow := func(id, channel, assetID, address string) artifact.Flow {
		return artifact.Flow{ID: id, Protocol: "http", Channel: channel, AssetID: assetID,
			Request:  &artifact.HttpMessage{Method: "GET", URL: address},
			Response: &artifact.HttpMessage{Status: 200}}
	}
	first := &artifact.Artifact{
		TestCase: artifact.TestCase{ID: "first", Name: "reads order", Status: "fail"},
		Schemas:  []artifact.SchemaRef{provided, omitted},
		Traffic: []artifact.Flow{
			flow("order-first", "test_to_sut", "orders", "http://provider/v1/orders/7"),
			flow("unregistered", "test_to_sut", "payments", "http://provider/v1/orders/7"),
			flow("outside-server", "test_to_sut", "orders", "http://provider/outside/orders/7"),
			flow("inventory-first", "sut_to_dependency", "inventory", "http://inventory/items/7"),
		},
	}
	second := &artifact.Artifact{
		TestCase: artifact.TestCase{ID: "second", Name: "reads order", Status: "pass"},
		Schemas:  []artifact.SchemaRef{provided, other, omitted},
		Traffic: []artifact.Flow{
			flow("order-second", "test_to_sut", "orders", "http://provider/v1/orders/8"),
			flow("payment-second", "test_to_sut", "payments", "http://provider/v1/orders/8"),
			flow("inventory-second", "sut_to_dependency", "inventory", "http://inventory/items/7"),
			flow("unassigned", "sut_to_dependency", "other", "http://inventory/items/7"),
		},
	}
	empty := &artifact.Artifact{TestCase: artifact.TestCase{ID: "empty", Name: "fails before request", Status: "fail"}}
	providedGroup, dependentGroup, cases, _ := buildCoverageWithSchemas([]artifactEntry{{Doc: first}, {Doc: second}, {Doc: empty}}, []artifact.SchemaRef{unregistered}, contractCache{})
	if len(cases) != 2 || cases[0].Name != empty.TestCase.Name || len(cases[0].Attempts) != 1 || len(cases[0].Traffic) != 0 {
		t.Fatalf("traffic-free attempt lost: %+v", cases)
	}
	testCase := cases[1]
	if len(testCase.Attempts) != 2 || len(testCase.Traffic) != 8 {
		t.Fatalf("retry or unmatched traffic lost: %+v", testCase)
	}
	for _, check := range []struct {
		group SpecCoverageGroup
		cases []SpecOperationSamples
		want  map[string][]string
	}{
		{providedGroup, testCase.Provided, map[string][]string{"orders": {"order-first", "order-second"}, "payments": {"payment-second"}, "unused": nil}},
		{dependentGroup, testCase.Dependent, map[string][]string{"inventory": {"inventory-first", "inventory-second"}}},
	} {
		if len(check.group.Specs) != len(check.want) {
			t.Fatalf("schema inventory changed: %+v", check.group)
		}
		for _, spec := range check.group.Specs {
			var ids []string
			for _, operation := range spec.Exercised {
				for _, sample := range operation.Samples {
					ids = append(ids, sample.FlowID)
				}
			}
			if !reflect.DeepEqual(ids, check.want[spec.SchemaID]) {
				t.Fatalf("%s attributed wrong evidence: got %v want %v", spec.SchemaID, ids, check.want[spec.SchemaID])
			}
			if len(ids) == 0 {
				if len(spec.NotCovered) != 2 {
					t.Fatalf("unregistered contract lost denominator: %+v", spec)
				}
				continue
			}
			found := false
			for _, perTest := range check.cases {
				if perTest.SchemaID == spec.SchemaID {
					found = true
					if !reflect.DeepEqual(spec.Exercised, perTest.Operations) {
						t.Fatalf("%s global and per-test evidence disagree", spec.SchemaID)
					}
				}
			}
			if !found {
				t.Fatalf("%s disappeared from per-test evidence", spec.SchemaID)
			}
			if spec.SchemaID == "inventory" {
				if spec.DenominatorKnown || spec.Exercised[0].Operation != "GET /items/7" || len(spec.NotCovered) != 0 {
					t.Fatalf("omitted contract was inferred: %+v", spec)
				}
			} else if !spec.DenominatorKnown || spec.Exercised[0].Operation != "GET /orders/{id}" || len(spec.NotCovered) != 1 {
				t.Fatalf("declared route coverage changed: %+v", spec)
			}
		}
	}
}
