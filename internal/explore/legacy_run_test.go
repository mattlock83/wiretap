package explore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
	"github.com/phrond/wiretap/internal/bundlereport"
	"github.com/phrond/wiretap/internal/coverage"
	"github.com/phrond/wiretap/internal/hooks"
)

// Legacy bundle/command adapter keeps the original generator regression suite
// runnable without publishing a second production execution interface.
type legacyConfig struct {
	Hooks            legacyHookLifecycle
	Dir              string
	ExpectedStatuses []int
	MaxCases         int
	RequestTimeout   string
	Bindings         []Binding
	Extract          []Extraction
	Redactions       []artifact.Redaction
}

func (c legacyConfig) config() Config {
	return Config{ExpectedStatuses: c.ExpectedStatuses, MaxCases: c.MaxCases, RequestTimeout: c.RequestTimeout, Bindings: c.Bindings, Extract: c.Extract, Redactions: c.Redactions}
}

type Options struct {
	Bundle, Attempt, Test, Target, Output string
	Config                                legacyConfig
	Logs                                  io.Writer
}

// Run never executes bundle content or updates the source bundle. Only the
// explicitly supplied project configuration selects executable hook commands.
func Run(ctx context.Context, opts Options) (result Result, err error) {
	cfg := opts.Config.config()
	cfg.Defaults()
	if err = cfg.Validate(); err != nil {
		return result, err
	}
	if opts.Output == "" {
		opts.Output = "artifacts"
	}
	if opts.Logs == nil {
		opts.Logs = os.Stderr
	}
	target, e := url.Parse(opts.Target)
	if e != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return result, fmt.Errorf("target must be an http(s) origin, without credentials, a base path, query or fragment")
	}
	source, captures, e := bundle.Load(opts.Bundle)
	if e != nil {
		return result, e
	}
	if source.RunState != "complete" || len(source.Pending) > 0 {
		return result, fmt.Errorf("source bundle must be complete")
	}
	seed, e := selectSeed(captures, opts.Attempt, opts.Test)
	if e != nil {
		return result, e
	}
	for _, flow := range seed.Traffic {
		if flow.Interaction != nil {
			return result, fmt.Errorf("exploration does not support event interaction %q (%s %s); replay would omit its recorded events, including any dependency interactions", flow.ID, flow.Protocol, flow.Interaction.Kind)
		}
		if flow.Channel == "test_to_sut" && flow.Protocol == "grpc" {
			return result, fmt.Errorf("gRPC provider exploration is not supported; select a test with only HTTP provider requests")
		}
	}
	cfg.Redactions = append(append([]artifact.Redaction(nil), seed.Redactions...), cfg.Redactions...)
	var ref artifact.SchemaRef
	for _, r := range seed.Schemas {
		if r.Role == "sut" {
			if ref.ID != "" {
				return result, fmt.Errorf("exploration requires one provider schema per seed attempt")
			}
			ref = r
		}
	}
	if ref.Type != "openapi" || ref.Source.Type != "file" {
		return result, fmt.Errorf("seed requires a supplied provider OpenAPI schema")
	}
	revision, e := coverage.ReplayContractRevision(ref)
	if e != nil {
		return result, e
	}
	s, e := loadSpec(ref)
	if e != nil {
		return result, e
	}
	steps, e := seedSteps(seed)
	if e != nil {
		return result, e
	}
	mutations, notes, e := generate(s, steps, source.APIID, revision, cfg.ExpectedStatuses)
	if e != nil {
		return result, e
	}
	if len(mutations) == 0 {
		return result, fmt.Errorf("no supported isolated invalid-input cases can be generated from this seed")
	}
	for _, b := range cfg.Bindings {
		if b.FlowID != "" && !hasStep(steps, b.FlowID) {
			return result, fmt.Errorf("binding refers to unknown provider flow %q", b.FlowID)
		}
	}
	for _, x := range cfg.Extract {
		if !hasStep(steps, x.FlowID) {
			return result, fmt.Errorf("extraction refers to unknown provider flow %q", x.FlowID)
		}
	}
	w, e := bundle.New(opts.Output)
	if e != nil {
		return result, e
	}
	result.Bundle = w.Path()
	result.Generated = len(mutations)
	provenance := artifact.Exploration{Version: Version, SourceRunID: source.RunID, SourceAttemptID: seed.TestCase.ID}
	for _, r := range source.Attempts {
		if r.AttemptID == seed.TestCase.ID {
			provenance.SourceSHA256 = r.SHA256
		}
	}
	if e = w.Configure(bundle.Identity{APIID: source.APIID, SUTTargetKind: "service", TestSource: map[string]any{"generator": Version, "source_run_id": source.RunID, "source_attempt_id": seed.TestCase.ID, "source_sha256": provenance.SourceSHA256}}); e != nil {
		return result, e
	}
	// Always finish a readable output, including failed setup and interrupted runs.
	defer func() {
		state := "complete"
		if err != nil {
			state = "interrupted"
		}
		err = errors.Join(err, w.Finalize(state))
		_, viewErr := bundlereport.Rebuild(w.Path())
		err = errors.Join(err, viewErr)
	}()
	base := hookInput{Version: hooks.Version, RunID: w.Snapshot().RunID, Scenario: seed.TestCase.Name, Bindings: map[string]any{}, State: map[string]any{}, Headers: map[string]string{}}
	call := func(callCtx context.Context, event string, in *hookInput) (hooks.Output, error) {
		in.Event = event
		output, err := callLegacyHook(callCtx, opts.Config.Hooks.Commands()[event], opts.Config.Dir, *in, opts.Logs)
		if err == nil && event == "check_case" && opts.Config.Hooks.CheckCase != nil && len(output.Checks) == 0 {
			err = fmt.Errorf("check_case must return at least one named check")
		}
		return output, err
	}
	defer func() {
		clean := context.WithoutCancel(ctx)
		base.Outcome = "pass"
		if err != nil || result.Failed > 0 {
			base.Outcome = "fail"
		}
		if err != nil {
			base.Error = err.Error()
		}
		_, cleanupErr := call(clean, "after_run", &base)
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("after_run: %w", cleanupErr))
		}
	}()
	initial, e := call(ctx, "before_run", &base)
	if e != nil {
		return result, fmt.Errorf("before_run: %w", e)
	}
	merge(&base, initial)
	catalogue := make([]artifact.RejectionTarget, 0, len(mutations))
	for _, m := range mutations {
		catalogue = append(catalogue, m.Target)
	}
	if cfg.MaxCases > 0 && len(mutations) > cfg.MaxCases {
		notes = append(notes, fmt.Sprintf("Case budget %d leaves %d generated targets unexecuted", cfg.MaxCases, len(mutations)-cfg.MaxCases))
	}
	baseline, runErr := runCase(ctx, w, s, steps, nil, base, cfg, target, provenance, catalogue, notes, call)
	if runErr != nil {
		return result, runErr
	}
	if baseline.TestCase.Status != "pass" {
		return result, fmt.Errorf("fresh baseline failed; no mutations were executed (see output bundle)")
	}
	for i := range mutations {
		if cfg.MaxCases > 0 && i >= cfg.MaxCases {
			break
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		art, caseErr := runCase(ctx, w, s, steps, &mutations[i], base, cfg, target, provenance, nil, nil, call)
		if art != nil {
			result.Executed++
			if art.TestCase.Status == "pass" {
				result.Passed++
			} else {
				result.Failed++
			}
		}
		if caseErr != nil {
			return result, caseErr
		}
	}
	return result, nil
}

func selectSeed(captures []*artifact.Artifact, attempt, test string) (*artifact.Artifact, error) {
	var selected *artifact.Artifact
	for _, a := range captures {
		if attempt != "" && a.TestCase.ID != attempt || test != "" && a.TestCase.Name != test {
			continue
		}
		if a.Exploration != nil {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("seed selection is ambiguous; specify --attempt")
		}
		selected = a
	}
	if selected == nil {
		return nil, fmt.Errorf("no matching authored test attempt")
	}
	if selected.TestCase.Status != "pass" || selected.CaptureIncomplete {
		return nil, fmt.Errorf("seed must be a passing, complete test attempt")
	}
	return selected, nil
}

// External command support below exists only for the original regression fixtures.
type legacyHookCommand struct {
	Command []string `yaml:"command" json:"command"`
	Timeout string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type legacyHookLifecycle struct {
	BeforeRun     *legacyHookCommand `yaml:"before_run"`
	BeforeCase    *legacyHookCommand `yaml:"before_case"`
	BeforeRequest *legacyHookCommand `yaml:"before_request"`
	CheckCase     *legacyHookCommand `yaml:"check_case"`
	AfterCase     *legacyHookCommand `yaml:"after_case"`
	AfterRun      *legacyHookCommand `yaml:"after_run"`
}

func (h legacyHookLifecycle) Commands() map[string]*legacyHookCommand {
	return map[string]*legacyHookCommand{"before_run": h.BeforeRun, "before_case": h.BeforeCase,
		"before_request": h.BeforeRequest, "check_case": h.CheckCase,
		"after_case": h.AfterCase, "after_run": h.AfterRun}
}

func (h legacyHookLifecycle) Validate() error {
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

func (c legacyHookCommand) duration() (time.Duration, error) {
	if c.Timeout == "" {
		return 30 * time.Second, nil
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("timeout must be a positive duration")
	}
	return d, nil
}

// Call starts a fresh process without a shell. stdin is one JSON document;
// stdout is optional JSON; stderr is forwarded to the supplied log stream.
func callLegacyHook(ctx context.Context, command *legacyHookCommand, dir string, input any, logs io.Writer) (hooks.Output, error) {
	var out hooks.Output
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
	buf := &legacyHookBuffer{limit: 1024 * 1024}
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

type legacyHookBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *legacyHookBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.Len()
	if n > left {
		b.overflow = true
		p = p[:left]
	}
	b.Buffer.Write(p)
	return n, nil
}
