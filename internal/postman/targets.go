package postman

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phrond/wiretap/internal/grpccontract"
)

func applyTargets(exports []Export, targets map[string]GRPCTarget, recordedBoundaries map[string]bool) error {
	// Match against the original recorded boundaries, before omission. An entire
	// gRPC boundary can be unrepresentable while healthy HTTP exports remain.
	// Unknown keys still fail instead of being mistaken for omitted boundaries.
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !recordedBoundaries[key] {
			return fmt.Errorf("gRPC target %q does not match a recorded export-eligible boundary; use sut or dependency:<id>", key)
		}
		target := targets[key]
		if _, _, err := grpcEndpoint(target.URL); err != nil {
			return fmt.Errorf("gRPC target %q: %w", key, err)
		}
		if (target.ProtoRoot == "") != (target.ProtoFile == "") {
			return fmt.Errorf("gRPC target %q needs both proto_root and proto_file", key)
		}
		if target.ProtoFile != "" && (!safeRelative(target.ProtoFile) || !strings.HasSuffix(target.ProtoFile, ".proto")) {
			return fmt.Errorf("gRPC target %q: proto_file must be a package-relative .proto path", key)
		}
	}
	for i := range exports {
		export := &exports[i]
		if export.GRPC == nil {
			continue
		}
		key := export.BoundaryID
		if key == "" {
			key = "sut"
		} else {
			key = "dependency:" + key
		}
		target, ok := targets[key]
		if !ok {
			continue
		}
		endpoint, secure, err := grpcEndpoint(target.URL)
		if err != nil {
			return fmt.Errorf("gRPC target %q: %w", key, err)
		}
		var schema *GRPCSchema
		if (target.ProtoRoot == "") != (target.ProtoFile == "") {
			return fmt.Errorf("gRPC target %q needs both proto_root and proto_file", key)
		}
		if target.ProtoFile != "" {
			prefix := "schemas/" + strings.TrimSuffix(export.Filename, ".postman_collection.json")
			files, err := protoPackage(target.ProtoRoot, target.ProtoFile, export.GRPC, prefix)
			if err != nil {
				return fmt.Errorf("gRPC target %q: %w", key, err)
			}
			export.Files = files
			schema = &GRPCSchema{Source: "file", Location: prefix + "/" + target.ProtoFile}
		}
		for f := range export.GRPC.Items {
			for r := range export.GRPC.Items[f].Items {
				request := &export.GRPC.Items[f].Items[r]
				request.URL = endpoint
				request.Schema = schema
				if endpoint != "" {
					request.Settings = &GRPCSettings{SecureConnection: secure}
				}
				for e := range request.Examples {
					request.Examples[e].Request.URL = endpoint
				}
			}
		}
	}
	return nil
}

func grpcEndpoint(value string) (string, bool, error) {
	if value == "" {
		return "", false, nil
	}
	raw := value
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, err
	}
	if u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false, errors.New("url must contain only server host and optional port")
	}
	switch u.Scheme {
	case "http", "grpc":
		return u.Host, false, nil
	case "https", "grpcs":
		return u.Host, true, nil
	default:
		return "", false, errors.New("url scheme must be http, https, grpc or grpcs")
	}
}

func protoPackage(root, entry string, collection *GRPCCollection, prefix string) ([]CompanionFile, error) {
	if !safeRelative(entry) || !strings.HasSuffix(entry, ".proto") {
		return nil, errors.New("proto_file must be a package-relative .proto path")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("proto_root must be a regular directory")
	}
	services := map[string]bool{}
	for _, folder := range collection.Items {
		for _, request := range folder.Items {
			services[strings.Split(request.MethodPath, "/")[0]] = true
		}
	}
	selected := make([]string, 0, len(services))
	for service := range services {
		selected = append(selected, service)
	}
	sort.Strings(selected)
	contract, err := grpccontract.Resolve(context.Background(), "", grpccontract.Source{Kind: "proto", Files: []string{entry}, ImportPaths: []string{root}}, selected)
	if err != nil {
		return nil, fmt.Errorf("compile supplied proto package: %w", err)
	}
	for _, folder := range collection.Items {
		for _, request := range folder.Items {
			if contract.Methods["/"+request.MethodPath] == nil {
				return nil, fmt.Errorf("proto package does not define %s", request.MethodPath)
			}
		}
	}
	files := []CompanionFile{}
	total := int64(0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative != "." && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".proto") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("proto source %q is not a regular file", relative)
		}
		if len(files) >= grpccontract.MaxDescriptorFiles || info.Size() > grpccontract.MaxDescriptorBytes || total+info.Size() > grpccontract.MaxDescriptorBytes {
			return errors.New("proto package exceeds 256 files or 16 MiB")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += int64(len(data))
		files = append(files, CompanionFile{Path: prefix + "/" + filepath.ToSlash(relative), Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func safeRelative(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == path && !strings.Contains(path, "\\") && path != ".." && !strings.HasPrefix(path, "../")
}
