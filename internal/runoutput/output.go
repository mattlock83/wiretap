// Package runoutput creates disposable exports from a verified capture archive.
package runoutput

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/version"
)

type Diagnostic struct {
	Format  string `json:"format"`
	Message string `json:"message"`
}
type Result struct {
	ReportVersion string            `json:"report_version"`
	Producer      version.Producer  `json:"producer"`
	BundlePath    string            `json:"bundle_path"`
	Coverage      map[string]string `json:"coverage"`
	Errors        []Diagnostic      `json:"errors"`
	Arazzo        string            `json:"arazzo,omitempty"`
	ArazzoFiles   []string          `json:"arazzo_files,omitempty"`
}

// Generate keeps successful outputs available when another exporter fails. It
// never changes the recording or silently turns missing exports into success.
func Generate(ctx context.Context, archive string) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := Load(ctx, archive, "", false)
	if err != nil {
		return nil, err
	}
	root := snapshot.root
	guard, err := archiveoutput.New(root)
	if err != nil {
		return nil, err
	}
	writer := archiveoutput.Writer{Guard: guard, Directory: filepath.Join(root, "exports"), CreateDir: true, Private: true}
	writeOutput := func(name string, data []byte) error {
		return writer.Write(ctx, []archiveoutput.File{{Name: name, Data: data}})
	}
	result := &Result{ReportVersion: "1.0", Producer: version.Current(), BundlePath: root, Coverage: map[string]string{}, Errors: []Diagnostic{}}
	var failures []error
	fail := func(format string, err error) {
		result.Errors = append(result.Errors, Diagnostic{format, err.Error()})
		failures = append(failures, fmt.Errorf("%s export: %w", format, err))
	}
	for _, format := range []string{"json", "html"} {
		data, err := snapshot.Coverage(ctx, format)
		if snapshot.reportErr != nil {
			fail("coverage", err)
			break
		}
		name := "coverage." + format
		if err == nil {
			err = writeOutput(name, data)
		}
		if err != nil {
			fail("coverage."+format, err)
		} else {
			result.Coverage[format] = filepath.Join(writer.Directory, name)
		}
	}
	err = ctx.Err()
	if err == nil {
		var generated *arazzo.Export
		generated, err = snapshot.Arazzo()
		if err == nil && generated != nil && generated.Document != nil {
			var files []archiveoutput.File
			files, err = ArazzoFiles(generated, "arazzo.json")
			if err == nil {
				for i := range files {
					files[i].Name = filepath.Join("arazzo", filepath.FromSlash(files[i].Name))
				}
				err = writer.Write(ctx, files)
			}
			if err == nil {
				result.Arazzo = filepath.Join(writer.Directory, files[len(files)-1].Name)
				for _, file := range files[:len(files)-1] {
					result.ArazzoFiles = append(result.ArazzoFiles, filepath.Join(writer.Directory, file.Name))
				}
			}
		}
	}
	if err != nil {
		fail("arazzo", err)
	}
	// A small receipt names this invocation's outputs and any failures. Consumers
	// use these exact paths instead of guessing the newest run or scanning a dir.
	data, err := json.MarshalIndent(result, "", "  ")
	if err == nil {
		err = writeOutput("result.json", append(data, '\n'))
	}
	if err != nil {
		fail("receipt", err)
	}
	return result, errors.Join(failures...)
}
