package bundle

import (
	"errors"
	"sort"

	"github.com/mattlock83/wiretap/internal/artifact"
)

// API describes one configured or observed boundary. IDs are local to the
// system; they do not establish the identity of a separately published provider.
type API struct {
	ID         string        `json:"id"`
	TargetKind string        `json:"target_kind"`
	Configured bool          `json:"configured"`
	Protocols  []string      `json:"protocols"`
	Contracts  []APIContract `json:"contracts"`
	ObservedIn []string      `json:"observed_in"`
}

type APIContract struct {
	ID     string                `json:"id"`
	Type   string                `json:"type"`
	Source artifact.SchemaSource `json:"source"`
}

// apiInventory rebuilds declarations and merges committed observations. It
// always allocates fresh slices so a failed inventory commit can roll back.
func apiInventory(run Run, captures []*artifact.Artifact) ([]API, []API) {
	groups := []map[string]*API{{}, {}}
	get := func(role int, id string) *API {
		if id == "" {
			id = "unknown"
		}
		if a := groups[role][id]; a != nil {
			return a
		}
		kind := run.SUTTargetKind
		if role == 1 {
			kind = run.DependencyKinds[id]
		}
		if kind == "" {
			kind = "unknown"
		}
		a := &API{ID: id, TargetKind: kind, Protocols: []string{}, Contracts: []APIContract{}, ObservedIn: []string{}}
		groups[role][id] = a
		return a
	}
	if run.APIID != "" || run.SUTTargetKind != "" {
		get(0, run.APIID).Configured = true
	}
	for id := range run.DependencyKinds {
		get(1, id).Configured = true
	}
	for _, s := range run.Schemas {
		role, id := 0, run.APIID
		if s.Role == "dependency" {
			role, id = 1, s.AssetID
		} else if s.Role == "provided" {
			id = s.AssetID
		} else if s.Role != "sut" {
			continue
		}
		if id == "" {
			id = s.AssetID
		}
		a := get(role, id)
		a.Configured = true
		protocol := s.Protocol
		if protocol == "" {
			switch s.Type {
			case "openapi":
				protocol = "http"
			}
		}
		if protocol != "" {
			a.Protocols = uniqueString(a.Protocols, protocol)
		}
		ref := APIContract{ID: s.ID, Type: s.Type, Source: s.Source}
		found := false
		for _, old := range a.Contracts {
			if sameJSON(old, ref) {
				found = true
				break
			}
		}
		if !found {
			a.Contracts = append(a.Contracts, ref)
		}
	}
	for role, apis := range [][]API{run.ProvidedAPIs, run.Dependencies} {
		for _, old := range apis {
			if len(old.ObservedIn) == 0 {
				continue
			}
			a := get(role, old.ID)
			for _, id := range old.ObservedIn {
				a.ObservedIn = uniqueString(a.ObservedIn, id)
			}
			for _, protocol := range old.Protocols {
				a.Protocols = uniqueString(a.Protocols, protocol)
			}
		}
	}
	for _, capture := range captures {
		for _, flow := range capture.Traffic {
			role, id := 0, run.APIID
			if flow.Channel == "sut_to_dependency" {
				role, id = 1, flow.AssetID
			} else if flow.Channel != "test_to_sut" {
				continue
			}
			if id == "" && role == 0 {
				for _, s := range capture.Schemas {
					if s.Role == "sut" && s.AssetID != "" {
						id = s.AssetID
						break
					}
				}
			}
			a := get(role, id)
			a.ObservedIn = uniqueString(a.ObservedIn, capture.TestCase.ID)
			protocol := flow.Protocol
			if protocol == "" {
				if flow.Request != nil {
					protocol = "http"
				}
			}
			if protocol != "" {
				a.Protocols = uniqueString(a.Protocols, protocol)
			}
		}
	}
	out := [][]API{{}, {}}
	for role, group := range groups {
		for _, a := range group {
			sort.Strings(a.Protocols)
			sort.Strings(a.ObservedIn)
			sort.Slice(a.Contracts, func(i, j int) bool {
				left, right := a.Contracts[i], a.Contracts[j]
				return left.ID+"|"+left.Source.Path < right.ID+"|"+right.Source.Path
			})
			out[role] = append(out[role], *a)
		}
		sort.Slice(out[role], func(i, j int) bool { return out[role][i].ID < out[role][j].ID })
	}
	return out[0], out[1]
}

func uniqueString(values []string, value string) []string {
	for _, old := range values {
		if old == value {
			return values
		}
	}
	return append(values, value)
}

func validateInventory(run Run, captures []*artifact.Artifact) error {
	if run.InventoryScope != "configured_and_observed" || run.ProvidedAPIs == nil || run.Dependencies == nil || run.Attempts == nil {
		return errors.New("missing or unsupported API inventory")
	}
	declared := run
	declared.ProvidedAPIs, declared.Dependencies = nil, nil
	provided, dependent := apiInventory(declared, captures)
	if !sameJSON(provided, run.ProvidedAPIs) || !sameJSON(dependent, run.Dependencies) {
		return errors.New("API inventory disagrees with contracts or recorded tests")
	}
	return nil
}
