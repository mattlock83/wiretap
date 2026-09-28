package app

import (
	"context"
	"fmt"
	"github.com/phrond/evermock/internal/tracecontext"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/proxy"
)

var validDependencyAssetID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type dependencyRegistration struct {
	AssetID      string               `json:"asset_id"`
	AssetName    string               `json:"asset_name,omitempty"`
	UpstreamKind string               `json:"upstream_kind,omitempty"`
	UpstreamURL  string               `json:"upstream_url"`
	Schema       *artifact.SchemaRef  `json:"schema,omitempty"`
	Schemas      []artifact.SchemaRef `json:"schemas,omitempty"`
}

// runtimeSession owns the capture state for one consumer test. Phrond does
// not own the SUT lifecycle; it only records traffic routed through its proxy.
type runtimeSession struct {
	finishMu     sync.Mutex
	CaseID       string
	Root         trace.SpanContext
	pending      int
	closing      bool
	drained      chan struct{}
	ID           string
	Name         string
	Store        *TestCaseStore
	SUTBaseURL   string
	Dependencies map[string]dependencyRegistration
	Explicit     bool
	CreatedAt    time.Time
}

func normalizeDependencies(dependencies []dependencyRegistration) (map[string]dependencyRegistration, []artifact.SchemaRef, error) {
	registered := make(map[string]dependencyRegistration, len(dependencies))
	var schemas []artifact.SchemaRef
	for index, dependency := range dependencies {
		dependency.AssetID = strings.TrimSpace(dependency.AssetID)
		if !validDependencyAssetID.MatchString(dependency.AssetID) {
			return nil, nil, fmt.Errorf("dependency %d requires a valid asset_id containing only letters, numbers, dots, underscores, and hyphens", index+1)
		}
		if _, exists := registered[dependency.AssetID]; exists {
			return nil, nil, fmt.Errorf("dependency asset_id %q is declared more than once", dependency.AssetID)
		}
		dependency.UpstreamURL = strings.TrimSpace(dependency.UpstreamURL)
		upstream, err := url.Parse(dependency.UpstreamURL)
		if err != nil || upstream.Host == "" || (upstream.Scheme != "http" && upstream.Scheme != "https") {
			return nil, nil, fmt.Errorf("dependency %q requires an absolute http or https upstream_url", dependency.AssetID)
		}
		if upstream.Fragment != "" {
			return nil, nil, fmt.Errorf("dependency %q upstream_url must not contain a fragment", dependency.AssetID)
		}
		dependency.AssetName = strings.TrimSpace(dependency.AssetName)
		if dependency.UpstreamKind == "" {
			dependency.UpstreamKind = "unknown"
		}
		if dependency.UpstreamKind != "unknown" && dependency.UpstreamKind != "mock" && dependency.UpstreamKind != "service" {
			return nil, nil, fmt.Errorf("dependency %q upstream_kind must be mock, service, or unknown", dependency.AssetID)
		}
		if dependency.AssetName == "" {
			dependency.AssetName = dependency.AssetID
		}
		selectedSchemas := append([]artifact.SchemaRef(nil), dependency.Schemas...)
		if dependency.Schema != nil {
			selectedSchemas = append(selectedSchemas, *dependency.Schema)
		}
		if len(selectedSchemas) == 0 {
			return nil, nil, fmt.Errorf("dependency %q requires an OpenAPI schema; supply schema or schemas", dependency.AssetID)
		}
		if err := validateSchemaSetup(selectedSchemas, false); err != nil {
			return nil, nil, fmt.Errorf("dependency %q: %w", dependency.AssetID, err)
		}
		for i := range selectedSchemas {
			selectedSchemas[i].Role = "dependency"
			selectedSchemas[i].AssetID = dependency.AssetID
			if selectedSchemas[i].AssetName == "" {
				selectedSchemas[i].AssetName = dependency.AssetName
			}
		}
		dependency.Schema = nil
		dependency.Schemas = selectedSchemas
		registered[dependency.AssetID] = dependency
		schemas = append(schemas, selectedSchemas...)
	}
	return registered, schemas, nil
}

func (a *App) resolveDependencyRoute(request *http.Request, requestSessionID string) (proxy.DependencyRoute, bool, error) {
	path := request.URL.Path
	var sessionID, assetID, remaining string
	switch {
	case strings.HasPrefix(path, "/__phrond/dep/"):
		parts := strings.SplitN(strings.TrimPrefix(path, "/__phrond/dep/"), "/", 2)
		assetID = parts[0]
		if len(parts) == 2 {
			remaining = "/" + parts[1]
		}
		sessionID = strings.TrimSpace(requestSessionID)
	case strings.HasPrefix(path, "/__phrond/session/"):
		parts := strings.SplitN(strings.TrimPrefix(path, "/__phrond/session/"), "/", 4)
		if len(parts) < 3 || parts[1] != "dep" {
			return proxy.DependencyRoute{}, true, fmt.Errorf("invalid session-scoped dependency capture URL")
		}
		sessionID, assetID = parts[0], parts[2]
		if requestSessionID != "" && requestSessionID != sessionID {
			return proxy.DependencyRoute{}, true, fmt.Errorf("dependency capture session does not match request session")
		}
		if len(parts) == 4 {
			remaining = "/" + parts[3]
		}
	default:
		return proxy.DependencyRoute{}, false, nil
	}
	if !validDependencyAssetID.MatchString(assetID) {
		return proxy.DependencyRoute{}, true, fmt.Errorf("invalid dependency asset_id")
	}
	if remaining == "" {
		remaining = "/"
	}
	escapedPath := request.URL.EscapedPath()
	var escapedPrefix string
	if sessionID != "" && strings.HasPrefix(path, "/__phrond/session/") {
		escapedPrefix = "/__phrond/session/" + sessionID + "/dep/" + assetID
	} else {
		escapedPrefix = "/__phrond/dep/" + assetID
	}
	rawRemaining := strings.TrimPrefix(escapedPath, escapedPrefix)
	if rawRemaining == "" {
		rawRemaining = "/"
	}

	a.sessionsMu.Lock()
	if sessionID == "" {
		sessionID = a.defaultSessionID
	}
	session := a.sessions[sessionID]
	var dependency dependencyRegistration
	var found bool
	if session != nil {
		dependency, found = session.Dependencies[assetID]
	}
	a.sessionsMu.Unlock()
	if session == nil {
		return proxy.DependencyRoute{}, true, fmt.Errorf("no active capture session for dependency %q", assetID)
	}
	if !found {
		return proxy.DependencyRoute{}, true, fmt.Errorf("dependency %q is not declared for capture session %q", assetID, sessionID)
	}
	return proxy.DependencyRoute{
		SessionID: sessionID, AssetID: assetID, UpstreamURL: dependency.UpstreamURL,
		Path: remaining, RawPath: rawRemaining,
	}, true, nil
}

func (a *App) nextSessionID() string {
	a.sessionSeq++
	return fmt.Sprintf("session-%d-%d", time.Now().UTC().UnixNano(), a.sessionSeq)
}

// admitRequest resolves all independent selectors before routing dependencies.
// Closed trace IDs stay reserved for the lifetime of this runtime.
func (a *App) admitRequest(request *http.Request, headerSession string) (*proxy.Admission, error) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	selected := headerSession
	explicit := selected != ""
	if strings.HasPrefix(request.URL.Path, "/__phrond/session/") {
		parts := strings.SplitN(strings.TrimPrefix(request.URL.Path, "/__phrond/session/"), "/", 4)
		if len(parts) < 3 || parts[1] != "dep" {
			return nil, fmt.Errorf("invalid session-scoped dependency URL")
		}
		if selected != "" && selected != parts[0] {
			return nil, fmt.Errorf("conflicting session selectors")
		}
		selected, explicit = parts[0], true
	}
	sc := tracecontext.Extract(request.Header)
	correlation := ""
	if sc.IsValid() {
		owner, known := a.traceOwners[sc.TraceID().String()]
		if known {
			if owner == "" {
				return nil, fmt.Errorf("trace belongs to a closed capture session")
			}
			if selected != "" && selected != owner {
				return nil, fmt.Errorf("trace and session selectors conflict")
			}
			selected, correlation = owner, "trace"
		}
	}
	if explicit && correlation == "" {
		correlation = "explicit_session"
	}
	if selected == "" && len(a.sessions) == 1 && a.defaultSessionID != "" && !a.hasRetiredWorkLocked() {
		selected, correlation = a.defaultSessionID, "active_session"
	}
	session := a.sessions[selected]
	if selected == "" && a.legacySession != nil {
		session, correlation = a.legacySession, "active_session"
	}
	if session == nil {
		if selected != "" || len(a.sessions) > 0 || strings.HasPrefix(request.URL.Path, "/__phrond/") {
			return nil, fmt.Errorf("no unambiguous capture session; propagate W3C trace context or use an explicit session")
		}
		// Traffic outside any test remains usable but must never be recorded later.
		return &proxy.Admission{Record: func(proxy.HTTPExchange) {}}, nil
	}
	if session.closing {
		return nil, fmt.Errorf("capture session is closing")
	}
	if sc.IsValid() && session.Root.IsValid() && sc.TraceID() != session.Root.TraceID() {
		session.Store.AddCaptureDiagnostic("received trace does not match the registered test trace; ownership used " + correlation)
	}
	session.pending++
	a.inFlight++
	var once sync.Once
	return &proxy.Admission{
		SessionID: session.ID, CaseID: session.CaseID, Root: session.Root, TestCorrelation: correlation,
		Record: session.Store.RecordHTTP,
		Release: func() {
			once.Do(func() {
				a.sessionsMu.Lock()
				defer a.sessionsMu.Unlock()
				session.pending--
				a.inFlight--
				if session.closing && session.pending == 0 {
					close(session.drained)
				}
			})
		},
	}, nil
}

// beginDrain must run while the control lock prevents setup/shutdown changes.
func (a *App) beginDrain(session *runtimeSession) bool {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if session.closing {
		return false
	}
	session.closing = true
	if session.Root.IsValid() {
		a.traceOwners[session.Root.TraceID().String()] = ""
	}
	if a.defaultSessionID == session.ID {
		a.defaultSessionID = ""
	}
	if session.pending == 0 {
		close(session.drained)
	}
	return true
}

func (a *App) waitForDrain(ctx context.Context, session *runtimeSession, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-session.drained:
		return
	default:
	}
	select {
	case <-session.drained:
	case <-timer.C:
		session.Store.MarkCaptureIncomplete("capture drain timed out; in-flight exchanges were omitted")
	case <-ctx.Done():
		session.Store.MarkCaptureIncomplete("capture drain cancelled; in-flight exchanges were omitted")
	}
}

func (a *App) hasActiveSessions() bool {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	return len(a.sessions) != 0 || a.legacySession != nil || a.inFlight != 0
}

// Control requests are serialized; traffic admission uses a separate lock.
func (a *App) setReverseTarget(target string) error {
	target = strings.TrimRight(strings.TrimSpace(target), "/")
	if a.hasActiveSessions() && target != a.reverseTarget {
		return fmt.Errorf("cannot change the SUT target while capture sessions are active")
	}
	if err := a.proxyComponent.SetReverseTarget(target); err != nil {
		return err
	}
	a.reverseTarget = target
	return nil
}

// Call with sessionsMu held. Unlabeled traffic cannot be safely assigned while
// a retired attempt can still produce dependency requests.
func (a *App) hasRetiredWorkLocked() bool {
	active := 0
	for _, session := range a.sessions {
		if session.closing {
			return true
		}
		active += session.pending
	}
	if a.legacySession != nil {
		active += a.legacySession.pending
	}
	return a.inFlight > active
}

// Called under controlMu only after all captures and admitted requests stop.
func (a *App) resetCaptureRouting() {
	a.sessionsMu.Lock()
	a.traceOwners = map[string]string{}
	a.sessions = map[string]*runtimeSession{}
	a.defaultSessionID = ""
	a.legacySession = nil
	a.sessionsMu.Unlock()
	a.reverseTarget = ""
	if a.proxyComponent != nil {
		_ = a.proxyComponent.SetReverseTarget("")
	}
}
