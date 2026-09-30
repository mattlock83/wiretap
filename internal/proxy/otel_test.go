package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tlsmgr "github.com/phrond/wiretap/internal/tls"
	"github.com/phrond/wiretap/internal/tracecontext"
)

func TestW3CContextAcrossReverseForwardAndMITM(t *testing.T) {
	parent := "00-11111111111111111111111111111111-2222222222222222-00"
	for _, mode := range []string{"reverse", "forward", "mitm", "mitm-dependency"} {
		t.Run(mode, func(t *testing.T) {
			observed := make(chan http.Header, 1)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { observed <- r.Header.Clone(); io.WriteString(w, "ok") })
			var upstream *httptest.Server
			if mode == "mitm" || mode == "mitm-dependency" {
				upstream = httptest.NewTLSServer(handler)
			} else {
				upstream = httptest.NewServer(handler)
			}
			defer upstream.Close()
			recorder := &recordingRecorder{exchanges: make(chan HTTPExchange, 1)}
			server := NewServer(Config{Mode: ModeMITM, TLSManager: tlsmgr.NewManager(t.TempDir()), UpstreamTLSConfig: &tls.Config{InsecureSkipVerify: true}, Recorder: recorder, ReverseTarget: upstream.URL})
			root, _ := (tracecontext.Context{Traceparent: parent}).Validate()
			server.SetAdmissionResolver(func(r *http.Request, session string) (*Admission, error) {
				return &Admission{SessionID: "owner", CaseID: "case", TestCorrelation: "explicit_session", Root: root}, nil
			})
			if mode == "mitm-dependency" {
				server.SetDependencyResolver(func(r *http.Request, session string) (DependencyRoute, bool, error) {
					return DependencyRoute{SessionID: session, AssetID: "inventory", UpstreamURL: upstream.URL, Path: "/items", RawPath: "/items"}, true, nil
				})
			}
			info, err := server.Start(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer server.Stop(t.Context())
			proxyURL, _ := ProxyURL(info.ListenAddr)
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			defer transport.CloseIdleConnections()
			ca, _ := server.CACertPEM()
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(ca)
			transport.TLSClientConfig = &tls.Config{RootCAs: pool}
			target := upstream.URL
			if mode == "reverse" {
				target = proxyURL.String()
				transport.Proxy = nil
			}
			req, _ := http.NewRequest("GET", target, nil)
			req.Header.Set("traceparent", parent)
			req.Header.Set("tracestate", "vendor=value")
			req.Header.Set("X-Wiretap-Session-Id", "owner")
			res, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			headers := <-observed
			if headers.Get("traceparent") != parent || headers.Get("tracestate") != "vendor=value" || headers.Get("X-Wiretap-Session-Id") != "" {
				t.Fatalf("context changed: %v", headers)
			}
			exchange := <-recorder.exchanges
			if exchange.TraceID != "11111111111111111111111111111111" || exchange.RequestSpanID != "2222222222222222" || exchange.TraceContextSource != "received" || exchange.CaseID != "case" {
				t.Fatalf("wrong evidence: %#v", exchange)
			}
			if mode == "mitm-dependency" && (exchange.Channel != "sut_to_dependency" || exchange.AssetID != "inventory") {
				t.Fatal("MITM bypassed dependency resolver")
			}
		})
	}
}
