package basicgrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/examples/basic-grpc/examplepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type harness struct {
	baseURL string
	client  *http.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	baseURL := getenv("PHROND_BASE_URL", "")
	if baseURL == "" {
		baseURL = "http://" + getenv("PHROND_HTTP_LISTEN", "127.0.0.1:18000")
	}
	return &harness{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
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

func startSUT(listen, dependencyAddr string) (*exec.Cmd, error) {
	binary := getenv("SUT_BINARY", "")
	var cmd *exec.Cmd
	if binary != "" {
		cmd = exec.Command(binary)
	} else {
		cmd = exec.Command("go", "run", "./examples/demo-apps/basic-grpc/sut")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "SUT_LISTEN="+listen, "DEPENDENCY_ADDR="+dependencyAddr)
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

func callProcess(proxyListen, id, action string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	conn, err := grpc.DialContext(ctx, proxyListen, grpc.WithTransportCredentials(creds))
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := examplepb.NewServiceClient(conn)
	resp, err := client.Process(ctx, examplepb.NewProcessRequest(id, action))
	if err != nil {
		return "", err
	}
	return examplepb.GetString(resp, "result"), nil
}

func grpcSchemas() []map[string]any {
	source := map[string]any{
		"type": "file",
		"path": "examples/basic-grpc/examplepb/example.proto",
	}
	return []map[string]any{
		{
			"id":     "basic-grpc-sut",
			"role":   "sut",
			"type":   "protobuf",
			"source": source,
		},
		{
			"id":     "basic-grpc-fake",
			"role":   "fake",
			"type":   "protobuf",
			"source": source,
		},
	}
}

func assertSchemaPass(t *testing.T, finishResp map[string]any) {
	t.Helper()
	validation, ok := finishResp["schema_validation"].(map[string]any)
	if !ok {
		t.Fatalf("schema_validation missing in finish response")
	}
	status, _ := validation["status"].(string)
	if status != "pass" {
		t.Fatalf("schema validation failed: %#v", validation)
	}
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
