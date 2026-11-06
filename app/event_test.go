package main

import (
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/types"
)

func TestEventPublishSubscribe(t *testing.T) {
	s := NewStore()
	ch := s.SubscribeEvents()
	defer s.UnsubscribeEvents(ch)

	s.AddNode(fakeNode{id: "n1", taiID: "t1"})

	select {
	case ev := <-ch:
		if ev.Type != EventNodeRegistered {
			t.Errorf("Type = %q, want %q", ev.Type, EventNodeRegistered)
		}
		if ev.Data["nodeId"] != "n1" {
			t.Errorf("nodeId = %v, want n1", ev.Data["nodeId"])
		}
		if ev.Timestamp == 0 {
			t.Error("Timestamp was not set")
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

// A slow subscriber must have events dropped rather than block publishing.
func TestEventSlowSubscriberDropped(t *testing.T) {
	s := NewStore()
	slow := s.SubscribeEvents()
	defer s.UnsubscribeEvents(slow)
	// Never read from slow, so it fills up.

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ { // far beyond the buffer size of 64
			s.publish(EventNodeRegistered, map[string]any{"i": i})
		}
	}()

	select {
	case <-done:
		// Returned normally, so a full channel did not block.
	case <-time.After(2 * time.Second):
		t.Fatal("publishing was blocked by a slow subscriber")
	}
}

// Every subscriber receives the same event.
func TestEventMultipleSubscribers(t *testing.T) {
	s := NewStore()
	a := s.SubscribeEvents()
	b := s.SubscribeEvents()
	defer s.UnsubscribeEvents(a)
	defer s.UnsubscribeEvents(b)

	s.RemoveNode("n1")

	for i, ch := range []chan Event{a, b} {
		select {
		case ev := <-ch:
			if ev.Type != EventNodeDisconnected {
				t.Errorf("subscriber %d: Type = %q, want %q", i, ev.Type, EventNodeDisconnected)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d received no event", i)
		}
	}
}

// Unsubscribing closes the channel and later publishes must not panic.
func TestEventUnsubscribe(t *testing.T) {
	s := NewStore()
	ch := s.SubscribeEvents()
	s.UnsubscribeEvents(ch)

	if _, ok := <-ch; ok {
		t.Error("channel was not closed after unsubscribe")
	}

	s.publish(EventNodeRegistered, map[string]any{"nodeId": "x"})
}

// Double unsubscribe must not panic on a second close.
func TestEventUnsubscribeTwice(t *testing.T) {
	s := NewStore()
	ch := s.SubscribeEvents()
	s.UnsubscribeEvents(ch)
	s.UnsubscribeEvents(ch) // must be a no-op, not a double close
}

// Publishing while another goroutine unsubscribes must never panic.
//
// Regression test for the close-outside-lock window: publish holds subMu while
// reading the subscriber map, so UnsubscribeEvents must close the channel under
// the same lock, otherwise a send can land on a closed channel.
func TestEventPublishUnsubscribeRace(t *testing.T) {
	s := NewStore()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Publisher.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.publish(EventNodeRegistered, map[string]any{"nodeId": "n"})
			}
		}
	}()

	// Churn subscribers: subscribe and drop them while publishing continues.
	for i := 0; i < 200; i++ {
		func() {
			ch := s.SubscribeEvents()
			defer s.UnsubscribeEvents(ch)
			// Drain a little so the buffer does not matter.
			select {
			case <-ch:
			default:
			}
		}()
	}

	close(stop)
	wg.Wait()
}

// Every status change publishes a session.status event.
//
// SessionInfo is built directly with a nil Session: the broadcast does not
// depend on the session instance.
func TestSessionStatusPublishes(t *testing.T) {
	s := NewStore()
	ch := s.SubscribeEvents()
	defer s.UnsubscribeEvents(ch)

	info := &SessionInfo{ID: "s1", Status: StatusRunning}
	s.AddSession(info)

	info.SetStatus(StatusDisconnected, s)

	select {
	case ev := <-ch:
		if ev.Type != EventSessionStatus {
			t.Errorf("Type = %q, want %q", ev.Type, EventSessionStatus)
		}
		if ev.Data["sessionId"] != "s1" {
			t.Errorf("sessionId = %v, want s1", ev.Data["sessionId"])
		}
		if ev.Data["status"] != string(StatusDisconnected) {
			t.Errorf("status = %v, want %q", ev.Data["status"], StatusDisconnected)
		}
	case <-time.After(time.Second):
		t.Fatal("no session.status event received")
	}
}

// Removing a session publishes session.closed.
func TestSessionClosedPublishes(t *testing.T) {
	s := NewStore()
	ch := s.SubscribeEvents()
	defer s.UnsubscribeEvents(ch)

	s.AddSession(&SessionInfo{ID: "s1", Status: StatusRunning})
	s.RemoveSession("s1")

	select {
	case ev := <-ch:
		if ev.Type != EventSessionClosed {
			t.Errorf("Type = %q, want %q", ev.Type, EventSessionClosed)
		}
	case <-time.After(time.Second):
		t.Fatal("no session.closed event received")
	}
}

// Every event type must start with one of the four prefixes so the frontend can
// subscribe by group.
func TestEventTypePrefixes(t *testing.T) {
	groups := map[string][]string{
		"node.":      {EventNodeRegistered, EventNodeDisconnected},
		"session.":   {EventSessionStatus, EventSessionClosed},
		"workspace.": {EventWorkspaceChange},
		"realtime.":  {EventRealtimeStarted, EventRealtimeStopped, EventRealtimeError},
	}
	for prefix, types := range groups {
		for _, typ := range types {
			if len(typ) <= len(prefix) || typ[:len(prefix)] != prefix {
				t.Errorf("%q does not start with %q", typ, prefix)
			}
		}
	}
}

// The Session field is read and written concurrently, so access must be race
// free. Run under go test -race: one goroutine reads via sess() while another
// swaps via replaceSession. Any direct field access outside the accessors makes
// this test report a race.
func TestSessionInfoConcurrentAccess(t *testing.T) {
	info := &SessionInfo{ID: "s1", Status: StatusRunning}

	const rounds = 2000
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			_ = info.sess()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			info.mu.Lock()
			info.Session = nil
			info.mu.Unlock()
			info.replaceSession(nil)
		}
	}()

	wg.Wait()
}

// replaceSession returns the displaced instance and a deleted flag.
//
// nil stands in for a real Session, which cannot be built cheaply in a unit
// test; the swap semantics are what matters here.
func TestReplaceSessionReturnsOld(t *testing.T) {
	info := &SessionInfo{ID: "s1"}

	old, deleted := info.replaceSession(nil)
	if deleted {
		t.Error("a live record must not report deleted")
	}
	if old != nil {
		t.Errorf("first swap should return nil, got %v", old)
	}

	again, _ := info.replaceSession(nil)
	if again != nil {
		t.Errorf("second swap should return nil, got %v", again)
	}
}

// A deleted record refuses replacement so a late Resume cannot install an
// ownerless instance.
//
// Regression test for the TOCTOU window between Resume finishing and a
// concurrent Delete.
func TestReplaceSessionRefusedAfterDelete(t *testing.T) {
	s := NewStore()
	info := &SessionInfo{ID: "s1", Status: StatusRunning}
	s.AddSession(info)

	s.RemoveSession("s1")

	old, deleted := info.replaceSession(nil)
	if !deleted {
		t.Fatal("replaceSession must report deleted after RemoveSession")
	}
	if old != nil {
		t.Errorf("a refused swap must not return the old instance, got %v", old)
	}
}

// fakeNode implements the parts of types.RemoteNode the store uses.
//
// The embedded interface panics on unused methods; the store only calls Info.
type fakeNode struct {
	types.RemoteNode
	id    string
	taiID string
}

func (f fakeNode) Info() *types.Node {
	return &types.Node{ID: f.id, TaiID: f.taiID}
}
