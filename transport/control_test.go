package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// trackingRegistry records calls for verification.
type trackingRegistry struct {
	testNodeRegistry
	bindCalled   bool
	unbindCalled bool
	pingUpdated  bool
	lastPingNode string
	lastBindNode string
	lastBindCtrl types.ControlWriter
}

func (r *trackingRegistry) BindStream(nodeID string, ctrl types.ControlWriter) {
	r.bindCalled = true
	r.lastBindNode = nodeID
	r.lastBindCtrl = ctrl
}

func (r *trackingRegistry) UnbindStream(nodeID string, ctrl types.ControlWriter) {
	r.unbindCalled = true
}

func (r *trackingRegistry) UpdatePing(nodeID string, ping *pb.Ping) {
	r.pingUpdated = true
	r.lastPingNode = nodeID
}

// testAuth implements types.AuthProvider for control stream tests.
type testAuth struct{}

func (a *testAuth) ValidateNodeToken(token string) (*types.NodeInfo, error) {
	return &types.NodeInfo{NodeID: "test-node"}, nil
}
func (a *testAuth) IssueNodeToken(nodeID string) (string, error) { return "tok", nil }
func (a *testAuth) RefreshNodeToken(old string) (string, error)  { return "new-tok", nil }
func (a *testAuth) IssueOperationToken(nodeID, sessionID string, ops []string, scope *types.TokenScope) (string, error) {
	return "op-tok", nil
}
func (a *testAuth) SigningPublicKey() (string, []byte)      { return "key-1", []byte("pubkey") }
func (a *testAuth) WellKnownInfo() *types.WellKnownResponse { return &types.WellKnownResponse{} }

func (a *testAuth) IssueUserToken(nodeID string, scopes types.Scope) (string, *types.UserIdentity, error) {
	return "user-tok", &types.UserIdentity{UserID: "u1", TeamID: "t1", Scope: scopes}, nil
}
func (a *testAuth) RefreshUserToken(oldToken string) (string, error) { return "user-tok-2", nil }
func (a *testAuth) ValidateUserToken(token string) (*types.UserIdentity, error) {
	return &types.UserIdentity{UserID: "u1", TeamID: "t1"}, nil
}

func writeCtrlFrame(t *testing.T, w io.Writer, codec *FrameCodec, ns, kind string, data []byte) {
	t.Helper()
	if err := codec.WriteFrame(w, &pb.Payload{Namespace: ns, Kind: kind, Data: data}); err != nil {
		t.Fatalf("writeCtrlFrame: %v", err)
	}
}

func TestControlStream_WriteControl(t *testing.T) {
	codec := DefaultCodec()
	var buf bytes.Buffer
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", strings.NewReader(""), &noopResponseWriter{&buf}, reg, &testAuth{}, codec)

	msg := &pb.Payload{Namespace: types.NamespaceControl, Kind: "test", Data: []byte("hello")}
	if err := cs.WriteControl(msg); err != nil {
		t.Fatalf("WriteControl: %v", err)
	}

	got, err := codec.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Kind != "test" {
		t.Errorf("Kind = %q, want test", got.Kind)
	}
}

func TestControlStream_WriteControl_Closed(t *testing.T) {
	codec := DefaultCodec()
	cs := &controlStream{codec: codec, closed: true}
	err := cs.WriteControl(&pb.Payload{})
	if !errors.Is(err, errControlClosed) {
		t.Errorf("err = %v, want errControlClosed", err)
	}
}

func TestControlStream_Run_DispatchRegister(t *testing.T) {
	codec := DefaultCodec()
	pr, pw := io.Pipe()
	outPR, outPW := io.Pipe()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", pr, &noopResponseWriter{outPW}, reg, &testAuth{}, codec)

	regData, _ := proto.Marshal(&pb.Register{NodeId: "n1", Version: "1.0"})

	// Read responses concurrently (WriteControl writes to outPW which blocks without a reader).
	type result struct {
		frame *pb.Payload
		err   error
	}
	ackCh := make(chan result, 1)
	go func() {
		f, err := codec.ReadFrame(outPR)
		ackCh <- result{f, err}
	}()

	go func() {
		writeCtrlFrame(t, pw, codec, types.NamespaceControl, "register", regData)
		pw.Close()
	}()

	cs.run(context.Background())

	if !reg.bindCalled {
		t.Error("BindStream not called")
	}
	if !reg.unbindCalled {
		t.Error("UnbindStream not called after run exits")
	}

	res := <-ackCh
	if res.err != nil {
		t.Fatalf("ReadFrame ack: %v", res.err)
	}
	if res.frame.Kind != "register_ack" {
		t.Errorf("ack Kind = %q, want register_ack", res.frame.Kind)
	}
}

func TestControlStream_Run_DispatchPing(t *testing.T) {
	codec := DefaultCodec()
	pr, pw := io.Pipe()
	outPR, outPW := io.Pipe()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", pr, &noopResponseWriter{outPW}, reg, &testAuth{}, codec)

	pingData, _ := proto.Marshal(&pb.Ping{Timestamp: 12345})

	type result struct {
		frame *pb.Payload
		err   error
	}
	pongCh := make(chan result, 1)
	go func() {
		f, err := codec.ReadFrame(outPR)
		pongCh <- result{f, err}
	}()

	go func() {
		writeCtrlFrame(t, pw, codec, types.NamespaceControl, "ping", pingData)
		pw.Close()
	}()

	cs.run(context.Background())

	if !reg.pingUpdated {
		t.Error("UpdatePing not called")
	}

	res := <-pongCh
	if res.err != nil {
		t.Fatalf("ReadFrame pong: %v", res.err)
	}
	if res.frame.Kind != "pong" {
		t.Errorf("pong Kind = %q, want pong", res.frame.Kind)
	}
}

func TestControlStream_Run_UnknownKind(t *testing.T) {
	codec := DefaultCodec()
	pr, pw := io.Pipe()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", pr, &noopResponseWriter{io.Discard}, reg, &testAuth{}, codec)

	go func() {
		writeCtrlFrame(t, pw, codec, types.NamespaceControl, "unknown_cmd", nil)
		pw.Close()
	}()

	cs.run(context.Background())
	// Should not panic; just logs and continues until EOF.
}

func TestControlStream_Run_UnknownNamespace(t *testing.T) {
	codec := DefaultCodec()
	pr, pw := io.Pipe()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", pr, &noopResponseWriter{io.Discard}, reg, &testAuth{}, codec)

	go func() {
		writeCtrlFrame(t, pw, codec, "wrong.ns", "register", nil)
		pw.Close()
	}()

	cs.run(context.Background())
}

func TestControlStream_Run_EOF(t *testing.T) {
	codec := DefaultCodec()
	pr, pw := io.Pipe()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", pr, &noopResponseWriter{io.Discard}, reg, &testAuth{}, codec)

	pw.Close() // immediate EOF

	cs.run(context.Background())
	if !reg.unbindCalled {
		t.Error("UnbindStream not called on EOF")
	}
}

func TestControlStream_Run_ContextCancel(t *testing.T) {
	codec := DefaultCodec()
	pr, _ := io.Pipe()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", pr, &noopResponseWriter{io.Discard}, reg, &testAuth{}, codec)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cs.run(ctx)
	if !reg.unbindCalled {
		t.Error("UnbindStream not called on context cancel")
	}
}

func TestIsNormalClose(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		normal bool
	}{
		{"nil", nil, true},
		{"EOF", io.EOF, true},
		{"context.Canceled", context.Canceled, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"H3_error_0x0", errors.New("H3 error (0x0) (local)"), true},
		{"NO_ERROR", errors.New("application error NO_ERROR"), true},
		{"random_error", errors.New("something broke"), false},
		{"connection_reset", errors.New("connection reset by peer"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNormalClose(tc.err); got != tc.normal {
				t.Errorf("isNormalClose(%v) = %v, want %v", tc.err, got, tc.normal)
			}
		})
	}
}

// noopResponseWriter wraps an io.Writer to satisfy http.ResponseWriter for tests.
type noopResponseWriter struct {
	w io.Writer
}

func (n *noopResponseWriter) Header() http.Header         { return make(http.Header) }
func (n *noopResponseWriter) Write(p []byte) (int, error) { return n.w.Write(p) }
func (n *noopResponseWriter) WriteHeader(int)             {}
func (n *noopResponseWriter) Flush()                      {}

var _ http.ResponseWriter = (*noopResponseWriter)(nil)

func TestControlStream_Dispatch_BadRegisterData(t *testing.T) {
	codec := DefaultCodec()
	pr, pw := io.Pipe()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", pr, &noopResponseWriter{io.Discard}, reg, &testAuth{}, codec)

	go func() {
		writeCtrlFrame(t, pw, codec, types.NamespaceControl, "register", []byte("not-protobuf"))
		pw.Close()
	}()

	cs.run(context.Background())
	if reg.bindCalled {
		t.Error("BindStream should not be called on bad register data")
	}
}

func TestControlStream_Dispatch_BadPingData(t *testing.T) {
	codec := DefaultCodec()
	pr, pw := io.Pipe()
	reg := &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}
	cs := newControlStream("n1", pr, &noopResponseWriter{&bytes.Buffer{}}, reg, &testAuth{}, codec)

	go func() {
		writeCtrlFrame(t, pw, codec, types.NamespaceControl, "ping", []byte("not-protobuf"))
		pw.Close()
	}()

	cs.run(context.Background())
	if reg.pingUpdated {
		t.Error("UpdatePing should not be called on bad ping data")
	}
}

func TestIsNormalClose_WrappedErrors(t *testing.T) {
	wrapped := errors.Join(errors.New("outer"), context.Canceled)
	if !isNormalClose(wrapped) {
		t.Error("wrapped context.Canceled should be normal")
	}

	wrappedNet := errors.Join(errors.New("outer"), net.ErrClosed)
	if !isNormalClose(wrappedNet) {
		t.Error("wrapped net.ErrClosed should be normal")
	}
}

func TestControlStream_WriteControl_FlushCalled(t *testing.T) {
	codec := DefaultCodec()
	var buf bytes.Buffer
	fw := &trackingFlusher{w: &buf}
	cs := newControlStream("n1", strings.NewReader(""), fw, &trackingRegistry{testNodeRegistry: testNodeRegistry{pool: &pb.PoolConfig{}}}, &testAuth{}, codec)

	cs.WriteControl(&pb.Payload{Namespace: types.NamespaceControl, Kind: "test"})
	if !fw.flushed {
		t.Error("Flush not called")
	}
}

type trackingFlusher struct {
	w       io.Writer
	flushed bool
}

func (f *trackingFlusher) Header() http.Header         { return make(http.Header) }
func (f *trackingFlusher) Write(p []byte) (int, error) { return f.w.Write(p) }
func (f *trackingFlusher) WriteHeader(int)             {}
func (f *trackingFlusher) Flush()                      { f.flushed = true }

var _ http.ResponseWriter = (*trackingFlusher)(nil)
