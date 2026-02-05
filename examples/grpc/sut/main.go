package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phrond/evermock/internal/grpcwire"
	tlsmgr "github.com/phrond/evermock/internal/tls"
)

func main() {
	listen := getenv("SUT_LISTEN", "127.0.0.1:18095")
	dependency := getenv("DEPENDENCY_ADDR", "127.0.0.1:19191")

	mgr := tlsmgr.NewManager(filepath.Join(os.TempDir(), "phrond", "grpc-sut"))
	cert, err := mgr.GetOrCreateCert(hostFromAddr(listen))
	if err != nil {
		log.Fatalf("cert error: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}
	listener := tls.NewListener(ln, tlsConfig)

	client := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Second,
	}

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		if r.Method != http.MethodPost {
			writeGRPCError(w, http.StatusMethodNotAllowed, 12, "method not allowed")
			return
		}
		if !strings.Contains(r.Header.Get("Content-Type"), "application/grpc") {
			writeGRPCError(w, http.StatusUnsupportedMediaType, 12, "invalid content-type")
			return
		}
		if r.URL.Path != "/example.Service/Process" {
			writeGRPCError(w, http.StatusNotFound, 12, "unknown method")
			return
		}

		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		payload := unwrapPayload(bodyBytes)

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		alpha, _, _, err := callDependency(ctx, client, dependency, "/example.Dependency/Alpha", payload)
		if err != nil {
			writeGRPCError(w, http.StatusBadGateway, 14, "alpha failed")
			return
		}
		beta, _, _, err := callDependency(ctx, client, dependency, "/example.Dependency/Beta", payload)
		if err != nil {
			writeGRPCError(w, http.StatusBadGateway, 14, "beta failed")
			return
		}

		result := []byte(fmt.Sprintf("alpha:%s|beta:%s", string(alpha), string(beta)))
		frame := grpcwire.EncodeFrame(result)

		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Add("Trailer", "Grpc-Status")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(frame)
		w.Header().Set("Grpc-Status", "0")
	})}

	log.Printf("grpc SUT listening on %s", listener.Addr().String())
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func callDependency(ctx context.Context, client *http.Client, target string, path string, payload []byte) ([]byte, int, string, error) {
	url := target
	if !strings.Contains(target, "://") {
		url = "https://" + target
	}
	url = url + path

	body := grpcwire.EncodeFrame(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	status, message := grpcStatus(resp)
	payloadOut := unwrapPayload(respBody)
	return payloadOut, status, message, nil
}

func grpcStatus(resp *http.Response) (int, string) {
	statusVal := headerValue(resp.Trailer, "grpc-status")
	message := headerValue(resp.Trailer, "grpc-message")
	if statusVal == "" {
		statusVal = headerValue(resp.Header, "grpc-status")
		if message == "" {
			message = headerValue(resp.Header, "grpc-message")
		}
	}
	if statusVal == "" {
		return 0, message
	}
	var status int
	_, _ = fmt.Sscanf(statusVal, "%d", &status)
	return status, message
}

func headerValue(headers map[string][]string, key string) string {
	for k, values := range headers {
		if strings.EqualFold(k, key) {
			if len(values) > 0 {
				return values[0]
			}
			return ""
		}
	}
	return ""
}

func unwrapPayload(body []byte) []byte {
	frames, err := grpcwire.DecodeFrames(body)
	if err != nil {
		return body
	}
	payload := grpcwire.JoinMessages(frames)
	if len(payload) == 0 {
		return body
	}
	return payload
}

func writeGRPCError(w http.ResponseWriter, httpStatus int, grpcStatus int, message string) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Add("Trailer", "Grpc-Status")
	w.Header().Add("Trailer", "Grpc-Message")
	w.WriteHeader(httpStatus)
	w.Header().Set("Grpc-Status", fmt.Sprintf("%d", grpcStatus))
	w.Header().Set("Grpc-Message", message)
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

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
