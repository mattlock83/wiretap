package app

import (
	"github.com/phrond/wiretap/internal/bundle"
	"net/http"
	"testing"
)

func TestLogicalIdentitySurvivesRetriesAndPendingCapture(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	for attempt := 0; attempt < 2; attempt++ {
		session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "same label", "schema_required": false, "test_identity": map[string]any{"id": "orders/create", "variant": "empty", "attempt_ordinal": attempt}})
		run := instance.currentBundle().Snapshot()
		if len(run.Pending) != 1 || run.Pending[0].Identity == nil || run.Pending[0].Identity.ID != "orders/create" || *run.Pending[0].Identity.AttemptOrdinal != attempt {
			t.Fatalf("pending identity lost: %+v", run.Pending)
		}
		result := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
		capture := dependencyArtifact(t, result)
		if capture.TestCase.Identity == nil || *capture.TestCase.Identity.AttemptOrdinal != attempt {
			t.Fatal("retry identity lost")
		}
	}
	path := instance.currentBundle().Path()
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	run, captures, err := bundle.Load(path)
	if err != nil || run.Producer == nil || len(captures) != 2 || captures[0].TestCase.ID == captures[1].TestCase.ID {
		t.Fatalf("wrong evidence: %+v %v", run, err)
	}
	for _, identity := range []map[string]any{{"id": ""}, {"id": "test", "attempt_ordinal": -1}} {
		apiStatus(t, control.URL, "/v1/setupTest", map[string]any{"name": "bad identity", "schema_required": false, "test_identity": identity}, http.StatusBadRequest)
	}
}
