package bundle

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestFrozenSchemaPreservesExactNumericValues(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.Replace(testSchema, `version: "1"`, `version: "1", x-values: [0.10000000000000001, 0x10, 0o12, 1.00e-1, "0.1", true]`, 1)
	refs, err := w.RegisterPrepared(artifact.TestCase{ID: "attempt"}, []artifact.SchemaRef{{ID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: schema}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(refs[0].Ref.Source.Path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var frozen map[string]any
	if err := decoder.Decode(&frozen); err != nil {
		t.Fatal(err)
	}
	values := frozen["info"].(map[string]any)["x-values"].([]any)
	expected := []any{json.Number("0.10000000000000001"), json.Number("16"), json.Number("10"), json.Number("0.1"), "0.1", true}
	for i, value := range values {
		if value != expected[i] {
			t.Errorf("frozen number %d changed: %#v != %#v", i, value, expected[i])
		}
	}
}
