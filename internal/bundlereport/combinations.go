package bundlereport

import (
	"sort"
	"strings"

	"github.com/phrond/wiretap/internal/coverage"
)

// contractCombination presents one recorded message without changing its
// measurement or combining it with evidence from another message.
type contractCombination struct {
	Operation, Fingerprint, Phase, Search string
	Attention                             bool
	Evidence                              viewerConditionEvidence
	Hits                                  []combinationHit
}

type combinationHit struct {
	Key, Field, Label, Location string
}

func contractCombinations(spec *coverage.ContractSpec) []contractCombination {
	targets := make(map[string]*coverage.ContractTarget, len(spec.Targets))
	for _, target := range spec.Targets {
		if target != nil {
			targets[target.Key] = target
		}
	}
	result := make([]contractCombination, 0, len(spec.Combinations))
	for _, combination := range spec.Combinations {
		e := combination.Evidence
		view := contractCombination{Fingerprint: combination.Fingerprint, Phase: combination.Phase, Attention: e.Result != "covered", Evidence: viewerConditionEvidence{ContractEvidence: e, TestURL: viewerEvidenceTestURL(e)}}
		var search strings.Builder
		search.WriteString(combination.Fingerprint + " " + combination.Phase + " " + viewerCoverageSearch(e))
		for _, hit := range combination.Hits {
			if target := targets[hit.Key]; target != nil {
				view.Operation = target.Operation
				field, label := viewerTargetField(target), viewerConditionLabel(target)
				view.Hits = append(view.Hits, combinationHit{Key: hit.Key, Field: field, Label: label, Location: hit.InstanceLocation})
				search.WriteString(" " + hit.Key + " " + field + " " + label + " " + hit.InstanceLocation)
			}
		}
		if len(view.Hits) == 0 {
			continue
		}
		view.Search = view.Operation + " " + search.String()
		sort.Slice(view.Hits, func(i, j int) bool {
			a, b := view.Hits[i], view.Hits[j]
			return strings.Join([]string{a.Field, a.Label, a.Location, a.Key}, "\x00") < strings.Join([]string{b.Field, b.Label, b.Location, b.Key}, "\x00")
		})
		result = append(result, view)
	}
	return result
}
