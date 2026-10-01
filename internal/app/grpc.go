package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/grpccontract"
	"github.com/phrond/wiretap/internal/project"
	"github.com/phrond/wiretap/internal/proxy"
	"github.com/phrond/wiretap/internal/tracecontext"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type grpcCaptureListener struct {
	server      *http.Server
	listener    net.Listener
	url         string
	connections *proxy.Connections
}

func (a *App) startGRPCListeners() error {
	if a.project == nil {
		return nil
	}
	a.grpcMu.Lock()
	defer a.grpcMu.Unlock()
	if a.grpcListeners != nil {
		return nil
	}
	listeners := map[string]*grpcCaptureListener{}
	for _, id := range a.project.DependencyIDs() {
		dep := a.project.Dependencies[id]
		if dep.Protocol != "grpc" {
			continue
		}
		addr := dep.CaptureListen
		if addr == "" {
			addr = "127.0.0.1:0"
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, l := range listeners {
				_ = l.server.Close()
			}
			return fmt.Errorf("gRPC dependency %s listener: %w", id, err)
		}
		assetID := id
		connections := &proxy.Connections{}
		server := &http.Server{ConnState: connections.State, Handler: h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.forwardGRPC(w, r, assetID) }), &http2.Server{}), ReadHeaderTimeout: 10 * time.Second}
		scheme := "http"
		if dep.CaptureTLS.CertFile != "" {
			if _, e := tls.LoadX509KeyPair(dep.CaptureTLS.CertFile, dep.CaptureTLS.KeyFile); e != nil {
				_ = ln.Close()
				for _, l := range listeners {
					_ = l.server.Close()
				}
				return fmt.Errorf("gRPC dependency %s TLS: %w", id, e)
			}
			scheme = "https"
		}
		listeners[id] = &grpcCaptureListener{server: server, listener: ln, url: scheme + "://" + ln.Addr().String(), connections: connections}
		go func() {
			if dep.CaptureTLS.CertFile != "" {
				_ = server.ServeTLS(ln, dep.CaptureTLS.CertFile, dep.CaptureTLS.KeyFile)
			} else {
				_ = server.Serve(ln)
			}
		}()
	}
	a.grpcListeners = listeners
	return nil
}

func (a *App) stopGRPCListeners(ctx context.Context) error {
	callErr := a.stopGRPCCalls(ctx)
	a.grpcTransportMu.Lock()
	for _, transport := range a.grpcTransports {
		transport.CloseIdleConnections()
	}
	a.grpcTransports = nil
	a.grpcTransportMu.Unlock()
	a.grpcMu.Lock()
	listeners := a.grpcListeners
	a.grpcListeners = nil
	a.grpcContracts = nil
	a.grpcSchemas = nil
	a.grpcMu.Unlock()
	var result = callErr
	for _, l := range listeners {
		result = errors.Join(result, l.server.Shutdown(ctx))
		_ = l.server.Close()
		l.connections.Close()
	}
	return result
}

func (a *App) grpcSUTURL() string {
	scheme := "http"
	if a.project != nil && a.project.SUT.CaptureTLS.CertFile != "" {
		scheme = "https"
	}
	return scheme + "://" + a.proxyComponent.ListenAddr()
}
func (a *App) grpcDependencyURL(id string) string {
	a.grpcMu.RLock()
	defer a.grpcMu.RUnlock()
	if l := a.grpcListeners[id]; l != nil {
		return l.url
	}
	return ""
}
func (a *App) grpcDependencyURLs() map[string]string {
	a.grpcMu.RLock()
	defer a.grpcMu.RUnlock()
	out := map[string]string{}
	for id, l := range a.grpcListeners {
		out[id] = l.url
	}
	return out
}

func (a *App) forwardGRPC(w http.ResponseWriter, r *http.Request, assetID string) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/grpc" && mediaType != "application/grpc+proto" {
		http.Error(w, "native gRPC required", http.StatusUnsupportedMediaType)
		return
	}
	target := ""
	channel := "test_to_sut"
	if assetID == "" {
		target = a.reverseTarget
	} else {
		channel = "sut_to_dependency"
		if a.project != nil {
			target = a.project.Dependencies[assetID].TargetURL()
		}
	}
	if target == "" {
		proxy.WriteGRPCError(w, 14, "gRPC upstream is not configured")
		return
	}
	var api project.API
	if a.project != nil {
		if assetID == "" {
			api = a.project.SUT
		} else {
			api = a.project.Dependencies[assetID]
		}
	}
	transport, err := a.sharedGRPCTransport(assetID, target, api)
	if err != nil {
		proxy.WriteGRPCError(w, 14, err.Error())
		return
	}
	admission, err := a.admitRequest(r, strings.TrimSpace(r.Header.Get("X-Wiretap-Session-Id")))
	if err != nil {
		proxy.WriteGRPCError(w, 9, err.Error())
		return
	}
	if admission.Release != nil {
		defer admission.Release()
	}
	sc := tracecontext.Extract(r.Header)
	source := ""
	if sc.IsValid() {
		source = "received"
	} else if assetID == "" && admission.Root.IsValid() {
		sc = admission.Root
		tracecontext.Inject(r.Header, sc)
		source = "injected"
	}
	if admission.RecordGRPC != nil {
		original := admission.RecordGRPC
		admission.RecordGRPC = func(ex proxy.GRPCExchange) {
			ex.TraceContextSource = source
			if sc.IsValid() {
				ex.TraceID = sc.TraceID().String()
				ex.RequestSpanID = sc.SpanID().String()
			}
			original(ex)
		}
	}
	proxy.ForwardGRPC(w, r, target, channel, assetID, admission, a.cfg.Capture.MaxBodyBytes, transport)
}

func (a *App) sharedGRPCTransport(assetID, target string, api project.API) (*http2.Transport, error) {
	a.grpcTransportMu.Lock()
	defer a.grpcTransportMu.Unlock()
	key := "sut"
	if assetID != "" {
		key = "dep:" + assetID
	}
	if existing := a.grpcTransports[key]; existing != nil {
		return existing, nil
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid gRPC upstream %q", target)
	}
	upstreamTLS, err := grpcUpstreamTLS(api)
	if err != nil {
		return nil, err
	}
	if a.grpcTransports == nil {
		a.grpcTransports = map[string]*http2.Transport{}
	}
	transport := proxy.NewGRPCTransport(u.Scheme, upstreamTLS)
	a.grpcTransports[key] = transport
	return transport, nil
}

func grpcUpstreamTLS(api project.API) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: api.UpstreamTLS.ServerName}
	if api.UpstreamTLS.CAFile != "" {
		data, err := os.ReadFile(api.UpstreamTLS.CAFile)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("upstream CA file contains no certificates")
		}
		config.RootCAs = pool
	}
	return config, nil
}

func grpcSource(api project.API) (grpccontract.Source, error) {
	src := grpccontract.Source{Kind: "reflection"}
	var err error
	src.TLSConfig, err = grpcUpstreamTLS(api)
	if err != nil {
		return src, err
	}
	if api.GRPCSchema == nil {
		return src, nil
	}
	opt := api.GRPCSchema
	src.Kind = opt.Source
	src.Path = opt.Path
	src.Files = opt.Files
	src.ImportPaths = opt.ImportPaths
	if opt.Timeout != "" {
		duration, err := time.ParseDuration(opt.Timeout)
		if err != nil || duration <= 0 {
			return src, fmt.Errorf("invalid reflection timeout %q", opt.Timeout)
		}
		src.Timeout = duration
	}
	if len(opt.Headers) > 0 {
		src.Headers = http.Header{}
		for key, value := range opt.Headers {
			src.Headers.Set(key, value)
		}
	}
	return src, nil
}

// Resolve all declared gRPC boundaries before the first test is admitted. The
// completed snapshot is reused by every later attempt in this run.
func (a *App) resolveGRPCContracts(ctx context.Context) ([]artifact.SchemaRef, map[string]*grpccontract.Contract, error) {
	a.grpcMu.Lock()
	defer a.grpcMu.Unlock()
	if a.grpcContracts != nil {
		schemas := append([]artifact.SchemaRef(nil), a.grpcSchemas...)
		contracts := a.grpcContracts
		return schemas, contracts, nil
	}
	if a.project == nil {
		return nil, nil, nil
	}
	type boundary struct {
		id, role string
		api      project.API
	}
	var boundaries []boundary
	if a.project.SUT.Protocol == "grpc" {
		id := a.project.SUT.ID
		if id == "" {
			id = "sut"
		}
		boundaries = append(boundaries, boundary{id, "sut", a.project.SUT})
	}
	for _, id := range a.project.DependencyIDs() {
		api := a.project.Dependencies[id]
		if api.Protocol == "grpc" {
			boundaries = append(boundaries, boundary{id, "dependency", api})
		}
	}
	if len(boundaries) == 0 {
		return nil, nil, nil
	}
	if _, err := a.ensureBundle(); err != nil {
		return nil, nil, err
	}
	if err := a.recordGRPCResolution("pending"); err != nil {
		return nil, nil, err
	}
	type result struct {
		contract *grpccontract.Contract
		err      error
	}
	results := make([]result, len(boundaries))
	var workers sync.WaitGroup
	limit := make(chan struct{}, 4)
	for index, item := range boundaries {
		workers.Add(1)
		go func(index int, b boundary) {
			defer workers.Done()
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			defer func() { <-limit }()
			src, err := grpcSource(b.api)
			if err == nil {
				results[index].contract, err = grpccontract.Resolve(ctx, b.api.TargetURL(), src, b.api.Services)
			}
			if err != nil {
				message := err.Error()
				for _, values := range src.Headers {
					for _, value := range values {
						if value != "" {
							message = strings.ReplaceAll(message, value, "[redacted]")
						}
					}
				}
				results[index].err = fmt.Errorf("gRPC contract for %s: %s", b.id, message)
			}
		}(index, item)
	}
	workers.Wait()
	contracts := map[string]*grpccontract.Contract{}
	refs := make([]artifact.SchemaRef, 0, len(boundaries))
	for index, b := range boundaries {
		if results[index].err != nil {
			return nil, nil, errors.Join(results[index].err, a.recordGRPCResolution("failed"))
		}
		contract := results[index].contract
		key := "dep:" + b.id
		if b.role == "sut" {
			key = "sut"
		}
		contracts[key] = contract
		acq := contract.Acquisition
		refs = append(refs, artifact.SchemaRef{ID: b.id, Role: b.role, AssetID: b.id, AssetName: b.id, Type: "protobuf", Services: contract.Services, AcquiredFrom: contract.Source, Acquisition: &artifact.SchemaAcquisition{AcquiredAt: acq.AcquiredAt, ReflectionVersion: acq.ReflectionVersion, AdvertisedServices: acq.AdvertisedServices, ExcludedServices: acq.ExcludedServices, CompilerVersion: acq.CompilerVersion, NormalizationProfile: acq.NormalizationProfile}, AssetSHA: contract.SHA256, Source: artifact.SchemaSource{Type: "protobuf_inline", Content: base64.StdEncoding.EncodeToString(contract.Bytes), SHA256: contract.SHA256, Format: "file_descriptor_set"}})
	}
	if err := a.currentBundle().RegisterSchemas(refs); err != nil {
		return nil, nil, errors.Join(err, a.recordGRPCResolution("failed"))
	}
	if err := a.recordGRPCResolution("resolved", refs...); err != nil {
		return nil, nil, err
	}

	a.grpcContracts = contracts
	a.grpcSchemas = refs
	return refs, contracts, nil
}
