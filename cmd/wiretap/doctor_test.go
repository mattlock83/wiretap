package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

const doctorValidOpenAPI = `openapi: 3.0.3
info: {title: Example, version: "1"}
paths:
  /items:
    get:
      responses:
        "200": {description: OK}
`

func setupProject(t *testing.T, extra string) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "api.yaml"), []byte(doctorValidOpenAPI), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".wiretap.yaml")
	data := "version: 1\nsut:\n  url: http://127.0.0.1:1\n  schema: api.yaml\n" + extra
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func setupCommandOutput(t *testing.T, run func() error) ([]byte, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = previous }()
	type output struct {
		data []byte
		err  error
	}
	result := make(chan output, 1)
	go func() {
		data, err := io.ReadAll(reader)
		result <- output{data, err}
	}()
	commandErr := run()
	_ = writer.Close()
	read := <-result
	_ = reader.Close()
	if read.err != nil {
		t.Fatal(read.err)
	}
	return read.data, commandErr
}

func doctorCheckNamed(t *testing.T, report doctorReport, name string) doctorCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("check %q missing from %+v", name, report)
	return doctorCheck{}
}

func TestDoctorValidatesLocalReferencesWithoutWriting(t *testing.T) {
	root, config := setupProject(t, "")
	if err := os.WriteFile(filepath.Join(root, "model.yaml"), []byte("type: object\nproperties:\n  name: {type: string}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := strings.Replace(doctorValidOpenAPI, `"200": {description: OK}`, `"200":
          description: OK
          content:
            application/json:
              schema: {$ref: 'model.yaml'}`, 1)
	if err := os.WriteFile(filepath.Join(root, "api.yaml"), []byte(spec), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	overriddenOutput := filepath.Join(root, "overridden", "artifacts")
	t.Setenv("WIRETAP_OUTPUT_DIR", overriddenOutput)
	data, err := setupCommandOutput(t, func() error {
		return doctorCommand(context.Background(), []string{"--format", "json"})
	})
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, data)
	}
	var report doctorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.ReportVersion != "1.0" || report.ConfigPath != config || doctorCheckNamed(t, report, "sut.schema").Status != "ok" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !strings.Contains(doctorCheckNamed(t, report, "output").Message, overriddenOutput) {
		t.Fatalf("output environment override not reflected: %+v", report)
	}
	for _, path := range []string{overriddenOutput, filepath.Join(root, "artifacts"), filepath.Join(root, "ca-cert.pem")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("doctor created %s: %v", path, err)
		}
	}
}

func TestDoctorWritesFailureReportForMissingAndInvalidConfig(t *testing.T) {
	root, config := setupProject(t, "")
	for _, scenario := range []struct{ name, path, contents, message string }{
		{"missing config", filepath.Join(root, "missing.yaml"), "", "missing.yaml"},
		{"malformed contract", config, "openapi: [", "api.yaml"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if scenario.contents != "" {
				if err := os.WriteFile(filepath.Join(root, "api.yaml"), []byte(scenario.contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			data, err := setupCommandOutput(t, func() error {
				return doctorCommand(context.Background(), []string{"--config", scenario.path, "--format", "json"})
			})
			var report doctorReport
			if err == nil || json.Unmarshal(data, &report) != nil || report.OK || !strings.Contains(string(data), scenario.message) {
				t.Fatalf("expected reported local failure, got %v\n%s", err, data)
			}
		})
	}
}

func TestDoctorMissingEnvironmentAndRuntimeOverrides(t *testing.T) {
	for _, scenario := range []struct{ key, value, check string }{
		{"WIRETAP_MAX_CAPTURE_BYTES", "invalid", "runtime"},
		{"WIRETAP_HTTP_READ_TIMEOUT", "-1s", "runtime"},
		{"WIRETAP_MAX_ARCHIVE_FILES", "0", "archive_limits"},
	} {
		t.Run(scenario.key, func(t *testing.T) {
			_, config := setupProject(t, "")
			t.Setenv(scenario.key, scenario.value)
			report := inspectSetup(t.Context(), config)
			if report.OK || doctorCheckNamed(t, report, scenario.check).Status != "error" {
				t.Fatalf("invalid environment setting was accepted: %+v", report)
			}
		})
	}
	t.Run("missing substitution", func(t *testing.T) {
		_, config := setupProject(t, "test_source: {revision: '${WIRETAP_DOCTOR_TEST_UNSET}'}\n")
		t.Setenv("WIRETAP_DOCTOR_TEST_UNSET", "placeholder")
		if err := os.Unsetenv("WIRETAP_DOCTOR_TEST_UNSET"); err != nil {
			t.Fatal(err)
		}
		report := inspectSetup(t.Context(), config)
		if report.OK || !strings.Contains(doctorCheckNamed(t, report, "config").Message, "WIRETAP_DOCTOR_TEST_UNSET is not set") {
			t.Fatalf("missing environment substitution accepted: %+v", report)
		}
	})
}

func TestDoctorDefersRemoteReferencesWithoutRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "doctor must not contact this server", http.StatusInternalServerError)
	}))
	defer server.Close()
	root, config := setupProject(t, "")
	spec := strings.Replace(doctorValidOpenAPI, `"200": {description: OK}`, fmt.Sprintf(`"200":
          description: OK
          content:
            application/json:
              schema: {$ref: '%s/model.yaml'}`, server.URL), 1)
	if err := os.WriteFile(filepath.Join(root, "api.yaml"), []byte(spec), 0600); err != nil {
		t.Fatal(err)
	}
	report := inspectSetup(t.Context(), config)
	if !report.OK || doctorCheckNamed(t, report, "sut.schema").Status != "deferred" {
		t.Fatalf("remote reference was not deferred: %+v", report)
	}
	if requests.Load() != 0 {
		t.Fatalf("remote schema contacted server %d times", requests.Load())
	}
}

func TestDoctorOutputParentRejectsFilesAndReadOnlyDirectories(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := doctorOutputParent(file); err == nil {
		t.Fatal("file accepted as output directory")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX directory permission bits")
	}
	dir := filepath.Join(root, "read-only")
	if err := os.Mkdir(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	if _, err := doctorOutputParent(filepath.Join(dir, "new", "artifacts")); err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("read-only output parent accepted: %v", err)
	}
}
