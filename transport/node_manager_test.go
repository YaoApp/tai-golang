package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

func testMgrAuth(t *testing.T) types.AuthProvider {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return auth.DefaultAuth(priv)
}

func testMgr() *NodeManager {
	pool := &pb.PoolConfig{
		LightMinConns: 2, LightMaxConns: 8, LightMaxStreams: 256,
		HeavyMinIdle: 2, HeavyMaxActive: 20, HeavyMaxIdle: 6,
	}
	return NewNodeManager(pool)
}

func TestNodeManagerRegisterAndLookup(t *testing.T) {
	a := testMgrAuth(t)
	mgr := testMgr()

	reg := &pb.Register{
		NodeId:  "node-1",
		Version: "1.0.0",
		Hardware: &pb.Hardware{
			Os: "linux", Arch: "amd64", CpuCores: 8, MemoryMb: 16384, DiskFreeMb: 102400,
		},
		Capabilities:      []string{"session", "container"},
		ContainerRuntimes: []*pb.ContainerRuntime{{Name: "docker", Version: "27.1.0"}},
	}

	ack, err := mgr.Register("node-1", reg, a)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if ack.TaiId == "" {
		t.Error("tai_id should not be empty")
	}
	if ack.PoolConfig == nil {
		t.Error("pool_config should not be nil")
	}

	node := mgr.NodeByID("node-1")
	if node == nil {
		t.Fatal("NodeByID returned nil")
	}
	if node.OS != "linux" {
		t.Errorf("OS=%q, want linux", node.OS)
	}
	if node.CPUCores != 8 {
		t.Errorf("CPUCores=%d, want 8", node.CPUCores)
	}
	if len(node.ContainerRuntimes) != 1 || node.ContainerRuntimes[0].Name != "docker" {
		t.Errorf("ContainerRuntimes=%v", node.ContainerRuntimes)
	}
	if len(mgr.AllNodes()) != 1 {
		t.Errorf("allNodes len=%d, want 1", len(mgr.AllNodes()))
	}
}

func TestNodeManagerUnregister(t *testing.T) {
	a := testMgrAuth(t)
	mgr := testMgr()

	reg := &pb.Register{NodeId: "node-2", Version: "1.0", Hardware: &pb.Hardware{Os: "darwin"}}
	mgr.Register("node-2", reg, a)
	mgr.Unregister("node-2")

	if mgr.NodeByID("node-2") != nil {
		t.Error("node should be removed")
	}
}

func TestNodeManagerOnRegisterCallback(t *testing.T) {
	a := testMgrAuth(t)
	mgr := testMgr()

	called := false
	mgr.SetOnRegister(func(node *types.Node) error {
		called = true
		return nil
	})

	reg := &pb.Register{NodeId: "node-cb", Version: "1.0", Hardware: &pb.Hardware{}}
	mgr.Register("node-cb", reg, a)

	if !called {
		t.Error("onRegister callback was not called")
	}
}

func TestNodeManagerRegisterCallbackError(t *testing.T) {
	a := testMgrAuth(t)
	mgr := testMgr()
	mgr.SetOnRegister(func(node *types.Node) error {
		return fmt.Errorf("rejected")
	})

	reg := &pb.Register{NodeId: "node-rej", Version: "1.0", Hardware: &pb.Hardware{}}
	_, err := mgr.Register("node-rej", reg, a)
	if err == nil {
		t.Error("expected error")
	}
	if mgr.NodeByID("node-rej") != nil {
		t.Error("node should be removed after callback error")
	}
}

func TestNodeManagerSendControlNoStream(t *testing.T) {
	a := testMgrAuth(t)
	mgr := testMgr()

	reg := &pb.Register{NodeId: "node-ns", Version: "1.0", Hardware: &pb.Hardware{}}
	mgr.Register("node-ns", reg, a)

	err := mgr.SendControl("node-ns", &pb.Payload{Namespace: types.NamespaceControl, Kind: "test"})
	if err == nil {
		t.Error("expected error for node with no stream")
	}
}

func TestNodeManagerSendControlNodeNotFound(t *testing.T) {
	mgr := testMgr()
	err := mgr.SendControl("nonexistent", &pb.Payload{Namespace: types.NamespaceControl, Kind: "test"})
	if err != types.ErrNodeNotFound {
		t.Errorf("got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeManagerGoAwayAll(t *testing.T) {
	a := testMgrAuth(t)
	mgr := testMgr()
	mgr.GoAwayAll("shutdown", 60) // empty, should not panic

	reg := &pb.Register{NodeId: "ga-node", Version: "1.0", Hardware: &pb.Hardware{}}
	mgr.Register("ga-node", reg, a)
	mgr.GoAwayAll("maintenance", 30)
}

func TestNodeManagerUpdatePing(t *testing.T) {
	a := testMgrAuth(t)
	mgr := testMgr()

	reg := &pb.Register{NodeId: "node-p", Version: "1.0", Hardware: &pb.Hardware{}}
	mgr.Register("node-p", reg, a)
	mgr.UpdatePing("node-p", &pb.Ping{Timestamp: 1000, SessionCount: 3, CpuPercent: 42})
}
