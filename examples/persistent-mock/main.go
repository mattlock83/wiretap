// Command persistent-mock runs a long-lived mock server built directly on the
// phrond mock package. It is meant for standing a persistent dependency fake in
// front of (or instead of) a running dev environment — e.g. a post-merge dev
// stack where you want a few routes pinned to deterministic responses.
//
// Routes are registered as static `Default` stubs: they use the built-in
// matcher, are never consumed, and persist for the life of the process. Any
// request that does not match a stub is forwarded to -upstream when set, so you
// can override a handful of endpoints while letting the rest hit the real env.
//
// Usage:
//
//	go run ./examples/persistent-mock -addr 127.0.0.1:8080
//	go run ./examples/persistent-mock -addr 127.0.0.1:8080 -upstream https://dev.internal.example.com
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/phrond/evermock/internal/mock"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	upstream := flag.String("upstream", "", "optional base URL to forward unmatched requests to (e.g. a running dev environment)")
	flag.Parse()

	server := mock.NewServer(nil, nil)

	// A persistent server should not accumulate recorded exchanges in memory.
	server.SetRecordingEnabled(false)

	if *upstream != "" {
		if err := server.SetFallbackUpstream(*upstream); err != nil {
			log.Fatalf("invalid -upstream: %v", err)
		}
	}

	for _, stub := range routes() {
		server.RegisterStub(stub)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bound, err := server.Start(ctx, *addr)
	if err != nil {
		log.Fatalf("start mock server: %v", err)
	}
	log.Printf("persistent mock listening on http://%s", bound)
	if *upstream != "" {
		log.Printf("forwarding unmatched requests to %s", *upstream)
	}

	<-ctx.Done()
	log.Println("shutting down")
	if err := server.Stop(context.Background()); err != nil {
		log.Printf("stop: %v", err)
		os.Exit(1)
	}
}

// routes defines the static responses this mock serves. Edit these for your own
// dependency. Default: true keeps each route persistent (not consumed on match).
func routes() []mock.Stub {
	json := map[string]string{"Content-Type": "application/json"}
	return []mock.Stub{
		{
			ID:       "account-acct-100",
			Default:  true,
			Match:    mock.StubMatch{Method: "GET", Path: "/accounts/acct-100"},
			Response: mock.StubResponse{Status: http.StatusOK, Headers: json, Body: `{"id":"acct-100","name":"Alice","balance":1000,"currency":"USD","status":"active"}`},
		},
		{
			ID:       "submit-payment",
			Default:  true,
			Match:    mock.StubMatch{Method: "POST", Path: "/payments/submit"},
			Response: mock.StubResponse{Status: http.StatusCreated, Headers: json, Body: `{"transaction_id":"txn-0001","status":"accepted"}`},
		},
	}
}
