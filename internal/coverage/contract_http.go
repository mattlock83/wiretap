package coverage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/openapivalidation"
)

type contractHTTPPlan struct {
	operation *ContractTarget
	request   contractMessagePlan
	responses map[string]*contractMessagePlan
	targets   map[string][]*ContractTarget
}
type contractMessagePlan struct {
	alternative  *ContractTarget
	fields       []contractField
	bodies       map[string]*conditionNode
	media        map[string]*ContractTarget
	bodyPresence []*ContractTarget
	unsafe       bool
}
type contractField struct {
	name, in        string
	node            *conditionNode
	allowEmptyValue bool
}

func (b *contractBuilder) httpPlans(spec *openapiSpec) map[string]*contractHTTPPlan {
	plans := map[string]*contractHTTPPlan{}
	for _, path := range sortedMapKeys(spec.Doc.Paths.Map()) {
		item := spec.Doc.Paths.Value(path)
		for _, method := range sortedMapKeys(item.Operations()) {
			op := item.Operations()[method]
			name := strings.ToUpper(method) + " " + path
			b.operation = name
			b.phase = "operation"
			loc := "/paths/" + pointerToken(path) + "/" + strings.ToLower(method)
			p := &contractHTTPPlan{operation: b.add(loc, "operation", "exercised", nil), responses: map[string]*contractMessagePlan{}, targets: map[string][]*ContractTarget{}}
			plans[name] = p
			b.phase = "request"
			p.request.bodies = map[string]*conditionNode{}
			p.request.media = map[string]*ContractTarget{}
			// Operation parameters override path parameters by location and name.
			parameters := map[string]*openapi3.Parameter{}
			for _, list := range []openapi3.Parameters{item.Parameters, op.Parameters} {
				for _, ref := range list {
					if ref != nil && ref.Value != nil {
						v := ref.Value
						parameters[v.In+"/"+v.Name] = v
					}
				}
			}
			for _, key := range sortedMapKeys(parameters) {
				param := parameters[key]
				fieldLoc := loc + "/parameters/" + pointerToken(param.In) + "/" + pointerToken(param.Name)
				field := b.parameter(param.Name, param.In, param.Required, param.Schema, fieldLoc)
				field.allowEmptyValue = param.AllowEmptyValue
				if len(param.Content) > 0 || (param.Style != "" && param.Style != "form" && param.Style != "simple") || (param.In == "path" && param.Style == "form") {
					field.node.unsafe = true
					field.node.targets = append(field.node.targets, b.unsupported(fieldLoc, "Unsupported parameter serialization"))
				}
				p.request.fields = append(p.request.fields, field)
				p.request.unsafe = p.request.unsafe || field.node.unsafe
			}
			if op.RequestBody != nil && op.RequestBody.Value != nil {
				body := op.RequestBody.Value
				p.request.bodyPresence = append(p.request.bodyPresence, b.add(loc+"/requestBody", "presence", "present", present))
				if !body.Required {
					p.request.bodyPresence = append(p.request.bodyPresence, b.add(loc+"/requestBody", "presence", "absent", absent))
				}
				b.content(&p.request, body.Content, loc+"/requestBody/content")
			}
			security := spec.Doc.Security
			if op.Security != nil {
				security = *op.Security
			}
			if len(security) > 0 {
				b.unsupported(loc+"/security", "Authentication assertions cannot be inferred from captured traffic")
			}
			b.phase = "response"
			if op.Responses != nil {
				for _, status := range sortedMapKeys(op.Responses.Map()) {
					ref := op.Responses.Value(status)
					if ref == nil || ref.Value == nil {
						continue
					}
					response := ref.Value
					responseLoc := loc + "/responses/" + pointerToken(status)
					msg := &contractMessagePlan{alternative: b.add(responseLoc, "response_status", status, nil), bodies: map[string]*conditionNode{}, media: map[string]*ContractTarget{}}
					b.content(msg, response.Content, responseLoc+"/content")
					if strings.EqualFold(method, http.MethodHead) || status == "204" || status == "304" {
						for _, media := range sortedMapKeys(msg.media) {
							exclude(msg.media[media], "HTTP forbids response content for this method or status")
							if node := msg.bodies[media]; node != nil {
								node.walkTargets(func(t *ContractTarget) {
									exclude(t, "HTTP forbids response content for this method or status")
								})
							}
						}
					}
					for _, name := range sortedMapKeys(response.Headers) {
						ref := response.Headers[name]
						if ref == nil || ref.Value == nil || strings.EqualFold(name, "Content-Type") {
							continue
						}
						h := ref.Value
						f := b.parameter(name, "header", h.Required, h.Schema, responseLoc+"/headers/"+pointerToken(name))
						if len(h.Content) > 0 || (h.Style != "" && h.Style != "simple") {
							f.node.unsafe = true
							f.node.targets = append(f.node.targets, b.unsupported(responseLoc+"/headers/"+pointerToken(name), "Unsupported header serialization"))
						}
						msg.fields = append(msg.fields, f)
						msg.unsafe = msg.unsafe || f.node.unsafe
					}
					p.responses[status] = msg
				}
			}
		}
	}
	for _, t := range b.spec.Targets {
		if p := plans[t.Operation]; p != nil {
			p.targets[t.Phase] = append(p.targets[t.Phase], t)
		}
	}
	return plans
}
func (b *contractBuilder) parameter(name, in string, required bool, schema *openapi3.SchemaRef, loc string) contractField {
	n := b.node(schema, loc+"/schema", map[*openapi3.Schema]bool{})
	n.targets = append(n.targets, b.add(loc, "presence", "present", present))
	if !required {
		n.targets = append(n.targets, b.add(loc, "presence", "absent", absent))
	}
	if schema != nil && schema.Value != nil {
		s := schema.Value
		if s.Type == nil || len(s.Type.Slice()) != 1 || s.Type.Includes("array") || s.Type.Includes("object") {
			n.targets = append(n.targets, b.unsupported(loc, "Only scalar parameter and header measurement is supported"))
			n.unsafe = true
		}
	}
	return contractField{name: name, in: in, node: n}
}
func (b *contractBuilder) content(p *contractMessagePlan, content openapi3.Content, loc string) {
	for _, media := range sortedMapKeys(content) {
		m := content[media]
		at := loc + "/" + pointerToken(media)
		p.media[media] = b.add(at, "media_type", media, nil)
		if m == nil || m.Schema == nil {
			continue
		}
		if media != "application/json" && !strings.HasSuffix(media, "+json") {
			n := &conditionNode{targets: []*ContractTarget{b.unsupported(at, "Only JSON body condition measurement is supported")}, unsafe: true}
			p.bodies[media] = n
			continue
		}
		p.bodies[media] = b.node(m.Schema, at+"/schema", map[*openapi3.Schema]bool{})
	}
}
func messageBytes(msg *artifact.HttpMessage) []byte {
	if msg == nil || msg.Body == nil {
		return nil
	}
	if msg.Body.Bytes != "" {
		raw, _ := base64.StdEncoding.DecodeString(msg.Body.Bytes)
		return raw
	}
	return []byte(msg.Body.Text)
}
func contractHeaders(headers artifact.Headers) http.Header {
	h := http.Header{}
	for k, vs := range headers {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	return h
}
func contractRequest(msg *artifact.HttpMessage) (*http.Request, error) {
	if msg == nil {
		return nil, fmt.Errorf("missing HTTP request")
	}
	u, err := parseRequestURL(msg.URL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(msg.Method, u.String(), bytes.NewReader(messageBytes(msg)))
	if err == nil {
		req.Header = contractHeaders(msg.Headers)
	}
	return req, err
}
func (b *contractBuilder) measureHTTP(spec *openapiSpec, plans map[string]*contractHTTPPlan, a *artifact.Artifact, f artifact.Flow, e ContractEvidence) {
	req, err := contractRequest(f.Request.HTTP)
	if err != nil {
		b.violation("", e, "operation", err.Error())
		return
	}
	route, params, err := spec.Router.FindRoute(req)
	if err != nil {
		b.violation(req.Method+" "+req.URL.Path, e, "operation", "Request does not match a declared operation")
		return
	}
	name := strings.ToUpper(route.Method) + " " + route.Path
	p := plans[name]
	if p == nil {
		return
	}
	input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: &openapi3filter.Options{MultiError: true, SkipSettingDefaults: true, AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	results := map[string]string{}
	validations := map[string]string{}
	for _, phase := range []string{"request", "response"} {
		e.Phase = phase
		e.InstanceLocation = ""
		e.Reason = ""
		e.Validation = "unknown"
		msg := f.Request.HTTP
		mp := &p.request
		if phase == "response" {
			msg = f.Response.HTTP
			mp = nil
			if msg != nil {
				key := strconv.Itoa(msg.Status)
				mp = p.responses[key]
				if mp == nil {
					mp = p.responses[strconv.Itoa(msg.Status/100)+"XX"]
				}
				if mp == nil {
					mp = p.responses["default"]
				}
			}
		}
		unknown := ""
		if msg == nil {
			unknown = "HTTP message is missing"
		}
		if msg != nil && msg.Body != nil {
			switch {
			case msg.Body.Omitted:
				unknown = "Message body was omitted; original conditions cannot be reconstructed"
			case msg.Body.Redacted:
				unknown = "Message body was redacted; original conditions cannot be reconstructed"
			case msg.Body.Truncated:
				unknown = "Message body was truncated"
			}
		}
		mediaKnown := true
		// Older archives may record only rules without body availability flags.
		// Original conditions cannot safely be inferred from their changed data.
		for _, r := range a.Redactions {
			field := strings.ToLower(strings.TrimSpace(r.Field))
			if strings.HasPrefix(field, phase+".") {
				unknown = "Message has redaction rules; original conditions cannot be reconstructed"
			}
			if field == phase+".headers.content-type" {
				mediaKnown = false
			}
		}
		if mp != nil && mp.unsafe {
			unknown = "Message contains unsupported schema or parameter semantics"
		}
		media := ""
		var node *conditionNode
		bodyApplicable := phase != "response" || msg == nil || !contractResponseHasNoBody(req.Method, msg.Status)
		if msg != nil && mp != nil {
			media = selectedMedia(mp.media, contractHeaders(msg.Headers).Get("Content-Type"))
			node = mp.bodies[media]
			if bodyApplicable && node != nil && node.unsafe {
				unknown = "Selected body schema has unsupported measurement semantics"
			}
		}
		if msg != nil && mp != nil {
			for _, field := range mp.fields {
				raw, exists := fieldValue(field, req, params, msg, phase)
				if exists && raw == "" && (field.allowEmptyValue || phase == "response" && schemaAccepts(field.node.schema, nil, phase)) {
					unknown = "Empty scalar parameters are not validated as their transmitted value"
				}
				if exists && hasImpreciseNumber(scalarValue(raw, field.node.schema)) {
					unknown = "Parameter exceeds the validator's reliable numeric precision"
				}
				if field.in == "query" && len(req.URL.Query()[field.name]) > 1 {
					unknown = "Repeated scalar query parameters have no condition measurement"
				}
				if field.in == "header" && len(contractHeaders(msg.Headers).Values(field.name)) > 1 {
					unknown = "Repeated scalar headers have no condition measurement"
				}
			}
		}
		if unknown != "" {
			e.Result = "unknown"
			e.Reason = unknown
			observeUnknownMessage(p, mp, msg, media, mediaKnown, e)
			results[phase] = "unknown"
			validations[phase] = "unknown"
			continue
		}
		var validationErr error
		if phase == "request" {
			validationErr = openapivalidation.ValidateRequest(context.Background(), input)
		} else {
			validationErr = validateContractResponse(input, msg)
		}
		var value any
		hasBody := len(messageBytes(msg)) > 0
		if bodyApplicable && node != nil && hasBody {
			dec := json.NewDecoder(bytes.NewReader(messageBytes(msg)))
			dec.UseNumber()
			if err := dec.Decode(&value); err != nil {
				if validationErr == nil {
					validationErr = fmt.Errorf("Invalid JSON body")
				}
			} else if err = dec.Decode(new(any)); err != io.EOF {
				validationErr = fmt.Errorf("Invalid trailing JSON body data")
			}
			if hasImpreciseNumber(value) {
				e.Result = "unknown"
				e.Reason = "Numeric value exceeds the validator's reliable precision"
				observeUnknownMessage(p, mp, msg, media, mediaKnown, e)
				results[phase] = "unknown"
				validations[phase] = "unknown"
				continue
			}
		}
		e.Result = "observed_valid"
		e.Validation = "valid"
		if validationErr != nil {
			e.Result = "observed"
			e.Validation = "invalid"
			b.validationViolations(name, e, phase, mp, validationErr)
		} else if a.TestCase.Status == "pass" && !a.CaptureIncomplete && f.TransportError == "" {
			e.Result = "covered"
		}
		results[phase] = e.Result
		validations[phase] = e.Validation
		hits := []ConditionHit{}
		if mp != nil {
			if mp.alternative != nil {
				recordHit(mp.alternative, e, &hits)
			}
			if t := mp.media[media]; t != nil && hasBody {
				recordHit(t, e, &hits)
			}
			for _, t := range mp.bodyPresence {
				if t.match(nil, hasBody) {
					recordHit(t, e, &hits)
				}
			}
			for _, field := range mp.fields {
				raw, present := fieldValue(field, req, params, msg, phase)
				v := scalarValue(raw, field.node.schema)
				field.node.measure(v, present, "/"+field.in+"/"+pointerToken(field.name), e, &hits)
			}
			if bodyApplicable && node != nil && hasBody && value != nil {
				node.measure(value, true, "/body", e, &hits)
			} else if bodyApplicable && node != nil && hasBody && string(bytes.TrimSpace(messageBytes(msg))) == "null" {
				node.measure(nil, true, "/body", e, &hits)
			}
		}
		if len(hits) > 0 {
			sort.Slice(hits, func(i, j int) bool {
				return hits[i].Key+"|"+hits[i].InstanceLocation < hits[j].Key+"|"+hits[j].InstanceLocation
			})
			// Only condition keys and exact instance paths enter this fingerprint. It is
			// not a payload hash and does not assert equality of arbitrary field values.
			fingerprint := contractDigest(struct {
				Version, Operation, Phase string
				Hits                      []ConditionHit
			}{ContractRulesVersion, p.operation.OperationKey, phase, hits})
			ce := e
			if ce.Result == "observed_valid" {
				ce.Result = "observed"
			}
			b.spec.Combinations = append(b.spec.Combinations, ContractCombination{Fingerprint: fingerprint, OperationKey: p.operation.OperationKey, Phase: phase, Hits: hits, Evidence: ce})
		}
	}
	e.Phase = "operation"
	e.InstanceLocation = ""
	e.Result = "observed"
	e.Reason = ""
	e.Validation = "valid"
	if validations["request"] == "invalid" || validations["response"] == "invalid" {
		e.Validation = "invalid"
	} else if validations["request"] == "unknown" || validations["response"] == "unknown" {
		e.Validation = "unknown"
	}
	if results["request"] == "covered" && results["response"] == "covered" {
		e.Result = "covered"
	}
	if results["request"] == "unknown" || results["response"] == "unknown" {
		e.Result = "unknown"
		e.Reason = "Complete message validation is unavailable"
	}
	p.operation.observe(e)
}

// Do not attach uncertainty from one selected alternative to alternatives that
// the retained status and Content-Type prove were not selected.
func observeUnknownMessage(p *contractHTTPPlan, mp *contractMessagePlan, msg *artifact.HttpMessage, media string, mediaKnown bool, e ContractEvidence) {
	if msg == nil {
		for _, target := range p.targets[e.Phase] {
			target.observe(e)
		}
		return
	}
	if mp == nil {
		return
	}
	seen := map[string]bool{}
	observe := func(target *ContractTarget) {
		if target != nil && !seen[target.Key] {
			target.observe(e)
			seen[target.Key] = true
		}
	}
	observe(mp.alternative)
	for _, target := range mp.bodyPresence {
		observe(target)
	}
	for _, field := range mp.fields {
		field.node.walkTargets(observe)
	}
	for _, name := range sortedMapKeys(mp.media) {
		if mediaKnown && name != media {
			continue
		}
		observe(mp.media[name])
		if node := mp.bodies[name]; node != nil {
			node.walkTargets(observe)
		}
	}
}

// kin-openapi deliberately skips all validation for HEAD and some redirect/cache
// statuses. Coverage needs to validate the actual selected response definition
// before granting credit, so validate it through an otherwise identical local
// route with a non-skipped status. HEAD/204/304 omit a body, but still have a declared
// status and response headers to validate. The captured request/spec stay intact.
func contractResponseHasNoBody(method string, status int) bool {
	return method == http.MethodHead || status == http.StatusNoContent || status == http.StatusNotModified
}

// ValidateReplayResponse uses the same response validation as coverage,
// including header/status validation for HEAD and bodyless responses.
func ValidateReplayResponse(input *openapi3filter.RequestValidationInput, msg *artifact.HttpMessage) error {
	return validateContractResponse(input, msg)
}

func validateContractResponse(input *openapi3filter.RequestValidationInput, msg *artifact.HttpMessage) error {
	status := msg.Status
	noBody := contractResponseHasNoBody(input.Request.Method, status)
	opts := &openapi3filter.Options{MultiError: true, IncludeResponseStatus: true, SkipSettingDefaults: true}
	validationInput := input
	if noBody || status == http.StatusMovedPermanently || status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect {
		response := input.Route.Operation.Responses.Status(status)
		if response == nil {
			response = input.Route.Operation.Responses.Default()
		}
		if response == nil {
			return fmt.Errorf("Response status %d is not declared", status)
		}
		request := input.Request.Clone(context.Background())
		request.Method = http.MethodGet
		route := *input.Route
		operation := *route.Operation
		operation.Responses = openapi3.NewResponses(openapi3.WithStatus(http.StatusOK, response))
		route.Operation = &operation
		copyInput := *input
		copyInput.Request, copyInput.Route = request, &route
		validationInput = &copyInput
		status = http.StatusOK
		opts.ExcludeResponseBody = noBody
	}
	if noBody && len(messageBytes(msg)) > 0 {
		return fmt.Errorf("Response must not contain a body for HEAD or status 204/304")
	}
	return openapivalidation.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{RequestValidationInput: validationInput, Status: status, Header: contractHeaders(msg.Headers), Body: io.NopCloser(bytes.NewReader(messageBytes(msg))), Options: opts})
}
func selectedMedia(media map[string]*ContractTarget, raw string) string {
	mime := strings.TrimSpace(strings.SplitN(raw, ";", 2)[0])
	if media[mime] != nil {
		return mime
	}
	if i := strings.IndexByte(mime, '/'); i >= 0 {
		generic := mime[:i] + "/*"
		if media[generic] != nil {
			return generic
		}
	}
	if media["*/*"] != nil {
		return "*/*"
	}
	return ""
}
func fieldValue(field contractField, req *http.Request, params map[string]string, msg *artifact.HttpMessage, phase string) (string, bool) {
	switch field.in {
	case "path":
		v, ok := params[field.name]
		return v, ok
	case "query":
		vs, ok := req.URL.Query()[field.name]
		if len(vs) > 0 {
			return vs[0], ok
		}
		return "", ok
	case "header":
		vs := contractHeaders(msg.Headers).Values(field.name)
		if len(vs) > 0 {
			return vs[0], true
		}
	case "cookie":
		if phase == "request" {
			c, err := req.Cookie(field.name)
			if err == nil {
				return c.Value, true
			}
		}
	}
	return "", false
}
func scalarValue(raw string, s *openapi3.Schema) any {
	if s != nil && s.Type != nil {
		if s.Type.Is("integer") {
			bits := 64
			if s.Format == "int32" {
				bits = 32
			}
			if value, err := strconv.ParseInt(raw, 0, bits); err == nil {
				return json.Number(strconv.FormatInt(value, 10))
			}
		}
		if s.Type.Is("number") {
			if numberRat(json.Number(raw)) != nil {
				return json.Number(raw)
			}
		}
		if s.Type.Is("boolean") {
			if value, err := strconv.ParseBool(raw); err == nil {
				return value
			}
		}
	}
	return raw
}
func hasImpreciseNumber(v any) bool {
	switch x := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(x.String(), 64)
		if err != nil {
			return true
		}
		a, b := numberRat(x), numberRat(f)
		return a == nil || b == nil || a.Cmp(b) != 0
	case map[string]any:
		for _, v := range x {
			if hasImpreciseNumber(v) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if hasImpreciseNumber(v) {
				return true
			}
		}
	}
	return false
}
func (b *contractBuilder) violation(operation string, e ContractEvidence, phase, reason string) {
	e.Phase = phase
	e.Result = "violation"
	e.Validation = "invalid"
	b.spec.Violations = append(b.spec.Violations, ContractViolation{Operation: operation, Evidence: e, Reason: reason})
}

// Preserve individual schema failures as structured observations. The validator
// remains responsible for deciding message validity; this adds evidence paths.
func (b *contractBuilder) validationViolations(operation string, e ContractEvidence, phase string, mp *contractMessagePlan, err error) {
	var visit func(error, string)
	visit = func(err error, instance string) {
		switch v := err.(type) {
		case openapi3.MultiError:
			for _, child := range v {
				visit(child, instance)
			}
		case *openapi3filter.RequestError:
			if v.Parameter != nil {
				instance = "/" + v.Parameter.In + "/" + pointerToken(v.Parameter.Name)
			}
			if v.Err != nil {
				visit(v.Err, instance)
			} else {
				evidence := e
				evidence.InstanceLocation = instance
				b.violation(operation, evidence, phase, v.Error())
			}
		case *openapi3filter.ResponseError:
			// The validator exposes the header's name in its response error
			// reason, but provides no structured equivalent of RequestError.Parameter.
			if strings.HasPrefix(v.Reason, "response header ") || strings.HasPrefix(v.Reason, "unable to decode header ") || strings.HasPrefix(v.Reason, "unable to get header ") {
				if at := strings.IndexByte(v.Reason, '"'); at >= 0 {
					if quoted, err := strconv.QuotedPrefix(v.Reason[at:]); err == nil {
						if name, err := strconv.Unquote(quoted); err == nil {
							instance = "/header/" + pointerToken(name)
						}
					}
				}
			}
			if v.Err != nil {
				visit(v.Err, instance)
			} else {
				evidence := e
				evidence.InstanceLocation = instance
				b.violation(operation, evidence, phase, v.Error())
			}
		case *openapi3.SchemaError:
			evidence := e
			evidence.Phase = phase
			evidence.Result = "violation"
			evidence.InstanceLocation = instance
			for _, part := range v.JSONPointer() {
				evidence.InstanceLocation += "/" + pointerToken(part)
			}
			location := ""
			var find func(*conditionNode)
			find = func(n *conditionNode) {
				if n == nil || location != "" {
					return
				}
				if n.schema == v.Schema {
					location = n.location
					return
				}
				for _, name := range sortedMapKeys(n.properties) {
					find(n.properties[name])
				}
				find(n.items)
				for _, name := range []string{"oneOf", "anyOf", "allOf"} {
					for _, child := range n.branches[name] {
						find(child)
					}
				}
			}
			if mp != nil {
				for _, f := range mp.fields {
					if instance == "/"+f.in+"/"+pointerToken(f.name) {
						find(f.node)
					}
				}
				if instance == "/body" {
					for _, name := range sortedMapKeys(mp.bodies) {
						find(mp.bodies[name])
					}
				}
			}
			b.spec.Violations = append(b.spec.Violations, ContractViolation{Operation: operation, Evidence: evidence, Constraint: v.SchemaField, SchemaLocation: location, Reason: v.Reason})
		default:
			evidence := e
			evidence.InstanceLocation = instance
			b.violation(operation, evidence, phase, err.Error())
		}
	}
	visit(err, "/body")
}
