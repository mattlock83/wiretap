package main

import (
	"context"
	"flag"
	"log"
	"net"
	"strings"

	"github.com/phrond/wiretap/examples/grpc-streaming/fixture"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9001", "native gRPC listen address")
	dependency := flag.String("dependency", "", "optional dependency capture URL")
	flag.Parse()
	contract, err := fixture.Compile(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	var before func(context.Context) error
	if *dependency != "" {
		conn, err := grpc.NewClient(strings.TrimPrefix(*dependency, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		before = func(ctx context.Context) error {
			// This is ordinary W3C propagation, not a Wiretap-specific request ID.
			incoming, _ := metadata.FromIncomingContext(ctx)
			outgoing := metadata.MD{}
			for _, name := range []string{"traceparent", "tracestate"} {
				if values := incoming.Get(name); len(values) > 0 {
					outgoing.Set(name, values...)
				}
			}
			_, err := contract.Exercise(metadata.NewOutgoingContext(ctx, outgoing), conn, "Chat", 2)
			return err
		}
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("gRPC streaming example listening on %s", listener.Addr())
	if err := contract.NewServer(before).Serve(listener); err != nil {
		log.Fatal(err)
	}
}
