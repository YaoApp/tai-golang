package types

import (
	"context"
	"encoding/json"
	"io"
)

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
	LLM      json.RawMessage   `json:"llm,omitempty"` // Providers matrix JSON
	Env      map[string]string `json:"env,omitempty"`
	Config   json.RawMessage   `json:"config,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Meta     map[string]any    `json:"_meta,omitempty"`
}

// McpServer describes an MCP server to attach to the session.
type McpServer struct {
	Type    string            `json:"type"` // "stdio" | "http"
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"` // stdio only
	Args    []string          `json:"args,omitempty"`    // stdio only
	Env     []EnvVariable     `json:"env,omitempty"`     // stdio only: environment variables
	URL     string            `json:"url,omitempty"`     // http: endpoint URL
	Headers map[string]string `json:"headers,omitempty"`
	Meta    map[string]any    `json:"_meta,omitempty"`
}

// EnvVariable is an ACP v2 environment variable key-value pair.
type EnvVariable struct {
	Name  string         `json:"name"`
	Value string         `json:"value"`
	Meta  map[string]any `json:"_meta,omitempty"`
}

// OpenSessionRequest is the transport-level wrapper for opening a session.
// NodeID selects the target node, Token carries the OperationToken,
// Request holds the session parameters.
type OpenSessionRequest struct {
	NodeID    string
	SessionID string
	Token     string // OperationToken JWT
	Request   *SessionRequest
}

// SessionOpenRequest is the node-level wrapper for opening a session.
// Unlike OpenSessionRequest, it does not carry NodeID (the RemoteNode
// already knows its own node ID).
type SessionOpenRequest struct {
	SessionID  string
	Token      string
	Request    *SessionRequest
	OnEvent    OnSessionEventFunc     // lifecycle event callback (disconnect/resume/gap)
	IssueToken func() (string, error) // issues a new OperationToken for refresh; nil disables auto-refresh
}

// ResumeSessionRequest carries the parameters for resuming a session
// after a Light connection drops. The ctrl/data streams are reopened
// from last_seq+1.
type ResumeSessionRequest struct {
	SessionID   string
	Token       string
	LastCtrlSeq uint64
	LastDataSeq uint64
	OnEvent     OnSessionEventFunc // lifecycle event callback (disconnect/resume/gap)
}

// Session represents an active agent session on a Tai node.
type Session interface {
	ID() string
	NodeID() string
	Status() SessionStatus

	Prompt(msg *UserMessage) error
	Followup(msg *UserMessage) (Disposition, error)
	Cancel() error
	Steer(msg *UserMessage) (Disposition, error)

	UpdateQueue(action *QueueAction) error

	ResolvePermission(requestID string, resp *PermissionResponse) error
	ResolveElicitation(id string, resp *ElicitationResponse) error
	SetConfigOption(configID string, value *ConfigOptionValue) error

	UpdateSession(ctx context.Context, opts *SessionUpdateOptions) error

	RecvEvent() (*SessionMsg, error)
	RecvAction() (*SessionMsg, error)

	StartRealtime(cfg *RealtimeConfig) error
	StopRealtime() error

	Close(ctx context.Context) error
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
	SessionOpening      SessionStatus = "opening"
	SessionRunning      SessionStatus = "running"
	SessionDisconnected SessionStatus = "disconnected" // ctrl or data lost; resume in progress
	SessionClosing      SessionStatus = "closing"
	SessionClosed       SessionStatus = "closed"
)

// ResumeFunc opens new ctrl and data streams for session resume.
// Called by Session when ctrl or data stream breaks. The implementation
// (provided by node) sends a resume first frame, reads the opened response,
// and opens a new data stream.
// Returns the new streams and the server's last buffered data seq.
type ResumeFunc func(ctx context.Context, lastCtrlSeq, lastDataSeq uint64) (ctrl, data io.ReadWriteCloser, serverLastDataSeq uint64, err error)

// SessionEventKind identifies lifecycle events reported to OnSessionEvent.
type SessionEventKind string

const (
	EventDisconnected SessionEventKind = "disconnected" // ctrl or data stream lost
	EventResumed      SessionEventKind = "resumed"      // session resumed after disconnect
	EventRecoveryGap  SessionEventKind = "recovery_gap" // server buffer overflow, needs full re-open
	EventResumeError  SessionEventKind = "resume_error" // resume attempt failed
)

// OnSessionEventFunc is called for session lifecycle events.
type OnSessionEventFunc func(sessionID string, event SessionEventKind)

// RealtimeConfig is a placeholder for Realtime extension configuration.
type RealtimeConfig struct{}
