// Package runset groups immutable archives from one application build. It never
// rewrites recordings or combines a request from one exchange with another response.
package runset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/schemajson"
	"github.com/mattlock83/wiretap/internal/version"
)

const Version = "1.0"
const MaxMembers = 64

// ErrOutputPath distinguishes destination resolution failures from invalid
// input archives when preparing a manifest.
var ErrOutputPath = errors.New("run set output path")

type Member struct {
	Path           string `json:"path"`
	ManifestSHA256 string `json:"manifest_sha256"`
}
type Manifest struct {
	Version string   `json:"run_set_version"`
	Members []Member `json:"members"`
}
type Archive struct {
	Path     string
	Digest   string
	Run      *bundle.Run
	Report   *coverage.Report
	Captures []*artifact.Artifact
}
type Set struct {
	Manifest Manifest
	Archives []Archive
}

// Create validates every archive and pins each manifest. Paths in the returned
// document are relative to its destination, so a directory tree can be copied.
func Create(ctx context.Context, paths []string, destination string) (*Manifest, error) {
	if len(paths) == 0 || len(paths) > MaxMembers {
		return nil, fmt.Errorf("run set requires 1–%d members", MaxMembers)
	}
	manifest := Manifest{Version: Version, Members: []Member{}}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root, err := archiveRoot(path)
		if err != nil {
			return nil, err
		}
		digest, err := Digest(root)
		if err != nil {
			return nil, err
		}
		manifest.Members = append(manifest.Members, Member{root, digest})
	}
	set, err := load(ctx, manifest, "")
	if err != nil {
		return nil, err
	}
	directory, err := filepath.Abs(filepath.Dir(destination))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOutputPath, err)
	}
	// Output writers resolve directory aliases before creating the manifest.
	// Compute relative members from that same physical directory, so loading
	// through its real path or copying the tree preserves their meaning.
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOutputPath, err)
	}
	for i, archive := range set.Archives {
		path, err := filepath.Rel(directory, archive.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrOutputPath, err)
		}
		manifest.Members[i] = Member{filepath.ToSlash(path), archive.Digest}
	}
	return &manifest, nil
}

func Load(ctx context.Context, path string) (*Set, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	data, err := readFile(path, 1<<20)
	if err != nil {
		return nil, err
	}
	// Match the existing contract decoder's duplicate-key rejection. Ordinary
	// encoding/json decoding otherwise silently selects the last declaration.
	if _, err := schemajson.Decode(data); err != nil {
		return nil, fmt.Errorf("run set: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("run set: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("run set must contain one JSON document")
	}
	return load(ctx, manifest, filepath.Dir(path))
}

func load(ctx context.Context, manifest Manifest, directory string) (*Set, error) {
	if manifest.Version != Version || len(manifest.Members) == 0 || len(manifest.Members) > MaxMembers {
		return nil, fmt.Errorf("run set requires version %s and 1–%d members", Version, MaxMembers)
	}
	set := &Set{Manifest: manifest}
	seen := map[string]bool{}
	var identity any
	for _, member := range manifest.Members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := member.Path
		if strings.TrimSpace(path) == "" {
			return nil, errors.New("run set member path is empty")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(directory, filepath.FromSlash(path))
		}
		root, err := archiveRoot(path)
		if err != nil {
			return nil, err
		}
		digest, err := Digest(root)
		if err != nil {
			return nil, err
		}
		if len(member.ManifestSHA256) != 64 || digest != member.ManifestSHA256 {
			return nil, fmt.Errorf("run set member %q changed or has an invalid manifest hash", path)
		}
		run, captures, err := bundle.Load(root)
		if err != nil {
			return nil, fmt.Errorf("run set member %q: %w", path, err)
		}
		if run.RunState != "complete" || len(run.Pending) != 0 {
			return nil, fmt.Errorf("run set member %q is not complete", path)
		}
		if seen[root] || seen[run.RunID] {
			return nil, fmt.Errorf("duplicate run set member %q", path)
		}
		seen[root], seen[run.RunID] = true, true
		if run.Application == nil || run.Application.ID == "" || run.APIID == "" {
			return nil, fmt.Errorf("run set member %q requires application.id and sut.id", path)
		}
		if len(manifest.Members) > 1 && !knownBuild(run.SUTSource) {
			return nil, errors.New("multiple run set members require matching sut_source.revision or sut_source.build_id; declare the tested build in .wiretap.yaml")
		}
		report, err := coverage.GenerateBundleSnapshot(root, run, captures, "", false)
		if err != nil {
			return nil, err
		}
		contracts := []string{}
		if report.Contracts != nil {
			for _, spec := range report.Contracts.Specs {
				contracts = append(contracts, strings.Join([]string{spec.APIID, spec.SchemaID, spec.Origin, spec.TargetKind, spec.RulesVersion, spec.Revision}, "\x00"))
			}
		}
		sort.Strings(contracts)
		current := []any{run.Application.ID, run.APIID, run.SUTTargetKind, run.SUTSource, run.TestSource, run.DependencyKinds, run.ProviderMappings, contracts}
		if identity != nil && !reflect.DeepEqual(identity, current) {
			return nil, errors.New("run set members must share application/API, tested build, test source, boundary kinds, provider mappings and frozen contracts")
		}
		identity = current
		if after, err := Digest(root); err != nil || after != digest {
			return nil, fmt.Errorf("run set member %q changed during loading", path)
		}
		set.Archives = append(set.Archives, Archive{Path: root, Digest: digest, Run: run, Report: report, Captures: captures})
	}
	// Member order carries no priority. Stable ordering makes the selected
	// witness and aggregate evidence independent of --bundle/manifest order.
	sort.Slice(set.Archives, func(i, j int) bool { return set.Archives[i].Run.RunID < set.Archives[j].Run.RunID })
	return set, nil
}

func knownBuild(source map[string]any) bool {
	for _, key := range []string{"revision", "build_id"} {
		if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func archiveRoot(path string) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if filepath.Base(root) == "bundle.json" {
		root = filepath.Dir(root)
	}
	return filepath.EvalSymlinks(root)
}

func readFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%q must be a regular file of at most %d bytes", path, limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%q must be a regular file of at most %d bytes", path, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%q exceeds %d bytes", path, limit)
	}
	return data, err
}

func Digest(root string) (string, error) {
	data, err := readFile(filepath.Join(root, "bundle.json"), 64<<20)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// Coverage keeps per-run reports intact and unions stable condition keys. The
// denominator is counted once; test attempts and Arazzo documents remain separate.
type Coverage struct {
	ReportVersion string             `json:"report_version"`
	Producer      version.Producer   `json:"producer"`
	Scope         string             `json:"scope"`
	Members       []*coverage.Report `json:"members"`
	Conditions    []Condition        `json:"conditions"`
}
type Condition struct {
	APIID        string                      `json:"api_id"`
	SchemaID     string                      `json:"schema_id"`
	Origin       string                      `json:"origin"`
	Revision     string                      `json:"revision"`
	RulesVersion string                      `json:"rules_version"`
	Key          string                      `json:"key"`
	Operation    string                      `json:"operation"`
	Family       string                      `json:"family"`
	State        string                      `json:"state"`
	Evidence     []coverage.ContractEvidence `json:"evidence,omitempty"`
}

func (s *Set) Coverage() Coverage {
	result := Coverage{ReportVersion: Version, Producer: version.Current(), Scope: "same_build_run_set", Members: []*coverage.Report{}, Conditions: []Condition{}}
	conditions := map[string]*Condition{}
	for _, archive := range s.Archives {
		result.Members = append(result.Members, archive.Report)
		if archive.Report.Contracts == nil {
			continue
		}
		for _, spec := range archive.Report.Contracts.Specs {
			for _, target := range spec.Targets {
				key := strings.Join([]string{spec.APIID, spec.SchemaID, spec.Origin, target.Key}, "\x00")
				condition := conditions[key]
				if condition == nil {
					condition = &Condition{APIID: spec.APIID, SchemaID: spec.SchemaID, Origin: spec.Origin, Revision: spec.Revision, RulesVersion: spec.RulesVersion, Key: target.Key, Operation: target.Operation, Family: target.Family, State: target.State}
					conditions[key] = condition
				}
				condition.State = unionState(condition.State, target.State)
				condition.Evidence = append(condition.Evidence, target.Evidence...)
			}
		}
	}
	keys := make([]string, 0, len(conditions))
	for key := range conditions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.Conditions = append(result.Conditions, *conditions[key])
	}
	return result
}

// A concrete witness covers a condition; without one, uncertainty must survive
// regardless of shard order. Unsupported/excluded conditions remain explicit.
func unionState(a, b string) string {
	rank := map[string]int{"excluded": 1, "not_covered": 2, "unsupported": 3, "unknown": 4, "covered": 5}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
