package wiretap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestManagedReadinessWaitsForWindowsSharingViolation(t *testing.T) {
	t.Run("deadline while locked", func(t *testing.T) {
		owner, path, _ := lockedManagedReadiness(t)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := owner.waitReady(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("locked readiness must wait until the startup deadline: %v", err)
		}
	})

	t.Run("ready after release", func(t *testing.T) {
		owner, path, lock := lockedManagedReadiness(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		type outcome struct {
			url string
			err error
		}
		result := make(chan outcome, 1)
		go func() {
			url, err := owner.waitReady(ctx, path)
			result <- outcome{url, err}
		}()
		select {
		case got := <-result:
			t.Fatalf("readiness returned while the file was locked: %+v", got)
		case <-time.After(100 * time.Millisecond):
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-result:
			if got.err != nil || got.url != "http://127.0.0.1:18765" {
				t.Fatalf("readiness did not recover after the lock was released: %+v", got)
			}
		case <-ctx.Done():
			t.Fatal("readiness did not return after the lock was released")
		}
	})
}

func lockedManagedReadiness(t *testing.T) (*managedRuntime, string, *os.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ready.json")
	data, err := json.Marshal(map[string]any{"control_url": "http://127.0.0.1:18765", "pid": os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	lock := os.NewFile(uintptr(handle), path)
	t.Cleanup(func() { _ = lock.Close() })
	// Check the real OS lock before testing polling; this is not a synthetic
	// error returned by a mocked readiness reader.
	if _, err := managedReadiness(path); !errors.Is(err, syscall.Errno(32)) {
		t.Fatalf("exclusive handle did not produce a sharing violation: %v", err)
	}
	owner := &managedRuntime{
		cmd:    &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
		exited: make(chan struct{}),
	}
	return owner, path, lock
}
