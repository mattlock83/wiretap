package main

import "testing"

func TestProviderNameFlagsParseStableParticipantMappings(t *testing.T) {
	values := providerNameFlags{}
	if err := values.Set(" inventory = Inventory Service "); err != nil {
		t.Fatal(err)
	}
	if err := values.Set("payments=payments-service"); err != nil {
		t.Fatal(err)
	}
	if values["inventory"] != "Inventory Service" || values.String() != "inventory=Inventory Service, payments=payments-service" {
		t.Fatalf("unexpected provider participant mappings: %#v (%q)", values, values.String())
	}
	for _, invalid := range []string{"inventory", "=service", "inventory="} {
		if err := values.Set(invalid); err == nil {
			t.Errorf("accepted invalid Pact provider mapping %q", invalid)
		}
	}
}
