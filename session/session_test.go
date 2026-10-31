package session

import (
	"context"
	"testing"

	"github.com/yaoapp/tai-golang/types"
)

func TestNewSession(t *testing.T) {
	s := New("sess-1", "node-1")
	if s.ID() != "sess-1" {
		t.Errorf("ID() = %q, want sess-1", s.ID())
	}
	if s.NodeID() != "node-1" {
		t.Errorf("NodeID() = %q, want node-1", s.NodeID())
	}
	if s.Status() != types.SessionOpening {
		t.Errorf("Status() = %q, want opening", s.Status())
	}
}

func TestSessionClose(t *testing.T) {
	s := New("sess-2", "node-2")
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if s.Status() != types.SessionClosed {
		t.Errorf("Status() = %q, want closed", s.Status())
	}
}

func TestSessionDoubleClose(t *testing.T) {
	s := New("sess-3", "node-3")
	s.Close(context.Background())
	err := s.Close(context.Background())
	if err != types.ErrSessionClosed {
		t.Errorf("second Close = %v, want ErrSessionClosed", err)
	}
}
