package grpccontract

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	v1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	v1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestLocalProtoAndDescriptorSetDoNotNeedReflection(t *testing.T) {
	root := t.TempDir()
	protoPath := filepath.Join(root, "inventory.proto")
	if err := os.WriteFile(protoPath, []byte(`syntax = "proto3";
package demo.v1;
import "common.proto";
service Inventory { rpc Check (CheckRequest) returns (CheckResponse); }
message CheckRequest { demo.common.Sku sku = 1; }
message CheckResponse { bool available = 1; }
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "common.proto"), []byte(`syntax = "proto3";
package demo.common;
message Sku { string value = 1; }
`), 0600); err != nil {
		t.Fatal(err)
	}
	compiled, err := Resolve(t.Context(), "http://127.0.0.1:1", Source{Kind: "proto", Files: []string{"inventory.proto"}, ImportPaths: []string{root}}, []string{"demo.v1.Inventory"})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Methods["/demo.v1.Inventory/Check"] == nil || compiled.SHA256 == "" {
		t.Fatalf("incomplete compiled contract: %#v", compiled)
	}
	if compiled.Acquisition.CompilerVersion != "protocompile/v0.14.1" || compiled.Acquisition.AcquiredAt.IsZero() || compiled.Acquisition.NormalizationProfile != NormalizationProfile {
		t.Fatalf("missing source acquisition provenance: %#v", compiled.Acquisition)
	}
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}}
	data, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "health.protoset")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	local, err := Resolve(context.Background(), "http://127.0.0.1:1", Source{Kind: "descriptor_set", Path: path}, []string{"grpc.health.v1.Health"})
	if err != nil {
		t.Fatal(err)
	}
	if len(local.Methods) != 3 {
		t.Fatalf("methods = %d", len(local.Methods))
	}
	revision, err := SemanticRevision(data, []string{"grpc.health.v1.Health"})
	if err != nil {
		t.Fatal(err)
	}
	variant := proto.Clone(set).(*descriptorpb.FileDescriptorSet)
	variant.File[0].SourceCodeInfo = &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{{Path: []int32{6, 0}, Span: []int32{0, 0, 0}, LeadingComments: proto.String("unrelated source comment")}}}
	variantData, err := proto.Marshal(variant)
	if err != nil {
		t.Fatal(err)
	}
	variantRevision, err := SemanticRevision(variantData, []string{"grpc.health.v1.Health"})
	if err != nil || variantRevision != revision {
		t.Fatalf("semantic revisions differ for equivalent selected service: %q, %q (%v)", revision, variantRevision, err)
	}
	variant.File = append(variant.File, &descriptorpb.FileDescriptorProto{Name: proto.String("unrelated.proto"), Syntax: proto.String("proto3")})
	variantData, err = proto.Marshal(variant)
	if err != nil {
		t.Fatal(err)
	}
	variantRevision, err = SemanticRevision(variantData, []string{"grpc.health.v1.Health"})
	if err != nil || variantRevision == revision {
		t.Fatalf("complete descriptor graph must affect revision: %q, %q (%v)", revision, variantRevision, err)
	}
	// Reverse file order to ensure the integrity artifact stays byte-for-byte
	// supplied even though normalization sorts the separate semantic identity.
	variant.File[0], variant.File[1] = variant.File[1], variant.File[0]
	original, _ := proto.Marshal(variant)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	local, err = Resolve(t.Context(), "", Source{Kind: "descriptor_set", Path: path}, []string{"grpc.health.v1.Health"})
	if err != nil || !bytes.Equal(local.Bytes, original) {
		t.Fatalf("supplied descriptor bytes changed: %v", err)
	}
	if _, err := Resolve(t.Context(), "http://127.0.0.1:1", Source{Kind: "descriptor_set", Path: path}, nil); err == nil {
		t.Fatal("local descriptor source allowed unspecified service scope")
	}
}

func TestProtoSourceRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.proto")
	if err := os.WriteFile(outside, []byte(`syntax = "proto3"; package secret; message Secret {}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "secret.proto")); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(t.Context(), "", Source{Kind: "proto", Files: []string{"secret.proto"}, ImportPaths: []string{root}}, []string{"secret.Service"})
	if err == nil || !strings.Contains(err.Error(), "escapes configured roots") {
		t.Fatalf("symlink escaped import roots: %v", err)
	}
}

type testReflectionServer struct {
	v1.UnimplementedServerReflectionServer
	services []string
	files    func(string) []*descriptorpb.FileDescriptorProto
}

func (s *testReflectionServer) ServerReflectionInfo(stream v1.ServerReflection_ServerReflectionInfoServer) error {
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		response := &v1.ServerReflectionResponse{OriginalRequest: request}
		if _, ok := request.MessageRequest.(*v1.ServerReflectionRequest_ListServices); ok {
			list := &v1.ListServiceResponse{}
			for _, name := range s.services {
				list.Service = append(list.Service, &v1.ServiceResponse{Name: name})
			}
			response.MessageResponse = &v1.ServerReflectionResponse_ListServicesResponse{ListServicesResponse: list}
		} else {
			files := &v1.FileDescriptorResponse{}
			for _, file := range s.files(request.GetFileContainingSymbol()) {
				data, err := proto.Marshal(file)
				if err != nil {
					return err
				}
				files.FileDescriptorProto = append(files.FileDescriptorProto, data)
			}
			response.MessageResponse = &v1.ServerReflectionResponse_FileDescriptorResponse{FileDescriptorResponse: files}
		}
		if err := stream.Send(response); err != nil {
			return err
		}
	}
}

func TestReflectionSelectionAndBounds(t *testing.T) {
	makeFile := func(pkg, service string) *descriptorpb.FileDescriptorProto {
		return &descriptorpb.FileDescriptorProto{Name: proto.String("service.proto"), Package: proto.String(pkg), Syntax: proto.String("proto3"), Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String(service)}}}
	}
	for _, tc := range []struct {
		name     string
		services []string
		files    func(string) []*descriptorpb.FileDescriptorProto
		want     string
	}{
		{"excluded services", []string{"demo.Service", "grpc.health.v1.Health", "grpc.reflection.v1.ServerReflection"}, func(symbol string) []*descriptorpb.FileDescriptorProto {
			if symbol != "demo.Service" {
				return nil // These excluded advertised definitions cannot be resolved.
			}
			return []*descriptorpb.FileDescriptorProto{makeFile("demo", "Service")}
		}, ""},
		{"conflicting definitions", []string{"demo.One", "demo.Two"}, func(symbol string) []*descriptorpb.FileDescriptorProto {
			return []*descriptorpb.FileDescriptorProto{makeFile("demo", strings.TrimPrefix(symbol, "demo."))}
		}, "conflicting definitions"},
		{"oversized file inventory", []string{"demo.Service"}, func(string) []*descriptorpb.FileDescriptorProto {
			files := []*descriptorpb.FileDescriptorProto{makeFile("demo", "Service")}
			for i := 0; i < MaxDescriptorFiles; i++ {
				files = append(files, &descriptorpb.FileDescriptorProto{Name: proto.String(fmt.Sprintf("extra%d.proto", i)), Syntax: proto.String("proto3")})
			}
			return files
		}, "exceeds 256 files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			v1.RegisterServerReflectionServer(server, &testReflectionServer{services: tc.services, files: tc.files})
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			contract, err := Resolve(t.Context(), "http://"+listener.Addr().String(), Source{}, nil)
			if tc.want == "" {
				if err != nil || len(contract.Services) != 1 || contract.Services[0] != "demo.Service" {
					t.Fatalf("filtered inventory: %#v (%v)", contract, err)
				}
				if len(contract.Acquisition.AdvertisedServices) != 3 || len(contract.Acquisition.ExcludedServices) != 2 || contract.Acquisition.ReflectionVersion != "v1" {
					t.Fatalf("missing reflection acquisition provenance: %#v", contract.Acquisition)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestReflectionFallsBackToV1Alpha(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	v1alpha.RegisterServerReflectionServer(server, reflection.NewServer(reflection.ServerOptions{Services: server}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	contract, err := Resolve(t.Context(), "http://"+listener.Addr().String(), Source{}, []string{"grpc.health.v1.Health"})
	if err != nil {
		t.Fatal(err)
	}
	if contract.Source != "reflection" || len(contract.Methods) != 3 || contract.Acquisition.ReflectionVersion != "v1alpha" {
		t.Fatalf("v1alpha reflection contract: %#v", contract)
	}
}
