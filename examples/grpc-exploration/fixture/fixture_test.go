package fixture

import (
	"buf.build/go/protovalidate"
	"testing"
)

func TestSourceAnnotationsEnforceOrderConstraints(t *testing.T) {
	contract, err := Compile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := protovalidate.Validate(contract.NewRequest("user-1", "sku-123", 2)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, user, sku string
		quantity        int32
	}{
		{"missing user", "", "sku-123", 2},
		{"short sku", "user-1", "a", 2},
		{"long sku", "user-1", "sku-is-too-long", 2},
		{"quantity below range", "user-1", "sku-123", 0},
		{"quantity above range", "user-1", "sku-123", 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := protovalidate.Validate(contract.NewRequest(test.user, test.sku, test.quantity)); err == nil {
				t.Fatal("declared constraint accepted invalid input")
			}
		})
	}
}
