package types

// SessionConfigOption is an agent runtime configuration option (ACP v2).
// Switched via session/set_config_option. Type discriminates "select" and "boolean".
type SessionConfigOption struct {
	ConfigID    string `json:"configId"` // required
	Name        string `json:"name"`     // required, human-readable
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"` // UX grouping
	Type        string `json:"type"`               // "select" | "boolean"

	// Shared by select and boolean (string for select, bool for boolean).
	CurrentValue any                         `json:"currentValue,omitempty"`
	Options      *SessionConfigSelectOptions `json:"options,omitempty"` // type=select only

	Meta map[string]any `json:"_meta,omitempty"`
}

// SessionConfigSelectOptions is the available values for a select config option.
type SessionConfigSelectOptions struct {
	Values []SessionConfigSelectOption `json:"values"`
}

// SessionConfigSelectOption is a single select value (ACP v2).
type SessionConfigSelectOption struct {
	Value       string         `json:"value"` // required
	Name        string         `json:"name"`  // required, display name
	Description string         `json:"description,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}
