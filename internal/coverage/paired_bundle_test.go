package coverage

import (
	"context"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

func TestProviderAndConsumerBundlesRetainJoinableEvidence(t *testing.T) {
	const schema = `openapi: 3.0.0
info: {title: Inventory, version: "1"}
paths:
  /items:
    get:
      responses:
        "200": {description: ok}
`
	makeBundle := func(runAPI, role, channel, assetID string) (*bundle.Run, *Report) {
		t.Helper()
		writer, err := bundle.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		identity := bundle.Identity{APIID: runAPI}
		if assetID != "" {
			identity.DependencyKinds = map[string]string{assetID: "mock"}
		}
		if err := writer.Configure(identity); err != nil {
			t.Fatal(err)
		}
		attempt := artifact.TestCase{ID: "attempt-1", Name: "reads inventory", StartedAt: time.Now().UTC(), Status: "pass"}
		frozen, err := writer.RegisterPrepared(attempt, []artifact.SchemaRef{{ID: "inventory", Role: role, Type: "openapi", AssetID: assetID, Source: artifact.SchemaSource{Type: "inline", Content: schema}}})
		if err != nil {
			t.Fatal(err)
		}
		capture := &artifact.Artifact{Version: 1, TestCase: attempt, Schemas: []artifact.SchemaRef{frozen[0].Ref}, Traffic: []artifact.Flow{{
			ID: "flow-1", Protocol: "http", Channel: channel, AssetID: assetID,
			Request:  &artifact.HttpMessage{Method: "GET", URL: "http://inventory/items"},
			Response: &artifact.HttpMessage{Status: 200, Body: &artifact.Body{Text: `{"available":true}`}},
		}}}
		if _, err := writer.Commit(capture); err != nil {
			t.Fatal(err)
		}
		if err := writer.Finalize("complete"); err != nil {
			t.Fatal(err)
		}
		run, captures, err := bundle.Load(writer.Path())
		if err != nil || len(captures) != 1 || len(captures[0].Traffic) != 1 {
			t.Fatalf("load paired fixture: %v", err)
		}
		report, err := Generate(context.Background(), writer.Path(), "", false)
		if err != nil {
			t.Fatal(err)
		}
		return run, report
	}
	provider, provided := makeBundle("inventory", "sut", "test_to_sut", "")
	consumer, dependent := makeBundle("shop", "dependency", "sut_to_dependency", "inventory")
	if consumer.DependencyKinds["inventory"] != "mock" {
		t.Fatal("consumer lost mock declaration")
	}
	if len(provided.Provided.Specs) != 1 || len(dependent.Dependent.Specs) != 1 {
		t.Fatal("provider or consumer API evidence missing")
	}
	providerSpec := provided.Provided.Specs[0]
	consumerSpec := dependent.Dependent.Specs[0]
	if len(providerSpec.Exercised) != 1 || len(consumerSpec.Exercised) != 1 {
		t.Fatal("matching operation evidence missing")
	}
	if provider.APIID != consumerSpec.AssetID || providerSpec.Exercised[0].Operation != consumerSpec.Exercised[0].Operation {
		t.Fatalf("bundles cannot join on API and operation: %q / %q", provider.APIID, consumerSpec.AssetID)
	}
	if len(providerSpec.Exercised[0].Samples) != 1 || len(consumerSpec.Exercised[0].Samples) != 1 {
		t.Fatal("captured request/response evidence missing")
	}
}
