package bundle

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func TestLoadEnvironmentOverrides(t *testing.T) {
	root, _, _ := httpLimitsFixture(t, 2)
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILE_BYTES", "1")
	if _, _, err := Load(root); !errors.Is(err, ErrLoadLimit) {
		t.Fatalf("binary loader ignored file budget: %v", err)
	}
	// A caller can increase all defaults explicitly without disabling bounds.
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILE_BYTES", strconv.FormatInt(DefaultLoadLimits().MaxFileBytes*2, 10))
	t.Setenv("WIRETAP_MAX_ARCHIVE_TOTAL_BYTES", strconv.FormatInt(DefaultLoadLimits().MaxTotalBytes*2, 10))
	t.Setenv("WIRETAP_MAX_ARCHIVE_FILES", strconv.Itoa(DefaultLoadLimits().MaxFileReads*2))
	limits, err := loadLimitsFromEnv()
	if err != nil || limits.MaxFileBytes != DefaultLoadLimits().MaxFileBytes*2 || limits.MaxTotalBytes != DefaultLoadLimits().MaxTotalBytes*2 || limits.MaxFileReads != DefaultLoadLimits().MaxFileReads*2 {
		t.Fatalf("larger override not applied: %+v %v", limits, err)
	}
	if _, captures, err := Load(root); err != nil || len(captures) != 2 {
		t.Fatalf("larger override rejected normal archive: captures=%d %v", len(captures), err)
	}
}

func TestLoadRejectsInvalidEnvironmentOverrides(t *testing.T) {
	for _, name := range []string{"WIRETAP_MAX_ARCHIVE_FILE_BYTES", "WIRETAP_MAX_ARCHIVE_TOTAL_BYTES", "WIRETAP_MAX_ARCHIVE_FILES"} {
		for _, value := range []string{"", "0", "-1", "1.5", "unlimited", "9223372036854775808"} {
			t.Run(name+"="+value, func(t *testing.T) {
				t.Setenv(name, value)
				if _, _, err := Load("missing archive"); err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("invalid override was not rejected before reading: %v", err)
				}
			})
		}
	}
}

func httpLimitsFixture(t *testing.T, attempts int) (string, Run, []Attempt) {
	t.Helper()
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var captures []Attempt
	for i := 0; i < attempts; i++ {
		tc := artifact.TestCase{ID: fmt.Sprintf("test-%d", i), Name: "shared test data", Status: "pass", StartedAt: time.Now().UTC()}
		if _, err := w.Register(tc, nil); err != nil {
			t.Fatal(err)
		}
		capture := &artifact.Artifact{Version: 1, TestCase: tc, Traffic: []artifact.Flow{{
			ID: "flow", Protocol: "http", Channel: "test_to_sut",
			Request:  artifact.Message{HTTP: &artifact.HttpMessage{Method: "POST", URL: "http://orders/orders", Body: &artifact.Body{Text: "shared body"}}},
			Response: artifact.Message{HTTP: &artifact.HttpMessage{Status: 200, Body: &artifact.Body{Text: "shared body"}}},
		}}}
		if _, err := w.Commit(capture); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, Attempt{Bodies: []BodyRef{{RetainedBytes: len("shared body")}, {RetainedBytes: len("shared body")}}})
	}
	if err := w.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	return w.Path(), w.Snapshot(), captures
}

func fileBytes(t *testing.T, name string) int64 {
	t.Helper()
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func maximumFileBytes(t *testing.T, root string) int64 {
	t.Helper()
	var largest int64
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		largest = max(largest, fileBytes(t, name))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return largest
}

func sparseFile(t *testing.T, name string, bytes int64) {
	t.Helper()
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(bytes); err != nil {
		t.Fatal(err)
	}
}

func expectLoadLimit(t *testing.T, root string, limits LoadLimits, field string) {
	t.Helper()
	run, captures, err := LoadWithLimits(root, limits)
	if !errors.Is(err, ErrLoadLimit) || !strings.Contains(err.Error(), field) {
		t.Fatalf("expected %s limit, got run=%v captures=%d error=%v", field, run, len(captures), err)
	}
	if run != nil || captures != nil {
		t.Fatal("limited load returned partial evidence")
	}
}

func TestLoadRejectsOversizedFilesBeforeParsingOrHashing(t *testing.T) {
	for _, resource := range []string{"run manifest", "capture", "contract", "contract manifest", "contract import", "HTTP payload"} {
		t.Run(resource, func(t *testing.T) {
			var root, target string
			switch resource {
			case "run manifest", "capture", "HTTP payload":
				var run Run
				root, run, _ = httpLimitsFixture(t, 1)
				switch resource {
				case "run manifest":
					target = filepath.Join(root, "bundle.json")
				case "capture":
					target = filepath.Join(root, run.Attempts[0].Path)
				case "HTTP payload":
					target = filepath.Join(root, "payloads", digest([]byte("shared body"))+".bin")
				}
			case "contract", "contract manifest", "contract import":
				ref, manifest := openAPIExportFixture(t)
				root = filepath.Dir(filepath.Dir(filepath.Dir(ref.Source.Path)))
				target = ref.Source.Path
				if resource == "contract manifest" {
					target = filepath.Join(filepath.Dir(target), "manifest.json")
				} else if resource == "contract import" {
					for _, file := range manifest.Files {
						if file.Path != manifest.Root {
							target = filepath.Join(root, filepath.FromSlash(file.Path))
							break
						}
					}
				}

			}
			if target == "" {
				t.Fatal("fixture has no target attachment")
			}
			if _, _, err := Load(root); err != nil {
				t.Fatalf("invalid baseline fixture: %v", err)
			}
			limit := maximumFileBytes(t, root)
			sparseFile(t, target, limit+1)
			expectLoadLimit(t, root, LoadLimits{MaxFileBytes: limit}, "MaxFileBytes")
		})
	}
}

func TestLoadDefaultLimitRejectsSparseManifest(t *testing.T) {
	root, _, _ := httpLimitsFixture(t, 1)
	sparseFile(t, filepath.Join(root, "bundle.json"), DefaultLoadLimits().MaxFileBytes+1)
	if _, _, err := Load(root); !errors.Is(err, ErrLoadLimit) {
		t.Fatalf("default loader accepted oversized sparse manifest: %v", err)
	}
}

func TestLoadBudgetsCountRepeatedPayloadReads(t *testing.T) {
	root, run, attempts := httpLimitsFixture(t, 3)
	total := fileBytes(t, filepath.Join(root, "bundle.json"))
	reads := 1
	for i, ref := range run.Attempts {
		total += fileBytes(t, filepath.Join(root, ref.Path))
		reads++
		for _, body := range attempts[i].Bodies {
			total += int64(body.RetainedBytes)
			reads++
		}
	}
	// There is only one physical payload file, but all six restored bodies
	// consume the read and byte budgets. Exact budgets still accept the run.
	limits := LoadLimits{MaxTotalBytes: total, MaxFileReads: reads}
	if _, captures, err := LoadWithLimits(root, limits); err != nil || len(captures) != 3 {
		t.Fatalf("exact budgets rejected normal suite: captures=%d error=%v", len(captures), err)
	}
	expectLoadLimit(t, root, LoadLimits{MaxTotalBytes: total - 1}, "MaxTotalBytes")
	expectLoadLimit(t, root, LoadLimits{MaxFileReads: reads - 1}, "MaxFileReads")
	if _, captures, err := Load(root); err != nil || len(captures) != 3 {
		t.Fatalf("defaults rejected normal suite: captures=%d error=%v", len(captures), err)
	}
}

func TestLoadCountsManifestAndInventoriesBeforeFollowingReferences(t *testing.T) {
	t.Run("run manifest consumes total budget", func(t *testing.T) {
		root, _, _ := httpLimitsFixture(t, 1)
		expectLoadLimit(t, root, LoadLimits{MaxTotalBytes: fileBytes(t, filepath.Join(root, "bundle.json")) - 1}, "MaxTotalBytes")
	})
	t.Run("test inventory", func(t *testing.T) {
		root, run, _ := httpLimitsFixture(t, 3)
		if err := os.Remove(filepath.Join(root, run.Attempts[0].Path)); err != nil {
			t.Fatal(err)
		}
		expectLoadLimit(t, root, LoadLimits{MaxFileReads: 3}, "run test inventory")
	})
	t.Run("contract imports", func(t *testing.T) {
		ref, manifest := openAPIExportFixture(t)
		root := filepath.Dir(filepath.Dir(filepath.Dir(ref.Source.Path)))
		for _, file := range manifest.Files {
			if file.Path != manifest.Root {
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(file.Path))); err != nil {
					t.Fatal(err)
				}
			}
		}
		// Run, root and contract manifest exhaust the available reads. The
		// manifest's inventory must fail before following its missing import.
		expectLoadLimit(t, root, LoadLimits{MaxFileReads: 3}, "file inventory")
	})
}

func TestLoadLimitsValidateConfigurationAndPreserveIntegrityChecks(t *testing.T) {
	root, _, _ := httpLimitsFixture(t, 1)
	for _, limits := range []LoadLimits{{MaxFileBytes: -1}, {MaxTotalBytes: -1}, {MaxFileReads: -1}, {MaxFileBytes: math.MaxInt64}, {MaxTotalBytes: math.MaxInt64}} {
		if _, _, err := LoadWithLimits(root, limits); err == nil || errors.Is(err, ErrLoadLimit) {
			t.Fatalf("invalid limits accepted or confused with exhausted budget: %+v, %v", limits, err)
		}
	}
	if _, _, err := LoadWithLimits(root, LoadLimits{}); err != nil {
		t.Fatalf("zero-valued limits should use defaults: %v", err)
	}
	payload := filepath.Join(root, "payloads", digest([]byte("shared body"))+".bin")
	if err := os.WriteFile(payload, []byte("forged body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadWithLimits(root, DefaultLoadLimits()); err == nil || !strings.Contains(err.Error(), "hash mismatch") || errors.Is(err, ErrLoadLimit) {
		t.Fatalf("configured loader lost hash verification: %v", err)
	}
}

func TestLoadDefaultsAcceptMultiAttemptSuite(t *testing.T) {
	const tests = 128
	root, _, _ := httpLimitsFixture(t, tests)
	run, captures, err := Load(root)
	if err != nil || run == nil || len(captures) != tests {
		t.Fatalf("normal suite failed: captures=%d error=%v", len(captures), err)
	}
	for _, capture := range captures {
		flow := capture.Traffic[0]
		if flow.Request.HTTP.Body.Text != "shared body" || flow.Response.HTTP.Body.Text != "shared body" {
			t.Fatalf("attempt %s lost retained payloads", capture.TestCase.ID)
		}
	}
}

func TestArchiveReaderRejectsNonRegularFiles(t *testing.T) {
	reader := &archiveReader{limits: DefaultLoadLimits()}
	if _, err := reader.readFile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("non-regular archive reference accepted: %v", err)
	}
}
