package main

import "time"

// Global event types.
//
// The naming follows the yao 1.0 event bus convention (dotted prefixes such as
// task./board./mail./job.). The frontend subscribes by prefix, so on("node.",
// fn) receives both node.registered and node.disconnected.
//
// The demo app's domains are node, session, workspace, and realtime.
const (
	// ---- node.* : node registration lifecycle (fired by main.go's
	// OnRegister / OnDisconnect callbacks) ----

	// EventNodeRegistered fires when a node registers.
	EventNodeRegistered = "node.registered"
	// EventNodeDisconnected fires when a node drops.
	EventNodeDisconnected = "node.disconnected"

	// ---- session.* : session lifecycle ----

	// EventSessionStatus fires on any status transition; data carries
	// sessionId and status.
	EventSessionStatus = "session.status"
	// EventSessionClosed fires when a session is removed; data carries
	// sessionId.
	EventSessionClosed = "session.closed"

	// ---- workspace.* : workspace changes (reserved) ----

	// EventWorkspaceChange fires when workspace files or metadata change.
	EventWorkspaceChange = "workspace.change"

	// ---- realtime.* : realtime channel lifecycle (reserved) ----

	// EventRealtimeStarted fires when a realtime session starts.
	EventRealtimeStarted = "realtime.started"
	// EventRealtimeStopped fires when a realtime session stops.
	EventRealtimeStopped = "realtime.stopped"
	// EventRealtimeError fires when a realtime session fails.
	EventRealtimeError = "realtime.error"
)

// EventPing is the message type used for heartbeat frames. Clients ignore it.
const EventPing = "ping"

// Event is the single message format on the global event stream.
//
// Matches yao 1.0's /v1/events: {type, timestamp, data}. The demo app has no
// internal fields yet; the convention is kept here so future fields can be
// stripped the same way yao strips __yao_ prefixed keys.
type Event struct {
	// Type is the event type, e.g. "node.registered".
	Type string `json:"type"`
	// Timestamp is the emission time as Unix milliseconds.
	Timestamp int64 `json:"timestamp"`
	// Data is the payload; its fields are determined by Type.
	Data map[string]any `json:"data,omitempty"`
}

// newEvent builds an event stamped with the current time.
//
// @param typ event type
// @param data payload, may be nil
// @returns the event
func newEvent(typ string, data map[string]any) Event {
	if data == nil {
		data = map[string]any{}
	}
	return Event{
		Type:      typ,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
}
