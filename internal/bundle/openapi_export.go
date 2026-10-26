package bundle

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/mattlock83/wiretap/internal/artifact"
)

// OpenAPIFiles reads a frozen OpenAPI contract for a portable derived export.
// Callers first verify the complete capture archive with Load. The returned root
// and file keys are archive-relative paths, including contracts/<set>/, so copying
// every member preserves the frozen documents' relative references. The manifest
// itself is not exported. Every read is bounded by ConfiguredLoadLimits and every
// member is checked again against its recorded hash. No URLs are fetched.
func OpenAPIFiles(ref artifact.SchemaRef) (root string, files map[string][]byte, err error) {
	return OpenAPIFilesWithLimits(context.Background(), ref, LoadLimits{})
}

// OpenAPIFilesWithLimits uses the same integrity and containment checks with
// optional tighter per-operation budgets. Zero limits use the configured limits.
func OpenAPIFilesWithLimits(ctx context.Context, ref artifact.SchemaRef, limits LoadLimits) (root string, files map[string][]byte, err error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if ref.Type != "openapi" || ref.Source.Type != "file" || !filepath.IsAbs(ref.Source.Path) || !validSchemaHash(ref.Source.SHA256) {
		return "", nil, errors.New("OpenAPI export requires a hash-pinned frozen OpenAPI file")
	}
	configured, err := ConfiguredLoadLimits()
	if err != nil {
		return "", nil, err
	}
	if limits.MaxFileBytes == 0 || configured.MaxFileBytes < limits.MaxFileBytes {
		limits.MaxFileBytes = configured.MaxFileBytes
	}
	if limits.MaxTotalBytes == 0 || configured.MaxTotalBytes < limits.MaxTotalBytes {
		limits.MaxTotalBytes = configured.MaxTotalBytes
	}
	if limits.MaxFileReads == 0 || configured.MaxFileReads < limits.MaxFileReads {
		limits.MaxFileReads = configured.MaxFileReads
	}
	limits, err = limits.normalized()
	if err != nil {
		return "", nil, err
	}
	reader := &FileReader{limits: limits}
	setDir := filepath.Dir(ref.Source.Path)
	manifestPath, err := containedPath(setDir, "manifest.json")
	if err != nil {
		return "", nil, fmt.Errorf("read frozen OpenAPI manifest: %w", err)
	}
	manifestLimit := reader.limits.MaxFileBytes
	reader.limits.MaxFileBytes = min(manifestLimit, 1<<20)
	data, err := reader.ReadFile(manifestPath)
	reader.limits.MaxFileBytes = manifestLimit
	if err != nil {
		return "", nil, err
	}
	files = map[string][]byte{}
	root, err = verifyFrozenOpenAPI(ctx, ref, setDir, data, reader, files)
	if err != nil {
		return "", nil, err
	}
	return root, files, nil
}

// verifyFrozenOpenAPI is the common contract-manifest verifier for archive
// loading and standalone exports. Reuse the caller's reader so every member
// contributes to its existing byte/read budgets. A nil files map validates
// members without retaining another copy of their contents in memory.
func verifyFrozenOpenAPI(ctx context.Context, ref artifact.SchemaRef, setDir string, manifestData []byte, reader *FileReader, files map[string][]byte) (string, error) {
	if err := rejectDuplicateKeys(manifestData); err != nil {
		return "", err
	}
	var manifest schemaManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return "", err
	}
	if !validSchemaHash(manifest.SetHash) || manifest.SetHash != filepath.Base(setDir) || manifest.Profile != "" || manifest.Type != "" || len(manifest.Files) == 0 {
		return "", errors.New("invalid frozen OpenAPI manifest")
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
		return "", err
	}
	sourceInfo, err := os.Stat(ref.Source.Path)
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		return "", err
	}
	if !os.SameFile(sourceInfo, rootInfo) {
		return "", errors.New("frozen OpenAPI manifest root does not match selected schema")
	}
	if err := reader.requireReads(len(manifest.Files)); err != nil {
		return "", fmt.Errorf("frozen OpenAPI file inventory: %w", err)
	}
	seen := make(map[string]bool, len(manifest.Files))
	foundRoot := false
	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if seen[file.Path] {
			return "", fmt.Errorf("duplicate frozen OpenAPI manifest member %q", file.Path)
		}
		seen[file.Path] = true
		if !validSchemaHash(file.SHA256) {
			return "", fmt.Errorf("invalid frozen OpenAPI hash for %q", file.Path)
		}
		path, err := member(file.Path)
		if err != nil {
			return "", err
		}
		data, err := reader.ReadFile(path)
		if err != nil {
			return "", err
		}
		if digest(data) != file.SHA256 {
			return "", fmt.Errorf("frozen OpenAPI member %q hash mismatch", file.Path)
		}
		if file.Path == manifest.Root {
			if file.SHA256 != ref.Source.SHA256 {
				return "", errors.New("selected frozen OpenAPI root hash mismatch")
			}
			foundRoot = true
		}
		if files != nil {
			files[file.Path] = data
		}
	}
	if !foundRoot {
		return "", errors.New("frozen OpenAPI root is missing from its manifest")
	}
	return manifest.Root, nil
}

func validSchemaHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ParseOpenAPI resolves references exclusively from the verified file inventory.
// Paths are virtual archive-relative names: parsing never opens files or URLs.
// Callers retain their own dialect, validation and routing policies.
func ParseOpenAPI(ctx context.Context, root string, files map[string][]byte) (*openapi3.T, error) {
	if !validRelative(root) || strings.Contains(root, "\\") {
		return nil, errors.New("invalid frozen OpenAPI root path")
	}
	loader := openapi3.NewLoader()
	loader.Context, loader.IsExternalRefsAllowed = ctx, true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, uri *url.URL) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if uri.Scheme != "file" || uri.RawQuery != "" || uri.Opaque != "" || uri.User != nil || uri.Host != "" && uri.Host != "localhost" || !strings.HasPrefix(uri.Path, "/") {
			return nil, errors.New("OpenAPI reference leaves frozen contract")
		}
		data, found := files[strings.TrimPrefix(path.Clean(uri.Path), "/")]
		if !found {
			return nil, errors.New("OpenAPI reference is absent from frozen manifest")
		}
		return data, nil
	}
	return loader.LoadFromURI(&url.URL{Scheme: "file", Path: "/" + root})
}
