package assumptions

import (
	"context"
	"fmt"
	"sort"

	"github.com/mattlock83/wiretap/internal/runset"
	"github.com/mattlock83/wiretap/internal/version"
)

type SetReport struct {
	ReportVersion  string           `json:"report_version"`
	RulesVersion   string           `json:"rules_version"`
	Producer       version.Producer `json:"producer"`
	Scope          string           `json:"scope"`
	Mode           string           `json:"mode"`
	Dependency     string           `json:"dependency"`
	IgnoredHeaders []string         `json:"ignored_headers"`
	Compatible     bool             `json:"compatible"`
	Consumers      []Source         `json:"consumers"`
	Providers      []Source         `json:"providers"`
	Counts         Counts           `json:"counts"`
	Matches        []Match          `json:"matches"`
	Findings       []Finding        `json:"findings"`
}

// CheckRunSets keeps each matching request/response pair in one original
// exchange. Only already-verified members from the same build can supply
// alternative witnesses for a consumer assumption.
func CheckRunSets(ctx context.Context, consumerPath, providerPath, mode, dependency string, ignored []string) (*SetReport, error) {
	consumers, err := runset.Load(ctx, consumerPath)
	if err != nil {
		return nil, fmt.Errorf("%w: consumer run set: %v", ErrInput, err)
	}
	providers, err := runset.Load(ctx, providerPath)
	if err != nil {
		return nil, fmt.Errorf("%w: provider run set: %v", ErrInput, err)
	}
	if len(consumers.Archives)*len(providers.Archives) > 256 {
		return nil, fmt.Errorf("%w: run set comparison exceeds 256 archive pairs", ErrInput)
	}
	if mode != "consumer" && mode != "provider" {
		return nil, fmt.Errorf("%w: invalid run set check mode", ErrInput)
	}
	selected, err := selectDependency(consumers.Archives[0].Run, providers.Archives[0].Run, dependency)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInput, err)
	}
	result := &SetReport{ReportVersion: "1.0", RulesVersion: RulesVersion, Producer: version.Current(), Scope: "same_build_run_sets", Mode: mode, Dependency: selected, IgnoredHeaders: []string{}, Consumers: []Source{}, Providers: []Source{}, Matches: []Match{}, Findings: []Finding{}}
	// Verify projections even for an empty shard; it cannot hide a corrupted
	// export merely because another shard supplied all usable evidence.
	for index, set := range []*runset.Set{consumers, providers} {
		for _, archive := range set.Archives {
			snapshot, err := loadSnapshot(archive.Path)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInput, err)
			}
			boundary := ""
			if index == 0 {
				boundary = selected
			}
			source := snapshot.source("")
			if err := snapshot.verifyProjection("", boundary, &source); err != nil {
				return nil, fmt.Errorf("%w: run set Arazzo inputs: %v", ErrInput, err)
			}
			if index == 0 {
				result.Consumers = append(result.Consumers, source)
			} else {
				result.Providers = append(result.Providers, source)
			}
		}
	}
	for _, consumer := range consumers.Archives {
		matches := map[string]Match{}
		findings := map[string]Finding{}
		for _, provider := range providers.Archives {
			report, err := Check(ctx, Options{Mode: mode, Consumer: consumer.Path, Provider: provider.Path, Dependency: selected, IgnoreHeaders: ignored})
			if err != nil {
				return nil, err
			}
			result.IgnoredHeaders = append([]string{}, report.IgnoredHeaders...)
			for _, match := range report.Matches {
				key := evidenceKey(match.Consumer)
				if _, exists := matches[key]; !exists {
					matches[key] = match
				}
			}
			for _, finding := range report.Findings {
				if finding.Code == "no_consumer_assumptions" {
					continue
				}
				if finding.Consumer == nil {
					// Identity/contract/run-level failure cannot be cured by an
					// unrelated successful shard.
					result.Findings = append(result.Findings, finding)
					continue
				}
				key := evidenceKey(*finding.Consumer)
				previous, exists := findings[key]
				if !exists || findingRank(finding.Kind) > findingRank(previous.Kind) {
					findings[key] = finding
				}
			}
		}
		keys := map[string]bool{}
		for key := range matches {
			keys[key] = true
		}
		for key := range findings {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			result.Counts.Assumptions++
			if match, exists := matches[key]; exists {
				result.Matches = append(result.Matches, match)
				result.Counts.Verified++
			} else {
				result.Findings = append(result.Findings, findings[key])
			}
		}
	}
	if result.Counts.Assumptions == 0 {
		result.Findings = append(result.Findings, Finding{Kind: "unverified", Code: "no_consumer_assumptions", Message: "selected run set has no consumer mock exchanges"})
	}
	for _, finding := range result.Findings {
		switch finding.Kind {
		case "uncovered":
			result.Counts.Uncovered++
		case "mismatch":
			result.Counts.Mismatched++
		case "unverified":
			result.Counts.Unverified++
		}
	}
	for _, set := range []*runset.Set{consumers, providers} {
		for _, archive := range set.Archives {
			if digest, err := runset.Digest(archive.Path); err != nil || digest != archive.Digest {
				return nil, fmt.Errorf("%w: run set member changed during comparison", ErrInput)
			}
		}
	}
	result.Compatible = len(result.Findings) == 0 && result.Counts.Verified > 0
	return result, nil
}

func evidenceKey(e Evidence) string { return e.RunID + "\x00" + e.AttemptID + "\x00" + e.FlowID }
func findingRank(kind string) int {
	switch kind {
	case "unverified":
		return 3
	case "mismatch":
		return 2
	default:
		return 1
	}
}
