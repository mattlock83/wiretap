package bundlereport

import "sort"

// Group generated cases by their source execution rather than presenting a
// baseline and every mutation as unrelated user-authored scenarios.
type viewerTestGroup struct {
	Name, ID, Search                    string
	Generated                           bool
	Passed, Other, Baselines, Mutations int
	MutationPassed, MutationOther       int
	Attempts                            []viewerAttempt
	BaselineRuns, Cases                 []viewerAttempt
}

func groupViewerAttempts(attempts []viewerAttempt) []viewerTestGroup {
	var groups []viewerTestGroup
	indices := map[string]int{}
	for _, a := range attempts {
		i, ok := indices[a.GroupKey]
		if !ok {
			i = len(groups)
			indices[a.GroupKey] = i
			groups = append(groups, viewerTestGroup{Name: a.GroupName, ID: "test-group-" + shortHash(a.GroupKey), Generated: a.Exploration != nil})
		}
		group := &groups[i]
		group.Attempts = append(group.Attempts, a)
		group.Search += " " + a.Search
		if a.Status == "pass" {
			group.Passed++
		} else {
			group.Other++
		}
		if a.Exploration != nil {
			if a.Exploration.Mode == "baseline" {
				group.Baselines++
				group.BaselineRuns = append(group.BaselineRuns, a)
			} else {
				group.Mutations++
				group.Cases = append(group.Cases, a)
				if a.Status == "pass" {
					group.MutationPassed++
				} else {
					group.MutationOther++
				}
			}
		}
	}
	return groups
}

func viewerMutationLabel(location, constraint string) string {
	return viewerInstanceField(location) + ": " + viewerRejectionLabel(constraint)
}

type viewerCoverageLink struct{ Label, URL string }

func viewerCoverageLinks(specs []viewerContractSpec, rejections []viewerRejectionOperation) map[string][]viewerCoverageLink {
	links := map[string]map[string]viewerCoverageLink{}
	add := func(attemptID, label, anchor string) {
		if links[attemptID] == nil {
			links[attemptID] = map[string]viewerCoverageLink{}
		}
		links[attemptID][anchor] = viewerCoverageLink{label, "#" + anchor}
	}
	for _, spec := range specs {
		for _, op := range spec.Operations {
			for _, group := range op.Groups {
				for _, row := range group.Rows {
					for _, e := range row.Evidence {
						add(e.AttemptID, spec.APIID+" · "+op.Name, op.ID)
					}
				}
			}
			for _, v := range append(append([]viewerContractViolation{}, op.Violations...), op.ExpectedViolations...) {
				add(v.Evidence.AttemptID, spec.APIID+" · "+op.Name, op.ID)
			}
		}
	}
	for _, op := range rejections {
		for _, row := range op.Rows {
			for _, e := range row.Evidence {
				add(e.AttemptID, op.Name+" · invalid inputs", op.ID)
			}
		}
	}
	result := map[string][]viewerCoverageLink{}
	for id, entries := range links {
		for _, link := range entries {
			result[id] = append(result[id], link)
		}
		sort.Slice(result[id], func(i, j int) bool { a, b := result[id][i], result[id][j]; return a.Label+a.URL < b.Label+b.URL })
	}
	return result
}
