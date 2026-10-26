package main

import (
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"path/filepath"
	"testing"
	"time"
)

func httpMockBundleFixture(t *testing.T) string {
	t.Helper()
	writer, err := bundle.New(filepath.Join(t.TempDir(), "capture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(bundle.Identity{
		Application: &bundle.Application{Name: "Orders", ID: "orders-app"}, APIID: "orders-api",
		DependencyKinds: map[string]string{"catalog": "mock", "inventory": "mock", "unused": "mock", "billing": "service", "unknown": "unknown"},
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC)
	flow := func(id, asset, path string, status int, offset time.Duration) artifact.Flow {
		channel, host := "sut_to_dependency", asset
		if asset == "" {
			channel, host = "test_to_sut", "sut"
		}
		return artifact.Flow{ID: id, AssetID: asset, Protocol: "http", Channel: channel, StartedAt: start.Add(offset),
			Request:  &artifact.HttpMessage{Method: "GET", URL: "http://" + host + path},
			Response: &artifact.HttpMessage{Status: status, Body: &artifact.Body{Text: `{"asset":"` + asset + `"}`}},
		}
	}
	first := flow("z-earlier", "inventory", "/first", 201, time.Second)
	first.Request.Method = "POST"
	first.Request.Body = &artifact.Body{Text: `{"sku":"one"}`}
	schemas := []artifact.SchemaRef{
		{ID: "inventory-api", Role: "dependency", AssetID: "inventory", AssetName: "Inventory", Type: "openapi", Source: artifact.SchemaSource{Type: "omitted"}},
		{ID: "catalog-api", Role: "dependency", AssetID: "catalog", AssetName: "Catalog", Type: "openapi", Source: artifact.SchemaSource{Type: "omitted"}},
	}
	for _, capture := range []*artifact.Artifact{
		{Version: 1, TestCase: artifact.TestCase{ID: "first", Name: "first test", StartedAt: start, Status: "pass"}, Traffic: []artifact.Flow{
			flow("sut-first", "", "/first", 200, 0), flow("a-later", "inventory", "/later", 202, 2*time.Second), first,
			flow("catalog-first", "catalog", "/first", 200, 3*time.Second), flow("service", "billing", "/private", 200, 4*time.Second),
			flow("unknown", "unknown", "/private", 200, 5*time.Second),
		}},
		{Version: 1, TestCase: artifact.TestCase{ID: "second", Name: "second test", StartedAt: start.Add(time.Minute), Status: "pass"}, Traffic: []artifact.Flow{
			flow("sut-second", "", "/second", 404, time.Minute), flow("catalog-second", "catalog", "/second", 204, time.Minute+time.Second),
		}},
	} {
		frozen, err := writer.RegisterPrepared(capture.TestCase, schemas)
		if err != nil {
			t.Fatal(err)
		}
		capture.Schemas = nil
		for _, schema := range frozen {
			capture.Schemas = append(capture.Schemas, schema.Ref)
		}
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return writer.Path()
}
