package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrdersServiceUsesInventoryAndPropagatesCaptureTrace(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/inventory/products/coffee-beans" || r.URL.Query().Get("quantity") != "2" {
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-Phrond-Trace-Id") != "trace-123" {
			t.Errorf("capture correlation header was not propagated: %#v", r.Header)
		}
		writeJSON(w, http.StatusOK, inventoryResponse{ProductID: "coffee-beans", Available: true, Quantity: 12})
	}))
	t.Cleanup(provider.Close)

	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"productId":"coffee-beans","quantity":2}`))
	request.Header.Set("X-Phrond-Trace-Id", "trace-123")
	response := httptest.NewRecorder()
	newOrdersHandler(provider.URL, provider.Client()).ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	var order struct {
		OrderID   string `json:"orderId"`
		ProductID string `json:"productId"`
		Quantity  int    `json:"quantity"`
		Status    string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		t.Fatal(err)
	}
	if order.OrderID != "order-coffee-beans" || order.ProductID != "coffee-beans" || order.Quantity != 2 || order.Status != "accepted" {
		t.Fatalf("unexpected order response: %#v", order)
	}
}

func TestOrdersServiceRejectsUnavailableInventory(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, inventoryResponse{ProductID: "decaf", Available: false, Quantity: 0})
	}))
	t.Cleanup(provider.Close)
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"productId":"decaf","quantity":1}`))
	response := httptest.NewRecorder()
	newOrdersHandler(provider.URL, provider.Client()).ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "product is unavailable") {
		t.Fatalf("unexpected unavailable response: %d %s", response.Code, response.Body.String())
	}
}

func TestOrdersServiceRejectsInvalidOrders(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"productId":"coffee-beans","quantity":0}`))
	response := httptest.NewRecorder()
	newOrdersHandler("http://inventory.invalid", http.DefaultClient).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid order was accepted: %d %s", response.Code, response.Body.String())
	}
}
