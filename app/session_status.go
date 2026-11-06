package main

// SessionStatus is the lifecycle state of a session as tracked by the demo app.
//
// These states were previously bare strings scattered across the handler and
// store (a dozen call sites), which made typos likely and gave callers no way
// to constrain the value set. Typing them makes the state machine explicit:
// which states are final and which can be recovered.
type SessionStatus string

const (
	// StatusStarting means the session is being established.
	//
	// Reserved: Create only registers a session after OpenSession succeeds, so
	// no session is ever observable in this state. It exists so a future
	// asynchronous create path has a status to report.
	StatusStarting SessionStatus = "starting"
	// StatusRunning means the session is connected and healthy.
	StatusRunning SessionStatus = "running"
	// StatusDisconnected means the transport dropped and the SDK is
	// attempting an automatic reconnect.
	StatusDisconnected SessionStatus = "disconnected"
	// StatusRecoveryGap means the disconnect lasted long enough that the
	// recovery buffer overflowed, so some events were lost.
	StatusRecoveryGap SessionStatus = "recovery_gap"
	// StatusResumeError means the automatic reconnect gave up and a manual
	// resume is required.
	StatusResumeError SessionStatus = "resume_error"
	// StatusResuming means a manual resume is in flight.
	StatusResuming SessionStatus = "resuming"
	// StatusClosed means the user closed the session.
	StatusClosed SessionStatus = "closed"
	// StatusTerminated means the session ended without being asked to
	// (node crash, lost connection, and the like).
	StatusTerminated SessionStatus = "terminated"
)

// Terminal reports whether the status is final and the session can no longer
// be recovered.
//
// @returns true when the session is closed or terminated
func (s SessionStatus) Terminal() bool {
	return s == StatusClosed || s == StatusTerminated
}

// CanResume reports whether a manual resume can recover the session.
//
// Only transport-level failures are worth retrying. A closed or terminated
// session has no remote state left to reattach to.
//
// @returns true for the three recoverable transport states
func (s SessionStatus) CanResume() bool {
	return s == StatusDisconnected || s == StatusRecoveryGap || s == StatusResumeError
}
