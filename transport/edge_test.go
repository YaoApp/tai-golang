package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

// ---------------------------------------------------------------------------
// Server accessors / rejection branches
// ---------------------------------------------------------------------------

func TestServerRegistryAccessor(t *testing.T) {
	reg := testRegistry()
	srv := NewServer(newTestAuth(t), reg, DefaultCodec())
	if srv.Registry() != reg {
		t.Error("Registry() should return the configured registry")
	}
}

type failingRegisterRegistry struct{ testNodeRegistry }

func (r *failingRegisterRegistry) Register(nodeID string, reg *pb.Register, auth types.AuthProvider) (*pb.RegisterAck, error) {
	return nil, fmt.Errorf("register rejected")
}

func TestControlStreamRegisterError(t *testing.T) {
	a := newTestAuth(t)
	reg := &failingRegisterRegistry{testNodeRegistry{pool: &pb.PoolConfig{}}}
	srv := NewServer(a, reg, DefaultCodec())
	st := NewConnState(nil, time.Second)
	defer st.Close()
	st.AuthorizeAs("node-reg-fail", ConnTypeControl)

	regData, _ := proto.Marshal(&pb.Register{NodeId: "node-reg-fail"})
	body := controlFrames(t, &pb.Payload{Namespace: types.NamespaceControl, Kind: "register", Data: regData})
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/control", bytes.NewReader(body))
	req = req.WithContext(WithConnState(req.Context(), st))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestHandleTaiConnRejections exercises the Connect-frame validation paths of
// the Tai frame protocol.
func TestHandleTaiConnRejections(t *testing.T) {
	is := newIntegrationServer(t)
	validToken, _ := is.auth.IssueNodeToken("frame-node")

	cases := []struct {
		name    string
		token   string
		nodeID  string
		connTyp string
	}{
		{name: "invalid token", token: "bad", nodeID: "frame-node", connTyp: ConnTypeLight},
		{name: "node mismatch", token: validToken, nodeID: "other-node", connTyp: ConnTypeLight},
		{name: "unknown connect type", token: validToken, nodeID: "frame-node", connTyp: "bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := tls.Dial("tcp", is.tcpAddr, &tls.Config{
				RootCAs:    is.roots,
				NextProtos: []string{AlpnTai},
			})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			if err := WriteProtoMsg(conn, &pb.Connect{NodeId: tc.nodeID, NodeToken: tc.token, ConnectType: tc.connTyp}, maxConnectFrameSize); err != nil {
				t.Fatalf("write connect: %v", err)
			}
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := conn.Read(make([]byte, 1)); err == nil {
				t.Error("rejected connection should be closed")
			}
		})
	}
}

// TestHandleHeavyConnWithoutPool covers the rejection path when a Heavy
// connection arrives but no Heavy pool was initialized.
func TestHandleHeavyConnWithoutPool(t *testing.T) {
	a := newTestAuth(t)
	cfg := &pb.PoolConfig{LightMaxStreams: 8}
	srv := NewServer(a, testRegistry(), DefaultCodec())
	srv.InitLightPool(cfg) // deliberately no InitHeavyPool
	tlsCfg, roots := newTestTLS(t)
	if err := srv.StartTCP("127.0.0.1:0", tlsCfg); err != nil {
		t.Fatalf("startTCP: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() {
		srv.CloseLightPool()
		_ = srv.Close(ctx)
	})

	token, _ := a.IssueNodeToken("heavy-no-pool")
	conn, err := tls.Dial("tcp", srv.TCPAddr(), &tls.Config{RootCAs: roots, NextProtos: []string{AlpnTai}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := WriteProtoMsg(conn, &pb.Connect{NodeId: "heavy-no-pool", NodeToken: token, ConnectType: ConnTypeHeavy}, maxConnectFrameSize); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("heavy connection without pool should be closed")
	}
}

// TestLightH3WithoutHandler covers the "no light handler registered" branch.
func TestLightH3WithoutHandler(t *testing.T) {
	a := newTestAuth(t)
	srv := NewServer(a, testRegistry(), DefaultCodec())
	tlsCfg, roots := newTestTLS(t)
	if err := srv.StartH3("127.0.0.1:0", tlsCfg); err != nil {
		t.Fatalf("StartH3: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer srv.Close(ctx)

	token, _ := a.IssueNodeToken("no-handler-node")
	d := &webtransport.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, NextProtos: []string{"h3"}},
		QUICConfig:      &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
	}
	defer d.Close()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	hdr.Set("X-Tai-Node-Id", "no-handler-node")
	_, sess, err := d.Dial(ctx, "https://"+srv.H3Addr()+"/v1/tai/light", hdr)
	if err != nil {
		// The server may also reject the upgrade before the session is usable.
		return
	}
	// A session with no handler is accepted at the WebTransport layer and then
	// closed by the server.
	sess.CloseWithError(0, "no handler")
}

// ---------------------------------------------------------------------------
// Length-prefixed proto helpers: error paths
// ---------------------------------------------------------------------------

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failed") }

func TestWriteProtoMsgErrors(t *testing.T) {
	if err := WriteProtoMsg(failingWriter{}, &pb.Connect{}, 1024); err == nil {
		t.Error("WriteProtoMsg should surface writer errors")
	}
	// Oversized message.
	big := &pb.Connect{NodeId: string(bytes.Repeat([]byte("x"), 64))}
	if err := WriteProtoMsg(io.Discard, big, 8); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("err = %v, want ErrFrameTooLarge", err)
	}
}

func TestReadProtoMsgErrors(t *testing.T) {
	var msg pb.Connect
	if err := ReadProtoMsg(bytes.NewReader([]byte{0, 0}), &msg, 1024); err == nil {
		t.Error("short header should error")
	}
	// Declared length larger than max.
	header := []byte{0, 0, 0, 9}
	if err := ReadProtoMsg(bytes.NewReader(header), &msg, 4); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("err = %v, want ErrFrameTooLarge", err)
	}
	// Body shorter than declared.
	if err := ReadProtoMsg(bytes.NewReader(append(header, 1)), &msg, 1024); err == nil {
		t.Error("short body should error")
	}
	// Invalid protobuf bytes.
	if err := ReadProtoMsg(bytes.NewReader(append([]byte{0, 0, 0, 2}, 0xff, 0xff)), &msg, 1024); err == nil {
		t.Error("invalid protobuf body should error")
	}
}

// ---------------------------------------------------------------------------
// Heavy pool rejection branches
// ---------------------------------------------------------------------------

func TestHeavyAcceptClosedPool(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	p := newTestHeavyPool()
	p.Close()
	p.Accept(server, "node")
	// Accept closed the connection on the closed pool.
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Error("connection should be closed on a closed pool")
	}
}

func TestHeavyAcceptWriteAckError(t *testing.T) {
	client, server := net.Pipe()
	client.Close() // peer gone: ConnectAck write fails
	p := newTestHeavyPool()
	defer p.Close()
	p.Accept(server, "node-ack")
	server.Close()
}

// ---------------------------------------------------------------------------
// Datagram error path on H3 with a cancelled context
// ---------------------------------------------------------------------------

func TestReceiveDatagramContextCanceled(t *testing.T) {
	is := newIntegrationServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	sess := is.connectLightH3(t, ctx, "dg-cancel")
	cancel()

	stream, err := is.srv.OpenLightStream(context.Background(), "dg-cancel", FirstFrame(types.NamespaceSession, "open", nil, nil))
	if err != nil {
		t.Fatalf("OpenLightStream: %v", err)
	}
	ls := stream.(*LightStream)
	acceptCtx, acceptCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer acceptCancel()
	if _, err := sess.AcceptStream(acceptCtx); err != nil {
		t.Fatalf("accept stream: %v", err)
	}

	// No datagrams are in flight: the receive must observe the context deadline.
	recvCtx, recvCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer recvCancel()
	if _, err := ls.DatagramConn().ReceiveDatagram(recvCtx); err == nil {
		t.Error("ReceiveDatagram should fail when the context expires")
	}
}

// TestLightPoolOpenStreamBrokenConn covers the retry paths of OpenStream when
// the cached H2 connection can no longer carry a stream.
func TestLightPoolOpenStreamBrokenConn(t *testing.T) {
	pool := newLightPool(&pb.PoolConfig{LightMaxStreams: 4}, DefaultCodec(), testRegistry(), newTestAuth(t))
	defer pool.close()

	client, remote := net.Pipe()
	go io.Copy(io.Discard, remote)
	cc, err := (&http2.Transport{AllowHTTP: true}).NewClientConn(client)
	if err != nil {
		t.Fatalf("NewClientConn: %v", err)
	}
	// Peer gone: every subsequent RoundTrip fails.
	remote.Close()
	lc := &lightConn{nodeID: "broken-node", proto: "H2", h2cc: cc, h2conn: client, pool: pool}
	if !pool.addConn(lc) {
		t.Fatal("addConn failed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := pool.OpenStream(ctx, "broken-node", &pb.Payload{}); err == nil {
		t.Fatal("OpenStream on a broken connection should fail")
	}
	if !lc.closed.Load() {
		t.Error("broken connection should be marked closed")
	}
}
