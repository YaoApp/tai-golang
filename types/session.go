package types

import (
	"context"
	"encoding/json"
	"errors"
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
	ResponseCreate() error
	RecvAudio() <-chan *AudioFrame

	// Render channels (data-driven Avatar motion + render stream)
	RecvMotion() <-chan *MotionFrame
	RecvRender() <-chan *RenderEvent

	// Video (Plan 7)
	SendVideoFrame(frame *VideoFrame) error
	RecvVideoFrame() <-chan *VideoFrame

	// Done returns a channel that is closed when the session terminates.
	Done() <-chan struct{}

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
	Enabled  bool              `json:"enabled"`
	Provider string            `json:"provider,omitempty"` // "fast" | "balanced" | custom
	Profile  string            `json:"profile,omitempty"`  // "arkit_52" | "live2d_16" | "viseme_15"
	FPS      int               `json:"fps,omitempty"`      // default 30
	Options  map[string]string `json:"options,omitempty"`  // provider-specific params
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

// MotionFrame carries data-driven Avatar blendshape + motion data.
// Wire format: 87 bytes payload, Big-Endian multi-byte integers.
type MotionFrame struct {
	Version      uint8     `json:"version"`
	Profile      uint8     `json:"profile"`
	State        uint8     `json:"state"`
	Emotion      uint8     `json:"emotion"`
	Intensity    uint8     `json:"intensity"`
	FrameSeq     uint16    `json:"frame_seq"`
	AudioSeq     uint32    `json:"audio_seq"`
	PtsMs        uint32    `json:"pts_ms"`
	Face         [52]uint8 `json:"face"`
	Head         [3]int8   `json:"head"`
	Gaze         [2]int8   `json:"gaze"`
	Viseme       [5]uint8  `json:"viseme"`
	GestureID    uint8     `json:"gesture_id"`
	GesturePhase uint8     `json:"gesture_phase"`
	Body         [8]uint8  `json:"body"`
}

// MotionFrameSize is the fixed wire size of a MotionFrame payload in bytes.
const MotionFrameSize = 87

// MotionFrameVersion is the current wire format version.
const MotionFrameVersion uint8 = 2

// Profile constants identify the blendshape layout.
const (
	ProfileARKit52  uint8 = 0
	ProfileLive2D16 uint8 = 1
	ProfileViseme15 uint8 = 2
	ProfileVendor   uint8 = 3
)

// Gesture constants identify pre-defined gesture animations.
const (
	GestureNone  uint8 = 0
	GestureNod   uint8 = 1
	GestureWave  uint8 = 2
	GestureShake uint8 = 3
	GesturePoint uint8 = 4
)

// State constants (uint8, used in MotionFrame.State).
const (
	StateIdle      uint8 = 0
	StateListening uint8 = 1
	StateThinking  uint8 = 2
	StateSpeaking  uint8 = 3
)

// Emotion constants (uint8, used in MotionFrame.Emotion).
const (
	EmotionNeutral   uint8 = 0
	EmotionHappy     uint8 = 1
	EmotionSurprised uint8 = 2
	EmotionConfused  uint8 = 3
	EmotionConcerned uint8 = 4
)

// Pack serializes a MotionFrame into the 87-byte wire format (Big-Endian).
func (f *MotionFrame) Pack() [MotionFrameSize]byte {
	var buf [MotionFrameSize]byte
	buf[0] = f.Version
	buf[1] = f.Profile
	buf[2] = f.State
	buf[3] = f.Emotion
	buf[4] = f.Intensity
	buf[5] = uint8(f.FrameSeq >> 8)
	buf[6] = uint8(f.FrameSeq)
	buf[7] = uint8(f.AudioSeq >> 24)
	buf[8] = uint8(f.AudioSeq >> 16)
	buf[9] = uint8(f.AudioSeq >> 8)
	buf[10] = uint8(f.AudioSeq)
	buf[11] = uint8(f.PtsMs >> 24)
	buf[12] = uint8(f.PtsMs >> 16)
	buf[13] = uint8(f.PtsMs >> 8)
	buf[14] = uint8(f.PtsMs)
	copy(buf[15:67], f.Face[:])
	buf[67] = uint8(f.Head[0])
	buf[68] = uint8(f.Head[1])
	buf[69] = uint8(f.Head[2])
	buf[70] = uint8(f.Gaze[0])
	buf[71] = uint8(f.Gaze[1])
	copy(buf[72:77], f.Viseme[:])
	buf[77] = f.GestureID
	buf[78] = f.GesturePhase
	copy(buf[79:87], f.Body[:])
	return buf
}

// UnpackMotionFrame deserializes a MotionFrame from wire bytes.
func UnpackMotionFrame(data []byte) (*MotionFrame, error) {
	if len(data) < MotionFrameSize {
		return nil, ErrShortMotionFrame
	}
	if data[0] != MotionFrameVersion {
		return nil, ErrBadMotionVersion
	}
	f := &MotionFrame{
		Version:   data[0],
		Profile:   data[1],
		State:     data[2],
		Emotion:   data[3],
		Intensity: data[4],
		FrameSeq:  uint16(data[5])<<8 | uint16(data[6]),
		AudioSeq:  uint32(data[7])<<24 | uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10]),
		PtsMs:     uint32(data[11])<<24 | uint32(data[12])<<16 | uint32(data[13])<<8 | uint32(data[14]),
	}
	copy(f.Face[:], data[15:67])
	f.Head[0] = int8(data[67])
	f.Head[1] = int8(data[68])
	f.Head[2] = int8(data[69])
	f.Gaze[0] = int8(data[70])
	f.Gaze[1] = int8(data[71])
	copy(f.Viseme[:], data[72:77])
	f.GestureID = data[77]
	f.GesturePhase = data[78]
	copy(f.Body[:], data[79:87])
	return f, nil
}

// ErrShortMotionFrame is returned when data is too short for a valid MotionFrame.
var ErrShortMotionFrame = errors.New("data too short for MotionFrame")

// ErrBadMotionVersion is returned when the version byte does not match MotionFrameVersion.
var ErrBadMotionVersion = errors.New("unsupported MotionFrame version")

// RenderEvent carries a message from the render stream (transcript, audio lifecycle, emotion).
// Mirrors the server-side RenderStreamMsg — same JSON wire format, single channel.
type RenderEvent struct {
	Type       string `json:"type"`                  // "transcript.delta" | "transcript.done" | "audio.done" | "input_transcript.done" | "emotion"
	Content    string `json:"content,omitempty"`     // transcript.delta: text fragment
	Transcript string `json:"transcript,omitempty"`  // transcript.done / input_transcript.done: full text
	AudioSeq   uint32 `json:"audio_seq,omitempty"`   // sequence counter
	ItemID     string `json:"item_id,omitempty"`     // audio.done: response item ID
	DurationMs int64  `json:"duration_ms,omitempty"` // audio.done: audio duration in ms
	State      string `json:"state,omitempty"`       // emotion: session state
	Emotion    string `json:"emotion,omitempty"`     // emotion: emotion label
	Intensity  uint8  `json:"intensity,omitempty"`   // emotion: 0~255
}

// VideoFrame carries a video frame. (Plan 7 placeholder)
type VideoFrame struct{}
