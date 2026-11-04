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

	StartRealtime(req *RealtimeRequest) error
	UpdateRealtime(opts *RealtimeUpdateOptions) error
	StopRealtime() error

	// Audio I/O (requires active Realtime session)
	AudioAppend(data []byte) error
	AudioCommit() error
	AudioClear() error
	AudioInterrupt() error
	RecvAudio() <-chan *AudioFrame

	// Motion (data-driven Avatar; Plan 7)
	RecvMotion() <-chan *MotionFrame

	// Video (Plan 7)
	SendVideoFrame(frame *VideoFrame) error
	RecvVideoFrame() <-chan *VideoFrame

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

// RealtimeRequest carries all parameters to start a Realtime session.
// Primary must be a Realtime model (gpt-realtime-2.1 / qwen-omni-turbo / gemini-live).
// Roles are optional and carry other-purpose models (light for summaries, heavy for reasoning, etc.).
type RealtimeRequest struct {
	Providers Providers     `json:"providers"`
	Audio     *AudioConfig  `json:"audio,omitempty"`
	Motion    *MotionConfig `json:"motion,omitempty"`
	Video     *VideoConfig  `json:"video,omitempty"`
}

// RealtimeUpdateOptions are runtime configuration changes for a running Realtime session.
// nil fields mean "no change". Providers cannot be changed at runtime (requires Stop + Start).
type RealtimeUpdateOptions struct {
	Audio  *AudioConfig  `json:"audio,omitempty"`
	Motion *MotionConfig `json:"motion,omitempty"`
	Video  *VideoConfig  `json:"video,omitempty"`
}

// AudioConfig configures audio input/output for a Realtime session.
type AudioConfig struct {
	InputFormat    string     `json:"input_format,omitempty"`
	OutputFormat   string     `json:"output_format,omitempty"`
	SampleRate     int        `json:"sample_rate,omitempty"`
	Channels       int        `json:"channels,omitempty"`
	VAD            *VADConfig `json:"vad,omitempty"`
	Voice          string     `json:"voice,omitempty"`
	NoiseReduction string     `json:"noise_reduction,omitempty"`
}

// VADConfig configures voice activity detection.
type VADConfig struct {
	Mode              string  `json:"mode,omitempty"`
	Threshold         float64 `json:"threshold,omitempty"`
	SilenceDurationMs int     `json:"silence_duration_ms,omitempty"`
	PrefixPaddingMs   int     `json:"prefix_padding_ms,omitempty"`
}

// MotionConfig configures data-driven Avatar motion output.
type MotionConfig struct {
	Enabled bool   `json:"enabled"`
	Profile string `json:"profile,omitempty"`
	FPS     int    `json:"fps,omitempty"`
}

// VideoConfig configures video input and output.
type VideoConfig struct {
	Input  *VideoInputConfig  `json:"input,omitempty"`
	Output *VideoOutputConfig `json:"output,omitempty"`
}

// VideoInputConfig configures user camera input.
type VideoInputConfig struct {
	Enabled   bool `json:"enabled"`
	MaxFPS    int  `json:"max_fps,omitempty"`
	MaxWidth  int  `json:"max_width,omitempty"`
	MaxHeight int  `json:"max_height,omitempty"`
}

// VideoOutputConfig configures avatar/rendered video output.
type VideoOutputConfig struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode,omitempty"`
	MaxFPS  int    `json:"max_fps,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Codec   string `json:"codec,omitempty"`
}

// AudioFrame is a decoded audio datagram carrying PCM data and a sequence number
// for loss detection and reordering.
type AudioFrame struct {
	Data []byte // PCM16 raw audio
	Seq  uint32 // datagram sequence number
}

// MotionFrame carries data-driven Avatar blendshape data. (Plan 7 placeholder)
type MotionFrame struct{}

// VideoFrame carries a video frame. (Plan 7 placeholder)
type VideoFrame struct{}
