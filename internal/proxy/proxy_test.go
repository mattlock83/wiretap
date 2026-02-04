package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tlsmgr "github.com/phrond/evermock/internal/tls"
)

func TestHTTPPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	proxy := NewServer(Config{Mode: ModePassthrough})
	info, err := proxy.Start(t.Context())
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Stop(t.Context())

	proxyURL, err := ProxyURL(info.ListenAddr)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("client get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("unexpected body: %s", string(body))
	}
}

func TestHTTPSPassthroughConnect(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secure"))
	}))
	defer upstream.Close()

	proxy := NewServer(Config{Mode: ModePassthrough})
	info, err := proxy.Start(t.Context())
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Stop(t.Context())

	proxyURL, err := ProxyURL(info.ListenAddr)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("client get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secure" {
		t.Fatalf("unexpected body: %s", string(body))
	}
}

func TestHTTPSMITM(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mitm"))
	}))
	defer upstream.Close()

	upstreamTLS := upstream.Client().Transport.(*http.Transport).TLSClientConfig

	mgr := tlsmgr.NewManager(t.TempDir())
	proxy := NewServer(Config{
		Mode:              ModeMITM,
		TLSManager:        mgr,
		UpstreamTLSConfig: upstreamTLS,
	})
	info, err := proxy.Start(t.Context())
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Stop(t.Context())

	proxyURL, err := ProxyURL(info.ListenAddr)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}

	caPEM, err := mgr.CACertPEM()
	if err != nil {
		t.Fatalf("load CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatalf("failed to append CA cert")
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("client get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "mitm" {
		t.Fatalf("unexpected body: %s", string(body))
	}
}
