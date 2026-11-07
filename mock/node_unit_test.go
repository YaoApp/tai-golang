package mock

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

func TestMockNodeGettersBeforeStart(t *testing.T) {
	mn := NewMockNode("idle", "http://127.0.0.1:1", "", "")
	if mn.TaiID() != "" {
		t.Errorf("TaiID = %q, want empty", mn.TaiID())
	}
	if mn.Protocol() != "" {
		t.Errorf("Protocol = %q, want empty", mn.Protocol())
	}
	if mn.PoolConfig() != nil {
		t.Errorf("PoolConfig = %+v, want nil", mn.PoolConfig())
	}
	if err := mn.WaitRegistered(10 * time.Millisecond); err == nil {
		t.Error("WaitRegistered should time out before registration")
	}
	if err := mn.SendControl(&pb.Payload{}); err == nil {
		t.Error("SendControl before connect should fail")
	}
	if _, err := mn.RecvControl(); err == nil {
		t.Error("RecvControl before connect should fail")
	}
	mn.Stop()
	mn.Stop() // idempotent
}

func TestMockNodeDiscoverError(t *testing.T) {
	mn := NewMockNode("disc", "http://127.0.0.1:1", "", "")
	if _, err := mn.Discover(); err == nil {
		t.Error("Discover against a closed port should fail")
	}
}

func TestMockNodeConnectErrors(t *testing.T) {
	mn := NewMockNode("conn", "http://127.0.0.1:1", "", "")
	if err := mn.Connect(context.Background(), []string{"HTTP/3"}); err == nil {
		t.Error("Connect without a node token should fail")
	}

	mn.SetNodeToken("token")
	if err := mn.Connect(context.Background(), []string{"BOGUS"}); err == nil {
		t.Error("Connect with unsupported protocols should fail")
	}
	// HTTP/3 skipped when no h3 address, HTTP/2 skipped when no h2 address.
	if err := mn.Connect(context.Background(), []string{"HTTP/3", "HTTP/2"}); err == nil {
		t.Error("Connect without transport addresses should fail")
	}
}

func TestMockNodeConnectHeavyErrors(t *testing.T) {
	mn := NewMockNode("heavy-err", "http://127.0.0.1:1", "", "")
	if _, err := mn.ConnectHeavy(); err == nil {
		t.Error("ConnectHeavy without node token should fail")
	}
	mn.SetNodeToken("token")
	if _, err := mn.ConnectHeavy(); err == nil {
		t.Error("ConnectHeavy without h2 address should fail")
	}
	mn.h2Addr = "127.0.0.1:1"
	if _, err := mn.ConnectHeavy(); err == nil {
		t.Error("ConnectHeavy against a closed port should fail")
	}
}

func TestMockNodeConnectLightErrors(t *testing.T) {
	mn := NewMockNode("light-err", "http://127.0.0.1:1", "", "")
	ctx := context.Background()
	if _, err := mn.ConnectLightH3(ctx); err == nil {
		t.Error("ConnectLightH3 without token should fail")
	}
	if _, err := mn.ConnectLightH2(ctx); err == nil {
		t.Error("ConnectLightH2 without token should fail")
	}
	mn.SetNodeToken("token")
	if _, err := mn.ConnectLightH3(ctx); err == nil {
		t.Error("ConnectLightH3 without h3 address should fail")
	}
	if _, err := mn.ConnectLightH2(ctx); err == nil {
		t.Error("ConnectLightH2 without h2 address should fail")
	}
	mn.h3Addr = "127.0.0.1:1"
	mn.h2Addr = "127.0.0.1:1"
	if _, err := mn.ConnectLightH3(ctx); err == nil {
		t.Error("ConnectLightH3 against a closed port should fail")
	}
	if _, err := mn.ConnectLightH2(ctx); err == nil {
		t.Error("ConnectLightH2 against a closed port should fail")
	}
}

func TestMockNodeEnsureControlReader(t *testing.T) {
	mn := NewMockNode("reader", "", "", "")
	if err := mn.ensureControlReader(); err != nil {
		t.Errorf("nil pending channel should be a no-op, got %v", err)
	}

	// Error result.
	ch := make(chan httpResult, 1)
	ch <- httpResult{err: io.ErrUnexpectedEOF}
	mn.pendingCh = ch
	if err := mn.ensureControlReader(); err == nil {
		t.Error("expected the pending error")
	}

	// Non-200 response.
	ch = make(chan httpResult, 1)
	ch <- httpResult{resp: &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(nil)}}
	mn.pendingCh = ch
	if err := mn.ensureControlReader(); err == nil {
		t.Error("expected the non-200 status error")
	}
}

func TestMockNodeRefreshToken(t *testing.T) {
	ts := NewTestServer(t)
	mn := NewMockNode("refresh", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken("refresh"))

	token, err := mn.RefreshToken()
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if token == "" {
		t.Error("refreshed token is empty")
	}

	// Bad URL → transport error.
	bad := NewMockNode("refresh-bad", "http://127.0.0.1:1", "", "")
	if _, err := bad.RefreshToken(); err == nil {
		t.Error("RefreshToken against a closed port should fail")
	}
}

// pingWriter accumulates written bytes, decodes complete frames and forwards
// them on a channel, avoiding concurrent buffer access from the test.
type pingWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
	ch  chan *pb.Payload
}

func (w *pingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		if len(data) < 4 {
			return len(p), nil
		}
		size := int(binary.BigEndian.Uint32(data[:4]))
		if len(data) < 4+size {
			return len(p), nil
		}
		frame, err := transport.DefaultCodec().ReadFrame(bytes.NewReader(data[:4+size]))
		w.buf.Next(4 + size)
		if err != nil {
			continue
		}
		select {
		case w.ch <- frame:
		default:
		}
	}
}

func (w *pingWriter) Close() error { return nil }

func TestMockNodeStartPing(t *testing.T) {
	mn := NewMockNode("ping", "", "", "")
	w := &pingWriter{ch: make(chan *pb.Payload, 8)}
	mn.controlW = w
	ctx, cancel := context.WithCancel(context.Background())
	mn.StartPing(ctx, 5*time.Millisecond)

	var frame *pb.Payload
	select {
	case frame = <-w.ch:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("no ping frame was sent")
	}
	cancel()
	if frame.Kind != "ping" {
		t.Errorf("kind = %q, want ping", frame.Kind)
	}
	var ping pb.Ping
	if err := proto.Unmarshal(frame.Data, &ping); err != nil {
		t.Errorf("unmarshal ping: %v", err)
	}
}

func TestCloseClient(t *testing.T) {
	closeClient(&http.Client{Transport: &http.Transport{}})
	closeClient(&http.Client{Transport: &http3.Transport{}})
	closeClient(&http.Client{}) // no transport: must not panic
}

func TestH2CClient(t *testing.T) {
	mn := NewMockNode("h2c", "", "", "")
	if mn.h2cClient() == nil {
		t.Error("h2cClient returned nil")
	}
}

func TestMockHeavyConnClose(t *testing.T) {
	empty := &MockHeavyConn{}
	empty.Close() // nil conn is a no-op

	client, server := net.Pipe()
	defer client.Close()
	hc := &MockHeavyConn{Conn: server}
	hc.Close()
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Error("Close should close the underlying connection")
	}
}

func TestHandleH2StreamBadFirstFrame(t *testing.T) {
	lc := &MockLightConn{proto: "H2"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/stream", io.NopCloser(newBadReader()))
	lc.handleH2Stream(rec, req, transport.DefaultCodec())
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// badReader yields bytes that are not a valid length-prefixed frame.
type badReader struct{ sent bool }

func newBadReader() *badReader { return &badReader{} }

func (r *badReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	return copy(p, []byte{0xff, 0xff, 0xff, 0xff}), nil
}

func TestMockNodeOnControl(t *testing.T) {
	mn := NewMockNode("oncontrol", "", "", "")
	called := false
	mn.OnControl(func(*pb.Payload) { called = true })
	mn.mu.Lock()
	handler := mn.onControl
	mn.mu.Unlock()
	if handler == nil {
		t.Fatal("OnControl did not install the handler")
	}
	handler(&pb.Payload{})
	if !called {
		t.Error("handler was not invoked")
	}
}

var _ = tls.Config{}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (failWriter) Close() error              { return nil }

func TestMockNodeFailurePaths(t *testing.T) {
	// Register before the control stream exists.
	mn := NewMockNode("nreg", "http://127.0.0.1:1", "", "")
	if _, err := mn.Register(); err == nil {
		t.Error("Register before connect should fail")
	}

	// Start surfaces discovery/connect errors.
	if err := mn.Start(context.Background()); err == nil {
		t.Error("Start against an unreachable server should fail")
	}
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/yao-transport":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer okServer.Close()
	bad := NewMockNode("nstart", okServer.URL, "", "")
	if err := bad.Start(context.Background()); err == nil {
		t.Error("Start should fail when discovery fails")
	}
	// Discovery succeeds but Connect has no node token.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.WellKnownResponse{})
	}))
	defer good.Close()
	noToken := NewMockNode("nstart2", good.URL, "", "")
	if err := noToken.Start(context.Background()); err == nil {
		t.Error("Start without a node token should fail")
	}

	// Discover rejects non-200 and malformed bodies.
	badStatus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer badStatus.Close()
	if _, err := NewMockNode("d1", badStatus.URL, "", "").Discover(); err == nil {
		t.Error("Discover should reject non-200")
	}
	badJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{not-json"))
	}))
	defer badJSON.Close()
	if _, err := NewMockNode("d2", badJSON.URL, "", "").Discover(); err == nil {
		t.Error("Discover should reject malformed JSON")
	}

	// SendControl surfaces write errors.
	broken := NewMockNode("nwrite", "", "", "")
	broken.controlW = failWriter{}
	if err := broken.SendControl(&pb.Payload{}); err == nil {
		t.Error("SendControl should surface write errors")
	}

	// RefreshToken rejects an invalid credential and non-200 responses.
	ts := NewTestServer(t)
	rn := NewMockNode("refresh-bad-token", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	rn.SetNodeToken("bogus")
	if _, err := rn.RefreshToken(); err == nil {
		t.Error("RefreshToken with an invalid token should fail")
	}

	// authorize rejects an invalid bearer token.
	if err := rn.authorize(context.Background(), http.DefaultClient, ts.HTTPURL()); err == nil {
		t.Error("authorize with an invalid token should fail")
	}
}
