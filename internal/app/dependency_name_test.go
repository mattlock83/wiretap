package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/project"
)

func TestDependencyDisplayNameWithoutSchema(t *testing.T) {
	for _, source := range []string{"project", "API"} {
		t.Run(source, func(t *testing.T) {
			mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("available"))
			}))
			defer mock.Close()
			var dependencyURL string
			sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				res, err := http.Get(dependencyURL + "/stock")
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				defer res.Body.Close()
				w.WriteHeader(res.StatusCode)
				_, _ = io.Copy(w, res.Body)
			}))
			defer sut.Close()
			control, instance := dependencyCaptureRuntime(t)
			setup := map[string]any{"name": "named mock without a contract"}
			if source == "project" {
				data := "version: 1\nsut: {id: orders-api, url: " + sut.URL + ", schema_required: false}\ndependencies:\n  inventory: {name: Inventory Service, kind: mock, url: " + mock.URL + ", schema_required: false}\n"
				cfg, err := project.Decode([]byte(data), filepath.Join(t.TempDir(), ".wiretap.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				instance.project = cfg
			} else {
				setup["sut_base_url"] = sut.URL
				setup["schema_required"] = false
				setup["dependencies"] = []map[string]any{{"asset_id": "inventory", "asset_name": "Inventory Service", "upstream_kind": "mock", "upstream_url": mock.URL, "schema_required": false}}
			}
			postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
			session := postDependencyJSON(t, control.URL, "/v1/setupTest", setup)
			dependencyURL = session["dependencies"].(map[string]any)["inventory"].(string)
			res, err := http.Get(session["sut_url"].(string) + "/orders")
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("SUT request = %s", res.Status)
			}
			finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": session["session_id"]})
			run, captures, err := bundle.Load(finished["bundle_path"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if run.DependencyKinds["inventory"] != "mock" || len(captures) != 1 {
				t.Fatalf("dependency kind or test capture lost: %+v", run)
			}
			for _, schemas := range [][]artifact.SchemaRef{run.Schemas, captures[0].Schemas} {
				var found bool
				for _, schema := range schemas {
					if schema.Role == "dependency" && schema.AssetID == "inventory" {
						found = true
						if schema.AssetName != "Inventory Service" || schema.Source.Type != "omitted" {
							t.Fatalf("named mock identity not retained without a contract: %+v", schema)
						}
					}
				}
				if !found {
					t.Fatalf("missing inventory dependency schema: %+v", schemas)
				}
			}
			var capturedDependency bool
			for _, flow := range captures[0].Traffic {
				if flow.Protocol == "http" && flow.Channel == "sut_to_dependency" && flow.AssetID == "inventory" {
					capturedDependency = true
				}
			}
			if !capturedDependency {
				t.Fatalf("named mock traffic was not captured: %+v", captures[0].Traffic)
			}
			postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
		})
	}
}
