package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type orderRequest struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

type inventoryResponse struct {
	ProductID string `json:"productId"`
	Available bool   `json:"available"`
	Quantity  int    `json:"quantity"`
}

func main() {
	listen := os.Getenv("ORDERS_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:18110"
	}
	inventoryURL := os.Getenv("INVENTORY_BASE_URL")
	if inventoryURL == "" {
		inventoryURL = "http://127.0.0.1:18111"
	}
	server := &http.Server{
		Addr: listen, Handler: newOrdersHandler(inventoryURL, http.DefaultClient),
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
	log.Printf("orders service listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}

func newOrdersHandler(inventoryURL string, client *http.Client) http.Handler {
	clone := *client
	clone.Transport = otelhttp.NewTransport(client.Transport, otelhttp.WithPropagators(propagation.TraceContext{}))
	client = &clone

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var order orderRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&order); err != nil || strings.TrimSpace(order.ProductID) == "" || order.Quantity < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a productId and positive quantity are required"})
			return
		}

		inventory, err := fetchInventory(r, client, inventoryURL, order)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if !inventory.Available || inventory.Quantity < order.Quantity {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "product is unavailable", "productId": order.ProductID,
			})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"orderId": "order-" + order.ProductID, "productId": order.ProductID,
			"quantity": order.Quantity, "status": "accepted",
		})
	})
	return otelhttp.NewHandler(mux, "orders", otelhttp.WithPropagators(propagation.TraceContext{}))
}

func fetchInventory(r *http.Request, client *http.Client, base string, order orderRequest) (inventoryResponse, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/") + "/inventory/products/" + url.PathEscape(order.ProductID))
	if err != nil {
		return inventoryResponse{}, fmt.Errorf("invalid inventory URL: %w", err)
	}
	query := parsed.Query()
	query.Set("quantity", strconv.Itoa(order.Quantity))
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, parsed.String(), nil)
	if err != nil {
		return inventoryResponse{}, err
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return inventoryResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return inventoryResponse{}, errors.New("inventory request failed")
	}
	var inventory inventoryResponse
	if err := json.NewDecoder(response.Body).Decode(&inventory); err != nil {
		return inventoryResponse{}, fmt.Errorf("invalid inventory response: %w", err)
	}
	return inventory, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
