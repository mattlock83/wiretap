package bundlereport

import "sort"

// Group executions of the same test together, including retries.
type viewerTestGroup struct {
	Name, ID, Search string
	Passed, Other    int
	Attempts         []viewerAttempt
}

func groupViewerAttempts(attempts []viewerAttempt) []viewerTestGroup {
	var groups []viewerTestGroup
	indices := map[string]int{}
	for _, attempt := range attempts {
		index, exists := indices[attempt.DisplayName]
		if !exists {
			index = len(groups)
			indices[attempt.DisplayName] = index
			groups = append(groups, viewerTestGroup{Name: attempt.DisplayName, ID: "test-group-" + shortHash(attempt.DisplayName)})
		}
		group := &groups[index]
		group.Attempts = append(group.Attempts, attempt)
		group.Search += " " + attempt.Search
		if attempt.Status == "pass" {
			group.Passed++
		} else {
			group.Other++
		}
	}
	return groups
}

type viewerCoverageLink struct{ Label, URL string }

func viewerCoverageLinks(specs []viewerContractSpec) map[string][]viewerCoverageLink {
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
			for _, v := range op.Violations {
				add(v.Evidence.AttemptID, spec.APIID+" · "+op.Name, op.ID)
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
