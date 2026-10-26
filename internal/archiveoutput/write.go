package archiveoutput

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// File is a generated document relative to its output directory.
type File struct {
	Name string
	Data []byte
}

// Writer stages a complete output group before replacing existing files. Guard
// protects archive inputs; callers without an archive protect explicit inputs
// before writing. Private outputs use owner-only permissions, while public
// reports respect the umask and preserve existing file permissions.
type Writer struct {
	Guard     *Guard
	Directory string
	CreateDir bool
	Private   bool
}

func (w Writer) Write(ctx context.Context, files []File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	base, err := filepath.Abs(w.Directory)
	if err != nil {
		return err
	}
	type destination struct {
		path, staged string
		mode         os.FileMode
		exists       bool
	}
	targets := make([]destination, len(files))
	seen := map[string]bool{}
	for i, file := range files {
		name := filepath.FromSlash(file.Name)
		if name == "." || !filepath.IsLocal(name) {
			return fmt.Errorf("%w: invalid output filename %q", ErrDestination, file.Name)
		}
		path := filepath.Join(base, name)
		if err := outputParents(base, filepath.Dir(path)); err != nil {
			return err
		}
		if w.Guard != nil {
			path, err = w.Guard.Destination(path)
			if err != nil {
				return err
			}
		}
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			return fmt.Errorf("%w: output filename collision: %s", ErrDestination, path)
		}
		seen[key] = true
		targets[i].path = path
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("output %q is not a regular file", path)
			}
			targets[i].mode, targets[i].exists = info.Mode().Perm(), true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	fileMode, dirMode := os.FileMode(0644), os.FileMode(0755)
	if w.Private {
		fileMode, dirMode = 0600, 0700
	}
	if w.CreateDir {
		if err := os.MkdirAll(base, dirMode); err != nil {
			return err
		}
	}
	staging, err := os.MkdirTemp(base, ".wiretap-output-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	for i, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := &targets[i]
		target.staged = filepath.Join(staging, filepath.FromSlash(file.Name))
		if err := os.MkdirAll(filepath.Dir(target.staged), dirMode); err != nil {
			return err
		}
		out, err := os.OpenFile(target.staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if err != nil {
			return err
		}
		if target.exists && !w.Private {
			if err := out.Chmod(target.mode); err != nil {
				_ = out.Close()
				return err
			}
		}
		_, writeErr := out.Write(file.Data)
		if err := errors.Join(writeErr, out.Close()); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if err := os.MkdirAll(filepath.Dir(target.path), dirMode); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, target := range targets {
		if err := os.Rename(target.staged, target.path); err != nil {
			return err
		}
	}
	return nil
}

func outputParents(base, dir string) error {
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("output destination %q is not a regular directory", current)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if current == base {
			return nil
		}
	}
}
