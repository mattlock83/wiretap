package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
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

type transactionService struct {
	mu          sync.RWMutex
	byAccount   map[string][]transactionRecord
	accountBase string
	accountHTTP *http.Client
}

func newTransactionService(accountBase string) *transactionService {
	return &transactionService{
		byAccount:   map[string][]transactionRecord{},
		accountBase: accountBase,
		accountHTTP: &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *transactionService) append(accountID string, tx transactionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byAccount[accountID] = append(s.byAccount[accountID], tx)
}

func (s *transactionService) list(accountID string) []transactionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := make([]transactionRecord, len(s.byAccount[accountID]))
	copy(copied, s.byAccount[accountID])
	return copied
}

func main() {
	listen := envOr("TRANSACTION_SERVICE_LISTEN", "127.0.0.1:18111")
	accountBase := envOr("ACCOUNT_BASE_URL", "http://127.0.0.1:19090")
	svc := newTransactionService(accountBase)

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

	log.Printf("transaction service listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}

func (s *transactionService) handleCreate(w http.ResponseWriter, r *http.Request) {
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

func (s *transactionService) handleList(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error_code": "invalid_request",
			"message":    "account_id query parameter is required",
		})
		return
	}

	// The transaction service depends on the account service for account details.
	accountDetails, err := s.fetchAccount(accountID)
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

func (s *transactionService) fetchAccount(accountID string) (account, error) {
	endpoint := strings.TrimRight(s.accountBase, "/") + "/accounts/" + url.PathEscape(accountID)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return account{}, err
	}

	resp, err := s.accountHTTP.Do(req)
	if err != nil {
		return account{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return account{}, fmt.Errorf("account service returned %d: %s", resp.StatusCode, string(body))
	}

	var payload account
	if err := decodeJSON(resp.Body, &payload); err != nil {
		return account{}, err
	}
	return payload, nil
}

func decodeJSON(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
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
