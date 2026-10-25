// Command release builds local, reproducible binary archives. It never publishes.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "local release version, for example v0.1.0")
	commit := flag.String("commit", "", "source Git commit; must match the current HEAD (default: HEAD)")
	platforms := flag.String("platforms", "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64", "comma-separated GOOS/GOARCH targets; host selects the current machine")
	out := flag.String("out", "dist", "local output directory")
	flag.Parse()
	if flag.NArg() != 0 || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,79}$`).MatchString(*version) {
		return fmt.Errorf("--version is required and must contain only letters, digits, dot, plus, underscore or hyphen")
	}
	resolvedCommit, err := resolveSourceCommit(*commit)
	if err != nil {
		return err
	}
	*commit = resolvedCommit
	status, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("inspect source working tree: %w", err)
	}
	dirty := strconv.FormatBool(len(status) != 0)
	epoch := os.Getenv("SOURCE_DATE_EPOCH")
	if epoch == "" {
		data, err := exec.Command("git", "show", "-s", "--format=%ct", *commit).Output()
		if err != nil {
			return fmt.Errorf("resolve source date (or set SOURCE_DATE_EPOCH): %w", err)
		}
		epoch = strings.TrimSpace(string(data))
	}
	seconds, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil || seconds < 315532800 || seconds > 4354819199 {
		return fmt.Errorf("SOURCE_DATE_EPOCH must be UTC seconds in the ZIP-supported 1980–2107 range")
	}
	stamp := time.Unix(seconds, 0).UTC()
	license, err := os.ReadFile("LICENSE")
	if err != nil {
		return fmt.Errorf("run from the Wiretap repository root: %w", err)
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	var archives []string
	seen := map[string]bool{}
	for _, platform := range strings.Split(*platforms, ",") {
		if platform == "host" {
			platform = runtime.GOOS + "/" + runtime.GOARCH
		}
		parts := strings.Split(platform, "/")
		if len(parts) != 2 || !regexp.MustCompile(`^(linux|darwin|windows)/(amd64|arm64)$`).MatchString(platform) || seen[platform] {
			return fmt.Errorf("unsupported or duplicate platform %q", platform)
		}
		seen[platform] = true
		binaryName := "wiretap"
		if parts[0] == "windows" {
			binaryName += ".exe"
		}
		binaryPath := filepath.Join(*out, "bin", parts[0]+"_"+parts[1], binaryName)
		if err := os.MkdirAll(filepath.Dir(binaryPath), 0755); err != nil {
			return err
		}
		ldflags := "-s -w -buildid= -X github.com/mattlock83/wiretap/internal/version.Version=" + *version + " -X github.com/mattlock83/wiretap/internal/version.Commit=" + *commit + " -X github.com/mattlock83/wiretap/internal/version.BuildDirty=" + dirty
		command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", binaryPath, "./cmd/wiretap")
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOAMD64=v1", "GOARM64=v8.0", "GOOS="+parts[0], "GOARCH="+parts[1])
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("build %s: %w", platform, err)
		}
		binary, err := os.ReadFile(binaryPath)
		if err != nil {
			return err
		}
		extension := ".tar.gz"
		if parts[0] == "windows" {
			extension = ".zip"
		}
		name := "wiretap_" + *version + "_" + parts[0] + "_" + parts[1] + extension
		if err := writeArchive(filepath.Join(*out, name), binaryName, binary, license, stamp); err != nil {
			return err
		}
		archives = append(archives, name)
		fmt.Println(filepath.Join(*out, name))
	}
	sort.Strings(archives)
	var checksums strings.Builder
	for _, name := range archives {
		data, err := os.ReadFile(filepath.Join(*out, name))
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(hash[:]), name)
	}
	return os.WriteFile(filepath.Join(*out, "SHA256SUMS"), []byte(checksums.String()), 0644)
}

func resolveSourceCommit(commit string) (string, error) {
	if commit != "" && !regexp.MustCompile(`^[a-fA-F0-9]{7,64}$`).MatchString(commit) {
		return "", fmt.Errorf("--commit must be a Git commit hash")
	}
	data, err := exec.Command("git", "rev-parse", "--verify", "HEAD^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("resolve source HEAD: %w", err)
	}
	head := strings.TrimSpace(string(data))
	if commit == "" {
		return head, nil
	}
	data, err = exec.Command("git", "rev-parse", "--verify", strings.ToLower(commit)+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("resolve --commit %q: %w", commit, err)
	}
	resolved := strings.TrimSpace(string(data))
	if resolved != head {
		return "", fmt.Errorf("--commit %s does not match current HEAD %s; check out the selected commit before building", resolved, head)
	}
	return resolved, nil
}

func writeArchive(path, binaryName string, binary, license []byte, stamp time.Time) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { err = joinClose(err, file.Close()) }()
	entries := []struct {
		name string
		data []byte
		mode int64
	}{{"LICENSE", license, 0644}, {binaryName, binary, 0755}}
	if strings.HasSuffix(path, ".zip") {
		writer := zip.NewWriter(file)
		defer func() { err = joinClose(err, writer.Close()) }()
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: stamp}
			header.SetMode(os.FileMode(entry.mode))
			part, err := writer.CreateHeader(header)
			if err != nil {
				return err
			}
			if _, err := part.Write(entry.data); err != nil {
				return err
			}
		}
		return nil
	}
	compressed := gzip.NewWriter(file)
	defer func() { err = joinClose(err, compressed.Close()) }()
	writer := tar.NewWriter(compressed)
	defer func() { err = joinClose(err, writer.Close()) }()
	for _, entry := range entries {
		if err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data)), ModTime: stamp, Format: tar.FormatPAX}); err != nil {
			return err
		}
		if _, err := writer.Write(entry.data); err != nil {
			return err
		}
	}
	return nil
}

func joinClose(previous, closeError error) error {
	if previous != nil {
		return previous
	}
	return closeError
}
