// Package runoutput creates disposable exports from a verified capture archive.
package runoutput

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/postman"
)

type Config struct {
	Coverage []string `json:"coverage,omitempty" yaml:"coverage,omitempty"`
	Postman  bool     `json:"postman,omitempty" yaml:"postman,omitempty"`
}

func Defaults() Config { return Config{Coverage: []string{"json"}, Postman: true} }
func (c Config) Validate() error {
	seen := map[string]bool{}
	for _, format := range c.Coverage {
		if format != "json" && format != "yaml" {
			return fmt.Errorf("outputs.coverage: unsupported format %q", format)
		}
		if seen[format] {
			return fmt.Errorf("outputs.coverage: duplicate format %q", format)
		}
		seen[format] = true
	}
	return nil
}

type Diagnostic struct {
	Format  string `json:"format"`
	Message string `json:"message"`
}
type Result struct {
	BundlePath string            `json:"bundle_path"`
	Coverage   map[string]string `json:"coverage"`
	Postman    []string          `json:"postman"`
	Errors     []Diagnostic      `json:"errors"`
}

// Generate keeps successful outputs available when another exporter fails. It
// never changes the recording or silently turns missing exports into success.
func Generate(ctx context.Context, archive string, cfg Config) (*Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(archive)
	if err != nil {
		return nil, err
	}
	if _, _, err := bundle.Load(root); err != nil {
		return nil, err
	}
	result := &Result{BundlePath: root, Coverage: map[string]string{}, Postman: []string{}, Errors: []Diagnostic{}}
	var failures []error
	fail := func(format string, err error) {
		result.Errors = append(result.Errors, Diagnostic{format, err.Error()})
		failures = append(failures, fmt.Errorf("%s export: %w", format, err))
	}
	if len(cfg.Coverage) > 0 {
		report, err := coverage.Generate(ctx, root, "", false)
		if err != nil {
			fail("coverage", err)
		} else {
			for _, format := range cfg.Coverage {
				var data bytes.Buffer
				err := coverage.WriteReport(&data, report, format)
				path := filepath.Join(root, "exports", "coverage."+format)
				if err == nil {
					err = writeFile(ctx, path, data.Bytes())
				}
				if err != nil {
					fail("coverage."+format, err)
				} else {
					result.Coverage[format] = path
				}
			}
		}
	}
	if cfg.Postman {
		if err := ctx.Err(); err != nil {
			fail("postman", err)
		} else {
			exports, err := postman.GenerateAll(root, "")
			if err != nil {
				fail("postman", err)
			} else {
				// Encode every collection before replacing any file, including mixed runs.
				encoded := make([][]byte, len(exports))
				for i, export := range exports {
					var data bytes.Buffer
					if err = postman.WriteExport(&data, export); err != nil {
						break
					}
					encoded[i] = data.Bytes()
				}
				if err != nil {
					fail("postman", err)
				} else {
					for i, export := range exports {
						path := filepath.Join(root, "exports", "postman", export.Filename)
						if err := writeFile(ctx, path, encoded[i]); err != nil {
							fail("postman", err)
							break
						}
						result.Postman = append(result.Postman, path)
					}
				}
			}
		}
	}
	// A small receipt names this invocation's outputs and any failures. Consumers
	// use these exact paths instead of guessing the newest run or scanning a dir.
	data, err := json.MarshalIndent(result, "", "  ")
	if err == nil {
		err = writeFile(ctx, filepath.Join(root, "exports", "result.json"), append(data, '\n'))
	}
	if err != nil {
		fail("receipt", err)
	}
	return result, errors.Join(failures...)
}

func writeFile(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	// These directories are fixed descendants of an already verified archive.
	dirs := []string{dir}
	if filepath.Base(dir) == "postman" {
		dirs = []string{filepath.Dir(dir), dir}
	}
	for _, current := range dirs {
		if info, err := os.Lstat(current); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("export destination %q is not a regular directory", current)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("export destination %q is not a regular file", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(dir, ".wiretap-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
