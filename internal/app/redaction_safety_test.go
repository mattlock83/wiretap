package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/postman"
	"github.com/phrond/wiretap/internal/project"
	"github.com/phrond/wiretap/internal/proxy"
)

func TestRedactedInvalidHTTPSecretAbsentFromArchiveAndExports(t *testing.T) {
	const secret = "wiretap-negative-secret-marker"
	w, err := bundle.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewTestCaseStore(w, 1024)
	if _, err := store.StartCase("invalid sensitive body", nil, nil, "http://provider"); err != nil {
		t.Fatal(err)
	}
	store.AddSchema(artifact.SchemaRef{ID: "api", Role: "sut", Type: "openapi", Source: artifact.SchemaSource{Type: "inline", Content: `openapi: 3.0.3
info: {title: Safety, version: "1"}
paths:
  /items:
    post:
      requestBody:
        content:
          application/json:
            schema: {type: object, properties: {token: {type: string, enum: [public]}}}
      responses:
        "200": {description: OK}
`}})
	store.AddRedaction(artifact.Redaction{Field: "request.body.token", Rule: "remove"})
	store.AddRedaction(artifact.Redaction{Field: "request.query.token", Rule: "remove"})
	if err := store.RegisterActive(); err != nil {
		t.Fatal(err)
	}
	store.RecordHTTP(proxy.HTTPExchange{Method: "POST", URL: "http://provider/items?token=" + secret, RequestHeaders: http.Header{"Content-Type": {"application/json"}}, RequestBody: []byte(`{"token":"` + secret + `"}`), Status: 200})
	a, _, err := store.FinishCase("pass", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.SchemaValidation.Status != "fail" || a.TestCase.Status != "fail" {
		t.Fatalf("redaction weakened validation: %+v", a.SchemaValidation)
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bundle.Load(w.Path()); err != nil {
		t.Fatal(err)
	}
	check := func(data []byte) {
		t.Helper()
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("secret persisted: %s", data)
		}
	}
	data, _ := json.Marshal(a)
	check(data)
	err = filepath.WalkDir(w.Path(), func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil {
			check(b)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := coverage.Generate(context.Background(), w.Path(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(report)
	check(data)
	for _, spec := range report.Contracts.Specs {
		for _, target := range spec.Targets {
			if target.Phase == "request" && target.State == "covered" {
				t.Fatalf("redacted request earned coverage: %+v", target)
			}
		}
	}
	exports, err := postman.GenerateAll(w.Path(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, export := range exports {
		var out bytes.Buffer
		if err := postman.WriteExport(&out, export); err != nil {
			t.Fatal(err)
		}
		check(out.Bytes())
	}
}

func TestProjectRedactionsCannotBeRemovedByTestOverrides(t *testing.T) {
	const secret = "configured-header-secret-marker"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != secret {
			t.Error("live request was redacted")
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	control, instance := dependencyCaptureRuntime(t)
	optional := false
	instance.project = &project.Config{SUT: project.API{URL: upstream.URL, SchemaRequired: &optional}, Redactions: []artifact.Redaction{{Field: "request.headers.authorization", Rule: "mask"}}}
	for _, override := range [][]artifact.Redaction{{}, {{Field: "request.headers.authorization", Rule: "mask", Replacement: "test-only-replacement"}}, {{Field: "request.headers.authorization", Rule: "mask"}, {Field: "request.headers.authorization", Rule: "mask", Replacement: "test-only-replacement"}}} {
		session := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "central policy", "redactions": override})
		req, _ := http.NewRequest("GET", session["sut_url"].(string)+"/items", nil)
		req.Header.Set("Authorization", secret)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		result := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"]})
		capture := dependencyArtifact(t, result)
		if capture.Traffic[0].Request.HTTP.Headers["Authorization"][0] != "****" {
			t.Fatal("test replaced the project mask")
		}
		data, _ := json.Marshal(result)
		if strings.Contains(string(data), secret) {
			t.Fatalf("project policy overridden: %s", data)
		}
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}
