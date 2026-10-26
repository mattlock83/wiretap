package arazzo

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestGeneratedDocumentsConformToOfficialArazzoSchema(t *testing.T) {
	data, err := os.ReadFile("testdata/arazzo-1.0.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://spec.openapis.org/arazzo/1.0/schema/2025-10-15"
	if err := compiler.AddResource(location, schemaValue); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	for _, frozen := range []bool{false, true} {
		name := "observed"
		var refs []artifact.SchemaRef
		flow := jsonFlow("read", "GET", "http://local.test/items?name=%24inputs.literal")
		if frozen {
			name = "frozen"
			refs = []artifact.SchemaRef{{ID: "items", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: frozenSchema}}}
			flow.Request.URL = "http://local.test/v2/items/42?limit=2"
		}
		t.Run(name, func(t *testing.T) {
			root := captureArchive(t, refs, &artifact.Artifact{TestCase: artifact.TestCase{ID: "attempt", Name: "read item"}, Traffic: []artifact.Flow{flow}})
			export, err := Generate(root, "Items API")
			if err != nil {
				t.Fatal(err)
			}
			if export.Document == nil {
				t.Fatal("missing workflow document")
			}
			var encoded bytes.Buffer
			if err := Write(&encoded, export.Document); err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(encoded.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(value); err != nil {
				t.Fatalf("official schema rejected export: %v", err)
			}
		})
	}
}
