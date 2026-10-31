package mock

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

func TestMockNodeFullFlow(t *testing.T) {
	ts := NewTestServer(t)

	mn := NewMockNode("node-full", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken("node-full"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mn.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mn.Stop()

	if err := mn.WaitRegistered(5 * time.Second); err != nil {
		t.Fatalf("WaitRegistered: %v", err)
	}

	if mn.TaiID() == "" {
		t.Error("TaiID empty after registration")
	}

	node := ts.Nodes.NodeByID("node-full")
	if node == nil {
		t.Fatal("node not found in server after registration")
	}
	if !node.Online {
		t.Error("node should be online")
	}
	if p := mn.Protocol(); p != "H3" && p != "H2" {
		t.Errorf("unexpected protocol %q", p)
	}
}

func TestMockNodeGoAway(t *testing.T) {
	ts := NewTestServer(t)

	mn := NewMockNode("node-goaway", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken("node-goaway"))

	var goAwayReceived sync.WaitGroup
	goAwayReceived.Add(1)
	var goAwayReason string

	mn.OnControl(func(msg *pb.Payload) {
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

	if err := mn.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mn.Stop()

	if err := mn.WaitRegistered(5 * time.Second); err != nil {
		t.Fatalf("WaitRegistered: %v", err)
	}

	ts.Nodes.GoAway("node-goaway", "server-shutdown", 5)

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

func TestMockNodeLowLevelAPI(t *testing.T) {
	ts := NewTestServer(t)

	mn := NewMockNode("node-lowlevel", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken("node-lowlevel"))

	wk, err := mn.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if wk.TransportPort == 0 {
		t.Error("transport_port should not be 0")
	}

	ctx := context.Background()
	if err := mn.Connect(ctx, []string{"HTTP/2"}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer mn.Stop()

	ack, err := mn.Register()
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if ack.TaiId == "" {
		t.Error("tai_id empty")
	}
	if ack.PoolConfig == nil {
		t.Error("pool_config nil")
	}
}

func TestMockNodeTokenRefresh(t *testing.T) {
	ts := NewTestServer(t)

	mn := NewMockNode("node-refresh", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	oldToken := ts.IssueTestNodeToken("node-refresh")
	mn.SetNodeToken(oldToken)

	newToken, err := mn.RefreshToken()
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if newToken == "" || newToken == oldToken {
		t.Error("new token should differ from old")
	}
}

func TestMockNodeWaitRegisteredTimeout(t *testing.T) {
	mn := NewMockNode("node-timeout", "http://localhost:1", "", "")

	err := mn.WaitRegistered(50 * time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestMockNodeSendControlNotConnected(t *testing.T) {
	mn := NewMockNode("node-nc", "http://localhost:1", "", "")
	err := mn.SendControl(&pb.Payload{Namespace: types.NamespaceControl, Kind: "test"})
	if err == nil {
		t.Error("expected error for not connected")
	}
}

func TestMockNodeConnectNoProtocol(t *testing.T) {
	ts := NewTestServer(t)
	mn := NewMockNode("node-nop", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken("node-nop"))

	ctx := context.Background()
	err := mn.Connect(ctx, []string{"HTTP/99"})
	if err == nil {
		t.Error("expected error for unsupported protocol")
	}
}

func TestMockNodeConnectNoToken(t *testing.T) {
	mn := NewMockNode("node-notoken", "http://127.0.0.1:1", "", "")
	err := mn.Connect(context.Background(), []string{"HTTP/2"})
	if err == nil {
		t.Error("expected error for missing nodeToken")
	}
}

func TestReconnectKeepsNewRegistration(t *testing.T) {
	ts := NewTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := func() *MockNode {
		m := NewMockNode("re-node", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
		m.SetNodeToken(ts.IssueTestNodeToken("re-node"))
		if err := m.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := m.WaitRegistered(5 * time.Second); err != nil {
			t.Fatal(err)
		}
		return m
	}

	first := start()
	second := start()
	defer second.Stop()

	first.Stop()
	time.Sleep(300 * time.Millisecond)

	if ts.Nodes.NodeByID("re-node") == nil {
		t.Fatal("old stream teardown removed the new registration")
	}
}
