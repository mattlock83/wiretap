package runoutput

import (
	"bytes"
	"context"

	"github.com/mattlock83/wiretap/internal/arazzo"
	"github.com/mattlock83/wiretap/internal/archiveoutput"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/coverage"
	"github.com/mattlock83/wiretap/internal/htmlreport"
)

// Snapshot supplies the same verified capture to individual CLI exports and
// automatic output groups. Each derived result is computed at most once.
type Snapshot struct {
	Archive          *bundle.Snapshot
	sutName          string
	includeArtifacts bool
	report           *coverage.Report
	reportErr        error
	measured         bool
	export           *arazzo.Export
	exportErr        error
	projected        bool
}

func Load(ctx context.Context, archive, sutName string, includeArtifacts bool) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := bundle.LoadSnapshot(archive)
	if err != nil {
		return nil, err
	}
	return &Snapshot{Archive: snapshot, sutName: sutName, includeArtifacts: includeArtifacts}, nil
}

func (s *Snapshot) Arazzo() (*arazzo.Export, error) {
	if !s.projected {
		s.export, s.exportErr = arazzo.GenerateSnapshot(s.Archive.Run, s.Archive.Captures, s.sutName)
		s.projected = true
	}
	return s.export, s.exportErr
}

func (s *Snapshot) Coverage(ctx context.Context, format string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.measured {
		s.report, s.reportErr = coverage.GenerateBundleSnapshot(s.Archive.Root, s.Archive.Run, s.Archive.Captures, s.sutName, s.includeArtifacts)
		s.measured = true
	}
	if s.reportErr != nil {
		return nil, s.reportErr
	}
	var data bytes.Buffer
	var err error
	if format == "html" {
		export, exportErr := s.Arazzo()
		var inputs htmlreport.Inputs
		inputs, err = htmlreport.FromSnapshot(ctx, s.Archive.Run, s.report, export, exportErr)
		if err == nil {
			err = htmlreport.Write(&data, inputs)
		}
	} else {
		err = coverage.WriteReport(&data, s.report, format)
	}
	return data.Bytes(), err
}

// ArazzoFiles encodes a workflow and its companions as one staged output group.
func ArazzoFiles(export *arazzo.Export, name string) ([]archiveoutput.File, error) {
	var data bytes.Buffer
	if err := arazzo.Write(&data, export.Document); err != nil {
		return nil, err
	}
	files := make([]archiveoutput.File, 0, len(export.Files)+1)
	for _, file := range export.Files {
		files = append(files, archiveoutput.File{Name: file.Path, Data: file.Data})
	}
	// Publish the workflow only after its referenced sources are in place.
	files = append(files, archiveoutput.File{Name: name, Data: data.Bytes()})
	return files, nil
}
