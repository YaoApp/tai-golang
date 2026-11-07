package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"google.golang.org/protobuf/proto"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

// ---------------------------------------------------------------------------
// /v1/tai/connect
// ---------------------------------------------------------------------------

func newConnectRequest(t *testing.T, nodeID, token, connType string, st *ConnState) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]string{"node_id": nodeID, "connect_type": connType})
	if err != nil {
		t.Fatalf("marshal connect body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/connect", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if st != nil {
		req = req.WithContext(WithConnState(req.Context(), st))
	}
	return req
}

func TestHandleConnectAuthorized(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	st := NewConnState(nil, time.Second)
	defer st.Close()

	token, err := a.IssueNodeToken("node-connect")
	if err != nil {
		t.Fatalf("issue node token: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newConnectRequest(t, "node-connect", token, ConnTypeControl, st))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	nodeID, ok := st.Check(ConnTypeControl)
	if !ok || nodeID != "node-connect" {
		t.Errorf("conn state = (%q,%v), want (node-connect,true)", nodeID, ok)
	}
}

func TestHandleConnectRejections(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	validToken, _ := a.IssueNodeToken("node-a")

	tests := []struct {
		name     string
		token    string
		nodeID   string
		connType string
		state    func() *ConnState
		want     int
	}{
		{
			name: "missing token", token: "", nodeID: "node-a", connType: ConnTypeControl,
			state: func() *ConnState { return NewConnState(nil, time.Second) }, want: http.StatusUnauthorized,
		},
		{
			name: "invalid token", token: "bad-token", nodeID: "node-a", connType: ConnTypeControl,
			state: func() *ConnState { return NewConnState(nil, time.Second) }, want: http.StatusUnauthorized,
		},
		{
			name: "node id mismatch", token: validToken, nodeID: "node-b", connType: ConnTypeControl,
			state: func() *ConnState { return NewConnState(nil, time.Second) }, want: http.StatusUnauthorized,
		},
		{
			name: "wrong connect type", token: validToken, nodeID: "node-a", connType: ConnTypeLight,
			state: func() *ConnState { return NewConnState(nil, time.Second) }, want: http.StatusUnauthorized,
		},
		{
			name: "already authorized differently", token: validToken, nodeID: "node-a", connType: ConnTypeControl,
			state: func() *ConnState {
				st := NewConnState(nil, time.Second)
				st.AuthorizeAs("node-a", ConnTypeLight)
				return st
			}, want: http.StatusUnauthorized,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := tt.state()
			defer st.Close()
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, newConnectRequest(t, tt.nodeID, tt.token, tt.connType, st))
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestHandleConnectNoConnState(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	token, _ := a.IssueNodeToken("node-ns")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newConnectRequest(t, "node-ns", token, ConnTypeControl, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestRequireAuthorizedRejects(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())

	// Missing connection state.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/control", strings.NewReader(""))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no state: status = %d, want 401", rec.Code)
	}

	// State exists but is not authorized.
	st := NewConnState(nil, time.Second)
	defer st.Close()
	req = httptest.NewRequest(http.MethodPost, "/v1/tai/control", strings.NewReader(""))
	req = req.WithContext(WithConnState(req.Context(), st))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthorized state: status = %d, want 401", rec.Code)
	}
}

func TestHandleLightRequiresHTTP3(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/tai/light", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("HTTP/1.1 status = %d, want 404", rec.Code)
	}

	// ProtoMajor 3 but no H3 server configured (StartH3 not called).
	req = httptest.NewRequest(http.MethodGet, "/v1/tai/light", nil)
	req.ProtoMajor, req.ProtoMinor, req.Proto = 3, 0, "HTTP/3.0"
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("H3 without server status = %d, want 404", rec.Code)
	}
}

// TestLightHandshakeRejections drives the real H3 endpoint with bad
// credentials: the WebTransport upgrade must never happen.
func TestLightHandshakeRejections(t *testing.T) {
	is := newIntegrationServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	validToken, _ := is.auth.IssueNodeToken("node-light")
	cases := []struct {
		name string
		hdr  http.Header
	}{
		{name: "missing token", hdr: http.Header{"X-Tai-Node-Id": {"node-light"}}},
		{name: "invalid token", hdr: http.Header{"Authorization": {"Bearer nope"}, "X-Tai-Node-Id": {"node-light"}}},
		{name: "node id mismatch", hdr: http.Header{"Authorization": {"Bearer " + validToken}, "X-Tai-Node-Id": {"other-node"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newWTTransport(is)
			defer d.Close()
			if _, _, err := d.Dial(ctx, "https://"+is.h3Addr+"/v1/tai/light", tc.hdr); err == nil {
				t.Fatal("dial with bad credentials should fail")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Control stream over the HTTP handler
// ---------------------------------------------------------------------------

func controlFrames(t *testing.T, frames ...*pb.Payload) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, f := range frames {
		if err := DefaultCodec().WriteFrame(&buf, f); err != nil {
			t.Fatalf("write control frame: %v", err)
		}
	}
	return buf.Bytes()
}

func TestControlStreamRegisterAndPing(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	st := NewConnState(nil, 5*time.Second)
	defer st.Close()
	if !st.AuthorizeAs("node-ctrl", ConnTypeControl) {
		t.Fatal("authorize control state")
	}

	regData, _ := proto.Marshal(&pb.Register{NodeId: "node-ctrl", Version: "2.0"})
	pingData, _ := proto.Marshal(&pb.Ping{Timestamp: 12345})

	body := controlFrames(t,
		&pb.Payload{Namespace: "not-control", Kind: "bogus"},
		&pb.Payload{Namespace: types.NamespaceControl, Kind: "unknown"},
		&pb.Payload{Namespace: types.NamespaceControl, Kind: "register", Data: regData},
		&pb.Payload{Namespace: types.NamespaceControl, Kind: "ping", Data: pingData},
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/tai/control", bytes.NewReader(body))
	req = req.WithContext(WithConnState(req.Context(), st))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	codec := DefaultCodec()
	var kinds []string
	for {
		frame, err := codec.ReadFrame(rec.Body)
		if err != nil {
			break
		}
		kinds = append(kinds, frame.Kind)
		if frame.Kind == "register_ack" {
			ack := &pb.RegisterAck{}
			if err := proto.Unmarshal(frame.Data, ack); err != nil {
				t.Errorf("unmarshal register_ack: %v", err)
			}
			if ack.TaiId == "" {
				t.Error("register_ack tai_id is empty")
			}
		}
		if frame.Kind == "pong" {
			pong := &pb.Pong{}
			if err := proto.Unmarshal(frame.Data, pong); err != nil {
				t.Errorf("unmarshal pong: %v", err)
			}
			if pong.Timestamp != 12345 {
				t.Errorf("pong timestamp = %d, want 12345", pong.Timestamp)
			}
		}
	}
	want := []string{"register_ack", "pong"}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Errorf("control responses = %v, want %v", kinds, want)
	}
}

// ---------------------------------------------------------------------------
// HTTP API server
// ---------------------------------------------------------------------------

func TestHTTPServerListenAndServe(t *testing.T) {
	a := newTestAuth(t)
	srv := NewHTTPServer(a)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe("127.0.0.1:0") }()

	deadline := time.Now().Add(5 * time.Second)
	for srv.Addr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("HTTP server did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	resp, err := http.Get("http://" + srv.Addr() + "/.well-known/yao-transport")
	if err != nil {
		t.Fatalf("GET well-known: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("well-known status = %d, want 200", resp.StatusCode)
	}
	var info types.WellKnownResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Errorf("decode well-known: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("ListenAndServe returned %v", err)
	}
}

// ---------------------------------------------------------------------------
// Pool lifecycle accessors
// ---------------------------------------------------------------------------

func TestServerPoolsNotInitialized(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())

	if _, err := srv.OpenLightStream(context.Background(), "n", &pb.Payload{}); err == nil {
		t.Error("OpenLightStream without pool should fail")
	}
	if srv.LightConnCount("n") != 0 {
		t.Error("LightConnCount without pool should be 0")
	}
	if _, err := srv.HeavyPick("n"); err == nil {
		t.Error("HeavyPick without pool should fail")
	}
	if srv.HeavyConnCount("n") != 0 || srv.HeavyIdleCount("n") != 0 {
		t.Error("heavy counts without pool should be 0")
	}
	srv.HeavyReturn(nil)
	srv.HeavyRemove(nil)
	srv.CloseLightPool()
	srv.CloseHeavyPool()
}

func TestServerPoolsLifecycle(t *testing.T) {
	a := newTestAuth(t)
	cfg := &pb.PoolConfig{LightMaxStreams: 4}
	srv := NewServer(a, testRegistry(), DefaultCodec())
	srv.InitLightPool(cfg)
	srv.InitHeavyPool(cfg)

	srv.CloseHeavyPool()
	srv.CloseLightPool()
	// Idempotent shutdown.
	srv.CloseHeavyPool()
	srv.CloseLightPool()
}

func TestSetQUICConfig(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	srv.SetQUICConfig(&quic.Config{MaxIdleTimeout: 3 * time.Second})
	tlsCfg, _ := newTestTLS(t)
	if err := srv.StartH3("127.0.0.1:0", tlsCfg); err != nil {
		t.Fatalf("StartH3: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPkgBackendHelpers(t *testing.T) {
	b := &fakeBackend{}
	SetTestBackend(b)

	identity := &pb.Identity{UserId: "u", TeamId: "t", UserToken: "tok"}
	stream, err := PkgOpenLightStream(context.Background(), "node-pkg", FirstFrame(types.NamespaceSession, "open", nil, identity))
	if err != nil {
		t.Fatalf("PkgOpenLightStream: %v", err)
	}
	if b.openCalls != 1 || b.lastNodeID != "node-pkg" || b.lastFrame.Identity != identity {
		t.Errorf("backend open = (%d,%q,%+v)", b.openCalls, b.lastNodeID, b.lastFrame)
	}
	_ = stream.Close()

	if _, err := PkgHeavyPick("node-pkg"); err != nil {
		t.Fatalf("PkgHeavyPick: %v", err)
	}
	PkgHeavyReturn(nil)
	PkgHeavyRemove(nil)
	if b.pickCalls != 1 || b.returnCalls != 1 || b.removeCalls != 1 {
		t.Errorf("backend calls = pick:%d return:%d remove:%d", b.pickCalls, b.returnCalls, b.removeCalls)
	}

	// SetDefault routes the package helpers to a real server.
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	SetDefault(srv)
	if _, err := PkgOpenLightStream(context.Background(), "node-pkg", &pb.Payload{}); err == nil {
		t.Error("PkgOpenLightStream on server without pool should fail")
	}
}

// ---------------------------------------------------------------------------
// Plaintext TCP routing
// ---------------------------------------------------------------------------

func TestPlaintextH2CRoute(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer srv.Close(ctx)

	token, _ := a.IssueNodeToken("node-h2c")
	body, _ := json.Marshal(map[string]string{"node_id": "node-h2c", "connect_type": ConnTypeControl})

	var protos http.Protocols
	protos.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: &protos}}
	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.TCPAddr()+"/v1/tai/connect", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("h2c request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("h2c connect status = %d, want 200", resp.StatusCode)
	}
}

func TestPlaintextTaiRoute(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())

	got := make(chan string, 1)
	srv.SetTaiHandler(func(conn net.Conn, st *ConnState) {
		defer conn.Close()
		if _, ok := st.Check(ConnTypeLight); !ok {
			st.AuthorizeAs("plain-node", ConnTypeLight)
		}
		got <- "called"
	})
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer srv.Close(ctx)

	conn, err := net.Dial("tcp", srv.TCPAddr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := WriteProtoMsg(conn, &pb.Connect{NodeId: "plain-node"}, maxConnectFrameSize); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("Tai TCP handler was not invoked")
	}
}

func TestPlaintextTaiRouteNoHandler(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer srv.Close(ctx)

	conn, err := net.Dial("tcp", srv.TCPAddr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := WriteProtoMsg(conn, &pb.Connect{NodeId: "no-handler"}, maxConnectFrameSize); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("connection without Tai handler should be closed")
	}
}

func TestPlaintextRouteAbortedHandshake(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer srv.Close(ctx)

	conn, err := net.Dial("tcp", srv.TCPAddr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Write a partial H2 preface then close: routeTCP must give up cleanly.
	conn.Write([]byte("PRI"))
	conn.Close()
	waitFor(t, "connection released", func() bool {
		return srv.tcp.connCount() == 0
	})
}

func TestPeekH2Preface(t *testing.T) {
	// Full h2c preface: detected as HTTP/2.
	r := bufio.NewReader(strings.NewReader(h2cPreface + "\r\nSM\r\n\r\n"))
	h2, err := peekH2Preface(r)
	if err != nil {
		t.Fatalf("peek h2 preface: %v", err)
	}
	if !h2 {
		t.Error("h2 preface should be detected")
	}

	// Non-preface first byte: not an H2 connection.
	r = bufio.NewReader(strings.NewReader("GET / HTTP/1.1"))
	h2, err = peekH2Preface(r)
	if err != nil {
		t.Fatalf("peek non-h2: %v", err)
	}
	if h2 {
		t.Error("GET should not be detected as H2 preface")
	}

	// Empty stream: error.
	if _, err := peekH2Preface(bufio.NewReader(strings.NewReader(""))); err == nil {
		t.Error("empty stream should return an error")
	}
}

// ---------------------------------------------------------------------------
// Light/heavy pool internals
// ---------------------------------------------------------------------------

type failingRegistry struct{ testNodeRegistry }

func (r *failingRegistry) SendControl(nodeID string, msg *pb.Payload) error {
	return fmt.Errorf("send control failed")
}

func TestLightPoolConfigAndConnCount(t *testing.T) {
	pool := newLightPool(&pb.PoolConfig{}, DefaultCodec(), testRegistry(), &testAuth{})
	pool.updateConfig(&pb.PoolConfig{LightMaxStreams: 9})
	if got := pool.maxStreams(); got != 9 {
		t.Errorf("maxStreams = %d, want 9", got)
	}
	pool.updateConfig(nil)
	if got := pool.maxStreams(); got != 256 {
		t.Errorf("maxStreams default = %d, want 256", got)
	}

	lc := &lightConn{nodeID: "node-conn"}
	if !pool.addConn(lc) {
		t.Fatal("addConn on open pool should succeed")
	}
	if pool.connCount("node-conn") != 1 {
		t.Errorf("connCount = %d, want 1", pool.connCount("node-conn"))
	}
	pool.removeConn(lc)
	if pool.connCount("node-conn") != 0 {
		t.Errorf("connCount after remove = %d, want 0", pool.connCount("node-conn"))
	}
	// Removing an unknown connection is a no-op.
	pool.removeConn(&lightConn{nodeID: "unknown"})

	pool.close()
	if pool.addConn(&lightConn{nodeID: "late"}) {
		t.Error("addConn after close should fail")
	}
	pool.close() // idempotent
}

func TestLightPoolSetHeavyPool(t *testing.T) {
	pool := newLightPool(&pb.PoolConfig{}, DefaultCodec(), testRegistry(), &testAuth{})
	hp := newHeavyPool(&pb.PoolConfig{}, testRegistry(), &testAuth{})
	pool.setHeavyPool(hp)
	if pool.heavy != hp {
		t.Error("setHeavyPool did not store the heavy pool")
	}
	pool.close()
	hp.Close()
}

func TestLightPoolRequestAdjustError(t *testing.T) {
	pool := newLightPool(&pb.PoolConfig{}, DefaultCodec(), &failingRegistry{}, &testAuth{})
	// Must not panic when the Control write fails.
	pool.requestAdjust("node-adj", 2)

	heavy := newHeavyPool(&pb.PoolConfig{}, &failingRegistry{}, &testAuth{})
	heavy.requestAdjust("node-adj", 2)
	heavy.updateConfig(&pb.PoolConfig{})
	heavy.Close()
}

func TestLightPoolOpenStreamContextCanceled(t *testing.T) {
	pool := newLightPool(&pb.PoolConfig{}, DefaultCodec(), testRegistry(), &testAuth{})
	defer pool.close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pool.OpenStream(ctx, "no-such-node", &pb.Payload{})
	if !errors.Is(err, types.ErrNoLightConn) && !errors.Is(err, context.Canceled) {
		t.Errorf("OpenStream error = %v, want ErrNoLightConn/context.Canceled", err)
	}

	// Closed pool returns immediately.
	pool.close()
	if _, err := pool.OpenStream(context.Background(), "no-such-node", &pb.Payload{}); err == nil {
		t.Error("OpenStream on closed pool should fail")
	}
}

func TestLightStreamResolveH2Error(t *testing.T) {
	// Transport error.
	pend := make(chan h2StreamResult, 1)
	ls := &LightStream{conn: &lightConn{}, h2Pend: pend}
	pend <- h2StreamResult{err: fmt.Errorf("dial failed")}
	if _, err := ls.Read(make([]byte, 1)); err == nil {
		t.Error("Read should surface the H2 transport error")
	}

	// Non-200 response.
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(strings.NewReader("bad gateway")),
	}
	pend = make(chan h2StreamResult, 1)
	ls = &LightStream{conn: &lightConn{}, h2Pend: pend}
	pend <- h2StreamResult{resp: resp}
	if _, err := ls.Read(make([]byte, 1)); err == nil {
		t.Error("Read should surface the H2 status error")
	}

	// Closing resolves the pending response as well.
	okResp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("x"))}
	pend = make(chan h2StreamResult, 1)
	ls = &LightStream{conn: &lightConn{}, h2Pend: pend, w: nopWriteCloser{io.Discard}}
	pend <- h2StreamResult{resp: okResp}
	if err := ls.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if ls.conn.active.Load() != -1 {
		t.Errorf("active = %d, want -1", ls.conn.active.Load())
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// tcpConnCount is used by the aborted-handshake test.
func (m *tcpMux) connCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conns)
}
