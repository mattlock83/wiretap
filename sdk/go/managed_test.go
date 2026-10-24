package wiretap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func managedHelperCommand(mode string, capture **exec.Cmd) func(string, ...string) *exec.Cmd {
	return func(_ string, _ ...string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestManagedHelperProcess$", "--", "wiretap-managed-helper", mode)
		if capture != nil {
			*capture = cmd
		}
		return cmd
	}
}

func managedTestOptions(t *testing.T) RunOptions {
	t.Helper()
	return RunOptions{Dir: t.TempDir(), StartupTimeout: 5 * time.Second, FinishTimeout: 3 * time.Second, ShutdownTimeout: 2 * time.Second}
}

// This is an actual child executable with local HTTP control and a stdin
// lifeline. Test behavior is selected only through the internal command seam.
func TestManagedHelperProcess(t *testing.T) {
	marker := -1
	for index, arg := range os.Args {
		if arg == "wiretap-managed-helper" {
			marker = index
			break
		}
	}
	if marker == -1 {
		return
	}
	mode := os.Args[marker+1]
	if mode == "exit" {
		fmt.Fprint(os.Stderr, strings.Repeat("x", managedLogLimit*2)+"startup diagnostic tail")
		os.Exit(7)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(8)
	}
	address := "http://" + listener.Addr().String()
	output := os.Getenv("WIRETAP_OUTPUT_DIR")
	if output == "" {
		output = "."
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(8)
	}
	output, _ = filepath.Abs(output)
	bundle, _ := os.MkdirTemp(output, "run-managed-")
	ready := map[string]any{"control_url": address, "pid": os.Getpid()}
	switch mode {
	case "wrong-pid":
		ready["pid"] = os.Getpid() + 1
	case "external-url":
		ready["control_url"] = "http://example.com:80"
	}
	readyData, _ := json.Marshal(ready)
	if mode == "bad-ready" {
		readyData = []byte("{not json}")
	}
	if mode != "no-ready" {
		file := os.Getenv("WIRETAP_READY_FILE")
		_ = os.WriteFile(file+".tmp", readyData, 0o600)
		_ = os.Rename(file+".tmp", file)
	}
	mux := http.NewServeMux()
	var finishCount int
	mux.HandleFunc("/v1/startRun", func(w http.ResponseWriter, r *http.Request) {
		if mode == "start-block" {
			<-r.Context().Done()
			return
		}
		if mode == "bad-start" {
			_, _ = io.WriteString(w, "null")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "run-managed", "bundle_path": bundle, "sut_url": address})
	})
	mux.HandleFunc("/v1/finishRun", func(w http.ResponseWriter, r *http.Request) {
		finishCount++
		if mode == "finish-block" {
			<-r.Context().Done()
			return
		}
		if mode == "bad-finish" {
			_, _ = io.WriteString(w, "null")
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		result := map[string]any{"status": "complete", "run_id": "run-managed", "bundle_path": bundle, "finish_count": finishCount, "requested": payload}
		if mode == "finish-error" {
			result["error"] = strings.Repeat("export failure detail ", 1200)
			result["outputs"] = map[string]any{"bundle_path": bundle, "coverage": map[string]string{"json": filepath.Join(bundle, "coverage.json")}, "postman": []string{filepath.Join(bundle, "postman.json")}}
			w.WriteHeader(http.StatusConflict)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	server := &http.Server{Handler: mux}
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		if mode == "stubborn" {
			return
		}
		fmt.Fprint(os.Stdout, "lifeline closed; graceful shutdown\n")
		_ = server.Close()
	}()
	fmt.Fprint(os.Stdout, "helper ready\n")
	_ = server.Serve(listener)
	os.Exit(0)
}

func TestManagedRuntimeOwnsChildAndWritesIsolatedReceipt(t *testing.T) {
	options := managedTestOptions(t)
	options.OutputDir = "artifacts"
	options.Outputs = &OutputOptions{Coverage: []string{"json"}, Postman: true}
	var cmd *exec.Cmd
	owner, err := startManagedWithCommand(context.Background(), options, managedHelperCommand("success", &cmd))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.close() })
	if !strings.HasPrefix(owner.info.BundlePath, filepath.Join(options.Dir, "artifacts")+string(filepath.Separator)) {
		t.Fatalf("bundle does not use resolved output directory: %s", owner.info.BundlePath)
	}
	results := make(chan *FinishResult, 12)
	errors := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			result, err := owner.close()
			results <- result
			errors <- err
		})
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result != owner.result || result.RunID != owner.info.RunID {
			t.Fatalf("close must share its result: %#v", result)
		}
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		t.Fatal("owned child did not exit gracefully")
	}
	assertManagedTempRemoved(t, cmd)
	data, err := os.ReadFile(filepath.Join(owner.info.BundlePath, "last-run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		FinishCount int `json:"finish_count"`
		Requested   struct {
			Outputs OutputOptions `json:"outputs"`
		} `json:"requested"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.FinishCount != 1 || !receipt.Requested.Outputs.Postman || len(receipt.Requested.Outputs.Coverage) != 1 {
		t.Fatalf("receipt did not preserve a single finalization and requested outputs: %s (%v)", data, err)
	}
	logs, _ := os.ReadFile(filepath.Join(owner.info.BundlePath, "wiretap.log"))
	if !strings.Contains(string(logs), "graceful shutdown") {
		t.Fatalf("log was written before process output drained: %s", logs)
	}
	if _, err := os.Stat(filepath.Join(options.Dir, "artifacts", "last-run.json")); !os.IsNotExist(err) {
		t.Fatal("receipt must not overwrite a shared parent directory")
	}
}

func TestManagedRuntimeStartupFailuresCleanUp(t *testing.T) {
	for _, tc := range []struct{ mode, message string }{
		{"wrong-pid", "owned local process"}, {"external-url", "owned local process"},
		{"bad-ready", "invalid Wiretap readiness"}, {"bad-start", "invalid run receipt"},
		{"exit", "startup diagnostic tail"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var cmd *exec.Cmd
			owner, err := startManagedWithCommand(context.Background(), managedTestOptions(t), managedHelperCommand(tc.mode, &cmd))
			if owner != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("unexpected startup outcome: owner=%v error=%v", owner, err)
			}
			if len(err.Error()) > managedLogLimit+2048 {
				t.Fatalf("startup diagnostics are not bounded: %d bytes", len(err.Error()))
			}
			if cmd.ProcessState == nil {
				t.Fatal("startup failure leaked the child")
			}
			assertManagedTempRemoved(t, cmd)
		})
	}
}

func TestManagedRuntimeCancellation(t *testing.T) {
	for _, mode := range []string{"no-ready", "start-block", "success"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var cmd *exec.Cmd
			if mode != "success" {
				timer := time.AfterFunc(150*time.Millisecond, cancel)
				defer timer.Stop()
			}
			owner, err := startManagedWithCommand(ctx, managedTestOptions(t), managedHelperCommand(mode, &cmd))
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				cancel()
				select {
				case <-owner.closed:
				case <-time.After(5 * time.Second):
					t.Fatal("context cancellation did not close the child")
				}
				if _, err := owner.close(); err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("startup cancellation was lost: %v", err)
			}
			if cmd.ProcessState == nil {
				t.Fatal("cancellation leaked the child")
			}
			assertManagedTempRemoved(t, cmd)
		})
	}
}

func TestManagedRuntimeStartupAndFinishDeadlines(t *testing.T) {
	for _, mode := range []string{"no-ready", "start-block", "finish-block"} {
		t.Run(mode, func(t *testing.T) {
			options := managedTestOptions(t)
			if mode != "finish-block" {
				options.StartupTimeout = 250 * time.Millisecond
			}
			options.FinishTimeout = 100 * time.Millisecond
			var cmd *exec.Cmd
			owner, err := startManagedWithCommand(context.Background(), options, managedHelperCommand(mode, &cmd))
			if mode == "finish-block" && err == nil {
				_, err = owner.close()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline was not preserved: %v", err)
			}
			if cmd.ProcessState == nil {
				t.Fatal("deadline leaked the child")
			}
			assertManagedTempRemoved(t, cmd)
		})
	}
}

func TestManagedRuntimeRetainsExportFailureReceipt(t *testing.T) {
	owner, err := startManagedWithCommand(context.Background(), managedTestOptions(t), managedHelperCommand("finish-error", nil))
	if err != nil {
		t.Fatal(err)
	}
	result, err := owner.close()
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") || result == nil || result.Outputs == nil || result.Outputs.Coverage["json"] == "" || len(result.Outputs.Postman) != 1 {
		t.Fatalf("partial export receipt lost: result=%#v error=%v", result, err)
	}
	data, readErr := os.ReadFile(filepath.Join(owner.info.BundlePath, "last-run.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var receipt map[string]any
	if json.Unmarshal(data, &receipt) != nil || len(receipt["error"].(string)) < 8192 || receipt["finish_count"].(float64) != 1 {
		t.Fatalf("full error receipt was not retained: %d bytes", len(data))
	}
}

func TestManagedRuntimeRejectsIncompleteFinishReceipt(t *testing.T) {
	owner, err := startManagedWithCommand(context.Background(), managedTestOptions(t), managedHelperCommand("bad-finish", nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = owner.close()
	if err == nil || !strings.Contains(err.Error(), "incomplete or mismatched") {
		t.Fatalf("null finish receipt succeeded: %v", err)
	}
}

func TestManagedRuntimeKillsChildThatIgnoresLifeline(t *testing.T) {
	options := managedTestOptions(t)
	options.ShutdownTimeout = 100 * time.Millisecond
	owner, err := startManagedWithCommand(context.Background(), options, managedHelperCommand("stubborn", nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = owner.close()
	if err == nil || !strings.Contains(err.Error(), "was killed") || owner.cmd.ProcessState == nil {
		t.Fatalf("stubborn child was not reaped and reported: %v", err)
	}
}

func TestManagedRuntimeStartErrorAndInvalidOptions(t *testing.T) {
	options := managedTestOptions(t)
	options.Binary = filepath.Join(options.Dir, "missing-wiretap-executable")
	if owner, err := startManaged(context.Background(), options); owner != nil || err == nil || !strings.Contains(err.Error(), "start Wiretap") {
		t.Fatalf("missing binary should fail clearly: %v", err)
	}
	for _, options := range []RunOptions{{StartupTimeout: -1}, {FinishTimeout: -1}, {ShutdownTimeout: -1}, {Binary: " "}} {
		if _, err := startManaged(context.Background(), options); err == nil {
			t.Fatalf("invalid options accepted: %#v", options)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := startManaged(ctx, RunOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("already cancelled context accepted: %v", err)
	}
}

func TestManagedRuntimeResolvesOptionsAndOwnsListenerSettings(t *testing.T) {
	t.Setenv("WIRETAP_BINARY", "tools/from-environment")
	t.Setenv("WIRETAP_HTTP_LISTEN", "0.0.0.0:18000")
	t.Setenv("WIRETAP_PROXY_LISTEN", "0.0.0.0:18080")
	t.Setenv("WIRETAP_EXIT_ON_STDIN_EOF", "0")
	options := managedTestOptions(t)
	options.Config = "config/wiretap.yaml"
	var observedBinary string
	var observedArgs []string
	var cmd *exec.Cmd
	owner, err := startManagedWithCommand(context.Background(), options, func(binary string, args ...string) *exec.Cmd {
		observedBinary, observedArgs = binary, args
		return managedHelperCommand("success", &cmd)(binary, args...)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.close() })
	if observedBinary != filepath.Join(options.Dir, "tools", "from-environment") || len(observedArgs) != 2 || observedArgs[0] != "--config" || observedArgs[1] != filepath.Join(options.Dir, options.Config) {
		t.Fatalf("options were not resolved against Dir: binary=%s args=%v", observedBinary, observedArgs)
	}
	for key, want := range map[string]string{"WIRETAP_HTTP_LISTEN": "127.0.0.1:0", "WIRETAP_PROXY_LISTEN": "127.0.0.1:0", "WIRETAP_EXIT_ON_STDIN_EOF": "1"} {
		count := 0
		for _, value := range cmd.Env {
			if strings.HasPrefix(value, key+"=") {
				count++
				if value != key+"="+want {
					t.Fatalf("owned process environment: got %q, want %q", value, want)
				}
			}
		}
		if count != 1 {
			t.Fatalf("environment must define %s once, got %d", key, count)
		}
	}
	if _, err := owner.close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedReadinessURLValidation(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:1234", "http://[::1]:1234/", "http://localhost:1234"} {
		if !managedLocalURL(value, false) {
			t.Errorf("owned local URL rejected: %s", value)
		}
	}
	for _, value := range []string{"http://example.com:1234", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1", "http://user@127.0.0.1:1234", "http://127.0.0.1:1234/path", "http://127.0.0.1:1234?query", "http://127.0.0.1:1234#fragment", "https://127.0.0.1:1234"} {
		if managedLocalURL(value, false) {
			t.Errorf("invalid control URL accepted: %s", value)
		}
	}
	if !managedLocalURL("https://127.0.0.1:1234", true) {
		t.Error("local TLS proxy URL rejected")
	}
}

func assertManagedTempRemoved(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	for _, value := range cmd.Env {
		if path, ok := strings.CutPrefix(value, "WIRETAP_READY_FILE="); ok {
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("readiness directory was not removed: %s (%v)", path, err)
			}
			return
		}
	}
	t.Fatal("readiness file environment missing")
}
