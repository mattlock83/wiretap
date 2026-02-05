package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

type downstreamResult struct {
	Path       string `json:"path"`
	StatusCode int    `json:"status"`
	Body       string `json:"body"`
}

type responsePayload struct {
	Message    string             `json:"message"`
	Downstream []downstreamResult `json:"downstream"`
}

func main() {
	listen := envOr("SUT_LISTEN", "127.0.0.1:18090")
	depBase := envOr("DEPENDENCY_BASE_URL", "http://127.0.0.1:19090")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		paths := []string{"/alpha", "/beta"}
		results := make([]downstreamResult, 0, len(paths))
		for _, path := range paths {
			status, respBody := callDependency(depBase+path, bodyBytes)
			results = append(results, downstreamResult{Path: path, StatusCode: status, Body: respBody})
		}

		payload := responsePayload{
			Message:    "processed",
			Downstream: results,
		}
		writeJSON(w, http.StatusOK, payload)
	})

	server := &http.Server{
		Addr:         listen,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("sut listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}

func callDependency(url string, body []byte) (int, string) {
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return http.StatusBadGateway, err.Error()
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
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
