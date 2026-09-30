package app

import (
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
)

func TestSchemaSetupRequiresSpecsUnlessExplicitlyOmitted(t *testing.T) {
	valid := artifact.SchemaRef{
		ID:   "orders-api",
		Role: "sut",
		Type: "openapi",
		Source: artifact.SchemaSource{
			Type: "inline",
			Content: `openapi: 3.1.0
info:
  title: Orders
  version: 1.0.0
paths: {}
`,
		},
	}
	invalid := valid
	invalid.Source.Content = "not: [valid"

	tests := []struct {
		name         string
		schemas      []artifact.SchemaRef
		allowMissing bool
		want         string
	}{
		{name: "missing schema", want: "OpenAPI schema is required"},
		{name: "explicit omission", allowMissing: true},
		{name: "valid schema", schemas: []artifact.SchemaRef{valid}},
		{name: "optional valid schema", schemas: []artifact.SchemaRef{valid}, allowMissing: true},
		{name: "invalid schema", schemas: []artifact.SchemaRef{invalid}, want: "invalid or unreadable"},
		{name: "optional invalid schema", schemas: []artifact.SchemaRef{invalid}, allowMissing: true, want: "invalid or unreadable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSchemaSetup(test.schemas, test.allowMissing)
			if test.want == "" {
				if err != nil {
					t.Fatalf("validate schema setup: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
