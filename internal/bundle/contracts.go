package bundle

import (
	"errors"
	"fmt"
	"sort"
)

// ContractResolution preserves declared scope even before discovery succeeds.
// Diagnostics are deliberately fixed messages and cannot contain credentials.
type ContractResolution struct {
	APIID      string   `json:"api_id"`
	Role       string   `json:"role"`
	Protocol   string   `json:"protocol"`
	Source     string   `json:"source"`
	State      string   `json:"contract_resolution"`
	Services   []string `json:"services,omitempty"`
	Diagnostic string   `json:"diagnostic,omitempty"`
}

func (w *Writer) SetContracts(states []ContractResolution) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run.RunState != "open" {
		return errors.New("cannot update contracts after run completion")
	}
	states = append([]ContractResolution(nil), states...)
	seen := map[string]bool{}
	for _, state := range states {
		if state.State != "pending" && state.State != "failed" && state.State != "resolved" {
			return fmt.Errorf("invalid contract resolution state %q", state.State)
		}
		key := state.Role + ":" + state.APIID
		if seen[key] {
			return errors.New("duplicate boundary contract state")
		}
		seen[key] = true
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Role+states[i].APIID < states[j].Role+states[j].APIID })
	if sameJSON(w.run.Contracts, states) {
		return nil
	}
	previous := w.run
	w.run.Contracts = states
	w.bump()
	if err := w.writeRun(); err != nil {
		w.run = previous
		return err
	}
	return nil
}
