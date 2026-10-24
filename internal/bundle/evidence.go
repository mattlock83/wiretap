package bundle

import "sort"

// EvidencePaths verifies an archive using the same limits as Load and returns
// every file it read, including attachments outside the usual payloads directory.
// Paths are absolute, resolve symlinks, and exclude disposable unreferenced exports.
func EvidencePaths(root string) ([]string, error) {
	limits, err := loadLimitsFromEnv()
	if err != nil {
		return nil, err
	}
	reader := &archiveReader{limits: limits, paths: map[string]struct{}{}}
	if _, _, err := loadWithReader(root, reader); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(reader.paths))
	for path := range reader.paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}
