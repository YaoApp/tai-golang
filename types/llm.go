package types

// Providers is the parsed form of SessionRequest.LLM.
// It carries the model configuration for a session.
type Providers struct {
	Primary Provider            `json:"primary"`
	Roles   map[string]Provider `json:"roles,omitempty"`
}

// Provider identifies a model via provider/model/base_url.
type Provider struct {
	Provider string `json:"provider"` // e.g. "deepseek" | "openai"
	Model    string `json:"model"`    // e.g. "deepseek-chat"
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

// UserIdentity carries authenticated user context.
// Placeholder — will be populated when RBAC is implemented.
type UserIdentity struct {
	UserID string `json:"userId"`
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
}
