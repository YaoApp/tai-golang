package types

// NamespaceControl is the Payload namespace of every message on the L1
// Control stream.
const NamespaceControl = "control"

// NamespaceSession is the Payload namespace for session streams.
const NamespaceSession = "session"

// ControlMsg is a message sent on the L1 Control stream. It is sent as a
// Payload with namespace NamespaceControl.
type ControlMsg struct {
	Kind string // Message kind
	Data []byte // Protobuf payload
}
