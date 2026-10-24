package assumptions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/bundle"
)

const (
	maxStoreDepth         = 32
	maxStoreEntries       = 100000
	maxStoreSnapshots     = 1024
	maxStoreManifestBytes = 64 << 20
	maxStoreTotalBytes    = 256 << 20
)

// StoreOptions selects a local capture and a downloaded, extracted artifact
// store. Each top-level application folder contains complete run directories.
// Consumer checks resolve providers; provider checks resolve mapped consumers.
type StoreOptions struct {
	Mode, Consumer, Provider, Dependency, Artifacts string
}

// StorePlan records a deterministic set of independent comparisons. It never
// merges examples from different runs, selects the newest run, or fetches files.
// Check still validates every selected archive and its own Postman exports.
type StorePlan struct {
	Root    string
	Checks  []Options
	digests map[string]string
}

type storeSnapshot struct {
	root   string
	digest string
	run    bundle.Run
}

// PlanStore resolves application/API mappings using bounded manifest reads.
// Discovery is only selection: full archive and collection verification remains
// the responsibility of Check, including incomplete runs and version mismatch.
func PlanStore(ctx context.Context, options StoreOptions) (*StorePlan, error) {
	plan, err := planStore(ctx, options)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return plan, err
	}
	return nil, fmt.Errorf("%w: %v", ErrInput, err)
}

func planStore(ctx context.Context, options StoreOptions) (*StorePlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var localPath string
	switch options.Mode {
	case "consumer":
		if options.Consumer == "" || options.Provider != "" {
			return nil, errors.New("--artifacts with check consumer requires --consumer and omits --provider; use explicit --consumer and --provider without --artifacts for one pair")
		}
		localPath = options.Consumer
	case "provider":
		if options.Provider == "" || options.Consumer != "" {
			return nil, errors.New("--artifacts with check provider requires --provider and omits --consumer; use explicit --consumer and --provider without --artifacts for one pair")
		}
		if options.Dependency != "" {
			return nil, errors.New("--dependency with check provider requires an explicit --consumer; artifact discovery checks every mapped consumer dependency")
		}
		localPath = options.Provider
	default:
		return nil, errors.New("check requires consumer or provider")
	}
	if strings.TrimSpace(options.Artifacts) == "" {
		return nil, errors.New("--artifacts requires a local directory containing application folders")
	}
	local, err := readStoreLocal(localPath)
	if err != nil {
		return nil, fmt.Errorf("%s capture: %w", options.Mode, err)
	}
	root, entries, err := discoverStore(ctx, options.Artifacts)
	if err != nil {
		return nil, err
	}
	plan := &StorePlan{Root: root, Checks: []Options{}, digests: map[string]string{}}
	byRunID := map[string]storeSnapshot{local.run.RunID: local}
	for _, entry := range entries {
		if previous, exists := byRunID[entry.run.RunID]; exists && previous.digest != entry.digest {
			return nil, fmt.Errorf("artifact snapshots %q and %q share run ID %q but have different manifests; retain one immutable snapshot of each run", previous.root, entry.root, entry.run.RunID)
		}
		if entry.root == local.root && entry.digest != local.digest {
			return nil, errors.New("local capture manifest changed during discovery; use a stable completed archive")
		}
		byRunID[entry.run.RunID] = entry
		plan.digests[entry.root] = entry.digest
	}
	plan.digests[local.root] = local.digest
	if options.Mode == "consumer" {
		plan.Checks, err = planStoreConsumer(local, entries, options.Dependency)
	} else {
		plan.Checks, err = planStoreProvider(local, entries)
	}
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func planStoreConsumer(consumer storeSnapshot, entries []storeSnapshot, explicit string) ([]Options, error) {
	var dependencies []string
	if explicit != "" {
		if consumer.run.DependencyKinds[explicit] != "mock" {
			return nil, fmt.Errorf("dependency %q must be a declared mock in the consumer capture", explicit)
		}
		dependencies = append(dependencies, explicit)
	} else {
		for id, kind := range consumer.run.DependencyKinds {
			if kind == "mock" {
				dependencies = append(dependencies, id)
			}
		}
		sort.Strings(dependencies)
	}
	if len(dependencies) == 0 {
		return nil, errors.New("consumer capture has no mock dependencies to check")
	}
	checks := make([]Options, 0, len(dependencies))
	for _, dependency := range dependencies {
		mapping, exists := consumer.run.ProviderMappings[dependency]
		if !exists {
			return nil, fmt.Errorf("mock dependency %q has no provider mapping; declare application_id and api_id, or use explicit --consumer and --provider inputs", dependency)
		}
		if mapping.ApplicationID == "." || mapping.ApplicationID == ".." || strings.ContainsAny(mapping.ApplicationID, `/\\`) {
			return nil, fmt.Errorf("mock dependency %q maps to application %q, which cannot be one top-level application folder; use explicit --consumer and --provider inputs for this identity", dependency, mapping.ApplicationID)
		}
		var candidates []storeSnapshot
		for _, entry := range entries {
			if sameStoreSnapshot(consumer, entry) {
				continue
			}
			if entry.run.Application.ID != mapping.ApplicationID || providerAPI(&entry.run) != mapping.APIID {
				continue
			}
			version, _ := entry.run.SUTSource["version"].(string)
			if mapping.Version != "" && version != mapping.Version {
				continue
			}
			candidates = append(candidates, entry)
		}
		identity := fmt.Sprintf("provider application %q API %q", mapping.ApplicationID, mapping.APIID)
		if mapping.Version != "" {
			identity += fmt.Sprintf(" version %q", mapping.Version)
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("mock dependency %q: no artifact snapshot matches %s; download the intended complete run into its application folder", dependency, identity)
		}
		if len(candidates) > 1 {
			return nil, ambiguousStoreSnapshots(identity, candidates)
		}
		checks = append(checks, Options{Mode: "consumer", Consumer: consumer.root, Provider: candidates[0].root, Dependency: dependency})
	}
	return checks, nil
}

func planStoreProvider(provider storeSnapshot, entries []storeSnapshot) ([]Options, error) {
	if provider.run.Application == nil || strings.TrimSpace(provider.run.Application.ID) == "" || providerAPI(&provider.run) == "" {
		return nil, errors.New("provider discovery requires captured application.id and API identity")
	}
	// A store used as a CI comparison set must select one snapshot per consumer
	// application/API. Otherwise historic consumers can create misleading gates.
	groups := map[string][]storeSnapshot{}
	for _, entry := range entries {
		if sameStoreSnapshot(provider, entry) {
			continue
		}
		if len(mappedStoreDependencies(entry.run, provider.run)) == 0 {
			continue
		}
		key := entry.run.Application.ID + "\x00" + providerAPI(&entry.run)
		groups[key] = append(groups[key], entry)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var checks []Options
	for _, key := range keys {
		candidates := groups[key]
		if len(candidates) != 1 {
			return nil, ambiguousStoreSnapshots(fmt.Sprintf("consumer application %q API %q", candidates[0].run.Application.ID, providerAPI(&candidates[0].run)), candidates)
		}
		consumer := candidates[0]
		for _, dependency := range mappedStoreDependencies(consumer.run, provider.run) {
			checks = append(checks, Options{Mode: "provider", Consumer: consumer.root, Provider: provider.root, Dependency: dependency})
		}
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("no consumer mock mappings in the artifact store target provider application %q API %q; an empty comparison cannot establish compatibility", provider.run.Application.ID, providerAPI(&provider.run))
	}
	return checks, nil
}

func sameStoreSnapshot(left, right storeSnapshot) bool {
	return left.root == right.root || left.run.RunID == right.run.RunID
}

func mappedStoreDependencies(consumer, provider bundle.Run) []string {
	var ids []string
	for id, mapping := range consumer.ProviderMappings {
		if consumer.DependencyKinds[id] == "mock" && mapping.ApplicationID == provider.Application.ID && mapping.APIID == providerAPI(&provider) {
			// Do not filter by mapped version here. Check must expose a consumer
			// pinned to a different provider version instead of silently omitting it.
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func ambiguousStoreSnapshots(identity string, candidates []storeSnapshot) error {
	var descriptions []string
	for _, candidate := range candidates {
		version, _ := candidate.run.SUTSource["version"].(string)
		descriptions = append(descriptions, fmt.Sprintf("%q (run %q, version %q)", candidate.root, candidate.run.RunID, version))
	}
	return fmt.Errorf("multiple artifact snapshots match %s: %s; select one intended run per application/API in the artifact directory, pin the mapped provider version, or use explicit --consumer and --provider inputs", identity, strings.Join(descriptions, "; "))
}

func readStoreLocal(input string) (storeSnapshot, error) {
	root, err := filepath.Abs(input)
	if err != nil {
		return storeSnapshot{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return storeSnapshot{}, err
	}
	if !info.IsDir() {
		if filepath.Base(root) != "bundle.json" {
			return storeSnapshot{}, errors.New("use the run directory or its bundle.json manifest; retain the complete archive")
		}
		root = filepath.Dir(root)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return storeSnapshot{}, err
	}
	entry, _, err := readStoreManifest(root)
	return entry, err
}

func discoverStore(ctx context.Context, input string) (string, []storeSnapshot, error) {
	root, err := filepath.Abs(input)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", nil, fmt.Errorf("artifact directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("--artifacts must be a directory, not a symbolic link")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, err
	}
	var snapshots []storeSnapshot
	var totalBytes int64
	entriesRead := 0
	var walk func(string, string, int) error
	walk = func(directory, application string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > maxStoreDepth {
			return fmt.Errorf("artifact directory nesting exceeds %d levels", maxStoreDepth)
		}
		entries, err := readStoreDirectory(directory, &entriesRead)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() != "bundle.json" {
				continue
			}
			if application == "" {
				return errors.New("--artifacts must contain application folders; use --consumer or --provider for a single run directory")
			}
			if len(snapshots) >= maxStoreSnapshots {
				return fmt.Errorf("artifact directory exceeds %d run manifests", maxStoreSnapshots)
			}
			snapshot, size, err := readStoreManifest(directory)
			if err != nil {
				return fmt.Errorf("artifact snapshot %q: %w", directory, err)
			}
			totalBytes += size
			if totalBytes > maxStoreTotalBytes {
				return fmt.Errorf("artifact manifests exceed %d MiB in total", maxStoreTotalBytes>>20)
			}
			if snapshot.run.Application == nil || snapshot.run.Application.ID != application {
				return fmt.Errorf("artifact snapshot %q must declare application.id %q to match its top-level application folder", directory, application)
			}
			snapshots = append(snapshots, snapshot)
			return nil // A run is an atomic archive; never search its payloads.
		}
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("artifact entry %q must not be a symbolic link", path)
			}
			if !entry.IsDir() {
				continue
			}
			app := application
			if depth == 0 {
				app = entry.Name()
			}
			if err := walk(path, app, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, "", 0); err != nil {
		return "", nil, err
	}
	return root, snapshots, nil
}

func readStoreDirectory(path string, entriesRead *int) ([]fs.DirEntry, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	var result []fs.DirEntry
	for {
		entries, err := directory.ReadDir(256)
		*entriesRead += len(entries)
		if *entriesRead > maxStoreEntries {
			return nil, fmt.Errorf("artifact directory exceeds %d entries", maxStoreEntries)
		}
		result = append(result, entries...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name() < result[j].Name() })
	return result, nil
}

func readStoreManifest(root string) (storeSnapshot, int64, error) {
	path := filepath.Join(root, "bundle.json")
	info, err := os.Lstat(path)
	if err != nil {
		return storeSnapshot{}, 0, err
	}
	if !info.Mode().IsRegular() {
		return storeSnapshot{}, 0, errors.New("bundle.json must be a regular file")
	}
	if info.Size() > maxStoreManifestBytes {
		return storeSnapshot{}, 0, fmt.Errorf("run manifest exceeds %d MiB", maxStoreManifestBytes>>20)
	}
	file, err := os.Open(path)
	if err != nil {
		return storeSnapshot{}, 0, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return storeSnapshot{}, 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return storeSnapshot{}, 0, errors.New("bundle.json changed during discovery")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStoreManifestBytes+1))
	if err != nil {
		return storeSnapshot{}, 0, err
	}
	if len(data) > maxStoreManifestBytes {
		return storeSnapshot{}, 0, fmt.Errorf("run manifest exceeds %d MiB", maxStoreManifestBytes>>20)
	}
	run, err := bundle.DecodeManifest(data)
	if err != nil {
		return storeSnapshot{}, 0, err
	}
	digest := sha256.Sum256(data)
	return storeSnapshot{root: root, digest: hex.EncodeToString(digest[:]), run: *run}, int64(len(data)), nil
}
