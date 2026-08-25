package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type product struct {
	ProductID string `json:"productId"`
	Available bool   `json:"available"`
	Quantity  int    `json:"quantity"`
	Warehouse string `json:"warehouse"`
}

type inventoryService struct {
	mu       sync.RWMutex
	products map[string]product
}

func main() {
	listen := os.Getenv("INVENTORY_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:18111"
	}
	server := &http.Server{
		Addr: listen, Handler: newInventoryHandler(), ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	log.Printf("inventory service listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}

func newInventoryHandler() http.Handler {
	service := &inventoryService{products: map[string]product{
		"coffee-beans": {ProductID: "coffee-beans", Available: true, Quantity: 12, Warehouse: "melbourne"},
		"decaf":        {ProductID: "decaf", Available: false, Quantity: 0, Warehouse: "melbourne"},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /inventory/products/{productId}", service.getProduct)
	mux.HandleFunc("POST /__pact/provider-states", service.setProviderState)
	return mux
}

func (service *inventoryService) getProduct(w http.ResponseWriter, r *http.Request) {
	quantity, err := strconv.Atoi(r.URL.Query().Get("quantity"))
	if err != nil || quantity < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a positive quantity is required"})
		return
	}
	service.mu.RLock()
	selected, exists := service.products[r.PathValue("productId")]
	service.mu.RUnlock()
	if !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "product not found"})
		return
	}
	writeJSON(w, http.StatusOK, selected)
}

func (service *inventoryService) setProviderState(w http.ResponseWriter, r *http.Request) {
	var state struct {
		State  string         `json:"state"`
		Action string         `json:"action"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid provider state"})
		return
	}
	if state.Action == "teardown" {
		w.WriteHeader(http.StatusOK)
		return
	}
	productID, _ := state.Params["productId"].(string)
	if strings.TrimSpace(productID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider state requires productId"})
		return
	}
	selected := product{ProductID: productID, Warehouse: "melbourne"}
	switch state.State {
	case "product is available":
		selected.Available = true
		selected.Quantity = 12
	case "product is unavailable":
		selected.Available = false
		selected.Quantity = 0
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown provider state"})
		return
	}
	service.mu.Lock()
	service.products[productID] = selected
	service.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"productId": productID})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
