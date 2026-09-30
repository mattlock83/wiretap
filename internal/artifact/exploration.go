package artifact

// Exploration is execution provenance, never executable code or hook context.
type Exploration struct {
	Version         string            `json:"version"`
	SourceRunID     string            `json:"source_run_id"`
	SourceAttemptID string            `json:"source_attempt_id"`
	SourceSHA256    string            `json:"source_sha256"`
	Mode            string            `json:"mode"` // baseline or rejection
	Target          *RejectionTarget  `json:"target,omitempty"`
	Catalogue       []RejectionTarget `json:"catalogue,omitempty"`
	Notes           []string          `json:"notes,omitempty"`
	RequestVerified bool              `json:"request_verified,omitempty"`
}

// RejectionTarget describes one independently validated negative mutation.
// The catalogue covers the supported cases derivable from the chosen seed.
type RejectionTarget struct {
	Key              string `json:"key" yaml:"key"`
	SchemaID         string `json:"schema_id" yaml:"schema_id"`
	Operation        string `json:"operation" yaml:"operation"`
	FlowID           string `json:"flow_id" yaml:"flow_id"`
	Location         string `json:"instance_location" yaml:"instance_location"`
	Constraint       string `json:"constraint" yaml:"constraint"`
	ExpectedStatuses []int  `json:"expected_statuses" yaml:"expected_statuses"`
}
