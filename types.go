package tai

import "context"

// Node represents a connected Tai node.
type Node struct {
	ID                string        // Unique node identifier
	TaiID             string        // Server-assigned identifier
	Version           string        // Tai binary version
	Capabilities      []string      // e.g. ["session","tunnel","realtime","container"]
	ContainerRuntimes []NodeRuntime // Available container runtimes
	OS                string        // "darwin" | "linux" | "windows"
	Arch              string        // "amd64" | "arm64"
	CPUCores          uint32
	MemoryMB          uint64
	DiskFreeMB        uint64
	Online            bool
}

// NodeRuntime describes a container runtime available on a node.
type NodeRuntime struct {
	Name    string // "docker" | "podman" | "containerd" | "nsjail"
	Version string // e.g. "27.1.0"
	Socket  string // e.g. "/var/run/docker.sock"
}

// Session represents an active agent session on a Tai node.
type Session interface {
	// ID returns the session identifier.
	ID() string

	// Prompt sends a user message to start or continue a turn.
	Prompt(req *PromptRequest) error

	// Cancel cancels the current turn.
	Cancel() error

	// Steer sends a steering message at a step boundary.
	Steer(message string) error

	// Resolve resolves a pending approval or question.
	Resolve(requestID string, resolution []byte) error

	// RecvEvent reads the next event from the Data stream.
	// Returns io.EOF when the session ends.
	RecvEvent() (*SessionMsg, error)

	// RecvAction reads the next action response from the Control stream.
	RecvAction() (*SessionMsg, error)

	// Close terminates the session.
	Close(ctx context.Context) error
}

// SessionMsg is a message received from a session stream.
type SessionMsg struct {
	Seq  uint64 // Monotonic sequence number
	Kind string // Message kind (e.g. "text_delta", "action.accepted")
	Data []byte // JSON payload
}

// NamespaceControl is the Payload namespace of every message on the L1
// Control stream.
const NamespaceControl = "control"

// ControlMsg is a message sent on the L1 Control stream. It is sent as a
// Payload with namespace NamespaceControl.
type ControlMsg struct {
	Kind string // Message kind
	Data []byte // Protobuf payload
}

// OpenSessionRequest contains parameters for opening a new session.
type OpenSessionRequest struct {
	NodeID    string
	SessionID string
	Options   *SessionOpts
}

// SessionOpts mirrors SessionOptions from the proto definition.
type SessionOpts struct {
	Runtime        string            // "agent" | "dsh" | "claude" | "codex"
	Isolation      string            // "auto" | "host" | "container"
	IdleTimeoutSec uint32            // Default 1800 (30 min)
	MaxDurationSec uint32            // 0 = unlimited
	Env            map[string]string // Runtime environment variables
	LLMProvider    string
	LLMModel       string
	LLMBaseURL     string
	Config         []byte // Runtime-specific JSON config
}

// PromptRequest is the input for Session.Prompt().
type PromptRequest struct {
	Message     string
	Attachments []Attachment
}

// Attachment is a file attached to a prompt.
type Attachment struct {
	FileID      string
	Path        string
	ContentType string
}

// ToolDefinition describes a server tool registered via Server.RegisterTool().
type ToolDefinition struct {
	// Name is the tool identifier used in `tai call <name>`.
	Name string

	// Description is a human-readable summary shown in `tai call --list`.
	Description string

	// InputSchema is the JSON Schema for validating tool input.
	InputSchema []byte

	// Handler processes a tool invocation and returns the result.
	Handler ToolHandler
}

// ToolHandler is the function signature for server tool implementations.
type ToolHandler func(ctx context.Context, input []byte) (output []byte, err error)

// TunnelProvider defines the interface for tunnel authorization and domain assignment.
// The SDK provides a default implementation; servers can supply their own.
type TunnelProvider interface {
	// Authorize checks whether a tunnel binding is allowed.
	// Returns the assigned domain on success, or an error on rejection.
	Authorize(nodeID string, req *TunnelBindRequest) (domain string, err error)
}

// TunnelBindRequest is the input for TunnelProvider.Authorize().
type TunnelBindRequest struct {
	TunnelID  string
	Target    string // e.g. "localhost:3000"
	Protocol  string // "http" | "tcp"
	TLS       bool
	SessionID string // Optional owning session
}
