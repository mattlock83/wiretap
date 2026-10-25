package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveSourceCommitRequiresCurrentHEAD(t *testing.T) {
	older, head := releaseSourceRepository(t)
	for _, tt := range []struct {
		name   string
		commit string
		want   string
		err    string
	}{
		{name: "default HEAD", want: head},
		{name: "full HEAD", commit: head, want: head},
		{name: "short HEAD", commit: head[:7], want: head},
		{name: "uppercase HEAD", commit: strings.ToUpper(head), want: head},
		{name: "older commit", commit: older, err: "does not match current HEAD"},
		{name: "nonexistent commit", commit: strings.Repeat("0", 40), err: "resolve --commit"},
		{name: "invalid hash", commit: "HEAD", err: "must be a Git commit hash"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSourceCommit(tt.commit)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("resolveSourceCommit(%q) = %q, %v; want error containing %q", tt.commit, got, err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("resolveSourceCommit(%q) = %q, %v; want %q", tt.commit, got, err, tt.want)
			}
		})
	}
}

func TestReleaseRejectsInvalidSourceCommitBeforeBuilding(t *testing.T) {
	older, _ := releaseSourceRepository(t)
	for _, epoch := range []string{"", "1700000000"} {
		for _, tt := range []struct {
			name   string
			commit string
			err    string
		}{
			{name: "older", commit: older, err: "does not match current HEAD"},
			{name: "nonexistent", commit: strings.Repeat("0", 40), err: "resolve --commit"},
		} {
			t.Run(tt.name+"/SOURCE_DATE_EPOCH="+epoch, func(t *testing.T) {
				t.Setenv("SOURCE_DATE_EPOCH", epoch)
				previousFlags, previousArgs := flag.CommandLine, os.Args
				t.Cleanup(func() { flag.CommandLine, os.Args = previousFlags, previousArgs })
				flag.CommandLine = flag.NewFlagSet("release", flag.ContinueOnError)
				out := filepath.Join(t.TempDir(), "output")
				os.Args = []string{"release", "--version", "v0.1.0-test", "--commit", tt.commit, "--out", out, "--platforms", "host"}
				if err := run(); err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("run() error = %v; want error containing %q", err, tt.err)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatalf("rejected release created output: %v", err)
				}
			})
		}
	}
}

func releaseSourceRepository(t *testing.T) (older, head string) {
	t.Helper()
	root := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = root
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, data)
		}
		return strings.TrimSpace(string(data))
	}
	runGit("init", "--quiet")
	for _, message := range []string{"first", "second"} {
		runGit("-c", "user.name=Release Test", "-c", "user.email=release@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", message)
		if message == "first" {
			older = runGit("rev-parse", "HEAD")
		}
	}
	head = runGit("rev-parse", "HEAD")
	t.Chdir(root)
	return older, head
}

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
