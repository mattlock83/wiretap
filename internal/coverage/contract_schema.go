package coverage

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
)

type conditionNode struct {
	location      string
	schema        *openapi3.Schema
	phase         string
	targets       []*ContractTarget
	properties    map[string]*conditionNode
	items         *conditionNode
	branches      map[string][]*conditionNode
	branchTargets map[string][]*ContractTarget
	unsafe        bool
	impossible    bool
	uncertain     bool
}

func pointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func present(_ any, p bool) bool { return p }
func absent(_ any, p bool) bool  { return !p }

func (b *contractBuilder) node(ref *openapi3.SchemaRef, loc string, stack map[*openapi3.Schema]bool) *conditionNode {
	n := &conditionNode{location: loc, phase: b.phase, properties: map[string]*conditionNode{}, branches: map[string][]*conditionNode{}, branchTargets: map[string][]*ContractTarget{}}
	if ref == nil || ref.Value == nil {
		n.targets = append(n.targets, b.unsupported(loc, "Unresolved schema"))
		n.unsafe = true
		return n
	}
	s := ref.Value
	n.schema = s
	if stack[s] {
		n.targets = append(n.targets, b.unsupported(loc, "Recursive condition analysis is unsupported"))
		n.unsafe = true
		return n
	}
	stack[s] = true
	defer delete(stack, s)
	add := func(family, condition string, predicate func(any, bool) bool) *ContractTarget {
		t := b.add(loc, family, condition, predicate)
		for _, existing := range n.targets {
			if existing == t {
				return t
			}
		}
		n.targets = append(n.targets, t)
		return t
	}
	unsupported := func(reason string) { n.targets = append(n.targets, b.unsupported(loc, reason)) }
	for _, typ := range s.Type.Slice() {
		typ := typ
		add("type", typ, func(v any, p bool) bool { return p && matchesType(v, typ) })
	}
	for _, v := range s.Enum {
		encoded, _ := json.Marshal(v)
		add("enum", string(encoded), func(actual any, p bool) bool { return p && jsonEqual(actual, v) })
	}
	if s.Nullable {
		add("nullable", "null", func(v any, p bool) bool { return p && v == nil })
		add("nullable", "non_null", func(v any, p bool) bool { return p && v != nil })
		// The current validator returns early for null before checking these
		// constraints. Do not interpret that short circuit as full validation.
		if len(s.Enum) > 0 || s.Not != nil || len(s.OneOf)+len(s.AnyOf)+len(s.AllOf) > 0 {
			unsupported("Nullable schemas with enum, not, or composition lack complete null validation")
			n.unsafe = true
		}
	}
	if s.Min != nil || s.Max != nil {
		min, max := floatRat(s.Min), floatRat(s.Max)
		if min != nil && !s.ExclusiveMin {
			add("number", "equal:"+min.RatString(), func(v any, p bool) bool { x := numberRat(v); return p && x != nil && x.Cmp(min) == 0 })
		}
		if max != nil && !s.ExclusiveMax {
			add("number", "equal:"+max.RatString(), func(v any, p bool) bool { x := numberRat(v); return p && x != nil && x.Cmp(max) == 0 })
		}
		t := add("number", "interior", func(v any, p bool) bool {
			x := numberRat(v)
			return p && x != nil && (min == nil || x.Cmp(min) > 0) && (max == nil || x.Cmp(max) < 0)
		})
		if min != nil && max != nil {
			impossible := min.Cmp(max) >= 0
			if s.Type.Is("integer") {
				lo := new(big.Int).Div(min.Num(), min.Denom())
				lo.Add(lo, big.NewInt(1))
				impossible = new(big.Rat).SetInt(lo).Cmp(max) >= 0
			}
			if impossible {
				exclude(t, "No value exists strictly between these bounds")
			}
		}
	}
	if s.MultipleOf != nil {
		unsupported("multipleOf has validation but no independent coverage class")
	}
	lengths := func(family string, min uint64, max *uint64, length func(any) (uint64, bool)) {
		add(family, "equal:"+strconv.FormatUint(min, 10), func(v any, p bool) bool { l, ok := length(v); return p && ok && l == min })
		if max != nil {
			m := *max
			add(family, "equal:"+strconv.FormatUint(m, 10), func(v any, p bool) bool { l, ok := length(v); return p && ok && l == m })
		}
		t := add(family, "interior", func(v any, p bool) bool { l, ok := length(v); return p && ok && l > min && (max == nil || l < *max) })
		if max != nil && (*max <= min || *max-min <= 1) {
			exclude(t, "No integral length exists strictly between these bounds")
		}
	}
	if s.Type.Includes("string") || s.MinLength > 0 || s.MaxLength != nil || s.Pattern != "" {
		lengths("string_length", s.MinLength, s.MaxLength, func(v any) (uint64, bool) { x, ok := v.(string); return uint64(utf8.RuneCountInString(x)), ok })
	}
	if s.Type.Includes("array") || s.Items != nil || s.MinItems > 0 || s.MaxItems != nil {
		lengths("array_length", s.MinItems, s.MaxItems, func(v any) (uint64, bool) { x, ok := v.([]any); return uint64(len(x)), ok })
	}
	if s.Pattern != "" {
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			unsupported("Unsupported pattern syntax")
			n.unsafe = true
		} else {
			add("pattern", "match:"+s.Pattern, func(v any, p bool) bool { x, ok := v.(string); return p && ok && re.MatchString(x) })
		}
	}
	// The runtime validator does not enforce every named format. Do not silently
	// turn a format annotation into a coverage promise.
	if s.Format != "" {
		unsupported("format:" + s.Format + " has no coverage measurement")
	}
	if s.UniqueItems {
		unsupported("uniqueItems has validation but no coverage measurement")
	}
	if s.MinProps > 0 || s.MaxProps != nil {
		unsupported("Object property counts have no coverage measurement")
	}
	if s.AdditionalProperties.Has != nil || s.AdditionalProperties.Schema != nil {
		unsupported("additionalProperties has validation but no coverage measurement")
		if s.AdditionalProperties.Schema != nil {
			if reason := unmeasuredSchemaSafety(s.AdditionalProperties.Schema, b.phase, stack); reason != "" {
				unsupported("additionalProperties: " + reason)
				n.unsafe = true
			}
		}
	}
	if s.Not != nil {
		unsupported("not has validation but no coverage measurement")
		if reason := unmeasuredSchemaSafety(s.Not, b.phase, stack); reason != "" {
			unsupported("not: " + reason)
			n.unsafe = true
		}
	}
	if s.Discriminator != nil {
		unsupported("Discriminator selection has no coverage measurement")
		n.unsafe = true
	}
	for _, name := range sortedMapKeys(s.Extensions) {
		if !strings.HasPrefix(name, "x-") {
			unsupported("Unsupported schema keyword: " + name)
			n.unsafe = true
		}
	}
	for _, name := range sortedMapKeys(s.Properties) {
		prop := s.Properties[name]
		if prop == nil || prop.Value == nil {
			n.unsafe = true
			continue
		}
		if b.phase == "request" && prop.Value.ReadOnly || b.phase == "response" && prop.Value.WriteOnly {
			continue
		}
		childLoc := loc + "/properties/" + pointerToken(name)
		child := b.node(prop, childLoc, stack)
		presence := b.add(childLoc, "presence", "present", present)
		if child.impossible {
			exclude(presence, "No value satisfies the property's schema")
		}
		child.targets = append(child.targets, presence)
		required := false
		for _, r := range s.Required {
			if r == name {
				required = true
			}
		}
		if !required {
			child.targets = append(child.targets, b.add(childLoc, "presence", "absent", absent))
		}
		n.properties[name] = child
		n.unsafe = n.unsafe || child.unsafe
	}
	// A required name need not have a properties entry. Its presence is still a
	// declared condition, even when its value has no individually named schema.
	for _, name := range s.Required {
		if _, declared := s.Properties[name]; declared {
			continue
		}
		childLoc := loc + "/properties/" + pointerToken(name)
		n.properties[name] = &conditionNode{
			location: childLoc,
			phase:    b.phase,
			targets:  []*ContractTarget{b.add(childLoc, "presence", "present", present)},
		}
	}
	if s.Items != nil {
		n.items = b.node(s.Items, loc+"/items", stack)
		n.unsafe = n.unsafe || n.items.unsafe
	}
	for _, group := range []struct {
		name string
		refs openapi3.SchemaRefs
	}{{"oneOf", s.OneOf}, {"anyOf", s.AnyOf}, {"allOf", s.AllOf}} {
		for i, ref := range group.refs {
			branchLoc := fmt.Sprintf("%s/%s/%d", loc, group.name, i)
			child := b.node(ref, branchLoc, stack)
			n.branches[group.name] = append(n.branches[group.name], child)
			n.branchTargets[group.name] = append(n.branchTargets[group.name], b.add(branchLoc, "branch", group.name+":matched", nil))
			n.unsafe = n.unsafe || child.unsafe
		}
	}
	// Finite enums allow exact feasibility checks against all constraints at this
	// schema node. For harder intersections retain explicit uncertainty.
	validEnums := make([]any, 0, len(s.Enum))
	for _, v := range s.Enum {
		if schemaAccepts(s, v, b.phase) {
			validEnums = append(validEnums, v)
		}
	}
	localImpossible := localDomainImpossible(s)
	n.impossible = !n.unsafe && (len(s.Enum) > 0 && len(validEnums) == 0 || localImpossible && !s.Nullable)
	for _, t := range n.targets {
		if t.Family == "number" && strings.HasPrefix(t.Condition, "equal:") && t.State != "excluded" {
			r, ok := new(big.Rat).SetString(strings.TrimPrefix(t.Condition, "equal:"))
			if ok {
				f, _ := r.Float64()
				if !schemaAccepts(s, f, b.phase) {
					exclude(t, "Numeric boundary violates another constraint in this schema")
				}
			}
		}
		if t.State == "unsupported" || t.State == "excluded" {
			continue
		}
		if len(s.Enum) > 0 {
			feasible := false
			for _, v := range validEnums {
				if t.match(v, true) {
					feasible = true
					break
				}
			}
			if !feasible {
				exclude(t, "No permitted enum value satisfies this condition and schema")
			}
		} else if s.Pattern != "" || s.MultipleOf != nil || len(s.AllOf)+len(s.OneOf)+len(s.AnyOf) > 0 || s.Not != nil {
			uncertain(t, "Feasibility under intersecting constraints is unresolved until a valid observation")
		}
	}
	if localImpossible && s.Nullable && !n.unsafe {
		for _, t := range n.targets {
			if t.State != "unsupported" && !(t.Family == "nullable" && t.Condition == "null") {
				exclude(t, "No non-null value satisfies the local bounds")
			}
		}
	}
	if len(s.AllOf)+len(s.OneOf)+len(s.AnyOf) > 0 || len(s.Enum) > 0 && (len(s.Properties) > 0 || s.Items != nil) {
		n.walkTargets(func(t *ContractTarget) {
			if t.State == "not_covered" {
				uncertain(t, "Feasibility under composed constraints is unresolved until a valid observation")
			}
		})
	}
	if n.impossible {
		n.walkTargets(func(t *ContractTarget) {
			if t.State != "unsupported" {
				exclude(t, "No value satisfies this schema")
			}
		})
		return n
	}
	// Child feasibility depends on the enclosing object/array as well as its
	// own schema. Do not label ancestors feasible when a nested
	// constraint has unresolved satisfiability. A valid observation can resolve
	// each condition it actually exercises without hiding untouched targets.
	nestedUncertain := n.items != nil && (n.items.impossible || n.items.uncertain)
	for _, child := range n.properties {
		nestedUncertain = nestedUncertain || child.impossible || child.uncertain
	}
	if nestedUncertain {
		for _, t := range n.targets {
			if t.State == "not_covered" {
				uncertain(t, "Feasibility under nested constraints is unresolved until a valid observation")
			}
		}
	}
	n.walkTargets(func(t *ContractTarget) { n.uncertain = n.uncertain || t.Feasibility == "unknown" })
	return n
}

// Constraints without their own measurement rules can still affect validity of
// the whole message. Inspect their schema graph for validation gaps without
// generating targets for arbitrary property names or expanding recursive refs.
func unmeasuredSchemaSafety(ref *openapi3.SchemaRef, phase string, stack map[*openapi3.Schema]bool) string {
	if ref == nil || ref.Value == nil {
		return "Unresolved schema"
	}
	s := ref.Value
	if stack[s] {
		return "Recursive condition analysis is unsupported"
	}
	stack[s] = true
	defer delete(stack, s)
	if s.Nullable && (len(s.Enum) > 0 || s.Not != nil || len(s.OneOf)+len(s.AnyOf)+len(s.AllOf) > 0) {
		return "Nullable schemas with enum, not, or composition lack complete null validation"
	}
	if s.Discriminator != nil {
		return "Discriminator selection has no coverage measurement"
	}
	if s.Pattern != "" {
		if _, err := regexp.Compile(s.Pattern); err != nil {
			return "Unsupported pattern syntax"
		}
	}
	for _, name := range sortedMapKeys(s.Extensions) {
		if !strings.HasPrefix(name, "x-") {
			return "Unsupported schema keyword: " + name
		}
	}
	children := openapi3.SchemaRefs{}
	for _, name := range sortedMapKeys(s.Properties) {
		child := s.Properties[name]
		if child != nil && child.Value != nil && (phase == "request" && child.Value.ReadOnly || phase == "response" && child.Value.WriteOnly) {
			continue
		}
		children = append(children, child)
	}
	for _, child := range []*openapi3.SchemaRef{s.Items, s.Not, s.AdditionalProperties.Schema} {
		if child != nil {
			children = append(children, child)
		}
	}
	children = append(children, s.OneOf...)
	children = append(children, s.AnyOf...)
	children = append(children, s.AllOf...)
	for _, child := range children {
		if reason := unmeasuredSchemaSafety(child, phase, stack); reason != "" {
			return reason
		}
	}
	return ""
}

func localDomainImpossible(s *openapi3.Schema) bool {
	if s.Type.Is("string") && s.MaxLength != nil && *s.MaxLength < s.MinLength {
		return true
	}
	if s.Type.Is("array") && s.MaxItems != nil && *s.MaxItems < s.MinItems {
		return true
	}
	if !(s.Type.Is("number") || s.Type.Is("integer")) || s.Min == nil || s.Max == nil {
		return false
	}
	min, max := floatRat(s.Min), floatRat(s.Max)
	if cmp := min.Cmp(max); cmp > 0 || cmp == 0 && (s.ExclusiveMin || s.ExclusiveMax) {
		return true
	}
	if s.Type.Is("integer") {
		lowest := new(big.Int).Div(min.Num(), min.Denom())
		if !min.IsInt() || s.ExclusiveMin {
			lowest.Add(lowest, big.NewInt(1))
		}
		cmp := new(big.Rat).SetInt(lowest).Cmp(max)
		return cmp > 0 || cmp == 0 && s.ExclusiveMax
	}
	return false
}

func exclude(t *ContractTarget, reason string) {
	t.State = "excluded"
	t.Feasibility = "excluded"
	t.Reason = reason
}
func uncertain(t *ContractTarget, reason string) {
	t.State = "unknown"
	t.Feasibility = "unknown"
	t.Reason = reason
}
func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func schemaAccepts(s *openapi3.Schema, v any, phase string) bool {
	if s == nil {
		return false
	}
	option := openapi3.VisitAsRequest()
	if phase == "response" {
		option = openapi3.VisitAsResponse()
	}
	return s.VisitJSON(validatorValue(v), option) == nil
}
func (n *conditionNode) walkTargets(fn func(*ContractTarget)) {
	for _, t := range n.targets {
		fn(t)
	}
	for _, name := range sortedMapKeys(n.properties) {
		n.properties[name].walkTargets(fn)
	}
	if n.items != nil {
		n.items.walkTargets(fn)
	}
	for _, name := range []string{"oneOf", "anyOf", "allOf"} {
		for _, t := range n.branchTargets[name] {
			fn(t)
		}
		for _, c := range n.branches[name] {
			c.walkTargets(fn)
		}
	}
}
func (n *conditionNode) measure(v any, p bool, path string, e ContractEvidence, hits *[]ConditionHit) {
	e.InstanceLocation = path
	for _, t := range n.targets {
		if t.match == nil || t.State == "excluded" || (!p && t.Family != "presence") {
			continue
		}
		if t.match(v, p) {
			recordHit(t, e, hits)
		}
	}
	if !p {
		return
	}
	if obj, ok := v.(map[string]any); ok {
		for _, name := range sortedMapKeys(n.properties) {
			value, exists := obj[name]
			n.properties[name].measure(value, exists, path+"/"+pointerToken(name), e, hits)
		}
	}
	if arr, ok := v.([]any); ok && n.items != nil {
		for i, value := range arr {
			n.items.measure(value, true, path+"/"+strconv.Itoa(i), e, hits)
		}
	}
	for _, name := range []string{"oneOf", "anyOf", "allOf"} {
		matches := []int{}
		for i, c := range n.branches[name] {
			if !c.unsafe && schemaAccepts(c.schema, v, n.phase) {
				matches = append(matches, i)
			}
		}
		if name == "oneOf" && len(matches) != 1 || name == "allOf" && len(matches) != len(n.branches[name]) {
			continue
		}
		for _, i := range matches {
			recordHit(n.branchTargets[name][i], e, hits)
			n.branches[name][i].measure(v, true, path, e, hits)
		}
	}
}
func recordHit(t *ContractTarget, e ContractEvidence, hits *[]ConditionHit) {
	if t.State == "unsupported" || t.State == "excluded" {
		return
	}
	// A valid failed attempt establishes feasibility, but earns no passing credit.
	if e.Result == "observed_valid" {
		t.Feasibility = "known"
		if t.State != "covered" {
			t.State = "not_covered"
			t.Reason = ""
		}
		e.Result = "observed"
	}
	t.observe(e)
	*hits = append(*hits, ConditionHit{Key: t.Key, InstanceLocation: e.InstanceLocation})
}
func matchesType(v any, typ string) bool {
	switch typ {
	case "null":
		return v == nil
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "number":
		return numberRat(v) != nil
	case "integer":
		r := numberRat(v)
		return r != nil && r.IsInt()
	}
	return false
}
func numberRat(v any) *big.Rat {
	var str string
	switch n := v.(type) {
	case json.Number:
		str = n.String()
	case float64:
		str = strconv.FormatFloat(n, 'g', -1, 64)
	case int:
		str = strconv.Itoa(n)
	default:
		return nil
	}
	r, ok := new(big.Rat).SetString(str)
	if !ok {
		return nil
	}
	return r
}
func floatRat(f *float64) *big.Rat {
	if f == nil {
		return nil
	}
	return numberRat(*f)
}
func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			other, exists := y[k]
			if !exists || !jsonEqual(v, other) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !jsonEqual(v, y[i]) {
				return false
			}
		}
		return true
	}

	if x, y := numberRat(a), numberRat(b); x != nil && y != nil {
		return x.Cmp(y) == 0
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// kin-openapi compares composite enums with reflect.DeepEqual. Keep exact JSON
// numbers for measurement, and use its expected representation for validation.
func validatorValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = validatorValue(v)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = validatorValue(v)
		}
		return out
	default:
		return v
	}
}
