package app

import (
	"strings"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/phrond/evermock/internal/artifact"
)

// decodeGRPCPayloads enriches gRPC bodies with protojson text and drops base64 payloads.
func decodeGRPCPayloads(art *artifact.Artifact) {
	if art == nil || len(art.Traffic) == 0 || len(art.Schemas) == 0 {
		return
	}

	protoByRole := loadProtoSchemasByRole(art.Schemas)
	if len(protoByRole) == 0 {
		return
	}

	for i := range art.Traffic {
		flow := &art.Traffic[i]
		if !strings.EqualFold(flow.Protocol, "grpc") || flow.GRPC == nil {
			continue
		}

		role := roleForChannel(flow.Channel)
		specs := protoByRole[role]
		if len(specs) == 0 {
			continue
		}

		methodDesc, schemaID := lookupGRPCMethod(specs, flow.GRPC.Service, flow.GRPC.Method)
		if methodDesc == nil {
			continue
		}

		if flow.Request.GRPC != nil {
			decodeGRPCBody(flow.Request.GRPC.Body, methodDesc.GetInputType(), schemaID)
		}
		if flow.Response.GRPC != nil {
			decodeGRPCBody(flow.Response.GRPC.Body, methodDesc.GetOutputType(), schemaID)
		}
	}
}

func loadProtoSchemasByRole(schemas []artifact.SchemaRef) map[string][]protoSpec {
	out := map[string][]protoSpec{}
	for _, schema := range schemas {
		if !strings.EqualFold(strings.TrimSpace(schema.Type), "protobuf") {
			continue
		}
		role := strings.TrimSpace(schema.Role)
		if role == "" {
			role = "sut"
		}
		spec, err := loadProtoSpec(schema)
		if err != nil {
			continue
		}
		out[role] = append(out[role], spec)
	}
	return out
}

func lookupGRPCMethod(specs []protoSpec, service, method string) (*desc.MethodDescriptor, string) {
	service = strings.TrimSpace(service)
	method = strings.TrimSpace(method)
	if service == "" || method == "" {
		return nil, ""
	}
	for _, spec := range specs {
		serviceDesc := spec.services[service]
		if serviceDesc == nil {
			continue
		}
		methodDesc := serviceDesc.FindMethodByName(method)
		if methodDesc != nil {
			return methodDesc, spec.ref.ID
		}
	}
	return nil, ""
}

func decodeGRPCBody(body *artifact.Body, messageDesc *desc.MessageDescriptor, schemaID string) {
	if body == nil || messageDesc == nil {
		return
	}
	raw := bodyBytes(body)
	if len(raw) == 0 {
		return
	}

	msg := dynamic.NewMessage(messageDesc)
	if err := msg.Unmarshal(raw); err != nil {
		return
	}
	decodedJSON, err := msg.MarshalJSON()
	if err != nil {
		return
	}

	decodedText := string(decodedJSON)
	body.Encoding = "utf8"
	body.Text = decodedText
	body.Bytes = ""
	body.Decoded = &artifact.DecodedBody{
		Encoding:  "protojson",
		Text:      decodedText,
		SchemaRef: schemaID,
	}
}
