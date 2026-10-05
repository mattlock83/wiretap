// Package fixture is a small ordinary grpc-go service shared by the runnable
// streaming example and Wiretap's integration tests. Descriptors are compiled
// from the checked-in proto so the demo needs no protoc installation.
package fixture

import (
	"context"
	_ "embed"
	"fmt"
	"io"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const Service = "wiretap.example.v1.Streams"

//go:embed stream.proto
var Source string

type Contract struct {
	Message protoreflect.MessageDescriptor
}

func Compile(ctx context.Context) (*Contract, error) {
	compiler := protocompile.Compiler{Resolver: &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{"stream.proto": Source})}}
	files, err := compiler.Compile(ctx, "stream.proto")
	if err != nil {
		return nil, err
	}
	return &Contract{Message: files[0].Messages().ByName("Message")}, nil
}
func (c *Contract) NewMessage(value string, count int32, data []byte) *dynamicpb.Message {
	m := dynamicpb.NewMessage(c.Message)
	if value != "" {
		m.Set(c.Message.Fields().ByName("value"), protoreflect.ValueOfString(value))
	}
	if count != 0 {
		m.Set(c.Message.Fields().ByName("count"), protoreflect.ValueOfInt32(count))
	}
	if len(data) > 0 {
		m.Set(c.Message.Fields().ByName("data"), protoreflect.ValueOfBytes(data))
	}
	return m
}
func Text(m proto.Message) string {
	r := m.ProtoReflect()
	return r.Get(r.Descriptor().Fields().ByName("value")).String()
}
func Count(m proto.Message) int32 {
	r := m.ProtoReflect()
	return int32(r.Get(r.Descriptor().Fields().ByName("count")).Int())
}
func Procedure(method string) string { return "/" + Service + "/" + method }
func StreamDescription(method string) *grpc.StreamDesc {
	return &grpc.StreamDesc{StreamName: method, ClientStreams: method == "Collect" || method == "Chat", ServerStreams: method == "Watch" || method == "Chat"}
}

// NewServer creates a normal grpc-go server; before can make an ordinary
// dependency request to demonstrate provider and consumer boundaries together.
func (c *Contract) NewServer(before func(context.Context) error) *grpc.Server {
	start := func(ctx context.Context) error {
		if err := grpc.SetHeader(ctx, metadata.Pairs("example-label", "one", "example-label", "two", "example-bin", string([]byte{0, 1, 2}))); err != nil {
			return err
		}
		grpc.SetTrailer(ctx, metadata.Pairs("example-finished", "true"))
		if before != nil {
			return before(ctx)
		}
		return nil
	}
	unary := func(_ any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := c.NewMessage("", 0, nil)
		if err := decode(in); err != nil {
			return nil, err
		}
		handler := func(ctx context.Context, request any) (any, error) {
			if err := start(ctx); err != nil {
				return nil, err
			}
			return proto.Clone(request.(proto.Message)), nil
		}
		if interceptor != nil {
			return interceptor(ctx, in, &grpc.UnaryServerInfo{FullMethod: Procedure("Echo")}, handler)
		}
		return handler(ctx, in)
	}
	watch := func(_ any, s grpc.ServerStream) error {
		if err := start(s.Context()); err != nil {
			return err
		}
		in := c.NewMessage("", 0, nil)
		if err := s.RecvMsg(in); err != nil {
			return err
		}
		count := Count(in)
		if count < 0 {
			if err := s.SendMsg(c.NewMessage(Text(in), 0, nil)); err != nil {
				return err
			}
			<-s.Context().Done()
			return status.FromContextError(s.Context().Err()).Err()
		}
		if count > 10000 {
			return status.Error(codes.InvalidArgument, "count exceeds fixture limit")
		}
		for i := int32(0); i < count; i++ {
			if err := s.SendMsg(c.NewMessage(Text(in), 0, nil)); err != nil {
				return err
			}
		}
		return nil
	}
	collect := func(_ any, s grpc.ServerStream) error {
		if err := start(s.Context()); err != nil {
			return err
		}
		var n int32
		var text string
		for {
			in := c.NewMessage("", 0, nil)
			err := s.RecvMsg(in)
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			n++
			text += Text(in)
		}
		return s.SendMsg(c.NewMessage(text, n, nil))
	}
	chat := func(_ any, s grpc.ServerStream) error {
		if err := start(s.Context()); err != nil {
			return err
		}
		for {
			in := c.NewMessage("", 0, nil)
			err := s.RecvMsg(in)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if err := s.SendMsg(in); err != nil {
				return err
			}
		}
	}
	server := grpc.NewServer()
	server.RegisterService(&grpc.ServiceDesc{ServiceName: Service, HandlerType: (*interface{})(nil), Metadata: "stream.proto", Methods: []grpc.MethodDesc{{MethodName: "Echo", Handler: unary}}, Streams: []grpc.StreamDesc{{StreamName: "Watch", Handler: watch, ServerStreams: true}, {StreamName: "Collect", Handler: collect, ClientStreams: true}, {StreamName: "Chat", Handler: chat, ClientStreams: true, ServerStreams: true}}}, struct{}{})
	return server
}

// Exercise uses ordinary native grpc-go calls over a reusable client channel.
// Chat deliberately receives each response before sending the next request.
func (c *Contract) Exercise(ctx context.Context, conn *grpc.ClientConn, method string, count int, opts ...grpc.CallOption) ([]*dynamicpb.Message, error) {
	if method == "Echo" {
		out := c.NewMessage("", 0, nil)
		err := conn.Invoke(ctx, Procedure(method), c.NewMessage("hello", 0, nil), out, opts...)
		return []*dynamicpb.Message{out}, err
	}
	stream, err := conn.NewStream(ctx, StreamDescription(method), Procedure(method), opts...)
	if err != nil {
		return nil, err
	}
	var output []*dynamicpb.Message
	switch method {
	case "Watch":
		if err := stream.SendMsg(c.NewMessage("hello", int32(count), nil)); err != nil {
			return nil, err
		}
	case "Collect", "Chat":
		for i := 0; i < count; i++ {
			if err := stream.SendMsg(c.NewMessage(fmt.Sprintf("message-%d", i), 0, nil)); err != nil {
				return nil, err
			}
			if method == "Chat" {
				out := c.NewMessage("", 0, nil)
				if err := stream.RecvMsg(out); err != nil {
					return nil, err
				}
				output = append(output, out)
			}
		}
	default:
		return nil, fmt.Errorf("unknown method %s", method)
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	for {
		out := c.NewMessage("", 0, nil)
		err := stream.RecvMsg(out)
		if err == io.EOF {
			return output, nil
		}
		if err != nil {
			return nil, err
		}
		output = append(output, out)
	}
}
