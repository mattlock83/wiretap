package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phrond/evermock/examples/paymentgrpc/accountpb"
	"github.com/phrond/evermock/examples/paymentgrpc/paymentpb"
	tlsmgr "github.com/phrond/evermock/internal/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/dynamicpb"
)

const maxPaymentAmount = 5000.0

type account struct {
	ID       string
	Name     string
	Balance  float64
	Currency string
	Status   string
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

type serviceServer struct {
	accountAddr        string
	transactionBaseURL string
	httpClient         *http.Client
}

func (s *serviceServer) Submit(ctx context.Context, req *dynamicpb.Message) (*dynamicpb.Message, error) {
	fromAccount := paymentpb.GetString(req, "from_account")
	toAccount := paymentpb.GetString(req, "to_account")
	amount := paymentpb.GetFloat64(req, "amount")
	currency := paymentpb.GetString(req, "currency")
	if currency == "" {
		currency = "USD"
	}

	if fromAccount == "" || toAccount == "" || amount <= 0 {
		return paymentpb.NewPaymentSubmitReply(
			"rejected",
			"invalid_request",
			"from_account, to_account, and amount > 0 are required",
			"",
			"",
			"",
			"",
			0,
		), nil
	}

	if amount > maxPaymentAmount {
		return paymentpb.NewPaymentSubmitReply(
			"rejected",
			"amount_too_high",
			fmt.Sprintf("amount exceeds max payment limit %.2f", maxPaymentAmount),
			"",
			"",
			"",
			"",
			0,
		), nil
	}

	from, err := s.fetchAccount(ctx, fromAccount)
	if err != nil {
		return paymentpb.NewPaymentSubmitReply(
			"error",
			"account_lookup_failed",
			err.Error(),
			"",
			"",
			"",
			"",
			0,
		), nil
	}

	to, err := s.fetchAccount(ctx, toAccount)
	if err != nil {
		return paymentpb.NewPaymentSubmitReply(
			"error",
			"account_lookup_failed",
			err.Error(),
			"",
			"",
			"",
			"",
			0,
		), nil
	}

	if from.Balance < amount {
		return paymentpb.NewPaymentSubmitReply(
			"rejected",
			"insufficient_balance",
			"from account balance is not enough for this payment",
			"",
			"",
			"",
			"",
			0,
		), nil
	}

	transactionID, err := s.createTransaction(transactionCreateRequest{
		FromAccount: fromAccount,
		ToAccount:   toAccount,
		Amount:      amount,
		Currency:    currency,
	})
	if err != nil {
		return paymentpb.NewPaymentSubmitReply(
			"error",
			"transaction_service_failed",
			err.Error(),
			"",
			"",
			"",
			"",
			0,
		), nil
	}

	return paymentpb.NewPaymentSubmitReply(
		"submitted",
		"",
		"",
		fmt.Sprintf("pay-%d", time.Now().UTC().UnixNano()),
		transactionID,
		from.Name,
		to.Name,
		from.Balance-amount,
	), nil
}

func (s *serviceServer) fetchAccount(ctx context.Context, accountID string) (account, error) {
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
		return account{}, errors.New("account reply missing id")
	}

	return account{
		ID:       accountpb.GetString(reply, "id"),
		Name:     accountpb.GetString(reply, "name"),
		Balance:  accountpb.GetFloat64(reply, "balance"),
		Currency: accountpb.GetString(reply, "currency"),
		Status:   accountpb.GetString(reply, "status"),
	}, nil
}

func (s *serviceServer) createTransaction(payload transactionCreateRequest) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	endpoint := strings.TrimRight(s.transactionBaseURL, "/") + "/transactions"
	resp, err := s.httpClient.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("transaction service returned %d: %s", resp.StatusCode, string(raw))
	}

	var parsed transactionCreateResponse
	if err := decodeJSON(resp.Body, &parsed); err != nil {
		return "", err
	}
	if parsed.TransactionID == "" {
		return "", errors.New("transaction service response missing transaction_id")
	}
	return parsed.TransactionID, nil
}

func main() {
	listen := getenv("PAYMENT_GRPC_SUT_LISTEN", "127.0.0.1:18120")
	accountAddr := getenv("ACCOUNT_GRPC_ADDR", "127.0.0.1:19191")
	transactionBaseURL := getenv("TRANSACTION_BASE_URL", "http://127.0.0.1:18121")

	mgr := tlsmgr.NewManager(filepath.Join(os.TempDir(), "phrond", "payment-grpc-sut"))
	cert, err := mgr.GetOrCreateCert(hostFromAddr(listen))
	if err != nil {
		log.Fatalf("cert error: %v", err)
	}

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}

	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	paymentpb.RegisterPaymentServiceServer(server, &serviceServer{
		accountAddr:        accountAddr,
		transactionBaseURL: transactionBaseURL,
		httpClient:         &http.Client{Timeout: 5 * time.Second},
	})

	log.Printf("payment grpc sut listening on %s", ln.Addr().String())
	if err := server.Serve(ln); err != nil {
		log.Fatalf("grpc serve error: %v", err)
	}
}

func dialGRPC(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	return grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(creds), grpc.WithBlock())
}

func hostFromAddr(addr string) string {
	if addr == "" {
		return "127.0.0.1"
	}
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1"
	}
	if host == "" || host == "0.0.0.0" {
		return "127.0.0.1"
	}
	return host
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
