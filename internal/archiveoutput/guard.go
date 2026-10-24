// Package archiveoutput protects verified capture evidence from derived exports.
package archiveoutput

import (
	"errors"
	"fmt"
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
	files         []os.FileInfo
	directories   []string
}

// New verifies the archive and inventories source identities. The inventory is
// reused for all generated files, including sources stored in exports.
func New(archive string) (*Guard, error) {
	root, err := filepath.Abs(archive)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	sources, err := bundle.EvidencePaths(root)
	if err != nil {
		return nil, err
	}
	guard := &Guard{root: root, exports: filepath.Join(root, "exports"), exportsOK: true}
	if info, err := os.Lstat(guard.exports); err == nil {
		guard.exportsOK = info.IsDir() && info.Mode()&os.ModeSymlink == 0
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	visited := map[string]bool{}
	var collect func(string) error
	collect = func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			// A verified archive cannot reference an unresolved symlink. Ignore
			// unrelated dangling links rather than rejecting valid evidence.
			if link, linkErr := os.Lstat(path); linkErr == nil && link.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			guard.files = append(guard.files, info)
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if !withinPath(root, resolved) {
			// Referenced directories cannot escape a verified archive. Do not
			// traverse unrelated symlinks into the rest of the filesystem.
			return nil
		}
		if visited[resolved] {
			return nil
		}
		visited[resolved] = true
		guard.directories = append(guard.directories, resolved)
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := collect(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	for _, source := range sources {
		if err := collect(source); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"bundle.json", "tests", "contracts", "payloads"} {
		path := filepath.Join(root, name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		if err := collect(path); err != nil {
			return nil, err
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
	if withinPath(guard.root, absolute) || withinPath(guard.root, resolved) {
		if !guard.exportsOK || !withinPath(guard.exports, resolved) {
			return "", fmt.Errorf("%w: output %q must be outside the capture archive or inside its exports directory", ErrDestination, path)
		}
	}
	for _, directory := range guard.directories {
		if withinPath(directory, resolved) {
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

func withinPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
		return true
	}
	// Match existing ancestors by identity as well, since resolving symlinks
	// need not canonicalize casing on a case-insensitive filesystem.
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false
	}
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		if info, err := os.Stat(current); err == nil && os.SameFile(rootInfo, info) {
			return true
		}
		if filepath.Dir(current) == current {
			return false
		}
	}
}
