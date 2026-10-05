package artifact

// GrpcStatusDetails is observed google.rpc.Status evidence. The terminal
// grpc_status remains authoritative; these details are not application messages.
// A nil pointer means the metadata was absent, unless capture was incomplete.
type GrpcStatusDetails struct {
	Source              string             `json:"source"`
	State               string             `json:"state"`
	Code                *int               `json:"code,omitempty"`
	Message             string             `json:"message,omitempty"`
	Diagnostic          string             `json:"diagnostic,omitempty"`
	Wire                *Body              `json:"wire,omitempty"`
	AuxiliaryDescriptor *Body              `json:"auxiliary_descriptor,omitempty"`
	AuxiliarySHA256     string             `json:"auxiliary_sha256,omitempty"`
	Details             []GrpcStatusDetail `json:"details"`
}

// GrpcStatusDetail preserves Any ordering and its contract-local interpretation.
// DescriptorSHA256 identifies either the boundary contract or the separately
// frozen helper descriptor set; helper types never change a service revision.
type GrpcStatusDetail struct {
	Index            int    `json:"index"`
	TypeURL          string `json:"type_url"`
	Type             string `json:"type,omitempty"`
	DescriptorSource string `json:"descriptor_source,omitempty"`
	DescriptorSHA256 string `json:"descriptor_sha256,omitempty"`
	State            string `json:"state"`
	Diagnostic       string `json:"diagnostic,omitempty"`
	Wire             *Body  `json:"wire,omitempty"`
	Body             *Body  `json:"body,omitempty"`
}
