package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/phrond/evermock/internal/mock"
)

const defaultStandaloneScenarioHeader = "X-Phrond-Scenario"

var (
	errStandaloneAlreadyActive = errors.New("standalone mode is already active")
	errStandaloneSessionsOpen  = errors.New("cannot start standalone mode while test sessions are active")
)

type standaloneState struct {
	Active         bool
	StartedAt      time.Time
	ScenarioHeader string
	TemplateFiles  []string
	TemplateCount  int
	MockListenAddr string
}

type StandaloneStartOptions struct {
	MockListenAddr string
	StackSize      int
	ScenarioHeader string
	TemplateFiles  []string
}

type StandaloneStatus struct {
	Status          string   `json:"status"`
	StartedAt       string   `json:"started_at,omitempty"`
	ScenarioHeader  string   `json:"scenario_header,omitempty"`
	TemplateFiles   []string `json:"template_files,omitempty"`
	TemplatesLoaded int      `json:"templates_loaded,omitempty"`
	Mock            struct {
		ListenAddr string `json:"listen_addr,omitempty"`
	} `json:"mock,omitempty"`
	StackSize int `json:"stack_size,omitempty"`
}

type standaloneTemplateFile struct {
	ID         string `json:"id,omitempty"`
	ScenarioID string `json:"scenario_id,omitempty"`
	Match      struct {
		Method       string            `json:"method,omitempty"`
		Path         string            `json:"path,omitempty"`
		Headers      map[string]string `json:"headers,omitempty"`
		Query        map[string]string `json:"query,omitempty"`
		Body         string            `json:"body,omitempty"`
		BodyContains string            `json:"body_contains,omitempty"`
	} `json:"match"`
	Response struct {
		Status          int               `json:"status,omitempty"`
		DelayMS         int               `json:"delay_ms,omitempty"`
		Headers         map[string]string `json:"headers,omitempty"`
		Body            string            `json:"body,omitempty"`
		BodyFile        string            `json:"body_file,omitempty"`
		ConnectionError bool              `json:"connection_error,omitempty"`
		SSLError        bool              `json:"ssl_error,omitempty"`
	} `json:"response"`
}

type loadedStandaloneTemplate struct {
	File       string
	ScenarioID string
	Stub       mock.Stub
}

func (a *App) isStandaloneActive() bool {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	return a.standalone != nil && a.standalone.Active
}

func (a *App) rejectIfStandaloneActive(w http.ResponseWriter) bool {
	if !a.isStandaloneActive() {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": "standalone mode is active; stop standalone mode before using test-session APIs"})
	return true
}

func normalizeStandaloneScenarioHeader(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultStandaloneScenarioHeader
	}
	return trimmed
}

func (a *App) StartStandalone(ctx context.Context, options StandaloneStartOptions) (*StandaloneStatus, error) {
	if a == nil || a.mockComponent == nil {
		return nil, errors.New("runtime components not configured")
	}

	resolvedFiles, err := resolveStandaloneTemplateFiles(options.TemplateFiles)
	if err != nil {
		return nil, err
	}
	if len(resolvedFiles) == 0 {
		return nil, errors.New("at least one template file is required")
	}

	scenarioHeader := normalizeStandaloneScenarioHeader(options.ScenarioHeader)
	loaded, err := loadStandaloneTemplates(resolvedFiles, scenarioHeader)
	if err != nil {
		return nil, err
	}

	a.sessionsMu.Lock()
	if a.standalone != nil && a.standalone.Active {
		a.sessionsMu.Unlock()
		return nil, errStandaloneAlreadyActive
	}
	if len(a.sessions) > 0 {
		a.sessionsMu.Unlock()
		return nil, errStandaloneSessionsOpen
	}
	if options.StackSize > 0 {
		a.stackSize = options.StackSize
	}
	stackSize := a.stackSize
	a.sessionsMu.Unlock()

	if len(loaded) > stackSize {
		return nil, fmt.Errorf("template count %d exceeds stack_size %d", len(loaded), stackSize)
	}

	if options.MockListenAddr != "" {
		if err := a.mockComponent.SetListenAddr(options.MockListenAddr); err != nil {
			return nil, err
		}
	}

	if err := a.mockComponent.Start(ctx); err != nil {
		return nil, err
	}

	a.mockComponent.SetRecordingEnabled(false)
	_ = a.mockComponent.SetFallbackUpstream("")
	a.mockComponent.ClearStubs()
	a.mockComponent.ClearAllSessionVars()
	if recorder := a.mockComponent.Recorder(); recorder != nil {
		recorder.Clear()
	}

	defaultStubs := make([]mock.Stub, 0, len(loaded))
	scenarioStubs := make([]mock.Stub, 0, len(loaded))
	for _, entry := range loaded {
		if strings.TrimSpace(entry.ScenarioID) != "" {
			scenarioStubs = append(scenarioStubs, entry.Stub)
			continue
		}
		defaultStubs = append(defaultStubs, entry.Stub)
	}
	for _, stub := range defaultStubs {
		a.mockComponent.RegisterStub(stub)
	}
	for _, stub := range scenarioStubs {
		a.mockComponent.RegisterStub(stub)
	}

	now := time.Now().UTC()
	state := &standaloneState{
		Active:         true,
		StartedAt:      now,
		ScenarioHeader: scenarioHeader,
		TemplateFiles:  resolvedFiles,
		TemplateCount:  len(loaded),
		MockListenAddr: a.mockComponent.Addr(),
	}

	a.sessionsMu.Lock()
	a.standalone = state
	a.sessionsMu.Unlock()

	return a.buildStandaloneStatus("standalone_started"), nil
}

func (a *App) StopStandalone(ctx context.Context) error {
	if a == nil || a.mockComponent == nil {
		return nil
	}

	a.sessionsMu.Lock()
	if a.standalone == nil || !a.standalone.Active {
		a.sessionsMu.Unlock()
		return nil
	}
	a.standalone = nil
	a.sessionsMu.Unlock()

	a.mockComponent.ClearStubs()
	a.mockComponent.ClearAllSessionVars()
	if recorder := a.mockComponent.Recorder(); recorder != nil {
		recorder.Clear()
	}
	a.mockComponent.SetRecordingEnabled(true)
	_ = a.mockComponent.SetFallbackUpstream("")
	return a.mockComponent.Stop(ctx)
}

func (a *App) buildStandaloneStatus(status string) *StandaloneStatus {
	out := &StandaloneStatus{Status: status}

	a.sessionsMu.Lock()
	stackSize := a.stackSize
	state := a.standalone
	a.sessionsMu.Unlock()

	out.StackSize = stackSize
	if state != nil {
		out.StartedAt = state.StartedAt.Format(time.RFC3339Nano)
		out.ScenarioHeader = state.ScenarioHeader
		out.TemplateFiles = append([]string{}, state.TemplateFiles...)
		out.TemplatesLoaded = state.TemplateCount
	}
	if a.mockComponent != nil {
		out.Mock.ListenAddr = a.mockComponent.Addr()
	}
	return out
}

func resolveStandaloneTemplateFiles(inputs []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0)

	for _, raw := range inputs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		resolved := resolvePath(trimmed)
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("template path %q: %w", trimmed, err)
		}

		if !info.IsDir() {
			if !strings.EqualFold(filepath.Ext(resolved), ".json") {
				continue
			}
			if _, ok := seen[resolved]; !ok {
				seen[resolved] = struct{}{}
				out = append(out, resolved)
			}
			continue
		}

		err = filepath.WalkDir(resolved, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(path), ".json") {
				return nil
			}
			if _, ok := seen[path]; ok {
				return nil
			}
			seen[path] = struct{}{}
			out = append(out, path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk template directory %q: %w", resolved, err)
		}
	}

	sort.Strings(out)
	return out, nil
}

func loadStandaloneTemplates(files []string, scenarioHeader string) ([]loadedStandaloneTemplate, error) {
	out := make([]loadedStandaloneTemplate, 0, len(files))
	idSet := map[string]struct{}{}

	for idx, file := range files {
		entry, err := loadStandaloneTemplateFile(file, scenarioHeader, idx+1)
		if err != nil {
			return nil, err
		}
		if _, exists := idSet[entry.Stub.ID]; exists {
			return nil, fmt.Errorf("duplicate standalone template id %q", entry.Stub.ID)
		}
		idSet[entry.Stub.ID] = struct{}{}
		out = append(out, entry)
	}
	return out, nil
}

func loadStandaloneTemplateFile(path string, scenarioHeader string, index int) (loadedStandaloneTemplate, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return loadedStandaloneTemplate{}, fmt.Errorf("read template file %q: %w", path, err)
	}

	var payload standaloneTemplateFile
	if err := json.Unmarshal(content, &payload); err != nil {
		return loadedStandaloneTemplate{}, fmt.Errorf("decode template file %q: %w", path, err)
	}

	method := strings.ToUpper(strings.TrimSpace(payload.Match.Method))
	matchPath := strings.TrimSpace(payload.Match.Path)
	if method == "" {
		return loadedStandaloneTemplate{}, fmt.Errorf("template file %q: match.method is required", path)
	}
	if matchPath == "" {
		return loadedStandaloneTemplate{}, fmt.Errorf("template file %q: match.path is required", path)
	}
	if !strings.HasPrefix(matchPath, "/") {
		matchPath = "/" + matchPath
	}

	if payload.Response.ConnectionError && payload.Response.SSLError {
		return loadedStandaloneTemplate{}, fmt.Errorf("template file %q: connection_error and ssl_error cannot both be true", path)
	}

	body := payload.Response.Body
	if bodyFile := strings.TrimSpace(payload.Response.BodyFile); bodyFile != "" {
		resolvedBodyPath := bodyFile
		if !filepath.IsAbs(resolvedBodyPath) {
			resolvedBodyPath = filepath.Join(filepath.Dir(path), bodyFile)
		}
		rawBody, readErr := os.ReadFile(resolvedBodyPath)
		if readErr != nil {
			return loadedStandaloneTemplate{}, fmt.Errorf("template file %q body_file %q: %w", path, bodyFile, readErr)
		}
		body = string(rawBody)
	}

	headers := cloneStringMap(payload.Match.Headers)
	scenarioID := strings.TrimSpace(payload.ScenarioID)
	if scenarioID != "" {
		if headers == nil {
			headers = map[string]string{}
		}
		if existing, ok := headers[scenarioHeader]; ok && existing != scenarioID {
			return loadedStandaloneTemplate{}, fmt.Errorf("template file %q: scenario header %q conflicts with scenario_id", path, scenarioHeader)
		}
		headers[scenarioHeader] = scenarioID
	}

	responseID := strings.TrimSpace(payload.ID)
	if responseID == "" {
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		responseID = fmt.Sprintf("standalone-%03d-%s", index, sanitizeStandaloneID(base))
	}

	fault := ""
	if payload.Response.ConnectionError {
		fault = "connection_error"
	} else if payload.Response.SSLError {
		fault = "ssl_error"
	}

	stub := mock.Stub{
		ID:      responseID,
		Default: true,
		Match: mock.StubMatch{
			Method:       method,
			Path:         matchPath,
			Headers:      headers,
			Query:        cloneStringMap(payload.Match.Query),
			Body:         payload.Match.Body,
			BodyContains: payload.Match.BodyContains,
		},
		Response: mock.StubResponse{
			Status:  payload.Response.Status,
			DelayMS: payload.Response.DelayMS,
			Headers: cloneStringMap(payload.Response.Headers),
			Body:    body,
			Fault:   fault,
		},
	}

	return loadedStandaloneTemplate{
		File:       path,
		ScenarioID: scenarioID,
		Stub:       stub,
	}, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sanitizeStandaloneID(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "template"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	normalized := strings.Trim(b.String(), "-")
	if normalized == "" {
		return "template"
	}
	return normalized
}
