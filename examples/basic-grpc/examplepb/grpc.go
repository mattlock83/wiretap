package examplepb

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/dynamicpb"
)

type ServiceServer interface {
	Process(context.Context, *dynamicpb.Message) (*dynamicpb.Message, error)
}

type DependencyServer interface {
	Alpha(context.Context, *dynamicpb.Message) (*dynamicpb.Message, error)
	Beta(context.Context, *dynamicpb.Message) (*dynamicpb.Message, error)
}

func RegisterServiceServer(s *grpc.Server, srv ServiceServer) {
	s.RegisterService(&serviceDesc, srv)
}

func RegisterDependencyServer(s *grpc.Server, srv DependencyServer) {
	s.RegisterService(&dependencyDesc, srv)
}

type ServiceClient struct {
	cc *grpc.ClientConn
}

type DependencyClient struct {
	cc *grpc.ClientConn
}

func NewServiceClient(cc *grpc.ClientConn) *ServiceClient {
	return &ServiceClient{cc: cc}
}

func NewDependencyClient(cc *grpc.ClientConn) *DependencyClient {
	return &DependencyClient{cc: cc}
}

func (c *ServiceClient) Process(ctx context.Context, req *dynamicpb.Message, opts ...grpc.CallOption) (*dynamicpb.Message, error) {
	resp := ProcessReply()
	if err := c.cc.Invoke(ctx, ProcessMethod, req, resp, opts...); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *DependencyClient) Alpha(ctx context.Context, req *dynamicpb.Message, opts ...grpc.CallOption) (*dynamicpb.Message, error) {
	resp := DependencyReply()
	if err := c.cc.Invoke(ctx, DependencyAlphaMethod, req, resp, opts...); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *DependencyClient) Beta(ctx context.Context, req *dynamicpb.Message, opts ...grpc.CallOption) (*dynamicpb.Message, error) {
	resp := DependencyReply()
	if err := c.cc.Invoke(ctx, DependencyBetaMethod, req, resp, opts...); err != nil {
		return nil, err
	}
	return resp, nil
}

var serviceDesc = grpc.ServiceDesc{
	ServiceName: ServiceName,
	HandlerType: (*ServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Process",
			Handler:    serviceProcessHandler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: fileName,
}

var dependencyDesc = grpc.ServiceDesc{
	ServiceName: DependencyServiceName,
	HandlerType: (*DependencyServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Alpha",
			Handler:    dependencyAlphaHandler,
		},
		{
			MethodName: "Beta",
			Handler:    dependencyBetaHandler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: fileName,
}

func serviceProcessHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := NewProcessRequest("", "")
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).Process(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ProcessMethod,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).Process(ctx, req.(*dynamicpb.Message))
	}
	return interceptor(ctx, in, info, handler)
}

func dependencyAlphaHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := NewDependencyRequest("", "")
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DependencyServer).Alpha(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: DependencyAlphaMethod,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DependencyServer).Alpha(ctx, req.(*dynamicpb.Message))
	}
	return interceptor(ctx, in, info, handler)
}

func dependencyBetaHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := NewDependencyRequest("", "")
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DependencyServer).Beta(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: DependencyBetaMethod,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DependencyServer).Beta(ctx, req.(*dynamicpb.Message))
	}
	return interceptor(ctx, in, info, handler)
}
