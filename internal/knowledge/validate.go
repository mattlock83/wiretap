package knowledge

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
	bundlespec "github.com/phrond/wiretap/docs"
	"github.com/phrond/wiretap/internal/schemajson"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

type Diagnostic struct {
	Level       string `json:"level"`
	Requirement string `json:"requirement"`
	Path        string `json:"path"`
	Message     string `json:"message"`
}
type Validation struct {
	BaseOKF          string       `json:"base_okf"`
	Application      string       `json:"application_profile"`
	Evidence         string       `json:"evidence_profile"`
	Measurement      string       `json:"measurement_profile"`
	OptionalProfiles []string     `json:"unsupported_optional_profiles"`
	Diagnostics      []Diagnostic `json:"diagnostics"`
}

func (v Validation) Valid() bool {
	return v.BaseOKF == "valid" && (v.Application == "valid" || v.Application == "not-present") && v.Evidence != "invalid"
}
func (v *Validation) fail(level, req, p string, err any) {
	v.Diagnostics = append(v.Diagnostics, Diagnostic{level, req, p, fmt.Sprint(err)})
	switch level {
	case "base-okf":
		v.BaseOKF = "invalid"
	case "application":
		v.Application = "invalid"
	case "evidence":
		v.Evidence = "invalid"
		v.Application = "invalid"
	}
}

type ecmaRegex struct{ *regexp2.Regexp }

func (r ecmaRegex) MatchString(s string) bool {
	ok, err := r.Regexp.MatchString(s)
	return err == nil && ok
}

type offlineLoader struct{}

func (offlineLoader) Load(u string) (any, error) {
	return nil, fmt.Errorf("external schema loading disabled: %s", u)
}

var schemaOnce sync.Once
var compiled map[string]*jsonschema.Schema
var schemaError error

func schemaCheck(name string, v any) error {
	schemaOnce.Do(func() {
		compiled = map[string]*jsonschema.Schema{}
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		c.UseLoader(offlineLoader{})
		c.UseRegexpEngine(func(s string) (jsonschema.Regexp, error) {
			r, err := regexp2.Compile(s, regexp2.ECMAScript)
			if err != nil {
				return nil, err
			}
			r.MatchTimeout = time.Second
			return ecmaRegex{r}, nil
		})
		legacy, err := bundlespec.Schemas.ReadFile("bundle-evidence.schema.json")
		if err != nil {
			schemaError = err
			return
		}
		lv, err := Decode(legacy)
		if err != nil {
			schemaError = err
			return
		}
		if err := c.AddResource("https://wiretap.invalid/bundle-evidence.schema.json", lv); err != nil {
			schemaError = err
			return
		}
		entries, err := bundlespec.Schemas.ReadDir("spec")
		if err != nil {
			schemaError = err
			return
		}
		for _, entry := range entries {
			b, err := bundlespec.Schemas.ReadFile("spec/" + entry.Name())
			if err != nil {
				schemaError = err
				return
			}
			v, err := Decode(b)
			if err != nil {
				schemaError = err
				return
			}
			if err := c.AddResource("https://wiretap.invalid/spec/"+entry.Name(), v); err != nil {
				schemaError = err
				return
			}
		}
		for _, entry := range entries {
			s, err := c.Compile("https://wiretap.invalid/spec/" + entry.Name())
			if err != nil {
				schemaError = err
				return
			}
			compiled[entry.Name()] = s
		}
	})
	if schemaError != nil {
		return schemaError
	}
	s := compiled[name]
	if s == nil {
		return fmt.Errorf("unknown schema %s", name)
	}
	return s.Validate(v)
}

// Snapshot rejects filesystem aliases before reading any file. Limits are
// explicit errors, never silently truncated valid bundles.
func snapshot(root string) (map[string][]byte, error) {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("bundle root must be an ordinary directory")
	}
	out := map[string][]byte{}
	folded := map[string]string{}
	total := int64(0)
	err = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if prior := folded[strings.ToLower(rel)]; prior != "" {
			return fmt.Errorf("case-fold path collision: %s and %s", prior, rel)
		}
		folded[strings.ToLower(rel)] = rel
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink forbidden: %s", rel)
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file: %s", rel)
		}
		s := reflect.Indirect(reflect.ValueOf(info.Sys()))
		if s.IsValid() && s.Kind() == reflect.Struct {
			n := s.FieldByName("Nlink")
			if n.IsValid() && n.CanUint() && n.Uint() > 1 {
				return fmt.Errorf("hard link forbidden: %s", rel)
			}
		}
		total += info.Size()
		if len(out) >= 10000 || info.Size() > 128<<20 || total > 512<<20 {
			return fmt.Errorf("READ-02 resource limit exceeded at %s", rel)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	return out, err
}

func frontmatter(b []byte, profile ...bool) (Object, string, error) {
	if !utf8.Valid(b) || bytes.HasPrefix(b, []byte{0xef, 0xbb, 0xbf}) {
		return nil, "", fmt.Errorf("invalid UTF-8 or BOM")
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, s, fmt.Errorf("missing YAML frontmatter")
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		if strings.HasSuffix(s, "\n---") {
			end = len(s) - 8
		} else {
			return nil, s, fmt.Errorf("unclosed YAML frontmatter")
		}
	}
	end += 4
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s[4:end]), &n); err != nil {
		return nil, s, err
	}
	var walk func(*yaml.Node, int) error
	visited := map[*yaml.Node]bool{}
	active := map[*yaml.Node]bool{}
	walk = func(n *yaml.Node, depth int) error {
		if active[n] {
			return fmt.Errorf("cyclic YAML alias")
		}
		if visited[n] {
			return nil
		}
		if len(visited) >= 100000 {
			return fmt.Errorf("READ-02 YAML node limit exceeded")
		}
		visited[n] = true
		active[n] = true
		defer delete(active, n)
		if depth > 64 {
			return fmt.Errorf("YAML nesting limit")
		}
		if n.Kind == yaml.AliasNode {
			if len(profile) > 0 && profile[0] {
				return fmt.Errorf("DIFF-04 YAML aliases are forbidden")
			}
			return walk(n.Alias, depth+1)
		}
		if n.Tag == "!!timestamp" {
			n.Tag = "!!str"
		}
		if n.Tag != "" && !strings.HasPrefix(n.Tag, "!!") {
			return fmt.Errorf("unsafe YAML tag %s", n.Tag)
		}
		for _, c := range n.Content {
			if err := walk(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(&n, 0); err != nil {
		return nil, s, err
	}
	v, err := schemajson.DecodeNode(&n)
	if err != nil {
		return nil, s, err
	}
	m := object(v)
	if m == nil {
		return nil, s, fmt.Errorf("frontmatter must be a mapping")
	}
	if end+5 > len(s) {
		return m, "", nil
	}
	return m, s[end+5:], nil
}
func baseCheck(files map[string][]byte, r *Validation) map[string]Object {
	meta := map[string]Object{}
	names := sortedPaths(files)
	for _, p := range names {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		b := files[p]
		m, body, err := frontmatter(b)
		switch path.Base(p) {
		case "index.md":
			if bytes.HasPrefix(b, []byte("---")) && (p != "index.md" || err != nil || len(m) != 1 || m["okf_version"] == nil) {
				r.fail("base-okf", "BASE-02", p, "reserved index frontmatter")
			}
		case "log.md":
			if bytes.HasPrefix(b, []byte("---")) {
				r.fail("base-okf", "BASE-02", p, "log must not have concept frontmatter")
			}
			last := "9999-99-99"
			seen := false
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "## ") {
					date := strings.TrimPrefix(line, "## ")
					if _, err := time.Parse("2006-01-02", date); err != nil || date > last {
						r.fail("base-okf", "BASE-02", p, "log dates must be YYYY-MM-DD, newest first")
					}
					last = date
					seen = true
				}
			}
			if !seen {
				r.fail("base-okf", "BASE-02", p, "log requires date-grouped entries")
			}
		default:
			if err != nil {
				r.fail("base-okf", "BASE-04", p, err)
			} else if strings.TrimSpace(str(m["type"])) == "" {
				r.fail("base-okf", "BASE-04", p, "concept requires a nonempty string type")
			} else {
				meta[p] = m
			}
		}
		if !utf8.Valid(b) {
			r.fail("base-okf", "BASE-01", p, "invalid UTF-8")
		}
		_ = body
	}
	return meta
}
func sortedPaths(files map[string][]byte) []string {
	a := make([]string, 0, len(files))
	for p := range files {
		a = append(a, p)
	}
	sort.Strings(a)
	return a
}

// Validate assesses the Markdown, core package and evidence independently. It
// never executes captured instructions or fetches contracts and profiles.
func Validate(root string) Validation {
	r := Validation{BaseOKF: "valid", Application: "not-present", Evidence: "not-present", Measurement: "not-present", OptionalProfiles: []string{}, Diagnostics: []Diagnostic{}}
	_, err := os.Lstat(root)
	if err != nil {
		r.BaseOKF = "unchecked"
		r.fail("application", "READ-02", root, err)
		return r
	}
	f, err := snapshot(root)
	if err != nil {
		r.BaseOKF = "unchecked"
		req := "PATH-02"
		if strings.Contains(err.Error(), "resource limit") {
			req = "READ-02"
		}
		r.fail("application", req, root, err)
		return r
	}
	return validateFiles(f, r)
}
func validateFiles(f map[string][]byte, r Validation) Validation {
	var total int64
	folded := map[string]string{}
	for p, b := range f {
		total += int64(len(b))
		if len(f) > 10000 || len(b) > 128<<20 || total > 512<<20 {
			r.BaseOKF = "unchecked"
			r.fail("application", "READ-02", p, "bundle resource limit exceeded")
			return r
		}
		lower := strings.ToLower(p)
		if old := folded[lower]; old != "" {
			r.BaseOKF = "unchecked"
			r.fail("application", "PATH-02", p, "case-folded path collision with "+old)
			return r
		}
		folded[lower] = p
	}
	meta := baseCheck(f, &r)
	b, present := f["bundle.json"]
	if !present {
		return r
	}
	v, err := Decode(b)
	if err != nil {
		r.fail("application", "MAN-01", "bundle.json", err)
		return r
	}
	m := object(v)
	prof := object(m["profile"])
	if str(prof["id"]) != "application-knowledge" || str(prof["version"]) != Version {
		r.Application = "unsupported"
		r.Diagnostics = append(r.Diagnostics, Diagnostic{"application", "VER-03", "bundle.json", "unsupported application profile"})
		return r
	}
	r.Application = "valid"
	unsupported := false
	if err := schemaCheck("manifest.schema.json", m); err != nil {
		r.fail("application", "MAN-01", "bundle.json", err)
		return r
	}
	hasTestResults := false
	for _, capability := range list(m["required_capabilities"]) {
		hasTestResults = hasTestResults || capability == "test-result.v1"
		if capability != "test-result.v1" {
			unsupported = true
			r.Diagnostics = append(r.Diagnostics, Diagnostic{"application", "VER-03", "bundle.json", "unsupported required capability: " + str(capability)})
		}
	}
	if len(list(m["evidence"])) > 0 && !hasTestResults {
		r.fail("application", "VER-02", "bundle.json", "test results require test-result.v1 capability")
	}
	for p := range meta {
		if _, _, err := frontmatter(f[p], true); err != nil {
			r.fail("application", "DIFF-04", p, err)
		}
	}
	if err := coreNumbers(m); err != nil {
		r.fail("application", "DIFF-02", "bundle.json", err)
	}
	for p := range f {
		if !safePath(p) {
			r.fail("application", "PATH-02", p, "unsafe or reserved file path")
		}
	}
	reg := map[string]Object{}
	for _, x := range list(m["files"]) {
		x := object(x)
		p := str(x["path"])
		if reg[p] != nil || p == "bundle.json" {
			r.fail("application", "MAN-02", p, "duplicate/self registry entry")
		}
		reg[p] = x
		data, ok := f[p]
		if !ok || hash(data) != str(x["sha256"]) {
			r.fail("application", "MAN-02", p, "missing file or SHA-256 mismatch")
		}
	}
	ref := func(p, role string) bool {
		if !safePath(p) || reg[p] == nil || str(reg[p]["role"]) != role {
			r.fail("application", "MAN-05", p, "unregistered or incorrect file role")
			return false
		}
		return true
	}
	if ref("index.md", "index") {
		im, _, err := frontmatter(f["index.md"])
		if err != nil || im["okf_version"] != "0.2" {
			r.fail("application", "BASE-02", "index.md", "required OKF version 0.2")
		}
	}
	subjects := map[string]Object{}
	ids := map[string]bool{}
	keys := map[string]string{}
	pages := map[string]string{}
	appCount := 0
	for _, x := range list(m["subjects"]) {
		s := object(x)
		id := str(s["id"])
		p := str(s["page"])
		key := str(s["key"])
		kind := str(s["kind"])
		if ids[id] {
			r.fail("application", "ID-04", p, "duplicate identity")
		}
		ids[id] = true
		subjects[id] = s
		if !safeKey(key) {
			r.fail("application", "ID-05", p, "unsafe subject key")
		}
		scope := kind + ":" + key
		if kind == "operation" {
			scope = str(s["parent_id"]) + ":" + scope
		}
		if keys[scope] != "" {
			r.fail("application", "ID-05", p, "key collision")
		}
		keys[scope] = id
		if pages[p] != "" {
			r.fail("application", "ID-04", p, "primary page shared by subjects")
		}
		pages[p] = id
		if path.Base(p) == "index.md" || path.Base(p) == "log.md" {
			r.fail("application", "BASE-02", p, "reserved primary page")
		}
		if ref(p, "concept") {
			fm := meta[p]
			if err := schemaCheck("concept.schema.json", fm); err != nil {
				r.fail("application", "VIEW-01", p, err)
			}
			if str(object(fm["application_knowledge"])["subject_id"]) != id || fm["title"] != s["title"] {
				r.fail("application", "MAN-05", p, "frontmatter binding/title mismatch")
			}
		}
		if kind == "application" {
			appCount++
			app := object(m["application"])
			if p != "system.md" || s["title"] != app["title"] || (s["resource"] != nil && s["resource"] != app["id"]) {
				r.fail("application", "MAN-05", p, "application declaration mismatch")
			}
		}
	}
	if appCount != 1 {
		r.fail("application", "ID-02", "bundle.json", "exactly one application subject required")
	}
	for _, s := range subjects {
		kind := str(s["kind"])
		if kind != "application" {
			parent := subjects[str(s["parent_id"])]
			pk := str(parent["kind"])
			if parent == nil || (kind == "operation" && pk != "provided-api" && pk != "dependency") || (kind != "operation" && pk != "application") {
				r.fail("application", "MAN-05", str(s["page"]), "invalid parent binding")
			}
		}
		expected := subjectPath(s, subjects)
		if expected == "" || str(s["page"]) != expected {
			r.fail("application", "PATH-04", str(s["page"]), "primary path does not follow subject mapping")
		}
	}
	removedIDs := map[string]Object{}
	removedPaths := map[string]bool{}
	for _, tomb := range list(m["removed_subjects"]) {
		tomb := object(tomb)
		id := str(tomb["id"])
		p := str(tomb["path"])
		if removedIDs[id] != nil || ids[id] || pages[p] != "" || removedPaths[p] || !safePath(p) || !safeKey(str(tomb["key"])) {
			r.fail("application", "ID-07", p, "duplicate, unsafe or reused tombstone identity/path")
		}
		removedIDs[id] = tomb
		ids[id] = true
		removedPaths[p] = true
	}
	for _, migration := range list(m["migrations"]) {
		migration := object(migration)
		id := str(migration["subject_id"])
		from, to := str(migration["from"]), str(migration["to"])
		if (subjects[id] == nil && removedIDs[id] == nil) || !safePath(from) || !safePath(to) || from == to || (pages[from] != "" && pages[from] != id) {
			r.fail("application", "ID-06", from, "invalid or reassigned migration")
		}
		if _, ok := f[from]; !ok {
			r.fail("application", "ID-06", from, "migration forwarding concept missing")
		} else if meta[from]["status"] != "deprecated" {
			r.fail("application", "ID-06", from, "migration must retain deprecated forwarding concept")
		}
		checkLinks(f, from, &r, true)
	}
	for _, s := range subjects {
		if id := str(s["replaces_id"]); id != "" && subjects[id] == nil && removedIDs[id] == nil {
			r.fail("application", "MAN-05", str(s["page"]), "unresolved replacement identity")
		}
	}
	contracts := map[string]Object{}
	contractHashes := map[string]string{}
	for _, x := range list(m["contracts"]) {
		c := object(x)
		id := str(c["id"])
		p := str(c["manifest"])
		if ids[id] {
			r.fail("application", "ID-04", p, "duplicate identity")
		}
		ids[id] = true
		contracts[id] = c
		if !safeKey(str(c["key"])) || p != "contracts/"+str(c["key"])+"/manifest.json" || keys["contract:"+str(c["key"])] != "" {
			r.fail("application", "CON-01", p, "invalid contract slot")
		}
		keys["contract:"+str(c["key"])] = id
		for _, sid := range list(c["subject_ids"]) {
			if subjects[str(sid)] == nil {
				r.fail("application", "MAN-05", p, "missing contract subject")
			}
		}
		if !ref(p, "contract") {
			continue
		}
		cm, err := Decode(f[p])
		if err != nil {
			r.fail("application", "CON-02", p, err)
			continue
		}
		if err := schemaCheck("contract.schema.json", cm); err != nil {
			r.fail("application", "CON-02", p, err)
			continue
		}
		d := object(cm)
		if d["contract_id"] != id || d["format"] != c["format"] {
			r.fail("application", "CON-02", p, "contract identity mismatch")
		}
		contractHashes[id] = hash(f[p])
		closure := map[string]bool{}
		for _, y := range list(d["files"]) {
			y := object(y)
			local := str(y["path"])
			target := path.Join(path.Dir(p), local)
			if closure[local] || !safePath(local) || local == "manifest.json" {
				r.fail("application", "CON-02", p, "invalid/duplicate package file")
			}
			closure[local] = true
			if ref(target, "contract") && str(y["sha256"]) != str(reg[target]["sha256"]) {
				r.fail("application", "CON-02", target, "package hash mismatch")
			}
		}
		if !closure[str(d["entrypoint"])] {
			r.fail("application", "CON-02", p, "entrypoint missing from closure")
		}
		sourceNames := map[string]bool{}
		for _, y := range list(d["source_map"]) {
			source := str(object(y)["source"])
			if !safeSourceIdentifier(source) || sourceNames[source] {
				r.fail("application", "CON-03", p, "unsafe or ambiguous source identifier")
			}
			sourceNames[source] = true
			if !closure[str(object(y)["path"])] {
				r.fail("application", "CON-03", p, "source mapping outside closure")
			}
		}
		for rp, x := range reg {
			if str(x["role"]) == "contract" && strings.HasPrefix(rp, path.Dir(p)+"/") && rp != p && !closure[strings.TrimPrefix(rp, path.Dir(p)+"/")] {
				r.fail("application", "CON-02", rp, "contract registry outside closure")
			}
		}
		if str(c["format"]) == "openapi" || str(c["format"]) == "asyncapi" {
			checkImports(f, p, d, closure, &r)
		}
	}
	for _, s := range subjects {
		for _, cid := range list(s["contract_ids"]) {
			if contracts[str(cid)] == nil {
				r.fail("application", "MAN-05", str(s["page"]), "missing subject contract")
			}
		}
	}
	pubPath := str(m["publication"])
	var pub Object
	if ref(pubPath, "provenance") {
		p, err := Decode(f[pubPath])
		if err != nil {
			r.fail("application", "PUB-01", pubPath, err)
		} else if err := schemaCheck("publication.schema.json", p); err != nil {
			r.fail("application", "PUB-01", pubPath, err)
		} else {
			pub = object(p)
		}
	}
	if pub != nil {
		validateEvidence(f, m, pub, subjects, reg, contractHashes, &r)
	}
	for _, x := range list(m["measurements"]) {
		x := object(x)
		id := str(x["id"])
		if ids[id] {
			r.fail("application", "ID-04", str(x["path"]), "duplicate identity")
		}
		ids[id] = true
		key := str(x["key"])
		if !safeKey(key) || str(x["path"]) != "measurements/"+key+"/measurement.json" || keys["measurement:"+key] != "" {
			r.fail("application", "VIEW-04", str(x["path"]), "invalid or duplicate measurement slot")
		}
		keys["measurement:"+key] = id
		ref(str(x["path"]), "measurement")
		for _, sid := range list(x["subject_ids"]) {
			if subjects[str(sid)] == nil {
				r.fail("application", "MAN-05", str(x["path"]), "missing measurement subject")
			}
		}
		r.Measurement = "unsupported"
		r.OptionalProfiles = append(r.OptionalProfiles, str(object(x["profile"])["id"]))
	}
	checkNavigation(f, subjects, &r)
	checkLinks(f, "index.md", &r, true)
	for p := range pages {
		checkLinks(f, p, &r)
	}
	sort.Strings(r.OptionalProfiles)
	if unsupported && r.Application == "valid" {
		r.Application = "unsupported"
	}
	return r
}
func subjectPath(s Object, subjects map[string]Object) string {
	key := str(s["key"])
	switch str(s["kind"]) {
	case "application":
		return "system.md"
	case "provided-api":
		return "apis/" + key + "/api.md"
	case "dependency":
		return "dependencies/" + key + "/api.md"
	case "test":
		return "tests/" + key + "/test.md"
	case "operation":
		parent := subjects[str(s["parent_id"])]
		if parent["kind"] != "provided-api" && parent["kind"] != "dependency" {
			return ""
		}
		return path.Dir(subjectPath(parent, subjects)) + "/operations/" + key + ".md"
	}
	return ""
}

var mdLinks = regexp.MustCompile(`\[[^\]]*\]\(([^\s)]+)(?:\s+[^)]*)?\)`)

func checkLinks(f map[string][]byte, p string, r *Validation, strict ...bool) {
	all := len(strict) > 0 && strict[0]
	for _, match := range mdLinks.FindAllStringSubmatch(string(f[p]), -1) {
		target := match[1]
		u, err := url.Parse(target)
		if err != nil {
			r.fail("application", "PATH-04", p, "malformed link")
			continue
		}
		if u.IsAbs() || strings.HasPrefix(target, "#") {
			continue
		}
		target = u.Path
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		} else {
			target = path.Join(path.Dir(p), target)
		}
		if !safePath(target) {
			r.fail("application", "PATH-03", p, "unsafe local link")
			continue
		}
		if _, ok := f[target]; !ok {
			if _, ok := f[strings.TrimSuffix(target, "/")+"/index.md"]; !ok {
				if all || strings.HasPrefix(target, "evidence/") || strings.HasPrefix(target, "contracts/") {
					r.fail("application", "PATH-04", p, "unresolved local link: "+target)
				}
			}
		}
	}
}
func checkImports(f map[string][]byte, manifest string, cm Object, closure map[string]bool, r *Validation) {
	for local := range closure {
		p := path.Join(path.Dir(manifest), local)
		value, err := schemajson.Decode(f[p])
		if err != nil {
			r.fail("application", "CON-02", p, "contract reference documents must be valid JSON or YAML")
			continue
		}
		var walk func(any)
		walk = func(v any) {
			switch v := v.(type) {
			case Object:
				if raw := str(v["$ref"]); raw != "" {
					u, err := url.Parse(raw)
					if err != nil || u.IsAbs() || u.RawQuery != "" || strings.HasPrefix(u.Path, "/") {
						r.fail("application", "CON-02", p, "unfrozen contract reference")
					} else if u.Path != "" {
						dest := path.Join(path.Dir(local), u.Path)
						if !safePath(dest) || !closure[dest] {
							r.fail("application", "CON-02", p, "reference outside frozen closure")
						}
					}
				}
				for _, x := range v {
					walk(x)
				}
			case []any:
				for _, x := range v {
					walk(x)
				}
			}
		}
		walk(value)
	}
}

// Root navigation must actually expose the application and each available group.
// Linking either a group's directory index or its subjects is sufficient.
func checkNavigation(files map[string][]byte, subjects map[string]Object, r *Validation) {
	targets := []string{}
	for _, match := range mdLinks.FindAllStringSubmatch(string(files["index.md"]), -1) {
		u, err := url.Parse(match[1])
		if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" {
			continue
		}
		targets = append(targets, path.Clean(strings.TrimPrefix(u.Path, "/")))
	}
	required := map[string]bool{"system.md": true}
	for _, subject := range subjects {
		switch subject["kind"] {
		case "provided-api":
			required["apis"] = true
		case "dependency":
			required["dependencies"] = true
		case "test":
			required["tests"] = true
		}
	}
	for group := range required {
		found := false
		for _, target := range targets {
			if target == group || group != "system.md" && strings.HasPrefix(target, group+"/") {
				found = true
			}
		}
		if !found {
			r.fail("application", "BASE-02", "index.md", "missing application/group navigation: "+group)
		}
	}
}
