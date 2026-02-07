package paymentpb

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/dynamicpb"
)

type PaymentServiceServer interface {
	Submit(context.Context, *dynamicpb.Message) (*dynamicpb.Message, error)
}

func RegisterPaymentServiceServer(s *grpc.Server, srv PaymentServiceServer) {
	s.RegisterService(&paymentServiceDesc, srv)
}

type PaymentServiceClient struct {
	cc *grpc.ClientConn
}

func NewPaymentServiceClient(cc *grpc.ClientConn) *PaymentServiceClient {
	return &PaymentServiceClient{cc: cc}
}

func (c *PaymentServiceClient) Submit(ctx context.Context, req *dynamicpb.Message, opts ...grpc.CallOption) (*dynamicpb.Message, error) {
	resp := PaymentSubmitReply()
	if err := c.cc.Invoke(ctx, PaymentSubmitMethod, req, resp, opts...); err != nil {
		return nil, err
	}
	return resp, nil
}

var paymentServiceDesc = grpc.ServiceDesc{
	ServiceName: PaymentServiceName,
	HandlerType: (*PaymentServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Submit",
			Handler:    paymentSubmitHandler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: fileName,
}

func paymentSubmitHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := NewPaymentSubmitRequest("", "", 0, "")
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PaymentServiceServer).Submit(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PaymentSubmitMethod,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PaymentServiceServer).Submit(ctx, req.(*dynamicpb.Message))
	}
	return interceptor(ctx, in, info, handler)
}
