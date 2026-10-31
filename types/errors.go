package types

import "errors"

// Sentinel errors shared across SDK packages.
var (
	// ErrNodeNotFound is returned when the target node is not connected.
	ErrNodeNotFound = errors.New("node not found")

	// ErrNoLightConn is returned when no Light connection is available
	// and pool.adjust timed out.
	ErrNoLightConn = errors.New("tai: no light connection available")

	// ErrSessionClosed is returned by session methods after the session
	// has been closed.
	ErrSessionClosed = errors.New("tai: session closed")
)
