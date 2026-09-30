package bundlereport

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
)

func contractViewFixture(t *testing.T) string {
	t.Helper()
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(bundle.Identity{APIID: "warehouse-api", SUTTargetKind: "service"}); err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "attempt", Name: "handles the delivery boundary", Status: "pass"}
	refs, err := w.Register(tc, []artifact.SchemaRef{{ID: "warehouse-schema", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: Warehouse, version: "1"}
paths:
  /items:
    get:
      responses:
        '204': {description: OK}
`}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(&artifact.Artifact{Version: 1, TestCase: tc, Schemas: refs, Traffic: []artifact.Flow{{ID: "flow", Channel: "test_to_sut", Protocol: "http", Request: artifact.Message{HTTP: &artifact.HttpMessage{Method: "GET", URL: "http://service/items"}}, Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 204}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(w.Path()); err != nil {
		t.Fatal(err)
	}
	return w.Path()
}

func TestContractConditionsSearchIncludesEvidenceAndScope(t *testing.T) {
	root := contractViewFixture(t)
	data, err := os.ReadFile(filepath.Join(root, "viewer/index.html"))
	if err != nil {
		t.Fatal(err)
	}
	section := strings.SplitN(strings.SplitN(string(data), `id="contracts"`, 2)[1], `</section>`, 2)[0]
	indexes := regexp.MustCompile(`data-search="([^"]*)"`).FindAllStringSubmatch(section, -1)
	if len(indexes) == 0 {
		t.Fatal("condition search indexes missing")
	}
	for _, query := range []string{"warehouse-api", "warehouse-schema", "service", "provided", "handles the delivery boundary", "response_status"} {
		found := false
		for _, index := range indexes {
			found = found || strings.Contains(html.UnescapeString(index[1]), query)
		}
		if !found {
			t.Errorf("condition evidence cannot be found by %q", query)
		}
	}
}

func TestValidateContractCatalogueIdentityAndEvidence(t *testing.T) {
	root := contractViewFixture(t)
	path := filepath.Join(root, "contract-coverage.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"malformed", "null", "wrong run", "stale revision", "wrong flow"} {
		t.Run(name, func(t *testing.T) {
			var catalogue coverage.ContractCoverage
			if err := json.Unmarshal(original, &catalogue); err != nil {
				t.Fatal(err)
			}
			data := original
			switch name {
			case "malformed":
				data = []byte("{invalid")
			case "null":
				data = []byte("null")
			case "wrong run":
				catalogue.RunID = "different-run"
			case "stale revision":
				catalogue.SnapshotRevision--
			case "wrong flow":
				catalogue.Specs[0].Targets[0].Evidence[0].FlowID = "missing-flow"
			}
			if name != "malformed" && name != "null" {
				data, err = json.Marshal(catalogue)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := Validate(root); err == nil {
				t.Fatal("invalid catalogue accepted")
			}
		})
	}
}
