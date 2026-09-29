package app

import "github.com/phrond/evermock/internal/artifact"

func requiresSchema(required *bool) bool { return required == nil || *required }
func omittedSchema(id, role string) artifact.SchemaRef {
	if id == "" {
		id = "sut"
	}
	return artifact.SchemaRef{ID: id, Role: role, AssetID: id, Type: "openapi", Source: artifact.SchemaSource{Type: "omitted"}}
}
func omittedForFlow(schemas []artifact.SchemaRef, role, asset string) bool {
	for _, s := range schemas {
		if s.Source.Type == "omitted" && s.Role == role && (role == "sut" || s.AssetID == asset) {
			return true
		}
	}
	return false
}
func (a *App) testDefaults() setupTestRequest {
	if a.project == nil {
		return setupTestRequest{}
	}
	cfg := a.project
	id := cfg.SUT.ID
	if id == "" {
		id = "sut"
	}
	out := setupTestRequest{APIID: id, SUTBaseURL: cfg.SUT.TargetURL(), SUTTargetKind: cfg.SUT.Kind, Schemas: cfg.SUT.Schemas(id, "sut"), SchemaRequired: cfg.SUT.SchemaRequired}
	for _, id := range cfg.DependencyIDs() {
		dep := cfg.Dependencies[id]
		out.Dependencies = append(out.Dependencies, dependencyRegistration{AssetID: id, UpstreamURL: dep.TargetURL(), UpstreamKind: dep.Kind, Schemas: dep.Schemas(id, "dependency"), SchemaRequired: dep.SchemaRequired})
	}
	return out
}
