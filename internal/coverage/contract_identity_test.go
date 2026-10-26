package coverage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func TestContractPrecisionIgnoresUnmeasuredNumbers(t *testing.T) {
	base := simpleResponseSpec(`{type: string}`)
	for name, schema := range map[string]string{
		"metadata":         strings.Replace(base, `version: "1"`, `version: "1", x-build-id: 9007199254740993`, 1),
		"example":          strings.Replace(base, "schema: {type: string}", "example: 9007199254740993\n              schema: {type: number}", 1),
		"unused component": base + "components:\n  schemas:\n    Unused:\n      type: integer\n      minimum: 9007199254740993\n",
	} {
		t.Run(name, func(t *testing.T) {
			body, typ := `"valid"`, "string"
			if name == "example" {
				body, typ = "1", "number"
			}
			run, a := contractFixture(schema, body)
			s := measureContractFixture(t, run, []*artifact.Artifact{a}).Specs[0]
			requireTargetState(t, s, "response", "/schema", "type", typ, "covered")
			if !s.DenominatorKnown || s.Counts["family:operation"].Covered != 1 {
				t.Fatalf("unmeasured number changed coverage: %+v", s)
			}
		})
	}
}

func TestContractRevisionsRetainExactNumericIdentity(t *testing.T) {
	revision := func(bound string) (string, bool) {
		t.Helper()
		value, precise, err := contractRevision(freezeCoverageSchema(t, artifact.SchemaRef{Source: artifact.SchemaSource{Type: "inline", Content: simpleResponseSpec(`{type: number, minimum: ` + bound + `}`)}}))
		if err != nil {
			t.Fatal(err)
		}
		return value, precise
	}
	a, aPrecise := revision("0.10000000000000001")
	b, bPrecise := revision("0.100000000000000001")
	if a == b || aPrecise || bPrecise {
		t.Fatal("distinct imprecise constraints lost identity or precision warning")
	}
	c, cPrecise := revision("0.1")
	d, dPrecise := revision("1.00e-1")
	if c != d || !cPrecise || !dPrecise {
		t.Fatal("equivalent numeric representations produced different identities")
	}
}

func TestContractPrecisionChecksReachableExternalSchemas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "root.yaml")
	if err := os.WriteFile(path, []byte(simpleResponseSpec(`{$ref: 'models.yaml#/types/Amount'}`)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.yaml"), []byte("types:\n  Amount:\n    type: integer\n    minimum: 9007199254740993\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, precise, err := contractRevision(freezeCoverageSchema(t, artifact.SchemaRef{Source: artifact.SchemaSource{Type: "file", Path: path}}))
	if err != nil || precise {
		t.Fatalf("reachable imprecise bound was missed: precise=%v err=%v", precise, err)
	}
}

func TestFrozenContractRetainsImpreciseDecimalConstraint(t *testing.T) {
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	schema := simpleResponseSpec(`{type: number, minimum: 0.10000000000000001}`)
	_, err = w.RegisterPrepared(artifact.TestCase{ID: "attempt", Name: "decimal boundary"}, []artifact.SchemaRef{{ID: "orders", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: schema}}})
	if err != nil {
		t.Fatal(err)
	}
	run, captures, err := bundle.Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	s := measureContractFixture(t, run, captures).Specs[0]
	if s.DenominatorKnown || s.Counts["family:unsupported"].Unsupported == 0 {
		t.Fatalf("freezing hid the supplied constraint's precision: %+v", s)
	}
}

func contractRevision(ref artifact.SchemaRef) (string, bool, error) {
	root, files, err := bundle.OpenAPIFiles(ref)
	if err != nil {
		return "", false, err
	}
	return contractRevisionFiles(context.Background(), root, files)
}
