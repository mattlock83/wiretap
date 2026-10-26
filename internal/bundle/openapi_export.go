package bundle

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattlock83/wiretap/internal/artifact"
)

// OpenAPIFiles reads a frozen OpenAPI contract for a portable derived export.
// Callers first verify the complete capture archive with Load. The returned root
// and file keys are archive-relative paths, including contracts/<set>/, so copying
// every member preserves the frozen documents' relative references. The manifest
// itself is not exported. Every read is bounded by ConfiguredLoadLimits and every
// member is checked again against its recorded hash. No URLs are fetched.
func OpenAPIFiles(ref artifact.SchemaRef) (root string, files map[string][]byte, err error) {
	if ref.Type != "openapi" || ref.Source.Type != "file" || !filepath.IsAbs(ref.Source.Path) || !openAPIExportHash(ref.Source.SHA256) {
		return "", nil, errors.New("OpenAPI export requires a hash-pinned frozen OpenAPI file")
	}
	limits, err := ConfiguredLoadLimits()
	if err != nil {
		return "", nil, err
	}
	reader := &archiveReader{limits: limits}
	setDir := filepath.Dir(ref.Source.Path)
	manifestPath, err := containedPath(setDir, "manifest.json")
	if err != nil {
		return "", nil, fmt.Errorf("read frozen OpenAPI manifest: %w", err)
	}
	data, err := reader.readFile(manifestPath)
	if err != nil {
		return "", nil, err
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return "", nil, err
	}
	var manifest schemaManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", nil, err
	}
	if !openAPIExportHash(manifest.SetHash) || manifest.Profile != "" || manifest.Type != "" || len(manifest.Files) == 0 {
		return "", nil, errors.New("invalid frozen OpenAPI manifest")
	}
	base := "contracts/" + manifest.SetHash + "/"
	member := func(path string) (string, error) {
		if !strings.HasPrefix(path, base) || !validRelative(path) || strings.Contains(path, "\\") || filepath.ToSlash(filepath.Clean(path)) != path {
			return "", fmt.Errorf("frozen OpenAPI manifest member %q is outside its contract set", path)
		}
		relative := strings.TrimPrefix(path, base)
		if !validRelative(relative) || relative == "manifest.json" {
			return "", fmt.Errorf("invalid frozen OpenAPI manifest member %q", path)
		}
		return containedPath(setDir, relative)
	}
	rootPath, err := member(manifest.Root)
	if err != nil {
		return "", nil, err
	}
	sourceInfo, err := os.Stat(ref.Source.Path)
	if err != nil {
		return "", nil, err
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		return "", nil, err
	}
	if !os.SameFile(sourceInfo, rootInfo) {
		return "", nil, errors.New("frozen OpenAPI manifest root does not match selected schema")
	}
	if err := reader.requireReads(len(manifest.Files)); err != nil {
		return "", nil, fmt.Errorf("frozen OpenAPI file inventory: %w", err)
	}
	files = make(map[string][]byte, len(manifest.Files))
	for _, file := range manifest.Files {
		if _, found := files[file.Path]; found {
			return "", nil, fmt.Errorf("duplicate frozen OpenAPI manifest member %q", file.Path)
		}
		if !openAPIExportHash(file.SHA256) {
			return "", nil, fmt.Errorf("invalid frozen OpenAPI hash for %q", file.Path)
		}
		path, err := member(file.Path)
		if err != nil {
			return "", nil, err
		}
		data, err := reader.readFile(path)
		if err != nil {
			return "", nil, err
		}
		if digest(data) != file.SHA256 {
			return "", nil, fmt.Errorf("frozen OpenAPI member %q hash mismatch", file.Path)
		}
		files[file.Path] = data
	}
	data, found := files[manifest.Root]
	if !found {
		return "", nil, errors.New("frozen OpenAPI root is missing from its manifest")
	}
	if digest(data) != ref.Source.SHA256 {
		return "", nil, errors.New("selected frozen OpenAPI root hash mismatch")
	}
	return manifest.Root, files, nil
}

func openAPIExportHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
