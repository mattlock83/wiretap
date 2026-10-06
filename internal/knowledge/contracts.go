package knowledge

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
)

type frozenContract struct {
	entry        Object
	root         string
	rootHash     string
	manifestHash string
}

func freezeContract(source, previous map[string][]byte, archiveRoot string, s artifact.SchemaRef, c ContractMapping, put func(string, []byte, string, string) error) (frozenContract, error) {
	result := frozenContract{}
	if !safeKey(c.Key) || c.ID == "" || len(c.SubjectIDs) == 0 || (c.Origin != "supplied" && c.Origin != "inferred") {
		return result, fmt.Errorf("contract %s requires explicit ID, safe key, subjects and origin", s.ID)
	}
	rel, err := filepath.Rel(archiveRoot, s.Source.Path)
	if err != nil {
		return result, err
	}
	rel = filepath.ToSlash(rel)
	if !safePath(rel) {
		return result, fmt.Errorf("contract escapes input archive")
	}
	oldManifestPath := path.Join(path.Dir(rel), "manifest.json")
	v, err := Decode(source[oldManifestPath])
	if err != nil {
		return result, err
	}
	old := object(v)
	names := map[string]string{}
	folded := map[string]bool{}
	mapping := []Object{}
	for _, x := range list(old["files"]) {
		x := object(x)
		op := str(x["path"])
		name := c.Files[strings.TrimPrefix(op, path.Dir(oldManifestPath)+"/")]
		if name == "" {
			name = str(x["source_name"])
		}
		if name == "" {
			name = strings.TrimPrefix(op, path.Dir(oldManifestPath)+"/")
			if strings.HasPrefix(path.Base(name), "doc-") {
				return result, fmt.Errorf("legacy OpenAPI %s requires an explicit readable files mapping for %s", s.ID, name)
			}
		}
		if !safePath(name) || name == "manifest.json" || folded[strings.ToLower(name)] {
			return result, fmt.Errorf("contract source name %q requires an explicit collision-free mapping", name)
		}
		folded[strings.ToLower(name)] = true
		names[op] = name
		sourceName := str(x["source_name"])
		if sourceName == "" {
			sourceName = name
		}
		if !safeSourceIdentifier(sourceName) {
			return result, fmt.Errorf("CON-03 source identifier must omit credentials and absolute checkout paths")
		}
		mapping = append(mapping, Object{"source": sourceName, "path": name})
	}
	if names[rel] == "" {
		return result, fmt.Errorf("entrypoint not in frozen archive closure")
	}
	base := "contracts/" + c.Key
	files := []Object{}
	oldPaths := make([]string, 0, len(names))
	for p := range names {
		oldPaths = append(oldPaths, p)
	}
	sort.Strings(oldPaths)
	for _, op := range oldPaths {
		data, ok := source[op]
		if !ok {
			return result, fmt.Errorf("missing frozen contract %s", op)
		}
		name := names[op]
		if s.Type == "asyncapi" && name != strings.TrimPrefix(op, path.Dir(oldManifestPath)+"/") {
			return result, fmt.Errorf("AsyncAPI source filenames must retain their original paths; renamed imports require an explicit upstream package transformation")
		}
		if s.Type == "openapi" {
			v, err := Decode(data)
			if err != nil {
				return result, err
			}
			var rewrite func(any) error
			rewrite = func(v any) error {
				switch v := v.(type) {
				case Object:
					if raw := str(v["$ref"]); raw != "" {
						u, err := url.Parse(raw)
						if err != nil || u.IsAbs() || strings.HasPrefix(u.Path, "/") {
							return fmt.Errorf("unfrozen reference %q", raw)
						}
						if u.Path != "" {
							target := names[path.Join(path.Dir(op), u.Path)]
							if target == "" {
								return fmt.Errorf("contract reference missing from closure: %s", raw)
							}
							relative, err := filepath.Rel(filepath.FromSlash(path.Dir(name)), filepath.FromSlash(target))
							if err != nil {
								return err
							}
							u.Path = filepath.ToSlash(relative)
							v["$ref"] = u.String()
						}
					}
					for _, x := range v {
						if err := rewrite(x); err != nil {
							return err
						}
					}
				case []any:
					for _, x := range v {
						if err := rewrite(x); err != nil {
							return err
						}
					}
				}
				return nil
			}
			if err := rewrite(v); err != nil {
				return result, err
			}
			data, err = Canonical(v)
			if err != nil {
				return result, err
			}
		}
		media := "application/octet-stream"
		if s.Type == "openapi" || strings.HasSuffix(name, ".json") {
			media = "application/json"
		} else if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			media = "application/yaml"
		} else if s.Type != "protobuf" {
			media = "text/plain"
		}
		// A contract source named .md is data, not an OKF concept. Require an
		// explicit non-Markdown mapping rather than rewriting the source bytes.
		if strings.HasSuffix(name, ".md") {
			return result, fmt.Errorf("contract .md source requires an explicit .txt/.bin files mapping")
		}
		if err := put(base+"/"+name, data, "contract", media); err != nil {
			return result, err
		}
		files = append(files, Object{"path": name, "sha256": hash(data)})
		if op == rel {
			result.root = base + "/" + name
			result.rootHash = hash(data)
		}
	}
	sort.Slice(files, func(i, j int) bool { return str(files[i]["path"]) < str(files[j]["path"]) })
	sort.Slice(mapping, func(i, j int) bool { return lessFields(mapping[i], mapping[j], "source", "path") })
	manifest := Object{"contract_id": c.ID, "format": s.Type, "entrypoint": names[rel], "files": files, "source_map": mapping}
	if s.Type == "openapi" {
		manifest["extensions"] = Object{Owner: Object{"transformation": "wiretap-openapi-json-v1: deterministic JSON normalization and local-reference rewriting; source document names preserved or explicitly mapped"}}
	}
	if oldBytes := previous[base+"/manifest.json"]; oldBytes != nil {
		old, err := Decode(oldBytes)
		if err != nil {
			return result, err
		}
		oldMap := object(old)
		for _, pair := range []struct {
			next []Object
			old  []any
			key  string
		}{{files, list(oldMap["files"]), "path"}, {mapping, list(oldMap["source_map"]), "source"}} {
			for i, x := range pair.next {
				for _, previous := range pair.old {
					previous := object(previous)
					if previous[pair.key] == x[pair.key] {
						pair.next[i] = object(merge(previous, x))
						break
					}
				}
			}
		}
		manifest = object(merge(oldMap, manifest))
	}
	data, err := coreCanonical(manifest)
	if err != nil {
		return result, err
	}
	if err := put(base+"/manifest.json", data, "contract", "application/json"); err != nil {
		return result, err
	}
	result.manifestHash = hash(data)
	result.entry = Object{"id": c.ID, "key": c.Key, "subject_ids": c.SubjectIDs, "origin": c.Origin, "format": s.Type, "manifest": base + "/manifest.json"}
	return result, nil
}

func safeSourceIdentifier(source string) bool {
	u, err := url.Parse(source)
	if err != nil || source == "" || u.User != nil || strings.Contains(source, "\\") || strings.HasPrefix(u.Path, "/") && u.Scheme == "" || strings.EqualFold(u.Scheme, "file") || len(u.Scheme) == 1 || u.Host != "" && u.Scheme == "" {
		return false
	}
	return true
}
