package consumerprovider

import (
	"encoding/json"
	"net/http"
	"time"
)

// inventoryHandler represents the provider. The renamed field is a deliberate
// API change; the matching provider test and OpenAPI document change with it.
func inventoryHandler(renamed bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stock/widget-1", func(w http.ResponseWriter, r *http.Request) {
		field := "quantity"
		if renamed {
			field = "availableQuantity"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{field: 3})
	})
	return mux
}

// ordersHandler represents a consumer that still needs the original field.
// Its tests use a mock; they do not connect to the real inventory provider.
func ordersHandler(inventoryURL string) http.Handler {
	client := &http.Client{Timeout: 5 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/order-1", func(w http.ResponseWriter, r *http.Request) {
		response, err := client.Get(inventoryURL + "/stock/widget-1")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		var stock struct {
			Quantity int `json:"quantity"`
		}
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&stock) != nil {
			http.Error(w, "invalid inventory response", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ready": stock.Quantity > 0})
	})
	return mux
}
