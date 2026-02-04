package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteFile writes the artifact to a YAML file in the output directory.
func WriteFile(outputDir string, art *Artifact) (string, error) {
	if outputDir == "" {
		return "", fmt.Errorf("output directory is required")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}

	name := art.TestCase.ID
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf("testcase-%d", time.Now().UTC().UnixNano())
	}
	name = sanitizeFilename(name)
	path := filepath.Join(outputDir, name+".yaml")

	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create artifact file: %w", err)
	}
	defer file.Close()

	if err := Encode(file, art); err != nil {
		return "", fmt.Errorf("encode artifact: %w", err)
	}
	return path, nil
}

func sanitizeFilename(raw string) string {
	if raw == "" {
		return "artifact"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
