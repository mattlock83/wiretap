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
	"strings"

	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/postman"
	"github.com/mattlock83/wiretap/internal/version"
)

type Config struct {
	Coverage       []string         `json:"coverage,omitempty" yaml:"coverage,omitempty"`
	Postman        bool             `json:"postman,omitempty" yaml:"postman,omitempty"`
	PostmanOptions *postman.Options `json:"postman_options,omitempty" yaml:"postman_options,omitempty"`
}

func Defaults() Config { return Config{Coverage: []string{"json", "text"}, Postman: true} }
func (c Config) Validate() error {
	seen := map[string]bool{}
	for _, format := range c.Coverage {
		if format != "json" && format != "yaml" && format != "text" {
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
	ReportVersion    string               `json:"report_version"`
	Producer         version.Producer     `json:"producer"`
	BundlePath       string               `json:"bundle_path"`
	Coverage         map[string]string    `json:"coverage"`
	Postman          []string             `json:"postman"`
	Errors           []Diagnostic         `json:"errors"`
	PostmanFiles     []string             `json:"postman_files,omitempty"`
	PostmanOmissions []postman.Diagnostic `json:"postman_omissions,omitempty"`
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
	guard, err := archiveoutput.New(root)
	if err != nil {
		return nil, err
	}
	writeOutput := func(path string, data []byte) error {
		if _, err := guard.Destination(path); err != nil {
			return err
		}
		return writeFile(ctx, root, path, data)
	}
	result := &Result{ReportVersion: "1.0", Producer: version.Current(), BundlePath: root, Coverage: map[string]string{}, Postman: []string{}, Errors: []Diagnostic{}}
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
				extension := format
				if format == "text" {
					extension = "txt"
				}
				path := filepath.Join(root, "exports", "coverage."+extension)
				if err == nil {
					err = writeOutput(path, data.Bytes())
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
			options := postman.Options{}
			if cfg.PostmanOptions != nil {
				options = *cfg.PostmanOptions
			}
			generated, err := postman.GenerateAllWithOptions(root, "", options)
			if generated != nil {
				result.PostmanOmissions = generated.Diagnostics
			}
			if err != nil {
				fail("postman", err)
			} else {
				exports := generated.Exports
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
						companions := []string{}
						for _, companion := range export.Files {
							path := filepath.Join(root, "exports", "postman", filepath.FromSlash(companion.Path))
							if err = writeOutput(path, companion.Data); err != nil {
								break
							}
							companions = append(companions, path)
						}
						if err != nil {
							fail("postman", err)
							break
						}
						path := filepath.Join(root, "exports", "postman", export.Filename)
						if err := writeOutput(path, encoded[i]); err != nil {
							fail("postman", err)
							break
						}
						result.Postman = append(result.Postman, path)
						result.PostmanFiles = append(result.PostmanFiles, companions...)
					}
				}
			}
		}
	}
	// A small receipt names this invocation's outputs and any failures. Consumers
	// use these exact paths instead of guessing the newest run or scanning a dir.
	data, err := json.MarshalIndent(result, "", "  ")
	if err == nil {
		err = writeOutput(filepath.Join(root, "exports", "result.json"), append(data, '\n'))
	}
	if err != nil {
		fail("receipt", err)
	}
	return result, errors.Join(failures...)
}

func writeFile(ctx context.Context, root, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	base := filepath.Join(root, "exports")
	relative, err := filepath.Rel(base, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("export path must be inside archive exports directory")
	}
	// Check every ancestor before creation, including nested proto companions.
	for current := base; ; {
		if info, err := os.Lstat(current); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("export destination %q is not a regular directory", current)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if current == dir {
			break
		}
		remainder, err := filepath.Rel(current, dir)
		if err != nil {
			return err
		}
		part := strings.Split(remainder, string(filepath.Separator))[0]
		current = filepath.Join(current, part)
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
