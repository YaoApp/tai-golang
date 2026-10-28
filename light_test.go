package tai

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// testLightServer returns a started TestServer and its lightPool.
func testLightServer(t *testing.T) (*TestServer, *lightPool) {
	t.Helper()
	ts := NewTestServer(t)
	s := ts.Server.(*server)
	return ts, s.light
}

// startMockAndLight registers a mock node and opens a Light connection.
func startMockAndLight(t *testing.T, ts *TestServer, nodeID, proto string) (*MockNode, *MockLightConn) {
	t.Helper()
	mock := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken(nodeID))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mock.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mock.Stop)

	var lc *MockLightConn
	var err error
	connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer connectCancel()
	switch proto {
	case "H3":
		lc, err = mock.ConnectLightH3(connectCtx)
	case "H2":
		lc, err = mock.ConnectLightH2(connectCtx)
	default:
		t.Fatalf("unsupported protocol: %s", proto)
	}
	if err != nil {
		t.Fatalf("connect light %s: %v", proto, err)
	}
	t.Cleanup(lc.Close)

	// Wait for the connection to appear in the pool
	deadline := time.Now().Add(3 * time.Second)
	s := ts.Server.(*server)
	for s.light.connCount(nodeID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("light connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	return mock, lc
}

func TestLightPoolOpenStreamH3(t *testing.T) {
	ts, pool := testLightServer(t)
	_, lc := startMockAndLight(t, ts, "lp-h3", "H3")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	openData, _ := proto.Marshal(&pb.SessionOpen{SessionId: "s1", NodeId: "lp-h3"})
	stream, err := pool.OpenStream(ctx, "lp-h3", &pb.Payload{
		Namespace: NamespaceSession,
		Kind:      "open",
		Data:      openData,
	})
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer stream.Close()

	// MockNode should accept the stream and see the first frame
	accepted, err := lc.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	if accepted.FirstFrame.Namespace != NamespaceSession || accepted.FirstFrame.Kind != "open" {
		t.Errorf("first frame = %s/%s, want session/open", accepted.FirstFrame.Namespace, accepted.FirstFrame.Kind)
	}

	// Bidirectional data exchange
	go func() {
		stream.Write([]byte("hello-from-yao"))
		stream.w.Close()
	}()

	data, _ := io.ReadAll(accepted)
	if string(data) != "hello-from-yao" {
		t.Errorf("mock received %q", data)
	}

	// MockNode writes back
	accepted.Write([]byte("hello-from-tai"))
	accepted.w.Close()

	reply, _ := io.ReadAll(stream)
	if string(reply) != "hello-from-tai" {
		t.Errorf("yao received %q", reply)
	}
}

func TestLightPoolOpenStreamH2(t *testing.T) {
	ts, pool := testLightServer(t)
	_, lc := startMockAndLight(t, ts, "lp-h2", "H2")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	openData, _ := proto.Marshal(&pb.SessionOpen{SessionId: "s1", NodeId: "lp-h2"})
	stream, err := pool.OpenStream(ctx, "lp-h2", &pb.Payload{
		Namespace: NamespaceSession,
		Kind:      "open",
		Data:      openData,
	})
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer stream.Close()

	accepted, err := lc.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	if accepted.FirstFrame.Namespace != NamespaceSession || accepted.FirstFrame.Kind != "open" {
		t.Errorf("first frame = %s/%s, want session/open", accepted.FirstFrame.Namespace, accepted.FirstFrame.Kind)
	}

	// MockNode writes reply data
	accepted.Write([]byte("h2-reply"))
	accepted.w.Close()

	reply, _ := io.ReadAll(stream)
	if string(reply) != "h2-reply" {
		t.Errorf("yao received %q", reply)
	}
}

func TestLightPoolPicksLeastBusy(t *testing.T) {
	ts, pool := testLightServer(t)
	nodeID := "lp-busy"
	mock := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken(nodeID))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mock.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mock.Stop)

	// Open 2 Light connections
	lc1, err := mock.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect light 1: %v", err)
	}
	t.Cleanup(lc1.Close)

	lc2, err := mock.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect light 2: %v", err)
	}
	t.Cleanup(lc2.Close)

	deadline := time.Now().Add(3 * time.Second)
	for pool.connCount(nodeID) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("expected 2 light connections")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Open a stream on each — should distribute
	openData, _ := proto.Marshal(&pb.SessionOpen{SessionId: "x", NodeId: nodeID})
	s1, err := pool.OpenStream(ctx, nodeID, &pb.Payload{Namespace: NamespaceSession, Kind: "open", Data: openData})
	if err != nil {
		t.Fatalf("OpenStream 1: %v", err)
	}

	s2, err := pool.OpenStream(ctx, nodeID, &pb.Payload{Namespace: NamespaceSession, Kind: "open", Data: openData})
	if err != nil {
		t.Fatalf("OpenStream 2: %v", err)
	}

	// Each connection should have exactly 1 active stream
	pool.mu.Lock()
	conns := pool.conns[nodeID]
	if len(conns) != 2 {
		t.Fatalf("pool has %d connections, want 2", len(conns))
	}
	a1 := conns[0].active.Load()
	a2 := conns[1].active.Load()
	pool.mu.Unlock()

	if a1 != 1 || a2 != 1 {
		t.Errorf("active counts = (%d, %d), want (1, 1)", a1, a2)
	}

	// Close MockNode write sides so SDK-side drain completes, then close
	// SDK streams. Order: mock.w.Close → s.Close (which drains mock's EOF).
	closeMockWrites := func(lc *MockLightConn) {
		for {
			shortCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			ms, err := lc.AcceptStream(shortCtx)
			cancel()
			if err != nil {
				return
			}
			ms.w.Close()
		}
	}
	closeMockWrites(lc1)
	closeMockWrites(lc2)

	s1.Close()
	s2.Close()
}

func TestLightPoolAdjustOnFull(t *testing.T) {
	ts, pool := testLightServer(t)
	nodeID := "lp-adjust"
	mock := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken(nodeID))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mock.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mock.Stop)

	// Set max_streams to 1 so one stream fills the connection
	pool.mu.Lock()
	pool.cfg = &pb.PoolConfig{LightMaxStreams: 1}
	pool.mu.Unlock()

	lc1, err := mock.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect light: %v", err)
	}
	t.Cleanup(lc1.Close)

	deadline := time.Now().Add(3 * time.Second)
	for pool.connCount(nodeID) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("expected 1 light connection")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Open a stream — fills the only connection
	openData, _ := proto.Marshal(&pb.SessionOpen{SessionId: "x", NodeId: nodeID})
	s1, err := pool.OpenStream(ctx, nodeID, &pb.Payload{Namespace: NamespaceSession, Kind: "open", Data: openData})
	if err != nil {
		t.Fatalf("OpenStream 1: %v", err)
	}

	// Capture pool.adjust via Control
	adjustReceived := make(chan struct{}, 1)
	mock.OnControl(func(msg *pb.Payload) {
		if msg.Kind == "pool.adjust" {
			select {
			case adjustReceived <- struct{}{}:
			default:
			}
		}
	})

	// Try to open another stream — connection is full, should trigger pool.adjust
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shortCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := pool.OpenStream(shortCtx, nodeID, &pb.Payload{Namespace: NamespaceSession, Kind: "open", Data: openData})
		if err == nil {
			t.Error("expected OpenStream to fail (timeout)")
		}
	}()

	// Verify pool.adjust was sent
	select {
	case <-adjustReceived:
		// Good
	case <-time.After(2 * time.Second):
		t.Error("pool.adjust not received")
	}

	wg.Wait()

	// Close MockNode write side so SDK drain completes.
	shortCtx2, shortCancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
	ms, err := lc1.AcceptStream(shortCtx2)
	shortCancel2()
	if err == nil {
		ms.w.Close()
	}
	s1.Close()
}

func TestLightPoolConnectionRemoved(t *testing.T) {
	ts, pool := testLightServer(t)
	nodeID := "lp-rm"
	mock := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken(nodeID))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mock.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mock.Stop)

	lc, err := mock.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect light: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for pool.connCount(nodeID) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("expected 1 light connection")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close the Light connection
	lc.Close()

	// Pool should remove it
	deadline = time.Now().Add(3 * time.Second)
	for pool.connCount(nodeID) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("light connection not removed from pool")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLightStreamDrainEOF(t *testing.T) {
	ts, pool := testLightServer(t)
	_, lc := startMockAndLight(t, ts, "lp-drain", "H3")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	openData, _ := proto.Marshal(&pb.SessionOpen{SessionId: "d1", NodeId: "lp-drain"})
	stream, err := pool.OpenStream(ctx, "lp-drain", &pb.Payload{
		Namespace: NamespaceSession,
		Kind:      "open",
		Data:      openData,
	})
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	accepted, err := lc.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}

	// Check active count
	pool.mu.Lock()
	conns := pool.conns["lp-drain"]
	if len(conns) == 0 {
		t.Fatal("no connections in pool")
	}
	conn := conns[0]
	pool.mu.Unlock()

	if conn.active.Load() != 1 {
		t.Errorf("active = %d before close, want 1", conn.active.Load())
	}

	// Close the accepted stream on MockNode side
	accepted.Write([]byte("tail"))
	accepted.w.Close()

	// Close the LightStream — should drain and decrement active
	stream.Close()

	if conn.active.Load() != 0 {
		t.Errorf("active = %d after close, want 0 (drain EOF failed)", conn.active.Load())
	}
}

func TestLightPoolCloseIdempotent(t *testing.T) {
	ts, pool := testLightServer(t)
	_ = ts // keep server alive

	// Calling close twice must not panic
	pool.close()
	pool.close()
}

func TestLightPoolCloseRaceRetry(t *testing.T) {
	ts, pool := testLightServer(t)
	nodeID := "lp-retry"
	mock := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken(nodeID))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mock.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mock.Stop)

	// Open 2 Light connections
	lc1, err := mock.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect light 1: %v", err)
	}
	t.Cleanup(lc1.Close)

	lc2, err := mock.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect light 2: %v", err)
	}
	t.Cleanup(lc2.Close)

	deadline := time.Now().Add(3 * time.Second)
	for pool.connCount(nodeID) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("expected 2 light connections")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close the first connection — its streams should fail and OpenStream
	// should retry on the second connection.
	lc1.Close()
	deadline = time.Now().Add(3 * time.Second)
	for pool.connCount(nodeID) > 1 {
		if time.Now().After(deadline) {
			t.Fatal("connection 1 not removed from pool")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Open a stream — should succeed on the remaining connection
	openData, _ := proto.Marshal(&pb.SessionOpen{SessionId: "retry", NodeId: nodeID})
	stream, err := pool.OpenStream(ctx, nodeID, &pb.Payload{
		Namespace: NamespaceSession,
		Kind:      "open",
		Data:      openData,
	})
	if err != nil {
		t.Fatalf("OpenStream after retry: %v", err)
	}

	accepted, err := lc2.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	if accepted.FirstFrame.Kind != "open" {
		t.Errorf("first frame kind = %q, want open", accepted.FirstFrame.Kind)
	}

	accepted.w.Close()
	stream.Close()
}

func TestLightPoolUpdateConfig(t *testing.T) {
	ts, pool := testLightServer(t)
	_ = ts

	newCfg := &pb.PoolConfig{LightMaxStreams: 128, LightMinConns: 4, LightMaxConns: 16}
	pool.updateConfig(newCfg)

	pool.mu.Lock()
	got := pool.cfg
	pool.mu.Unlock()

	if got.LightMaxStreams != 128 {
		t.Errorf("LightMaxStreams = %d, want 128", got.LightMaxStreams)
	}
	if pool.maxStreams() != 128 {
		t.Errorf("maxStreams() = %d, want 128", pool.maxStreams())
	}
}

func TestLightPoolMaxStreamsDefault(t *testing.T) {
	ts, pool := testLightServer(t)
	_ = ts

	// With nil cfg, maxStreams should return default 256
	pool.mu.Lock()
	pool.cfg = nil
	pool.mu.Unlock()

	if pool.maxStreams() != 256 {
		t.Errorf("maxStreams() = %d, want 256 (default)", pool.maxStreams())
	}
}

func TestLightPoolAddConnAfterClose(t *testing.T) {
	ts, pool := testLightServer(t)
	_ = ts
	pool.close()

	lc := &lightConn{nodeID: "closed-node", proto: "H3", pool: pool}
	ok := pool.addConn(lc)
	if ok {
		t.Error("addConn after close should return false")
	}
}

func TestLightStreamHasResponse(t *testing.T) {
	ts, pool := testLightServer(t)
	nodeID := "has-resp"
	mock := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken(nodeID))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mock.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mock.Stop)

	lc, err := mock.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(lc.Close)

	deadline := time.Now().Add(3 * time.Second)
	for pool.connCount(nodeID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no connection")
		}
		time.Sleep(10 * time.Millisecond)
	}

	openData, _ := proto.Marshal(&pb.SessionOpen{SessionId: "hr1", NodeId: nodeID})
	stream, err := pool.OpenStream(ctx, nodeID, &pb.Payload{
		Namespace: NamespaceSession,
		Kind:      "open",
		Data:      openData,
	})
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	if stream.HasResponse() {
		t.Error("HasResponse should be false before Tai replies")
	}

	accepted, err := lc.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	accepted.Write([]byte("resp"))
	accepted.w.Close()

	buf := make([]byte, 4)
	n, _ := stream.Read(buf)
	if string(buf[:n]) != "resp" {
		t.Errorf("read = %q, want resp", buf[:n])
	}

	if !stream.HasResponse() {
		t.Error("HasResponse should be true after Tai replied")
	}
	stream.Close()
}

// TestLightStreamDoubleClose verifies that calling Close twice does not
// double-decrement the active counter (P1-3 fix).
func TestLightStreamDoubleClose(t *testing.T) {
	ts, pool := testLightServer(t)
	_, lc := startMockAndLight(t, ts, "dbl-close", "H3")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	envelope := &pb.Payload{Namespace: "test", Kind: "double-close"}
	stream, err := pool.OpenStream(ctx, "dbl-close", envelope)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	accepted, err := lc.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	accepted.Write([]byte("ok"))
	accepted.w.Close()

	buf := make([]byte, 2)
	stream.Read(buf)

	// First Close
	stream.Close()
	conn := stream.conn
	activeAfterFirst := conn.active.Load()

	// Second Close — must not decrement again
	stream.Close()
	activeAfterSecond := conn.active.Load()

	if activeAfterSecond != activeAfterFirst {
		t.Errorf("double Close decremented active: first=%d second=%d",
			activeAfterFirst, activeAfterSecond)
	}
}
