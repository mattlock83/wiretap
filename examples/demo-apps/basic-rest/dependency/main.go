package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// This process represents an independently owned dependency or mock server.
// The example runner, never Wiretap, owns its configuration and lifecycle.
func main() {
	listen := os.Getenv("DEPENDENCY_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:18091"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/inventory/check", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": request.ID, "available": true})
	})
	server := &http.Server{
		Addr: listen, Handler: mux, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	log.Printf("independently managed dependency listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}
