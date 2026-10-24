package assumptions

import (
	"context"
	"fmt"
)

// StoreReport retains the full pair reports so every discovered relationship
// has its own verdict, selected snapshots and evidence references.
type StoreReport struct {
	ReportVersion string    `json:"report_version"`
	RulesVersion  string    `json:"rules_version"`
	Mode          string    `json:"mode"`
	Scope         string    `json:"scope"`
	ArtifactsPath string    `json:"artifacts_path"`
	Compatible    bool      `json:"compatible"`
	Counts        Counts    `json:"counts"`
	Comparisons   []*Report `json:"comparisons"`
}

// CheckStore checks every relationship selected by declared mappings. Each
// provider witness belongs to one snapshot; evidence is never pooled across
// runs. An invalid input prevents publication of a partial aggregate report.
func CheckStore(ctx context.Context, options StoreOptions, ignoreHeaders []string) (*StoreReport, error) {
	plan, err := PlanStore(ctx, options)
	if err != nil {
		return nil, err
	}
	report := &StoreReport{
		ReportVersion: "1.0", RulesVersion: RulesVersion, Mode: options.Mode,
		Scope: "discovered_consumer_provider_relationships", ArtifactsPath: plan.Root,
		Compatible: len(plan.Checks) > 0, Comparisons: []*Report{},
	}
	for _, pair := range plan.Checks {
		pair.IgnoreHeaders = ignoreHeaders
		comparison, err := Check(ctx, pair)
		if err != nil {
			return nil, fmt.Errorf("consumer %q, provider %q, dependency %q: %w", pair.Consumer, pair.Provider, pair.Dependency, err)
		}
		for _, source := range []Source{comparison.Consumer, comparison.Provider} {
			if source.ManifestSHA256 != plan.digests[source.BundlePath] {
				return nil, fmt.Errorf("%w: capture %q changed after artifact selection; use stable completed snapshots", ErrInput, source.BundlePath)
			}
		}
		report.Comparisons = append(report.Comparisons, comparison)
		report.Compatible = report.Compatible && comparison.Compatible
		report.Counts.Assumptions += comparison.Counts.Assumptions
		report.Counts.Verified += comparison.Counts.Verified
		report.Counts.Uncovered += comparison.Counts.Uncovered
		report.Counts.Mismatched += comparison.Counts.Mismatched
		report.Counts.Unverified += comparison.Counts.Unverified
	}
	anchor := plan.Checks[0].Consumer
	if options.Mode == "provider" {
		anchor = plan.Checks[0].Provider
	}
	if err := verifyStoreSelection(ctx, plan, anchor); err != nil {
		return nil, err
	}
	return report, nil
}

// Even an unselected manifest influences discovery: changing its mappings or
// adding a new consumer could change the comparison set. Recheck membership
// and all manifest digests before publishing a verdict.
func verifyStoreSelection(ctx context.Context, plan *StorePlan, anchor string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, entries, err := discoverStore(ctx, plan.Root)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: artifact selection changed during comparison: %v", ErrInput, err)
	}
	digest, err := manifestDigest(anchor)
	if err != nil {
		return fmt.Errorf("%w: local capture %q: %v", ErrInput, anchor, err)
	}
	current := map[string]string{anchor: digest}
	for _, entry := range entries {
		current[entry.root] = entry.digest
	}
	if len(current) != len(plan.digests) {
		return fmt.Errorf("%w: artifact snapshots changed during comparison; use a stable downloaded comparison set", ErrInput)
	}
	for _, path := range sortedRuleKeys(plan.digests) {
		if current[path] != plan.digests[path] {
			return fmt.Errorf("%w: capture %q changed during comparison; use stable completed snapshots", ErrInput, path)
		}
	}
	return ctx.Err()
}
