package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/phrond/evermock/examples/grpc/examplepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

type harness struct {
	baseURL string
	client  *http.Client
}

func main() {
	baseURL := getenv("PHROND_BASE_URL", "")
	if baseURL == "" {
		listen := getenv("PHROND_HTTP_LISTEN", "127.0.0.1:18000")
		baseURL = "http://" + listen
	}

	h := &harness{baseURL: baseURL, client: &http.Client{Timeout: 10 * time.Second}}

	if _, err := h.postJSON("/v1/testcases/start", map[string]any{
		"name": "grpc-basic",
		"meta": map[string]any{"suite": "examples"},
		"schemas": []map[string]any{
			{
				"id":   "grpc-sut",
				"role": "sut",
				"type": "protobuf",
				"source": map[string]any{
					"type": "file",
					"path": "examples/grpc/examplepb/example.proto",
				},
			},
			{
				"id":   "grpc-fake",
				"role": "fake",
				"type": "protobuf",
				"source": map[string]any{
					"type": "file",
					"path": "examples/grpc/examplepb/example.proto",
				},
			},
		},
	}); err != nil {
		log.Fatalf("start testcase: %v", err)
	}

	sutListen := getenv("SUT_LISTEN", "127.0.0.1:18095")

	proxyResp, err := h.postJSON("/v1/grpc/proxy/start", map[string]any{
		"target":               sutListen,
		"insecure_skip_verify": true,
	})
	if err != nil {
		log.Fatalf("start grpc proxy: %v", err)
	}

	mockResp, err := h.postJSON("/v1/grpc/mock/start", map[string]any{})
	if err != nil {
		log.Fatalf("start grpc mock: %v", err)
	}

	proxyListen := getString(proxyResp, "listen_addr")
	if proxyListen == "" {
		log.Fatalf("proxy listen address missing")
	}
	mockAddr := getString(mockResp, "addr")
	if mockAddr == "" {
		log.Fatalf("mock addr missing")
	}

	sutCmd, err := startSUT(sutListen, mockAddr)
	if err != nil {
		log.Fatalf("start SUT: %v", err)
	}
	defer func() {
		if sutCmd.Process != nil {
			_ = sutCmd.Process.Kill()
		}
	}()

	if err := waitForPort(sutListen, 40); err != nil {
		log.Fatalf("SUT failed to start: %v", err)
	}

	alphaPayload := examplepb.NewDependencyReply("alpha-ok")
	alphaBytes, err := proto.Marshal(alphaPayload)
	if err != nil {
		log.Fatalf("marshal alpha reply: %v", err)
	}

	betaPayload := examplepb.NewDependencyReply("beta-ok")
	betaBytes, err := proto.Marshal(betaPayload)
	if err != nil {
		log.Fatalf("marshal beta reply: %v", err)
	}

	_, err = h.postJSON("/v1/grpc/stubs", map[string]any{
		"id":   "alpha",
		"path": examplepb.DependencyAlphaMethod,
		"response": map[string]any{
			"status":      0,
			"body_base64": base64.StdEncoding.EncodeToString(alphaBytes),
		},
	})
	if err != nil {
		log.Fatalf("register alpha stub: %v", err)
	}

	_, err = h.postJSON("/v1/grpc/stubs", map[string]any{
		"id":   "beta",
		"path": examplepb.DependencyBetaMethod,
		"response": map[string]any{
			"status":      0,
			"body_base64": base64.StdEncoding.EncodeToString(betaBytes),
		},
	})
	if err != nil {
		log.Fatalf("register beta stub: %v", err)
	}

	result, err := callProcess(proxyListen, "123", "process")
	if err != nil {
		log.Fatalf("call process: %v", err)
	}
	fmt.Printf("gRPC response: %s\n", result)

	finishResp, err := h.postJSON("/v1/testcases/finish", map[string]any{"status": "pass"})
	if err != nil {
		log.Fatalf("finish testcase: %v", err)
	}
	fmt.Printf("artifact: %s\n", getString(finishResp, "path"))
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
	req := examplepb.NewProcessRequest(id, action)
	resp, err := client.Process(ctx, req)
	if err != nil {
		return "", err
	}

	return examplepb.GetString(resp, "result"), nil
}

func (h *harness) postJSON(path string, payload any) (map[string]any, error) {
	body, _ := json.Marshal(payload)
	url := h.baseURL + path
	resp, err := h.client.Post(url, "application/json", bytes.NewReader(body))
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

func getString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	if value, ok := data[key]; ok {
		if s, ok := value.(string); ok {
			return s
		}
	}
	return ""
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func startSUT(listen, dependency string) (*exec.Cmd, error) {
	binary := getenv("SUT_BINARY", "")
	var cmd *exec.Cmd
	if binary != "" {
		cmd = exec.Command(binary)
	} else {
		cmd = exec.Command("go", "run", "./examples/grpc/sut")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "SUT_LISTEN="+listen, "DEPENDENCY_ADDR="+dependency)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
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
