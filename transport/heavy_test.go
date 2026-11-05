package transport

import (
	"errors"
	"net"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

func newTestHeavyPool() *heavyPool {
	return newHeavyPool(&pb.PoolConfig{}, &testNodeRegistry{pool: &pb.PoolConfig{}}, &testAuth{})
}

func addIdleConn(p *heavyPool, nodeID string) (net.Conn, net.Conn) {
	client, server := net.Pipe()
	hc := &heavyConn{conn: server, nodeID: nodeID, idle: true, done: make(chan struct{})}
	p.mu.Lock()
	p.conns[nodeID] = append(p.conns[nodeID], hc)
	p.mu.Unlock()
	return client, server
}

// ---------------------------------------------------------------------------
// Pick
// ---------------------------------------------------------------------------

func TestHeavyPool_Pick_Idle(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	_, server := addIdleConn(p, "n1")
	got, err := p.Pick("n1")
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got != server {
		t.Error("Pick returned wrong connection")
	}
	if p.IdleCount("n1") != 0 {
		t.Errorf("IdleCount = %d, want 0 after Pick", p.IdleCount("n1"))
	}
}

func TestHeavyPool_Pick_NoIdle(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	_, err := p.Pick("n1")
	if err == nil {
		t.Fatal("Pick should fail when no connections exist")
	}
	if !errors.Is(err, types.ErrNoHeavyConn) {
		t.Errorf("expected ErrNoHeavyConn, got %v", err)
	}
}

func TestHeavyPool_Pick_SkipsBusy(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	addIdleConn(p, "n1")
	p.Pick("n1") // mark first as busy

	_, err := p.Pick("n1")
	if err == nil {
		t.Error("Pick should fail when all connections are busy")
	}
}

// ---------------------------------------------------------------------------
// IdleCount / ConnCount
// ---------------------------------------------------------------------------

func TestHeavyPool_IdleCount(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	addIdleConn(p, "n1")
	addIdleConn(p, "n1")

	if p.IdleCount("n1") != 2 {
		t.Errorf("IdleCount = %d, want 2", p.IdleCount("n1"))
	}
	if p.ConnCount("n1") != 2 {
		t.Errorf("ConnCount = %d, want 2", p.ConnCount("n1"))
	}

	p.Pick("n1")
	if p.IdleCount("n1") != 1 {
		t.Errorf("IdleCount after Pick = %d, want 1", p.IdleCount("n1"))
	}
	if p.ConnCount("n1") != 2 {
		t.Errorf("ConnCount unchanged = %d, want 2", p.ConnCount("n1"))
	}
}

func TestHeavyPool_IdleCount_Empty(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	if p.IdleCount("n1") != 0 {
		t.Errorf("IdleCount = %d, want 0", p.IdleCount("n1"))
	}
}

// ---------------------------------------------------------------------------
// Return
// ---------------------------------------------------------------------------

func TestHeavyPool_Return(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	_, server := addIdleConn(p, "n1")
	p.Pick("n1")

	if p.IdleCount("n1") != 0 {
		t.Fatal("precondition: conn should be busy")
	}

	p.Return(server)
	if p.IdleCount("n1") != 1 {
		t.Errorf("IdleCount after Return = %d, want 1", p.IdleCount("n1"))
	}
}

func TestHeavyPool_Return_Unknown(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()

	// Return unknown conn should not panic.
	p.Return(c)
}

// ---------------------------------------------------------------------------
// Remove
// ---------------------------------------------------------------------------

func TestHeavyPool_Remove(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	_, server := addIdleConn(p, "n1")
	p.Remove(server)

	if p.ConnCount("n1") != 0 {
		t.Errorf("ConnCount after Remove = %d, want 0", p.ConnCount("n1"))
	}
}

func TestHeavyPool_Remove_Unknown(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()

	// Remove unknown conn should not panic.
	p.Remove(c)
}

func TestHeavyPool_Remove_MiddleConn(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	_, s1 := addIdleConn(p, "n1")
	_, s2 := addIdleConn(p, "n1")
	addIdleConn(p, "n1")

	p.Remove(s2)
	if p.ConnCount("n1") != 2 {
		t.Errorf("ConnCount = %d, want 2", p.ConnCount("n1"))
	}

	// Remaining conns should still be pickable.
	got, err := p.Pick("n1")
	if err != nil {
		t.Fatalf("Pick after Remove: %v", err)
	}
	if got == s2 {
		t.Error("Pick returned removed connection")
	}
	_ = s1
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestHeavyPool_Close(t *testing.T) {
	p := newTestHeavyPool()

	addIdleConn(p, "n1")
	addIdleConn(p, "n2")

	p.Close()

	if p.ConnCount("n1") != 0 {
		t.Errorf("ConnCount n1 = %d, want 0", p.ConnCount("n1"))
	}
	if p.ConnCount("n2") != 0 {
		t.Errorf("ConnCount n2 = %d, want 0", p.ConnCount("n2"))
	}
}

func TestHeavyPool_Close_Idempotent(t *testing.T) {
	p := newTestHeavyPool()
	p.Close()
	p.Close() // should not panic
}

// ---------------------------------------------------------------------------
// UpdateConfig
// ---------------------------------------------------------------------------

func TestHeavyPool_UpdateConfig(t *testing.T) {
	p := newTestHeavyPool()
	defer p.Close()

	p.updateConfig(&pb.PoolConfig{HeavyMaxActive: 42})

	p.mu.Lock()
	got := p.cfg.HeavyMaxActive
	p.mu.Unlock()

	if got != 42 {
		t.Errorf("cfg.HeavyMaxActive = %d, want 42", got)
	}
}
