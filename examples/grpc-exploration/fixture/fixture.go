// Package fixture is an ordinary gRPC application with Protovalidate enforcement
// and mutable test fixtures. The test owns its lifecycle and invokes Wiretap
// exploration through the SDK; this package has no Wiretap dependency.
package fixture

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"sync"

	_ "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const Service = "wiretap.example.orders.v1.Orders"
const Procedure = "/" + Service + "/CreateOrder"

//go:embed orders.proto
var Source string

type Contract struct {
	Files             *protoregistry.Files
	Request, Response protoreflect.MessageDescriptor
	DescriptorSet     *descriptorpb.FileDescriptorSet
}

func Compile(ctx context.Context) (*Contract, error) {
	compiler := protocompile.Compiler{Resolver: protocompile.CompositeResolver{
		&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{"orders.proto": Source})},
		protocompile.ResolverFunc(func(path string) (protocompile.SearchResult, error) {
			file, err := protoregistry.GlobalFiles.FindFileByPath(path)
			return protocompile.SearchResult{Desc: file}, err
		}),
	}}
	compiled, err := compiler.Compile(ctx, "orders.proto")
	if err != nil {
		return nil, err
	}
	set := &descriptorpb.FileDescriptorSet{}
	visited := map[string]bool{}
	var add func(protoreflect.FileDescriptor)
	add = func(file protoreflect.FileDescriptor) {
		if visited[file.Path()] {
			return
		}
		visited[file.Path()] = true
		for i := 0; i < file.Imports().Len(); i++ {
			add(file.Imports().Get(i).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(file))
	}
	add(compiled[0])
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, err
	}
	file, err := files.FindFileByPath("orders.proto")
	if err != nil {
		return nil, err
	}
	return &Contract{Files: files, Request: file.Messages().ByName("CreateOrderRequest"), Response: file.Messages().ByName("Order"), DescriptorSet: set}, nil
}
func (c *Contract) NewRequest(user, sku string, quantity int32) *dynamicpb.Message {
	request := dynamicpb.NewMessage(c.Request)
	request.Set(c.Request.Fields().ByName("user_id"), protoreflect.ValueOfString(user))
	request.Set(c.Request.Fields().ByName("sku"), protoreflect.ValueOfString(sku))
	request.Set(c.Request.Fields().ByName("quantity"), protoreflect.ValueOfInt32(quantity))
	return request
}
func (c *Contract) NewResponse() *dynamicpb.Message { return dynamicpb.NewMessage(c.Response) }

type State struct {
	// Before allows integration tests to make an ordinary dependency request.
	Before func(context.Context) error
	// Fixtureless disables user/auth setup when testing protocol behaviour alone.
	Fixtureless bool
	mu          sync.Mutex
	serial      int
	user, token string
	rows        int
}

// Reset creates a different user and token each time, proving every generated
// case needs fixture setup and fresh authentication before it can test validation.
func (s *State) Reset() (user, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serial++
	s.user = fmt.Sprintf("user-%d", s.serial)
	s.token = fmt.Sprintf("Bearer example-token-%d", s.serial)
	s.rows = 0
	return s.user, s.token
}
func (s *State) Count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.rows }

func (c *Contract) NewServer(state *State, enableReflection bool, options ...grpc.ServerOption) (*grpc.Server, error) {
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	process := func(ctx context.Context, request proto.Message) (proto.Message, error) {
		incoming, _ := metadata.FromIncomingContext(ctx)
		state.mu.Lock()
		user, token := state.user, state.token
		state.mu.Unlock()
		if !state.Fixtureless {
			if values := incoming.Get("authorization"); len(values) != 1 || values[0] != token {
				return nil, status.Error(codes.Unauthenticated, "fresh authorization required")
			}
		}
		if err := validator.Validate(request); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		fields := request.ProtoReflect()
		if !state.Fixtureless && fields.Get(c.Request.Fields().ByName("user_id")).String() != user {
			return nil, status.Error(codes.FailedPrecondition, "fixture user does not exist")
		}
		if state.Before != nil {
			if err := state.Before(ctx); err != nil {
				return nil, err
			}
		}
		state.mu.Lock()
		state.rows++
		id := fmt.Sprintf("order-%d", state.rows)
		state.mu.Unlock()
		response := c.NewResponse()
		response.Set(c.Response.Fields().ByName("id"), protoreflect.ValueOfString(id))
		response.Set(c.Response.Fields().ByName("sku"), fields.Get(c.Request.Fields().ByName("sku")))
		response.Set(c.Response.Fields().ByName("quantity"), fields.Get(c.Request.Fields().ByName("quantity")))
		return response, nil
	}
	unary := func(_ any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := dynamicpb.NewMessage(c.Request)
		if err := decode(in); err != nil {
			return nil, err
		}
		handler := func(ctx context.Context, message any) (any, error) { return process(ctx, message.(proto.Message)) }
		if interceptor != nil {
			return interceptor(ctx, in, &grpc.UnaryServerInfo{FullMethod: Procedure}, handler)
		}
		return handler(ctx, in)
	}
	watch := func(_ any, stream grpc.ServerStream) error {
		request := dynamicpb.NewMessage(c.Request)
		if err := stream.RecvMsg(request); err != nil {
			return err
		}
		response, err := process(stream.Context(), request)
		if err != nil {
			return err
		}
		for i := int64(0); i < request.Get(c.Request.Fields().ByName("quantity")).Int(); i++ {
			if err := stream.SendMsg(response); err != nil {
				return err
			}
		}
		return nil
	}
	collect := func(_ any, stream grpc.ServerStream) error {
		var last proto.Message
		for {
			request := dynamicpb.NewMessage(c.Request)
			err := stream.RecvMsg(request)
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			last, err = process(stream.Context(), request)
			if err != nil {
				return err
			}
		}
		if last == nil {
			return status.Error(codes.InvalidArgument, "at least one order is required")
		}
		return stream.SendMsg(last)
	}
	chat := func(_ any, stream grpc.ServerStream) error {
		for {
			request := dynamicpb.NewMessage(c.Request)
			err := stream.RecvMsg(request)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			response, err := process(stream.Context(), request)
			if err != nil {
				return err
			}
			if err := stream.SendMsg(response); err != nil {
				return err
			}
		}
	}
	server := grpc.NewServer(options...)
	server.RegisterService(&grpc.ServiceDesc{ServiceName: Service, HandlerType: (*interface{})(nil), Metadata: "orders.proto", Methods: []grpc.MethodDesc{{MethodName: "CreateOrder", Handler: unary}}, Streams: []grpc.StreamDesc{
		{StreamName: "WatchOrders", Handler: watch, ServerStreams: true},
		{StreamName: "CreateOrders", Handler: collect, ClientStreams: true},
		{StreamName: "ChatOrders", Handler: chat, ClientStreams: true, ServerStreams: true},
	}}, struct{}{})
	if enableReflection {
		reflectionv1.RegisterServerReflectionServer(server, reflection.NewServerV1(reflection.ServerOptions{Services: server, DescriptorResolver: c.Files}))
	}
	return server, nil
}

func ProcedureFor(method string) string { return "/" + Service + "/" + method }
func StreamDescription(method string) *grpc.StreamDesc {
	return &grpc.StreamDesc{StreamName: method, ClientStreams: method == "CreateOrders" || method == "ChatOrders", ServerStreams: method == "WatchOrders" || method == "ChatOrders"}
}

// Exercise uses a normal grpc-go channel. Chat waits for each response before
// sending the next request, so its recorded conversation contains response gates.
func (c *Contract) Exercise(ctx context.Context, conn *grpc.ClientConn, method string, requests []*dynamicpb.Message) ([]*dynamicpb.Message, error) {
	if method == "CreateOrder" {
		if len(requests) != 1 {
			return nil, fmt.Errorf("unary request requires one message")
		}
		response := c.NewResponse()
		err := conn.Invoke(ctx, ProcedureFor(method), requests[0], response)
		return []*dynamicpb.Message{response}, err
	}
	if method != "WatchOrders" && method != "CreateOrders" && method != "ChatOrders" {
		return nil, fmt.Errorf("unknown procedure %s", method)
	}
	stream, err := conn.NewStream(ctx, StreamDescription(method), ProcedureFor(method))
	if err != nil {
		return nil, err
	}
	var responses []*dynamicpb.Message
	for _, request := range requests {
		if err := stream.SendMsg(request); err != nil {
			return nil, err
		}
		if method == "ChatOrders" {
			response := c.NewResponse()
			if err := stream.RecvMsg(response); err != nil {
				return nil, err
			}
			responses = append(responses, response)
		}
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	for {
		response := c.NewResponse()
		err := stream.RecvMsg(response)
		if err == io.EOF {
			return responses, nil
		}
		if err != nil {
			return responses, err
		}
		responses = append(responses, response)
	}
}
