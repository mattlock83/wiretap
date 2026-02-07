package paymentpb

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	PaymentServiceName  = "payment.PaymentService"
	PaymentSubmitMethod = "/payment.PaymentService/Submit"

	paymentSubmitRequestName = "PaymentSubmitRequest"
	paymentSubmitReplyName   = "PaymentSubmitReply"
	fileName                 = "payment.proto"
	packageName              = "payment"
)

var (
	fileOnce sync.Once
	fileDesc protoreflect.FileDescriptor
)

func fileDescriptor() protoreflect.FileDescriptor {
	fileOnce.Do(func() {
		fd := &descriptorpb.FileDescriptorProto{
			Syntax:  proto.String("proto3"),
			Name:    proto.String(fileName),
			Package: proto.String(packageName),
			MessageType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String(paymentSubmitRequestName),
					Field: []*descriptorpb.FieldDescriptorProto{
						stringField("from_account", 1),
						stringField("to_account", 2),
						doubleField("amount", 3),
						stringField("currency", 4),
					},
				},
				{
					Name: proto.String(paymentSubmitReplyName),
					Field: []*descriptorpb.FieldDescriptorProto{
						stringField("payment_id", 1),
						stringField("status", 2),
						stringField("error_code", 3),
						stringField("message", 4),
						stringField("transaction_id", 5),
						stringField("from_account_name", 6),
						stringField("to_account_name", 7),
						doubleField("remaining_balance", 8),
					},
				},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("PaymentService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						{
							Name:       proto.String("Submit"),
							InputType:  proto.String("." + packageName + "." + paymentSubmitRequestName),
							OutputType: proto.String("." + packageName + "." + paymentSubmitReplyName),
						},
					},
				},
			},
		}

		file, err := protodesc.NewFile(fd, nil)
		if err != nil {
			panic(err)
		}
		fileDesc = file
	})

	return fileDesc
}

func stringField(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	}
}

func doubleField(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   descriptorpb.FieldDescriptorProto_TYPE_DOUBLE.Enum(),
	}
}

func messageDescriptor(name string) protoreflect.MessageDescriptor {
	return fileDescriptor().Messages().ByName(protoreflect.Name(name))
}

func newMessage(name string) *dynamicpb.Message {
	desc := messageDescriptor(name)
	if desc == nil {
		return dynamicpb.NewMessage(nil)
	}
	return dynamicpb.NewMessage(desc)
}

func NewPaymentSubmitRequest(fromAccount, toAccount string, amount float64, currency string) *dynamicpb.Message {
	msg := newMessage(paymentSubmitRequestName)
	setString(msg, "from_account", fromAccount)
	setString(msg, "to_account", toAccount)
	setFloat64(msg, "amount", amount)
	setString(msg, "currency", currency)
	return msg
}

func NewPaymentSubmitReply(status, errorCode, message, paymentID, transactionID, fromAccountName, toAccountName string, remainingBalance float64) *dynamicpb.Message {
	msg := newMessage(paymentSubmitReplyName)
	setString(msg, "status", status)
	setString(msg, "error_code", errorCode)
	setString(msg, "message", message)
	setString(msg, "payment_id", paymentID)
	setString(msg, "transaction_id", transactionID)
	setString(msg, "from_account_name", fromAccountName)
	setString(msg, "to_account_name", toAccountName)
	setFloat64(msg, "remaining_balance", remainingBalance)
	return msg
}

func PaymentSubmitReply() *dynamicpb.Message {
	return newMessage(paymentSubmitReplyName)
}

func GetString(msg *dynamicpb.Message, field string) string {
	if msg == nil {
		return ""
	}
	desc := msg.Descriptor().Fields().ByName(protoreflect.Name(field))
	if desc == nil {
		return ""
	}
	return msg.Get(desc).String()
}

func GetFloat64(msg *dynamicpb.Message, field string) float64 {
	if msg == nil {
		return 0
	}
	desc := msg.Descriptor().Fields().ByName(protoreflect.Name(field))
	if desc == nil {
		return 0
	}
	return msg.Get(desc).Float()
}

func setString(msg *dynamicpb.Message, field string, value string) {
	if msg == nil {
		return
	}
	desc := msg.Descriptor().Fields().ByName(protoreflect.Name(field))
	if desc == nil {
		return
	}
	msg.Set(desc, protoreflect.ValueOfString(value))
}

func setFloat64(msg *dynamicpb.Message, field string, value float64) {
	if msg == nil {
		return
	}
	desc := msg.Descriptor().Fields().ByName(protoreflect.Name(field))
	if desc == nil {
		return
	}
	msg.Set(desc, protoreflect.ValueOfFloat64(value))
}
