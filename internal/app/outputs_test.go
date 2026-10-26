package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/runoutput"
)

func TestFinishRunProducesOutputsAndPreservesFailedAttempts(t *testing.T) {
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer sut.Close()
	control, _ := dependencyCaptureRuntime(t)
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "fails as expected", "sut_base_url": sut.URL, "schema_required": false})
	response, err := http.Get(session["sut_url"].(string) + "/orders")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	attempt := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "fail"})
	finished := postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{"outputs": runoutput.Defaults()})
	validateControlResponse(t, "finish", finished)
	validateControlResponse(t, "exports", finished["outputs"])
	if finished["bundle_path"] != attempt["bundle_path"] {
		t.Fatal("wrong archive")
	}
	data, _ := json.Marshal(finished["outputs"])
	var result runoutput.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Arazzo == "" || len(result.ArazzoFiles) != 1 || result.Coverage["json"] == "" {
		t.Fatalf("missing outputs: %+v", result)
	}
	for _, path := range []string{result.Arazzo, result.ArazzoFiles[0], result.Coverage["json"]} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	captured := dependencyArtifact(t, attempt)
	if captured.TestCase.Status != "fail" || captured.Traffic[0].Response.HTTP.Status != 503 {
		t.Fatal("failed evidence changed")
	}
}

func TestFinishRunExportsObservedJSONRESTAsArazzo(t *testing.T) {
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":123}`))
	}))
	defer sut.Close()
	control, _ := dependencyCaptureRuntime(t)
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "reads an order", "sut_base_url": sut.URL, "schema_required": false})
	response, err := http.Get(session["sut_url"].(string) + "/orders/123")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
	finished := postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{"outputs": runoutput.Config{Arazzo: true}})
	validateControlResponse(t, "finish", finished)
	validateControlResponse(t, "exports", finished["outputs"])
	data, _ := json.Marshal(finished["outputs"])
	var result runoutput.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Arazzo == "" || len(result.ArazzoFiles) != 1 || len(result.Errors) != 0 {
		t.Fatalf("missing Arazzo result: %+v", result)
	}
	data, err = os.ReadFile(result.Arazzo)
	if err != nil {
		t.Fatal(err)
	}
	var document arazzo.Document
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Workflows) != 1 || len(document.Workflows[0].Steps) != 1 || document.Workflows[0].Steps[0].SuccessCriteria[0].Condition != "$statusCode == 200" {
		t.Fatalf("missing captured workflow: %+v", document)
	}
	if _, err := os.Stat(result.ArazzoFiles[0]); err != nil {
		t.Fatal(err)
	}
}
