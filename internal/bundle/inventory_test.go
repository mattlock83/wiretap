package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestBundleInventoryIncludesUnusedAndObservedDependencies(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(Identity{APIID: "orders", SUTTargetKind: "service", DependencyKinds: map[string]string{"stock": "mock", "unused": "service"}}); err != nil {
		t.Fatal(err)
	}
	tc := artifact.TestCase{ID: "create-order", Name: "creates an order", StartedAt: time.Now().UTC(), Status: "pass"}
	refs, err := w.RegisterPrepared(tc, []artifact.SchemaRef{{ID: "stock-api", Role: "dependency", Type: "openapi", AssetID: "stock", Source: artifact.SchemaSource{Type: "omitted"}}})
	if err != nil {
		t.Fatal(err)
	}
	art := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: []artifact.SchemaRef{refs[0].Ref}, Traffic: []artifact.Flow{
		{ID: "request", Channel: "test_to_sut", Protocol: "http", Request: &artifact.HttpMessage{Method: "POST", URL: "http://sut/orders"}},
		{ID: "stock-call", Channel: "sut_to_dependency", AssetID: "stock", Protocol: "http", Request: &artifact.HttpMessage{Method: "GET", URL: "http://stock/items"}},
		{ID: "unregistered-call", Channel: "sut_to_dependency", AssetID: "unregistered", Protocol: "http", Request: &artifact.HttpMessage{Method: "GET", URL: "http://other/items"}},
	}}
	if _, err := w.Commit(art); err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	run, captures, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(run.ProvidedAPIs) != 1 || len(run.Dependencies) != 3 || len(captures) != 1 {
		t.Fatalf("incomplete inventory: %+v", run)
	}
	byID := map[string]API{}
	for _, a := range run.Dependencies {
		byID[a.ID] = a
	}
	if a := byID["stock"]; !a.Configured || a.TargetKind != "mock" || !reflect.DeepEqual(a.ObservedIn, []string{tc.ID}) || len(a.Contracts) != 1 || a.Contracts[0].Source.Type != "omitted" {
		t.Fatalf("incorrect stock identity: %+v", a)
	}
	if a := byID["unused"]; !a.Configured || a.TargetKind != "service" || len(a.ObservedIn) != 0 {
		t.Fatalf("unused dependency lost: %+v", a)
	}
	if a := byID["unregistered"]; a.Configured || a.TargetKind != "unknown" || !reflect.DeepEqual(a.ObservedIn, []string{tc.ID}) {
		t.Fatalf("observation misclassified: %+v", a)
	}
	data, err := os.ReadFile(filepath.Join(w.Path(), "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"format_version", "provided_apis", "dependencies", "tests"} {
		if manifest[key] == nil {
			t.Fatalf("manifest missing %s", key)
		}
	}
	for _, key := range []string{"evidence_version", "attempts", "schemas"} {
		if manifest[key] != nil {
			t.Fatalf("obsolete manifest field %s", key)
		}
	}
	for _, name := range []string{"evidence", "schemas", "scenarios", "results"} {
		if _, err := os.Stat(filepath.Join(w.Path(), name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected folder %s: %v", name, err)
		}
	}
	if run.Attempts[0].Path != "tests/create-order.json" {
		t.Fatal("recording outside tests folder")
	}
	// A metadata-only dependency graph edit cannot silently contradict the recordings.
	var stored Run
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	stored.Dependencies[0].ObservedIn = []string{}
	data, err = json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path(), "bundle.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(w.Path()); err == nil || !strings.Contains(err.Error(), "inventory disagrees") {
		t.Fatalf("inconsistent inventory accepted: %v", err)
	}
}

func TestEmptyBundleAndConsumerOnlyInventory(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if run.ProvidedAPIs == nil || run.Dependencies == nil || run.Attempts == nil || len(run.ProvidedAPIs) != 0 || len(run.Dependencies) != 0 {
		t.Fatal("empty arrays must be explicit")
	}
	if err := w.Configure(Identity{DependencyKinds: map[string]string{"payments": "service"}}); err != nil {
		t.Fatal(err)
	}
	run, _, err = Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(run.ProvidedAPIs) != 0 || len(run.Dependencies) != 1 || len(run.Dependencies[0].Protocols) != 0 {
		t.Fatalf("consumer-only configured boundary lost: %+v", run)
	}
}

func TestInventoryPreservesUniqueObservationsAcrossCommits(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(Identity{APIID: "orders", DependencyKinds: map[string]string{"stock": "mock"}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"third", "first", "second"} {
		tc := artifact.TestCase{ID: id, Name: id, Status: "pass", StartedAt: time.Now().UTC()}
		refs, err := w.RegisterPrepared(tc, []artifact.SchemaRef{{ID: "stock-api", Role: "dependency", Type: "openapi", AssetID: "stock", Source: artifact.SchemaSource{Type: "omitted"}}})
		if err != nil {
			t.Fatal(err)
		}
		capture := &artifact.Artifact{Version: 1, TestCase: tc, Schemas: []artifact.SchemaRef{refs[0].Ref}, Traffic: []artifact.Flow{
			{ID: "inbound", Channel: "test_to_sut", Protocol: "http", Request: &artifact.HttpMessage{Method: "GET", URL: "http://sut/orders"}},
			{ID: "stock-first", Channel: "sut_to_dependency", AssetID: "stock", Protocol: "http", Request: &artifact.HttpMessage{Method: "GET", URL: "http://stock/items"}},
			{ID: "stock-second", Channel: "sut_to_dependency", AssetID: "stock", Protocol: "http", Request: &artifact.HttpMessage{Method: "GET", URL: "http://stock/items"}},
		}}
		if _, err := w.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	run, captures, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 3 || len(run.Schemas) != 1 || len(run.Dependencies) != 1 || len(run.Dependencies[0].Contracts) != 1 || len(run.ProvidedAPIs) != 1 {
		t.Fatalf("commits lost or duplicated declarations: %+v", run)
	}
	for _, api := range []API{run.ProvidedAPIs[0], run.Dependencies[0]} {
		if !reflect.DeepEqual(api.ObservedIn, []string{"first", "second", "third"}) || !reflect.DeepEqual(api.Protocols, []string{"http"}) {
			t.Fatalf("observations are not unique and sorted: %+v", api)
		}
	}
}
