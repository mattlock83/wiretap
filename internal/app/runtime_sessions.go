package app

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/proxy"
)

var validDependencyAssetID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type dependencyRegistration struct {
	AssetID     string               `json:"asset_id"`
	AssetName   string               `json:"asset_name,omitempty"`
	UpstreamURL string               `json:"upstream_url"`
	Schema      *artifact.SchemaRef  `json:"schema,omitempty"`
	Schemas     []artifact.SchemaRef `json:"schemas,omitempty"`
}

// runtimeSession owns the capture state for one consumer test. Phrond does
// not own the SUT lifecycle; it only records traffic routed through its proxy.
type runtimeSession struct {
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

func (a *App) recordProxyForSession(exchange proxy.HTTPExchange) {
	sessionID := strings.TrimSpace(exchange.SessionID)

	a.sessionsMu.Lock()
	if sessionID == "" {
		sessionID = a.defaultSessionID
	}
	session := a.sessions[sessionID]
	a.sessionsMu.Unlock()

	if session != nil && session.Store != nil {
		session.Store.RecordHTTP(exchange)
	}
}
