package types

import "encoding/json"

// SessionUpdateOptions are runtime configuration changes applied to a running Session.
// nil fields mean "no change". Delegates to Sandbox.Update / Sandbox.Mount / Sandbox.Unmount.
type SessionUpdateOptions struct {
	// Workspace
	WorkspaceID   *string `json:"workspaceId,omitempty"`
	WorkspacePath *string `json:"workspacePath,omitempty"` // takes priority over WorkspaceID

	// Access policy
	AccessPolicy *string `json:"accessPolicy,omitempty"` // "read-only" | "workspace-write" | "full-access"

	// Shared mounts (ACP additionalDirectories)
	AddMounts    []MountSpec `json:"addMounts,omitempty"`
	RemoveMounts []string    `json:"removeMounts,omitempty"` // by name

	// LLM model matrix
	LLM *json.RawMessage `json:"llm,omitempty"` // same format as SessionRequest.LLM

	// Environment variables (merge semantics, does not overwrite existing)
	Environment map[string]string `json:"environment,omitempty"`

	// Resource limits
	Resources *ResourceLimit `json:"resources,omitempty"`
}

// MountSpec declares a shared mount.
type MountSpec struct {
	Name     string `json:"name"`               // mount name → /shared/<name>/
	Path     string `json:"path"`               // host path
	ReadOnly bool   `json:"readOnly,omitempty"` // default read-only
}

// ResourceLimit specifies resource quotas.
type ResourceLimit struct {
	Memory    int64   `json:"memory,omitempty"`    // bytes
	CPUs      float64 `json:"cpus,omitempty"`      // number of cores
	GPU       string  `json:"gpu,omitempty"`       // GPU device
	DiskSpace int64   `json:"diskSpace,omitempty"` // bytes
}

// ConfigOptionValue is the value portion of an ACP v2 SetSessionConfigOptionRequest.
// Type discriminates between "id" (select) and "boolean".
type ConfigOptionValue struct {
	Type  string `json:"type"`  // "id" | "boolean"
	Value any    `json:"value"` // type=id: string; type=boolean: bool
}
