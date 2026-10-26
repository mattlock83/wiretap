package artifact

import "fmt"

// ValidateCapture checks the supported evidence format and per-attempt flow IDs.
func ValidateCapture(capture *Artifact) error {
	if capture == nil {
		return nil
	}
	for _, schema := range capture.Schemas {
		if schema.Type != "openapi" || schema.Protocol != "" && schema.Protocol != "http" {
			return fmt.Errorf("unsupported contract type/protocol %q/%q; only OpenAPI over HTTP is supported", schema.Type, schema.Protocol)
		}
	}
	seen := map[string]bool{}
	for _, flow := range capture.Traffic {
		if flow.Protocol != "" && flow.Protocol != "http" {
			return fmt.Errorf("unsupported capture protocol %q; only HTTP is supported", flow.Protocol)
		}
		if flow.ID == "" || seen[flow.ID] {
			return fmt.Errorf("duplicate or empty flow ID %q", flow.ID)
		}
		seen[flow.ID] = true
	}
	return nil
}
