// Package hooks invokes project-owned lifecycle commands using a small JSON
// protocol. Commands are never loaded from capture bundles.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const Version = 1

type Command struct {
	Command []string `yaml:"command" json:"command"`
	Timeout string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type Lifecycle struct {
	BeforeRun     *Command `yaml:"before_run"`
	BeforeCase    *Command `yaml:"before_case"`
	BeforeRequest *Command `yaml:"before_request"`
	CheckCase     *Command `yaml:"check_case"`
	AfterCase     *Command `yaml:"after_case"`
	AfterRun      *Command `yaml:"after_run"`
}

func (h Lifecycle) Commands() map[string]*Command {
	return map[string]*Command{"before_run": h.BeforeRun, "before_case": h.BeforeCase,
		"before_request": h.BeforeRequest, "check_case": h.CheckCase,
		"after_case": h.AfterCase, "after_run": h.AfterRun}
}

func (h Lifecycle) Validate() error {
	for event, cmd := range h.Commands() {
		if cmd == nil {
			continue
		}
		if len(cmd.Command) == 0 || cmd.Command[0] == "" {
			return fmt.Errorf("%s: command is required", event)
		}
		if _, err := cmd.duration(); err != nil {
			return fmt.Errorf("%s: %w", event, err)
		}
	}
	return nil
}

func (c Command) duration() (time.Duration, error) {
	if c.Timeout == "" {
		return 30 * time.Second, nil
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("timeout must be a positive duration")
	}
	return d, nil
}

type Check struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Output contains runtime-only context, plus explicit assertion results. The
// caller decides which check evidence to persist; bindings/state are not logs.
type Output struct {
	Bindings map[string]any    `json:"bindings,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	State    map[string]any    `json:"state,omitempty"`
	Checks   []Check           `json:"checks,omitempty"`
}

// Call starts a fresh process without a shell. stdin is one JSON document;
// stdout is optional JSON; stderr is forwarded to the supplied log stream.
func Call(ctx context.Context, command *Command, dir string, input any, logs io.Writer) (Output, error) {
	var out Output
	if command == nil {
		return out, nil
	}
	if len(command.Command) == 0 || command.Command[0] == "" {
		return out, fmt.Errorf("hook command is empty")
	}
	d, err := command.duration()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	raw, err := json.Marshal(input)
	if err != nil {
		return out, err
	}
	cmd := exec.CommandContext(ctx, command.Command[0], command.Command[1:]...)
	cmd.Dir, cmd.Stdin, cmd.Stderr = dir, bytes.NewReader(raw), logs
	cmd.WaitDelay = 250 * time.Millisecond
	buf := &limitedBuffer{limit: 1024 * 1024}
	cmd.Stdout = buf
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return out, fmt.Errorf("hook interrupted: %w", ctx.Err())
		}
		return out, fmt.Errorf("hook command failed: %w", err)
	}
	if buf.overflow {
		return out, fmt.Errorf("hook output exceeds 1 MiB")
	}
	if len(bytes.TrimSpace(buf.Bytes())) == 0 {
		return out, nil
	}
	if bytes.TrimSpace(buf.Bytes())[0] != '{' {
		return out, fmt.Errorf("hook output must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("invalid hook output: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return out, fmt.Errorf("hook output must contain one JSON document")
	}
	seen := map[string]bool{}
	for _, check := range out.Checks {
		if check.Name == "" || seen[check.Name] {
			return out, fmt.Errorf("hook checks require unique, nonempty names")
		}
		seen[check.Name] = true
	}
	return out, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.Len()
	if n > left {
		b.overflow = true
		p = p[:left]
	}
	b.Buffer.Write(p)
	return n, nil
}
