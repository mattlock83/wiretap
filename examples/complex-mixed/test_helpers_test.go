package complexmixed

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/examples/complex-mixed/accountpb"
	"github.com/phrond/evermock/examples/complex-mixed/paymentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

type harness struct {
	baseURL string
	client  *http.Client
}

type account struct {
	ID       string
	Name     string
	Balance  float64
	Currency string
	Status   string
}

type transactionListResponse struct {
	Account      map[string]any   `json:"account"`
	Transactions []map[string]any `json:"transactions"`
}

type scenarioRuntime struct {
	harness            *harness
	proxyAddr          string
	grpcProxyAddr      string
	transactionBaseURL string
	paymentSchemas     []map[string]any
	defaultFromAccount account
	defaultToAccount   account
	highBalanceAccount account
	lowBalanceAccount  account
}

func newRuntime(t *testing.T) *scenarioRuntime {
	t.Helper()

	baseURL := getenv("PHROND_BASE_URL", "")
	if baseURL == "" {
		baseURL = "http://" + getenv("PHROND_HTTP_LISTEN", "127.0.0.1:18000")
	}
	proxyAddr := getenv("PHROND_PROXY_LISTEN", "127.0.0.1:18081")
	grpcProxyAddr := getenv("PHROND_GRPC_PROXY_LISTEN", "127.0.0.1:18082")
	grpcMockAddr := getenv("PHROND_GRPC_MOCK_LISTEN", "127.0.0.1:18083")
	paymentListen := getenv("PAYMENT_GRPC_SUT_LISTEN", "127.0.0.1:18120")
	transactionListen := getenv("TRANSACTION_SERVICE_LISTEN", "127.0.0.1:18121")

	h := &harness{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}

	transactionCmd, err := startTransactionService(transactionListen, grpcMockAddr)
	if err != nil {
		t.Fatalf("start transaction service: %v", err)
	}
	t.Cleanup(func() { killProcess(transactionCmd) })

	paymentCmd, err := startPaymentSUT(paymentListen, grpcMockAddr, transactionListen)
	if err != nil {
		t.Fatalf("start payment grpc sut: %v", err)
	}
	t.Cleanup(func() { killProcess(paymentCmd) })

	if err := waitForHTTP("http://" + transactionListen + "/healthz"); err != nil {
		t.Fatalf("wait for transaction service: %v", err)
	}
	if err := waitForPort(paymentListen, 40); err != nil {
		t.Fatalf("wait for payment sut: %v", err)
	}

	return &scenarioRuntime{
		harness:            h,
		proxyAddr:          proxyAddr,
		grpcProxyAddr:      grpcProxyAddr,
		transactionBaseURL: "http://" + transactionListen,
		paymentSchemas:     schemas(),
		defaultFromAccount: account{ID: "acct-100", Name: "Alice", Balance: 1000, Currency: "USD", Status: "active"},
		defaultToAccount:   account{ID: "acct-200", Name: "Bob", Balance: 500, Currency: "USD", Status: "active"},
		highBalanceAccount: account{ID: "acct-100", Name: "Alice", Balance: 20000, Currency: "USD", Status: "active"},
		lowBalanceAccount:  account{ID: "acct-100", Name: "Alice", Balance: 40, Currency: "USD", Status: "active"},
	}
}

func (r *scenarioRuntime) runCase(name string, run func(*scenarioRuntime) error) error {
	if _, err := r.harness.postJSON("/v1/testcases/start", map[string]any{
		"name":    name,
		"meta":    map[string]any{"suite": "complex-mixed"},
		"schemas": r.paymentSchemas,
	}); err != nil {
		return fmt.Errorf("start testcase: %w", err)
	}

	runErr := run(r)
	status := "pass"
	if runErr != nil {
		status = "fail"
	}

	finishResp, finishErr := r.harness.postJSON("/v1/testcases/finish", map[string]any{"status": status})
	if finishErr != nil {
		if runErr != nil {
			return fmt.Errorf("%v; finish testcase: %w", runErr, finishErr)
		}
		return fmt.Errorf("finish testcase: %w", finishErr)
	}

	validation, ok := finishResp["schema_validation"].(map[string]any)
	if !ok {
		return fmt.Errorf("schema_validation missing in finish response")
	}
	if got, _ := validation["status"].(string); got != "pass" {
		if runErr != nil {
			return fmt.Errorf("%v; schema validation failed: %v", runErr, validation)
		}
		return fmt.Errorf("schema validation failed: %v", validation)
	}

	if runErr != nil {
		return runErr
	}
	return nil
}

func (r *scenarioRuntime) registerAccounts(from account, to account) error {
	if _, err := r.harness.postJSON("/v1/grpc/stubs/clear", map[string]any{}); err != nil {
		return fmt.Errorf("clear grpc stubs: %w", err)
	}

	stubs := []struct {
		id      string
		account account
	}{
		{id: "account-" + from.ID, account: from},
		{id: "account-" + to.ID, account: to},
	}

	seen := map[string]bool{}
	for _, stub := range stubs {
		if seen[stub.account.ID] {
			continue
		}
		seen[stub.account.ID] = true

		reqBytes, err := proto.Marshal(accountpb.NewAccountRequest(stub.account.ID))
		if err != nil {
			return fmt.Errorf("marshal account request %s: %w", stub.account.ID, err)
		}
		respBytes, err := proto.Marshal(accountpb.NewAccountReply(
			stub.account.ID,
			stub.account.Name,
			stub.account.Balance,
			stub.account.Currency,
			stub.account.Status,
		))
		if err != nil {
			return fmt.Errorf("marshal account response %s: %w", stub.account.ID, err)
		}

		_, err = r.harness.postJSON("/v1/grpc/stubs", map[string]any{
			"id":   stub.id,
			"path": accountpb.AccountGetMethod,
			"match": map[string]any{
				"body_base64": base64.StdEncoding.EncodeToString(reqBytes),
			},
			"response": map[string]any{
				"status":      0,
				"body_base64": base64.StdEncoding.EncodeToString(respBytes),
			},
		})
		if err != nil {
			return fmt.Errorf("register grpc stub %s: %w", stub.id, err)
		}
	}
	return nil
}

func (r *scenarioRuntime) submit(fromAccount, toAccount string, amount float64, currency string) (*dynamicpb.Message, error) {
	conn, err := dialGRPC(r.grpcProxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial grpc proxy: %w", err)
	}
	defer conn.Close()

	client := paymentpb.NewPaymentServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reply, err := client.Submit(ctx, paymentpb.NewPaymentSubmitRequest(fromAccount, toAccount, amount, currency))
	if err != nil {
		return nil, fmt.Errorf("submit grpc call: %w", err)
	}
	return reply, nil
}

func (r *scenarioRuntime) paymentString(msg *dynamicpb.Message, field string) string {
	return paymentpb.GetString(msg, field)
}

func (r *scenarioRuntime) listTransactions(accountID string) (transactionListResponse, error) {
	listResp, err := requestViaProxy(
		r.proxyAddr,
		r.transactionBaseURL+"/transactions?account_id="+url.QueryEscape(accountID),
		http.MethodGet,
		nil,
		map[string]string{"Accept": "application/json"},
	)
	if err != nil {
		return transactionListResponse{}, fmt.Errorf("list transactions via proxy: %w", err)
	}
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		return transactionListResponse{}, fmt.Errorf("unexpected transaction list status %d: %s", listResp.StatusCode, string(body))
	}

	var parsed transactionListResponse
	if err := decodeJSON(listResp.Body, &parsed); err != nil {
		return transactionListResponse{}, fmt.Errorf("decode transaction list response: %w", err)
	}
	return parsed, nil
}

func (h *harness) postJSON(path string, payload any) (map[string]any, error) {
	body, _ := json.Marshal(payload)
	resp, err := h.client.Post(h.baseURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed: %d", resp.StatusCode)
	}
	return parsed, nil
}

func schemas() []map[string]any {
	paymentProtoSource := map[string]any{
		"type": "file",
		"path": "examples/complex-mixed/paymentpb/payment.proto",
	}
	accountProtoSource := map[string]any{
		"type": "file",
		"path": "examples/complex-mixed/accountpb/account.proto",
	}

	return []map[string]any{
		{
			"id":     "complex-mixed-payment-sut",
			"role":   "sut",
			"type":   "protobuf",
			"source": paymentProtoSource,
		},
		{
			"id":     "complex-mixed-fake-account",
			"role":   "fake",
			"type":   "protobuf",
			"source": accountProtoSource,
		},
		{
			"id":   "complex-mixed-transaction-rest",
			"role": "sut",
			"type": "openapi",
			"source": map[string]any{
				"type": "file",
				"path": "examples/complex-mixed/schemas/transaction-openapi.yaml",
			},
		},
	}
}

func startPaymentSUT(listen, accountAddr, transactionListen string) (*exec.Cmd, error) {
	binary := getenv("PAYMENT_GRPC_SUT_BINARY", "")
	var cmd *exec.Cmd
	if binary != "" {
		cmd = exec.Command(binary)
	} else {
		cmd = exec.Command("go", "run", "./examples/demo-apps/complex-mixed/payment-sut")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(
		os.Environ(),
		"PAYMENT_GRPC_SUT_LISTEN="+listen,
		"ACCOUNT_GRPC_ADDR="+accountAddr,
		"TRANSACTION_BASE_URL=http://"+transactionListen,
	)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func startTransactionService(listen, accountAddr string) (*exec.Cmd, error) {
	binary := getenv("PAYMENT_GRPC_TRANSACTION_BINARY", "")
	var cmd *exec.Cmd
	if binary != "" {
		cmd = exec.Command(binary)
	} else {
		cmd = exec.Command("go", "run", "./examples/demo-apps/complex-mixed/transaction-service")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(
		os.Environ(),
		"TRANSACTION_SERVICE_LISTEN="+listen,
		"ACCOUNT_GRPC_ADDR="+accountAddr,
	)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

func requestViaProxy(proxyAddr, targetURL, method string, body []byte, headers map[string]string) (*http.Response, error) {
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}

	req, err := http.NewRequest(method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	return client.Do(req)
}

func waitForHTTP(url string) error {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	for i := 0; i < 40; i++ {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func waitForPort(addr string, attempts int) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" {
		host = "127.0.0.1"
	}
	for i := 0; i < attempts; i++ {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

func dialGRPC(addr string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
