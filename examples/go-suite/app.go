package orders

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// NewHandler is the application's normal handler, using synthetic test data.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id < 1 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid order ID"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": id, "status": "ready"})
	})
	return mux
}
