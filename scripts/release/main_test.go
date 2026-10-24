package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveBytesAndEntryMetadataAreReproducible(t *testing.T) {
	for _, extension := range []string{".tar.gz", ".zip"} {
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			stamp := time.Unix(1700000000, 0).UTC()
			var previous []byte
			for _, prefix := range []string{"first", "different-build-directory"} {
				path := filepath.Join(root, prefix+extension)
				if err := writeArchive(path, "wiretap", []byte("binary"), []byte("license"), stamp); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if previous != nil && !bytes.Equal(previous, data) {
					t.Fatal("archive content depends on the output filename or wall clock")
				}
				previous = data
				if extension == ".zip" {
					reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
					if err != nil || len(reader.File) != 2 {
						t.Fatalf("invalid ZIP: %v", err)
					}
					if reader.File[0].Name != "LICENSE" || reader.File[1].Name != "wiretap" || reader.File[1].Mode().Perm() != 0755 || !reader.File[1].Modified.Equal(stamp) {
						t.Fatal("ZIP metadata differs from the fixed release metadata")
					}
				} else {
					compressed, err := gzip.NewReader(bytes.NewReader(data))
					if err != nil {
						t.Fatal(err)
					}
					reader := tar.NewReader(compressed)
					var names []string
					for {
						header, err := reader.Next()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						names = append(names, header.Name)
						if header.Uid != 0 || header.Gid != 0 || !header.ModTime.Equal(stamp) {
							t.Fatal("tar contains environment-dependent owner or timestamp")
						}
					}
					_ = compressed.Close()
					if len(names) != 2 || names[0] != "LICENSE" || names[1] != "wiretap" {
						t.Fatalf("unexpected archive entries: %v", names)
					}
				}
			}
		})
	}
}
