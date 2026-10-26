// Package bundle stores a run's immutable captures, frozen contracts and payloads.
package bundle

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/version"
)

const EvidenceVersion = "3.1"

// ErrInput identifies an unreadable or invalid capture archive. Callers can
// distinguish input failures from errors writing derived reports and exports.
var ErrInput = errors.New("invalid capture archive")

type FileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type AttemptRef struct {
	AttemptID string `json:"attempt_id"`
	FileRef
}

type PendingAttempt struct {
	Identity  *artifact.TestIdentity `json:"identity,omitempty"`
	AttemptID string                 `json:"attempt_id"`
	Name      string                 `json:"name"`
	StartedAt time.Time              `json:"started_at"`
	Meta      map[string]any         `json:"meta,omitempty"`
}

// ProviderRef is declared routing knowledge, never proof of provider agreement.
type ProviderRef struct {
	ApplicationID string `json:"application_id" yaml:"application_id"`
	APIID         string `json:"api_id" yaml:"api_id"`
	Version       string `json:"version,omitempty" yaml:"version,omitempty"`
}

func (p ProviderRef) Validate() error {
	if strings.TrimSpace(p.ApplicationID) == "" || strings.TrimSpace(p.APIID) == "" {
		return errors.New("provider mapping requires application_id and api_id")
	}
	return nil
}

type Run struct {
	ProviderMappings     map[string]ProviderRef `json:"provider_mappings,omitempty"`
	Producer             *version.Producer      `json:"producer,omitempty"`
	Application          *Application           `json:"application,omitempty"`
	RequiredCapabilities []string               `json:"required_capabilities"`
	InventoryScope       string                 `json:"inventory_scope"`
	ProvidedAPIs         []API                  `json:"provided_apis"`
	Dependencies         []API                  `json:"dependencies"`
	EvidenceVersion      string                 `json:"format_version"`
	RunID                string                 `json:"run_id"`
	Revision             uint64                 `json:"revision"`
	RunState             string                 `json:"run_state"`
	StartedAt            time.Time              `json:"started_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
	FinishedAt           *time.Time             `json:"finished_at,omitempty"`
	APIID                string                 `json:"api_id,omitempty"`
	SUTTargetKind        string                 `json:"sut_target_kind,omitempty"`
	DependencyKinds      map[string]string      `json:"dependency_kinds,omitempty"`
	TestSource           map[string]any         `json:"test_source,omitempty"`
	SUTSource            map[string]any         `json:"sut_source,omitempty"`
	Schemas              []artifact.SchemaRef   `json:"contracts,omitempty"`
	Pending              []PendingAttempt       `json:"pending_tests,omitempty"`
	Attempts             []AttemptRef           `json:"tests"`
}

type Identity struct {
	ProviderMappings map[string]ProviderRef
	Application      *Application
	APIID            string
	SUTTargetKind    string
	DependencyKinds  map[string]string
	TestSource       map[string]any
	SUTSource        map[string]any
}

func (w *Writer) Configure(identity Identity) (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	previous := w.run
	defer func() {
		if err != nil {
			w.run = previous
		}
	}()
	if len(identity.ProviderMappings) > 0 {
		mappings := make(map[string]ProviderRef, len(w.run.ProviderMappings)+len(identity.ProviderMappings))
		for id, provider := range w.run.ProviderMappings {
			mappings[id] = provider
		}
		for id, provider := range identity.ProviderMappings {
			if !safeID(id) {
				return errors.New("invalid provider mapping dependency id")
			}
			if err := provider.Validate(); err != nil {
				return err
			}
			if previous, ok := mappings[id]; ok && previous != provider {
				return errors.New("provider mapping conflicts with this run")
			}
			mappings[id] = provider
		}
		w.run.ProviderMappings = mappings
	}
	if identity.Application != nil {
		if err := validateApplication(identity.Application); err != nil {
			return err
		}
		if w.run.Application != nil && !sameJSON(w.run.Application, identity.Application) {
			return errors.New("application identity conflicts with this run")
		}
		copy := *identity.Application
		w.run.Application = &copy
	}
	if identity.APIID != "" {
		if !safeID(identity.APIID) {
			return fmt.Errorf("invalid api_id %q", identity.APIID)
		}
		if w.run.APIID != "" && w.run.APIID != identity.APIID {
			return errors.New("api_id conflicts with this run")
		}
		w.run.APIID = identity.APIID
	}
	if identity.SUTTargetKind != "" {
		if !validKind(identity.SUTTargetKind) {
			return fmt.Errorf("invalid sut_target_kind %q", identity.SUTTargetKind)
		}
		if w.run.SUTTargetKind != "" && w.run.SUTTargetKind != identity.SUTTargetKind {
			return errors.New("sut_target_kind conflicts with this run")
		}
		w.run.SUTTargetKind = identity.SUTTargetKind
	}
	if len(identity.DependencyKinds) > 0 {
		copyKinds := make(map[string]string, len(w.run.DependencyKinds)+len(identity.DependencyKinds))
		for key, value := range w.run.DependencyKinds {
			copyKinds[key] = value
		}
		w.run.DependencyKinds = copyKinds
	}
	for assetID, kind := range identity.DependencyKinds {
		if !safeID(assetID) || !validKind(kind) {
			return fmt.Errorf("invalid dependency identity for %q", assetID)
		}
		if old := w.run.DependencyKinds[assetID]; old != "" && old != kind {
			return fmt.Errorf("upstream_kind for %q conflicts with this run", assetID)
		}
		w.run.DependencyKinds[assetID] = kind
	}
	if len(identity.TestSource) > 0 {
		if len(w.run.TestSource) > 0 && !sameJSON(w.run.TestSource, identity.TestSource) {
			return errors.New("test_source conflicts with this run")
		}
		w.run.TestSource = identity.TestSource
	}
	if len(identity.SUTSource) > 0 {
		if len(w.run.SUTSource) > 0 && !sameJSON(w.run.SUTSource, identity.SUTSource) {
			return errors.New("sut_source conflicts with this run")
		}
		w.run.SUTSource = identity.SUTSource
	}
	if sameJSON(previous, w.run) {
		return nil
	}
	w.bump()
	if err := w.writeRun(); err != nil {
		return err
	}
	return nil
}

func validKind(kind string) bool { return kind == "mock" || kind == "service" || kind == "unknown" }

func sameJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

type BodyRef struct {
	EventID        string `json:"event_id,omitempty"`
	PayloadName    string `json:"payload_name,omitempty"`
	FlowID         string `json:"flow_id"`
	Phase          string `json:"phase"`
	Representation string `json:"representation"`
	FileRef
	RetainedBytes int `json:"retained_bytes"`
}

type Attempt struct {
	RequiredCapabilities []string          `json:"required_capabilities"`
	EvidenceVersion      string            `json:"format_version"`
	RunID                string            `json:"run_id"`
	AttemptID            string            `json:"attempt_id"`
	Capture              artifact.Artifact `json:"capture"`
	Bodies               []BodyRef         `json:"bodies,omitempty"`
}

// Writer serializes run inventory commits. Every attempt file is immutable.
type Writer struct {
	mu   sync.Mutex
	root string
	run  Run
}

func New(outputRoot string) (*Writer, error) {
	if strings.TrimSpace(outputRoot) == "" {
		return nil, errors.New("output root is required")
	}
	return newWriter(outputRoot, true)
}

// NewAt creates a bundle at an exact empty directory, used for explicit imports.
func NewAt(path string) (*Writer, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("bundle output path is required")
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("bundle destination %q is not empty", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return newWriter(path, false)
}

func newWriter(path string, child bool) (*Writer, error) {
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	id := "run-" + now.Format("20060102T150405.000000000") + "-" + hex.EncodeToString(entropy[:])
	if child {
		path = filepath.Join(path, id)
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		return nil, err
	}
	producer := version.Current()
	w := &Writer{root: root, run: Run{Producer: &producer, EvidenceVersion: EvidenceVersion, RunID: id, Revision: 1, RunState: "open", StartedAt: now, UpdatedAt: now}}
	if err := w.writeRun(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) Path() string { return w.root }

func (w *Writer) Snapshot() Run {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.run
}

// Register freezes schemas before traffic admission and records the pending test.
// Its returned schema paths are suitable for live validation against the snapshot.
func (w *Writer) Register(tc artifact.TestCase, schemas []artifact.SchemaRef) ([]artifact.SchemaRef, error) {
	if err := tc.Identity.Validate(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run.RunState != "open" {
		return nil, errors.New("bundle run is closed")
	}
	if !safeID(tc.ID) {
		return nil, fmt.Errorf("invalid attempt ID %q", tc.ID)
	}
	for _, pending := range w.run.Pending {
		if pending.AttemptID == tc.ID {
			return nil, fmt.Errorf("attempt %q already registered", tc.ID)
		}
	}
	for _, committed := range w.run.Attempts {
		if committed.AttemptID == tc.ID {
			return nil, fmt.Errorf("attempt %q already committed", tc.ID)
		}
	}
	frozen := make([]artifact.SchemaRef, 0, len(schemas))
	for _, schema := range schemas {
		ref, err := w.freezeSchema(schema)
		if err != nil {
			return nil, fmt.Errorf("freeze schema %q: %w", schema.ID, err)
		}
		frozen = append(frozen, ref)
	}
	// Persist setup identity before traffic admission, including for interrupted tests.
	metaBytes, err := json.Marshal(tc.Meta)
	if err != nil {
		return nil, err
	}
	var meta map[string]any
	decoder := json.NewDecoder(bytes.NewReader(metaBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&meta); err != nil {
		return nil, err
	}
	previous := w.run
	w.run.Pending = append(w.run.Pending, PendingAttempt{Identity: tc.Identity, AttemptID: tc.ID, Name: tc.Name, StartedAt: tc.StartedAt, Meta: meta})
	for _, ref := range frozen {
		stored := ref
		if stored.Source.Path != "" {
			stored.Source.Path, _ = filepath.Rel(w.root, stored.Source.Path)
			stored.Source.Path = filepath.ToSlash(stored.Source.Path)
		}
		exists := false
		for _, existing := range w.run.Schemas {
			if sameJSON(existing, stored) {
				exists = true
				break
			}
		}
		if !exists {
			w.run.Schemas = append(w.run.Schemas, stored)
		}
	}
	w.bump()
	if err := w.writeRun(); err != nil {
		w.run = previous
		return nil, err
	}
	return frozen, nil
}

// RegisterSchemas freezes a declared inventory even when a run has no tests.
func (w *Writer) RegisterSchemas(schemas []artifact.SchemaRef) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run.RunState != "open" {
		return errors.New("bundle run is closed")
	}
	refs := make([]artifact.SchemaRef, 0, len(schemas))
	for _, schema := range schemas {
		ref, err := w.freezeSchema(schema)
		if err != nil {
			return err
		}
		if ref.Source.Path != "" {
			rel, err := filepath.Rel(w.root, ref.Source.Path)
			if err != nil || !validRelative(rel) {
				return fmt.Errorf("schema path escapes bundle")
			}
			ref.Source.Path = filepath.ToSlash(rel)
		}
		exists := false
		for _, existing := range w.run.Schemas {
			if sameJSON(existing, ref) {
				exists = true
				break
			}
		}
		if !exists {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	previous := w.run
	w.run.Schemas = append(w.run.Schemas, refs...)
	w.bump()
	if err := w.writeRun(); err != nil {
		w.run = previous
		return err
	}
	return nil
}

// Commit writes attachments and immutable attempt JSON before publishing it in
// the run inventory. If the inventory update fails, the finalized attempt can
// be committed again with the same ID and bytes.
func (w *Writer) Commit(art *artifact.Artifact) (string, error) {
	if art == nil {
		return "", errors.New("capture is nil")
	}
	if err := art.TestCase.Identity.Validate(); err != nil {
		return "", err
	}
	if err := artifact.ValidateInteractions(art); err != nil {
		return "", err
	}
	for _, ref := range art.Schemas {
		if err := validateSchemaBoundary(ref); err != nil {
			return "", err
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	id := art.TestCase.ID
	if !safeID(id) {
		return "", fmt.Errorf("invalid attempt ID %q", id)
	}
	pending := false
	for _, item := range w.run.Pending {
		pending = pending || item.AttemptID == id
	}
	if !pending {
		return "", fmt.Errorf("attempt %q was not registered", id)
	}
	encodedCapture, err := json.Marshal(art)
	if err != nil {
		return "", err
	}
	if err := rejectDuplicateKeys(encodedCapture); err != nil {
		return "", err
	}
	var copyArt artifact.Artifact
	captureDecoder := json.NewDecoder(bytes.NewReader(encodedCapture))
	captureDecoder.UseNumber()
	if err := captureDecoder.Decode(&copyArt); err != nil {
		return "", err
	}
	for i := range copyArt.Schemas {
		if path := copyArt.Schemas[i].Source.Path; filepath.IsAbs(path) {
			rel, err := filepath.Rel(w.root, path)
			if err != nil || !validRelative(rel) {
				return "", fmt.Errorf("schema path escapes bundle: %q", path)
			}
			copyArt.Schemas[i].Source.Path = filepath.ToSlash(rel)
		}
	}
	attempt := Attempt{EvidenceVersion: EvidenceVersion, RunID: w.run.RunID, AttemptID: id, Capture: copyArt, RequiredCapabilities: requiredCapabilities(copyArt.Schemas, []*artifact.Artifact{&copyArt})}
	seenFlows := map[string]bool{}
	for i := range copyArt.Traffic {
		flow := &copyArt.Traffic[i]
		if flow.ID == "" || seenFlows[flow.ID] {
			return "", fmt.Errorf("duplicate or empty flow ID %q", flow.ID)
		}
		seenFlows[flow.ID] = true
		for _, phase := range []string{"request", "response"} {
			var msg *artifact.Message
			if phase == "request" {
				msg = &flow.Request
			} else {
				msg = &flow.Response
			}
			if msg.HTTP == nil || msg.HTTP.Body == nil {
				continue
			}
			body := msg.HTTP.Body
			var raw []byte
			representation := ""
			if body.Bytes != "" {
				var err error
				raw, err = base64.StdEncoding.DecodeString(body.Bytes)
				if err != nil {
					return "", fmt.Errorf("decode body for flow %q: %w", flow.ID, err)
				}
				representation = "bytes"
			} else if body.Text != "" {
				raw, representation = []byte(body.Text), "text"
			}
			if representation == "" {
				continue
			}
			hash := digest(raw)
			rel := "payloads/" + hash + ".bin"
			if err := writeContent(w.root, rel, raw); err != nil {
				return "", err
			}
			attempt.Bodies = append(attempt.Bodies, BodyRef{FlowID: flow.ID, Phase: phase, Representation: representation, FileRef: FileRef{Path: rel, SHA256: hash}, RetainedBytes: len(raw)})
			body.Text, body.Bytes = "", ""
		}
	}
	if err := w.attachEvents(&attempt); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(attempt, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	rel := "tests/" + id + ".json"
	path := filepath.Join(w.root, filepath.FromSlash(rel))
	if old, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(old, data) {
			return "", fmt.Errorf("attempt %q already has different finalized evidence", id)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := writeAtomic(path, data); err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	previous := w.run
	w.run.Pending = removePending(w.run.Pending, id)
	w.run.Attempts = append(w.run.Attempts, AttemptRef{AttemptID: id, FileRef: FileRef{Path: rel, SHA256: digest(data)}})
	w.run.ProvidedAPIs, w.run.Dependencies = apiInventory(w.run, []*artifact.Artifact{art})
	w.run.RequiredCapabilities = mergeCapabilities(w.run.RequiredCapabilities, attempt.RequiredCapabilities)
	w.bump()
	if err := w.writeRun(); err != nil {
		w.run = previous
		return "", err
	}
	return path, nil
}

func (w *Writer) Finalize(state string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if state != "complete" && state != "interrupted" {
		return fmt.Errorf("invalid run state %q", state)
	}
	if w.run.RunState != "open" {
		return nil
	}
	previous := w.run
	w.run.RunState = state
	now := time.Now().UTC()
	w.run.FinishedAt = &now
	w.bump()
	if err := w.writeRun(); err != nil {
		w.run = previous
		return err
	}
	return nil
}

func (w *Writer) bump() { w.run.Revision++; w.run.UpdatedAt = time.Now().UTC() }

func (w *Writer) writeRun() error {
	w.run.RequiredCapabilities = mergeCapabilities(w.run.RequiredCapabilities, requiredCapabilities(w.run.Schemas, nil))
	w.run.InventoryScope = "configured_and_observed"
	w.run.ProvidedAPIs, w.run.Dependencies = apiInventory(w.run, nil)
	if w.run.Attempts == nil {
		w.run.Attempts = []AttemptRef{}
	}
	data, err := json.MarshalIndent(w.run, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(w.root, "bundle.json"), append(data, '\n'))
}

func removePending(items []PendingAttempt, id string) []PendingAttempt {
	out := make([]PendingAttempt, 0, len(items))
	for _, item := range items {
		if item.AttemptID != id {
			out = append(out, item)
		}
	}
	return out
}

func safeID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func validRelative(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func writeContent(root, rel string, data []byte) error {
	if !validRelative(rel) {
		return fmt.Errorf("invalid bundle path %q", rel)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if old, err := os.ReadFile(path); err == nil {
		if digest(old) != digest(data) {
			return fmt.Errorf("content collision at %s", rel)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeAtomic(path, data)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".wiretap-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Load verifies canonical evidence, then restores body and schema paths for
// existing coverage and export calculations. It never reads generated Markdown.
func Load(root string) (*Run, []*artifact.Artifact, error) {
	limits, err := loadLimitsFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInput, err)
	}
	return LoadWithLimits(root, limits)
}

// LoadWithLimits verifies and restores an archive within explicit read budgets.
// Zero limit fields use DefaultLoadLimits; negative fields are invalid. Every
// file read, including manifests, imported contracts and repeated attachments,
// consumes the same budget. Errors wrapping ErrLoadLimit identify budget failures.
func LoadWithLimits(root string, limits LoadLimits) (*Run, []*artifact.Artifact, error) {
	limits, err := limits.normalized()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInput, err)
	}
	reader := &archiveReader{limits: limits}
	run, captures, err := loadWithReader(root, reader)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInput, err)
	}
	return run, captures, nil
}

func loadWithReader(root string, reader *archiveReader) (*Run, []*artifact.Artifact, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	runPath, err := containedPath(root, "bundle.json")
	if err != nil {
		return nil, nil, fmt.Errorf("read run evidence: %w", err)
	}
	data, err := reader.readFile(runPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read run evidence: %w", err)
	}
	decoded, err := DecodeManifest(data)
	if err != nil {
		return nil, nil, err
	}
	run := *decoded
	// Each inventory entry requires at least one read. Reject an impossible
	// inventory before allocating per-attempt bookkeeping or resolving contracts.
	if err := reader.requireReads(len(run.Attempts)); err != nil {
		return nil, nil, fmt.Errorf("run test inventory: %w", err)
	}
	// Preserve portable paths for inventory validation after captures are loaded.
	storedInventory := run
	storedInventory.Schemas = append([]artifact.SchemaRef(nil), run.Schemas...)
	if err := resolveSchemas(root, run.Schemas, reader); err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	var captures []*artifact.Artifact
	for _, ref := range run.Attempts {
		if !safeID(ref.AttemptID) || seen[ref.AttemptID] || ref.Path != "tests/"+ref.AttemptID+".json" {
			return nil, nil, fmt.Errorf("duplicate or invalid attempt ID %q", ref.AttemptID)
		}
		seen[ref.AttemptID] = true
		data, err := readRef(root, ref.FileRef, reader.readFile)
		if err != nil {
			return nil, nil, fmt.Errorf("attempt %q: %w", ref.AttemptID, err)
		}
		if err := rejectDuplicateKeys(data); err != nil {
			return nil, nil, err
		}
		var attempt Attempt
		attemptDecoder := json.NewDecoder(bytes.NewReader(data))
		attemptDecoder.UseNumber()
		if err := attemptDecoder.Decode(&attempt); err != nil {
			return nil, nil, err
		}
		if !supportedEvidenceVersion(attempt.EvidenceVersion) || attempt.RunID != run.RunID || attempt.AttemptID != ref.AttemptID || attempt.Capture.TestCase.ID != ref.AttemptID || attempt.Capture.Version < 1 {
			return nil, nil, fmt.Errorf("attempt %q identity/version mismatch", ref.AttemptID)
		}
		if err := attempt.Capture.TestCase.Identity.Validate(); err != nil {
			return nil, nil, err
		}
		if err := checkCapabilities(attempt.RequiredCapabilities); err != nil {
			return nil, nil, err
		}
		if !sameJSON(attempt.RequiredCapabilities, requiredCapabilities(attempt.Capture.Schemas, []*artifact.Artifact{&attempt.Capture})) {
			return nil, nil, fmt.Errorf("attempt %q capability declaration disagrees with content", ref.AttemptID)
		}
		if err := resolveSchemas(root, attempt.Capture.Schemas, reader); err != nil {
			return nil, nil, err
		}
		if err := reader.requireReads(len(attempt.Bodies)); err != nil {
			return nil, nil, fmt.Errorf("attempt %q attachment inventory: %w", ref.AttemptID, err)
		}
		flowMap := map[string]*artifact.Flow{}
		for i := range attempt.Capture.Traffic {
			flow := &attempt.Capture.Traffic[i]
			if flow.ID == "" || flowMap[flow.ID] != nil {
				return nil, nil, fmt.Errorf("duplicate or empty flow ID %q", flow.ID)
			}
			flowMap[flow.ID] = flow
		}
		seenBodies := map[string]bool{}
		for _, bodyRef := range attempt.Bodies {
			if bodyRef.Phase == "event" {
				if err := restoreEventBody(root, flowMap, seenBodies, bodyRef, reader.readFile); err != nil {
					return nil, nil, err
				}
				continue
			}
			if bodyRef.EventID != "" || bodyRef.PayloadName != "" {
				return nil, nil, errors.New("event selectors on a pair body reference")
			}
			key := bodyRef.FlowID + "/" + bodyRef.Phase
			if seenBodies[key] {
				return nil, nil, fmt.Errorf("duplicate body reference %q", key)
			}
			seenBodies[key] = true
			flow := flowMap[bodyRef.FlowID]
			if flow == nil {
				return nil, nil, fmt.Errorf("body references missing flow %q", bodyRef.FlowID)
			}
			var msg *artifact.Message
			switch bodyRef.Phase {
			case "request":
				msg = &flow.Request
			case "response":
				msg = &flow.Response
			default:
				return nil, nil, fmt.Errorf("invalid body phase %q", bodyRef.Phase)
			}
			var body *artifact.Body
			if msg.HTTP != nil {
				body = msg.HTTP.Body
			}
			if body == nil {
				return nil, nil, fmt.Errorf("body %q missing in interaction", key)
			}
			if body.Text != "" || body.Bytes != "" {
				return nil, nil, fmt.Errorf("body %q has both inline and attachment data", key)
			}
			raw, err := readRef(root, bodyRef.FileRef, reader.readFile)
			if err != nil {
				return nil, nil, err
			}
			if len(raw) != bodyRef.RetainedBytes {
				return nil, nil, fmt.Errorf("body %q size mismatch", key)
			}
			switch bodyRef.Representation {
			case "text":
				body.Text = string(raw)
			case "bytes":
				body.Bytes = base64.StdEncoding.EncodeToString(raw)
			default:
				return nil, nil, fmt.Errorf("invalid body representation %q", bodyRef.Representation)
			}
		}
		for _, flow := range attempt.Capture.Traffic {
			for _, part := range []struct {
				name string
				msg  artifact.Message
			}{{"request", flow.Request}, {"response", flow.Response}} {
				if part.msg.HTTP != nil && part.msg.HTTP.Body != nil && (part.msg.HTTP.Body.Text != "" || part.msg.HTTP.Body.Bytes != "") && !seenBodies[flow.ID+"/"+part.name] {
					return nil, nil, fmt.Errorf("inline body data without attachment for flow %q", flow.ID)
				}
			}
		}
		if err := validateEventAttachments(attempt.Capture, seenBodies); err != nil {
			return nil, nil, err
		}
		if err := artifact.ValidateInteractions(&attempt.Capture); err != nil {
			return nil, nil, err
		}
		captures = append(captures, &attempt.Capture)
	}
	for _, item := range run.Pending {
		if !safeID(item.AttemptID) || seen[item.AttemptID] {
			return nil, nil, fmt.Errorf("invalid pending attempt %q", item.AttemptID)
		}
		seen[item.AttemptID] = true
	}
	if err := validateInventory(storedInventory, captures); err != nil {
		return nil, nil, err
	}
	if !sameJSON(run.RequiredCapabilities, requiredCapabilities(run.Schemas, captures)) {
		return nil, nil, errors.New("bundle capability declaration disagrees with content")
	}
	return &run, captures, nil
}

func supportedEvidenceVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] != "3" {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	return err == nil && minor >= 0
}

func resolveSchemas(root string, schemas []artifact.SchemaRef, reader *archiveReader) error {
	frozenCount := 0
	for _, ref := range schemas {
		if ref.Source.Type != "omitted" {
			frozenCount++
		}
	}
	if err := reader.requireReads(frozenCount); err != nil {
		return fmt.Errorf("contract inventory: %w", err)
	}
	for i := range schemas {
		ref := &schemas[i]
		if err := validateSchemaBoundary(*ref); err != nil {
			return err
		}
		if ref.Type != "openapi" && !opaqueContractType(ref.Type) {
			return fmt.Errorf("unsupported contract type %q", ref.Type)
		}
		if ref.Source.Type == "omitted" {
			if ref.Source.Path != "" || ref.Source.URL != "" || ref.Source.Content != "" || ref.Source.SHA256 != "" || len(ref.Source.Files) != 0 {
				return fmt.Errorf("omitted schema %q contains a source", ref.ID)
			}
			continue
		}
		if ref.Source.Type != "file" || ref.Source.Path == "" {
			return fmt.Errorf("schema %q is not a frozen file or explicit omission", ref.ID)
		}
		if !validRelative(ref.Source.Path) || !strings.HasPrefix(ref.Source.Path, "contracts/") {
			return fmt.Errorf("invalid schema path %q", ref.Source.Path)
		}
		rootRel := ref.Source.Path
		parts := strings.Split(rootRel, "/")
		if len(parts) < 3 || parts[1] == "" || filepath.ToSlash(filepath.Clean(rootRel)) != rootRel {
			return fmt.Errorf("invalid schema path %q", rootRel)
		}
		setBase := strings.Join(parts[:2], "/")
		path, err := containedPath(root, rootRel)
		if err != nil {
			return err
		}
		data, err := reader.readFile(path)
		if err != nil {
			return err
		}
		if digest(data) != ref.Source.SHA256 {
			return fmt.Errorf("schema %q hash mismatch", ref.ID)
		}
		manifestRel := setBase + "/manifest.json"
		manifestPath, err := containedPath(root, manifestRel)
		if err != nil {
			return err
		}
		manifestData, err := reader.readFile(manifestPath)
		if err != nil {
			return err
		}
		if err := rejectDuplicateKeys(manifestData); err != nil {
			return err
		}
		var manifest schemaManifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return err
		}
		if manifest.SetHash != parts[1] || manifest.Root != rootRel {
			return fmt.Errorf("schema %q manifest identity mismatch", ref.ID)
		}
		if opaqueContractType(ref.Type) {
			if err := validateContractPackageManifest(*ref, manifest, setBase); err != nil {
				return err
			}
		} else if manifest.Profile != "" || manifest.Type != "" {
			return fmt.Errorf("schema %q has an unsupported package profile", ref.ID)
		}
		if err := reader.requireReads(len(manifest.Files)); err != nil {
			return fmt.Errorf("schema %q file inventory: %w", ref.ID, err)
		}
		foundRoot := false
		seen := map[string]bool{}
		for _, file := range manifest.Files {
			if seen[file.Path] {
				return fmt.Errorf("duplicate schema file %q", file.Path)
			}
			seen[file.Path] = true
			if file.Path == rootRel {
				if file.SHA256 != ref.Source.SHA256 {
					return fmt.Errorf("schema %q manifest root hash mismatch", ref.ID)
				}
				foundRoot = true
			}
			if !strings.HasPrefix(file.Path, setBase+"/") || filepath.ToSlash(filepath.Clean(file.Path)) != file.Path {
				return fmt.Errorf("schema %q manifest file outside schema set", ref.ID)
			}
			if _, err := readRef(root, FileRef{Path: file.Path, SHA256: file.SHA256}, reader.readFile); err != nil {
				return err
			}
		}
		if !foundRoot {
			return fmt.Errorf("schema %q root missing from manifest", ref.ID)
		}
		ref.Source.Path = path
	}
	return nil
}

func containedPath(root, rel string) (string, error) {
	if !validRelative(rel) {
		return "", fmt.Errorf("invalid bundle reference %q", rel)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	actualRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	check, err := filepath.Rel(actualRoot, actual)
	if err != nil || !validRelative(check) {
		return "", fmt.Errorf("bundle reference escapes root: %q", rel)
	}
	return actual, nil
}

func readRef(root string, ref FileRef, readFile func(string) ([]byte, error)) ([]byte, error) {
	path, err := containedPath(root, ref.Path)
	if err != nil {
		return nil, err
	}
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	if digest(data) != ref.SHA256 {
		return nil, fmt.Errorf("hash mismatch for %s", ref.Path)
	}
	return data, nil
}

func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("invalid JSON object key")
				}
				if keys[key] {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				keys[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := dec.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("extra JSON content")
	}
	return nil
}

func sortRefs(refs []AttemptRef) {
	sort.Slice(refs, func(i, j int) bool { return refs[i].AttemptID < refs[j].AttemptID })
}

func (ref *BodyRef) UnmarshalJSON(data []byte) error {
	type plain BodyRef
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key := range fields {
		for _, removed := range []string{"kind", "message_index", "status_detail_index"} {
			if strings.EqualFold(key, removed) {
				return fmt.Errorf("unsupported native gRPC body selector %q", key)
			}
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*ref = BodyRef(value)
	return nil
}
