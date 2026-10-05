package bundle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/grpccontract"
	"github.com/phrond/wiretap/internal/schemajson"
)

type schemaFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type schemaManifest struct {
	Profile string       `json:"profile,omitempty"`
	Type    string       `json:"type,omitempty"`
	SetHash string       `json:"set_hash"`
	Root    string       `json:"root"`
	Files   []schemaFile `json:"files"`
}

func validateSchemaBoundary(ref artifact.SchemaRef) error {
	if ref.Role != "sut" && ref.Role != "provided" && ref.Role != "dependency" {
		return fmt.Errorf("contract %q has unsupported boundary role %q", ref.ID, ref.Role)
	}
	return nil
}

// freezeSchema loads transitive file/URL references and rewrites them to
// deterministic local names. The snapshot is used for live validation too.
func (w *Writer) freezeSchema(ref artifact.SchemaRef) (artifact.SchemaRef, error) {
	if err := validateSchemaBoundary(ref); err != nil {
		return ref, err
	}
	switch ref.Type {
	case "protobuf":
		return w.freezeProtobuf(ref)
	case "avro", "asyncapi", "graphql":
		return w.freezeContractPackage(ref)
	case "openapi":
	default:
		return ref, fmt.Errorf("unsupported contract type %q", ref.Type)
	}
	var rootURL *url.URL
	switch strings.ToLower(ref.Source.Type) {
	case "omitted":
		if ref.Source.Path != "" || ref.Source.URL != "" || ref.Source.Content != "" || ref.Source.SHA256 != "" || len(ref.Source.Files) != 0 {
			return ref, errors.New("omitted schema cannot contain a source")
		}
		return ref, nil
	case "file":
		path, err := filepath.Abs(ref.Source.Path)
		if err != nil {
			return ref, err
		}
		rootURL = &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	case "url":
		var err error
		rootURL, err = url.Parse(ref.Source.URL)
		if err != nil {
			return ref, err
		}
		if rootURL.User != nil {
			return ref, errors.New("schema URL credentials are not supported")
		}
	case "inline":
		rootURL = &url.URL{Scheme: "wiretap-inline", Host: digest([]byte(ref.Source.Content))}
	default:
		return ref, fmt.Errorf("unsupported schema source %q", ref.Source.Type)
	}
	type sourceDoc struct {
		uri   *url.URL
		value any
	}
	docs := map[string]sourceDoc{}
	var collect func(*url.URL, []byte) error
	collect = func(uri *url.URL, inline []byte) error {
		key := uri.String()
		if _, found := docs[key]; found {
			return nil
		}
		if len(docs) >= 128 {
			return errors.New("schema has too many referenced documents")
		}
		data := inline
		if data == nil {
			var err error
			data, err = fetchSchema(uri)
			if err != nil {
				return err
			}
		}
		value, err := schemajson.Decode(data)
		if err != nil {
			return fmt.Errorf("decode schema %s: %w", uri, err)
		}
		docs[key] = sourceDoc{uri: uri, value: value}
		var visit func(any) error
		visit = func(node any) error {
			switch v := node.(type) {
			case map[string]any:
				if raw, ok := v["$ref"].(string); ok && raw != "" {
					parsed, err := url.Parse(raw)
					if err != nil {
						return err
					}
					resolved := uri.ResolveReference(parsed)
					resolved.Fragment = ""
					if resolved.User != nil {
						return errors.New("schema reference URL credentials are not supported")
					}
					if resolved.String() != key {
						if err := collect(resolved, nil); err != nil {
							return err
						}
					}
				}
				for _, item := range v {
					if err := visit(item); err != nil {
						return err
					}
				}
			case []any:
				for _, item := range v {
					if err := visit(item); err != nil {
						return err
					}
				}
			}
			return nil
		}
		return visit(value)
	}
	var inline []byte
	if strings.EqualFold(ref.Source.Type, "inline") {
		inline = []byte(ref.Source.Content)
	}
	if err := collect(rootURL, inline); err != nil {
		return ref, err
	}
	keys := make([]string, 0, len(docs))
	for key := range docs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// The set hash changes with any referenced document's content.
	identity := ""
	for _, key := range keys {
		data, err := json.Marshal(docs[key].value)
		if err != nil {
			return ref, err
		}
		identity += digest([]byte(key)) + ":" + digest(data) + "/"
	}
	setHash := digest([]byte(identity))
	base := "contracts/" + setHash
	filename := func(key string) string { return "doc-" + digest([]byte(key))[:20] + ".json" }
	manifest := schemaManifest{SetHash: setHash}
	for _, key := range keys {
		doc := docs[key]
		var rewrite func(any)
		rewrite = func(node any) {
			switch v := node.(type) {
			case map[string]any:
				if raw, ok := v["$ref"].(string); ok {
					parsed, err := url.Parse(raw)
					if err == nil {
						resolved := doc.uri.ResolveReference(parsed)
						fragment := resolved.EscapedFragment()
						resolved.Fragment = ""
						if resolved.String() != key {
							v["$ref"] = filename(resolved.String())
							if fragment != "" {
								v["$ref"] = v["$ref"].(string) + "#" + fragment
							}
						}
					}
				}
				for _, item := range v {
					rewrite(item)
				}
			case []any:
				for _, item := range v {
					rewrite(item)
				}
			}
		}
		rewrite(doc.value)
		data, err := json.MarshalIndent(doc.value, "", "  ")
		if err != nil {
			return ref, err
		}
		data = append(data, '\n')
		rel := base + "/" + filename(key)
		if err := writeContent(w.root, rel, data); err != nil {
			return ref, err
		}
		manifest.Files = append(manifest.Files, schemaFile{Path: rel, SHA256: digest(data)})
		if key == rootURL.String() {
			manifest.Root = rel
			ref.Source = artifact.SchemaSource{Type: "file", Path: filepath.Join(w.root, filepath.FromSlash(rel)), SHA256: digest(data), Format: "json"}
			ref.AssetSHA = digest(data)
		}
	}
	if ref.Source.Path == "" {
		return ref, errors.New("root schema snapshot was not created")
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ref, err
	}
	if err := writeContent(w.root, base+"/manifest.json", append(data, '\n')); err != nil {
		return ref, err
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loaded, err := loader.LoadFromFile(ref.Source.Path)
	if err != nil {
		return ref, fmt.Errorf("load frozen schema: %w", err)
	}
	if err := loaded.Validate(context.Background()); err != nil {
		return ref, fmt.Errorf("validate frozen schema: %w", err)
	}
	return ref, nil
}

func (w *Writer) freezeProtobuf(ref artifact.SchemaRef) (artifact.SchemaRef, error) {
	if ref.Source.Type != "protobuf_inline" {
		return ref, fmt.Errorf("protobuf schema requires a resolved descriptor set")
	}
	raw, err := base64.StdEncoding.DecodeString(ref.Source.Content)
	if err != nil {
		return ref, err
	}
	contract, err := grpccontract.Parse(raw, ref.Services)
	if err != nil {
		return ref, fmt.Errorf("validate descriptor set: %w", err)
	}
	if ref.Source.SHA256 != "" && ref.Source.SHA256 != contract.SHA256 {
		return ref, errors.New("descriptor set hash mismatch")
	}
	base := "contracts/" + contract.SHA256
	rel := base + "/descriptor-set.protoset"
	if err := writeContent(w.root, rel, raw); err != nil {
		return ref, err
	}
	manifest := schemaManifest{SetHash: contract.SHA256, Root: rel, Files: []schemaFile{{Path: rel, SHA256: contract.SHA256}}}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ref, err
	}
	if err := writeContent(w.root, base+"/manifest.json", append(data, '\n')); err != nil {
		return ref, err
	}
	ref.Source = artifact.SchemaSource{Type: "file", Path: filepath.Join(w.root, filepath.FromSlash(rel)), SHA256: contract.SHA256, Format: "file_descriptor_set"}
	ref.AssetSHA = contract.SHA256
	return ref, nil
}

func fetchSchema(uri *url.URL) ([]byte, error) {
	switch uri.Scheme {
	case "file":
		return os.ReadFile(filepath.FromSlash(uri.Path))
	case "http", "https":
		client := http.Client{Timeout: 15 * time.Second}
		res, err := client.Get(uri.String())
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("schema URL returned HTTP %d", res.StatusCode)
		}
		return io.ReadAll(io.LimitReader(res.Body, 16<<20))
	default:
		return nil, fmt.Errorf("unsupported schema URI scheme %q", uri.Scheme)
	}
}
