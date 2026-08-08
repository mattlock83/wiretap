package app

import (
	"strings"
	"testing"

	"github.com/phrond/evermock/internal/artifact"
)

func TestSchemaSetupIsStrictUnlessGenerationIsExplicit(t *testing.T) {
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
		name     string
		schemas  []artifact.SchemaRef
		generate bool
		want     string
	}{
		{name: "missing schema", want: "OpenAPI schema is required"},
		{name: "explicit generation", generate: true},
		{name: "valid schema", schemas: []artifact.SchemaRef{valid}},
		{name: "ambiguous", schemas: []artifact.SchemaRef{valid}, generate: true, want: "cannot be used together"},
		{name: "invalid schema", schemas: []artifact.SchemaRef{invalid}, want: "invalid or unreadable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSchemaSetup(test.schemas, test.generate)
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
