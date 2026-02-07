package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/phrond/evermock/examples/complex-mixed/accountpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type transactionCreateRequest struct {
	FromAccount string  `json:"from_account"`
	ToAccount   string  `json:"to_account"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
}

type transactionRecord struct {
	TransactionID       string  `json:"transaction_id"`
	Direction           string  `json:"direction"`
	AccountID           string  `json:"account_id"`
	CounterpartyAccount string  `json:"counterparty_account"`
	Amount              float64 `json:"amount"`
	Currency            string  `json:"currency"`
	CreatedAt           string  `json:"created_at"`
}

type account struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Balance  float64 `json:"balance"`
	Currency string  `json:"currency"`
	Status   string  `json:"status"`
}

type service struct {
	mu          sync.RWMutex
	byAccount   map[string][]transactionRecord
	accountAddr string
}

func newService(accountAddr string) *service {
	return &service{
		byAccount:   map[string][]transactionRecord{},
		accountAddr: accountAddr,
	}
}

func (s *service) append(accountID string, tx transactionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byAccount[accountID] = append(s.byAccount[accountID], tx)
}

func (s *service) list(accountID string) []transactionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := make([]transactionRecord, len(s.byAccount[accountID]))
	copy(copied, s.byAccount[accountID])
	return copied
}

func (s *service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req transactionCreateRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error_code": "invalid_request",
			"message":    "request body must be valid JSON",
		})
		return
	}

	if req.FromAccount == "" || req.ToAccount == "" || req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error_code": "invalid_request",
			"message":    "from_account, to_account and amount > 0 are required",
		})
		return
	}

	if req.Currency == "" {
		req.Currency = "USD"
	}

	now := time.Now().UTC()
	txID := fmt.Sprintf("txn-%d", now.UnixNano())

	s.append(req.FromAccount, transactionRecord{
		TransactionID:       txID,
		Direction:           "debit",
		AccountID:           req.FromAccount,
		CounterpartyAccount: req.ToAccount,
		Amount:              req.Amount,
		Currency:            req.Currency,
		CreatedAt:           now.Format(time.RFC3339),
	})
	s.append(req.ToAccount, transactionRecord{
		TransactionID:       txID,
		Direction:           "credit",
		AccountID:           req.ToAccount,
		CounterpartyAccount: req.FromAccount,
		Amount:              req.Amount,
		Currency:            req.Currency,
		CreatedAt:           now.Format(time.RFC3339),
	})

	writeJSON(w, http.StatusCreated, map[string]string{
		"transaction_id": txID,
		"status":         "recorded",
	})
}

func (s *service) handleList(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error_code": "invalid_request",
			"message":    "account_id query parameter is required",
		})
		return
	}

	accountDetails, err := s.fetchAccount(r.Context(), accountID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error_code": "account_lookup_failed",
			"message":    err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"account":      accountDetails,
		"transactions": s.list(accountID),
	})
}

func (s *service) fetchAccount(ctx context.Context, accountID string) (account, error) {
	conn, err := dialGRPC(ctx, s.accountAddr)
	if err != nil {
		return account{}, err
	}
	defer conn.Close()

	client := accountpb.NewAccountServiceClient(conn)
	reply, err := client.GetAccount(ctx, accountpb.NewAccountRequest(accountID))
	if err != nil {
		return account{}, err
	}

	if accountpb.GetString(reply, "id") == "" {
		return account{}, fmt.Errorf("account response missing id")
	}

	return account{
		ID:       accountpb.GetString(reply, "id"),
		Name:     accountpb.GetString(reply, "name"),
		Balance:  accountpb.GetFloat64(reply, "balance"),
		Currency: accountpb.GetString(reply, "currency"),
		Status:   accountpb.GetString(reply, "status"),
	}, nil
}

func main() {
	listen := getenv("TRANSACTION_SERVICE_LISTEN", "127.0.0.1:18121")
	accountAddr := getenv("ACCOUNT_GRPC_ADDR", "127.0.0.1:19191")
	svc := newService(accountAddr)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/transactions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			svc.handleCreate(w, r)
		case http.MethodGet:
			svc.handleList(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	server := &http.Server{
		Addr:         listen,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("payment grpc transaction service listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}

func dialGRPC(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	return grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(creds), grpc.WithBlock())
}

func decodeJSON(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
