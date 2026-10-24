package wiretap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const managedLogLimit = 64 << 10

type managedRuntime struct {
	runtime Runtime
	info    struct {
		RunID      string `json:"run_id"`
		BundlePath string `json:"bundle_path"`
		SUTURL     string `json:"sut_url"`
	}
	options RunOptions
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	tempDir string
	logs    managedLog
	exited  chan struct{}
	exitErr error // Written before exited closes.
	closed  chan struct{}
	once    sync.Once
	result  *FinishResult
	err     error
}

// managedLog retains the end of combined stdout/stderr without allowing a
// noisy child to grow the test process's memory indefinitely.
type managedLog struct {
	mu   sync.Mutex
	data []byte
}

func (l *managedLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(data)
	if len(data) >= managedLogLimit {
		l.data = append(l.data[:0], data[len(data)-managedLogLimit:]...)
	} else {
		if excess := len(l.data) + len(data) - managedLogLimit; excess > 0 {
			copy(l.data, l.data[excess:])
			l.data = l.data[:len(l.data)-excess]
		}
		l.data = append(l.data, data...)
	}
	return n, nil
}

func (l *managedLog) snapshot() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.data...)
}

func startManaged(ctx context.Context, options RunOptions) (*managedRuntime, error) {
	return startManagedWithCommand(ctx, options, exec.Command)
}

// Keeping command construction separate permits lifecycle tests to use a real
// subprocess without adding special test behavior to the shipped executable.
func startManagedWithCommand(ctx context.Context, options RunOptions, command func(string, ...string) *exec.Cmd) (_ *managedRuntime, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.StartupTimeout < 0 || options.FinishTimeout < 0 || options.ShutdownTimeout < 0 {
		return nil, errors.New("Wiretap startup, finish, and shutdown timeouts must not be negative")
	}
	if options.StartupTimeout == 0 {
		options.StartupTimeout = 30 * time.Second
	}
	if options.FinishTimeout == 0 {
		options.FinishTimeout = 30 * time.Second
	}
	if options.ShutdownTimeout == 0 {
		options.ShutdownTimeout = 5 * time.Second
	}
	dir := options.Dir
	if dir == "" {
		dir = "."
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("Wiretap working directory: %w", err)
	}
	binary := options.Binary
	if binary == "" {
		binary = os.Getenv("WIRETAP_BINARY")
	}
	if binary == "" {
		binary = "wiretap"
	}
	if strings.TrimSpace(binary) == "" {
		return nil, errors.New("Wiretap binary must be an executable path or command")
	}
	if !filepath.IsAbs(binary) && strings.ContainsAny(binary, `/\`) {
		binary = filepath.Join(dir, binary)
	}
	var args []string
	if options.Config != "" {
		args = []string{"--config", managedPath(dir, options.Config)}
	}
	owner := &managedRuntime{options: options, exited: make(chan struct{}), closed: make(chan struct{})}
	owner.tempDir, err = os.MkdirTemp("", "wiretap-go-")
	if err != nil {
		return nil, fmt.Errorf("Wiretap readiness directory: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		_, cleanupErr := owner.close()
		err = errors.Join(err, cleanupErr)
		if logs := strings.TrimSpace(string(owner.logs.snapshot())); logs != "" {
			err = fmt.Errorf("%w\nWiretap process output:\n%s", err, logs)
		}
	}()
	readyFile := filepath.Join(owner.tempDir, "ready.json")
	env := map[string]string{
		"WIRETAP_READY_FILE":        readyFile,
		"WIRETAP_HTTP_LISTEN":       "127.0.0.1:0",
		"WIRETAP_PROXY_LISTEN":      "127.0.0.1:0",
		"WIRETAP_EXIT_ON_STDIN_EOF": "1",
	}
	if options.OutputDir != "" {
		env["WIRETAP_OUTPUT_DIR"] = managedPath(dir, options.OutputDir)
	}
	owner.cmd = command(binary, args...)
	owner.cmd.Dir = dir
	owner.cmd.Env = managedEnvironment(os.Environ(), env)
	owner.cmd.Stdout = &owner.logs
	owner.cmd.Stderr = &owner.logs
	owner.cmd.WaitDelay = options.ShutdownTimeout
	owner.stdin, err = owner.cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("Wiretap process lifeline: %w", err)
	}
	if err = owner.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Wiretap (%s): %w", binary, err)
	}
	go func() {
		owner.exitErr = owner.cmd.Wait()
		close(owner.exited)
	}()
	startup, cancel := context.WithTimeout(ctx, options.StartupTimeout)
	defer cancel()
	owner.runtime.BaseURL, err = owner.waitReady(startup, readyFile)
	if err != nil {
		return nil, err
	}
	if err = owner.runtime.call(startup, "/v1/startRun", map[string]any{}, &owner.info); err != nil {
		return nil, fmt.Errorf("start Wiretap capture: %w", err)
	}
	if err = owner.validateStart(); err != nil {
		// An incomplete identity cannot safely name the receipt destination.
		owner.info.BundlePath = ""
		return nil, err
	}
	if err = startup.Err(); err != nil {
		return nil, fmt.Errorf("start Wiretap capture: %w", err)
	}
	go func() {
		select {
		case <-ctx.Done():
			_, _ = owner.close()
		case <-owner.closed:
		}
	}()
	return owner, nil
}

func managedPath(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}

func managedEnvironment(original []string, override map[string]string) []string {
	env := make([]string, 0, len(original)+len(override))
	for _, value := range original {
		key, _, _ := strings.Cut(value, "=")
		replaced := false
		for name := range override {
			if strings.EqualFold(key, name) {
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, value)
		}
	}
	for key, value := range override {
		env = append(env, key+"="+value)
	}
	return env
}

func (owner *managedRuntime) waitReady(ctx context.Context, path string) (string, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for Wiretap readiness: %w", ctx.Err())
		case <-owner.exited:
			return "", fmt.Errorf("Wiretap exited before becoming ready: %v", owner.exitErr)
		default:
		}
		data, err := managedReadiness(path)
		if err == nil {
			var ready struct {
				ControlURL string `json:"control_url"`
				PID        int    `json:"pid"`
			}
			if err := json.Unmarshal(data, &ready); err != nil {
				return "", fmt.Errorf("invalid Wiretap readiness file: %w", err)
			}
			if ready.PID != owner.cmd.Process.Pid || !managedLocalURL(ready.ControlURL, false) {
				return "", errors.New("Wiretap readiness file did not identify the owned local process")
			}
			return strings.TrimRight(ready.ControlURL, "/"), nil
		}
		// Windows can expose the renamed file before releasing its DELETE-access
		// handle. Retry that sharing violation within the startup deadline.
		sharingViolation := runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(32))
		if !errors.Is(err, os.ErrNotExist) && !sharingViolation {
			return "", fmt.Errorf("read Wiretap readiness: %w", err)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for Wiretap readiness: %w", ctx.Err())
		case <-owner.exited:
			return "", fmt.Errorf("Wiretap exited before becoming ready: %v", owner.exitErr)
		case <-ticker.C:
		}
	}
}

func managedReadiness(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, managedLogLimit+1))
	if err == nil && len(data) > managedLogLimit {
		err = errors.New("readiness file exceeds 64 KiB")
	}
	return data, err
}

func managedLocalURL(value string, allowTLS bool) bool {
	address, err := url.Parse(value)
	if err != nil || (address.Scheme != "http" && !(allowTLS && address.Scheme == "https")) || address.User != nil || address.RawQuery != "" || address.Fragment != "" || (address.Path != "" && address.Path != "/") {
		return false
	}
	ip := net.ParseIP(address.Hostname())
	if address.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	port, err := strconv.Atoi(address.Port())
	return err == nil && port > 0 && port <= 65535
}

func (owner *managedRuntime) validateStart() error {
	if strings.TrimSpace(owner.info.RunID) == "" || !filepath.IsAbs(owner.info.BundlePath) || !managedLocalURL(owner.info.SUTURL, true) {
		return errors.New("Wiretap startRun returned an incomplete or invalid run receipt")
	}
	if info, err := os.Stat(owner.info.BundlePath); err != nil || !info.IsDir() {
		return errors.New("Wiretap startRun returned a missing or invalid bundle directory")
	}
	return nil
}

func (owner *managedRuntime) close() (*FinishResult, error) {
	owner.once.Do(func() {
		defer close(owner.closed)
		var raw json.RawMessage
		if owner.info.RunID != "" && owner.info.BundlePath != "" {
			ctx, cancel := context.WithTimeout(context.Background(), owner.options.FinishTimeout)
			owner.err = owner.runtime.call(ctx, "/v1/finishRun", struct {
				Outputs *OutputOptions `json:"outputs,omitempty"`
			}{owner.options.Outputs}, &raw)
			cancel()
			if len(raw) > 0 {
				owner.result = &FinishResult{}
				if err := json.Unmarshal(raw, owner.result); err != nil {
					owner.err = errors.Join(owner.err, fmt.Errorf("decode Wiretap finish receipt: %w", err))
					owner.result = nil
				}
			}
			if owner.err == nil && (owner.result == nil || owner.result.Status != "complete" || owner.result.RunID != owner.info.RunID || owner.result.BundlePath != owner.info.BundlePath) {
				owner.err = errors.New("Wiretap finishRun returned an incomplete or mismatched run receipt")
			}
		}
		owner.err = errors.Join(owner.err, owner.stop())
		if owner.info.BundlePath != "" {
			if err := os.MkdirAll(owner.info.BundlePath, 0o755); err != nil {
				owner.err = errors.Join(owner.err, fmt.Errorf("create Wiretap receipt directory: %w", err))
			} else {
				if err := os.WriteFile(filepath.Join(owner.info.BundlePath, "wiretap.log"), owner.logs.snapshot(), 0o644); err != nil {
					owner.err = errors.Join(owner.err, fmt.Errorf("write Wiretap process log: %w", err))
				}
				if len(raw) > 0 && json.Valid(raw) {
					if err := os.WriteFile(filepath.Join(owner.info.BundlePath, "last-run.json"), append(raw, '\n'), 0o644); err != nil {
						owner.err = errors.Join(owner.err, fmt.Errorf("write Wiretap finish receipt: %w", err))
					}
				}
			}
		}
		if owner.tempDir != "" {
			owner.err = errors.Join(owner.err, os.RemoveAll(owner.tempDir))
		}
	})
	return owner.result, owner.err
}

func (owner *managedRuntime) stop() error {
	if owner.stdin != nil {
		_ = owner.stdin.Close()
	}
	if owner.cmd == nil || owner.cmd.Process == nil {
		return nil
	}
	timer := time.NewTimer(owner.options.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-owner.exited:
		if owner.exitErr != nil {
			return fmt.Errorf("Wiretap process exited unsuccessfully: %w", owner.exitErr)
		}
		return nil
	case <-timer.C:
	}
	if err := owner.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill Wiretap process after shutdown timeout: %w", err)
	}
	timer.Reset(owner.options.ShutdownTimeout)
	select {
	case <-owner.exited:
		return errors.New("Wiretap process exceeded its shutdown timeout and was killed")
	case <-timer.C:
		return errors.New("Wiretap process did not exit after being killed")
	}
}
