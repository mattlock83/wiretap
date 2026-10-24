package assumptions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/bundle"
)

func storeConsumerIdentity(application string) bundle.Identity {
	identity := consumerCheckIdentity()
	identity.Application.ID = application
	identity.ProviderMappings = map[string]bundle.ProviderRef{"stock": {ApplicationID: "inventory", APIID: "inventory-api"}}
	return identity
}

func putStoreArchive(t *testing.T, root, application, suffix, source string) string {
	t.Helper()
	destination := filepath.Join(root, application, suffix)
	if err := os.CopyFS(destination, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	return canonicalStorePath(t, destination)
}

func canonicalStorePath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func requireStoreInputError(t *testing.T, options StoreOptions, message string) {
	t.Helper()
	_, err := PlanStore(t.Context(), options)
	if !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), message) {
		t.Fatalf("wanted input error containing %q, got %v", message, err)
	}
}

func TestPlanStoreConsumerResolvesEveryMappedMockDeterministically(t *testing.T) {
	root := t.TempDir()
	identity := storeConsumerIdentity("shop")
	identity.DependencyKinds["audit"] = "mock"
	identity.DependencyKinds["real"] = "service"
	identity.ProviderMappings["audit"] = bundle.ProviderRef{ApplicationID: "audit", APIID: "audit-api"}
	consumer := writeCheckArchive(t, identity, "complete")
	provider := putStoreArchive(t, root, "inventory", "inventory-api/build/run", writeCheckArchive(t, providerCheckIdentity(), "complete"))
	auditIdentity := providerCheckIdentity()
	auditIdentity.Application.ID, auditIdentity.APIID = "audit", "audit-api"
	audit := putStoreArchive(t, root, "audit", "run", writeCheckArchive(t, auditIdentity, "complete"))
	plan, err := PlanStore(t.Context(), StoreOptions{Mode: "consumer", Consumer: filepath.Join(consumer, "bundle.json"), Artifacts: root})
	if err != nil {
		t.Fatal(err)
	}
	expected := []Options{
		{Mode: "consumer", Consumer: canonicalStorePath(t, consumer), Provider: audit, Dependency: "audit"},
		{Mode: "consumer", Consumer: canonicalStorePath(t, consumer), Provider: provider, Dependency: "stock"},
	}
	if plan.Root != canonicalStorePath(t, root) || !reflect.DeepEqual(plan.Checks, expected) {
		t.Fatalf("incorrect deterministic plan: %+v", plan)
	}
	for _, path := range []string{consumer, provider, audit} {
		path = canonicalStorePath(t, path)
		digest, err := manifestDigest(path)
		if err != nil || plan.digests[path] != digest {
			t.Fatalf("missing discovery digest for %s: %q / %v", path, plan.digests[path], err)
		}
	}
}

func TestPlanStoreConsumerRequiresMappingForEachSelectedMock(t *testing.T) {
	root := t.TempDir()
	putStoreArchive(t, root, "inventory", "run", writeCheckArchive(t, providerCheckIdentity(), "complete"))
	identity := storeConsumerIdentity("shop")
	identity.DependencyKinds["unmapped"] = "mock"
	consumer := writeCheckArchive(t, identity, "complete")
	options := StoreOptions{Mode: "consumer", Consumer: consumer, Artifacts: root}
	requireStoreInputError(t, options, `mock dependency "unmapped" has no provider mapping`)
	options.Dependency = "stock"
	plan, err := PlanStore(t.Context(), options)
	if err != nil || len(plan.Checks) != 1 || plan.Checks[0].Dependency != "stock" {
		t.Fatalf("explicit dependency did not narrow selection: %+v / %v", plan, err)
	}
	options.Dependency = "unknown"
	requireStoreInputError(t, options, "must be a declared mock")
}

func TestPlanStoreConsumerUsesDeclaredVersionAndNeverNewestRun(t *testing.T) {
	root := t.TempDir()
	provider := providerCheckIdentity()
	first := putStoreArchive(t, root, "inventory", "v1/run", writeCheckArchive(t, provider, "complete"))
	provider.SUTSource["version"] = "v2"
	putStoreArchive(t, root, "inventory", "v2/run", writeCheckArchive(t, provider, "complete"))
	consumerIdentity := storeConsumerIdentity("shop")
	consumer := writeCheckArchive(t, consumerIdentity, "complete")
	options := StoreOptions{Mode: "consumer", Consumer: consumer, Artifacts: root}
	requireStoreInputError(t, options, "multiple artifact snapshots")
	consumerIdentity.ProviderMappings["stock"] = bundle.ProviderRef{ApplicationID: "inventory", APIID: "inventory-api", Version: "v1"}
	options.Consumer = writeCheckArchive(t, consumerIdentity, "complete")
	plan, err := PlanStore(t.Context(), options)
	if err != nil || len(plan.Checks) != 1 || plan.Checks[0].Provider != first {
		t.Fatalf("version pin did not select the intended run: %+v / %v", plan, err)
	}
	provider.SUTSource["version"] = "v1"
	putStoreArchive(t, root, "inventory", "v1/retry", writeCheckArchive(t, provider, "complete"))
	requireStoreInputError(t, options, "multiple artifact snapshots")
	consumerIdentity.ProviderMappings["stock"] = bundle.ProviderRef{ApplicationID: "inventory", APIID: "inventory-api", Version: "absent"}
	options.Consumer = writeCheckArchive(t, consumerIdentity, "complete")
	requireStoreInputError(t, options, `no artifact snapshot matches provider application "inventory" API "inventory-api" version "absent"`)
}

func TestPlanStoreProviderKeepsVersionMismatchesAndEveryMappedDependency(t *testing.T) {
	root := t.TempDir()
	provider := writeCheckArchive(t, providerCheckIdentity(), "complete")
	identity := storeConsumerIdentity("shop")
	identity.DependencyKinds["warehouse"] = "mock"
	identity.ProviderMappings["warehouse"] = bundle.ProviderRef{ApplicationID: "inventory", APIID: "inventory-api", Version: "future"}
	shop := putStoreArchive(t, root, "shop", "run", writeCheckArchive(t, identity, "complete"))
	other := putStoreArchive(t, root, "other", "run", writeCheckArchive(t, storeConsumerIdentity("other"), "complete"))
	plan, err := PlanStore(t.Context(), StoreOptions{Mode: "provider", Provider: provider, Artifacts: root})
	if err != nil {
		t.Fatal(err)
	}
	expected := []Options{
		{Mode: "provider", Consumer: other, Provider: canonicalStorePath(t, provider), Dependency: "stock"},
		{Mode: "provider", Consumer: shop, Provider: canonicalStorePath(t, provider), Dependency: "stock"},
		{Mode: "provider", Consumer: shop, Provider: canonicalStorePath(t, provider), Dependency: "warehouse"},
	}
	if !reflect.DeepEqual(plan.Checks, expected) {
		t.Fatalf("version mismatch or dependency disappeared: %+v", plan.Checks)
	}
}

func TestPlanStoreProviderRejectsAmbiguousConsumerHistory(t *testing.T) {
	root := t.TempDir()
	provider := writeCheckArchive(t, providerCheckIdentity(), "complete")
	identity := storeConsumerIdentity("shop")
	putStoreArchive(t, root, "shop", "old/run", writeCheckArchive(t, identity, "complete"))
	identity.SUTSource = map[string]any{"version": "new"}
	putStoreArchive(t, root, "shop", "new/run", writeCheckArchive(t, identity, "complete"))
	requireStoreInputError(t, StoreOptions{Mode: "provider", Provider: provider, Artifacts: root}, `multiple artifact snapshots match consumer application "shop" API "shop-api"`)
}

func TestPlanStoreNeverUsesTheAnchorAsItsOwnPeer(t *testing.T) {
	root := t.TempDir()
	identity := storeConsumerIdentity("inventory")
	identity.APIID = "inventory-api"
	anchor := writeCheckArchive(t, identity, "complete")
	putStoreArchive(t, root, "inventory", "copied-anchor", anchor)
	requireStoreInputError(t, StoreOptions{Mode: "consumer", Consumer: anchor, Artifacts: root}, "no artifact snapshot matches")
	requireStoreInputError(t, StoreOptions{Mode: "provider", Provider: anchor, Artifacts: root}, "no consumer mock mappings")
}

func TestPlanStoreRejectsChangedCopyOfAnchorRatherThanHidingItsMappings(t *testing.T) {
	root := t.TempDir()
	identity := storeConsumerIdentity("inventory")
	identity.APIID = "inventory-api"
	anchor := writeCheckArchive(t, identity, "complete")
	copied := putStoreArchive(t, root, "inventory", "copied-anchor", anchor)
	path := filepath.Join(copied, "bundle.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["revision"] = 999
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	requireStoreInputError(t, StoreOptions{Mode: "provider", Provider: anchor, Artifacts: root}, "different manifests")
}

func TestPlanStoreDoesNotHideIncompleteSnapshots(t *testing.T) {
	root := t.TempDir()
	consumer := writeCheckArchive(t, storeConsumerIdentity("shop"), "complete")
	provider := putStoreArchive(t, root, "inventory", "run", writeCheckArchive(t, providerCheckIdentity(), "open"))
	plan, err := PlanStore(t.Context(), StoreOptions{Mode: "consumer", Consumer: consumer, Artifacts: root})
	if err != nil || len(plan.Checks) != 1 || plan.Checks[0].Provider != provider {
		t.Fatalf("incomplete run was silently filtered from selection: %+v / %v", plan, err)
	}
	// The full comparison, rather than discovery, supplies the failure verdict.
	report, err := Check(t.Context(), plan.Checks[0])
	if err != nil || !findingCode(report, "provider_run_incomplete") {
		t.Fatalf("incomplete snapshot did not retain its useful finding: %+v / %v", report, err)
	}
}

func TestPlanStoreRejectsIncorrectApplicationFolderAndMalformedManifests(t *testing.T) {
	consumer := writeCheckArchive(t, storeConsumerIdentity("shop"), "complete")
	for _, mutation := range []string{"folder", "duplicate", "trailing", "oversize", "manifest symlink", "directory symlink"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			provider := writeCheckArchive(t, providerCheckIdentity(), "complete")
			folder := "inventory"
			if mutation == "folder" {
				folder = "other"
			}
			destination := putStoreArchive(t, root, folder, "run", provider)
			manifest := filepath.Join(destination, "bundle.json")
			message := ""
			switch mutation {
			case "folder":
				message = "top-level application folder"
			case "duplicate":
				data, err := os.ReadFile(manifest)
				if err != nil {
					t.Fatal(err)
				}
				data = append([]byte(`{"run_id":"ambiguous",`), data[1:]...)
				if err := os.WriteFile(manifest, data, 0600); err != nil {
					t.Fatal(err)
				}
				message = "duplicate JSON key"
			case "trailing":
				file, err := os.OpenFile(manifest, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteString("{}")
				if closeErr := file.Close(); err != nil || closeErr != nil {
					t.Fatalf("append JSON: %v / %v", err, closeErr)
				}
				message = "extra JSON content"
			case "oversize":
				if err := os.Truncate(manifest, maxStoreManifestBytes+1); err != nil {
					t.Fatal(err)
				}
				message = "manifest exceeds"
			case "manifest symlink":
				if err := os.Remove(manifest); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(provider, "bundle.json"), manifest); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				message = "regular file"
			case "directory symlink":
				if err := os.Symlink(provider, filepath.Join(root, "linked")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				message = "symbolic link"
			}
			requireStoreInputError(t, StoreOptions{Mode: "consumer", Consumer: consumer, Artifacts: root}, message)
		})
	}
}

func TestPlanStoreBoundsDirectoryTraversalAndStopsAtRun(t *testing.T) {
	root := t.TempDir()
	consumer := writeCheckArchive(t, storeConsumerIdentity("shop"), "complete")
	deep := filepath.Join(root, strings.Repeat("deep/", maxStoreDepth+1))
	if err := os.MkdirAll(deep, 0700); err != nil {
		t.Fatal(err)
	}
	requireStoreInputError(t, StoreOptions{Mode: "consumer", Consumer: consumer, Artifacts: root}, "nesting exceeds")
	root = t.TempDir()
	provider := putStoreArchive(t, root, "inventory", "run", writeCheckArchive(t, providerCheckIdentity(), "complete"))
	if err := os.MkdirAll(filepath.Join(provider, "payloads", strings.Repeat("deep/", maxStoreDepth+1)), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanStore(t.Context(), StoreOptions{Mode: "consumer", Consumer: consumer, Artifacts: root}); err != nil {
		t.Fatalf("discovery traversed an atomic run archive: %v", err)
	}
}

func TestPlanStoreCannotIgnoreUnknownOrCorruptManifestRelationships(t *testing.T) {
	provider := writeCheckArchive(t, providerCheckIdentity(), "complete")
	for _, mutation := range []string{"format", "capability", "kind"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			putStoreArchive(t, root, "valid", "run", writeCheckArchive(t, storeConsumerIdentity("valid"), "complete"))
			consumer := putStoreArchive(t, root, "corrupt", "run", writeCheckArchive(t, storeConsumerIdentity("corrupt"), "complete"))
			path := filepath.Join(consumer, "bundle.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var manifest bundle.Run
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			message := ""
			switch mutation {
			case "format":
				manifest.EvidenceVersion = "4.0"
				manifest.ProviderMappings = nil // The future format may encode these elsewhere.
				message = "unsupported evidence version"
			case "capability":
				manifest.RequiredCapabilities = []string{"future-mappings.v1"}
				manifest.ProviderMappings = nil
				message = "unsupported required capability"
			case "kind":
				manifest.DependencyKinds["stock"] = "service" // Contradicts the stored inventory.
				message = "inventory disagrees"
			}
			data, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			requireStoreInputError(t, StoreOptions{Mode: "provider", Provider: provider, Artifacts: root}, message)
		})
	}
}

func TestPlanStoreRejectsEmptyAndConflictingSelections(t *testing.T) {
	root := t.TempDir()
	consumer := writeCheckArchive(t, consumerCheckIdentity(), "complete")
	provider := writeCheckArchive(t, providerCheckIdentity(), "complete")
	requireStoreInputError(t, StoreOptions{Mode: "provider", Provider: provider, Artifacts: root}, "empty comparison")
	requireStoreInputError(t, StoreOptions{Mode: "provider", Provider: provider, Consumer: consumer, Artifacts: root}, "omits --consumer")
	requireStoreInputError(t, StoreOptions{Mode: "consumer", Provider: provider, Consumer: consumer, Artifacts: root}, "omits --provider")
	requireStoreInputError(t, StoreOptions{Mode: "provider", Provider: provider, Dependency: "stock", Artifacts: root}, "requires an explicit --consumer")
	requireStoreInputError(t, StoreOptions{Mode: "consumer", Consumer: consumer}, "requires a local directory")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PlanStore(ctx, StoreOptions{Mode: "provider", Provider: provider, Artifacts: root}); !errors.Is(err, context.Canceled) || errors.Is(err, ErrInput) {
		t.Fatalf("cancellation lost its identity: %v", err)
	}
}

func TestPlanStoreConsumerExplainsApplicationIdentityThatCannotNameOneFolder(t *testing.T) {
	identity := storeConsumerIdentity("shop")
	identity.ProviderMappings["stock"] = bundle.ProviderRef{ApplicationID: "example/inventory", APIID: "inventory-api"}
	consumer := writeCheckArchive(t, identity, "complete")
	requireStoreInputError(t, StoreOptions{Mode: "consumer", Consumer: consumer, Artifacts: t.TempDir()}, "cannot be one top-level application folder")
}
