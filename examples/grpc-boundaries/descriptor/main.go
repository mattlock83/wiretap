// The fixture writes the generated health service's FileDescriptorSet without
// requiring protoc or Buf on the test machine.
package main

import (
	"log"
	"os"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: emit-descriptor output.protoset")
	}
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(set)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(os.Args[1], data, 0600); err != nil {
		log.Fatal(err)
	}
}
