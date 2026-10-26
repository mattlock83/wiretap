package app

import (
	"net/http"
	"testing"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/project"
)

func TestApplicationIdentityFromStartRunPersistsWithoutTests(t *testing.T) {
	control, _ := dependencyCaptureRuntime(t)
	application := bundle.Application{Name: "Orders", ID: "orders-app", Description: "Order processing"}
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{"application": application})
	root := start["bundle_path"].(string)
	run, captures, err := bundle.Load(root)
	if err != nil || run.Application == nil || *run.Application != application || len(captures) != 0 {
		t.Fatalf("run application = %+v, captures = %d, error = %v", run, len(captures), err)
	}
	next := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	if next["bundle_path"] != root {
		t.Fatal("repeated startRun created another run")
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	run, _, err = bundle.Load(root)
	if err != nil || run.RunState != "complete" || run.Application == nil || *run.Application != application {
		t.Fatalf("finished run application lost: %+v, %v", run, err)
	}
	fresh := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	freshRun, _, err := bundle.Load(fresh["bundle_path"].(string))
	if err != nil || fresh["bundle_path"] == root || freshRun.Application != nil {
		t.Fatalf("API application leaked into the next run: %+v, %v", freshRun, err)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}

func TestApplicationIdentityFromSetupTestAndLaterOmission(t *testing.T) {
	control, _ := dependencyCaptureRuntime(t)
	application := bundle.Application{Name: "Orders", ID: "orders-app"}
	first := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
		"name": "first", "application": application, "api_id": "orders-api", "schema_required": false,
	})
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": first["session_id"], "status": "pass"})
	second := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "second", "schema_required": false})
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": second["session_id"], "status": "pass"})
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	run, captures, err := bundle.Load(first["bundle_path"].(string))
	if err != nil || run.Application == nil || *run.Application != application || run.APIID != "orders-api" || len(captures) != 2 {
		t.Fatalf("application identity lost between tests: %+v, captures = %d, %v", run, len(captures), err)
	}
}

func TestApplicationIdentityConflictsPreserveProjectAndRun(t *testing.T) {
	for _, fromProject := range []bool{false, true} {
		name := "API identity"
		if fromProject {
			name = "project identity"
		}
		t.Run(name, func(t *testing.T) {
			control, instance := dependencyCaptureRuntime(t)
			application := bundle.Application{Name: "Orders", ID: "orders-app", Description: "Order processing"}
			payload := map[string]any{"application": application}
			if fromProject {
				optional := false
				configured := application
				instance.project = &project.Config{Application: &configured, SUT: project.API{ID: "orders-api", SchemaRequired: &optional}}
				payload = map[string]any{}
			}
			start := postDependencyJSON(t, control.URL, "/v1/startRun", payload)
			for _, endpoint := range []string{"/v1/startRun", "/v1/setupTest"} {
				for _, invalid := range []any{
					map[string]any{"name": "Other", "id": application.ID, "description": application.Description},
					map[string]any{"name": application.Name, "id": "other-app", "description": application.Description},
					map[string]any{"name": application.Name, "id": application.ID, "description": "changed"},
					map[string]any{"id": application.ID},
					map[string]any{},
					map[string]any{"name": "Orders", "unknown": true},
				} {
					request := map[string]any{"application": invalid}
					if endpoint == "/v1/setupTest" {
						request["schema_required"] = false
					}
					apiStatus(t, control.URL, endpoint, request, http.StatusBadRequest)
					if fromProject && *instance.project.Application != application {
						t.Fatalf("rejected request mutated project application: %+v", instance.project.Application)
					}
					if current := instance.currentBundle().Snapshot().Application; current == nil || *current != application {
						t.Fatalf("rejected request mutated run application: %+v", current)
					}
					if len(instance.sessions) != 0 || len(instance.currentBundle().Snapshot().Pending) != 0 {
						t.Fatal("rejected identity admitted a capture session")
					}
				}
			}
			// Defaults remain usable after partial and invalid identity overrides.
			session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "after rejection", "schema_required": false})
			postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"]})
			postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
			run, _, err := bundle.Load(start["bundle_path"].(string))
			if err != nil || run.Application == nil || *run.Application != application {
				t.Fatalf("identity lost after rejected override: %+v, %v", run, err)
			}
		})
	}
}
