package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPTransportReusesConnectionsAndClosesPoolsOnStop(t *testing.T) {
	for _, mode := range []string{"reverse-http", "forward-http", "reverse-https"} {
		t.Run(mode, func(t *testing.T) {
			var opened atomic.Int32
			closed := make(chan struct{}, 10)
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "complete response")
			}))
			upstream.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					opened.Add(1)
				}
				if state == http.StateClosed {
					closed <- struct{}{}
				}
			}
			if mode == "reverse-https" {
				upstream.StartTLS()
			} else {
				upstream.Start()
			}
			defer upstream.Close()
			cfg := Config{ReverseTarget: upstream.URL}
			if mode == "reverse-https" {
				cfg.UpstreamTLSConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig
			}
			server := NewServer(cfg)
			defer server.Stop(t.Context())
			for run := 0; run < 2; run++ {
				info, err := server.Start(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				clientTransport := &http.Transport{}
				requestURL := "http://" + info.ListenAddr
				if mode == "forward-http" {
					proxyURL, _ := ProxyURL(info.ListenAddr)
					clientTransport.Proxy = http.ProxyURL(proxyURL)
					requestURL = upstream.URL
				}
				client := &http.Client{Transport: clientTransport, Timeout: 5 * time.Second}
				for request := 0; request < 6; request++ {
					response, err := client.Get(requestURL)
					if err != nil {
						t.Fatal(err)
					}
					_, readErr := io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
					if readErr != nil || response.StatusCode != http.StatusOK {
						t.Fatalf("response: status %d, read error %v", response.StatusCode, readErr)
					}
				}
				clientTransport.CloseIdleConnections()
				if got := opened.Load(); got != int32(run+1) {
					t.Fatalf("six requests should reuse one upstream connection per run; opened %d", got)
				}
				if err := server.Stop(t.Context()); err != nil {
					t.Fatal(err)
				}
				select {
				case <-closed:
				case <-time.After(2 * time.Second):
					t.Fatal("Stop left the upstream keepalive connection open")
				}
			}
		})
	}
}
