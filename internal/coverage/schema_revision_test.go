package coverage

import (
	"testing"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
)

func TestSameSchemaIDKeepsRevisionBoundaries(t *testing.T) {
	schema := func(paths string) artifact.SchemaRef {
		return artifact.SchemaRef{ID: "api", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.0
info: {title: API, version: "1"}
paths:
` + paths}}
	}
	first := schema(`  /a:
    get:
      responses: {"200": {description: ok}}
`)
	second := schema(`  /a:
    get:
      responses: {"200": {description: ok}}
  /b:
    get:
      responses: {"200": {description: ok}}
`)
	entry := func(id, path string, ref artifact.SchemaRef) artifactEntry {
		return artifactEntry{Doc: &artifact.Artifact{
			Version:  1,
			TestCase: artifact.TestCase{ID: id, Name: id, StartedAt: time.Now().UTC(), Status: "pass"},
			Schemas:  []artifact.SchemaRef{ref},
			Traffic:  []artifact.Flow{{ID: "flow", Protocol: "http", Channel: "test_to_sut", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://sut" + path}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200}}}},
		}}
	}
	provided, _, cases, notes := buildCoverageWithSchemas([]artifactEntry{entry("first", "/a", first), entry("second", "/b", second)}, nil)
	if len(notes) > 0 {
		t.Fatalf("schema did not load: %v", notes)
	}
	if len(provided.Specs) != 2 {
		t.Fatalf("wanted two schema revisions, got %d", len(provided.Specs))
	}
	if provided.Specs[0].SchemaSet == provided.Specs[1].SchemaSet {
		t.Fatal("schema set identity collided")
	}
	for _, spec := range provided.Specs {
		if len(spec.Exercised) != 1 || len(spec.Exercised[0].Samples) != 1 {
			t.Fatalf("cross-revision sample contamination: %+v", spec)
		}
		switch spec.Exercised[0].Operation {
		case "GET /a":
			if len(spec.NotCovered) != 0 {
				t.Fatalf("old revision gained new path: %+v", spec)
			}
		case "GET /b":
			if len(spec.NotCovered) != 1 || spec.NotCovered[0].Operation != "GET /a" {
				t.Fatalf("new revision denominator wrong: %+v", spec)
			}
		default:
			t.Fatalf("unexpected operation: %+v", spec)
		}
	}
	if len(cases) != 2 || len(cases[0].Provided) != 1 || len(cases[1].Provided) != 1 {
		t.Fatalf("attempts lost revision mapping: %+v", cases)
	}
}
