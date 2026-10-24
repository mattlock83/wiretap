package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeManifest validates the declarations available in a run manifest without
// opening referenced files. It is suitable for selecting archives, not for
// certifying their evidence. Load additionally verifies contracts, attempts,
// attachments and the inventory against the captured observations.
func DecodeManifest(data []byte) (*Run, error) {
	if err := rejectDuplicateKeys(data); err != nil {
		return nil, err
	}
	var run Run
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&run); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("run manifest contains trailing JSON or invalid data")
	}
	if !supportedEvidenceVersion(run.EvidenceVersion) {
		return nil, fmt.Errorf("unsupported evidence version %q", run.EvidenceVersion)
	}
	if err := checkCapabilities(run.RequiredCapabilities); err != nil {
		return nil, err
	}
	if err := validateApplication(run.Application); err != nil {
		return nil, err
	}
	if !safeID(run.RunID) || run.Revision == 0 || run.StartedAt.IsZero() || (run.RunState != "open" && run.RunState != "complete" && run.RunState != "interrupted") {
		return nil, errors.New("invalid run identity or revision")
	}
	if run.APIID != "" && !safeID(run.APIID) || run.SUTTargetKind != "" && !validKind(run.SUTTargetKind) {
		return nil, errors.New("invalid provided API identity or target kind")
	}
	for id, kind := range run.DependencyKinds {
		if !safeID(id) || !validKind(kind) {
			return nil, fmt.Errorf("invalid dependency identity or target kind for %q", id)
		}
	}
	for _, pending := range run.Pending {
		if err := pending.Identity.Validate(); err != nil {
			return nil, err
		}
	}
	for id, provider := range run.ProviderMappings {
		if !safeID(id) {
			return nil, errors.New("invalid provider mapping dependency id")
		}
		if err := provider.Validate(); err != nil {
			return nil, err
		}
	}
	if run.InventoryScope != "configured_and_observed" || run.ProvidedAPIs == nil || run.Dependencies == nil || run.Attempts == nil {
		return nil, errors.New("missing or unsupported API inventory")
	}
	// Preserve declared observation references while rebuilding the configured
	// boundaries. This catches a stale/edited target kind before discovery can
	// silently omit a mapped mock. Full Load later checks the observations too.
	provided, dependent := apiInventory(run, nil)
	if !sameJSON(provided, run.ProvidedAPIs) || !sameJSON(dependent, run.Dependencies) {
		return nil, errors.New("API inventory disagrees with manifest declarations")
	}
	return &run, nil
}
