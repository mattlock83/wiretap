package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
)

type responsePayload struct {
	Message string `json:"message"`
	ID      string `json:"id"`
	Action  string `json:"action"`
}

func main() {
	listen := envOr("SUT_LISTEN", "127.0.0.1:18090")
	mux := http.NewServeMux()
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithPropagators(propagation.TraceContext{}))}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 512))
	})

	mux.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     *string `json:"id"`
			Action *string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		// The contract requires both strings; missing and null values are invalid.
		// Pointers preserve the distinction between missing and valid empty strings.
		if request.ID == nil || request.Action == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id and action are required strings"})
			return
		}
		if dependencyURL := os.Getenv("DEPENDENCY_BASE_URL"); dependencyURL != "" {
			data, err := json.Marshal(request)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			outbound, err := http.NewRequestWithContext(r.Context(), http.MethodPost, dependencyURL+"/inventory/check", bytes.NewReader(data))
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			outbound.Header.Set("Content-Type", "application/json")

			response, err := client.Do(outbound)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "inventory dependency request failed"})
				return
			}
		}
		payload := responsePayload{Message: "processed", ID: *request.ID, Action: *request.Action}
		writeJSON(w, http.StatusOK, payload)
	})

	var handler http.Handler = otelhttp.NewHandler(mux, "sut", otelhttp.WithPropagators(propagation.TraceContext{}))
	if os.Getenv("DEMO_DISABLE_OTEL") == "1" {
		handler = mux
		client.Transport = http.DefaultTransport
	}
	server := &http.Server{
		Addr:         listen,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("sut listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}

func envOr(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
