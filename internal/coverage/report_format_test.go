package coverage

import (
	"strings"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
)

func TestPrettyJSON_FormatsObject(t *testing.T) {
	raw := `{"z":1,"a":{"nested":"value"}}`
	got := prettyJSON(raw)
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected pretty JSON with newlines, got %q", got)
	}
	if !strings.Contains(got, `"a": {`) {
		t.Fatalf("expected formatted object, got %q", got)
	}
	if !strings.Contains(got, `"nested": "value"`) {
		t.Fatalf("expected nested field in output, got %q", got)
	}
}

func TestPrettyJSON_InvalidPassthrough(t *testing.T) {
	raw := `{"missing"`
	if got := prettyJSON(raw); got != raw {
		t.Fatalf("expected invalid JSON to pass through, got %q", got)
	}
}

func TestFormatHeaders_SortedAndExpanded(t *testing.T) {
	headers := artifact.Headers{
		"X-Zeta":  {"z1"},
		"X-Alpha": {"a1", "a2"},
	}
	got := formatHeaders(headers)
	want := "X-Alpha: a1\nX-Alpha: a2\nX-Zeta: z1"
	if got != want {
		t.Fatalf("unexpected headers format:\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}
