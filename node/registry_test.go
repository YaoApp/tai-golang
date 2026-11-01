package node

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"github.com/yaoapp/tai-golang/workspace"
	"google.golang.org/protobuf/proto"
)

func testAuth(t *testing.T) types.AuthProvider {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return auth.DefaultAuth(priv)
}

func testRegistry() *Registry {
	pool := &pb.PoolConfig{
		LightMinConns: 2, LightMaxConns: 8, LightMaxStreams: 256,
		HeavyMinIdle: 2, HeavyMaxActive: 20, HeavyMaxIdle: 6,
	}
	return NewRegistry(pool, workspace.DefaultPolicy())
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()

	pbReg := &pb.Register{
		NodeId:  "node-1",
		Version: "1.0.0",
		Hardware: &pb.Hardware{
			Os: "linux", Arch: "amd64", CpuCores: 8, MemoryMb: 16384, DiskFreeMb: 102400,
		},
		Capabilities:      []string{"session", "container"},
		ContainerRuntimes: []*pb.ContainerRuntime{{Name: "docker", Version: "27.1.0"}},
	}

	ack, err := reg.Register("node-1", pbReg, a)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if ack.TaiId == "" {
		t.Error("tai_id should not be empty")
	}
	if ack.PoolConfig == nil {
		t.Error("pool_config should not be nil")
	}

	node := reg.NodeByID("node-1")
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
	if len(reg.AllNodes()) != 1 {
		t.Errorf("allNodes len=%d, want 1", len(reg.AllNodes()))
	}
}

func TestRegistryRemoteNodeByID(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()

	pbReg := &pb.Register{NodeId: "node-rn", Version: "1.0", Hardware: &pb.Hardware{Os: "darwin"}}
	reg.Register("node-rn", pbReg, a)

	rn := reg.RemoteNodeByID("node-rn")
	if rn == nil {
		t.Fatal("RemoteNodeByID returned nil")
	}
	info := rn.Info()
	if info == nil || info.OS != "darwin" {
		t.Errorf("unexpected info: %+v", info)
	}

	if reg.RemoteNodeByID("nonexistent") != nil {
		t.Error("should return nil for nonexistent")
	}
}

func TestRegistryOnRegisterCallback(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()

	called := false
	reg.SetOnRegister(func(n types.RemoteNode) error {
		called = true
		if n.Info() == nil {
			t.Error("callback received nil info")
		}
		return nil
	})

	pbReg := &pb.Register{NodeId: "node-cb", Version: "1.0", Hardware: &pb.Hardware{}}
	reg.Register("node-cb", pbReg, a)

	if !called {
		t.Error("onRegister callback was not called")
	}
}

func TestRegistryRegisterCallbackError(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.SetOnRegister(func(n types.RemoteNode) error {
		return fmt.Errorf("rejected")
	})

	pbReg := &pb.Register{NodeId: "node-rej", Version: "1.0", Hardware: &pb.Hardware{}}
	_, err := reg.Register("node-rej", pbReg, a)
	if err == nil {
		t.Error("expected error")
	}
	if reg.NodeByID("node-rej") != nil {
		t.Error("node should be removed after callback error")
	}
}

func TestRegistrySendControlNoStream(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()

	pbReg := &pb.Register{NodeId: "node-ns", Version: "1.0", Hardware: &pb.Hardware{}}
	reg.Register("node-ns", pbReg, a)

	err := reg.SendControl("node-ns", &pb.Payload{Namespace: types.NamespaceControl, Kind: "test"})
	if err == nil {
		t.Error("expected error for node with no stream")
	}
}

func TestRegistrySendControlNodeNotFound(t *testing.T) {
	reg := testRegistry()
	err := reg.SendControl("nonexistent", &pb.Payload{Namespace: types.NamespaceControl, Kind: "test"})
	if err != types.ErrNodeNotFound {
		t.Errorf("got %v, want ErrNodeNotFound", err)
	}
}

func TestRegistryGoAwayAll(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.GoAwayAll("shutdown", 60) // empty, should not panic

	pbReg := &pb.Register{NodeId: "ga-node", Version: "1.0", Hardware: &pb.Hardware{}}
	reg.Register("ga-node", pbReg, a)
	reg.GoAwayAll("maintenance", 30)
}

func TestRegistryUpdatePing(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()

	pbReg := &pb.Register{NodeId: "node-p", Version: "1.0", Hardware: &pb.Hardware{}}
	reg.Register("node-p", pbReg, a)
	reg.UpdatePing("node-p", &pb.Ping{Timestamp: 1000, SessionCount: 3, CpuPercent: 42})
}

func TestGlobalGetAndList(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	SetDefault(reg)

	pbReg := &pb.Register{NodeId: "global-1", Version: "1.0", Hardware: &pb.Hardware{}}
	reg.Register("global-1", pbReg, a)

	if Get("global-1") == nil {
		t.Error("Get returned nil")
	}
	if Get("nonexistent") != nil {
		t.Error("Get should return nil for nonexistent")
	}
	if len(List()) != 1 {
		t.Errorf("List len=%d, want 1", len(List()))
	}
}

// ---------- Registry: BindStream, UnbindStream, SetPoolConfig, SetOnDisconnect ----------

type mockWriter struct {
	mu   sync.Mutex
	msgs []*pb.Payload
}

func (m *mockWriter) WriteControl(msg *pb.Payload) error {
	m.mu.Lock()
	m.msgs = append(m.msgs, msg)
	m.mu.Unlock()
	return nil
}

func TestRegistryBindAndSendControl(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.Register("node-bs", &pb.Register{Hardware: &pb.Hardware{}}, a)

	mw := &mockWriter{}
	reg.BindStream("node-bs", mw)

	msg := &pb.Payload{Namespace: types.NamespaceControl, Kind: "custom", Data: []byte("x")}
	if err := reg.SendControl("node-bs", msg); err != nil {
		t.Fatalf("SendControl: %v", err)
	}
	if len(mw.msgs) != 1 || mw.msgs[0].Kind != "custom" {
		t.Errorf("msgs=%d, want 1 with kind=custom", len(mw.msgs))
	}
}

func TestRegistryUnbindStream(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.Register("node-ub", &pb.Register{Hardware: &pb.Hardware{}}, a)

	mw := &mockWriter{}
	reg.BindStream("node-ub", mw)

	reg.UnbindStream("node-ub", mw)
	if reg.NodeByID("node-ub") != nil {
		t.Error("node should be removed after UnbindStream")
	}
}

func TestRegistryUnbindStreamWrongWriter(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.Register("node-uw", &pb.Register{Hardware: &pb.Hardware{}}, a)

	mw1 := &mockWriter{}
	mw2 := &mockWriter{}
	reg.BindStream("node-uw", mw1)
	reg.UnbindStream("node-uw", mw2) // different writer, should NOT remove

	if reg.NodeByID("node-uw") == nil {
		t.Error("node should NOT be removed when writer doesn't match")
	}
}

func TestRegistryUnbindStreamCallsOnDisconnect(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.Register("node-dc", &pb.Register{Hardware: &pb.Hardware{}}, a)

	disconnected := ""
	reg.SetOnDisconnect(func(nodeID string) { disconnected = nodeID })

	mw := &mockWriter{}
	reg.BindStream("node-dc", mw)
	reg.UnbindStream("node-dc", mw)

	if disconnected != "node-dc" {
		t.Errorf("onDisconnect called with %q, want node-dc", disconnected)
	}
}

func TestRegistrySetPoolConfig(t *testing.T) {
	reg := testRegistry()
	newCfg := &pb.PoolConfig{LightMinConns: 4, LightMaxConns: 16}
	reg.SetPoolConfig(newCfg)

	got := reg.PoolConfig()
	if got.LightMinConns != 4 || got.LightMaxConns != 16 {
		t.Errorf("PoolConfig = %+v", got)
	}
}

func TestGlobalOnRegisterAndOnDisconnect(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	SetDefault(reg)

	registered := ""
	OnRegister(func(n types.RemoteNode) error {
		registered = n.Info().ID
		return nil
	})
	disconnected := ""
	OnDisconnect(func(nodeID string) { disconnected = nodeID })

	reg.Register("global-cb", &pb.Register{Hardware: &pb.Hardware{}}, a)
	if registered != "global-cb" {
		t.Errorf("registered=%q, want global-cb", registered)
	}

	mw := &mockWriter{}
	reg.BindStream("global-cb", mw)
	reg.UnbindStream("global-cb", mw)
	if disconnected != "global-cb" {
		t.Errorf("disconnected=%q, want global-cb", disconnected)
	}
}

// ---------- remoteNode method tests ----------

// mockTransportBackend implements transport.Backend for unit testing node methods.
type mockTransportBackend struct {
	mu      sync.Mutex
	handler func(firstFrame *pb.Payload) (io.ReadWriteCloser, error)
}

func (m *mockTransportBackend) OpenLightStream(_ context.Context, _ string, firstFrame *pb.Payload) (io.ReadWriteCloser, error) {
	m.mu.Lock()
	h := m.handler
	m.mu.Unlock()
	if h != nil {
		return h(firstFrame)
	}
	return nil, fmt.Errorf("no handler")
}

func (m *mockTransportBackend) HeavyPick(_ string) (net.Conn, error) {
	return nil, fmt.Errorf("no heavy")
}

func (m *mockTransportBackend) HeavyReturn(_ net.Conn) {}

// buildFramedResponse creates a length-prefixed protobuf Payload that ReadResponse can parse.
func buildFramedResponse(kind string, data []byte) []byte {
	resp := &pb.Payload{Namespace: workspace.NamespaceWorkspace, Kind: kind, Data: data}
	raw, _ := proto.Marshal(resp)
	size := len(raw)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(raw)
	return buf.Bytes()
}

// buildCodecFrame creates a FrameCodec-framed Payload (4-byte length + protobuf).
func buildCodecFrame(namespace, kind string, data []byte) []byte {
	p := &pb.Payload{Namespace: namespace, Kind: kind, Data: data}
	raw, _ := proto.Marshal(p)
	size := len(raw)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(raw)
	return buf.Bytes()
}

type mockNodeStream struct {
	*bytes.Reader
	buf *bytes.Buffer
}

func (ms *mockNodeStream) Write(p []byte) (int, error) { return ms.buf.Write(p) }
func (ms *mockNodeStream) Close() error                { return nil }

func registerNode(t *testing.T, reg *Registry, nodeID string) {
	t.Helper()
	a := testAuth(t)
	reg.Register(nodeID, &pb.Register{Hardware: &pb.Hardware{Os: "linux"}}, a)
}

func TestRemoteNodeOnline(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-online")

	rn := reg.RemoteNodeByID("n-online")
	if !rn.Online() {
		t.Error("should be online")
	}
}

func TestRemoteNodeOpenWorkspace(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-ow")

	metaJSON, _ := json.Marshal(types.Meta{Name: "proj", Owner: "dev"})
	respBody := buildFramedResponse(workspace.KindWSOpen+".result", metaJSON)

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return &mockNodeStream{Reader: bytes.NewReader(respBody), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-ow")
	ws, err := rn.OpenWorkspace(context.Background(), "ws_1")
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if ws.ID() != "ws_1" {
		t.Errorf("ID=%q, want ws_1", ws.ID())
	}
	if ws.Meta().Name != "proj" {
		t.Errorf("Meta.Name=%q, want proj", ws.Meta().Name)
	}
}

func TestRemoteNodeCreateWorkspace(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-cw")

	resultJSON, _ := json.Marshal(struct {
		ID   string     `json:"id"`
		Meta types.Meta `json:"meta"`
	}{ID: "ws_new", Meta: types.Meta{Name: "new-proj"}})
	respBody := buildFramedResponse(workspace.KindWSCreate+".result", resultJSON)

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return &mockNodeStream{Reader: bytes.NewReader(respBody), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-cw")
	ws, err := rn.CreateWorkspace(context.Background(), "/tmp/ws", types.Meta{Name: "new-proj"})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if ws.ID() != "ws_new" {
		t.Errorf("ID=%q, want ws_new", ws.ID())
	}
}

func TestRemoteNodeListWorkspaces(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-lw")

	entries := []types.RegistryEntry{{ID: "ws_1", Meta: types.Meta{Name: "p1"}}}
	entriesJSON, _ := json.Marshal(entries)
	respBody := buildFramedResponse(workspace.KindWSList+".result", entriesJSON)

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return &mockNodeStream{Reader: bytes.NewReader(respBody), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-lw")
	result, err := rn.ListWorkspaces(context.Background(), types.ListOptions{})
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(result) != 1 || result[0].ID != "ws_1" {
		t.Errorf("result=%+v", result)
	}
}

func TestRemoteNodeDeleteWorkspace(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-dw")

	respBody := buildFramedResponse(workspace.KindWSDelete+".result", []byte("{}"))
	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return &mockNodeStream{Reader: bytes.NewReader(respBody), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-dw")
	if err := rn.DeleteWorkspace(context.Background(), "ws_del"); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
}

func TestRemoteNodeOpenSession(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-os")

	callCount := 0
	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			callCount++
			if ff.Kind == "open" {
				// Return "opened" frame via codec format
				opened := buildCodecFrame(types.NamespaceSession, "opened", nil)
				return &mockNodeStream{Reader: bytes.NewReader(opened), buf: &bytes.Buffer{}}, nil
			}
			if ff.Kind == "data" {
				return &mockNodeStream{Reader: bytes.NewReader(nil), buf: &bytes.Buffer{}}, nil
			}
			return &mockNodeStream{Reader: bytes.NewReader(nil), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-os")
	sess, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: "sess-1",
		Token:     "test-token",
		Request:   &types.SessionRequest{Runtime: "agent", Isolation: "none"},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if sess.ID() != "sess-1" {
		t.Errorf("ID=%q, want sess-1", sess.ID())
	}
	if sess.NodeID() != "n-os" {
		t.Errorf("NodeID=%q, want n-os", sess.NodeID())
	}
	if callCount != 2 {
		t.Errorf("expected 2 stream opens (ctrl+data), got %d", callCount)
	}
	sess.Close(context.Background())
}

func TestRemoteNodeOpenSessionError(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-oserr")

	errData, _ := proto.Marshal(&pb.SessionError{Code: "REJECTED", Message: "no capacity"})
	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			errFrame := buildCodecFrame(types.NamespaceSession, "error", errData)
			return &mockNodeStream{Reader: bytes.NewReader(errFrame), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-oserr")
	_, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: "sess-err",
		Token:     "tok",
		Request:   &types.SessionRequest{Runtime: "agent"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("REJECTED")) {
		t.Errorf("error=%q, want to contain REJECTED", err)
	}
}

func TestRemoteNodeResumeSession(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-rs")

	rn := reg.RemoteNodeByID("n-rs")
	_, err := rn.ResumeSession(context.Background(), &types.ResumeSessionRequest{SessionID: "s1"})
	if err == nil {
		t.Fatal("expected not-implemented error")
	}
}

func TestRemoteNodeSendControl(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.Register("n-sc", &pb.Register{Hardware: &pb.Hardware{}}, a)

	mw := &mockWriter{}
	reg.BindStream("n-sc", mw)

	rn := reg.RemoteNodeByID("n-sc")
	err := rn.SendControl(&types.ControlMsg{Kind: "custom", Data: []byte("hi")})
	if err != nil {
		t.Fatalf("SendControl: %v", err)
	}
	if len(mw.msgs) != 1 || mw.msgs[0].Kind != "custom" {
		t.Errorf("msgs=%d", len(mw.msgs))
	}
}

func TestRemoteNodeGoAway(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.Register("n-ga", &pb.Register{Hardware: &pb.Hardware{}}, a)

	mw := &mockWriter{}
	reg.BindStream("n-ga", mw)

	rn := reg.RemoteNodeByID("n-ga")
	err := rn.GoAway("maintenance", 300)
	if err != nil {
		t.Fatalf("GoAway: %v", err)
	}
	if len(mw.msgs) != 1 || mw.msgs[0].Kind != "go_away" {
		t.Errorf("msgs=%d", len(mw.msgs))
	}
}

func TestSessionRequestToProto(t *testing.T) {
	req := &types.SessionRequest{
		Runtime:     "agent",
		Isolation:   "container",
		Cwd:         "/home/user",
		IdleTimeout: 300,
		MaxDuration: 3600,
		LLM:         json.RawMessage(`{"model":"deepseek"}`),
		Env:         map[string]string{"KEY": "val"},
		McpServers: []types.McpServer{{
			Type:    "stdio",
			Name:    "tool",
			Command: "npx",
			Args:    []string{"-y", "tool"},
			Env:     []types.EnvVariable{{Name: "A", Value: "B"}},
		}},
		Metadata: map[string]string{"user": "test"},
		Meta:     map[string]any{"custom": true},
	}
	pr := sessionRequestToProto(req)
	if pr.Runtime != "agent" || pr.Isolation != "container" || pr.Cwd != "/home/user" {
		t.Errorf("basic fields: %+v", pr)
	}
	if pr.IdleTimeoutSec != 300 || pr.MaxDurationSec != 3600 {
		t.Errorf("timeouts: idle=%d max=%d", pr.IdleTimeoutSec, pr.MaxDurationSec)
	}
	if len(pr.McpServers) != 1 || pr.McpServers[0].Name != "tool" {
		t.Errorf("McpServers: %+v", pr.McpServers)
	}
	if len(pr.McpServers[0].Env) != 1 || pr.McpServers[0].Env[0].Name != "A" {
		t.Errorf("McpServer Env: %+v", pr.McpServers[0].Env)
	}
	if pr.Metadata["user"] != "test" {
		t.Errorf("Metadata: %+v", pr.Metadata)
	}
}

func TestSessionRequestToProtoNil(t *testing.T) {
	pr := sessionRequestToProto(nil)
	if pr == nil {
		t.Fatal("should return empty, not nil")
	}
}

// ---------- Error path tests ----------

func TestRemoteNodeOpenWorkspaceTransportError(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-ow-err")

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return nil, fmt.Errorf("connection refused")
		},
	})

	rn := reg.RemoteNodeByID("n-ow-err")
	_, err := rn.OpenWorkspace(context.Background(), "ws_1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRemoteNodeOpenWorkspaceServerError(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-ow-se")

	errPayload := &pb.Payload{
		Namespace: workspace.NamespaceWorkspace,
		Kind:      "error",
		Data:      []byte(`{"code":"NOT_FOUND","message":"gone"}`),
	}
	respBody := buildFramedResponse("error", errPayload.Data)
	// Rebuild with error kind directly
	errBytes, _ := proto.Marshal(errPayload)
	size := len(errBytes)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(errBytes)
	finalResp := buf.Bytes()
	_ = respBody // unused, using finalResp

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return &mockNodeStream{Reader: bytes.NewReader(finalResp), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-ow-se")
	_, err := rn.OpenWorkspace(context.Background(), "ws_1")
	if err == nil {
		t.Fatal("expected error from server")
	}
}

func TestRemoteNodeOpenWorkspaceBadMeta(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-ow-bm")

	respBody := buildFramedResponse(workspace.KindWSOpen+".result", []byte("not-json"))
	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return &mockNodeStream{Reader: bytes.NewReader(respBody), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-ow-bm")
	_, err := rn.OpenWorkspace(context.Background(), "ws_1")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestRemoteNodeCreateWorkspaceTransportError(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-cw-err")

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return nil, fmt.Errorf("transport down")
		},
	})

	rn := reg.RemoteNodeByID("n-cw-err")
	_, err := rn.CreateWorkspace(context.Background(), "/tmp", types.Meta{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRemoteNodeListWorkspacesTransportError(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-lw-err")

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return nil, fmt.Errorf("transport down")
		},
	})

	rn := reg.RemoteNodeByID("n-lw-err")
	_, err := rn.ListWorkspaces(context.Background(), types.ListOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRemoteNodeDeleteWorkspaceTransportError(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-dw-err")

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return nil, fmt.Errorf("transport down")
		},
	})

	rn := reg.RemoteNodeByID("n-dw-err")
	err := rn.DeleteWorkspace(context.Background(), "ws_del")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRemoteNodeOpenSessionTransportError(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-os-te")

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			return nil, fmt.Errorf("transport down")
		},
	})

	rn := reg.RemoteNodeByID("n-os-te")
	_, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: "s1", Token: "tok", Request: &types.SessionRequest{Runtime: "agent"},
	})
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestRemoteNodeOpenSessionUnexpectedKind(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-os-uk")

	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			frame := buildCodecFrame(types.NamespaceSession, "something_else", nil)
			return &mockNodeStream{Reader: bytes.NewReader(frame), buf: &bytes.Buffer{}}, nil
		},
	})

	rn := reg.RemoteNodeByID("n-os-uk")
	_, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: "s1", Token: "tok", Request: &types.SessionRequest{Runtime: "agent"},
	})
	if err == nil {
		t.Fatal("expected unexpected-kind error")
	}
}

func TestRemoteNodeOpenSessionDataStreamError(t *testing.T) {
	reg := testRegistry()
	registerNode(t, reg, "n-os-de")

	callCount := 0
	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			callCount++
			if ff.Kind == "open" {
				opened := buildCodecFrame(types.NamespaceSession, "opened", nil)
				return &mockNodeStream{Reader: bytes.NewReader(opened), buf: &bytes.Buffer{}}, nil
			}
			// data stream fails
			return nil, fmt.Errorf("data stream unavailable")
		},
	})

	rn := reg.RemoteNodeByID("n-os-de")
	_, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: "s1", Token: "tok", Request: &types.SessionRequest{Runtime: "agent"},
	})
	if err == nil {
		t.Fatal("expected data stream error")
	}
}
