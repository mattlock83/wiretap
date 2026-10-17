package wiretap

// Application identifies the system under test across capture runs.
type Application struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

// Schema and Dependency are optional per-test overrides of .wiretap.yaml defaults.
type Schema struct {
	ID     string       `json:"id,omitempty"`
	Type   string       `json:"type"`
	Role   string       `json:"role,omitempty"`
	Source SchemaSource `json:"source"`
}
type SchemaSource struct {
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	URL     string `json:"url,omitempty"`
	Content string `json:"content,omitempty"`
}
type Dependency struct {
	AssetID        string   `json:"asset_id"`
	AssetName      string   `json:"asset_name,omitempty"`
	UpstreamURL    string   `json:"upstream_url"`
	UpstreamKind   string   `json:"upstream_kind,omitempty"`
	Protocol       string   `json:"protocol,omitempty"`
	Schema         *Schema  `json:"schema,omitempty"`
	Schemas        []Schema `json:"schemas,omitempty"`
	SchemaRequired *bool    `json:"schema_required,omitempty"`
}
type Redaction struct {
	Field       string `json:"field"`
	Rule        string `json:"rule"`
	Replacement string `json:"replacement,omitempty"`
}
