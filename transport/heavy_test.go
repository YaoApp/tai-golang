package transport

import (
	"net"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
)

func TestHeavyPoolAcceptClosedPool(t *testing.T) {
	pool := newHeavyPool(&pb.PoolConfig{}, nil, nil)
	pool.Close()

	// Accept on a closed pool should close the conn immediately.
	server, client := net.Pipe()
	defer server.Close()

	// Write a ConnectAck won't fail on pipe, but the pool will
	// see p.closed and close the conn.
	go pool.Accept(client, "node-1")

	// The server side should see EOF because client was closed by Accept.
	buf := make([]byte, 1)
	_, err := server.Read(buf)
	if err == nil {
		t.Fatal("expected read error on server side after closed pool Accept")
	}
}

func TestHeavyPoolDoubleClose(t *testing.T) {
	pool := newHeavyPool(&pb.PoolConfig{}, nil, nil)
	pool.Close()
	pool.Close() // should not panic
}

func TestHeavyPoolUpdateConfig(t *testing.T) {
	pool := newHeavyPool(&pb.PoolConfig{HeavyMinIdle: 2}, nil, nil)
	defer pool.Close()

	if pool.cfg.HeavyMinIdle != 2 {
		t.Fatalf("initial HeavyMinIdle = %d, want 2", pool.cfg.HeavyMinIdle)
	}

	pool.updateConfig(&pb.PoolConfig{HeavyMinIdle: 5})

	pool.mu.Lock()
	got := pool.cfg.HeavyMinIdle
	pool.mu.Unlock()

	if got != 5 {
		t.Errorf("HeavyMinIdle after update = %d, want 5", got)
	}
}

func TestHeavyPoolCloseWithConnections(t *testing.T) {
	pool := newHeavyPool(&pb.PoolConfig{}, nil, nil)

	// Manually add connections (bypassing Accept to test Close path)
	s1, c1 := net.Pipe()
	s2, c2 := net.Pipe()
	defer s1.Close()
	defer s2.Close()

	pool.mu.Lock()
	pool.conns["node-1"] = []*heavyConn{
		{conn: c1, nodeID: "node-1", idle: true, done: make(chan struct{})},
		{conn: c2, nodeID: "node-1", idle: false, done: make(chan struct{})},
	}
	pool.mu.Unlock()

	if pool.ConnCount("node-1") != 2 {
		t.Fatalf("ConnCount = %d, want 2", pool.ConnCount("node-1"))
	}

	pool.Close()

	if pool.ConnCount("node-1") != 0 {
		t.Errorf("ConnCount after Close = %d, want 0", pool.ConnCount("node-1"))
	}

	// Verify connections are closed (reads should fail)
	buf := make([]byte, 1)
	if _, err := s1.Read(buf); err == nil {
		t.Error("s1 should be closed")
	}
	if _, err := s2.Read(buf); err == nil {
		t.Error("s2 should be closed")
	}
}

func TestHeavyPoolRemoveNonexistent(t *testing.T) {
	pool := newHeavyPool(&pb.PoolConfig{}, nil, nil)
	defer pool.Close()

	_, c := net.Pipe()
	defer c.Close()

	// Remove a connection not in the pool should not panic
	pool.Remove(c)
}

func TestHeavyPoolAcceptWriteError(t *testing.T) {
	pool := newHeavyPool(&pb.PoolConfig{}, nil, nil)
	defer pool.Close()

	// Create a pipe and close the client side so WriteProtoMsg fails.
	_, client := net.Pipe()
	client.Close()

	// Accept should handle the write error gracefully.
	pool.Accept(client, "node-1")

	if pool.ConnCount("node-1") != 0 {
		t.Errorf("ConnCount = %d, want 0 after write error", pool.ConnCount("node-1"))
	}
}

func TestHeavyPoolReturnNonexistent(t *testing.T) {
	pool := newHeavyPool(&pb.PoolConfig{}, nil, nil)
	defer pool.Close()

	_, c := net.Pipe()
	defer c.Close()

	// Return a connection not in the pool should not panic
	pool.Return(c)
}
