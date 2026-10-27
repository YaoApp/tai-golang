package tai

import (
	"context"
	"sync"
	"time"
)

// DefaultUnauthTimeout is how long a connection may stay unauthorized after
// its transport handshake completes. The first message must carry a valid
// node_token before it expires.
const DefaultUnauthTimeout = 5 * time.Second

// closeGrace delays closing a connection after an unauthorized response so
// the response frame reaches the peer first. HTTP/2 and HTTP/3 handlers
// cannot flush-and-wait on the underlying connection.
const closeGrace = 200 * time.Millisecond

// Connection types carried by the first message of a connection.
const (
	connTypeControl = "control"
	connTypeLight   = "light"
	connTypeHeavy   = "heavy"
)

type connStateKey struct{}

// connState is the authorization state of one transport connection (a TCP
// connection or a QUIC connection). It is created when the connection is
// accepted, stored in the connection's context, and read by handlers via the
// request context. Once authorized it lives as long as the connection, so
// streams opened later on the same connection need no credentials.
type connState struct {
	mu         sync.Mutex
	authorized bool
	expired    bool // the unauthorized timer fired; authorize must fail
	nodeID     string
	connType   string
	closeFn    func()
	timer      *time.Timer
}

// newConnState creates the state for a freshly accepted connection and starts
// the unauthorized timer: closeFn runs when timeout elapses before authorize.
func newConnState(closeFn func(), timeout time.Duration) *connState {
	s := &connState{closeFn: closeFn}
	if timeout > 0 {
		s.timer = time.AfterFunc(timeout, func() {
			s.mu.Lock()
			authorized := s.authorized
			if !authorized {
				s.expired = true
			}
			s.mu.Unlock()
			if !authorized {
				s.close()
			}
		})
	}
	return s
}

// authorizeAs authorizes an unauthorized connection for nodeID and connType
// and cancels the unauthorized timer. An already authorized connection is
// confirmed only when node and type match; otherwise it returns false.
func (s *connState) authorizeAs(nodeID, connType string) bool {
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

// check reports the node ID when the connection is authorized for connType.
func (s *connState) check(connType string) (nodeID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorized || s.connType != connType {
		return "", false
	}
	return s.nodeID, true
}

// close closes the underlying connection.
func (s *connState) close() {
	if s.closeFn != nil {
		s.closeFn()
	}
}

// closeSoon closes the underlying connection after closeGrace.
func (s *connState) closeSoon() {
	time.AfterFunc(closeGrace, s.close)
}

func withConnState(ctx context.Context, s *connState) context.Context {
	return context.WithValue(ctx, connStateKey{}, s)
}

// connStateFrom returns the connection state stored in ctx, or nil.
func connStateFrom(ctx context.Context) *connState {
	s, _ := ctx.Value(connStateKey{}).(*connState)
	return s
}
