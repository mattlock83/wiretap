package coverage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
	"github.com/phrond/evermock/internal/schemajson"
)

// ReplayContractRevision shares coverage's canonical identity and precision
// boundary with generated tests, so the two cannot silently disagree.
func ReplayContractRevision(ref artifact.SchemaRef) (string, error) {
	revision, precise, err := contractRevision(ref)
	if err != nil {
		return "", err
	}
	if !precise {
		return "", fmt.Errorf("contract numbers exceed the validator's reliable precision")
	}
	return revision, nil
}

// Canonicalise the frozen document graph with traversal-assigned document names.
// Frozen storage hashes include original source URIs, which are provenance, not
// contract identity. Sorted traversal preserves refs and cycles without copying
// recursive schemas or depending on repository paths and generated filenames.
func contractRevision(ref artifact.SchemaRef) (string, bool, error) {
	type document struct {
		uri   *url.URL
		value any
	}
	var root *url.URL
	var inline []byte
	if ref.Source.Type == "inline" {
		root = &url.URL{Scheme: "wiretap-inline", Host: "root"}
		inline = []byte(ref.Source.Content)
	} else if ref.Source.Type == "file" {
		absolute, err := filepath.Abs(ref.Source.Path)
		if err != nil {
			return "", false, err
		}
		root = &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	} else {
		return "", false, fmt.Errorf("Contract identity requires a frozen file or inline schema")
	}
	ids := map[string]int{root.String(): 0}
	docs := []document{{uri: root}}
	imprecise := map[string]bool{}
	for i := 0; i < len(docs); i++ {
		var raw []byte
		if i == 0 && inline != nil {
			raw = inline
		} else {
			if docs[i].uri.Scheme != "file" {
				return "", false, fmt.Errorf("Unfrozen external schema reference")
			}
			var err error
			raw, err = os.ReadFile(filepath.FromSlash(docs[i].uri.Path))
			if err != nil {
				return "", false, err
			}
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
