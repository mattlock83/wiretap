package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxPaymentAmount = 5000.0

type paymentSubmitRequest struct {
	FromAccount string  `json:"from_account"`
	ToAccount   string  `json:"to_account"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
}

type account struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Balance  float64 `json:"balance"`
	Currency string  `json:"currency"`
	Status   string  `json:"status"`
}

type paymentSubmitResponse struct {
	PaymentID        string  `json:"payment_id"`
	Status           string  `json:"status"`
	TransactionID    string  `json:"transaction_id"`
	FromAccount      string  `json:"from_account"`
	ToAccount        string  `json:"to_account"`
	FromAccountName  string  `json:"from_account_name"`
	ToAccountName    string  `json:"to_account_name"`
	Amount           float64 `json:"amount"`
	Currency         string  `json:"currency"`
	RemainingBalance float64 `json:"remaining_balance"`
}

type transactionCreateRequest struct {
	FromAccount string  `json:"from_account"`
	ToAccount   string  `json:"to_account"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
}

type transactionCreateResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
}

type errorResponse struct {
	ErrorCode        string   `json:"error_code"`
	Message          string   `json:"message"`
	Limit            *float64 `json:"limit,omitempty"`
	AvailableBalance *float64 `json:"available_balance,omitempty"`
}

func main() {
	listen := envOr("PAYMENT_SUT_LISTEN", "127.0.0.1:18110")
	accountBaseURL := envOr("ACCOUNT_BASE_URL", "http://127.0.0.1:19090")
	transactionBaseURL := envOr("TRANSACTION_BASE_URL", "http://127.0.0.1:18111")

	client := &http.Client{Timeout: 5 * time.Second}
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
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

	mux.HandleFunc("/payments/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req paymentSubmitRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				ErrorCode: "invalid_request",
				Message:   "request body must be valid JSON",
			})
			return
		}

		if req.Currency == "" {
			req.Currency = "USD"
		}
		if req.FromAccount == "" || req.ToAccount == "" || req.Amount <= 0 {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				ErrorCode: "invalid_request",
				Message:   "from_account, to_account, and amount > 0 are required",
			})
			return
		}

		if req.Amount > maxPaymentAmount {
			limit := maxPaymentAmount
			writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
				ErrorCode: "amount_too_high",
				Message:   fmt.Sprintf("amount exceeds max payment limit %.2f", maxPaymentAmount),
				Limit:     &limit,
			})
			return
		}

		fromAccount, err := fetchAccount(client, accountBaseURL, req.FromAccount)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, errorResponse{
				ErrorCode: "account_lookup_failed",
				Message:   err.Error(),
			})
			return
		}

		toAccount, err := fetchAccount(client, accountBaseURL, req.ToAccount)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, errorResponse{
				ErrorCode: "account_lookup_failed",
				Message:   err.Error(),
			})
			return
		}

		if fromAccount.Balance < req.Amount {
			available := fromAccount.Balance
			writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
				ErrorCode:        "insufficient_balance",
				Message:          "from account balance is not enough for this payment",
				AvailableBalance: &available,
			})
			return
		}

		transactionID, err := createTransaction(client, transactionBaseURL, transactionCreateRequest{
			FromAccount: req.FromAccount,
			ToAccount:   req.ToAccount,
			Amount:      req.Amount,
			Currency:    req.Currency,
		})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, errorResponse{
				ErrorCode: "transaction_service_failed",
				Message:   err.Error(),
			})
			return
		}

		response := paymentSubmitResponse{
			PaymentID:        fmt.Sprintf("pay-%d", time.Now().UTC().UnixNano()),
			Status:           "submitted",
			TransactionID:    transactionID,
			FromAccount:      req.FromAccount,
			ToAccount:        req.ToAccount,
			FromAccountName:  fromAccount.Name,
			ToAccountName:    toAccount.Name,
			Amount:           req.Amount,
			Currency:         req.Currency,
			RemainingBalance: fromAccount.Balance - req.Amount,
		}
		writeJSON(w, http.StatusCreated, response)
	})

	server := &http.Server{
		Addr:         listen,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("payment sut listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}

func fetchAccount(client *http.Client, baseURL, accountID string) (account, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/accounts/" + url.PathEscape(accountID)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return account{}, err
	}

	resp, err := client.Do(req)
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

func createTransaction(client *http.Client, baseURL string, payload transactionCreateRequest) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/transactions"
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("transaction service returned %d: %s", resp.StatusCode, string(raw))
	}

	var txResp transactionCreateResponse
	if err := decodeJSON(resp.Body, &txResp); err != nil {
		return "", err
	}
	if txResp.TransactionID == "" {
		return "", errors.New("transaction service response missing transaction_id")
	}
	return txResp.TransactionID, nil
}

func decodeJSON(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
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
