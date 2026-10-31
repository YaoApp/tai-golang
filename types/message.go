package types

// UserMessage is the input for Session.Prompt(), Followup(), and Steer().
// Placeholder — fields will be populated when session/ is implemented.
type UserMessage struct {
	Message     string       `json:"message"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is a file attached to a prompt.
type Attachment struct {
	FileID      string `json:"fileId,omitempty"`
	Path        string `json:"path,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

// ContentBlock is a typed content element within a message.
// Placeholder — full definition follows tai/types/message.go.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
