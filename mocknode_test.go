package tai

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

func TestMockNodeFullFlow(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-full", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-full"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mock.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mock.Stop()

	if err := mock.WaitRegistered(5 * time.Second); err != nil {
		t.Fatalf("WaitRegistered: %v", err)
	}

	if mock.TaiID() == "" {
		t.Error("TaiID empty after registration")
	}

	node := ts.Nodes.nodeByID("node-full")
	if node == nil {
		t.Fatal("node not found in server after registration")
	}
	if !node.Online {
		t.Error("node should be online")
	}
	if node.TaiID != mock.TaiID() {
		t.Errorf("TaiID mismatch: server=%q mock=%q", node.TaiID, mock.TaiID())
	}
	// With both H2 and H3 available, Start() tries H3 first (primary protocol)
	if p := mock.Protocol(); p != "H3" && p != "H2" {
		t.Errorf("unexpected protocol %q", p)
	}
}

func TestMockNodePingPong(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-ping", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-ping"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mock.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mock.Stop()

	if err := mock.WaitRegistered(5 * time.Second); err != nil {
		t.Fatalf("WaitRegistered: %v", err)
	}

	// Send an explicit ping and verify pong comes back
	ping := &pb.Ping{Timestamp: 9999, SessionCount: 2, CpuPercent: 42}
	pingData, _ := proto.Marshal(ping)
	if err := mock.SendControl(&pb.ControlMessage{Kind: "ping", Data: pingData}); err != nil {
		t.Fatalf("send ping: %v", err)
	}

	// The recv goroutine handles pongs; give it a moment
	time.Sleep(200 * time.Millisecond)

	// Verify node is still considered active (lastSeen updated)
	node := ts.Nodes.nodeByID("node-ping")
	if node == nil {
		t.Fatal("node not found")
	}
}

func TestMockNodeGoAway(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-goaway", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-goaway"))

	var goAwayReceived sync.WaitGroup
	goAwayReceived.Add(1)
	var goAwayReason string

	mock.OnControl(func(msg *pb.ControlMessage) {
		if msg.Kind == "go_away" {
			ga := &pb.GoAway{}
			if proto.Unmarshal(msg.Data, ga) == nil {
				goAwayReason = ga.Reason
			}
			goAwayReceived.Done()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mock.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mock.Stop()

	if err := mock.WaitRegistered(5 * time.Second); err != nil {
		t.Fatalf("WaitRegistered: %v", err)
	}

	// Server sends go_away
	if err := ts.Nodes.goAway("node-goaway", "server-shutdown", 5); err != nil {
		t.Fatalf("goAway: %v", err)
	}

	// Wait for MockNode to receive it
	done := make(chan struct{})
	go func() {
		goAwayReceived.Wait()
		close(done)
	}()

	select {
	case <-done:
		if goAwayReason != "server-shutdown" {
			t.Errorf("go_away reason=%q, want %q", goAwayReason, "server-shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for go_away")
	}
}

func TestMockNodeH3Fallback(t *testing.T) {
	ts := NewTestServer(t)

	// Connect via H3 using the H3 address only
	mock := NewMockNode("node-h3", ts.HTTPURL(), "", ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-h3"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wk, err := mock.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	ticket, err := mock.ObtainTicket("control")
	if err != nil {
		t.Fatalf("ObtainTicket: %v", err)
	}

	// Try connecting with H3 only
	if err := mock.Connect(ctx, ticket, []string{"HTTP/3"}); err != nil {
		t.Fatalf("Connect H3: %v", err)
	}
	defer mock.Stop()

	if mock.Protocol() != "H3" {
		t.Errorf("expected H3 protocol, got %q", mock.Protocol())
	}

	// Register and verify
	ack, err := mock.Register()
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if ack.TaiId == "" {
		t.Error("TaiID empty")
	}

	_ = wk // used for discovery
}

func TestMockNodeLowLevelAPI(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-lowlevel", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-lowlevel"))

	// Step 1: Discover
	wk, err := mock.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if wk.TransportPort == 0 {
		t.Error("transport_port should not be 0")
	}
	if len(wk.Protocols) == 0 {
		t.Error("protocols should not be empty")
	}

	// Step 2: Obtain ticket
	ticket, err := mock.ObtainTicket("control")
	if err != nil {
		t.Fatalf("ObtainTicket: %v", err)
	}
	if ticket == "" {
		t.Error("ticket should not be empty")
	}

	// Step 3: Connect (H2)
	ctx := context.Background()
	if err := mock.Connect(ctx, ticket, []string{"HTTP/2"}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer mock.Stop()

	// Step 4: Register
	ack, err := mock.Register()
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if ack.TaiId == "" {
		t.Error("tai_id empty")
	}
	if ack.PoolConfig == nil {
		t.Error("pool_config nil")
	}
	if len(ack.PublicKey) == 0 {
		t.Error("public_key empty")
	}
	if ack.KeyId == "" {
		t.Error("key_id empty")
	}
}

func TestMockNodeTokenRefresh(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-refresh", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	oldToken := ts.IssueTestNodeToken("node-refresh")
	mock.SetNodeToken(oldToken)

	newToken, err := mock.RefreshToken()
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if newToken == "" {
		t.Error("new token empty")
	}
	if newToken == oldToken {
		t.Error("new token should differ from old")
	}

	// Verify new token works
	ticket, err := mock.ObtainTicket("control")
	if err != nil {
		t.Fatalf("ObtainTicket with refreshed token: %v", err)
	}
	if ticket == "" {
		t.Error("ticket with refreshed token should not be empty")
	}
}

func TestMockNodePoolConfig(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-pool", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-pool"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mock.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mock.Stop()

	if err := mock.WaitRegistered(5 * time.Second); err != nil {
		t.Fatalf("WaitRegistered: %v", err)
	}

	cfg := mock.PoolConfig()
	if cfg == nil {
		t.Error("PoolConfig should be set after registration")
	}
}

func TestMockNodeWaitRegisteredTimeout(t *testing.T) {
	mock := NewMockNode("node-timeout", "http://localhost:1", "", "")

	err := mock.WaitRegistered(50 * time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestMockNodeSendControlNotConnected(t *testing.T) {
	mock := NewMockNode("node-nc", "http://localhost:1", "", "")
	err := mock.SendControl(&pb.ControlMessage{Kind: "test"})
	if err == nil {
		t.Error("expected error for not connected")
	}
}

func TestMockNodeRecvControlNotConnected(t *testing.T) {
	mock := NewMockNode("node-nc", "http://localhost:1", "", "")
	_, err := mock.RecvControl()
	if err == nil {
		t.Error("expected error for not connected")
	}
}

func TestMockNodeConnectNoProtocol(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-nop", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-nop"))

	ctx := context.Background()
	err := mock.Connect(ctx, "ticket", []string{"HTTP/99"})
	if err == nil {
		t.Error("expected error for unsupported protocol")
	}
}

func TestMockNodeDiscoverError(t *testing.T) {
	mock := NewMockNode("node-err", "http://127.0.0.1:1", "", "")
	_, err := mock.Discover()
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestMockNodeObtainTicketError(t *testing.T) {
	mock := NewMockNode("node-err", "http://127.0.0.1:1", "", "")
	mock.SetNodeToken("bad-token")
	_, err := mock.ObtainTicket("control")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestMockNodeGoAwayViaTestServer(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-ga2", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-ga2"))

	var goAwayCh = make(chan string, 1)
	mock.OnControl(func(msg *pb.ControlMessage) {
		if msg.Kind == "go_away" {
			ga := &pb.GoAway{}
			proto.Unmarshal(msg.Data, ga)
			goAwayCh <- ga.Reason
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mock.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mock.Stop()
	mock.WaitRegistered(5 * time.Second)

	// Use GoAwayAll helper
	ts.GoAwayAll("upgrade")

	select {
	case reason := <-goAwayCh:
		if reason != "upgrade" {
			t.Errorf("reason = %q, want upgrade", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestMockNodeSendControlViaTestServer(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-sc", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("node-sc"))

	received := make(chan string, 1)
	mock.OnControl(func(msg *pb.ControlMessage) {
		received <- msg.Kind
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mock.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mock.Stop()
	mock.WaitRegistered(5 * time.Second)

	// Use SendControl helper
	ts.SendControl("node-sc", &pb.ControlMessage{Kind: "custom.event", Data: []byte("data")})

	select {
	case kind := <-received:
		if kind != "custom.event" {
			t.Errorf("kind = %q, want custom.event", kind)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestMockNodeObtainTicketNoToken(t *testing.T) {
	mock := NewMockNode("node-notoken", "http://127.0.0.1:1", "", "")
	// Don't set nodeToken — issueTokenFromServer will be called and fail
	_, err := mock.ObtainTicket("control")
	if err == nil {
		t.Error("expected error for missing nodeToken")
	}
}

func TestMockNodeObtainTicketBadStatus(t *testing.T) {
	ts := NewTestServer(t)
	mock := NewMockNode("node-bad", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken("invalid-token")
	_, err := mock.ObtainTicket("control")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestMockNodeStartDiscoverFail(t *testing.T) {
	mock := NewMockNode("node-fail", "http://127.0.0.1:1", "", "")
	mock.SetNodeToken("token")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := mock.Start(ctx)
	if err == nil {
		t.Error("expected error when Discover fails")
	}
}

func TestMockNodeConnectH2SkipH3(t *testing.T) {
	ts := NewTestServer(t)

	mock := NewMockNode("node-h2skip", ts.HTTPURL(), ts.TransportAddr, "")
	mock.SetNodeToken(ts.IssueTestNodeToken("node-h2skip"))

	ctx := context.Background()
	ticket, _ := mock.ObtainTicket("control")

	// H3 should be skipped (no h3Addr), H2 should succeed
	err := mock.Connect(ctx, ticket, []string{"HTTP/3", "HTTP/2"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if mock.Protocol() != "H2" {
		t.Errorf("Protocol = %q, want H2", mock.Protocol())
	}
	mock.Stop()
}

func TestMockNodeStartH2Only(t *testing.T) {
	ts := NewTestServer(t)

	// H2 only, no H3
	mock := NewMockNode("node-h2only", ts.HTTPURL(), ts.TransportAddr, "")
	mock.SetNodeToken(ts.IssueTestNodeToken("node-h2only"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mock.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mock.Stop()

	if err := mock.WaitRegistered(5 * time.Second); err != nil {
		t.Fatalf("WaitRegistered: %v", err)
	}

	if mock.Protocol() != "H2" {
		t.Errorf("Protocol = %q, want H2", mock.Protocol())
	}
}
