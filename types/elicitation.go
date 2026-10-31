package types

import "encoding/json"

// ElicitationResponse is the user's reply to an elicitation/create request.
// Passed to Session.ResolveElicitation.
type ElicitationResponse struct {
	Action  string          `json:"action"`            // "accept" | "decline" | "cancel"
	Content json.RawMessage `json:"content,omitempty"` // action=accept: form data
}
