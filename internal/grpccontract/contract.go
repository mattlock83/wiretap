// Package grpccontract resolves and validates protobuf service inventories.
package grpccontract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"github.com/bufbuild/protocompile"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

const MaxDescriptorBytes = 16 << 20
const MaxDescriptorFiles = 256
const MaxResolutionTimeout = time.Minute
const NormalizationProfile = "wiretap-protobuf-1"

type Acquisition struct {
	AcquiredAt           time.Time
	ReflectionVersion    string
	AdvertisedServices   []string
	ExcludedServices     []string
	CompilerVersion      string
	NormalizationProfile string
}

type Source struct {
	Kind        string
	Path        string
	Files       []string
	ImportPaths []string
	Timeout     time.Duration
	Headers     http.Header
	TLSConfig   *tls.Config
}

type Contract struct {
	Bytes       []byte
	SHA256      string
	Source      string
	Services    []string
	Methods     map[string]protoreflect.MethodDescriptor
	Files       *protoregistry.Files
	Acquisition Acquisition
}

var excluded = map[string]bool{"grpc.health.v1.Health": true, "grpc.reflection.v1.ServerReflection": true, "grpc.reflection.v1alpha.ServerReflection": true}

func Resolve(ctx context.Context, target string, src Source, selected []string) (*Contract, error) {
	if src.Kind == "" {
		src.Kind = "reflection"
	}
	var set *descriptorpb.FileDescriptorSet
	var advertised []string
	var original []byte
	acquisition := Acquisition{NormalizationProfile: NormalizationProfile}
	var err error
	switch src.Kind {
	case "reflection":
		set, advertised, err = reflectSet(ctx, target, src, selected, &acquisition)
	case "descriptor_set":
		original, err = readBoundedFile(src.Path, MaxDescriptorBytes)
		if err == nil {
			set = &descriptorpb.FileDescriptorSet{}
			err = proto.Unmarshal(original, set)
		}
	case "proto":
		set, err = compileSet(ctx, src)
		acquisition.CompilerVersion = "protocompile/v0.14.1"
	default:
		return nil, fmt.Errorf("unsupported gRPC contract source %q", src.Kind)
	}
	if err != nil {
		return nil, err
	}
	if len(set.File) == 0 {
		return nil, errors.New("protobuf contract contains no files")
	}
	if len(set.File) > MaxDescriptorFiles {
		return nil, errors.New("protobuf contract exceeds 256 files")
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("link protobuf descriptors: %w", err)
	}
	if len(selected) == 0 {
		if src.Kind != "reflection" {
			return nil, errors.New("local protobuf contract requires explicit services")
		}
		for _, name := range advertised {
			if !excluded[name] {
				selected = append(selected, name)
			}
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("no gRPC services selected")
	}
	services := append([]string(nil), selected...)
	sort.Strings(services)
	methods := map[string]protoreflect.MethodDescriptor{}
	for i, name := range services {
		if i > 0 && name == services[i-1] {
			return nil, fmt.Errorf("duplicate service %q", name)
		}
		desc, e := files.FindDescriptorByName(protoreflect.FullName(name))
		if e != nil {
			return nil, fmt.Errorf("selected service %q is absent from contract: %w", name, e)
		}
		svc, ok := desc.(protoreflect.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("%q is not a service", name)
		}
		for j := 0; j < svc.Methods().Len(); j++ {
			m := svc.Methods().Get(j)
			methods["/"+name+"/"+string(m.Name())] = m
		}
	}
	// The set is canonicalized by logical filename, without changing dependency indices.
	sort.Slice(set.File, func(i, j int) bool { return set.File[i].GetName() < set.File[j].GetName() })
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(set)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxDescriptorBytes {
		return nil, errors.New("descriptor set exceeds 16 MiB")
	}
	if original != nil {
		// Integrity identifies the supplied artifact, not its normalized identity.
		encoded = original
	}
	sum := sha256.Sum256(encoded)
	acquisition.AcquiredAt = time.Now().UTC()
	return &Contract{Bytes: encoded, SHA256: hex.EncodeToString(sum[:]), Source: src.Kind, Services: services, Methods: methods, Files: files, Acquisition: acquisition}, nil
}

func Parse(data []byte, services []string) (*Contract, error) {
	if len(data) > MaxDescriptorBytes {
		return nil, errors.New("descriptor set exceeds 16 MiB")
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, set); err != nil {
		return nil, err
	}
	if len(set.File) == 0 || len(set.File) > MaxDescriptorFiles {
		return nil, errors.New("protobuf contract requires 1–256 files")
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, err
	}
	methods := map[string]protoreflect.MethodDescriptor{}
	for _, name := range services {
		d, err := files.FindDescriptorByName(protoreflect.FullName(name))
		if err != nil {
			return nil, err
		}
		svc, ok := d.(protoreflect.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("%s is not a service", name)
		}
		for i := 0; i < svc.Methods().Len(); i++ {
			m := svc.Methods().Get(i)
			methods["/"+name+"/"+string(m.Name())] = m
		}
	}
	sum := sha256.Sum256(data)
	return &Contract{Bytes: data, SHA256: hex.EncodeToString(sum[:]), Services: services, Methods: methods, Files: files}, nil
}

// SemanticRevision uses the complete frozen descriptor graph and selected
// services, excluding source comments/locations and descriptor-set ordering.
// Unrelated declarations intentionally remain part of this conservative profile.
func SemanticRevision(data []byte, services []string) (string, error) {
	_, err := Parse(data, services)
	if err != nil {
		return "", err
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, set); err != nil {
		return "", err
	}
	for _, file := range set.File {
		file.SourceCodeInfo = nil
	}
	sort.Slice(set.File, func(i, j int) bool { return set.File[i].GetName() < set.File[j].GetName() })
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(set)
	if err != nil {
		return "", err
	}
	sortedServices := append([]string(nil), services...)
	sort.Strings(sortedServices)
	unique := sortedServices[:0]
	for _, service := range sortedServices {
		if len(unique) == 0 || unique[len(unique)-1] != service {
			unique = append(unique, service)
		}
	}
	identity := append([]byte(NormalizationProfile+"\x00"), encoded...)
	identity = append(identity, []byte("\x00"+strings.Join(unique, "\x00"))...)
	sum := sha256.Sum256(identity)
	return hex.EncodeToString(sum[:]), nil
}

func compileSet(ctx context.Context, src Source) (*descriptorpb.FileDescriptorSet, error) {
	if len(src.Files) == 0 || len(src.Files) > 256 {
		return nil, errors.New("proto source requires 1–256 files")
	}
	for _, name := range src.Files {
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(filepath.Clean(name), ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("proto file escapes import roots: %q", name)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, MaxResolutionTimeout)
	defer cancel()
	var sourceMu sync.Mutex
	sourceBytes := 0
	loaded := map[string][]byte{}
	resolver := protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: src.ImportPaths, Accessor: func(path string) (io.ReadCloser, error) {
		realPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		allowed := false
		for _, root := range src.ImportPaths {
			realRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(realRoot, realPath)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("proto import escapes configured roots: %q", path)
		}
		sourceMu.Lock()
		defer sourceMu.Unlock()
		if data, ok := loaded[realPath]; ok {
			return io.NopCloser(bytes.NewReader(data)), nil
		}
		if len(loaded) >= MaxDescriptorFiles {
			return nil, errors.New("proto source exceeds 256 files")
		}
		data, err := readBoundedFile(realPath, 4<<20)
		if err != nil {
			return nil, err
		}
		sourceBytes += len(data)
		if sourceBytes > MaxDescriptorBytes {
			return nil, errors.New("proto sources exceed 16 MiB in total")
		}
		loaded[realPath] = data
		return io.NopCloser(bytes.NewReader(data)), nil
	}})
	compiler := protocompile.Compiler{Resolver: resolver}
	linked, err := compiler.Compile(ctx, src.Files...)
	if err != nil {
		return nil, fmt.Errorf("compile proto sources: %w", err)
	}
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var visit func(protoreflect.FileDescriptor)
	visit = func(file protoreflect.FileDescriptor) {
		if seen[file.Path()] {
			return
		}
		seen[file.Path()] = true
		for i := 0; i < file.Imports().Len(); i++ {
			visit(file.Imports().Get(i).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(file))
	}
	for _, file := range linked {
		visit(file)
	}
	return set, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("contract file must be a regular file of at most %d bytes", limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(data) > int(limit) {
		return nil, fmt.Errorf("contract file exceeds %d bytes", limit)
	}
	return data, err
}

func reflectSet(ctx context.Context, target string, src Source, selected []string, acquisition *Acquisition) (*descriptorpb.FileDescriptorSet, []string, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, nil, fmt.Errorf("gRPC target must be an absolute http or https URL: %q", target)
	}
	if src.Timeout <= 0 {
		src.Timeout = 10 * time.Second
	}
	if src.Timeout > MaxResolutionTimeout {
		return nil, nil, errors.New("reflection timeout must not exceed one minute")
	}
	ctx, cancel := context.WithTimeout(ctx, src.Timeout)
	defer cancel()
	var transport *http2.Transport
	if u.Scheme == "http" {
		transport = &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{Timeout: src.Timeout}).DialContext(ctx, network, addr)
		}}
	} else {
		transport = &http2.Transport{TLSClientConfig: src.TLSConfig}
	}
	defer transport.CloseIdleConnections()
	client := grpcreflect.NewClient(&http.Client{Transport: transport}, strings.TrimRight(target, "/"), connect.WithGRPC(), connect.WithReadMaxBytes(MaxDescriptorBytes))
	stream := client.NewStream(ctx, grpcreflect.WithRequestHeaders(src.Headers))
	defer stream.Close()
	listed, err := stream.ListServices()
	if err != nil {
		return nil, nil, fmt.Errorf("gRPC reflection list services: %w", err)
	}
	acquisition.ReflectionVersion = "v1"
	if strings.Contains(stream.Spec().Procedure, ".v1alpha.") {
		acquisition.ReflectionVersion = "v1alpha"
	}
	advertised := make([]string, 0, len(listed))
	has := map[string]bool{}
	for _, name := range listed {
		advertised = append(advertised, string(name))
		has[string(name)] = true
	}
	sort.Strings(advertised)
	acquisition.AdvertisedServices = append([]string(nil), advertised...)
	if len(selected) == 0 {
		for _, name := range advertised {
			if !excluded[name] {
				selected = append(selected, name)
			} else {
				acquisition.ExcludedServices = append(acquisition.ExcludedServices, name)
			}
		}
	}
	if len(selected) > MaxDescriptorFiles {
		return nil, nil, errors.New("reflection selection exceeds 256 services")
	}
	set := &descriptorpb.FileDescriptorSet{}
	files := map[string]*descriptorpb.FileDescriptorProto{}
	totalBytes := 0
	add := func(items []*descriptorpb.FileDescriptorProto) error {
		for _, f := range items {
			if f != nil && f.GetName() != "" {
				if previous := files[f.GetName()]; previous != nil {
					if !proto.Equal(previous, f) {
						return fmt.Errorf("reflection returned conflicting definitions for %q", f.GetName())
					}
					continue
				}
				totalBytes += proto.Size(f)
				if len(files) >= MaxDescriptorFiles || totalBytes > MaxDescriptorBytes {
					return errors.New("reflection contract exceeds 256 files or 16 MiB")
				}
				files[f.GetName()] = f
			}
		}
		return nil
	}
	for _, name := range selected {
		if !has[name] {
			return nil, nil, fmt.Errorf("selected service %q was not advertised by reflection", name)
		}
		got, e := stream.FileContainingSymbol(protoreflect.FullName(name))
		if e != nil {
			return nil, nil, fmt.Errorf("reflect service %q: %w", name, e)
		}
		if err := add(got); err != nil {
			return nil, nil, err
		}
	}
	for {
		missing := ""
		for _, f := range files {
			for _, dep := range f.Dependency {
				if files[dep] == nil {
					missing = dep
					break
				}
			}
			if missing != "" {
				break
			}
		}
		if missing == "" {
			break
		}
		if len(files) >= 256 {
			return nil, nil, errors.New("reflection returned too many descriptor files")
		}
		got, e := stream.FileByFilename(missing)
		if e != nil {
			return nil, nil, fmt.Errorf("reflect import %q: %w", missing, e)
		}
		before := len(files)
		if err := add(got); err != nil {
			return nil, nil, err
		}
		if len(files) == before {
			return nil, nil, fmt.Errorf("reflection omitted import %q", missing)
		}
	}
	for _, f := range files {
		set.File = append(set.File, f)
	}
	return set, advertised, nil
}
