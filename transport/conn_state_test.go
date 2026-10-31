package transport

import (
	"context"
	"testing"
	"time"
)

func TestConnStateUnauthorizedTimerClosesConnection(t *testing.T) {
	closed := make(chan struct{})
	s := NewConnState(func() { close(closed) }, 50*time.Millisecond)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("unauthorized timer did not close the connection")
	}
	if _, ok := s.Check(ConnTypeControl); ok {
		t.Error("unauthorized state must not pass check")
	}
}

func TestConnStateAuthorizeCancelsTimer(t *testing.T) {
	closed := make(chan struct{}, 1)
	s := NewConnState(func() { closed <- struct{}{} }, 50*time.Millisecond)
	s.AuthorizeAs("n1", ConnTypeControl)
	time.Sleep(200 * time.Millisecond)
	select {
	case <-closed:
		t.Fatal("authorized connection was closed by the timer")
	default:
	}
	if nodeID, ok := s.Check(ConnTypeControl); !ok || nodeID != "n1" {
		t.Errorf("check = (%q, %v), want (n1, true)", nodeID, ok)
	}
	if _, ok := s.Check(ConnTypeLight); ok {
		t.Error("check must fail for a different connection type")
	}
}

func TestConnStateAuthorizeAs(t *testing.T) {
	s := NewConnState(nil, 0)
	if !s.AuthorizeAs("n1", ConnTypeControl) {
		t.Fatal("first authorize failed")
	}
	if !s.AuthorizeAs("n1", ConnTypeControl) {
		t.Error("same authorization must be confirmed")
	}
	if s.AuthorizeAs("n2", ConnTypeControl) {
		t.Error("different node must be refused")
	}
	if s.AuthorizeAs("n1", ConnTypeLight) {
		t.Error("different type must be refused")
	}
}

func TestConnStateAfterExpiryFails(t *testing.T) {
	closed := make(chan struct{})
	s := NewConnState(func() { close(closed) }, 20*time.Millisecond)
	<-closed
	if s.AuthorizeAs("n1", ConnTypeControl) {
		t.Error("authorize after the unauthorized timer fired must fail")
	}
}

func TestConnStateCloseSoon(t *testing.T) {
	closed := make(chan struct{})
	s := NewConnState(func() { close(closed) }, 0)
	s.CloseSoon()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("closeSoon did not close the connection")
	}
}

func TestConnStateNilClose(t *testing.T) {
	s := NewConnState(nil, 0)
	s.Close() // must not panic
}

func TestConnStateFromContext(t *testing.T) {
	if ConnStateFrom(context.Background()) != nil {
		t.Error("ConnStateFrom on empty context should be nil")
	}
	s := NewConnState(nil, 0)
	ctx := WithConnState(context.Background(), s)
	if ConnStateFrom(ctx) != s {
		t.Error("ConnStateFrom should return stored state")
	}
}
