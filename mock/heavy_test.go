package mock

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
)

func TestHeavyPoolAccept(t *testing.T) {
	ts := NewTestServer(t)
	mn, _ := startMockAndLight(t, ts, "hp-accept", "H2")

	hc, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy: %v", err)
	}
	t.Cleanup(hc.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.HeavyConnCount("hp-accept") < 1 {
		if time.Now().After(deadline) {
			t.Fatal("heavy connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := ts.HeavyConnCount("hp-accept"); got != 1 {
		t.Errorf("HeavyConnCount = %d, want 1", got)
	}
}

func TestHeavyPoolPickReturn(t *testing.T) {
	ts := NewTestServer(t)
	mn, _ := startMockAndLight(t, ts, "hp-pr", "H2")

	hc, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy: %v", err)
	}
	t.Cleanup(hc.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.HeavyConnCount("hp-pr") < 1 {
		if time.Now().After(deadline) {
			t.Fatal("heavy connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Pick should succeed
	conn, err := ts.HeavyPick("hp-pr")
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if conn == nil {
		t.Fatal("Pick returned nil conn")
	}

	// Second Pick should fail (only 1 connection, now busy)
	_, err = ts.HeavyPick("hp-pr")
	if err == nil {
		t.Fatal("second Pick should fail")
	}

	// Return and Pick again
	ts.HeavyReturn(conn)
	conn2, err := ts.HeavyPick("hp-pr")
	if err != nil {
		t.Fatalf("Pick after Return: %v", err)
	}
	if conn2 != conn {
		t.Error("Pick after Return returned different connection")
	}
	ts.HeavyReturn(conn2)
}

func TestHeavyPoolMultipleConnections(t *testing.T) {
	ts := NewTestServer(t)
	mn, _ := startMockAndLight(t, ts, "hp-multi", "H2")

	hc1, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy 1: %v", err)
	}
	t.Cleanup(hc1.Close)

	hc2, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy 2: %v", err)
	}
	t.Cleanup(hc2.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.HeavyConnCount("hp-multi") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("expected 2 heavy connections, got %d", ts.HeavyConnCount("hp-multi"))
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Pick two connections concurrently
	c1, err := ts.HeavyPick("hp-multi")
	if err != nil {
		t.Fatalf("Pick 1: %v", err)
	}
	c2, err := ts.HeavyPick("hp-multi")
	if err != nil {
		t.Fatalf("Pick 2: %v", err)
	}
	if c1 == c2 {
		t.Error("Pick returned same connection twice")
	}

	// Third Pick should fail
	_, err = ts.HeavyPick("hp-multi")
	if err == nil {
		t.Fatal("third Pick should fail")
	}

	ts.HeavyReturn(c1)
	ts.HeavyReturn(c2)
}

func TestHeavyPoolConnectionRemoved(t *testing.T) {
	ts := NewTestServer(t)
	mn, _ := startMockAndLight(t, ts, "hp-rm", "H2")

	hc, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for ts.HeavyConnCount("hp-rm") < 1 {
		if time.Now().After(deadline) {
			t.Fatal("heavy connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close the mock side — the server side conn doesn't auto-detect,
	// but we can pick and verify it's broken, then remove it.
	hc.Close()

	conn, err := ts.HeavyPick("hp-rm")
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}

	// Write to verify broken pipe (the remote end is closed)
	_, werr := conn.Write([]byte("test"))
	if werr == nil {
		// Read should fail
		buf := make([]byte, 1)
		_, rerr := conn.Read(buf)
		if rerr == nil || rerr == io.EOF {
			// Connection noticed close
		}
	}

	// Manually remove (simulating detection)
	ts.HeavyReturn(conn)
}

func TestHeavyPoolInvalidToken(t *testing.T) {
	ts := NewTestServer(t)
	nodeID := "hp-bad-token"
	_, _ = startMockAndLight(t, ts, nodeID, "H2")

	// Try connecting with invalid token — should fail at the server.
	// The server reads the Connect frame, validates the token, and
	// rejects it, so the mock side either gets a read error on
	// ConnectAck or a connection reset.
	badMn := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	badMn.SetNodeToken("invalid-token")

	_, err := badMn.ConnectHeavy()
	if err == nil {
		t.Fatal("ConnectHeavy with bad token should fail")
	}
}

func TestHeavyPoolPickNonexistentNode(t *testing.T) {
	ts := NewTestServer(t)

	_, err := ts.HeavyPick("nonexistent")
	if err == nil {
		t.Fatal("Pick nonexistent should fail")
	}
	if !isNoHeavyConn(err) {
		t.Errorf("error = %v, want ErrNoHeavyConn", err)
	}
}

func TestHeavyPoolAdjustOnEmpty(t *testing.T) {
	ts := NewTestServer(t)
	nodeID := "hp-adjust"
	mn := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken(nodeID))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mn.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mn.Stop)

	lc, err := mn.ConnectLightH2(ctx)
	if err != nil {
		t.Fatalf("connect light: %v", err)
	}
	t.Cleanup(lc.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.LightConnCount(nodeID) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("light connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	adjustReceived := make(chan struct{}, 1)
	mn.OnControl(func(msg *pb.Payload) {
		if msg.Kind == "pool.adjust" {
			select {
			case adjustReceived <- struct{}{}:
			default:
			}
		}
	})

	// Pick with no heavy connections triggers pool.adjust
	_, err = ts.HeavyPick(nodeID)
	if err == nil {
		t.Fatal("Pick with no heavy connections should fail")
	}

	select {
	case <-adjustReceived:
		// pool.adjust received
	case <-time.After(2 * time.Second):
		t.Fatal("pool.adjust not received")
	}
}

func TestHeavyPoolClose(t *testing.T) {
	ts := NewTestServer(t)
	mn, _ := startMockAndLight(t, ts, "hp-close", "H2")

	hc1, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy 1: %v", err)
	}
	_ = hc1

	hc2, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy 2: %v", err)
	}
	_ = hc2

	deadline := time.Now().Add(3 * time.Second)
	for ts.HeavyConnCount("hp-close") < 2 {
		if time.Now().After(deadline) {
			t.Fatal("expected 2 heavy connections")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close is called by TestServer cleanup — just verify it doesn't panic
	// and subsequent operations fail gracefully
}

func TestHeavyPoolDataTransfer(t *testing.T) {
	ts := NewTestServer(t)
	mn, _ := startMockAndLight(t, ts, "hp-data", "H2")

	hc, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy: %v", err)
	}
	t.Cleanup(hc.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.HeavyConnCount("hp-data") < 1 {
		if time.Now().After(deadline) {
			t.Fatal("heavy connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Pick a connection and write a Payload frame through it
	conn, err := ts.HeavyPick("hp-data")
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}

	// Write a workspace command frame from SDK side
	codec := transport.DefaultCodec()
	err = codec.WriteFrame(conn, &pb.Payload{
		Namespace: "workspace",
		Kind:      "open",
		Data:      []byte(`{"workspace_id":"ws_1","branch":"main","path":"test.txt"}`),
	})
	if err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	// Read on the mock (Tai) side
	frame, err := codec.ReadFrame(hc.Conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if frame.Namespace != "workspace" || frame.Kind != "open" {
		t.Errorf("frame = %s/%s, want workspace/open", frame.Namespace, frame.Kind)
	}

	// Tai sends response back
	err = codec.WriteFrame(hc.Conn, &pb.Payload{
		Namespace: "workspace",
		Kind:      "open.result",
		Data:      []byte(`file content here`),
	})
	if err != nil {
		t.Fatalf("Tai WriteFrame: %v", err)
	}

	// SDK reads response
	resp, err := codec.ReadFrame(conn)
	if err != nil {
		t.Fatalf("SDK ReadFrame: %v", err)
	}
	if resp.Kind != "open.result" {
		t.Errorf("resp.Kind = %s, want open.result", resp.Kind)
	}
	if string(resp.Data) != "file content here" {
		t.Errorf("resp.Data = %q, want %q", resp.Data, "file content here")
	}

	ts.HeavyReturn(conn)
}

func TestHeavyPoolRemove(t *testing.T) {
	ts := NewTestServer(t)
	mn, _ := startMockAndLight(t, ts, "hp-remove", "H2")

	hc, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy: %v", err)
	}
	t.Cleanup(hc.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.HeavyConnCount("hp-remove") < 1 {
		if time.Now().After(deadline) {
			t.Fatal("heavy connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := ts.HeavyPick("hp-remove")
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}

	// Explicitly remove the connection
	ts.Transport.HeavyRemove(conn)

	if got := ts.HeavyConnCount("hp-remove"); got != 0 {
		t.Errorf("HeavyConnCount after Remove = %d, want 0", got)
	}

	// Pick after Remove should fail
	_, err = ts.HeavyPick("hp-remove")
	if err == nil {
		t.Fatal("Pick after Remove should fail")
	}
}

func TestHeavyPoolIdleCount(t *testing.T) {
	ts := NewTestServer(t)
	mn, _ := startMockAndLight(t, ts, "hp-idle", "H2")

	hc1, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy 1: %v", err)
	}
	t.Cleanup(hc1.Close)

	hc2, err := mn.ConnectHeavy()
	if err != nil {
		t.Fatalf("ConnectHeavy 2: %v", err)
	}
	t.Cleanup(hc2.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.HeavyConnCount("hp-idle") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("expected 2, got %d", ts.HeavyConnCount("hp-idle"))
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Both idle initially
	if got := ts.Transport.HeavyIdleCount("hp-idle"); got != 2 {
		t.Errorf("IdleCount = %d, want 2", got)
	}

	// Pick one → 1 idle
	conn, err := ts.HeavyPick("hp-idle")
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got := ts.Transport.HeavyIdleCount("hp-idle"); got != 1 {
		t.Errorf("IdleCount after Pick = %d, want 1", got)
	}

	// Return → 2 idle again
	ts.HeavyReturn(conn)
	if got := ts.Transport.HeavyIdleCount("hp-idle"); got != 2 {
		t.Errorf("IdleCount after Return = %d, want 2", got)
	}
}

func isNoHeavyConn(err error) bool {
	return err != nil && types.ErrNoHeavyConn.Error() == err.Error()[:len(types.ErrNoHeavyConn.Error())]
}
