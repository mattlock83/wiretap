package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInventoryServiceReturnsSeededProducts(t *testing.T) {
	handler := newInventoryHandler()
	for _, scenario := range []struct {
		id        string
		available bool
		quantity  int
	}{
		{id: "coffee-beans", available: true, quantity: 12},
		{id: "decaf", available: false, quantity: 0},
	} {
		t.Run(scenario.id, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/inventory/products/"+scenario.id+"?quantity=1", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("unexpected inventory response: %d %s", response.Code, response.Body.String())
			}
			var selected product
			if err := json.NewDecoder(response.Body).Decode(&selected); err != nil {
				t.Fatal(err)
			}
			if selected.ProductID != scenario.id || selected.Available != scenario.available || selected.Quantity != scenario.quantity {
				t.Fatalf("unexpected inventory product: %#v", selected)
			}
		})
	}
}

func TestInventoryServiceConfiguresStandardPactProviderState(t *testing.T) {
	handler := newInventoryHandler()
	setup := httptest.NewRecorder()
	handler.ServeHTTP(setup, httptest.NewRequest(http.MethodPost, "/__pact/provider-states",
		strings.NewReader(`{"state":"product is available","action":"setup","params":{"productId":"tea"}}`)))
	if setup.Code != http.StatusOK {
		t.Fatalf("provider state setup failed: %d %s", setup.Code, setup.Body.String())
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/inventory/products/tea?quantity=2", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":true`) {
		t.Fatalf("provider state did not configure the product: %d %s", response.Code, response.Body.String())
	}
}

func TestInventoryServiceRejectsUnknownProviderStates(t *testing.T) {
	response := httptest.NewRecorder()
	newInventoryHandler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/__pact/provider-states",
		strings.NewReader(`{"state":"unexpected","params":{"productId":"tea"}}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider state was accepted: %d %s", response.Code, response.Body.String())
	}
}
