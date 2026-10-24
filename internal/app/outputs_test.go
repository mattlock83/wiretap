package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mattlock83/wiretap/internal/runoutput"
)

func TestFinishRunProducesOutputsAndPreservesFailedAttempts(t *testing.T) {
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("unavailable"))
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
	if finished["bundle_path"] != attempt["bundle_path"] {
		t.Fatal("wrong archive")
	}
	data, _ := json.Marshal(finished["outputs"])
	var result runoutput.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Postman) != 1 || result.Coverage["json"] == "" {
		t.Fatalf("missing outputs: %+v", result)
	}
	for _, path := range []string{result.Postman[0], result.Coverage["json"]} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	captured := dependencyArtifact(t, attempt)
	if captured.TestCase.Status != "fail" || captured.Traffic[0].Response.HTTP.Status != 503 {
		t.Fatal("failed evidence changed")
	}
}
