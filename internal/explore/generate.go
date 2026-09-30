package explore

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/phrond/wiretap/internal/artifact"
)

type mutation struct {
	Target   artifact.RejectionTarget
	Step     int
	In, Path string
	Value    any
	Remove   bool
}

func (m mutation) apply(r *request, s *spec) error {
	in, err := s.input(*r)
	if err != nil {
		return err
	}
	return setValue(r, m.In, m.Path, m.Value, m.Remove, in)
}

// exactViolation ensures we attribute a rejection only to the intended
// constraint. Compound failures and parse failures we cannot classify are not
// used as independent coverage targets.
func exactViolation(err error, location, constraint string) bool {
	if err == nil {
		return false
	}
	var walk func(error, string) bool
	walk = func(e error, prefix string) bool {
		switch x := e.(type) {
		case openapi3.MultiError:
			if len(x) == 0 {
				return false
			}
			for _, child := range x {
				if !walk(child, prefix) {
					return false
				}
			}
			return true
		case *openapi3filter.RequestError:
			prefix = "/body"
			if x.Parameter != nil {
				prefix = "/" + x.Parameter.In + "/" + token(x.Parameter.Name)
			}
			if errors.Is(x.Err, openapi3filter.ErrInvalidRequired) {
				return location == prefix && constraint == "required"
			}
			var parse *openapi3filter.ParseError
			if errors.As(x.Err, &parse) {
				return x.Parameter != nil && location == prefix && constraint == "type"
			}
			return walk(x.Err, prefix)
		case *openapi3.SchemaError:
			path := prefix
			for _, p := range x.JSONPointer() {
				path += "/" + token(p)
			}
			return x.Origin == nil && path == location && x.SchemaField == constraint
		default:
			return false
		}
	}
	return walk(err, "")
}

func generate(s *spec, steps []step, apiID, revision string, statuses []int) ([]mutation, []string, error) {
	var out []mutation
	var notes []string
	seen := map[string]bool{}
	add := func(m mutation, r request) {
		if len(out) >= 10000 {
			return
		}
		candidate := r.clone()
		if m.apply(&candidate, s) != nil || !exactViolation(s.validate(candidate), m.Target.Location, m.Target.Constraint) {
			notes = append(notes, "Skipped an unisolated or unclassified candidate for "+m.Target.Operation+" "+m.Target.Location+" "+m.Target.Constraint)
			return
		}
		raw, _ := json.Marshal([]any{Version, apiID, revision, m.Target.Operation, m.Target.Location, m.Target.Constraint})
		m.Target.Key = fmt.Sprintf("%x", sha256.Sum256(raw))
		m.Target.SchemaID = s.ref.ID
		m.Target.ExpectedStatuses = append([]int(nil), statuses...)
		if !seen[m.Target.Key] {
			seen[m.Target.Key] = true
			out = append(out, m)
		}
	}
	for index, st := range steps {
		r := st.Request
		if err := s.validate(r); err != nil {
			return nil, nil, fmt.Errorf("seed flow %s must have a valid request before generation", st.ID)
		}
		input, err := s.input(r)
		if err != nil {
			return nil, nil, err
		}
		operation := input.Route.Method + " " + input.Route.Path
		base := mutation{Step: index, Target: artifact.RejectionTarget{FlowID: st.ID, Operation: operation}}
		emit := func(in, path, constraint string, value any, remove bool) {
			m := base
			m.In, m.Path, m.Value, m.Remove = in, path, value, remove
			m.Target.Location = "/" + in
			if in == "body" {
				m.Target.Location += path
			} else {
				m.Target.Location += "/" + token(path)
			}
			m.Target.Constraint = constraint
			add(m, r)
		}
		op := input.Route.Operation
		if op.RequestBody != nil && op.RequestBody.Value != nil {
			body := op.RequestBody.Value
			if body.Required {
				emit("body", "", "required", nil, true)
			}
			media := body.Content.Get(r.Headers.Get("Content-Type"))
			if media != nil && media.Schema != nil && media.Schema.Value != nil && len(r.Body) > 0 {
				value, e := decodeJSON(r.Body)
				if e != nil {
					return nil, nil, e
				}
				var walk func(*openapi3.Schema, any, string, int, map[*openapi3.Schema]bool)
				walk = func(schema *openapi3.Schema, value any, path string, depth int, ancestors map[*openapi3.Schema]bool) {
					if schema == nil || schema.ReadOnly {
						return
					}
					if depth > 20 || ancestors[schema] || len(schema.AllOf)+len(schema.AnyOf)+len(schema.OneOf) > 0 || schema.Not != nil {
						notes = append(notes, "Skipped composed, recursive or deep schema at "+operation+" /body"+path)
						return
					}
					ancestors[schema] = true
					defer delete(ancestors, schema)
					for _, v := range values(schema, value) {
						emit("body", path, v.constraint, v.value, false)
					}
					if obj, ok := value.(map[string]any); ok {
						required := append([]string(nil), schema.Required...)
						sort.Strings(required)
						for _, name := range required {
							if _, present := obj[name]; present {
								emit("body", path+"/"+token(name), "required", nil, true)
							}
						}
						for _, name := range sortedKeys(schema.Properties) {
							if child := schema.Properties[name]; child != nil {
								if v, present := obj[name]; present {
									walk(child.Value, v, path+"/"+token(name), depth+1, ancestors)
								}
							}
						}
					}
					if list, ok := value.([]any); ok && schema.Items != nil {
						for i, v := range list {
							if i >= 32 {
								notes = append(notes, "Array item mutation limited to first 32 items at /body"+path)
								break
							}
							walk(schema.Items.Value, v, path+"/"+strconv.Itoa(i), depth+1, ancestors)
						}
					}
				}
				walk(media.Schema.Value, value, "", 0, map[*openapi3.Schema]bool{})
			}
		}
		params := map[string]*openapi3.Parameter{}
		for _, list := range []openapi3.Parameters{input.Route.PathItem.Parameters, op.Parameters} {
			for _, ref := range list {
				if ref != nil && ref.Value != nil {
					p := ref.Value
					params[p.In+"/"+p.Name] = p
				}
			}
		}
		for _, key := range sortedKeys(params) {
			p := params[key]
			if strings.EqualFold(p.Name, "Authorization") || p.In == "cookie" {
				notes = append(notes, "Authentication headers and cookies are not mutation targets")
				continue
			}
			if p.Required && p.In != "path" {
				emit(p.In, p.Name, "required", nil, true)
			}
			if p.Schema == nil || p.Schema.Value == nil || p.Schema.Value.Type == nil || len(p.Content) > 0 {
				continue
			}
			schema := p.Schema.Value
			if schema.Type.Includes("array") || schema.Type.Includes("object") || len(schema.AllOf)+len(schema.AnyOf)+len(schema.OneOf) > 0 || schema.Not != nil {
				notes = append(notes, "Skipped complex parameter "+key)
				continue
			}
			if schema.Type.Includes("integer") || schema.Type.Includes("number") || schema.Type.Includes("boolean") {
				emit(p.In, p.Name, "type", "wiretap-invalid", false)
			}
			for _, v := range values(schema, "") {
				if v.constraint != "type" && v.constraint != "nullable" {
					emit(p.In, p.Name, v.constraint, v.value, false)
				}
			}
		}
	}
	if len(out) >= 10000 {
		notes = append(notes, "Generation capped at 10000 targets")
	}
	sort.Strings(notes)
	notes = unique(notes)
	return out, notes, nil
}

type invalidValue struct {
	constraint string
	value      any
}

func values(s *openapi3.Schema, original any) []invalidValue {
	var out []invalidValue
	add := func(c string, v any) {
		if s.VisitJSON(v, openapi3.MultiErrors(), openapi3.VisitAsRequest()) == nil {
			return
		}
		// Some validators stop at enum/type errors even in MultiError mode.
		// Require the candidate to satisfy this node with only the intended
		// constraint relaxed, so an invalid enum cannot hide a wrong type or
		// an unrelated pattern/length violation.
		relaxed := *s
		switch c {
		case "type":
			relaxed.Type = nil
		case "nullable":
			relaxed.Nullable = true
		case "enum":
			relaxed.Enum = nil
		case "minimum", "exclusiveMinimum":
			relaxed.Min = nil
			relaxed.ExclusiveMin = false
		case "maximum", "exclusiveMaximum":
			relaxed.Max = nil
			relaxed.ExclusiveMax = false
		case "minLength":
			relaxed.MinLength = 0
		case "maxLength":
			relaxed.MaxLength = nil
		case "pattern":
			relaxed.Pattern = ""
		case "minItems":
			relaxed.MinItems = 0
		case "maxItems":
			relaxed.MaxItems = nil
		}
		if relaxed.VisitJSON(v, openapi3.MultiErrors(), openapi3.VisitAsRequest()) == nil {
			out = append(out, invalidValue{c, v})
		}
	}
	if s.Type != nil && len(s.Type.Slice()) == 1 {
		if s.Type.Includes("string") {
			add("type", 123)
		} else {
			add("type", "wiretap-invalid")
		}
	}
	if !s.Nullable {
		add("nullable", nil)
	}
	if len(s.Enum) > 0 {
		// Try several typed witnesses; whole-request validation removes any that
		// violate another constraint (for example an enum that exhausts a range).
		for _, v := range []any{"wiretap-invalid", 0, 1, -1, true, false} {
			add("enum", v)
		}
	}
	if s.Min != nil {
		v := *s.Min
		c := "exclusiveMinimum"
		if !s.ExclusiveMin {
			v = math.Nextafter(v, math.Inf(-1))
			c = "minimum"
			if s.Type != nil && s.Type.Includes("integer") {
				v = math.Ceil(*s.Min) - 1
			}
		}
		if !math.IsInf(v, 0) {
			add(c, v)
		}
	}
	if s.Max != nil {
		v := *s.Max
		c := "exclusiveMaximum"
		if !s.ExclusiveMax {
			v = math.Nextafter(v, math.Inf(1))
			c = "maximum"
			if s.Type != nil && s.Type.Includes("integer") {
				v = math.Floor(*s.Max) + 1
			}
		}
		if !math.IsInf(v, 0) {
			add(c, v)
		}
	}
	if s.MinLength > 0 && s.MinLength <= 10000 {
		add("minLength", strings.Repeat("a", int(s.MinLength-1)))
	}
	if s.MaxLength != nil && *s.MaxLength < 10000 {
		add("maxLength", strings.Repeat("a", int(*s.MaxLength+1)))
	}
	if s.Pattern != "" {
		for _, v := range []string{"", "!", "wiretap-invalid", "123", "a b"} {
			add("pattern", v)
		}
	}
	if list, ok := original.([]any); ok {
		if s.MinItems > 0 && s.MinItems <= uint64(len(list)) {
			add("minItems", list[:int(s.MinItems-1)])
		}
		if s.MaxItems != nil && *s.MaxItems < 10000 && len(list) > 0 {
			next := append([]any(nil), list...)
			for uint64(len(next)) <= *s.MaxItems {
				next = append(next, list[0])
			}
			add("maxItems", next)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func unique(values []string) []string {
	out := []string{}
	for _, v := range values {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}
