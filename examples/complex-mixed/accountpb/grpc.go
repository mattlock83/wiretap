package accountpb

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/dynamicpb"
)

type AccountServiceServer interface {
	GetAccount(context.Context, *dynamicpb.Message) (*dynamicpb.Message, error)
}

func RegisterAccountServiceServer(s *grpc.Server, srv AccountServiceServer) {
	s.RegisterService(&accountServiceDesc, srv)
}

type AccountServiceClient struct {
	cc *grpc.ClientConn
}

func NewAccountServiceClient(cc *grpc.ClientConn) *AccountServiceClient {
	return &AccountServiceClient{cc: cc}
}

func (c *AccountServiceClient) GetAccount(ctx context.Context, req *dynamicpb.Message, opts ...grpc.CallOption) (*dynamicpb.Message, error) {
	resp := AccountReply()
	if err := c.cc.Invoke(ctx, AccountGetMethod, req, resp, opts...); err != nil {
		return nil, err
	}
	return resp, nil
}

var accountServiceDesc = grpc.ServiceDesc{
	ServiceName: AccountServiceName,
	HandlerType: (*AccountServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetAccount",
			Handler:    accountGetHandler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: fileName,
}

func accountGetHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := NewAccountRequest("")
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AccountServiceServer).GetAccount(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: AccountGetMethod,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AccountServiceServer).GetAccount(ctx, req.(*dynamicpb.Message))
	}
	return interceptor(ctx, in, info, handler)
}
