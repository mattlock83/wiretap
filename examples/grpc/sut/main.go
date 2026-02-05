package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phrond/evermock/examples/grpc/examplepb"
	tlsmgr "github.com/phrond/evermock/internal/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/dynamicpb"
)

type serviceServer struct {
	dependencyAddr string
}

func (s *serviceServer) Process(ctx context.Context, req *dynamicpb.Message) (*dynamicpb.Message, error) {
	id := examplepb.GetString(req, "id")
	action := examplepb.GetString(req, "action")

	depConn, err := dialDependency(ctx, s.dependencyAddr)
	if err != nil {
		return nil, err
	}
	defer depConn.Close()

	depClient := examplepb.NewDependencyClient(depConn)
	depReq := examplepb.NewDependencyRequest(id, action)

	alphaResp, err := depClient.Alpha(ctx, depReq)
	if err != nil {
		return nil, err
	}
	betaResp, err := depClient.Beta(ctx, depReq)
	if err != nil {
		return nil, err
	}

	alphaValue := examplepb.GetString(alphaResp, "value")
	betaValue := examplepb.GetString(betaResp, "value")
	result := fmt.Sprintf("alpha:%s|beta:%s", alphaValue, betaValue)

	return examplepb.NewProcessReply(result), nil
}

func main() {
	listen := getenv("SUT_LISTEN", "127.0.0.1:18095")
	dependency := getenv("DEPENDENCY_ADDR", "127.0.0.1:19191")

	mgr := tlsmgr.NewManager(filepath.Join(os.TempDir(), "phrond", "grpc-sut"))
	cert, err := mgr.GetOrCreateCert(hostFromAddr(listen))
	if err != nil {
		log.Fatalf("cert error: %v", err)
	}
	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	examplepb.RegisterServiceServer(grpcServer, &serviceServer{dependencyAddr: dependency})

	log.Printf("grpc SUT listening on %s", ln.Addr().String())
	if err := grpcServer.Serve(ln); err != nil {
		log.Fatalf("grpc serve error: %v", err)
	}
}

func dialDependency(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	clientTLS := &tls.Config{InsecureSkipVerify: true}
	creds := credentials.NewTLS(clientTLS)
	return grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(creds), grpc.WithBlock())
}

func hostFromAddr(addr string) string {
	if addr == "" {
		return "127.0.0.1"
	}
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1"
	}
	if host == "" || host == "0.0.0.0" {
		return "127.0.0.1"
	}
	return host
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
