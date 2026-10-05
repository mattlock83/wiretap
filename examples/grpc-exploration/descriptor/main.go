package main

import (
	"context"
	"github.com/phrond/wiretap/examples/grpc-exploration/fixture"
	"google.golang.org/protobuf/proto"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: emit-descriptor output.protoset")
	}
	contract, err := fixture.Compile(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(contract.DescriptorSet)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(os.Args[1], data, 0600); err != nil {
		log.Fatal(err)
	}
}
