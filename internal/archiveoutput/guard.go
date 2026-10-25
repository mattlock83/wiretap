// Package archiveoutput protects verified capture evidence from derived exports.
package archiveoutput

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattlock83/wiretap/internal/bundle"
)

// ErrDestination identifies an output that would replace or alias raw evidence.
var ErrDestination = errors.New("unsafe archive output destination")

// Guard protects the raw archive while allowing its disposable
// exports to be regenerated. Cache file identities so aliases and hard links to
// the evidence cannot turn an otherwise external destination into an input.
type Guard struct {
	root, exports string
	exportsOK     bool
	rootInfo      os.FileInfo
	exportsInfo   os.FileInfo
	files         []os.FileInfo
	directories   []directory
}

type directory struct {
	path string
	info os.FileInfo
}

// New verifies the archive and inventories source identities. The inventory is
// reused for all generated files, including sources stored in exports. Its raw
// directory scan has a separate entry budget equal to WIRETAP_MAX_ARCHIVE_FILES;
// files, directories and symlinks each consume one entry, whether referenced or
// not. The four fixed raw roots do not consume that budget.
func New(archive string) (*Guard, error) {
	root, err := filepath.Abs(archive)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	limits, err := bundle.ConfiguredLoadLimits()
	if err != nil {
		return nil, err
	}
	sources, err := bundle.EvidencePaths(root)
	if err != nil {
		return nil, err
	}
	guard := &Guard{root: root, exports: filepath.Join(root, "exports"), exportsOK: true}
	guard.rootInfo, err = os.Stat(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(guard.exports); err == nil {
		guard.exportsOK = info.IsDir() && info.Mode()&os.ModeSymlink == 0
		if guard.exportsOK {
			guard.exportsInfo = info
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	files := map[string]bool{}
	addFile := func(path string, info os.FileInfo) {
		if !files[path] {
			files[path] = true
			guard.files = append(guard.files, info)
		}
	}
	for _, source := range sources {
		info, err := os.Stat(source)
		if err != nil {
			return nil, err
		}
		addFile(source, info)
	}
	// Keep the walk iterative and read directory entries in batches. A large
	// unreferenced directory must not allocate an unbounded ReadDir result, and
	// deeply nested trees must not keep a directory descriptor open per level.
	type entry struct{ path, protectedRoot string }
	var pending []entry
	for _, name := range []string{"bundle.json", "tests", "contracts", "payloads"} {
		path := filepath.Join(root, name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		pending = append(pending, entry{path: path})
	}
	visited := map[string]bool{}
	entriesRead := 0
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		info, err := os.Stat(current.path)
		if err != nil {
			// A verified archive cannot reference an unresolved symlink. Ignore
			// unrelated dangling links rather than rejecting valid evidence.
			if link, linkErr := os.Lstat(current.path); linkErr == nil && link.Mode()&os.ModeSymlink != 0 {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			addFile(current.path, info)
			continue
		}
		resolved, err := filepath.EvalSymlinks(current.path)
		if err != nil {
			return nil, err
		}
		if !withinLexicalPath(root, resolved) &&
			!withinDirectory(root, guard.rootInfo, resolved, ancestorInfos(resolved)) {
			// Referenced directories cannot escape a verified archive. Do not
			// traverse unrelated symlinks into the rest of the filesystem.
			continue
		}
		if visited[resolved] {
			continue
		}
		visited[resolved] = true
		// Ordinary descendants are already protected by their raw root. Only
		// retain another root when a directory alias leads somewhere else, such
		// as tests/link backed by exports/source-tests.
		if current.protectedRoot == "" || !withinLexicalPath(current.protectedRoot, resolved) {
			guard.directories = append(guard.directories, directory{resolved, info})
			current.protectedRoot = resolved
		}
		dir, err := os.Open(current.path)
		if err != nil {
			return nil, err
		}
		for {
			entries, readErr := dir.ReadDir(128)
			if len(entries) > limits.MaxFileReads-entriesRead {
				_ = dir.Close()
				return nil, fmt.Errorf("%w: archive output inventory exceeds WIRETAP_MAX_ARCHIVE_FILES (%d) raw entries; raise this setting for a trusted archive", bundle.ErrLoadLimit, limits.MaxFileReads)
			}
			entriesRead += len(entries)
			for _, child := range entries {
				pending = append(pending, entry{filepath.Join(current.path, child.Name()), current.protectedRoot})
			}
			if readErr != nil {
				closeErr := dir.Close()
				if errors.Is(readErr, io.EOF) {
					readErr = nil
				}
				if err := errors.Join(readErr, closeErr); err != nil {
					return nil, err
				}
				break
			}
		}
	}
	return guard, nil
}

// Destination resolves existing parents without requiring nested output
// directories to exist yet. It never follows a destination file symlink.
func (guard *Guard) Destination(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := resolveOutputDestination(absolute)
	if err != nil {
		return "", err
	}
	// Resolve each destination ancestor identity once, then reuse it for all
	// containment checks. Source directory identities were cached by New.
	ancestors := ancestorInfos(filepath.Dir(resolved))
	insideRoot := withinLexicalPath(guard.root, absolute) || withinDirectory(guard.root, guard.rootInfo, resolved, ancestors)
	if !insideRoot && absolute != resolved {
		// An archive alias can lead through an exports symlink to an external
		// directory. Keep the original parent's identities as well, so that
		// resolving the symlink cannot bypass the exports-directory check.
		insideRoot = withinDirectory(guard.root, guard.rootInfo, absolute, ancestorInfos(filepath.Dir(absolute)))
	}
	if insideRoot {
		exportsInfo := guard.exportsInfo
		if exportsInfo == nil {
			// Exports may be created after New. Its identity is only needed for
			// casing aliases; lexical paths work before the directory exists.
			exportsInfo, _ = os.Stat(guard.exports)
		}
		if !guard.exportsOK || !withinDirectory(guard.exports, exportsInfo, resolved, ancestors) {
			return "", fmt.Errorf("%w: output %q must be outside the capture archive or inside its exports directory", ErrDestination, path)
		}
	}
	for _, directory := range guard.directories {
		if withinDirectory(directory.path, directory.info, resolved, ancestors) {
			return "", fmt.Errorf("%w: output %q must not replace capture evidence", ErrDestination, path)
		}
	}
	if info, err := os.Lstat(resolved); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("output %q is not a regular file", path)
		}
		for _, source := range guard.files {
			if os.SameFile(source, info) {
				return "", fmt.Errorf("%w: output %q must not replace capture evidence", ErrDestination, path)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return resolved, nil
}

func resolveOutputDestination(absolute string) (string, error) {
	parent := filepath.Dir(absolute)
	parts := []string{filepath.Base(absolute)}
	for {
		if _, err := os.Lstat(parent); err == nil {
			resolved, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return "", err
			}
			if !info.IsDir() {
				return "", fmt.Errorf("output parent %q is not a directory", parent)
			}
			for i := len(parts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, parts[i])
			}
			return resolved, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parts = append(parts, filepath.Base(parent))
		next := filepath.Dir(parent)
		if next == parent {
			return "", fmt.Errorf("output parent %q does not exist", parent)
		}
		parent = next
	}
}

func withinLexicalPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func withinDirectory(root string, rootInfo os.FileInfo, path string, ancestors []os.FileInfo) bool {
	if withinLexicalPath(root, path) {
		return true
	}
	// Match existing ancestors by identity as well, since resolving symlinks
	// need not canonicalize casing on a case-insensitive filesystem.
	if rootInfo == nil {
		return false
	}
	for _, info := range ancestors {
		if os.SameFile(rootInfo, info) {
			return true
		}
	}
	return false
}

func ancestorInfos(path string) []os.FileInfo {
	var ancestors []os.FileInfo
	for current := path; ; current = filepath.Dir(current) {
		if info, err := os.Stat(current); err == nil {
			ancestors = append(ancestors, info)
		}
		if filepath.Dir(current) == current {
			return ancestors
		}
	}
}
