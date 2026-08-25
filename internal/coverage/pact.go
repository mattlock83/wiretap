package coverage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/phrond/evermock/internal/artifact"
)

const pactSpecificationVersion = "3.0.0"

// PactOptions configures stable consumer and provider participant names.
type PactOptions struct {
	ConsumerName  string
	ProviderNames map[string]string
}

// PactExport contains one standard Pact V3 document per consumer/provider.
type PactExport struct {
	ConsumerName    string
	ConsumerVersion string
	Branch          string
	Contracts       []Pact
}

// Pact is a standard HTTP Pact Specification V3 contract.
type Pact struct {
	Consumer     PactParticipant   `json:"consumer"`
	Provider     PactParticipant   `json:"provider"`
	Interactions []PactInteraction `json:"interactions"`
	Metadata     PactMetadata      `json:"metadata"`
}

type PactParticipant struct {
	Name string `json:"name"`
}

type PactMetadata struct {
	PactSpecification PactSpecification `json:"pactSpecification"`
}

type PactSpecification struct {
	Version string `json:"version"`
}

type PactInteraction struct {
	Description    string                   `json:"description"`
	ProviderStates []artifact.ProviderState `json:"providerStates,omitempty"`
	Request        PactRequest              `json:"request"`
	Response       PactResponse             `json:"response"`
}

type PactRequest struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query,omitempty"`
	Headers map[string]string   `json:"headers,omitempty"`
	Body    any                 `json:"body,omitempty"`
}

type PactResponse struct {
	Status        int                `json:"status"`
	Headers       map[string]string  `json:"headers,omitempty"`
	Body          any                `json:"body,omitempty"`
	MatchingRules *PactMatchingRules `json:"matchingRules,omitempty"`
}

type PactMatchingRules struct {
	Body map[string]PactMatcherGroup `json:"body,omitempty"`
}

type PactMatcherGroup struct {
	Matchers []PactMatcher `json:"matchers"`
}

type PactMatcher struct {
	Match  string `json:"match"`
	Regex  string `json:"regex,omitempty"`
	Format string `json:"format,omitempty"`
}

// PactsFromReport translates only successful SUT-to-dependency captures into
// stable, deduplicated consumer-driven Pact V3 contracts.
func PactsFromReport(report *Report, options PactOptions) (*PactExport, error) {
	if report == nil {
		return nil, errors.New("report is nil")
	}
	if len(report.artifactEntries) == 0 {
		return nil, errors.New("pact export requires a report generated from capture artifacts")
	}

	producer, err := pactProducer(report.artifactEntries)
	if err != nil {
		return nil, err
	}
	consumer := strings.TrimSpace(options.ConsumerName)
	if consumer == "" && report.explicitSUTName {
		consumer = strings.TrimSpace(report.SUTName)
	}
	if consumer == "" && producer != nil {
		consumer = path.Base(strings.TrimSpace(producer.Repo))
		if consumer == "." || consumer == "/" {
			consumer = ""
		}
	}
	if consumer == "" {
		consumer = strings.TrimSpace(report.SUTName)
	}
	if consumer == "" {
		return nil, errors.New("pact export requires a consumer name")
	}
	result := &PactExport{ConsumerName: consumer}
	if producer != nil {
		result.ConsumerVersion = producer.CommitSHA
		result.Branch = producer.Branch
	}

	providerNames := map[string]string{}
	for assetID, name := range options.ProviderNames {
		assetID = strings.TrimSpace(assetID)
		name = strings.TrimSpace(name)
		if assetID == "" || name == "" {
			return nil, errors.New("Pact provider overrides require non-empty asset IDs and participant names")
		}
		providerNames[assetID] = name
	}

	type candidate struct {
		interaction PactInteraction
		identity    string
	}
	byProvider := map[string]map[string]candidate{}
	for _, entry := range report.artifactEntries {
		doc := entry.Doc
		if doc == nil || strings.EqualFold(doc.TestCase.Status, "fail") {
			continue
		}
		for _, flow := range doc.Traffic {
			if flow.Channel != "sut_to_dependency" || !strings.EqualFold(flow.Protocol, "http") ||
				flow.TransportError != "" || flow.Request.HTTP == nil || flow.Response.HTTP == nil {
				continue
			}
			assetID := strings.TrimSpace(flow.AssetID)
			if assetID == "" {
				return nil, fmt.Errorf("dependency flow %q has no stable asset_id", flow.ID)
			}
			provider := providerNames[assetID]
			if provider == "" {
				provider = assetID
			}
			interaction, err := pactInteraction(doc, flow)
			if err != nil {
				return nil, fmt.Errorf("dependency %q, test %q, flow %q: %w", assetID, doc.TestCase.Name, flow.ID, err)
			}
			identity, err := pactInteractionIdentity(interaction)
			if err != nil {
				return nil, err
			}
			if byProvider[provider] == nil {
				byProvider[provider] = map[string]candidate{}
			}
			if existing, found := byProvider[provider][identity]; found {
				if interaction.Description < existing.interaction.Description {
					byProvider[provider][identity] = candidate{interaction: interaction, identity: identity}
				}
				continue
			}
			byProvider[provider][identity] = candidate{interaction: interaction, identity: identity}
		}
	}

	providers := make([]string, 0, len(byProvider))
	for provider := range byProvider {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		candidates := make([]candidate, 0, len(byProvider[provider]))
		for _, item := range byProvider[provider] {
			candidates = append(candidates, item)
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].interaction.Description != candidates[j].interaction.Description {
				return candidates[i].interaction.Description < candidates[j].interaction.Description
			}
			return candidates[i].identity < candidates[j].identity
		})

		seenDescriptions := map[string]bool{}
		interactions := make([]PactInteraction, 0, len(candidates))
		for _, item := range candidates {
			interaction := item.interaction
			key, err := pactDescriptionKey(interaction)
			if err != nil {
				return nil, err
			}
			if seenDescriptions[key] {
				interaction.Description += " [" + item.identity[:12] + "]"
			}
			seenDescriptions[key] = true
			interactions = append(interactions, interaction)
		}
		result.Contracts = append(result.Contracts, Pact{
			Consumer: PactParticipant{Name: consumer}, Provider: PactParticipant{Name: provider},
			Interactions: interactions,
			Metadata:     PactMetadata{PactSpecification: PactSpecification{Version: pactSpecificationVersion}},
		})
	}
	return result, nil
}

// WritePact writes deterministic, standards-compliant Pact JSON.
func WritePact(w io.Writer, contract Pact) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(contract)
}

// WritePactDirectory writes one contract per consumer/provider pair.
func WritePactDirectory(report *Report, directory string, options PactOptions) (*PactExport, []string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, nil, errors.New("pact output directory is required")
	}
	export, err := PactsFromReport(report, options)
	if err != nil || len(export.Contracts) == 0 {
		return export, nil, err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, nil, err
	}
	var files []string
	usedNames := map[string]bool{}
	for _, contract := range export.Contracts {
		base := pactFileParticipant(contract.Consumer.Name) + "-" + pactFileParticipant(contract.Provider.Name)
		if usedNames[base] {
			sum := sha256.Sum256([]byte(contract.Provider.Name))
			base += "-" + hex.EncodeToString(sum[:])[:8]
		}
		usedNames[base] = true
		filePath := filepath.Join(directory, base+".json")
		file, err := os.Create(filePath)
		if err != nil {
			return nil, nil, err
		}
		writeErr := WritePact(file, contract)
		closeErr := file.Close()
		if writeErr != nil {
			return nil, nil, writeErr
		}
		if closeErr != nil {
			return nil, nil, closeErr
		}
		files = append(files, filePath)
	}
	return export, files, nil
}

func pactProducer(entries []artifactEntry) (*artifact.Producer, error) {
	var producer *artifact.Producer
	for _, entry := range entries {
		if entry.Doc == nil || entry.Doc.Producer == nil || strings.EqualFold(entry.Doc.TestCase.Status, "fail") {
			continue
		}
		current := entry.Doc.Producer
		if producer == nil {
			copy := *current
			producer = &copy
			continue
		}
		if producer.Repo != "" && current.Repo != "" && producer.Repo != current.Repo {
			return nil, fmt.Errorf("Pact artifacts contain multiple consumer repositories: %q and %q", producer.Repo, current.Repo)
		}
		if producer.CommitSHA != "" && current.CommitSHA != "" && producer.CommitSHA != current.CommitSHA {
			return nil, fmt.Errorf("Pact artifacts contain multiple consumer commit versions: %q and %q", producer.CommitSHA, current.CommitSHA)
		}
		if producer.Repo == "" {
			producer.Repo = current.Repo
		}
		if producer.CommitSHA == "" {
			producer.CommitSHA = current.CommitSHA
		}
		if producer.Branch == "" {
			producer.Branch = current.Branch
		}
	}
	return producer, nil
}

func pactInteraction(doc *artifact.Artifact, flow artifact.Flow) (PactInteraction, error) {
	request := flow.Request.HTTP
	response := flow.Response.HTTP
	parsed, err := url.Parse(request.URL)
	if err != nil || parsed.Path == "" {
		return PactInteraction{}, fmt.Errorf("invalid captured dependency request URL %q", request.URL)
	}
	requestBody, hasRequestBody, err := pactBody(request)
	if err != nil {
		return PactInteraction{}, fmt.Errorf("request body: %w", err)
	}
	responseBody, hasResponseBody, err := pactBody(response)
	if err != nil {
		return PactInteraction{}, fmt.Errorf("response body: %w", err)
	}

	interaction := PactInteraction{
		Description:    pactInteractionDescription(doc.TestCase.Name, request.Method, parsed.Path, response.Status),
		ProviderStates: append([]artifact.ProviderState(nil), flow.ProviderStates...),
		Request: PactRequest{
			Method: strings.ToUpper(request.Method), Path: parsed.Path,
			Query: pactQuery(parsed.Query()), Headers: pactHeaders(request.Headers),
		},
		Response: PactResponse{Status: response.Status, Headers: pactHeaders(response.Headers)},
	}
	if hasRequestBody {
		interaction.Request.Body = requestBody
	}
	if hasResponseBody {
		interaction.Response.Body = responseBody
	}

	assertions := pactFlowAssertions(doc, flow.ID)
	matchers := map[string]PactMatcherGroup{}
	if len(assertions) > 0 {
		if !hasResponseBody {
			return PactInteraction{}, errors.New("selected response assertions require a captured response body")
		}
		var selected any
		for _, assertion := range assertions {
			parts, err := parsePactPath(assertion.Field)
			if err != nil {
				return PactInteraction{}, err
			}
			selected, err = projectPactPath(responseBody, selected, parts)
			if err != nil {
				return PactInteraction{}, fmt.Errorf("response assertion %q: %w", assertion.Field, err)
			}
			matchers[assertion.Field] = PactMatcherGroup{Matchers: []PactMatcher{{Match: "equality"}}}
		}
		interaction.Response.Body = selected
	} else if hasResponseBody {
		if schema := pactResponseSchema(doc, flow, parsed); schema != nil {
			addPactSchemaMatchers(matchers, "$", responseBody, schema)
		}
	}
	if len(matchers) > 0 {
		interaction.Response.MatchingRules = &PactMatchingRules{Body: matchers}
	}
	return interaction, nil
}

func pactFlowAssertions(doc *artifact.Artifact, flowID string) []artifact.Assertion {
	var assertions []artifact.Assertion
	for _, assertion := range doc.Assertions {
		if assertion.FlowID == flowID && assertion.Channel == "sut_to_dependency" &&
			assertion.Phase == "response" && assertion.PathLang == "jsonpath" && assertion.Result == "pass" {
			assertions = append(assertions, assertion)
		}
	}
	sort.SliceStable(assertions, func(i, j int) bool { return assertions[i].Field < assertions[j].Field })
	return assertions
}

func pactBody(message *artifact.HttpMessage) (any, bool, error) {
	if message == nil || message.Body == nil {
		return nil, false, nil
	}
	body := message.Body
	if body.Truncated {
		return nil, false, errors.New("captured body is truncated; increase PHROND_MAX_CAPTURE_BYTES before exporting Pact")
	}
	if body.Encoding == "base64" {
		return nil, false, errors.New("binary bodies cannot be represented safely by the HTTP Pact exporter")
	}
	if body.Text == "" {
		return nil, false, nil
	}
	contentType := strings.ToLower(pactHeaderValue(message.Headers, "Content-Type"))
	isJSON := strings.Contains(contentType, "/json") || strings.Contains(contentType, "+json")
	if isJSON || (contentType == "" && json.Valid([]byte(body.Text))) {
		var value any
		if err := json.Unmarshal([]byte(body.Text), &value); err != nil {
			return nil, false, fmt.Errorf("invalid JSON: %w", err)
		}
		return value, true, nil
	}
	return body.Text, true, nil
}

func pactHeaderValue(headers artifact.Headers, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func pactHeaders(headers artifact.Headers) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := map[string]string{}
	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(name)
		switch strings.ToLower(canonical) {
		case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key",
			"connection", "keep-alive", "proxy-authenticate", "te", "trailer",
			"transfer-encoding", "upgrade", "host", "content-length", "content-encoding",
			"date", "server", "user-agent", "accept-encoding", "traceparent", "tracestate",
			"baggage", "x-request-id", "x-correlation-id":
			continue
		}
		if strings.HasPrefix(strings.ToLower(canonical), "x-phrond-") || len(values) == 0 {
			continue
		}
		out[canonical] = strings.Join(values, ", ")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func pactQuery(values url.Values) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string][]string{}
	for key, selected := range values {
		switch strings.ToLower(strings.ReplaceAll(key, "-", "_")) {
		case "access_token", "api_key", "apikey", "password", "client_secret", "signature":
			continue
		}
		out[key] = append([]string(nil), selected...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func pactResponseSchema(doc *artifact.Artifact, flow artifact.Flow, parsed *url.URL) *openapi3.Schema {
	for _, ref := range doc.Schemas {
		if ref.Role != "dependency" || ref.AssetID != flow.AssetID || !strings.EqualFold(ref.Type, "openapi") {
			continue
		}
		spec, err := loadOpenAPISpec(ref)
		if err != nil {
			continue
		}
		request, err := http.NewRequest(flow.Request.HTTP.Method, parsed.String(), nil)
		if err != nil {
			continue
		}
		route, _, err := spec.Router.FindRoute(request)
		if err != nil || route.Operation == nil || route.Operation.Responses == nil {
			continue
		}
		response := route.Operation.Responses.Status(flow.Response.HTTP.Status)
		if response == nil {
			response = route.Operation.Responses.Default()
		}
		if response == nil || response.Value == nil {
			continue
		}
		contentType := pactHeaderValue(flow.Response.HTTP.Headers, "Content-Type")
		media := response.Value.Content.Get(contentType)
		if media == nil && contentType == "" {
			media = response.Value.Content.Get("application/json")
		}
		if media != nil && media.Schema != nil {
			return media.Schema.Value
		}
	}
	return nil
}

func addPactSchemaMatchers(target map[string]PactMatcherGroup, current string, value any, schema *openapi3.Schema) {
	if schema == nil || value == nil {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			ref := schema.Properties[name]
			if ref != nil {
				addPactSchemaMatchers(target, current+"."+name, child, ref.Value)
			}
		}
	case []any:
		if schema.Items == nil {
			return
		}
		for index, child := range typed {
			addPactSchemaMatchers(target, current+"["+strconv.Itoa(index)+"]", child, schema.Items.Value)
		}
	default:
		matcher, ok := pactMatcherForSchema(schema)
		if ok {
			target[current] = PactMatcherGroup{Matchers: []PactMatcher{matcher}}
		}
	}
}

func pactMatcherForSchema(schema *openapi3.Schema) (PactMatcher, bool) {
	if schema == nil || schema.Type == nil {
		return PactMatcher{}, false
	}
	switch {
	case schema.Type.Is("boolean"):
		return PactMatcher{Match: "boolean"}, true
	case schema.Type.Is("integer"):
		return PactMatcher{Match: "integer"}, true
	case schema.Type.Is("number"):
		return PactMatcher{Match: "number"}, true
	case schema.Type.Is("string"):
		if schema.Pattern != "" {
			return PactMatcher{Match: "regex", Regex: schema.Pattern}, true
		}
		switch strings.ToLower(schema.Format) {
		case "uuid":
			return PactMatcher{Match: "regex", Regex: `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`}, true
		case "date":
			return PactMatcher{Match: "date", Format: "yyyy-MM-dd"}, true
		case "date-time":
			return PactMatcher{Match: "regex", Regex: `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$`}, true
		case "time":
			return PactMatcher{Match: "regex", Regex: `^\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?$`}, true
		default:
			return PactMatcher{Match: "type"}, true
		}
	default:
		return PactMatcher{}, false
	}
}

type pactPathPart struct {
	key   string
	index int
	array bool
}

func parsePactPath(value string) ([]pactPathPart, error) {
	if !strings.HasPrefix(value, "$") || len(value) == 1 {
		return nil, fmt.Errorf("unsupported Pact response field path %q", value)
	}
	var parts []pactPathPart
	for remaining := value[1:]; remaining != ""; {
		switch remaining[0] {
		case '.':
			remaining = remaining[1:]
			end := strings.IndexAny(remaining, ".[ ")
			if end == -1 {
				end = len(remaining)
			}
			if end == 0 {
				return nil, fmt.Errorf("invalid Pact response field path %q", value)
			}
			parts = append(parts, pactPathPart{key: remaining[:end]})
			remaining = remaining[end:]
		case '[':
			end := strings.IndexByte(remaining, ']')
			if end < 2 {
				return nil, fmt.Errorf("invalid Pact response field path %q", value)
			}
			index, err := strconv.Atoi(remaining[1:end])
			if err != nil || index < 0 {
				return nil, fmt.Errorf("invalid Pact response field path %q", value)
			}
			parts = append(parts, pactPathPart{index: index, array: true})
			remaining = remaining[end+1:]
		default:
			return nil, fmt.Errorf("unsupported Pact response field path %q", value)
		}
	}
	return parts, nil
}

func projectPactPath(source, selected any, parts []pactPathPart) (any, error) {
	if len(parts) == 0 {
		return source, nil
	}
	part := parts[0]
	if part.array {
		array, ok := source.([]any)
		if !ok || part.index >= len(array) {
			return nil, errors.New("selected array element does not exist")
		}
		out, _ := selected.([]any)
		for len(out) <= part.index {
			out = append(out, array[len(out)])
		}
		var existing any
		if previous, ok := selected.([]any); ok && part.index < len(previous) {
			existing = previous[part.index]
		}
		child, err := projectPactPath(array[part.index], existing, parts[1:])
		if err != nil {
			return nil, err
		}
		out[part.index] = child
		return out, nil
	}
	object, ok := source.(map[string]any)
	if !ok {
		return nil, errors.New("selected response value is not an object")
	}
	value, ok := object[part.key]
	if !ok {
		return nil, errors.New("selected response property does not exist")
	}
	out, _ := selected.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	child, err := projectPactPath(value, out[part.key], parts[1:])
	if err != nil {
		return nil, err
	}
	out[part.key] = child
	return out, nil
}

func pactInteractionDescription(testName, method, requestPath string, status int) string {
	testName = strings.TrimSpace(testName)
	if testName == "" {
		testName = "captured dependency interaction"
	}
	return fmt.Sprintf("%s - %s %s (%d)", testName, strings.ToUpper(method), requestPath, status)
}

func pactInteractionIdentity(interaction PactInteraction) (string, error) {
	interaction.Description = ""
	data, err := json.Marshal(interaction)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func pactDescriptionKey(interaction PactInteraction) (string, error) {
	states, err := json.Marshal(interaction.ProviderStates)
	if err != nil {
		return "", err
	}
	return interaction.Description + "|" + string(states), nil
}

func pactFileParticipant(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, strings.TrimSpace(value))
	value = strings.Trim(value, "._")
	if value == "" {
		return "participant"
	}
	return value
}
