// The example's existing SUT and mock use ordinary generated grpc-go clients
// and servers. Neither imports Wiretap.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

type service struct {
	healthpb.UnimplementedHealthServer
	dependency healthpb.HealthClient
}

func (s *service) Check(ctx context.Context, request *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if s.dependency == nil {
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
	}
	return s.dependency.Check(ctx, &healthpb.HealthCheckRequest{Service: "inventory"})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:50051", "gRPC listen address")
	dependency := flag.String("dependency", "", "mock capture origin for the SUT")
	reflectEnabled := flag.Bool("reflection", true, "advertise the development gRPC reflection service")
	flag.Parse()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	instance := &service{}
	if *dependency != "" {
		conn, err := grpc.NewClient(strings.TrimPrefix(*dependency, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		instance.dependency = healthpb.NewHealthClient(conn)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, instance)
	if *reflectEnabled {
		reflection.Register(server)
	}
	log.Printf("gRPC server listening on %s", ln.Addr())
	log.Fatal(server.Serve(ln))
}
