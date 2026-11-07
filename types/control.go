package types

// NamespaceControl is the Payload namespace of every message on the L1
// Control stream.
const NamespaceControl = "control"

// NamespaceRemoteTool is the Payload namespace for remote tool calls
// (Tai → Yao direction, carried on the Control stream in the stub phase).
const NamespaceRemoteTool = "remote_tool"

// Remote tool control message kinds.
const (
	KindRemoteToolCall   = "remote_tool.call"
	KindRemoteToolResult = "remote_tool.result"
)

// NamespaceSession is the Payload namespace for session streams.
const NamespaceSession = "session"

// ControlMsg is a message sent on the L1 Control stream. It is sent as a
// Payload with namespace NamespaceControl.
type ControlMsg struct {
	Kind string // Message kind
	Data []byte // Protobuf payload
}
