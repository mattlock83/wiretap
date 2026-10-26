package coverage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/schemajson"
)

// Canonicalise the frozen document graph with traversal-assigned document names.
// Frozen storage hashes include original source URIs, which are provenance, not
// contract identity. Sorted traversal preserves refs and cycles without copying
// recursive schemas or depending on repository paths and generated filenames.
func contractRevisionFiles(ctx context.Context, rootPath string, files map[string][]byte) (string, bool, error) {
	type document struct {
		uri   *url.URL
		value any
	}
	root := &url.URL{Scheme: "file", Path: "/" + rootPath}
	ids := map[string]int{root.String(): 0}
	docs := []document{{uri: root}}
	imprecise := map[string]bool{}
	for i := 0; i < len(docs); i++ {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		uri := docs[i].uri
		if uri.Scheme != "file" || uri.Host != "" || uri.RawQuery != "" || uri.Opaque != "" || uri.User != nil {
			return "", false, fmt.Errorf("Unfrozen external schema reference")
		}
		raw, ok := files[strings.TrimPrefix(path.Clean(uri.Path), "/")]
		if !ok {
			return "", false, fmt.Errorf("Contract reference is absent from frozen manifest")
		}
		value, err := schemajson.Decode(raw)
		if err != nil {
			return "", false, err
		}
		var checkNumbers func(any, string)
		checkNumbers = func(value any, location string) {
			switch n := value.(type) {
			case json.Number:
				r := numberRat(n)
				f, err := strconv.ParseFloat(string(n), 64)
				if err != nil || r == nil || numberRat(f) == nil || r.Cmp(numberRat(f)) != 0 {
					imprecise[fmt.Sprintf("%d:%s", i, location)] = true
				}
			case map[string]any:
				for name, child := range n {
					checkNumbers(child, location+"/"+pointerToken(name))
				}
			case []any:
				for j, child := range n {
					checkNumbers(child, fmt.Sprintf("%s/%d", location, j))
				}
			}
		}
		checkNumbers(value, "")
		var rewrite func(any) error
		rewrite = func(v any) error {
			switch n := v.(type) {
			case map[string]any:
				for _, key := range sortedMapKeys(n) {
					if key == "$ref" {
						reference, ok := n[key].(string)
						if !ok {
							continue
						}
						parsed, err := url.Parse(reference)
						if err != nil {
							return err
						}
						resolved := docs[i].uri.ResolveReference(parsed)
						fragment := resolved.EscapedFragment()
						resolved.Fragment = ""
						resolved.RawFragment = ""
						id, ok := ids[resolved.String()]
						if !ok {
							if len(docs) >= 128 {
								return fmt.Errorf("Too many contract reference documents")
							}
							id = len(docs)
							ids[resolved.String()] = id
							docs = append(docs, document{uri: resolved})
						}
						n[key] = fmt.Sprintf("doc:%d#%s", id, fragment)
					} else if err := rewrite(n[key]); err != nil {
						return err
					}
				}
			case []any:
				for _, v := range n {
					if err := rewrite(v); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := rewrite(value); err != nil {
			return "", false, err
		}
		docs[i].value = value
	}
	values := make([]any, len(docs))
	for i, doc := range docs {
		values[i] = doc.value
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", false, err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), contractPrecision(values, imprecise), nil
}

func numberRatJSON(s string) *big.Rat { return numberRat(json.Number(strings.ReplaceAll(s, "_", ""))) }

// Only numeric constraints reachable from declared operations affect measurement.
// Examples, defaults, extensions and unused components are contract provenance,
// but their numbers are never inputs to condition validation.
func contractPrecision(documents []any, imprecise map[string]bool) bool {
	if len(imprecise) == 0 {
		return true
	}
	seen := map[string]bool{}
	var walk func(any, int, string, string) bool
	walk = func(value any, doc int, location, kind string) bool {
		key := fmt.Sprintf("%d:%s:%s", doc, location, kind)
		if seen[key] {
			return true
		}
		seen[key] = true
		object, ok := value.(map[string]any)
		if !ok {
			return true
		}
		if reference, ok := object["$ref"].(string); ok {
			parts := strings.SplitN(reference, "#", 2)
			id, err := strconv.Atoi(strings.TrimPrefix(parts[0], "doc:"))
			if err != nil || id < 0 || id >= len(documents) || len(parts) != 2 {
				return false
			}
			pointer, err := url.PathUnescape(parts[1])
			if err != nil {
				return false
			}
			resolved := documents[id]
			if pointer != "" {
				if !strings.HasPrefix(pointer, "/") {
					return false
				}
				for _, token := range strings.Split(pointer[1:], "/") {
					token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
					switch parent := resolved.(type) {
					case map[string]any:
						resolved = parent[token]
					case []any:
						index, err := strconv.Atoi(token)
						if err != nil || index < 0 || index >= len(parent) {
							return false
						}
						resolved = parent[index]
					default:
						return false
					}
				}
			}
			return walk(resolved, id, pointer, kind)
		}
		child := func(name, childKind string) bool {
			return walk(object[name], doc, location+"/"+pointerToken(name), childKind)
		}
		entries := func(name, childKind string) bool {
			prefix := location + "/" + pointerToken(name)
			switch list := object[name].(type) {
			case map[string]any:
				for name, value := range list {
					if !walk(value, doc, prefix+"/"+pointerToken(name), childKind) {
						return false
					}
				}
			case []any:
				for index, value := range list {
					if !walk(value, doc, fmt.Sprintf("%s/%d", prefix, index), childKind) {
						return false
					}
				}
			}
			return true
		}
		switch kind {
		case "document":
			return entries("paths", "path")
		case "path":
			if !entries("parameters", "parameter") {
				return false
			}
			for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
				if !child(method, "operation") {
					return false
				}
			}
		case "operation":
			return entries("parameters", "parameter") && child("requestBody", "body") && entries("responses", "response")
		case "parameter":
			return child("schema", "schema") && entries("content", "media")
		case "body":
			return entries("content", "media")
		case "response":
			return entries("headers", "parameter") && entries("content", "media")
		case "media":
			return child("schema", "schema")
		case "schema":
			for _, name := range []string{"minimum", "maximum", "multipleOf", "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties", "enum"} {
				prefix := fmt.Sprintf("%d:%s/%s", doc, location, name)
				for issue := range imprecise {
					if issue == prefix || strings.HasPrefix(issue, prefix+"/") {
						return false
					}
				}
			}
			return entries("properties", "schema") && child("items", "schema") && child("additionalProperties", "schema") && child("not", "schema") && entries("oneOf", "schema") && entries("anyOf", "schema") && entries("allOf", "schema")
		}
		return true
	}
	return len(documents) > 0 && walk(documents[0], 0, "", "document")
}

// ContractIdentities verifies the frozen contracts of a loaded archive without
// building coverage targets or measuring captured traffic.
func ContractIdentities(ctx context.Context, run *bundle.Run, captures []*artifact.Artifact) ([]string, error) {
	refs := append([]artifact.SchemaRef{}, run.Schemas...)
	for _, capture := range captures {
		refs = append(refs, capture.Schemas...)
	}
	revisions := map[string]string{}
	seen := map[string]bool{}
	identities := []string{}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, origin, kind, ok := contractBoundary(run, ref)
		if !ok {
			continue
		}
		revision := schemaRevision(ref)
		if ref.Source.Type != "omitted" && ref.Type == "openapi" {
			key := schemaKey(ref)
			var found bool
			revision, found = revisions[key]
			if !found {
				root, files, err := bundle.OpenAPIFilesWithLimits(ctx, ref, bundle.LoadLimits{})
				if err != nil {
					return nil, err
				}
				revision, _, err = contractRevisionFiles(ctx, root, files)
				if err != nil {
					return nil, err
				}
				revisions[key] = revision
			}
		}
		identity := strings.Join([]string{id, ref.ID, origin, kind, ContractRulesVersion, revision}, "\x00")
		if !seen[identity] {
			identities = append(identities, identity)
			seen[identity] = true
		}
	}
	sort.Strings(identities)
	return identities, nil
}

func contractBoundary(run *bundle.Run, ref artifact.SchemaRef) (id, origin, kind string, ok bool) {
	if ref.Role != "sut" && ref.Role != "provided" && ref.Role != "dependency" {
		return "", "", "", false
	}
	id, origin, kind = run.APIID, "provided", run.SUTTargetKind
	if ref.Role == "provided" || id == "" {
		id = ref.AssetID
	}
	if ref.Role == "dependency" {
		id, origin, kind = ref.AssetID, "dependency", run.DependencyKinds[ref.AssetID]
	}
	if kind == "" {
		kind = "unknown"
	}
	return id, origin, kind, true
}

// A report uses one verified file graph for its operation index, condition
// measurement and canonical identity. No input is reopened just for hashing.
type contractPreparation struct {
	revision    string
	precise     bool
	identityErr error
	spec        *openapiSpec
	loadErr     error
}
type contractCache map[string]contractPreparation

func (cache contractCache) load(ref artifact.SchemaRef) contractPreparation {
	key := schemaKey(ref)
	if prepared, ok := cache[key]; ok {
		return prepared
	}
	prepared := contractPreparation{}
	root, files, err := bundle.OpenAPIFiles(ref)
	prepared.loadErr, prepared.identityErr = err, err
	if err == nil {
		prepared.revision, prepared.precise, prepared.identityErr = contractRevisionFiles(context.Background(), root, files)
		prepared.spec, prepared.loadErr = parseOpenAPISpec(root, files)
	}
	cache[key] = prepared
	return prepared
}
