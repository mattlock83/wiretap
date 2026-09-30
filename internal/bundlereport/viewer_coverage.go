package bundlereport

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/phrond/evermock/internal/coverage"
)

// These types are presentation-only: condition identity, measurement and
// evidence remain in the coverage report. In particular, observing a declared
// value is distinct from executing a test or verifying an invalid input.
type viewerContractSpec struct {
	*coverage.ContractSpec
	ID, Search                               string
	Counts                                   coverage.ContractCounts
	UnexpectedViolations, ExpectedViolations int
	Attention                                bool
	Operations                               []viewerContractOperation
}

type viewerContractOperation struct {
	ID, Name, Search               string
	Counts                         coverage.ContractCounts
	Attention                      bool
	Groups                         []viewerConditionGroup
	Violations, ExpectedViolations []viewerContractViolation
}

type viewerConditionGroup struct {
	Phase, Label      string
	Limits, Attention bool
	Counts            coverage.ContractCounts
	Rows              []viewerConditionRow
}

type viewerConditionRow struct {
	*coverage.ContractTarget
	Field, Label, Detail string
	Attention            bool
	Evidence             []viewerConditionEvidence
}

type viewerConditionEvidence struct {
	coverage.ContractEvidence
	TestURL string
}

type viewerContractViolation struct {
	coverage.ContractViolation
	Evidence viewerConditionEvidence
}

type viewerRejectionOperation struct {
	ID, Name, SchemaID, Search  string
	Covered, Failed, NotCovered int
	Attention                   bool
	Rows                        []viewerRejectionRow
}

type viewerRejectionRow struct {
	*coverage.RejectionCoverageTarget
	Field, Label string
	Attention    bool
	Evidence     []viewerConditionEvidence
}

func buildViewerCoverage(report *coverage.ContractCoverage) []viewerContractSpec {
	if report == nil {
		return nil
	}
	var result []viewerContractSpec
	for _, spec := range report.Specs {
		if spec == nil {
			continue
		}
		identity := strings.Join([]string{spec.Origin, spec.APIID, spec.SchemaID, spec.Revision}, "\x00")
		item := viewerContractSpec{ContractSpec: spec, ID: "cov-" + shortHash(identity)}
		operations := map[string]*viewerContractOperation{}
		operationFor := func(name string) *viewerContractOperation {
			if operations[name] == nil {
				label := name
				if label == "" {
					label = "Contract-wide"
				}
				operations[name] = &viewerContractOperation{ID: "cov-" + shortHash(identity+"\x00"+name), Name: label, Search: strings.Join([]string{spec.APIID, spec.SchemaID, spec.Origin, spec.TargetKind, label}, " ")}
			}
			return operations[name]
		}
		for _, target := range spec.Targets {
			if target == nil {
				continue
			}
			op := operationFor(target.Operation)
			row := viewerConditionRow{ContractTarget: target, Field: viewerConditionField(target.Location, target.Phase), Label: viewerConditionLabel(target), Detail: target.Reason, Attention: target.State != "covered" && target.State != "excluded", Evidence: viewerCoverageEvidence(target.Evidence)}
			phase, label := target.Phase, "Operation"
			switch phase {
			case "request":
				label = "Request"
			case "response":
				label = "Response"
			}
			limits := target.State == "unsupported" || target.State == "excluded"
			if limits {
				phase, label = "limits", "Measurement limits"
			}
			group := -1
			for i := range op.Groups {
				if op.Groups[i].Phase == phase {
					group = i
					break
				}
			}
			if group < 0 {
				op.Groups = append(op.Groups, viewerConditionGroup{Phase: phase, Label: label, Limits: limits})
				group = len(op.Groups) - 1
			}
			op.Groups[group].Rows = append(op.Groups[group].Rows, row)
			op.Groups[group].Attention = op.Groups[group].Attention || row.Attention
			countViewerCondition(&op.Groups[group].Counts, target.State)
			countViewerCondition(&op.Counts, target.State)
			countViewerCondition(&item.Counts, target.State)
			op.Attention = op.Attention || row.Attention
			op.Search += " " + row.Field + " " + row.Label + " " + viewerCoverageSearch(target)
		}
		for _, violation := range spec.Violations {
			op := operationFor(violation.Operation)
			view := viewerContractViolation{ContractViolation: violation, Evidence: viewerConditionEvidence{ContractEvidence: violation.Evidence, TestURL: viewerEvidenceTestURL(violation.Evidence)}}
			if violation.Expected {
				op.ExpectedViolations = append(op.ExpectedViolations, view)
				item.ExpectedViolations++
			} else {
				op.Violations = append(op.Violations, view)
				op.Attention = true
				item.UnexpectedViolations++
			}
			op.Search += " " + viewerCoverageSearch(violation)
		}
		for _, op := range operations {
			sort.Slice(op.Groups, func(i, j int) bool {
				return viewerPhaseOrder(op.Groups[i].Phase) < viewerPhaseOrder(op.Groups[j].Phase)
			})
			for i := range op.Groups {
				sort.SliceStable(op.Groups[i].Rows, func(a, b int) bool {
					x, y := op.Groups[i].Rows[a], op.Groups[i].Rows[b]
					return strings.Join([]string{x.Field, x.Label, x.Key}, "\x00") < strings.Join([]string{y.Field, y.Label, y.Key}, "\x00")
				})
			}
			item.Attention = item.Attention || op.Attention
			item.Operations = append(item.Operations, *op)
		}
		sort.Slice(item.Operations, func(i, j int) bool { return item.Operations[i].Name < item.Operations[j].Name })
		item.Search = strings.Join([]string{spec.APIID, spec.SchemaID, spec.Origin, spec.TargetKind, spec.Revision, strings.Join(spec.Notes, " ")}, " ")
		for _, op := range item.Operations {
			item.Search += " " + op.Search
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		return strings.Join([]string{a.Origin, a.APIID, a.SchemaID, a.Revision}, "\x00") < strings.Join([]string{b.Origin, b.APIID, b.SchemaID, b.Revision}, "\x00")
	})
	return result
}

func buildViewerRejections(report *coverage.RejectionCoverage) []viewerRejectionOperation {
	if report == nil {
		return nil
	}
	operations := map[string]*viewerRejectionOperation{}
	for _, target := range report.Targets {
		if target == nil {
			continue
		}
		key := target.SchemaID + "\x00" + target.Operation
		op := operations[key]
		if op == nil {
			op = &viewerRejectionOperation{ID: "rejection-" + shortHash(key), Name: target.Operation, SchemaID: target.SchemaID, Search: target.SchemaID + " " + target.Operation}
			operations[key] = op
		}
		row := viewerRejectionRow{RejectionCoverageTarget: target, Field: viewerInstanceField(target.Location), Label: viewerRejectionLabel(target.Constraint), Attention: target.State != "covered", Evidence: viewerCoverageEvidence(target.Evidence)}
		op.Rows = append(op.Rows, row)
		switch target.State {
		case "covered":
			op.Covered++
		case "failed":
			op.Failed++
		default:
			op.NotCovered++
		}
		op.Attention = op.Attention || row.Attention
		op.Search += " " + row.Field + " " + row.Label + " " + viewerCoverageSearch(target)
	}
	var result []viewerRejectionOperation
	for _, operation := range operations {
		sort.SliceStable(operation.Rows, func(i, j int) bool {
			a, b := operation.Rows[i], operation.Rows[j]
			return strings.Join([]string{a.Field, a.Label, a.Key}, "\x00") < strings.Join([]string{b.Field, b.Label, b.Key}, "\x00")
		})
		result = append(result, *operation)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].SchemaID+"\x00"+result[i].Name < result[j].SchemaID+"\x00"+result[j].Name
	})
	return result
}

func countViewerCondition(counts *coverage.ContractCounts, state string) {
	switch state {
	case "covered":
		counts.Covered++
	case "not_covered":
		counts.NotCovered++
	case "unknown":
		counts.Unknown++
	case "unsupported":
		counts.Unsupported++
	case "excluded":
		counts.Excluded++
	}
}

func viewerPhaseOrder(phase string) int {
	switch phase {
	case "request":
		return 0
	case "response":
		return 1
	case "operation":
		return 2
	default:
		return 3
	}
}

func viewerCoverageEvidence(evidence []coverage.ContractEvidence) []viewerConditionEvidence {
	var result []viewerConditionEvidence
	for _, item := range evidence {
		result = append(result, viewerConditionEvidence{ContractEvidence: item, TestURL: viewerEvidenceTestURL(item)})
	}
	return result
}

func viewerEvidenceTestURL(evidence coverage.ContractEvidence) string {
	if evidence.AttemptID == "" {
		return ""
	}
	return "#attempt-" + shortHash(evidence.AttemptID)
}

func viewerCoverageSearch(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func viewerPointerParts(location string) []string {
	parts := strings.Split(strings.TrimPrefix(location, "/"), "/")
	for i := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(parts[i], "~1", "/"), "~0", "~")
	}
	return parts
}

// Locations use the coverage catalogue's resolved OpenAPI pointers, which
// retain operation and response context even for referenced schemas.
func viewerConditionField(location, phase string) string {
	parts := viewerPointerParts(location)
	base, start := "Operation", len(parts)
	if phase != "operation" {
		base = "Contract"
	}
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "requestBody":
			base, start = "Body", i+1
		case "parameters":
			if i+2 < len(parts) {
				base, start = viewerParameterLabel(parts[i+1])+" · "+parts[i+2], i+3
			}
		case "responses":
			if i+1 < len(parts) {
				base, start = parts[i+1]+" response", i+2
				if i+3 < len(parts) && parts[i+2] == "headers" {
					base, start = parts[i+1]+" header · "+parts[i+3], i+4
				} else if i+2 < len(parts) && parts[i+2] == "content" {
					base = parts[i+1] + " body"
				}
			}
		case "security":
			return "Authentication"
		}
		if start != len(parts) {
			break
		}
	}
	var field, branches string
	for i := start; i < len(parts); i++ {
		switch parts[i] {
		case "properties":
			if i+1 < len(parts) {
				if field != "" {
					field += "."
				}
				field += parts[i+1]
				i++
			}
		case "items":
			field += "[]"
		case "oneOf", "anyOf", "allOf":
			if i+1 < len(parts) {
				branch := parts[i+1]
				if n, err := strconv.Atoi(branch); err == nil {
					branch = strconv.Itoa(n + 1)
				}
				branches += " (" + parts[i] + " branch " + branch + ")"
				i++
			}
		}
	}
	if field != "" {
		base += " · " + field
	}
	return base + branches
}

func viewerParameterLabel(in string) string {
	switch in {
	case "query":
		return "Query"
	case "path":
		return "Path"
	case "header":
		return "Header"
	case "cookie":
		return "Cookie"
	case "body":
		return "Body"
	default:
		return in
	}
}

func viewerInstanceField(location string) string {
	parts := viewerPointerParts(location)
	if len(parts) == 0 || parts[0] == "" {
		return "Request"
	}
	base := viewerParameterLabel(parts[0])
	if len(parts) > 1 {
		base += " · " + strings.Join(parts[1:], ".")
	}
	return base
}

func viewerConditionLabel(target *coverage.ContractTarget) string {
	condition := target.Condition
	switch target.Family {
	case "operation":
		return "exchange observed"
	case "response_status":
		if condition == "default" {
			return "default response observed"
		}
		return "HTTP " + condition + " observed"
	case "media_type":
		return "content type " + condition
	case "presence":
		if condition == "absent" {
			return "omitted"
		}
		return condition
	case "type":
		return "type " + condition
	case "enum":
		return "equals " + condition
	case "nullable":
		if condition == "non_null" {
			return "non-null value"
		}
		return "null value"
	case "number":
		if condition == "interior" {
			return "inside numeric bounds"
		}
		return "equals boundary " + strings.TrimPrefix(condition, "equal:")
	case "string_length", "array_length":
		if condition == "interior" {
			return "length inside bounds"
		}
		return "length " + strings.TrimPrefix(condition, "equal:")
	case "pattern":
		return "matches " + strings.TrimPrefix(condition, "match:")
	case "branch":
		return "schema branch matched"
	case "unsupported":
		return condition
	default:
		return strings.ReplaceAll(target.Family, "_", " ") + ": " + condition
	}
}

func viewerRejectionLabel(constraint string) string {
	switch constraint {
	case "required":
		return "required value missing"
	case "type":
		return "wrong value type"
	case "nullable":
		return "null where disallowed"
	case "enum":
		return "value outside allowed choices"
	case "minimum":
		return "below minimum"
	case "exclusiveMinimum":
		return "at or below exclusive minimum"
	case "maximum":
		return "above maximum"
	case "exclusiveMaximum":
		return "at or above exclusive maximum"
	case "minLength":
		return "string too short"
	case "maxLength":
		return "string too long"
	case "pattern":
		return "pattern mismatch"
	case "minItems":
		return "too few array items"
	case "maxItems":
		return "too many array items"
	default:
		return "violates " + constraint
	}
}
