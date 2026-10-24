package bundle

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
)

// ErrLoadLimit identifies an archive that exceeds the configured loading budget.
// Callers can use errors.Is to distinguish it from an integrity or format error.
var ErrLoadLimit = errors.New("archive loading limit exceeded")

// LoadLimits bounds filesystem reads during one LoadWithLimits call. Repeated
// reads of the same file count again: repeated attachments are also restored into
// separate capture bodies. These limits bound input, not the exact size of the
// decoded Go objects, which can be larger than the archive bytes.
type LoadLimits struct {
	MaxFileBytes  int64 // Maximum bytes in any individual file read.
	MaxTotalBytes int64 // Maximum cumulative bytes read, including repeated reads.
	MaxFileReads  int   // Maximum number of file reads, including repeated reads.
}

// DefaultLoadLimits returns the finite budgets used by Load. Set larger explicit
// limits with LoadWithLimits for a trusted archive that exceeds these defaults.
func DefaultLoadLimits() LoadLimits {
	return LoadLimits{MaxFileBytes: 64 << 20, MaxTotalBytes: 512 << 20, MaxFileReads: 100_000}
}

// Load uses explicit positive environment overrides so the binary's report,
// export and finalization paths can handle larger trusted archives. Explicit
// LoadWithLimits calls remain independent of process environment.
func loadLimitsFromEnv() (LoadLimits, error) {
	limits := DefaultLoadLimits()
	for _, setting := range []struct {
		name        string
		destination *int64
	}{
		{"WIRETAP_MAX_ARCHIVE_FILE_BYTES", &limits.MaxFileBytes},
		{"WIRETAP_MAX_ARCHIVE_TOTAL_BYTES", &limits.MaxTotalBytes},
	} {
		if raw, present := os.LookupEnv(setting.name); present {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value <= 0 || value == math.MaxInt64 {
				return LoadLimits{}, fmt.Errorf("%s must be a positive integer less than MaxInt64", setting.name)
			}
			*setting.destination = value
		}
	}
	if raw, present := os.LookupEnv("WIRETAP_MAX_ARCHIVE_FILES"); present {
		value, err := strconv.ParseInt(raw, 10, strconv.IntSize)
		if err != nil || value <= 0 {
			return LoadLimits{}, errors.New("WIRETAP_MAX_ARCHIVE_FILES must be a positive integer within the platform int range")
		}
		limits.MaxFileReads = int(value)
	}
	return limits, nil
}

func (limits LoadLimits) normalized() (LoadLimits, error) {
	if limits.MaxFileBytes < 0 || limits.MaxTotalBytes < 0 || limits.MaxFileReads < 0 {
		return LoadLimits{}, errors.New("archive loading limits must not be negative")
	}
	defaults := DefaultLoadLimits()
	if limits.MaxFileBytes == 0 {
		limits.MaxFileBytes = defaults.MaxFileBytes
	}
	if limits.MaxTotalBytes == 0 {
		limits.MaxTotalBytes = defaults.MaxTotalBytes
	}
	if limits.MaxFileReads == 0 {
		limits.MaxFileReads = defaults.MaxFileReads
	}
	// Bounded reads reserve one extra byte to detect a file growing after Stat.
	if limits.MaxFileBytes == math.MaxInt64 || limits.MaxTotalBytes == math.MaxInt64 {
		return LoadLimits{}, errors.New("archive byte limits must be less than MaxInt64")
	}
	return limits, nil
}

type archiveReader struct {
	limits LoadLimits
	reads  int
	bytes  int64
	paths  map[string]struct{}
}

func (reader *archiveReader) requireReads(count int) error {
	if count > reader.limits.MaxFileReads-reader.reads {
		return fmt.Errorf("%w: file reads would exceed MaxFileReads (%d)", ErrLoadLimit, reader.limits.MaxFileReads)
	}
	return nil
}

func (reader *archiveReader) readFile(path string) ([]byte, error) {
	if err := reader.requireReads(1); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Check before opening as well as on the opened descriptor. This also rejects
	// static device/FIFO references rather than trying to read an endless stream.
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if err := reader.checkSize(path, info); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if err := reader.checkSize(path, info); err != nil {
		return nil, err
	}
	remaining := reader.limits.MaxTotalBytes - reader.bytes
	limit := min(reader.limits.MaxFileBytes, remaining)
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: %s grew beyond its file or remaining total byte budget", ErrLoadLimit, path)
	}
	reader.reads++
	reader.bytes += int64(len(raw))
	if reader.paths != nil {
		reader.paths[path] = struct{}{}
	}
	return raw, nil
}

func (reader *archiveReader) checkSize(path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("archive reference %s is not a regular file", path)
	}
	if info.Size() > reader.limits.MaxFileBytes {
		return fmt.Errorf("%w: %s has %d bytes, exceeding MaxFileBytes (%d)", ErrLoadLimit, path, info.Size(), reader.limits.MaxFileBytes)
	}
	if info.Size() > reader.limits.MaxTotalBytes-reader.bytes {
		return fmt.Errorf("%w: %s exceeds remaining MaxTotalBytes (%d)", ErrLoadLimit, path, reader.limits.MaxTotalBytes)
	}
	return nil
}
