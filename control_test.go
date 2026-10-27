package tai

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// Protocol test clients
// ---------------------------------------------------------------------------

// h2cClient returns an HTTP client that speaks h2c (unencrypted HTTP/2).
func h2cClient() *http.Client {
	var protos http.Protocols
	protos.SetHTTP1(false)
	protos.SetUnencryptedHTTP2(true)
	return &http.Client{
		Transport: &http.Transport{
			Protocols: &protos,
		},
	}
}

// h3TestClient returns an HTTP/3 client that trusts the test server's self-signed cert.
func h3TestClient(serverTLS *tls.Config) *http.Client {
	pool := x509.NewCertPool()
	for _, cert := range serverTLS.Certificates {
		if len(cert.Certificate) > 0 {
			c, _ := x509.ParseCertificate(cert.Certificate[0])
			if c != nil {
				pool.AddCert(c)
			}
		}
	}
	return &http.Client{
		Transport: &http3.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: pool,
			},
		},
	}
}

// transportTestCase describes a protocol variant for sub-tests.
type transportTestCase struct {
	name      string
	newClient func() *http.Client // factory: fresh client per request
	url       string              // base URL for transport endpoints
}

// h2TLSClient returns an HTTP/2-over-TLS client that trusts the test server's self-signed cert.
func h2TLSClient(serverTLS *tls.Config) *http.Client {
	pool := x509.NewCertPool()
	for _, cert := range serverTLS.Certificates {
		if len(cert.Certificate) > 0 {
			c, _ := x509.ParseCertificate(cert.Certificate[0])
			if c != nil {
				pool.AddCert(c)
			}
		}
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: pool},
			ForceAttemptHTTP2: true,
		},
	}
}

// protocolCases returns H2 and H3 test cases for the given TestServer.
func protocolCases(ts *TestServer) []transportTestCase {
	return []transportTestCase{
		{"H2", func() *http.Client { return h2TLSClient(ts.TLSConfig) }, "https://" + ts.TransportAddr},
		{"H3", func() *http.Client { return h3TestClient(ts.TLSConfig) }, ts.H3URL()},
	}
}

// postConnect sends POST /v1/tai/connect on client and returns the status code.
// The response body is drained so the connection stays reusable.
func postConnect(client *http.Client, baseURL, token, nodeID, connType string) (int, error) {
	body, _ := json.Marshal(connectRequest{NodeID: nodeID, ConnectType: connType})
	req, _ := http.NewRequest("POST", baseURL+"/v1/tai/connect", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

// authorizedClient returns a client whose connection to the test server has
// been authorized as a Control connection for nodeID. Later requests from the
// same client reuse that connection.
func authorizedClient(t *testing.T, ts *TestServer, tc transportTestCase, nodeID string) *http.Client {
	t.Helper()
	client := tc.newClient()
	status, err := postConnect(client, tc.url, ts.IssueTestNodeToken(nodeID), nodeID, "control")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("connect status=%d, want 200", status)
	}
	return client
}

// ---------------------------------------------------------------------------
// Frame tests (protocol-independent)
// ---------------------------------------------------------------------------

func TestFrameRoundTrip(t *testing.T) {
	msg := &pb.Payload{Namespace: NamespaceControl, Kind: "test", Data: []byte("hello")}

	var buf bytes.Buffer
	if err := writeFrame(&buf, msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := readFrame(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Kind != "test" {
		t.Errorf("kind=%q, want test", got.Kind)
	}
	if string(got.Data) != "hello" {
		t.Errorf("data=%q, want hello", string(got.Data))
	}
}

func TestFrameMultiple(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 5; i++ {
		msg := &pb.Payload{Namespace: NamespaceControl, Kind: "ping", Data: []byte{byte(i)}}
		if err := writeFrame(&buf, msg); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	for i := 0; i < 5; i++ {
		got, err := readFrame(&buf)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if got.Kind != "ping" {
			t.Errorf("frame %d kind=%q", i, got.Kind)
		}
	}

	_, err := readFrame(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestFrameMaxSize(t *testing.T) {
	huge := make([]byte, maxFrameSize+1)
	msg := &pb.Payload{Namespace: NamespaceControl, Kind: "big", Data: huge}

	var buf bytes.Buffer
	err := writeFrame(&buf, msg)
	if err != ErrFrameTooLarge {
		t.Errorf("got %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameReadTruncated(t *testing.T) {
	msg := &pb.Payload{Namespace: NamespaceControl, Kind: "test", Data: []byte("data")}
	var buf bytes.Buffer
	writeFrame(&buf, msg)

	truncated := buf.Bytes()[:6]
	_, err := readFrame(bytes.NewReader(truncated))
	if err == nil {
		t.Error("expected error for truncated frame")
	}
}

func TestFrameWriteError(t *testing.T) {
	w := &errWriter{}
	msg := &pb.Payload{Namespace: NamespaceControl, Kind: "test"}
	err := writeFrame(w, msg)
	if err == nil {
		t.Error("expected error from broken writer")
	}
}

type errWriter struct{}

func (e *errWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestFrameReadBadHeader(t *testing.T) {
	_, err := readFrame(bytes.NewReader([]byte{0x00, 0x01}))
	if err == nil {
		t.Error("expected error for short header")
	}
}

func TestFrameReadBadPayload(t *testing.T) {
	header := []byte{0x00, 0x00, 0x00, 0x03}
	data := append(header, 0xFF, 0xFF, 0xFF)
	_, err := readFrame(bytes.NewReader(data))
	if err == nil {
		t.Error("expected error for invalid protobuf payload")
	}
}

// ---------------------------------------------------------------------------
// NodeManager tests (protocol-independent)
// ---------------------------------------------------------------------------

func TestNodeManagerRegisterAndLookup(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()

	reg := &pb.Register{
		NodeId:  "node-1",
		Version: "1.0.0",
		Hardware: &pb.Hardware{
			Os: "linux", Arch: "amd64", CpuCores: 8, MemoryMb: 16384, DiskFreeMb: 102400,
		},
		Capabilities:      []string{"session", "container"},
		ContainerRuntimes: []*pb.ContainerRuntime{{Name: "docker", Version: "27.1.0"}},
	}

	ack, err := mgr.register("node-1", reg, auth)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if ack.TaiId == "" {
		t.Error("tai_id should not be empty")
	}
	if ack.PoolConfig == nil {
		t.Error("pool_config should not be nil")
	}

	node := mgr.nodeByID("node-1")
	if node == nil {
		t.Fatal("nodeByID returned nil")
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
	if len(mgr.allNodes()) != 1 {
		t.Errorf("allNodes len=%d, want 1", len(mgr.allNodes()))
	}
}

func TestNodeManagerUnregister(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()

	reg := &pb.Register{NodeId: "node-2", Version: "1.0", Hardware: &pb.Hardware{Os: "darwin"}}
	mgr.register("node-2", reg, auth)
	mgr.unregister("node-2")

	if mgr.nodeByID("node-2") != nil {
		t.Error("node should be removed")
	}
}

func TestNodeManagerOnRegisterCallback(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()

	called := false
	mgr.onRegister = func(node *Node) error {
		called = true
		return nil
	}

	reg := &pb.Register{NodeId: "node-cb", Version: "1.0", Hardware: &pb.Hardware{}}
	mgr.register("node-cb", reg, auth)

	if !called {
		t.Error("onRegister callback was not called")
	}
}

func TestNodeManagerRegisterCallbackError(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()
	mgr.onRegister = func(node *Node) error {
		return fmt.Errorf("rejected")
	}

	reg := &pb.Register{NodeId: "node-rej", Version: "1.0", Hardware: &pb.Hardware{}}
	_, err := mgr.register("node-rej", reg, auth)
	if err == nil {
		t.Error("expected error")
	}
	if mgr.nodeByID("node-rej") != nil {
		t.Error("node should be removed after callback error")
	}
}

func TestNodeManagerUpdatePing(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()

	reg := &pb.Register{NodeId: "node-p", Version: "1.0", Hardware: &pb.Hardware{}}
	mgr.register("node-p", reg, auth)
	mgr.updatePing("node-p", &pb.Ping{Timestamp: 1000, SessionCount: 3, CpuPercent: 42})
}

func TestNodeManagerSendControlNoStream(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()

	reg := &pb.Register{NodeId: "node-ns", Version: "1.0", Hardware: &pb.Hardware{}}
	mgr.register("node-ns", reg, auth)

	err := mgr.sendControl("node-ns", &pb.Payload{Namespace: NamespaceControl, Kind: "test"})
	if err == nil {
		t.Error("expected error for node with no stream")
	}
}

func TestNodeManagerSendControlNodeNotFound(t *testing.T) {
	mgr := newNodeManager()
	err := mgr.sendControl("nonexistent", &pb.Payload{Namespace: NamespaceControl, Kind: "test"})
	if err != ErrNodeNotFound {
		t.Errorf("got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeManagerGoAwayNodeNotFound(t *testing.T) {
	mgr := newNodeManager()
	err := mgr.goAway("nonexistent", "shutdown", 30)
	if err != ErrNodeNotFound {
		t.Errorf("got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeManagerGoAwayAll(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()
	mgr.goAwayAll("shutdown", 60) // empty, should not panic

	reg := &pb.Register{NodeId: "ga-node", Version: "1.0", Hardware: &pb.Hardware{}}
	mgr.register("ga-node", reg, auth)
	mgr.goAwayAll("maintenance", 30) // no stream, swallows error
}

// ---------------------------------------------------------------------------
// End-to-end Control stream tests — H2 and H3
// ---------------------------------------------------------------------------

func TestControlStreamEndToEnd(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			nodeID := "e2e-" + tc.name

			client := authorizedClient(t, ts, tc, nodeID)

			pr, pw := io.Pipe()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST",
				tc.url+"/v1/tai/control", pr)

			type result struct {
				resp *http.Response
				err  error
			}
			ch := make(chan result, 1)
			go func() {
				resp, err := client.Do(req)
				ch <- result{resp, err}
			}()

			// Send Register
			regPB := &pb.Register{
				NodeId:  nodeID,
				Version: "2.0.0",
				Hardware: &pb.Hardware{
					Os: "linux", Arch: "amd64", CpuCores: 4, MemoryMb: 8192,
				},
				Capabilities: []string{"session"},
			}
			regData, _ := proto.Marshal(regPB)
			writeFrame(pw, &pb.Payload{Namespace: NamespaceControl, Kind: "register", Data: regData})

			res := <-ch
			if res.err != nil {
				t.Fatalf("request: %v", res.err)
			}
			defer res.resp.Body.Close()

			if res.resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d, want 200", res.resp.StatusCode)
			}

			// Read register_ack
			ackMsg, err := readFrame(res.resp.Body)
			if err != nil {
				t.Fatalf("read register_ack: %v", err)
			}
			if ackMsg.Kind != "register_ack" {
				t.Fatalf("kind=%q, want register_ack", ackMsg.Kind)
			}

			ack := &pb.RegisterAck{}
			proto.Unmarshal(ackMsg.Data, ack)
			if ack.TaiId == "" {
				t.Error("tai_id empty")
			}
			if ack.PoolConfig == nil || ack.PoolConfig.LightMinConns != 2 {
				t.Error("pool_config unexpected")
			}

			// A message in another namespace is ignored: no pong for it.
			foreign, _ := proto.Marshal(&pb.Ping{Timestamp: 1111})
			writeFrame(pw, &pb.Payload{Namespace: "other", Kind: "ping", Data: foreign})

			// Send Ping
			pingData, _ := proto.Marshal(&pb.Ping{Timestamp: 7777})
			writeFrame(pw, &pb.Payload{Namespace: NamespaceControl, Kind: "ping", Data: pingData})

			// Read Pong
			pongMsg, err := readFrame(res.resp.Body)
			if err != nil {
				t.Fatalf("read pong: %v", err)
			}
			if pongMsg.Kind != "pong" {
				t.Fatalf("kind=%q, want pong", pongMsg.Kind)
			}
			pong := &pb.Pong{}
			proto.Unmarshal(pongMsg.Data, pong)
			if pong.Timestamp != 7777 {
				t.Errorf("pong timestamp=%d, want 7777", pong.Timestamp)
			}

			// Unknown kind (forward compat)
			writeFrame(pw, &pb.Payload{Namespace: NamespaceControl, Kind: "future.msg"})

			// Verify node in manager
			time.Sleep(50 * time.Millisecond)
			node := ts.Nodes.nodeByID(nodeID)
			if node == nil {
				t.Fatal("node not registered")
			}
			if node.Version != "2.0.0" {
				t.Errorf("version=%q", node.Version)
			}

			pw.Close()
		})
	}
}

func TestControlStreamCallback(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			nodeID := "cb-" + tc.name

			registered := make(chan string, 1)
			ts.Nodes.onRegister = func(node *Node) error {
				registered <- node.ID
				return nil
			}

			client := authorizedClient(t, ts, tc, nodeID)
			pr, pw := io.Pipe()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST",
				tc.url+"/v1/tai/control", pr)

			type result struct {
				resp *http.Response
				err  error
			}
			ch := make(chan result, 1)
			go func() {
				resp, err := client.Do(req)
				ch <- result{resp, err}
			}()

			regData, _ := proto.Marshal(&pb.Register{
				NodeId: nodeID, Version: "1.0",
				Hardware: &pb.Hardware{Os: "darwin", Arch: "arm64"},
			})
			writeFrame(pw, &pb.Payload{Namespace: NamespaceControl, Kind: "register", Data: regData})

			res := <-ch
			if res.err != nil {
				t.Fatalf("request: %v", res.err)
			}
			defer res.resp.Body.Close()

			ackMsg, err := readFrame(res.resp.Body)
			if err != nil {
				t.Fatalf("read ack: %v", err)
			}
			if ackMsg.Kind != "register_ack" {
				t.Fatalf("kind=%q", ackMsg.Kind)
			}

			select {
			case id := <-registered:
				if id != nodeID {
					t.Errorf("callback node=%q, want %s", id, nodeID)
				}
			case <-time.After(2 * time.Second):
				t.Error("callback not called")
			}

			pw.Close()
		})
	}
}

// ---------------------------------------------------------------------------
// NewFrameCodec unit tests
// ---------------------------------------------------------------------------

func TestNewFrameCodecDefault(t *testing.T) {
	c, err := NewFrameCodec(0)
	if err != nil {
		t.Fatalf("NewFrameCodec(0): %v", err)
	}
	if c.MaxSize() != DefaultMaxFrameSize {
		t.Errorf("MaxSize() = %d, want %d", c.MaxSize(), DefaultMaxFrameSize)
	}
}

func TestNewFrameCodecCustom(t *testing.T) {
	c, err := NewFrameCodec(1 << 20) // 1 MB
	if err != nil {
		t.Fatalf("NewFrameCodec(1MB): %v", err)
	}
	if c.MaxSize() != 1<<20 {
		t.Errorf("MaxSize() = %d, want %d", c.MaxSize(), 1<<20)
	}
}

func TestNewFrameCodecTooSmall(t *testing.T) {
	_, err := NewFrameCodec(1024)
	if err == nil {
		t.Error("expected error for frame size below minimum")
	}
}

func TestNewFrameCodecTooLarge(t *testing.T) {
	_, err := NewFrameCodec(128 * 1024 * 1024)
	if err == nil {
		t.Error("expected error for frame size above maximum")
	}
}

func TestFrameCodecWriteRead(t *testing.T) {
	c, _ := NewFrameCodec(256 * 1024)
	var buf bytes.Buffer

	msg := &pb.Payload{Namespace: NamespaceControl, Kind: "test", Data: []byte("hello")}
	if err := c.WriteFrame(&buf, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := c.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Kind != "test" {
		t.Errorf("Kind = %q, want test", got.Kind)
	}
}

// A write that arrives after the Control stream's handler has returned must
// fail instead of touching the ResponseWriter, which is invalid by then.
func TestControlStreamRejectsWritesAfterRun(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()
	var out bytes.Buffer
	cs := newControlStream("node-closed", bytes.NewReader(nil), httptest.NewRecorder(), mgr, auth, defaultCodec)
	cs.w = &out
	mgr.register("node-closed", &pb.Register{Hardware: &pb.Hardware{}}, auth)
	mgr.setStream("node-closed", cs)

	cs.run(context.Background()) // empty body: EOF ends the loop

	if err := mgr.sendControl("node-closed", &pb.Payload{Namespace: NamespaceControl, Kind: "pong"}); err == nil {
		t.Error("sendControl after the stream closed must fail")
	}
	if err := cs.writeMsg(&pb.Payload{Namespace: NamespaceControl, Kind: "pong"}); !errors.Is(err, errControlClosed) {
		t.Errorf("writeMsg after run = %v, want errControlClosed", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %d bytes after the stream closed", out.Len())
	}
}
