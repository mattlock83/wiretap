package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/core"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/project"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

const healthService = "grpc.health.v1.Health"

func healthServer(t *testing.T, service healthpb.HealthServer) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, service)
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	return "http://" + listener.Addr().String()
}

func healthCall(t *testing.T, origin, service string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(strings.TrimPrefix(origin, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	res, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: service})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health = %v", res.Status)
	}
}

func TestNativeGRPCProviderConsumerAndMixedBoundaries(t *testing.T) {
	for _, mode := range []string{"provider", "consumer", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			control, instance := dependencyCaptureRuntime(t)
			optional := false
			var sutURL, depURL string
			switch mode {
			case "provider":
				server := health.NewServer()
				server.SetServingStatus("provider", healthpb.HealthCheckResponse_SERVING)
				sutURL = healthServer(t, server)
				instance.project = &project.Config{SUT: project.API{ID: "health", URL: sutURL, Protocol: "grpc", Kind: "service", Services: []string{healthService}}}
			case "consumer":
				server := health.NewServer()
				server.SetServingStatus("consumer", healthpb.HealthCheckResponse_SERVING)
				depURL = healthServer(t, server)
				var captureURL string
				sut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					defer cancel()
					conn, err := grpc.NewClient(strings.TrimPrefix(captureURL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
					if err != nil {
						http.Error(w, err.Error(), 502)
						return
					}
					defer conn.Close()
					res, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "consumer"})
					if err != nil {
						http.Error(w, err.Error(), 502)
						return
					}
					fmt.Fprint(w, res.Status.String())
				}))
				defer sut.Close()
				sutURL = sut.URL
				instance.project = &project.Config{SUT: project.API{ID: "http-sut", URL: sutURL, SchemaRequired: &optional}, Dependencies: map[string]project.API{"health-mock": {URL: depURL, Protocol: "grpc", Kind: "mock", Services: []string{healthService}}}}
				defer func() { _ = captureURL }()
				start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
				captureURL = start["dependencies"].(map[string]any)["health-mock"].(string)
				runGRPCTestCase(t, control.URL, start, mode)
				return
			case "mixed":
				mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true}`) }))
				defer mock.Close()
				depURL = mock.URL
				var captureURL string
				sutURL = healthServer(t, &mixedHealthServer{dependencyURL: &captureURL})
				instance.project = &project.Config{SUT: project.API{ID: "grpc-sut", URL: sutURL, Protocol: "grpc", Kind: "service", Services: []string{healthService}}, Dependencies: map[string]project.API{"http-mock": {URL: depURL, Kind: "mock", SchemaRequired: &optional}}}
				start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
				captureURL = start["sut_url"].(string) + "/__wiretap/dep/http-mock"
				runGRPCTestCase(t, control.URL, start, mode)
				return
			}
			start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
			if mode == "provider" {
				badSetup, err := http.Post(control.URL+"/v1/setupTest", "application/json", strings.NewReader(`{"schema_required":false}`))
				if err != nil {
					t.Fatal(err)
				}
				badSetup.Body.Close()
				if badSetup.StatusCode != http.StatusBadRequest {
					t.Fatalf("gRPC schema opt-out was admitted: HTTP %d", badSetup.StatusCode)
				}
				for _, codec := range []string{"application/grpc-web+proto", "application/proto", "application/connect+proto", "application/json"} {
					response, err := http.Post(start["sut_url"].(string)+"/"+healthService+"/Check", codec, strings.NewReader(""))
					if err != nil {
						t.Fatal(err)
					}
					response.Body.Close()
					if response.StatusCode != http.StatusUnsupportedMediaType {
						t.Fatalf("unsupported codec %s was accepted: HTTP %d", codec, response.StatusCode)
					}
				}
			}
			runGRPCTestCase(t, control.URL, start, mode)
		})
	}
}

func TestNativeGRPCWithDescriptorSetAndNoReflection(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	health := health.NewServer()
	health.SetServingStatus("provider", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, health)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}}
	data, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "health.protoset")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	instance.project = &project.Config{SUT: project.API{ID: "health", URL: "http://" + listener.Addr().String(), Protocol: "grpc", Services: []string{healthService}, GRPCSchema: &project.GRPCSchema{Source: "descriptor_set", Path: path}}}
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	runGRPCTestCase(t, control.URL, start, "provider")
}

func TestConnectClientInNativeGRPCMode(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	server := health.NewServer()
	server.SetServingStatus("provider", healthpb.HealthCheckResponse_SERVING)
	instance.project = &project.Config{SUT: project.API{ID: "health", URL: healthServer(t, server), Protocol: "grpc", Services: []string{healthService}}}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "Connect client with native gRPC"})
	transport := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	defer transport.CloseIdleConnections()
	client := connect.NewClient[healthpb.HealthCheckRequest, healthpb.HealthCheckResponse](&http.Client{Transport: transport}, setup["sut_url"].(string)+"/"+healthService+"/Check", connect.WithGRPC())
	response, err := client.CallUnary(t.Context(), connect.NewRequest(&healthpb.HealthCheckRequest{Service: "provider"}))
	if err != nil || response.Msg.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("Connect gRPC call: response=%#v err=%v", response, err)
	}
	finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	art := dependencyArtifact(t, finished)
	if len(art.Traffic) != 1 || art.Traffic[0].Protocol != "grpc" || art.Traffic[0].Response.GRPC.StatusCode != 0 {
		t.Fatalf("Connect native gRPC evidence: %#v", art.Traffic)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}

func TestNativeGRPCUpstreamTLSAndReflection(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	server := grpc.NewServer()
	health := health.NewServer()
	health.SetServingStatus("provider", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, health)
	reflection.Register(server)
	upstream := httptest.NewUnstartedServer(server)
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	caPath := filepath.Join(t.TempDir(), "upstream-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	instance.project = &project.Config{SUT: project.API{ID: "health", URL: upstream.URL, Protocol: "grpc", Services: []string{healthService}, UpstreamTLS: project.TLSConfig{CAFile: caPath}}}
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	runGRPCTestCase(t, control.URL, start, "provider")
}

func TestNativeGRPCCaptureListenerTLS(t *testing.T) {
	certificateServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := certificateServer.TLS.Certificates[0]
	certificateServer.Close()
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(t.TempDir(), "capture.crt")
	keyPath := filepath.Join(t.TempDir(), "capture.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	server := health.NewServer()
	server.SetServingStatus("tls", healthpb.HealthCheckResponse_SERVING)
	config := core.DefaultConfig()
	config.Proxy.ListenAddr = "127.0.0.1:0"
	config.Artifact.OutputDir = t.TempDir()
	instance, err := New(Deps{Config: config, Project: &project.Config{SUT: project.API{ID: "health", URL: healthServer(t, server), Protocol: "grpc", Services: []string{healthService}, CaptureTLS: project.TLSConfig{CertFile: certPath, KeyFile: keyPath}}}})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(instance.routes())
	defer control.Close()
	defer func() { _ = instance.proxyComponent.Stop(t.Context()) }()
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "TLS gRPC capture"})
	if !strings.HasPrefix(setup["sut_url"].(string), "https://") {
		t.Fatalf("capture endpoint is not TLS: %#v", setup)
	}
	pool := x509.NewCertPool()
	pool.AddCert(mustParseCertificate(t, certificate.Certificate[0]))
	conn, err := grpc.NewClient(strings.TrimPrefix(setup["sut_url"].(string), "https://"), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	response, err := healthpb.NewHealthClient(conn).Check(t.Context(), &healthpb.HealthCheckRequest{Service: "tls"})
	if err != nil || response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("TLS capture call: response=%#v err=%v", response, err)
	}
	finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	art := dependencyArtifact(t, finished)
	if len(art.Traffic) != 1 || art.Traffic[0].Protocol != "grpc" || art.Traffic[0].Response.GRPC.StatusCode != 0 {
		t.Fatalf("TLS capture evidence: %#v", art.Traffic)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}

func mustParseCertificate(t *testing.T, raw []byte) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

type mixedHealthServer struct {
	healthpb.UnimplementedHealthServer
	dependencyURL *string
}

func (m *mixedHealthServer) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", *m.dependencyURL+"/health", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func runGRPCTestCase(t *testing.T, controlURL string, start map[string]any, mode string) {
	t.Helper()
	setup := postDependencyJSON(t, controlURL, "/v1/setupTest", map[string]any{"name": mode + " gRPC boundary"})
	if mode == "consumer" {
		res, err := http.Get(setup["sut_url"].(string) + "/check")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("HTTP status %d", res.StatusCode)
		}
	} else {
		healthCall(t, setup["sut_url"].(string), mode)
	}
	finished := postDependencyJSON(t, controlURL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	art := dependencyArtifact(t, finished)
	if art.TestCase.Status != "pass" || art.SchemaValidation == nil || art.SchemaValidation.Status != "pass" && mode == "provider" {
		t.Fatalf("test/validation: status=%s validation=%#v", art.TestCase.Status, art.SchemaValidation)
	}
	grpcFlows := 0
	for _, flow := range art.Traffic {
		if flow.Protocol != "grpc" {
			continue
		}
		grpcFlows++
		if flow.Request.GRPC == nil || flow.Response.GRPC == nil || len(flow.Request.GRPC.Messages) != 1 || flow.Response.GRPC.StatusCode != 0 || len(flow.Response.GRPC.Messages) != 1 {
			t.Fatalf("bad gRPC evidence: %#v", flow)
		}
	}
	if grpcFlows != 1 {
		t.Fatalf("got %d gRPC flows in %d total", grpcFlows, len(art.Traffic))
	}
	if mode != "provider" && len(art.Traffic) != 2 {
		t.Fatalf("expected mixed traffic: %#v", art.Traffic)
	}
	root := finished["bundle_path"].(string)
	run, captures, err := bundle.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || len(run.Schemas) == 0 {
		t.Fatalf("bundle missing contracts: %#v", run)
	}
	for _, schema := range run.Schemas {
		if schema.Type == "protobuf" && (schema.Source.Type != "file" || schema.Source.Path == "" || schema.Source.SHA256 == "" || len(schema.Services) == 0) {
			t.Fatalf("protobuf contract was not frozen: %#v", schema)
		}
	}
	for _, flow := range captures[0].Traffic {
		if flow.Protocol != "grpc" {
			continue
		}
		if flow.Response.GRPC.StatusCode != 0 || flow.Request.GRPC.Messages[0].Body == nil || flow.Request.GRPC.Messages[0].Wire == nil || flow.Response.GRPC.Messages[0].Body == nil || flow.Response.GRPC.Messages[0].Wire == nil {
			t.Fatalf("gRPC JSON/wire attachments not restored: %#v", flow)
		}
	}
	canonical, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(run.Attempts[0].Path)))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Capture struct {
			Traffic []struct {
				Protocol string         `json:"protocol"`
				Response map[string]any `json:"response"`
			} `json:"traffic"`
		} `json:"capture"`
		Bodies []struct {
			FlowID       string `json:"flow_id"`
			MessageIndex *int   `json:"message_index"`
		} `json:"bodies"`
	}
	if err := json.Unmarshal(canonical, &stored); err != nil {
		t.Fatal(err)
	}
	grpcBodies := 0
	for _, body := range stored.Bodies {
		if body.MessageIndex != nil {
			grpcBodies++
		}
	}
	if grpcBodies != 4 {
		t.Fatalf("expected four indexed gRPC attachments, got %d", grpcBodies)
	}
	for _, flow := range stored.Capture.Traffic {
		if flow.Protocol == "grpc" && flow.Response["grpc_status"] != float64(0) {
			t.Fatalf("canonical evidence lacks explicit successful grpc_status: %#v", flow.Response)
		}
	}
	report, err := coverage.Generate(t.Context(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	group := report.Provided
	if mode == "consumer" {
		group = report.Dependent
	}
	found := false
	for _, spec := range group.Specs {
		if spec.Type == "protobuf" {
			found = true
			if !spec.DenominatorKnown || len(spec.Exercised) != 1 || spec.Exercised[0].Operation != "/"+healthService+"/Check" || len(spec.NotCovered) != 2 {
				t.Fatalf("gRPC coverage: %#v", spec)
			}
		}
	}
	if !found {
		t.Fatalf("missing gRPC coverage: %#v", group)
	}
	postDependencyJSON(t, controlURL, "/v1/finishRun", map[string]any{})
	finalRun, finalCaptures, err := bundle.Load(root)
	if err != nil || finalRun.RunState != "complete" || len(finalCaptures) == 0 {
		t.Fatalf("finalized archive lost gRPC evidence: %v", err)
	}

	_ = start
}

func TestGRPCZeroTestRunStillFreezesCoverageInventory(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	server := health.NewServer()
	server.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	instance.project = &project.Config{SUT: project.API{ID: "health", URL: healthServer(t, server), Protocol: "grpc", Services: []string{healthService}}}
	start := postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
	root := start["bundle_path"].(string)
	run, attempts, err := bundle.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunState != "complete" || len(run.Schemas) != 1 || len(attempts) != 0 {
		t.Fatalf("zero-test inventory: %#v", run)
	}
	report, err := coverage.Generate(t.Context(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Provided.Specs) != 1 || !report.Provided.Specs[0].DenominatorKnown || len(report.Provided.Specs[0].NotCovered) != 3 {
		t.Fatalf("zero-test coverage: %#v", report.Provided)
	}
}

func TestGRPCApplicationErrorStatusIsNotHTTPStatus(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	server := health.NewServer()
	instance.project = &project.Config{SUT: project.API{ID: "health", URL: healthServer(t, server), Protocol: "grpc", Services: []string{healthService}}}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	setup := postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": "unknown service is rejected"})
	conn, err := grpc.NewClient(strings.TrimPrefix(setup["sut_url"].(string), "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("RPC error = %v", err)
	}
	finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": setup["session_id"], "status": "pass"})
	art := dependencyArtifact(t, finished)
	if art.TestCase.Status != "pass" || art.SchemaValidation.Status != "pass" || len(art.Traffic) != 1 || art.Traffic[0].Response.GRPC.StatusCode != int(codes.NotFound) || art.Traffic[0].Response.GRPC.HTTPStatus != 200 {
		t.Fatalf("error RPC evidence: %#v", art)
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}

func TestParallelGRPCUsesW3CTraceOwnership(t *testing.T) {
	control, instance := dependencyCaptureRuntime(t)
	server := health.NewServer()
	server.SetServingStatus("a", healthpb.HealthCheckResponse_SERVING)
	server.SetServingStatus("b", healthpb.HealthCheckResponse_SERVING)
	instance.project = &project.Config{SUT: project.API{ID: "health", URL: healthServer(t, server), Protocol: "grpc", Services: []string{healthService}}}
	postDependencyJSON(t, control.URL, "/v1/startRun", map[string]any{})
	type owner struct {
		service, trace string
		session        map[string]any
	}
	owners := []owner{{service: "a", trace: "11111111111111111111111111111111"}, {service: "b", trace: "22222222222222222222222222222222"}}
	for i := range owners {
		owners[i].session = postDependencyJSON(t, control.URL, "/v1/setupTest", map[string]any{"name": owners[i].service, "explicit_session": true, "trace_context": map[string]string{"traceparent": "00-" + owners[i].trace + "-3333333333333333-01"}})
	}
	conn, err := grpc.NewClient(strings.TrimPrefix(owners[0].session["sut_url"].(string), "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	results := make(chan error, 2)
	for _, o := range owners {
		go func(o owner) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("traceparent", "00-"+o.trace+"-4444444444444444-01"))
			_, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: o.service})
			results <- err
		}(o)
	}
	for range owners {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range owners {
		finished := postDependencyJSON(t, control.URL, "/v1/teardownTest", map[string]any{"session_id": o.session["session_id"], "status": "pass"})
		art := dependencyArtifact(t, finished)
		if len(art.Traffic) != 1 || art.Traffic[0].TraceID != o.trace || art.Traffic[0].TestCorrelation != "trace" || !strings.Contains(art.Traffic[0].Request.GRPC.Messages[0].Body.Text, o.service) {
			t.Fatalf("wrong owner for %s: %#v", o.service, art.Traffic)
		}
	}
	postDependencyJSON(t, control.URL, "/v1/finishRun", map[string]any{})
}
