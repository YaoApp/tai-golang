package types

// Providers is the parsed form of SessionRequest.LLM.
// It carries the LLM matrix for a session: one endpoint per role.
type Providers struct {
	Primary Provider            `json:"primary"`
	Roles   map[string]Provider `json:"roles,omitempty"`
}

// Provider is one resolved LLM connection point: yao's llm.Provider joined with
// llm.Model. Field names follow yao/llm/types.go, which mirrors PiG's ai types.
type Provider struct {
	// From llm.Provider.
	ProviderID string         `json:"provider_id"` // e.g. "deepseek" | "openai"
	API        string         `json:"api"`         // "openai-completions" | "anthropic-messages"
	BaseURL    string         `json:"base_url,omitempty"`
	APIKey     string         `json:"api_key,omitempty"`
	Headers    map[string]any `json:"headers,omitempty"`
	Compat     map[string]any `json:"compat,omitempty"`

	// From llm.Model.
	Model          string         `json:"model"` // e.g. "deepseek-chat"
	SamplingParams map[string]any `json:"sampling_params,omitempty"`
}

// UserIdentity carries authenticated user context.
// Placeholder — will be populated when RBAC is implemented.
type UserIdentity struct {
	UserID string `json:"userId"`
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
}
