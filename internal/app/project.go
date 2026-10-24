package app

import (
	"errors"
	"maps"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

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
	redactions := append([]artifact.Redaction(nil), cfg.Redactions...)
	id := cfg.SUT.ID
	if id == "" {
		id = "sut"
	}
	out := setupTestRequest{TestSource: maps.Clone(cfg.TestSource), SUTSource: maps.Clone(cfg.SUTSource), APIID: id, SUTBaseURL: cfg.SUT.TargetURL(), SUTTargetKind: cfg.SUT.Kind, SUTProtocol: cfg.SUT.Protocol, Schemas: cfg.SUT.Schemas(id, "sut"), SchemaRequired: cfg.SUT.SchemaRequired, Redactions: redactions}

	if cfg.Application != nil {
		application := *cfg.Application
		out.Application = &application
	}
	for _, id := range cfg.DependencyIDs() {
		dep := cfg.Dependencies[id]
		out.Dependencies = append(out.Dependencies, dependencyRegistration{AssetID: id, AssetName: dep.Name, UpstreamURL: dep.TargetURL(), UpstreamKind: dep.Kind, Protocol: dep.Protocol, Schemas: dep.Schemas(id, "dependency"), SchemaRequired: dep.SchemaRequired})
	}
	return out
}

// Application identity belongs to the run. Omitted values inherit it, while
// explicit values must agree with both the project and any existing run.
func (a *App) resolveApplication(input *bundle.Application) (*bundle.Application, error) {
	var configured, current *bundle.Application
	if a.project != nil {
		configured = a.project.Application
	}
	if writer := a.currentBundle(); writer != nil {
		current = writer.Snapshot().Application
	}
	selected := input
	if selected == nil {
		selected = configured
	}
	if selected == nil {
		selected = current
	}
	if selected == nil {
		return nil, nil
	}
	if strings.TrimSpace(selected.Name) == "" && strings.TrimSpace(selected.ID) == "" {
		return nil, errors.New("application requires a name or ID")
	}
	if configured != nil && *configured != *selected {
		return nil, errors.New("application identity conflicts with project configuration")
	}
	if current != nil && *current != *selected {
		return nil, errors.New("application identity conflicts with this run")
	}
	application := *selected
	return &application, nil
}
