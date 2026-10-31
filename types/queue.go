package types

// QueueAction is a queue management operation for Steer/Followup messages.
type QueueAction struct {
	Action    string         `json:"action"`              // "remove" | "edit" | "clear" | "promote"
	MessageID string         `json:"messageId,omitempty"` // remove/edit/promote
	Content   []ContentBlock `json:"content,omitempty"`   // edit: new content
}
