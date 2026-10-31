// Package session implements session lifecycle management over Tai Light streams.
// This is a placeholder — full implementation follows the 6BCD plan.
package session

import (
	"context"
	"sync"

	"github.com/yaoapp/tai-golang/types"
)

// Session represents an active agent session on a Tai node.
// Each session occupies 2 Light streams: ctrl (bidirectional) and data (unidirectional).
type Session struct {
	id     string
	nodeID string
	status types.SessionStatus

	mu     sync.Mutex
	closed bool
}

// New creates a Session placeholder. Full wiring is deferred to the
// 6BCD session implementation plan.
func New(id, nodeID string) *Session {
	return &Session{
		id:     id,
		nodeID: nodeID,
		status: types.SessionOpening,
	}
}

// ID returns the session identifier.
func (s *Session) ID() string { return s.id }

// NodeID returns the target node.
func (s *Session) NodeID() string { return s.nodeID }

// Status returns the current lifecycle state.
func (s *Session) Status() types.SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Close terminates the session.
func (s *Session) Close(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return types.ErrSessionClosed
	}
	s.closed = true
	s.status = types.SessionClosed
	return nil
}
