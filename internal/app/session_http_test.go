package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func TestTestLifecycleWritesRedactedBundle(t *testing.T) {
	sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "secret" {
			t.Error("proxy changed the live request")
		}
		w.Write([]byte("ok"))
	}))
	defer sut.Close()
	control, _ := dependencyCaptureRuntime(t)
	session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{
		"name": "redacted boundary", "sut_base_url": sut.URL, "schema_required": false,
		"redactions": []artifact.Redaction{{Field: "request.headers.authorization", Rule: "mask"}},
	})
	request, _ := http.NewRequest(http.MethodGet, session["sut_url"].(string)+"/items", nil)
	request.Header.Set("Authorization", "secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	result := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"], "status": "pass"})
	capture := dependencyArtifact(t, result)
	if len(capture.Redactions) != 1 || len(capture.Traffic) != 1 {
		t.Fatal("capture lost its evidence or redaction rule")
	}
	if result["artifact"] != nil || result["path"] != nil || result["schema_validation"] != nil {
		t.Fatal("response contains duplicate compatibility fields")
	}
	raw, err := os.ReadFile(result["attempt_path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatal("persisted capture contains unredacted data")
	}
	for _, key := range []string{"index_path", "viewer_path", "attempt_page_path", "view_error"} {
		if _, exists := result[key]; exists {
			t.Fatalf("capture advertises removed view %s", key)
		}
	}
	finished := postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	if finished["bundle_path"] != result["bundle_path"] || finished["run_id"] != result["run_id"] {
		t.Fatalf("finish response lost archive identity: %v", finished)
	}
	for _, path := range []string{"index.md", "docs"} {
		if _, err := os.Stat(filepath.Join(result["bundle_path"].(string), path)); !os.IsNotExist(err) {
			t.Fatalf("capture created %s: %v", path, err)
		}
	}

	run, captures, err := bundle.Load(result["bundle_path"].(string))
	if err != nil || run.RunState != "complete" || len(captures) != 1 {
		t.Fatalf("bundle: %v, %v", run, err)
	}
}

func TestOnlyCurrentControlRoutesAndFieldsAreAccepted(t *testing.T) {
	control, _ := dependencyCaptureRuntime(t)
	for _, path := range []string{"/v1/generateSchema", "/v1/startServer", "/v1/killServer", "/v1/lifecycle/start", "/v1/lifecycle/stop", "/v1/proxy/start", "/v1/proxy/mode", "/v1/testcases/start", "/v1/testcases/finish", "/v1/testcases/status", "/v1/artifact", "/v1/redactions"} {
		apiStatus(t, control.URL, path, map[string]any{}, http.StatusNotFound)
	}
	apiStatus(t, control.URL, "/v1/setupTest", map[string]any{"name": "removed identity", "application_knowledge": map[string]string{"test_id": "test:old"}}, http.StatusBadRequest)
	apiStatus(t, control.URL, "/v1/setupTest", map[string]any{"name": "removed generation", "generate_schema": true}, http.StatusBadRequest)
	for path, payload := range map[string]map[string]any{
		"/v1/startRun":     {"target_url": "http://sut.test"},
		"/v1/setupTest":    {"name": "old schema field", "sut_schemas": []any{}},
		"/v1/report":       {"artifacts_dir": t.TempDir()},
		"/v1/teardownTest": {"session_id": "unused", "unknown": true},
	} {
		apiStatus(t, control.URL, path, payload, http.StatusBadRequest)
	}
}
