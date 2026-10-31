package transport

import (
	"context"
	"sync"
	"time"
)

// DefaultUnauthTimeout is how long a connection may stay unauthorized after
// its transport handshake completes.
const DefaultUnauthTimeout = 5 * time.Second

// closeGrace delays closing a connection after an unauthorized response.
const closeGrace = 200 * time.Millisecond

// Connection types carried by the first message of a connection.
const (
	ConnTypeControl = "control"
	ConnTypeLight   = "light"
	ConnTypeHeavy   = "heavy"
)

type connStateKey struct{}

// ConnState is the authorization state of one transport connection.
type ConnState struct {
	mu         sync.Mutex
	authorized bool
	expired    bool
	nodeID     string
	connType   string
	closeFn    func()
	timer      *time.Timer
}

// NewConnState creates the state for a freshly accepted connection.
func NewConnState(closeFn func(), timeout time.Duration) *ConnState {
	s := &ConnState{closeFn: closeFn}
	if timeout > 0 {
		s.timer = time.AfterFunc(timeout, func() {
			s.mu.Lock()
			authorized := s.authorized
			if !authorized {
				s.expired = true
			}
			s.mu.Unlock()
			if !authorized {
				s.Close()
			}
		})
	}
	return s
}

// AuthorizeAs authorizes an unauthorized connection for nodeID and connType.
func (s *ConnState) AuthorizeAs(nodeID, connType string) bool {
	s.mu.Lock()
	if s.authorized {
		same := s.nodeID == nodeID && s.connType == connType
		s.mu.Unlock()
		return same
	}
	if s.expired {
		s.mu.Unlock()
		return false
	}
	s.authorized = true
	s.nodeID = nodeID
	s.connType = connType
	timer := s.timer
	s.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	return true
}

// Check reports the node ID when the connection is authorized for connType.
func (s *ConnState) Check(connType string) (nodeID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorized || s.connType != connType {
		return "", false
	}
	return s.nodeID, true
}

// Close closes the underlying connection.
func (s *ConnState) Close() {
	if s.closeFn != nil {
		s.closeFn()
	}
}

// CloseSoon closes the underlying connection after closeGrace.
func (s *ConnState) CloseSoon() {
	time.AfterFunc(closeGrace, s.Close)
}

// WithConnState stores a ConnState in the context.
func WithConnState(ctx context.Context, s *ConnState) context.Context {
	return context.WithValue(ctx, connStateKey{}, s)
}

// ConnStateFrom returns the connection state stored in ctx, or nil.
func ConnStateFrom(ctx context.Context) *ConnState {
	s, _ := ctx.Value(connStateKey{}).(*ConnState)
	return s
}
