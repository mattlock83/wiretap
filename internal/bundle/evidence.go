package bundle

import (
	"errors"
	"fmt"

	"github.com/mattlock83/wiretap/internal/artifact"
)

// Snapshot holds one verified archive and the files read to restore it. Consumers
// treat its data as immutable and reuse it when calculating and writing exports.
type Snapshot struct {
	Root           string
	Run            *Run
	Captures       []*artifact.Artifact
	EvidencePaths  []string
	manifestSHA256 string
}

func LoadSnapshot(root string) (*Snapshot, error) {
	reader, err := NewFileReader()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInput, err)
	}
	snapshot, err := loadWithReader(root, reader)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInput, err)
	}
	return snapshot, nil
}

// CheckUnchanged prevents output protection from using an obsolete evidence
// inventory after another process changes the run's declarations.
func (s *Snapshot) CheckUnchanged() error {
	reader, err := NewFileReader()
	if err != nil {
		return err
	}
	path, err := containedPath(s.Root, "bundle.json")
	if err != nil {
		return err
	}
	data, err := reader.ReadFile(path)
	if err != nil {
		return err
	}
	if digest(data) != s.manifestSHA256 {
		return errors.New("archive changed after loading")
	}
	return nil
}
