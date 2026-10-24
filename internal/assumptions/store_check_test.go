package assumptions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreVerdictRequiresUnchangedDiscoveryInputs(t *testing.T) {
	for _, mutation := range []string{"unrelated manifest", "new consumer", "removed snapshot", "anchor", "unchanged"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			consumer := writeCheckArchive(t, storeConsumerIdentity("shop"), "complete")
			putStoreArchive(t, root, "inventory", "run", writeCheckArchive(t, providerCheckIdentity(), "complete"))
			unrelatedIdentity := providerCheckIdentity()
			unrelatedIdentity.Application.ID = "unrelated"
			unrelated := putStoreArchive(t, root, "unrelated", "run", writeCheckArchive(t, unrelatedIdentity, "complete"))
			plan, err := PlanStore(t.Context(), StoreOptions{Mode: "consumer", Consumer: consumer, Artifacts: root})
			if err != nil {
				t.Fatal(err)
			}
			anchor := plan.Checks[0].Consumer
			switch mutation {
			case "anchor", "unrelated manifest":
				path := filepath.Join(unrelated, "bundle.json")
				if mutation == "anchor" {
					path = filepath.Join(anchor, "bundle.json")
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				// Even a valid manifest rewrite changes the snapshot used to select
				// relationships and must not be hidden by already verified pairs.
				if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			case "new consumer":
				putStoreArchive(t, root, "billing", "run", writeCheckArchive(t, storeConsumerIdentity("billing"), "complete"))
			case "removed snapshot":
				if err := os.RemoveAll(unrelated); err != nil {
					t.Fatal(err)
				}
			}
			err = verifyStoreSelection(t.Context(), plan, anchor)
			if mutation == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "changed during comparison") {
				t.Fatalf("changed discovery inputs accepted: %v", err)
			}
		})
	}
}
