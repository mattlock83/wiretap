package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

type runtimeReadiness struct {
	ControlURL string `json:"control_url"`
	PID        int    `json:"pid"`
}

// publishReadiness exposes the bound control address to a process that owns this
// runtime. Renaming a private temporary file keeps readers from seeing partial
// JSON. The caller owns the destination directory and removes the file on exit.
func publishReadiness(path string, address net.Addr) (func() error, error) {
	if path == "" {
		return func() error { return nil }, nil
	}
	host, port, err := net.SplitHostPort(address.String())
	if err != nil {
		return nil, fmt.Errorf("resolve runtime readiness address: %w", err)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
		if ip.To4() == nil {
			host = "::1"
		}
	}
	ready := runtimeReadiness{
		ControlURL: "http://" + net.JoinHostPort(host, port),
		PID:        os.Getpid(),
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".wiretap-ready-*")
	if err != nil {
		return nil, fmt.Errorf("create runtime readiness file: %w", err)
	}
	defer os.Remove(file.Name())
	if err := json.NewEncoder(file).Encode(ready); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write runtime readiness file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close runtime readiness file: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return nil, fmt.Errorf("publish runtime readiness file: %w", err)
	}
	return func() error {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove runtime readiness file: %w", err)
		}
		return nil
	}, nil
}
