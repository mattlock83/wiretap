package artifact

// Exploration is execution provenance, never executable code or hook context.
type Exploration struct {
	Version         string            `json:"version"`
	SourceRunID     string            `json:"source_run_id"`
	SourceAttemptID string            `json:"source_attempt_id"`
	SourceSHA256    string            `json:"source_sha256,omitempty"`
	SourceKind      string            `json:"source_kind,omitempty"`
	SourceFlowIDs   []string          `json:"source_flow_ids,omitempty"`
	InvocationID    string            `json:"exploration_id,omitempty"`
	Mode            string            `json:"mode"` // baseline or rejection
	Target          *RejectionTarget  `json:"target,omitempty"`
	Catalogue       []RejectionTarget `json:"catalogue,omitempty"`
	Notes           []string          `json:"notes,omitempty"`
	RequestVerified bool              `json:"request_verified,omitempty"`
}

// RejectionTarget describes one independently validated negative mutation.
// The catalogue covers the supported cases derivable from the chosen seed.
type RejectionTarget struct {
	Protocol         string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	MessageIndex     *int   `json:"message_index,omitempty" yaml:"message_index,omitempty"`
	Key              string `json:"key" yaml:"key"`
	SchemaID         string `json:"schema_id" yaml:"schema_id"`
	Operation        string `json:"operation" yaml:"operation"`
	FlowID           string `json:"flow_id" yaml:"flow_id"`
	Location         string `json:"instance_location" yaml:"instance_location"`
	Constraint       string `json:"constraint" yaml:"constraint"`
	ExpectedStatuses []int  `json:"expected_statuses" yaml:"expected_statuses"`
}
