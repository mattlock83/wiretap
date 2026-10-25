package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/core"
	"github.com/mattlock83/wiretap/internal/fileuri"
	"github.com/mattlock83/wiretap/internal/grpccontract"
	"github.com/mattlock83/wiretap/internal/project"
	"github.com/mattlock83/wiretap/internal/schemajson"
)

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type doctorReport struct {
	ReportVersion string        `json:"report_version"`
	OK            bool          `json:"ok"`
	ConfigPath    string        `json:"config_path,omitempty"`
	Checks        []doctorCheck `json:"checks"`
}

func (report *doctorReport) add(name, message string, err error) {
	status := "ok"
	if err != nil {
		status, message = "error", err.Error()
		report.OK = false
	}
	report.Checks = append(report.Checks, doctorCheck{name, status, message})
}

func doctorCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("wiretap doctor", flag.ContinueOnError)
	configPath := flags.String("config", "", "project configuration (default: discover .wiretap.yaml)")
	format := flags.String("format", "text", "diagnostic format: text or json")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: wiretap doctor [--config path] [--format text|json]\nCheck local setup without starting capture, contacting servers or writing files.")
		flags.PrintDefaults()
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*format != "text" && *format != "json") {
		return fmt.Errorf("%w: doctor accepts --config and --format text|json", errUsage)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	report := inspectSetup(ctx, *configPath)
	if *format == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else {
		for _, check := range report.Checks {
			if _, err := fmt.Fprintf(os.Stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Message); err != nil {
				return err
			}
		}
	}
	if !report.OK {
		return errors.New("Wiretap setup checks failed; fix the reported errors and run doctor again")
	}
	return nil
}

func inspectSetup(ctx context.Context, path string) doctorReport {
	report := doctorReport{ReportVersion: "1.0", OK: true, Checks: []doctorCheck{}}
	configured, err := project.Load(path)
	if err == nil && configured == nil {
		err = errors.New("no .wiretap.yaml found; run wiretap init --sut-url <url> --schema <file> --application <id>, or supply --config")
	}
	if configured != nil {
		report.ConfigPath = configured.Path
	}
	report.add("config", report.ConfigPath, err)
	config := core.DefaultConfig()
	if configured != nil {
		if configured.HTTPListen != "" {
			config.HTTP.ListenAddr = configured.HTTPListen
		}
		if configured.ProxyListen != "" {
			config.Proxy.ListenAddr = configured.ProxyListen
		}
		config.Artifact.OutputDir = configured.OutputDir
		config.Proxy.CABaseDir = configured.CADir
	}
	err = applyEnv(&config)
	if err == nil {
		err = config.Validate()
	}
	report.add("runtime", fmt.Sprintf("HTTP %s; proxy %s; capture limit %d bytes per body (0 = unlimited)", config.HTTP.ListenAddr, config.Proxy.ListenAddr, config.Capture.MaxBodyBytes), err)
	_, err = bundle.ConfiguredLoadLimits()
	report.add("archive_limits", "archive loading and output inventory budgets are valid", err)
	parent, err := doctorOutputParent(config.Artifact.OutputDir)
	report.add("output", fmt.Sprintf("%s; existing parent %s has directory write/search permission bits", config.Artifact.OutputDir, parent), err)
	if err == nil && runtime.GOOS == "windows" {
		report.Checks[len(report.Checks)-1].Message = fmt.Sprintf("%s; existing parent %s is a directory; write access is checked at capture startup", config.Artifact.OutputDir, parent)
	}
	if configured != nil {
		doctorAPI(ctx, &report, "sut", configured.SUT)
		for _, id := range configured.DependencyIDs() {
			doctorAPI(ctx, &report, "dependency."+id, configured.Dependencies[id])
		}
	}
	return report
}

func doctorAPI(ctx context.Context, report *doctorReport, name string, api project.API) {
	if api.Protocol == "grpc" {
		if api.CaptureTLS.CertFile != "" {
			_, err := tls.LoadX509KeyPair(api.CaptureTLS.CertFile, api.CaptureTLS.KeyFile)
			report.add(name+".capture_tls", "capture certificate and private key parse and match", err)
		}
		if api.UpstreamTLS.CAFile != "" {
			data, err := os.ReadFile(api.UpstreamTLS.CAFile)
			if err == nil && !x509.NewCertPool().AppendCertsFromPEM(data) {
				err = fmt.Errorf("%s: upstream CA file contains no certificates", api.UpstreamTLS.CAFile)
			}
			report.add(name+".upstream_ca", "upstream CA file contains parseable certificates", err)
		}
		if api.GRPCSchema == nil || api.GRPCSchema.Source == "reflection" {
			report.Checks = append(report.Checks, doctorCheck{name + ".schema", "deferred", "gRPC reflection is resolved when capture starts; no server was contacted"})
			return
		}
		schema := api.GRPCSchema
		_, err := grpccontract.Resolve(ctx, "", grpccontract.Source{Kind: schema.Source, Path: schema.Path, Files: schema.Files, ImportPaths: schema.ImportPaths}, api.Services)
		report.add(name+".schema", "local protobuf contract and selected services resolve", err)
		return
	}
	if api.Schema == "" {
		report.Checks = append(report.Checks, doctorCheck{name + ".schema", "deferred", "OpenAPI intentionally omitted; capture has no contract coverage for this boundary"})
		return
	}
	err := doctorOpenAPI(ctx, api.Schema)
	if errors.Is(err, errDoctorRemoteReference) {
		report.Checks = append(report.Checks, doctorCheck{name + ".schema", "deferred", err.Error() + "; remote reference validation is deferred to capture startup"})
		return
	}
	report.add(name+".schema", "OpenAPI and local references validate: "+api.Schema, err)
}

var errDoctorRemoteReference = errors.New("doctor does not fetch remote contract references")

func doctorOpenAPI(ctx context.Context, path string) error {
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		if location.Scheme != "file" || (location.Host != "" && location.Host != "localhost") {
			return nil, fmt.Errorf("%w: %s", errDoctorRemoteReference, location)
		}
		name, err := fileuri.Path(location)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(name)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("contract %s must be a regular file", name)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		value, err := schemajson.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("decode contract %s: %w", name, err)
		}
		return json.Marshal(value)
	}
	uri, err := fileuri.FromPath(path)
	if err != nil {
		return err
	}
	document, err := loader.LoadFromURI(uri)
	if err != nil {
		return fmt.Errorf("load OpenAPI %s: %w", path, err)
	}
	if err := document.Validate(ctx); err != nil {
		return fmt.Errorf("validate OpenAPI %s: %w", path, err)
	}
	return nil
}

func doctorOutputParent(path string) (string, error) {
	current, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("output parent %s must be a directory", current)
			}
			if runtime.GOOS != "windows" && (info.Mode().Perm()&0222 == 0 || info.Mode().Perm()&0111 == 0) {
				return "", fmt.Errorf("output parent %s needs write and directory search permission; choose WIRETAP_OUTPUT_DIR or fix its permissions", current)
			}
			return current, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect output parent %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("output parent does not exist: %s", path)
		}
		current = parent
	}
}
