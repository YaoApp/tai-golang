package types

import "encoding/json"

// SessionRequest mirrors tai/types.SessionRequest — same name, same fields.
// It carries all parameters needed to open a session on a Tai node.
type SessionRequest struct {
	// ACP standard fields
	Cwd                   string      `json:"cwd,omitempty"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
	McpServers            []McpServer `json:"mcpServers,omitempty"`

	// Tai extensions
	AssistantID string `json:"assistantId,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	Runtime     string `json:"runtime"`
	Isolation   string `json:"isolation"`
	IdleTimeout uint32 `json:"idleTimeout,omitempty"` // seconds
	MaxDuration uint32 `json:"maxDuration,omitempty"` // seconds

	// Agent configuration
	LLM      json.RawMessage   `json:"llm,omitempty"`      // Providers matrix JSON
	Env      map[string]string `json:"env,omitempty"`
	Config   json.RawMessage   `json:"config,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Meta     map[string]any    `json:"_meta,omitempty"`
}

// McpServer describes an MCP server to attach to the session.
type McpServer struct {
	Type    string            `json:"type"`              // "stdio" | "sse" | "streamable-http"
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"` // stdio only
	Args    []string          `json:"args,omitempty"`    // stdio only
	URL     string            `json:"url,omitempty"`     // sse / streamable-http
	Headers map[string]string `json:"headers,omitempty"`
}

// OpenSessionRequest is the transport-level wrapper for opening a session.
// NodeID selects the target node, Token carries the OperationToken,
// Request holds the session parameters.
type OpenSessionRequest struct {
	NodeID    string
	SessionID string
	Token     string          // OperationToken JWT
	Request   *SessionRequest
}

// SessionMsg is a message received from a session stream.
type SessionMsg struct {
	Seq  uint64 // Monotonic sequence number
	Kind string // Message kind (e.g. "text_delta", "action.accepted")
	Data []byte // JSON payload
}

// SessionStatus represents the lifecycle state of a session.
type SessionStatus string

const (
	SessionOpening SessionStatus = "opening"
	SessionRunning SessionStatus = "running"
	SessionClosing SessionStatus = "closing"
	SessionClosed  SessionStatus = "closed"
)
