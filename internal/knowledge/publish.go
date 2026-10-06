package knowledge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}
type Publication struct {
	Root       string     `json:"root"`
	Check      bool       `json:"check"`
	Changes    []Change   `json:"changes"`
	Validation Validation `json:"validation"`
}

// Publish uses the same candidate for check and write modes. The directory swap
// retains a recoverable prior snapshot until the new manifest is in place.
func Publish(input, output string, c Config, check bool) (Publication, error) {
	result := Publication{Check: check, Changes: []Change{}}
	input, err := filepath.Abs(input)
	if err != nil {
		return result, err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return result, err
	}
	input, err = realParentPath(input)
	if err != nil {
		return result, err
	}
	output, err = realParentPath(output)
	if err != nil {
		return result, err
	}
	result.Root = output
	if contains(input, output) || contains(output, input) {
		return result, fmt.Errorf("publication and source archive must be separate, non-overlapping directories")
	}
	var unlock func()
	if !check {
		unlock, err = lockPublication(output)
		if err != nil {
			return result, err
		}
		defer unlock()
	}
	before, err := snapshot(output)
	if err != nil {
		return result, err
	}
	archive, err := snapshot(input)
	if err != nil {
		return result, err
	}
	run, captures, err := bundle.Load(input)
	if err != nil {
		return result, fmt.Errorf("source archive: %w", err)
	}
	verifiedInput, err := snapshot(input)
	if err != nil {
		return result, err
	}
	if !sameSnapshot(archive, verifiedInput) {
		return result, fmt.Errorf("source archive changed during conversion")
	}
	if run.RunState == "open" {
		return result, fmt.Errorf("finalize or interrupt the run before publishing")
	}
	candidate, roles, err := buildCandidate(input, archive, before, run, captures, c)
	if err != nil {
		return result, err
	}
	result.Validation = validateFiles(candidate, Validation{BaseOKF: "valid", Application: "not-present", Evidence: "not-present", Measurement: "not-present", OptionalProfiles: []string{}, Diagnostics: []Diagnostic{}})
	if !result.Validation.Valid() || result.Validation.Application != "valid" {
		b, _ := Canonical(result.Validation)
		return result, fmt.Errorf("candidate validation failed:\n%s", b)
	}
	paths := map[string]bool{}
	for p := range before {
		paths[p] = true
	}
	for p := range candidate {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		old, oldOK := before[p]
		next, nextOK := candidate[p]
		if oldOK && nextOK && bytes.Equal(old, next) {
			continue
		}
		action := "modify"
		if !oldOK {
			action = "add"
		} else if !nextOK {
			action = "remove"
		}
		reason := roles[p]
		if reason == "" {
			reason = roleFor(p)
		}
		result.Changes = append(result.Changes, Change{p, action, reason})
	}
	if check || len(result.Changes) == 0 {
		return result, nil
	}
	// Detect writers that do not honor our lock, including authored edits made
	// while the candidate was being generated. Never silently lose their work.
	current, err := snapshot(output)
	if err != nil {
		return result, err
	}
	if !sameSnapshot(before, current) {
		return result, fmt.Errorf("PUB-06 concurrent publication/edit conflict; rerun against the current bundle")
	}
	if err := commitDirectory(output, candidate, before); err != nil {
		return result, err
	}
	return result, nil
}
func contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Normalize platform aliases in ancestors (for example macOS /var) while
// retaining the final component so a symlink bundle root is still rejected.
func realParentPath(p string) (string, error) {
	parent := filepath.Dir(p)
	suffix := filepath.Base(p)
	for {
		_, err := os.Lstat(parent)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			return filepath.Join(resolved, suffix), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		suffix = filepath.Join(filepath.Base(parent), suffix)
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		parent = next
	}
}
func sameSnapshot(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for p, v := range a {
		other, ok := b[p]
		if !ok || !bytes.Equal(v, other) {
			return false
		}
	}
	return true
}
func lockPublication(root string) (func(), error) {
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, err
	}
	if resolved != parent {
		return nil, fmt.Errorf("publication parent contains a symlink; use its real path")
	}
	// Keep the lock outside the bundle so it is never a publication input.
	p := root + ".wiretap-lock"
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("PUB-06 publication lock %s: %w (after a crash, inspect recovery directories before removing a stale lock)", p, err)
	}
	f.Close()
	return func() { os.Remove(p) }, nil
}
func commitDirectory(root string, files, before map[string][]byte) error {
	stage, err := os.MkdirTemp(filepath.Dir(root), ".wiretap-publication-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	mode := os.FileMode(0755)
	if info, err := os.Stat(root); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(stage, mode); err != nil {
		return err
	}
	// Manifest is written last within the staged snapshot.
	for _, p := range sortedPaths(files) {
		if p == "bundle.json" {
			continue
		}
		if err := stageFile(stage, p, files[p]); err != nil {
			return err
		}
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil && info.Mode().IsRegular() {
			if err := os.Chmod(filepath.Join(stage, filepath.FromSlash(p)), info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	if err := stageFile(stage, "bundle.json", files["bundle.json"]); err != nil {
		return err
	}
	// Recheck after staging: a slow write must not open a lost-update window.
	current, err := snapshot(root)
	if err != nil {
		return err
	}
	if !sameSnapshot(before, current) {
		return fmt.Errorf("PUB-06 concurrent edit during staging")
	}
	backup := root + ".wiretap-recovery"
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		return fmt.Errorf("recoverable prior publication exists at %s; resolve it before publishing", backup)
	}
	hadRoot := false
	if _, err := os.Lstat(root); err == nil {
		hadRoot = true
		if err := os.Rename(root, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stage, root); err != nil {
		if hadRoot {
			if rollback := os.Rename(backup, root); rollback != nil {
				return fmt.Errorf("publication failed: %v; restore prior snapshot from %s: %v", err, backup, rollback)
			}
		}
		return err
	}
	if hadRoot {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("publication committed; prior snapshot cleanup failed at %s: %w", backup, err)
		}
	}
	return nil
}
func stageFile(root, p string, b []byte) error {
	if !safePath(p) {
		return fmt.Errorf("unsafe candidate path %q", p)
	}
	dest := filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
func roleFor(p string) string {
	if p == "bundle.json" {
		return "navigation"
	}
	if p == "publication.json" {
		return "provenance"
	}
	if strings.HasPrefix(p, "contracts/") {
		return "contract"
	}
	if strings.HasPrefix(p, "evidence/") {
		return "evidence"
	}
	if p == "index.md" {
		return "navigation"
	}
	return "subject"
}

func buildCandidate(input string, archive, before map[string][]byte, run *bundle.Run, captures []*artifact.Artifact, c Config) (map[string][]byte, map[string]string, error) {
	if c.Version != 1 {
		return nil, nil, fmt.Errorf("publication configuration version must be 1")
	}
	if c.InventoryScope == "" {
		c.InventoryScope = "declared-and-observed"
	}
	files := map[string][]byte{}
	for p, b := range before {
		files[p] = b
	}
	reg := map[string]Object{}
	roles := map[string]string{}
	prior := Object{}
	if b := before["bundle.json"]; b != nil {
		validation := validateFiles(before, Validation{BaseOKF: "valid", Application: "not-present", Evidence: "not-present", Measurement: "not-present", OptionalProfiles: []string{}, Diagnostics: []Diagnostic{}})
		if !validation.Valid() || validation.Application != "valid" {
			return nil, nil, fmt.Errorf("previous publication is invalid or unsupported; resolve integrity/ownership conflicts first: %+v", validation.Diagnostics)
		}
		v, err := Decode(b)
		if err != nil {
			return nil, nil, err
		}
		prior = object(v)
		pv, err := Decode(before[str(prior["publication"])])
		if err != nil || object(object(pv)["producer"])["name"] != "wiretap" {
			return nil, nil, fmt.Errorf("PUB-07 changing producer requires an explicit ownership handoff")
		}
		if prior["bundle_id"] != c.BundleID || object(prior["application"])["id"] != c.Application["id"] {
			return nil, nil, fmt.Errorf("publication application/bundle identity cannot change")
		}
		for _, x := range list(prior["files"]) {
			x := object(x)
			reg[str(x["path"])] = x
		}
	}
	oldReg := map[string]Object{}
	for p, x := range reg {
		oldReg[p] = x
	}
	put := func(p string, b []byte, role, media string) error {
		if !safePath(p) {
			return fmt.Errorf("unsafe output path %q", p)
		}
		if old, exists := before[p]; exists {
			entry := oldReg[p]
			if entry == nil || entry["ownership"] != "generated" || entry["owner"] != Owner {
				if !bytes.Equal(old, b) {
					return fmt.Errorf("PUB-07 ownership conflict at %s; authored, unlisted or another producer's bytes are preserved", p)
				}
				return nil
			}
			if hash(old) != str(entry["sha256"]) {
				return fmt.Errorf("PUB-07 modified generated file %s", p)
			}
		}
		files[p] = b
		next := Object{"path": p, "sha256": hash(b), "media_type": media, "role": role, "ownership": "generated", "owner": Owner}
		reg[p] = object(merge(reg[p], next))
		roles[p] = roleFor(p)
		return nil
	}
	putJSON := func(p string, v any, role string) error {
		if strings.HasSuffix(p, "/attempt.json") {
			if old, err := Decode(before[p]); err == nil {
				next := object(v)
				previous := object(old)
				for _, key := range []string{"finished_at", "transformation"} {
					if next[key] == nil {
						delete(previous, key)
					}
				}
				for key, identity := range map[string]string{"artifacts": "name", "contracts": "contract_id"} {
					rows := listValue(next[key])
					for i, row := range rows {
						for _, prior := range list(previous[key]) {
							if object(row)[identity] == object(prior)[identity] {
								rows[i] = merge(prior, row)
								break
							}
						}
					}
					next[key] = rows
				}
				v = merge(previous, next)
			}
		}
		if strings.HasSuffix(p, "/recording.json") {
			if old, err := Decode(before[p]); err == nil {
				next := object(v)
				for key, value := range object(old) {
					if _, exists := next[key]; !exists {
						next[key] = value
					}
				}
			}
		}
		var b []byte
		var err error
		if strings.HasSuffix(p, "/recording.json") {
			b, err = Canonical(v)
		} else {
			b, err = coreCanonical(v)
		}
		if err != nil {
			return err
		}
		return put(p, b, role, "application/json")
	}
	subjects := map[string]Object{}
	previousPages := map[string]string{}
	for _, x := range list(prior["subjects"]) {
		s := object(x)
		subjects[str(s["id"])] = s
		previousPages[str(s["id"])] = str(s["page"])
	}
	migrations := append([]any{}, list(prior["migrations"])...)
	migrationMap := map[string]Object{}
	for _, migration := range c.Migrations {
		id := str(migration["subject_id"])
		from, to := str(migration["from"]), str(migration["to"])
		if !safePath(from) || !safePath(to) || from == to || strings.TrimSpace(str(migration["reason"])) == "" || subjects[id] == nil {
			return nil, nil, fmt.Errorf("ID-06 migration needs an existing subject, safe distinct paths and reason")
		}
		if previousPages[id] == to {
			found := false
			for _, old := range migrations {
				old := object(old)
				if old["subject_id"] == id && old["from"] == from && old["to"] == to && old["reason"] == migration["reason"] {
					found = true
				}
			}
			if !found {
				return nil, nil, fmt.Errorf("ID-06 an already moved subject requires its actual persisted migration record")
			}
			continue
		}
		if previousPages[id] != from || migrationMap[id] != nil {
			return nil, nil, fmt.Errorf("ID-06 migration must match the current subject path once")
		}
		migrationMap[id] = migration
		migrations = append(migrations, migration)
	}
	for _, s := range c.Subjects {
		id := str(s["id"])
		if old := subjects[id]; old != nil && old["key"] != s["key"] && migrationMap[id] == nil {
			return nil, nil, fmt.Errorf("ID-06 key change for %s requires an explicit migration; retain the original key", id)
		}
		subjects[id] = object(merge(subjects[id], s))
	}
	for id, s := range subjects {
		p := subjectPath(s, subjects)
		if p == "" {
			return nil, nil, fmt.Errorf("MAN-05 invalid subject kind or parent for %s", id)
		}
		if old := str(s["page"]); old != "" && old != p {
			if migrationMap[id] == nil || migrationMap[id]["to"] != p {
				return nil, nil, fmt.Errorf("ID-06 path change for %s requires an explicit migration", id)
			}
		}
		s["page"] = p
		if s["status"] == nil {
			s["status"] = "active"
		}
	}
	for _, migration := range migrations {
		migration := object(migration)
		for id, s := range subjects {
			if s["page"] == migration["from"] && id != str(migration["subject_id"]) {
				return nil, nil, fmt.Errorf("ID-06 migration paths cannot be reassigned")
			}
		}
	}
	removed := []any{}
	removed = append(removed, list(prior["removed_subjects"])...)
	for _, tomb := range c.RemovedSubjects {
		id := str(tomb["id"])
		old := subjects[id]
		if old == nil {
			persisted := false
			for _, previous := range removed {
				a, _ := Canonical(previous)
				b, _ := Canonical(tomb)
				persisted = persisted || bytes.Equal(a, b)
			}
			if persisted {
				continue
			}
		}
		if old == nil || tomb["path"] != old["page"] || tomb["key"] != old["key"] || tomb["kind"] != old["kind"] || strings.TrimSpace(str(tomb["reason"])) == "" {
			return nil, nil, fmt.Errorf("ID-07 removal requires exact previous identity/path and reason")
		}
		delete(subjects, id)
		removed = append(removed, tomb)
		p := str(tomb["path"])
		if e := reg[p]; e != nil && e["ownership"] == "generated" && e["owner"] == Owner {
			forwarded := false
			for _, migration := range migrations {
				if object(migration)["subject_id"] == id {
					forwarded = true
				}
			}
			if forwarded {
				fm, _, err := frontmatter(before[p])
				if err != nil {
					return nil, nil, err
				}
				fm["status"] = "deprecated"
				fm["description"] = "Retired subject: " + str(tomb["reason"])
				if gen := object(fm["generated"]); gen != nil {
					delete(gen, "at")
				}
				y, err := marshalFrontmatter(fm)
				if err != nil {
					return nil, nil, err
				}
				b := []byte("---\n" + string(y) + "---\n# Retired subject\n\n" + display(str(tomb["reason"])) + "\n")
				if err := put(p, b, "concept", "text/markdown"); err != nil {
					return nil, nil, err
				}
			} else {
				delete(files, p)
				delete(reg, p)
			}
		}
	}
	for _, tomb := range removed {
		tomb := object(tomb)
		if subjects[str(tomb["id"])] != nil {
			return nil, nil, fmt.Errorf("ID-07 tombstoned identity cannot be reused")
		}
		for _, s := range subjects {
			if s["page"] == tomb["path"] {
				return nil, nil, fmt.Errorf("ID-07 tombstoned path cannot be reused")
			}
		}
	}
	selection := c.Selection
	if selection == nil {
		return nil, nil, fmt.Errorf("explicit selection is required")
	}
	selected := map[string]bool{}
	for _, id := range list(selection["test_ids"]) {
		selected[str(id)] = true
	}
	for id := range migrationMap {
		if subjects[id]["kind"] == "test" && !selected[id] {
			return nil, nil, fmt.Errorf("migrating a test path requires selecting that test so its evidence references can move together")
		}
	}
	// Remove old evidence only for explicitly selected/removed tests. Referenced
	// older contract slots remain and retain their exact hashes.
	evidence := []any{}
	obsolete := map[string]bool{}
	retainedFiles := map[string]bool{}
	for _, x := range list(prior["evidence"]) {
		x := object(x)
		id := str(x["test_id"])
		if !selected[id] && subjects[id] != nil {
			evidence = append(evidence, x)
			p := str(x["path"])
			retainedFiles[p] = true
			if v, err := Decode(before[p]); err == nil {
				for _, a := range list(object(v)["artifacts"]) {
					retainedFiles[str(object(a)["path"])] = true
				}
			}
			continue
		}
		p := str(x["path"])
		obsolete[p] = true
		if v, err := Decode(before[p]); err == nil {
			for _, a := range list(object(v)["artifacts"]) {
				obsolete[str(object(a)["path"])] = true
			}
		}
	}
	for p := range obsolete {
		if retainedFiles[p] {
			continue
		}
		if e := reg[p]; e == nil || e["ownership"] != "generated" || e["owner"] != Owner {
			return nil, nil, fmt.Errorf("PUB-07 cannot replace evidence owned by another author at %s", p)
		}
		delete(files, p)
		delete(reg, p)
	}
	oldPub := Object{}
	if b := before[str(prior["publication"])]; b != nil {
		v, err := Decode(b)
		if err != nil {
			return nil, nil, err
		}
		oldPub = object(v)
	}
	sources := map[string]Object{}
	for _, s := range list(oldPub["sources"]) {
		s := object(s)
		sources[str(s["id"])] = s
	}
	actual := []string{}
	for _, item := range []struct {
		role  string
		facts Object
	}{{"application", run.SUTSource}, {"tests", run.TestSource}} {
		source := Object{"role": item.role, "state": "unknown"}
		for _, k := range []string{"repository", "revision", "build_digest", "dirty"} {
			if v := item.facts[k]; v != nil {
				source[k] = v
			}
		}
		if source["revision"] != nil || source["build_digest"] != nil {
			source["state"] = "known"
		}
		if len(item.facts) > 0 {
			source["extensions"] = Object{Owner: Object{"captured": item.facts}}
		}
		b, _ := Canonical(source)
		id := item.role + "-source-" + hash(b)[:16]
		source["id"] = id
		sources[id] = object(merge(sources[id], source))
		actual = append(actual, id)
	}
	for _, s := range c.Sources {
		id := str(s["id"])
		if old := sources[id]; old != nil {
			a, _ := Canonical(old)
			b, _ := Canonical(s)
			if !bytes.Equal(a, b) {
				return nil, nil, fmt.Errorf("PUB-02 source identity %s cannot be rebound to different facts", id)
			}
		}
		sources[id] = s
	}
	target := c.TargetSourceIDs
	if len(target) == 0 {
		target = actual
	}
	contractEntries := map[string]Object{}
	for _, x := range list(prior["contracts"]) {
		x := object(x)
		contractEntries[str(x["id"])] = x
	}
	frozen := map[string]frozenContract{}
	schemaIdentity := func(s artifact.SchemaRef) string { return s.ID + ":" + s.Source.SHA256 }
	allSchemas := append([]artifact.SchemaRef(nil), run.Schemas...)
	for _, a := range captures {
		allSchemas = append(allSchemas, a.Schemas...)
	}
	sort.Slice(allSchemas, func(i, j int) bool { return schemaIdentity(allSchemas[i]) < schemaIdentity(allSchemas[j]) })
	for _, s := range allSchemas {
		if s.Source.Type == "omitted" || frozen[schemaIdentity(s)].root != "" {
			continue
		}
		var mapping *ContractMapping
		for i := range c.Contracts {
			x := &c.Contracts[i]
			if x.SchemaID == s.ID && (x.SourceSHA256 == "" || x.SourceSHA256 == s.Source.SHA256) {
				if mapping != nil {
					return nil, nil, fmt.Errorf("ambiguous contract mapping for %s", s.ID)
				}
				mapping = x
			}
		}
		if mapping == nil {
			return nil, nil, fmt.Errorf("explicit contract mapping required for %s", s.ID)
		}
		if old := contractEntries[mapping.ID]; old != nil && old["key"] != mapping.Key {
			return nil, nil, fmt.Errorf("ID-06 contract slot key cannot change; assign a distinct contract revision ID")
		}
		fc, err := freezeContract(archive, before, input, s, *mapping, put)
		if err != nil {
			return nil, nil, err
		}
		if old := contractEntries[mapping.ID]; old != nil {
			manifestPath := str(old["manifest"])
			oldValue, _ := Decode(before[manifestPath])
			newValue, _ := Decode(files[manifestPath])
			keep := map[string]bool{}
			for _, x := range list(object(newValue)["files"]) {
				keep[str(object(x)["path"])] = true
			}
			for _, x := range list(object(oldValue)["files"]) {
				name := str(object(x)["path"])
				if !keep[name] {
					p := filepath.ToSlash(filepath.Join(filepath.Dir(manifestPath), name))
					entry := reg[p]
					if entry == nil || entry["ownership"] != "generated" || entry["owner"] != Owner {
						return nil, nil, fmt.Errorf("PUB-07 cannot remove contract source owned by another producer")
					}
					delete(files, p)
					delete(reg, p)
				}
			}
		}
		frozen[schemaIdentity(s)] = fc
		contractEntries[mapping.ID] = object(merge(contractEntries[mapping.ID], fc.entry))
	}
	// Updating an old slot is forbidden while an unselected observation still
	// pins its old manifest. Use a distinct explicit contract revision slot.
	for _, x := range evidence {
		p := str(object(x)["path"])
		v, _ := Decode(files[p])
		for _, binding := range list(object(v)["contracts"]) {
			binding := object(binding)
			c := contractEntries[str(binding["contract_id"])]
			if c == nil || hash(files[str(c["manifest"])]) != str(binding["manifest_sha256"]) {
				return nil, nil, fmt.Errorf("CON-01 retained evidence pins old contract; configure a distinct stable revision slot")
			}
		}
	}
	testMappings := map[string]TestMapping{}
	nameMappings := map[string]TestMapping{}
	for _, t := range c.Tests {
		if testMappings[t.ID].ID != "" || (t.Name != "" && nameMappings[t.Name].ID != "") {
			return nil, nil, fmt.Errorf("ambiguous logical test mapping")
		}
		testMappings[t.ID] = t
		if t.Name != "" {
			nameMappings[t.Name] = t
		}
	}
	counts := map[string]int{}
	assigned := map[string][]*artifact.Artifact{}
	for _, a := range captures {
		binding := object(a.TestCase.Meta["application_knowledge"])
		id := str(binding["test_id"])
		if id == "" {
			id = nameMappings[a.TestCase.Name].ID
		}
		if id == "" {
			return nil, nil, fmt.Errorf("ID-04 test %q needs an explicit logical test mapping", a.TestCase.Name)
		}
		counts[id]++
		assigned[id] = append(assigned[id], a)
	}
	for id := range selected {
		s := subjects[id]
		if s["kind"] != "test" {
			return nil, nil, fmt.Errorf("selected test identity %s is not declared", id)
		}
		attempts := assigned[id]
		t := testMappings[id]
		group := t.Group
		if group == "" {
			group = "default"
		}
		if !safeKey(group) {
			return nil, nil, fmt.Errorf("unsafe execution group")
		}
		pending := []bundle.PendingAttempt{}
		for _, item := range run.Pending {
			bindingID := str(object(item.Meta["application_knowledge"])["test_id"])
			if bindingID == "" {
				bindingID = nameMappings[item.Name].ID
			}
			if bindingID == id {
				pending = append(pending, item)
			}
		}
		executionObserved := len(pending) > 0
		if len(attempts) == 0 && len(pending) == 0 {
			pending = append(pending, bundle.PendingAttempt{AttemptID: "unavailable-" + str(s["key"]), Name: str(s["title"]), StartedAt: run.StartedAt})
		}
		if len(pending) > 0 {
			for _, item := range pending {
				ordinal, explicit := t.AttemptOrdinals[item.AttemptID]
				binding := object(item.Meta["application_knowledge"])
				if key := str(binding["key"]); key != "" && key != str(s["key"]) {
					return nil, nil, fmt.Errorf("ID-06 harness key disagrees with stable subject mapping")
				}
				if value, ok := binding["attempt_ordinal"]; ok {
					if explicit && ordinal != number(value) {
						return nil, nil, fmt.Errorf("harness and configuration ordinal disagree")
					}
					ordinal, explicit = number(value), true
				}
				if ordinal < 0 {
					return nil, nil, fmt.Errorf("invalid attempt ordinal")
				}
				g := str(binding["execution_group"])
				if g == "" {
					g = group
				}
				if len(pending)+len(attempts) > 1 && !explicit {
					return nil, nil, fmt.Errorf("pending retries require explicit harness ordinals")
				}
				slot := "primary"
				if ordinal > 0 {
					slot = fmt.Sprintf("retry-%d", ordinal)
				}
				if g != "default" {
					slot = g + "-" + slot
				}
				if !safeKey(slot) {
					return nil, nil, fmt.Errorf("unsafe execution slot")
				}
				p := "evidence/tests/" + str(s["key"]) + "/" + slot + "/attempt.json"
				attempt := Object{"profile": Object{"id": "test-result", "version": Version}, "test_id": id, "execution_group": g, "attempt_ordinal": ordinal, "run_id": run.RunID, "attempt_id": item.AttemptID, "started_at": item.StartedAt.UTC().Format(time.RFC3339Nano), "outcome": "unknown", "capture_state": "unavailable", "source_ids": actual, "contracts": []any{}, "artifacts": []any{}, "diagnostics": []Object{{"code": "selected-test-unavailable", "message": "No completed capture exists for this selected test. An unavailable selection record does not claim a test execution or a passing result."}}, "capture_scope": Object{"description": "No captured boundary observations are available.", "subject_ids": []any{}}, "extensions": Object{Owner: Object{"execution_observed": executionObserved, "selection_record": true}}}
				if err := putJSON(p, attempt, "evidence"); err != nil {
					return nil, nil, err
				}
				evidence = append(evidence, Object{"test_id": id, "slot": slot, "group": g, "path": p})
			}
		}
		for _, a := range attempts {
			binding := object(a.TestCase.Meta["application_knowledge"])
			if key := str(binding["key"]); key != "" && key != str(s["key"]) {
				return nil, nil, fmt.Errorf("ID-06 harness key disagrees with stable subject mapping")
			}
			ordinal, explicit := t.AttemptOrdinals[a.TestCase.ID]
			if value, ok := binding["attempt_ordinal"]; ok {
				if explicit && ordinal != number(value) {
					return nil, nil, fmt.Errorf("harness and configuration ordinal disagree")
				}
				ordinal = number(value)
				explicit = true
			}
			if !explicit && counts[id]+len(pending) != 1 {
				return nil, nil, fmt.Errorf("PUB-04 retries for %s require harness ordinals or explicit attempt_ordinals", id)
			}
			if ordinal < 0 {
				return nil, nil, fmt.Errorf("invalid attempt ordinal")
			}
			g := str(binding["execution_group"])
			if g == "" {
				g = group
			}
			slot := "primary"
			if ordinal > 0 {
				slot = fmt.Sprintf("retry-%d", ordinal)
			}
			if g != "default" {
				slot = g + "-" + slot
			}
			if !safeKey(slot) {
				return nil, nil, fmt.Errorf("execution slot requires a safe explicit group")
			}
			p := "evidence/tests/" + str(s["key"]) + "/" + slot + "/attempt.json"
			capBytes, err := Canonical(a)
			if err != nil {
				return nil, nil, err
			}
			capValue, _ := Decode(capBytes)
			capMap := object(capValue)
			originalPath := ""
			originalDigest := ""
			for _, ref := range run.Attempts {
				if ref.AttemptID == a.TestCase.ID {
					originalPath = ref.Path
					originalDigest = ref.SHA256
				}
			}
			raw, _ := Decode(archive[originalPath])
			capMap = object(mergeCapture(object(raw)["capture"], capMap))
			testcase := object(capMap["testcase"])
			testcase["started_at"] = a.TestCase.StartedAt.UTC().Format(time.RFC3339Nano)
			if a.TestCase.FinishedAt.IsZero() {
				delete(testcase, "finished_at")
			} else {
				testcase["finished_at"] = a.TestCase.FinishedAt.UTC().Format(time.RFC3339Nano)
			}
			bindings := []Object{}
			schemaIDs := Object{}
			schemaHashes := map[string]string{}
			scope := map[string]bool{}
			for sid, subject := range subjects {
				if subject["kind"] == "provided-api" || subject["kind"] == "dependency" {
					scope[sid] = true
				}
			}
			for _, schema := range list(capMap["schemas"]) {
				schema := object(schema)
				src := object(schema["source"])
				if src["type"] == "omitted" {
					continue
				}
				fc := frozen[str(schema["id"])+":"+str(src["sha256"])]
				if fc.root == "" {
					return nil, nil, fmt.Errorf("missing frozen contract mapping")
				}
				src["path"] = fc.root
				src["sha256"] = fc.rootHash
				delete(src, "url")
				delete(src, "content")
				delete(src, "import_paths")
				delete(src, "files")
				schema["asset_sha"] = fc.rootHash
				schemaHashes[str(schema["id"])] = fc.rootHash
				cid := str(fc.entry["id"])
				schemaIDs[str(schema["id"])] = cid
				bindings = append(bindings, Object{"contract_id": cid, "manifest_sha256": fc.manifestHash})
				for _, sid := range listValue(fc.entry["subject_ids"]) {
					scope[str(sid)] = true
				}
			}
			// Writer/reader hashes follow the declared package transformation.
			for _, flow := range list(capMap["traffic"]) {
				interaction := object(object(flow)["interaction"])
				for _, event := range list(interaction["events"]) {
					for _, payload := range list(object(event)["payloads"]) {
						payload := object(payload)
						for _, k := range []string{"writer_schema", "reader_schema"} {
							link := object(payload[k])
							if link != nil {
								if sha := schemaHashes[str(link["id"])]; sha != "" {
									link["sha256"] = sha
								}
							}
						}
					}
				}
			}
			bindings = uniqueBindings(bindings)
			sort.Slice(bindings, func(i, j int) bool { return str(bindings[i]["contract_id"]) < str(bindings[j]["contract_id"]) })
			kind := run.SUTTargetKind
			if kind == "" {
				kind = "unknown"
			}
			depKinds := run.DependencyKinds
			if depKinds == nil {
				depKinds = map[string]string{}
			}
			record := Object{"profile": Object{"id": RecordingID, "version": RecordingVersion}, "run_id": run.RunID, "attempt_id": a.TestCase.ID, "capture": capMap, "contract_ids": schemaIDs, "source_record_sha256": originalDigest, "boundary": Object{"api_id": run.APIID, "sut_target_kind": kind, "dependency_kinds": depKinds}}
			rp := recordingPath(p)
			if err := putJSON(rp, record, "evidence"); err != nil {
				return nil, nil, err
			}
			state := "complete"
			diagnostics := []Object{}
			for _, message := range a.CaptureDiagnostics {
				diagnostics = append(diagnostics, Object{"code": "wiretap-capture", "message": message})
			}
			if capturePartial(a) || a.TestCase.FinishedAt.IsZero() || run.RunState == "interrupted" {
				state = "partial"
				diagnostics = append(diagnostics, Object{"code": "wiretap-incomplete", "message": "Capture or originating run was incomplete/interrupted; retained bytes do not establish complete behavior."})
			}
			scopeIDs := []string{}
			for sid := range scope {
				scopeIDs = append(scopeIDs, sid)
			}
			sort.Strings(scopeIDs)
			attempt := Object{"profile": Object{"id": "test-result", "version": Version}, "test_id": id, "execution_group": g, "attempt_ordinal": ordinal, "run_id": run.RunID, "attempt_id": a.TestCase.ID, "started_at": a.TestCase.StartedAt.UTC().Format(time.RFC3339Nano), "outcome": outcome(a.TestCase.Status), "capture_state": state, "source_ids": actual, "contracts": bindings, "artifacts": []Object{{"name": "recording", "kind": "recording", "path": rp, "sha256": hash(files[rp]), "media_type": "application/json", "profile": Object{"id": RecordingID, "version": RecordingVersion}}}, "diagnostics": diagnostics, "capture_scope": Object{"description": "Wiretap's configured recording boundaries; the recording preserves HTTP, gRPC and event observations, assertions, validation and capture limitations without claiming exhaustive application behavior.", "subject_ids": scopeIDs}, "transformation": Object{"source_record_sha256": originalDigest, "profile": Object{"id": RecordingID, "version": RecordingVersion}, "description": "Restore retained inline payload bytes and map frozen contract references to stable publication packages; preserve ordered observations and original execution identity."}}
			if !a.TestCase.FinishedAt.IsZero() {
				attempt["finished_at"] = a.TestCase.FinishedAt.UTC().Format(time.RFC3339Nano)
			} else {
				attempt["outcome"] = "unknown"
			}
			if err := putJSON(p, attempt, "evidence"); err != nil {
				return nil, nil, err
			}
			evidence = append(evidence, Object{"test_id": id, "slot": slot, "group": g, "path": p})
		}
	}
	// Old measurements cannot be carried forward across a changed evidence or
	// contract graph without recomputation by their declared semantic profile.
	if len(list(prior["measurements"])) > 0 {
		return nil, nil, fmt.Errorf("measurement profile regeneration is unsupported; resolve retained measurements explicitly before publishing")
	}
	pubSources := []Object{}
	for _, s := range sources {
		pubSources = append(pubSources, s)
	}
	sortObjects(pubSources, "id")
	pub := object(merge(oldPub, Object{"producer": Object{"name": "wiretap", "version": RecordingVersion}, "sources": pubSources, "target_source_ids": target, "selection": selection, "diagnostics": []Object{}}))
	if err := putJSON("publication.json", pub, "provenance"); err != nil {
		return nil, nil, err
	}
	subList := []Object{}
	for _, s := range subjects {
		subList = append(subList, s)
	}
	sortObjects(subList, "id")
	// Stable slot metadata from other consumers survives replacing its execution.
	for i, slot := range evidence {
		for _, old := range list(prior["evidence"]) {
			if object(old)["test_id"] == object(slot)["test_id"] && object(old)["slot"] == object(slot)["slot"] {
				evidence[i] = merge(old, slot)
				break
			}
		}
	}
	// Views and manifests must use the same ordering, independent of completion.
	sort.Slice(evidence, func(i, j int) bool {
		return lessFields(object(evidence[i]), object(evidence[j]), "test_id", "slot")
	})
	for _, s := range subList {
		p := str(s["page"])
		if e := oldReg[p]; e != nil && e["ownership"] == "authored" {
			continue
		}
		b, err := renderSubject(s, subList, contractEntries, evidence, before[p])
		if err != nil {
			return nil, nil, err
		}
		if err := put(p, b, "concept", "text/markdown"); err != nil {
			return nil, nil, err
		}
	}
	for id, migration := range migrationMap {
		s := subjects[id]
		p := str(migration["from"])
		oldFM, _, err := frontmatter(before[p])
		if err != nil {
			return nil, nil, err
		}
		oldFM["status"] = "deprecated"
		oldFM["description"] = "Moved to " + str(migration["to"])
		if gen := object(oldFM["generated"]); gen != nil {
			delete(gen, "at")
		}
		y, err := marshalFrontmatter(oldFM)
		if err != nil {
			return nil, nil, err
		}
		body := fmt.Sprintf("---\n%s---\n# %s\n\nThis concept has moved to [%s](%s).\n", y, display(str(s["title"])), display(str(s["title"])), markdownRelative(p, str(migration["to"])))
		if err := put(p, []byte(body), "concept", "text/markdown"); err != nil {
			return nil, nil, err
		}
	}
	var index strings.Builder
	index.WriteString("---\nokf_version: \"0.2\"\n---\n\n# Application knowledge\n\n")
	for _, s := range subList {
		fmt.Fprintf(&index, "- [%s](%s) — %s.\n", display(str(s["title"])), str(s["page"]), str(s["kind"]))
	}
	if err := put("index.md", []byte(index.String()), "index", "text/markdown"); err != nil {
		return nil, nil, err
	}
	contractList := []Object{}
	for _, e := range contractEntries {
		contractList = append(contractList, e)
	}
	sortObjects(contractList, "id")
	sort.Slice(evidence, func(i, j int) bool {
		a, b := object(evidence[i]), object(evidence[j])
		return lessFields(a, b, "test_id", "slot")
	})
	entries := []Object{}
	for _, p := range sortedPaths(files) {
		if e := reg[p]; e != nil {
			entries = append(entries, e)
		}
	}
	caps := []string{}
	if len(evidence) > 0 {
		caps = append(caps, "test-result.v1")
	}
	sort.Slice(removed, func(i, j int) bool { return str(object(removed[i])["id"]) < str(object(removed[j])["id"]) })
	sort.Slice(migrations, func(i, j int) bool {
		a, b := object(migrations[i]), object(migrations[j])
		return lessFields(a, b, "subject_id", "from", "to")
	})
	m := object(merge(prior, Object{"profile": Object{"id": "application-knowledge", "version": Version}, "okf_version": "0.2", "bundle_id": c.BundleID, "application": c.Application, "inventory_scope": c.InventoryScope, "publication": "publication.json", "required_capabilities": caps, "subjects": subList, "contracts": contractList, "evidence": evidence, "measurements": []any{}, "files": entries, "removed_subjects": removed, "migrations": migrations}))
	if m["migrations"] == nil {
		m["migrations"] = []any{}
	}
	b, err := coreCanonical(m)
	if err != nil {
		return nil, nil, err
	}
	files["bundle.json"] = b
	return files, roles, nil
}
func listValue(v any) []any { b, _ := Canonical(v); result, _ := Decode(b); return list(result) }
func sortObjects(a []Object, key string) {
	sort.Slice(a, func(i, j int) bool { return str(a[i][key]) < str(a[j][key]) })
}
func uniqueBindings(a []Object) []Object {
	out := []Object{}
	seen := map[string]bool{}
	for _, x := range a {
		id := str(x["contract_id"])
		if !seen[id] {
			seen[id] = true
			out = append(out, x)
		}
	}
	return out
}
func display(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "[", "\\[", "]", "\\]", "#", "\\#").Replace(s)
}
func renderSubject(s Object, subjects []Object, contracts map[string]Object, evidence []any, old []byte) ([]byte, error) {
	kind := str(s["kind"])
	title := str(s["title"])
	fm := Object{"type": kind, "title": title, "description": "Declared " + kind + ": " + title, "generated": Object{"by": "wiretap/" + RecordingVersion}, "application_knowledge": Object{"subject_id": s["id"]}}
	if s["resource"] != nil {
		fm["resource"] = s["resource"]
	}
	if s["status"] == "deprecated" {
		fm["status"] = "deprecated"
	}
	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n", display(title))
	if kind == "operation" {
		fmt.Fprintf(&body, "Protocol: `%s`\n\nOperation: `%s`\n\n", str(s["protocol"]), str(s["operation"]))
	}
	for _, other := range subjects {
		if other["parent_id"] == s["id"] {
			fmt.Fprintf(&body, "- [%s](%s)\n", display(str(other["title"])), markdownRelative(str(s["page"]), str(other["page"])))
		}
	}
	contractIDs := listValue(s["contract_ids"])
	sort.Slice(contractIDs, func(i, j int) bool { return str(contractIDs[i]) < str(contractIDs[j]) })
	for _, cid := range contractIDs {
		c := contracts[str(cid)]
		if c != nil {
			fmt.Fprintf(&body, "\n[Contract: %s](%s) (%s).\n", display(str(c["key"])), markdownRelative(str(s["page"]), str(c["manifest"])), str(c["origin"]))
		}
	}
	if kind == "test" {
		for _, x := range evidence {
			x := object(x)
			if x["test_id"] == s["id"] {
				fmt.Fprintf(&body, "\n[Selected evidence: %s](%s).\n", str(x["slot"]), markdownRelative(str(s["page"]), str(x["path"])))
			}
		}
		body.WriteString("\nExecution outcomes, source revisions and capture limitations are recorded in the evidence.\n")
	}
	if kind == "application" {
		body.WriteString("\nThis inventory follows the scope declared in bundle.json. It does not assert exhaustive behavior or coverage.\n")
	}
	if len(old) > 0 {
		om, ob, err := frontmatter(old)
		if err != nil {
			return nil, err
		}
		fm = object(merge(om, fm))
		a, _ := Canonical(om)
		b, _ := Canonical(fm)
		if bytes.Equal(a, b) && ob == body.String() {
			return old, nil
		}
		if gen := object(fm["generated"]); gen != nil {
			delete(gen, "at")
		}
	}
	y, err := marshalFrontmatter(fm)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(y) + "---\n" + body.String()), nil
}

// Read returns verified structured records without conflating unknown optional
// profiles with valid semantic measurements. Unknown fields remain available.
func Read(root string) (Object, error) {
	f, err := snapshot(root)
	if err != nil {
		return nil, err
	}
	v := validateFiles(f, Validation{BaseOKF: "valid", Application: "not-present", Evidence: "not-present", Measurement: "not-present", OptionalProfiles: []string{}, Diagnostics: []Diagnostic{}})
	if !v.Valid() || v.Application != "valid" {
		return nil, fmt.Errorf("bundle cannot be consumed: %+v", v)
	}
	value, err := Decode(f["bundle.json"])
	if err != nil {
		return nil, err
	}
	manifest := object(value)
	result := Object{"manifest": manifest, "validation": v}
	publication, err := Decode(f[str(manifest["publication"])])
	if err != nil {
		return nil, err
	}
	result["publication"] = publication
	attempts := []any{}
	recordings := []Object{}
	for _, ref := range list(manifest["evidence"]) {
		a, err := Decode(f[str(object(ref)["path"])])
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, a)
		for _, attachment := range list(object(a)["artifacts"]) {
			attachment := object(attachment)
			profile := object(attachment["profile"])
			if profile["id"] != RecordingID || profile["version"] != RecordingVersion {
				continue
			}
			p := str(attachment["path"])
			record, err := Decode(f[p])
			if err != nil {
				return nil, err
			}
			recordings = append(recordings, Object{"path": p, "sha256": attachment["sha256"], "record": record})
		}
	}
	result["attempts"] = attempts
	result["recordings"] = recordings
	return result, nil
}

// Compare tuples field-by-field; IDs may themselves contain separators.
func lessFields(a, b Object, fields ...string) bool {
	for _, field := range fields {
		if str(a[field]) != str(b[field]) {
			return str(a[field]) < str(b[field])
		}
	}
	return false
}
