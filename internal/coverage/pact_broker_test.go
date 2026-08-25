package coverage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
)

func TestPactBrokerImportsExportedContract(t *testing.T) {
	brokerURL := strings.TrimSpace(os.Getenv("PACT_BROKER_BASE_URL"))
	if brokerURL == "" {
		t.Skip("set PACT_BROKER_BASE_URL or run ./examples/run-pact-broker-e2e.sh to test against a real Pact Broker")
	}
	parsed, err := url.Parse(brokerURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		t.Fatalf("PACT_BROKER_BASE_URL must be an absolute HTTP(S) URL: %q", brokerURL)
	}

	directory := t.TempDir()
	doc := pactArtifact("broker-test", "imports a captured consumer contract", "inventory", http.MethodGet,
		"/items/widget?include=stock", http.StatusOK, `{"id":"widget","available":true,"warehouse":"private"}`)
	doc.Assertions = []artifact.Assertion{{
		ID: "assert-available", Channel: "sut_to_dependency", Phase: "response", FlowID: "flow-1",
		Field: "$.available", PathLang: "jsonpath", Operator: "equals", Expected: true, Result: "pass",
	}}
	doc.Traffic[0].AssertionIDs = []string{"assert-available"}
	doc.Traffic[0].ProviderStates = []artifact.ProviderState{{
		Name: "widget is available", Params: map[string]any{"productId": "widget"},
	}}
	writePactArtifact(t, directory, doc)
	writePactArtifact(t, directory, clonePactArtifact(t, doc, "broker-test-duplicate", "duplicate captured contract"))
	report, err := Generate(t.Context(), directory, "", false)
	if err != nil {
		t.Fatalf("generate capture report: %v", err)
	}
	export, err := PactsFromReport(report, PactOptions{
		ConsumerName: "phrond-broker-test-orders",
		ProviderNames: map[string]string{
			"inventory": "phrond-broker-test-inventory",
		},
	})
	if err != nil || len(export.Contracts) != 1 {
		t.Fatalf("export broker-ready Pact contract: export=%#v err=%v", export, err)
	}
	contract := export.Contracts[0]
	var encoded bytes.Buffer
	if err := WritePact(&encoded, contract); err != nil {
		t.Fatalf("encode exported Pact: %v", err)
	}
	version := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	publicationURL := strings.TrimRight(brokerURL, "/") + "/pacts/provider/" +
		url.PathEscape(contract.Provider.Name) + "/consumer/" +
		url.PathEscape(contract.Consumer.Name) + "/version/" + url.PathEscape(version)

	for attempt := range 2 {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, publicationURL, bytes.NewReader(encoded.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("publish exported Pact to the real broker (attempt %d): %v", attempt+1, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatalf("read Pact Broker publication response: %v", readErr)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			t.Fatalf("real Pact Broker rejected the exported V3 contract on attempt %d: status=%d body=%s", attempt+1, response.StatusCode, body)
		}
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, publicationURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("retrieve published Pact from the real broker: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("real Pact Broker did not retain the published contract: status=%d body=%s", response.StatusCode, body)
	}
	var imported Pact
	if err := json.NewDecoder(response.Body).Decode(&imported); err != nil {
		t.Fatalf("decode contract returned by Pact Broker: %v", err)
	}
	if imported.Consumer.Name != contract.Consumer.Name || imported.Provider.Name != contract.Provider.Name ||
		imported.Metadata.PactSpecification.Version != "3.0.0" {
		t.Fatalf("Pact Broker did not preserve contract participants or V3 metadata: %#v", imported)
	}
	if len(imported.Interactions) != 1 {
		t.Fatalf("duplicate captured interactions were not deduplicated in the imported contract: %#v", imported.Interactions)
	}
	interaction := imported.Interactions[0]
	if len(interaction.ProviderStates) != 1 || interaction.ProviderStates[0].Name != "widget is available" {
		t.Fatalf("Pact Broker did not preserve the standard provider state: %#v", interaction.ProviderStates)
	}
	body, ok := interaction.Response.Body.(map[string]any)
	if !ok || len(body) != 1 || body["available"] != true {
		t.Fatalf("Pact Broker did not retain the asserted-only consumer expectation: %#v", interaction.Response.Body)
	}
	if interaction.Response.MatchingRules == nil ||
		interaction.Response.MatchingRules.Body["$.available"].Matchers[0].Match != "equality" {
		t.Fatalf("Pact Broker did not preserve the Pact V3 matching rule: %#v", interaction.Response.MatchingRules)
	}
}
