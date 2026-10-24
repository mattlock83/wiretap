package assumptions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/mattlock83/wiretap/internal/artifact"
)

type contractCheckProblem struct {
	kind, code, message string
	differences         []string
}

func verifyHTTPContracts(ctx context.Context, verifier *httpContractVerifier, consumer, provider sample) (string, *contractCheckProblem) {
	unavailable := func(reason string) (string, *contractCheckProblem) {
		return "", &contractCheckProblem{"unverified", "http_contract_unavailable", reason, []string{"contract"}}
	}
	if consumer.schemaProblem != "" {
		return unavailable("consumer " + consumer.schemaProblem)
	}
	if provider.schemaProblem != "" {
		return unavailable("provider " + provider.schemaProblem)
	}
	available := func(ref *artifact.SchemaRef) bool { return ref != nil && ref.Source.Type != "omitted" }
	if !available(consumer.schema) && !available(provider.schema) {
		return "unavailable", nil
	}
	if !available(consumer.schema) || !available(provider.schema) {
		return unavailable("both consumer and provider frozen OpenAPI contracts are required when either side declares one; cannot compare validation rules")
	}
	var rules [2]string
	for i, entry := range []struct {
		role string
		item sample
	}{{"consumer", consumer}, {"provider", provider}} {
		contract, err := verifier.load(ctx, entry.item.schema)
		if err != nil {
			return unavailable(entry.role + " OpenAPI contract: " + err.Error())
		}
		if err := contract.validate(ctx, entry.item.flow); err != nil {
			if errors.Is(err, errHTTPContractUnavailable) {
				return unavailable(entry.role + " OpenAPI validation: " + err.Error())
			}
			return "", &contractCheckProblem{"mismatch", "http_contract_violation", entry.role + " captured exchange violates its frozen OpenAPI contract: " + err.Error(), []string{"contract/validation"}}
		}
		rules[i], err = contract.rules(entry.item.flow)
		if err != nil {
			return unavailable(entry.role + " OpenAPI rules: " + err.Error())
		}
	}
	if rules[0] != rules[1] {
		return "", &contractCheckProblem{"mismatch", "http_contract_mismatch", "consumer and provider OpenAPI validation rules differ for this exercised operation; rule drift is an error even when the captured values satisfy both contracts", jsonDifferencePaths(rules[0], rules[1], "contract")}
	}
	return "openapi_3_0_validation_rules", nil
}

// Compare the validation rules of the exercised operation, not a document hash.
// This is deliberately conservative equality, not a schema-subtyping proof.
// An enum addition or relaxed limit is still drift even if this example is valid.
func (c *httpContract) rules(flow artifact.Flow) (string, error) {
	input, err := c.input(flow)
	if err != nil {
		return "", err
	}
	p := ruleProjector{active: map[*openapi3.Schema]string{}}
	parameters := map[string]any{}
	parameterNames := map[string]string{}
	for _, list := range []openapi3.Parameters{input.Route.PathItem.Parameters, input.Route.Operation.Parameters} {
		for _, ref := range list {
			if ref == nil || ref.Value == nil {
				return "", fmt.Errorf("unresolved request parameter")
			}
			param := ref.Value
			name := param.Name
			if param.In == "header" {
				name = strings.ToLower(name)
			}
			key := param.In + "/" + name
			if previous, exists := parameterNames[key]; exists && previous != param.Name {
				return "", fmt.Errorf("ambiguous case variants of request header %q", param.Name)
			}
			parameterNames[key] = param.Name
			value, err := p.parameter(param, "contract/request/parameters/"+rulePointer(key))
			if err != nil {
				return "", err
			}
			parameters[key] = value
		}
	}
	request := map[string]any{"parameters": parameters}
	if ref := input.Route.Operation.RequestBody; ref != nil {
		if ref.Value == nil {
			return "", fmt.Errorf("unresolved request body")
		}
		body, err := p.content(ref.Value.Content, httpHeader(flow.Request.HTTP.Headers, "Content-Type"), "contract/request/body")
		if err != nil {
			return "", err
		}
		request["body"] = map[string]any{"required": ref.Value.Required, "content": body}
	}
	response := map[string]any{}
	responses := input.Route.Operation.Responses
	if responses == nil {
		return "", fmt.Errorf("operation has no declared responses")
	}
	ref := responses.Status(flow.Response.HTTP.Status)
	if ref == nil {
		ref = responses.Default()
	}
	if ref == nil || ref.Value == nil {
		return "", fmt.Errorf("response status %d is not declared", flow.Response.HTTP.Status)
	}
	headers := map[string]any{}
	for _, name := range sortedRuleKeys(ref.Value.Headers) {
		header := ref.Value.Headers[name]
		if header == nil || header.Value == nil {
			return "", fmt.Errorf("unresolved response header %q", name)
		}
		param := header.Value.Parameter
		param.In, param.Name = "header", strings.ToLower(name)
		if _, exists := headers[param.Name]; exists {
			return "", fmt.Errorf("ambiguous case variants of response header %q", name)
		}
		value, err := p.parameter(&param, "contract/response/headers/"+rulePointer(param.Name))
		if err != nil {
			return "", err
		}
		headers[param.Name] = value
	}
	response["headers"] = headers
	body, err := p.content(ref.Value.Content, httpHeader(flow.Response.HTTP.Headers, "Content-Type"), "contract/response/body")
	if err != nil {
		return "", err
	}
	response["body"] = body
	data, err := json.Marshal(map[string]any{"request": request, "response": response})
	return string(data), err
}

func httpHeader(headers artifact.Headers, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

type ruleProjector struct {
	active map[*openapi3.Schema]string
	nodes  int
}

func (p *ruleProjector) parameter(param *openapi3.Parameter, path string) (any, error) {
	method, err := param.SerializationMethod()
	if err != nil {
		return nil, err
	}
	schema, err := p.schema(param.Schema, path+"/schema", 0)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"required": param.Required, "style": method.Style, "explode": method.Explode,
		"allowEmptyValue": param.AllowEmptyValue, "allowReserved": param.AllowReserved, "schema": schema}
	if len(param.Content) > 0 {
		content := map[string]any{}
		for _, media := range sortedRuleKeys(param.Content) {
			value, err := p.media(param.Content[media], path+"/content/"+rulePointer(media))
			if err != nil {
				return nil, err
			}
			content[media] = value
		}
		result["content"] = content
	}
	return result, nil
}

func (p *ruleProjector) content(content openapi3.Content, mime, path string) (any, error) {
	if len(content) == 0 {
		return nil, nil
	}
	media := content.Get(mime)
	if media == nil {
		// There is no selected body schema. Value validation decides whether
		// an absent/undeclared body is legitimate for this captured exchange.
		return map[string]any{"selected": false}, nil
	}
	return p.media(media, path+"/schema")
}

func (p *ruleProjector) media(media *openapi3.MediaType, path string) (any, error) {
	if media == nil {
		return nil, fmt.Errorf("unresolved media definition")
	}
	if len(media.Encoding) > 0 {
		return nil, fmt.Errorf("media encoding rules cannot be compared reliably")
	}
	return p.schema(media.Schema, path, 0)
}

func (p *ruleProjector) schema(ref *openapi3.SchemaRef, path string, depth int) (any, error) {
	if ref == nil {
		return nil, nil
	}
	s := ref.Value
	if s == nil {
		return nil, fmt.Errorf("unresolved schema at %s", path)
	}
	if ancestor, found := p.active[s]; found {
		return map[string]any{"recursive_ref": ancestor}, nil
	}
	p.nodes++
	if depth > 128 || p.nodes > 50000 {
		return nil, fmt.Errorf("validation-rule projection exceeds its nesting or node limit")
	}
	p.active[s] = path
	defer delete(p.active, s)
	value, err := s.MarshalYAML()
	if err != nil {
		return nil, err
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unsupported schema representation at %s", path)
	}
	result := map[string]any{}
	// These are the OpenAPI 3.0 Schema Object validation keywords. All other
	// fields are annotations; references and nested rules are resolved below.
	for _, key := range []string{"format", "nullable", "readOnly", "writeOnly", "allowEmptyValue", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties"} {
		if v, exists := raw[key]; exists {
			if flag, ok := v.(bool); ok && !flag {
				continue
			}
			result[key] = v
		}
	}
	if s.Type != nil && len(s.Type.Slice()) > 0 {
		types := append([]string{}, s.Type.Slice()...)
		sort.Strings(types)
		result["type"] = unique(types)
	}
	if len(s.Enum) > 0 {
		values := []string{}
		for _, v := range s.Enum {
			data, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			value, err := normalizeJSONValue(decoder, 0)
			if err != nil {
				return nil, err
			}
			canonical, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			values = append(values, string(canonical))
		}
		sort.Strings(values)
		result["enum"] = unique(values)
	}
	if len(s.Required) > 0 {
		required := append([]string{}, s.Required...)
		sort.Strings(required)
		result["required"] = unique(required)
	}
	if len(s.Properties) > 0 {
		properties := map[string]any{}
		for _, name := range sortedRuleKeys(s.Properties) {
			value, err := p.schema(s.Properties[name], path+"/properties/"+rulePointer(name), depth+1)
			if err != nil {
				return nil, err
			}
			properties[name] = value
		}
		result["properties"] = properties
	}
	if s.AdditionalProperties.Schema != nil {
		value, err := p.schema(s.AdditionalProperties.Schema, path+"/additionalProperties", depth+1)
		if err != nil {
			return nil, err
		}
		result["additionalProperties"] = value
	} else if s.AdditionalProperties.Has != nil && !*s.AdditionalProperties.Has {
		result["additionalProperties"] = false
	}
	for _, item := range []struct {
		name string
		ref  *openapi3.SchemaRef
	}{{"items", s.Items}, {"not", s.Not}} {
		if item.ref != nil {
			value, err := p.schema(item.ref, path+"/"+item.name, depth+1)
			if err != nil {
				return nil, err
			}
			result[item.name] = value
		}
	}
	for _, list := range []struct {
		name string
		refs openapi3.SchemaRefs
	}{{"allOf", s.AllOf}, {"anyOf", s.AnyOf}, {"oneOf", s.OneOf}} {
		if len(list.refs) > 0 {
			values := []any{}
			for i, ref := range list.refs {
				value, err := p.schema(ref, fmt.Sprintf("%s/%s/%d", path, list.name, i), depth+1)
				if err != nil {
					return nil, err
				}
				values = append(values, value)
			}
			result[list.name] = values
		}
	}
	if s.Discriminator != nil {
		result["discriminator"] = s.Discriminator
	}
	return result, nil
}

func rulePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func sortedRuleKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
