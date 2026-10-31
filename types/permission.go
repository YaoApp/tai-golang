package types

// PermissionResponse is the Client → Agent response to a permission request.
// Passed to Session.ResolvePermission.
type PermissionResponse struct {
	Outcome  string         `json:"outcome"`            // "selected" | "cancelled"
	OptionID string         `json:"optionId,omitempty"` // required when outcome=selected
	Meta     map[string]any `json:"_meta,omitempty"`
}
