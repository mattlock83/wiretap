package accountpb

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	AccountServiceName = "account.AccountService"
	AccountGetMethod   = "/account.AccountService/GetAccount"

	accountRequestName = "AccountRequest"
	accountReplyName   = "AccountReply"
	fileName           = "account.proto"
	packageName        = "account"
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
					Name: proto.String(accountRequestName),
					Field: []*descriptorpb.FieldDescriptorProto{
						stringField("account_id", 1),
					},
				},
				{
					Name: proto.String(accountReplyName),
					Field: []*descriptorpb.FieldDescriptorProto{
						stringField("id", 1),
						stringField("name", 2),
						doubleField("balance", 3),
						stringField("currency", 4),
						stringField("status", 5),
					},
				},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("AccountService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						{
							Name:       proto.String("GetAccount"),
							InputType:  proto.String("." + packageName + "." + accountRequestName),
							OutputType: proto.String("." + packageName + "." + accountReplyName),
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

func NewAccountRequest(accountID string) *dynamicpb.Message {
	msg := newMessage(accountRequestName)
	setString(msg, "account_id", accountID)
	return msg
}

func NewAccountReply(id, name string, balance float64, currency, status string) *dynamicpb.Message {
	msg := newMessage(accountReplyName)
	setString(msg, "id", id)
	setString(msg, "name", name)
	setFloat64(msg, "balance", balance)
	setString(msg, "currency", currency)
	setString(msg, "status", status)
	return msg
}

func AccountReply() *dynamicpb.Message {
	return newMessage(accountReplyName)
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
