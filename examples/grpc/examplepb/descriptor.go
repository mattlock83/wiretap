package examplepb

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	ServiceName           = "example.Service"
	DependencyServiceName = "example.Dependency"
	ProcessMethod         = "/example.Service/Process"
	DependencyAlphaMethod = "/example.Dependency/Alpha"
	DependencyBetaMethod  = "/example.Dependency/Beta"
	processRequestName    = "ProcessRequest"
	processReplyName      = "ProcessReply"
	dependencyRequestName = "DependencyRequest"
	dependencyReplyName   = "DependencyReply"
	fileName              = "example.proto"
	packageName           = "example"
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
					Name: proto.String(processRequestName),
					Field: []*descriptorpb.FieldDescriptorProto{
						{
							Name:   proto.String("id"),
							Number: proto.Int32(1),
							Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
						{
							Name:   proto.String("action"),
							Number: proto.Int32(2),
							Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
					},
				},
				{
					Name: proto.String(processReplyName),
					Field: []*descriptorpb.FieldDescriptorProto{
						{
							Name:   proto.String("result"),
							Number: proto.Int32(1),
							Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
					},
				},
				{
					Name: proto.String(dependencyRequestName),
					Field: []*descriptorpb.FieldDescriptorProto{
						{
							Name:   proto.String("id"),
							Number: proto.Int32(1),
							Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
						{
							Name:   proto.String("action"),
							Number: proto.Int32(2),
							Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
					},
				},
				{
					Name: proto.String(dependencyReplyName),
					Field: []*descriptorpb.FieldDescriptorProto{
						{
							Name:   proto.String("value"),
							Number: proto.Int32(1),
							Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
					},
				},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("Service"),
					Method: []*descriptorpb.MethodDescriptorProto{
						{
							Name:       proto.String("Process"),
							InputType:  proto.String("." + packageName + "." + processRequestName),
							OutputType: proto.String("." + packageName + "." + processReplyName),
						},
					},
				},
				{
					Name: proto.String("Dependency"),
					Method: []*descriptorpb.MethodDescriptorProto{
						{
							Name:       proto.String("Alpha"),
							InputType:  proto.String("." + packageName + "." + dependencyRequestName),
							OutputType: proto.String("." + packageName + "." + dependencyReplyName),
						},
						{
							Name:       proto.String("Beta"),
							InputType:  proto.String("." + packageName + "." + dependencyRequestName),
							OutputType: proto.String("." + packageName + "." + dependencyReplyName),
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

func NewProcessRequest(id, action string) *dynamicpb.Message {
	msg := newMessage(processRequestName)
	setString(msg, "id", id)
	setString(msg, "action", action)
	return msg
}

func NewProcessReply(result string) *dynamicpb.Message {
	msg := newMessage(processReplyName)
	setString(msg, "result", result)
	return msg
}

func NewDependencyRequest(id, action string) *dynamicpb.Message {
	msg := newMessage(dependencyRequestName)
	setString(msg, "id", id)
	setString(msg, "action", action)
	return msg
}

func NewDependencyReply(value string) *dynamicpb.Message {
	msg := newMessage(dependencyReplyName)
	setString(msg, "value", value)
	return msg
}

func ProcessReply() *dynamicpb.Message {
	return newMessage(processReplyName)
}

func DependencyReply() *dynamicpb.Message {
	return newMessage(dependencyReplyName)
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
