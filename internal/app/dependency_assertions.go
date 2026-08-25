package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
)

type dependencyAssertionRequest struct {
	SessionID      string                   `json:"session_id"`
	AssetID        string                   `json:"asset_id"`
	Method         string                   `json:"method,omitempty"`
	Path           string                   `json:"path,omitempty"`
	FlowID         string                   `json:"flow_id,omitempty"`
	ProviderStates []artifact.ProviderState `json:"provider_states,omitempty"`
	Response       map[string]any           `json:"response"`
}

func (a *App) handleAssertDependency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var request dependencyAssertionRequest
	if err := decodeStrictJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.AssetID = strings.TrimSpace(request.AssetID)
	request.Method = strings.ToUpper(strings.TrimSpace(request.Method))
	request.Path = strings.TrimSpace(request.Path)
	request.FlowID = strings.TrimSpace(request.FlowID)
	if request.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	if !validDependencyAssetID.MatchString(request.AssetID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a valid dependency asset_id is required"})
		return
	}
	if len(request.Response) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one response field assertion is required"})
		return
	}
	for path := range request.Response {
		if _, err := parseAssertionPath(path); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	for i := range request.ProviderStates {
		request.ProviderStates[i].Name = strings.TrimSpace(request.ProviderStates[i].Name)
		if request.ProviderStates[i].Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider states require a non-empty name"})
			return
		}
	}

	a.sessionsMu.Lock()
	session := a.sessions[request.SessionID]
	a.sessionsMu.Unlock()
	if session == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if _, configured := session.Dependencies[request.AssetID]; !configured {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("dependency %q is not configured for this session", request.AssetID),
		})
		return
	}

	assertions, err := session.Store.AssertDependency(request)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": err.Error(), "assertions": assertions,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "pass", "asset_id": request.AssetID,
		"flow_id": assertions[0].FlowID, "assertions": assertions,
	})
}

// AssertDependency checks the most recent matching captured dependency flow
// and records each selected response field against the original exchange.
func (s *TestCaseStore) AssertDependency(request dependencyAssertionRequest) ([]artifact.Assertion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return nil, errors.New("no active test case")
	}

	var selected *artifact.Flow
	for i := len(s.active.flows) - 1; i >= 0; i-- {
		flow := &s.active.flows[i]
		if flow.Channel != "sut_to_dependency" || flow.AssetID != request.AssetID || flow.Request.HTTP == nil {
			continue
		}
		if request.FlowID != "" && flow.ID != request.FlowID {
			continue
		}
		if request.Method != "" && !strings.EqualFold(flow.Request.HTTP.Method, request.Method) {
			continue
		}
		if request.Path != "" {
			parsed, err := url.Parse(flow.Request.HTTP.URL)
			if err != nil || (parsed.Path != request.Path && parsed.RequestURI() != request.Path) {
				continue
			}
		}
		selected = flow
		break
	}
	if selected != nil {
		for _, state := range request.ProviderStates {
			found := false
			for _, existing := range selected.ProviderStates {
				if existing.Name == state.Name && reflect.DeepEqual(existing.Params, state.Params) {
					found = true
					break
				}
			}
			if !found {
				selected.ProviderStates = append(selected.ProviderStates, state)
			}
		}
		sort.SliceStable(selected.ProviderStates, func(i, j int) bool {
			return selected.ProviderStates[i].Name < selected.ProviderStates[j].Name
		})
	}

	paths := make([]string, 0, len(request.Response))
	for path := range request.Response {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var response any
	var responseError error
	switch {
	case selected == nil:
		responseError = fmt.Errorf("no captured dependency response matched asset %q", request.AssetID)
	case selected.Response.HTTP == nil || selected.Response.HTTP.Body == nil:
		responseError = errors.New("captured dependency response has no body")
	case selected.Response.HTTP.Body.Truncated:
		responseError = errors.New("captured dependency response body was truncated")
	case selected.Response.HTTP.Body.Encoding == "base64":
		responseError = errors.New("captured dependency response body is not JSON")
	default:
		if err := json.Unmarshal([]byte(selected.Response.HTTP.Body.Text), &response); err != nil {
			responseError = fmt.Errorf("captured dependency response body is not valid JSON: %w", err)
		}
	}

	assertions := make([]artifact.Assertion, 0, len(paths))
	var failed []string
	for _, path := range paths {
		s.assertSeq++
		assertion := artifact.Assertion{
			ID: fmt.Sprintf("assertion-%d", s.assertSeq), Channel: "sut_to_dependency",
			Phase: "response", Field: path, PathLang: "jsonpath", Operator: "equals",
			Expected: request.Response[path], Result: "pass",
		}
		if selected != nil {
			assertion.FlowID = selected.ID
		}
		if responseError != nil {
			assertion.Result = "fail"
			assertion.Message = responseError.Error()
		} else {
			actual, err := lookupAssertionPath(response, path)
			if err != nil {
				assertion.Result = "fail"
				assertion.Message = err.Error()
			} else {
				assertion.Actual = actual
				if !reflect.DeepEqual(actual, assertion.Expected) {
					assertion.Result = "fail"
					assertion.Message = fmt.Sprintf("%s: expected %#v, got %#v", path, assertion.Expected, actual)
				}
			}
		}
		if assertion.Result == "fail" {
			failed = append(failed, assertion.Message)
		}
		if selected != nil {
			selected.AssertionIDs = append(selected.AssertionIDs, assertion.ID)
		}
		s.active.assertions = append(s.active.assertions, assertion)
		assertions = append(assertions, assertion)
	}
	if len(failed) > 0 {
		return assertions, fmt.Errorf("dependency %q response assertion failed: %s", request.AssetID, strings.Join(failed, "; "))
	}
	return assertions, nil
}

type assertionPathPart struct {
	key   string
	index int
	array bool
}

func parseAssertionPath(path string) ([]assertionPathPart, error) {
	if !strings.HasPrefix(path, "$") || len(path) == 1 {
		return nil, fmt.Errorf("response assertion path %q must start with $. or $[", path)
	}
	var parts []assertionPathPart
	for remaining := path[1:]; remaining != ""; {
		switch remaining[0] {
		case '.':
			remaining = remaining[1:]
			end := strings.IndexAny(remaining, ".[ ")
			if end < 0 {
				end = len(remaining)
			}
			if end == 0 {
				return nil, fmt.Errorf("response assertion path %q contains an empty property", path)
			}
			parts = append(parts, assertionPathPart{key: remaining[:end]})
			remaining = remaining[end:]
		case '[':
			end := strings.IndexByte(remaining, ']')
			if end < 2 {
				return nil, fmt.Errorf("response assertion path %q contains an invalid array index", path)
			}
			index, err := strconv.Atoi(remaining[1:end])
			if err != nil || index < 0 {
				return nil, fmt.Errorf("response assertion path %q contains an invalid array index", path)
			}
			parts = append(parts, assertionPathPart{index: index, array: true})
			remaining = remaining[end+1:]
		default:
			return nil, fmt.Errorf("response assertion path %q is not a supported JSONPath", path)
		}
	}
	return parts, nil
}

func lookupAssertionPath(value any, path string) (any, error) {
	parts, err := parseAssertionPath(path)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		if part.array {
			array, ok := value.([]any)
			if !ok || part.index >= len(array) {
				return nil, fmt.Errorf("response field %q does not exist", path)
			}
			value = array[part.index]
			continue
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("response field %q does not exist", path)
		}
		value, ok = object[part.key]
		if !ok {
			return nil, fmt.Errorf("response field %q does not exist", path)
		}
	}
	return value, nil
}
