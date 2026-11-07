package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"golang.org/x/net/http2"

	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

// ---------------------------------------------------------------------------
// TLS fixtures
// ---------------------------------------------------------------------------

// newTestTLS creates a self-signed certificate for 127.0.0.1 and returns the
// server config plus the client root pool.
func newTestTLS(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "tai-transport-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:     []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)

	return &tls.Config{Certificates: []tls.Certificate{cert}}, roots
}

func newTestAuth(t *testing.T) types.AuthProvider {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return auth.DefaultAuth(priv)
}

// ---------------------------------------------------------------------------
// Integration server
// ---------------------------------------------------------------------------

// integrationServer is a fully wired transport server (Light + Heavy pools,
// TCP/TLS and H3 listeners) used by the stream-level integration tests.
type integrationServer struct {
	srv     *Server
	auth    types.AuthProvider
	tlsCfg  *tls.Config
	roots   *x509.CertPool
	tcpAddr string
	h3Addr  string
}

func newIntegrationServer(t *testing.T) *integrationServer {
	t.Helper()

	a := newTestAuth(t)
	cfg := &pb.PoolConfig{LightMaxStreams: 256}
	srv := NewServer(a, &testNodeRegistry{pool: cfg}, DefaultCodec())
	srv.InitLightPool(cfg)
	srv.InitHeavyPool(cfg)
	srv.SetUnauthTimeout(5 * time.Second)

	tlsCfg, roots := newTestTLS(t)
	if err := srv.StartTCP("127.0.0.1:0", tlsCfg); err != nil {
		t.Fatalf("startTCP: %v", err)
	}
	if err := srv.StartH3("127.0.0.1:0", tlsCfg); err != nil {
		t.Fatalf("startH3: %v", err)
	}

	is := &integrationServer{
		srv:     srv,
		auth:    a,
		tlsCfg:  tlsCfg,
		roots:   roots,
		tcpAddr: srv.TCPAddr(),
		h3Addr:  srv.H3Addr(),
	}
	t.Cleanup(func() {
		srv.CloseLightPool()
		srv.CloseHeavyPool()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	})
	return is
}

func (s *integrationServer) nodeToken(t *testing.T, nodeID string) string {
	t.Helper()
	token, err := s.auth.IssueNodeToken(nodeID)
	if err != nil {
		t.Fatalf("issue node token: %v", err)
	}
	return token
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Fake Tai node: Light over H3 (WebTransport)
// ---------------------------------------------------------------------------

func (s *integrationServer) connectLightH3(t *testing.T, ctx context.Context, nodeID string) *webtransport.Session {
	t.Helper()

	d := &webtransport.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    s.roots,
			NextProtos: []string{http3.NextProtoH3},
		},
		QUICConfig: &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+s.nodeToken(t, nodeID))
	hdr.Set("X-Tai-Node-Id", nodeID)

	_, sess, err := d.Dial(ctx, "https://"+s.h3Addr+"/v1/tai/light", hdr)
	if err != nil {
		d.Close()
		t.Fatalf("dial H3 light: %v", err)
	}
	t.Cleanup(func() {
		sess.CloseWithError(0, "test done")
		d.Close()
	})
	waitFor(t, "H3 light connection cached", func() bool {
		return s.srv.LightConnCount(nodeID) > 0
	})
	return sess
}

// newWTTransport builds a WebTransport client transport trusting the test cert.
func newWTTransport(is *integrationServer) *webtransport.Transport {
	return &webtransport.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    is.roots,
			NextProtos: []string{http3.NextProtoH3},
		},
		QUICConfig: &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
	}
}

// acceptH3FirstFrame accepts the next SDK-opened WebTransport stream and
// decodes its first frame.
func acceptH3FirstFrame(t *testing.T, ctx context.Context, sess *webtransport.Session) (*webtransport.Stream, *pb.Payload) {
	t.Helper()
	stream, err := sess.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("accept H3 stream: %v", err)
	}
	frame, err := DefaultCodec().ReadFrame(stream)
	if err != nil {
		t.Fatalf("read H3 first frame: %v", err)
	}
	return stream, frame
}

// ---------------------------------------------------------------------------
// Fake Tai node: Light over H2 (reversed HTTP/2: Tai is the server)
// ---------------------------------------------------------------------------

// fakeH2Light is the Tai side of a reversed Light connection.
type fakeH2Light struct {
	conn    net.Conn
	streams chan *fakeH2Stream
}

type fakeH2Stream struct {
	first *pb.Payload
	reply *pb.Payload
	done  chan struct{}
	w     http.ResponseWriter
}

func (s *integrationServer) connectLightH2(t *testing.T, ctx context.Context, nodeID string) *fakeH2Light {
	t.Helper()

	conn, err := tls.Dial("tcp", s.tcpAddr, &tls.Config{
		RootCAs:    s.roots,
		NextProtos: []string{AlpnTai},
	})
	if err != nil {
		t.Fatalf("dial H2 light: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := WriteProtoMsg(conn, &pb.Connect{
		NodeId:      nodeID,
		NodeToken:   s.nodeToken(t, nodeID),
		ConnectType: ConnTypeLight,
	}, maxConnectFrameSize); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	var ack pb.ConnectAck
	if err := ReadProtoMsg(conn, &ack, maxConnectFrameSize); err != nil {
		t.Fatalf("read connect ack: %v", err)
	}

	f := &fakeH2Light{conn: conn, streams: make(chan *fakeH2Stream, 8)}
	h2s := &http2.Server{}
	go h2s.ServeConn(conn, &http2.ServeConnOpts{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first, err := DefaultCodec().ReadFrame(r.Body)
		if err != nil {
			http.Error(w, "bad first frame", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		st := &fakeH2Stream{first: first, done: make(chan struct{}), w: w}
		f.streams <- st
		select {
		case <-st.done:
		case <-r.Context().Done():
		}
		if st.reply != nil {
			_ = DefaultCodec().WriteFrame(w, st.reply)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	})})

	waitFor(t, "H2 light connection cached", func() bool {
		return s.srv.LightConnCount(nodeID) > 0
	})
	return f
}

func (f *fakeH2Light) accept(t *testing.T, ctx context.Context) *fakeH2Stream {
	t.Helper()
	select {
	case st := <-f.streams:
		return st
	case <-ctx.Done():
		t.Fatalf("timeout accepting H2 stream: %v", ctx.Err())
		return nil
	}
}

// ---------------------------------------------------------------------------
// Fake Tai node: Heavy
// ---------------------------------------------------------------------------

func (s *integrationServer) connectHeavy(t *testing.T, nodeID string) net.Conn {
	t.Helper()

	conn, err := tls.Dial("tcp", s.tcpAddr, &tls.Config{
		RootCAs:    s.roots,
		NextProtos: []string{AlpnTai},
	})
	if err != nil {
		t.Fatalf("dial heavy: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := WriteProtoMsg(conn, &pb.Connect{
		NodeId:      nodeID,
		NodeToken:   s.nodeToken(t, nodeID),
		ConnectType: ConnTypeHeavy,
	}, maxConnectFrameSize); err != nil {
		t.Fatalf("write heavy connect: %v", err)
	}
	var ack pb.ConnectAck
	if err := ReadProtoMsg(conn, &ack, maxConnectFrameSize); err != nil {
		t.Fatalf("read heavy connect ack: %v", err)
	}
	waitFor(t, "heavy connection cached", func() bool {
		return s.srv.HeavyConnCount(nodeID) > 0
	})
	return conn
}

// ---------------------------------------------------------------------------
// Fake SDK backend for package-level helpers
// ---------------------------------------------------------------------------

type fakeBackend struct {
	openCalls   int
	pickCalls   int
	returnCalls int
	removeCalls int
	lastFrame   *pb.Payload
	lastNodeID  string
}

func (b *fakeBackend) OpenLightStream(_ context.Context, nodeID string, firstFrame *pb.Payload) (io.ReadWriteCloser, error) {
	b.openCalls++
	b.lastNodeID = nodeID
	b.lastFrame = firstFrame
	return NewLightStreamForTest(io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}), nil
}

func (b *fakeBackend) HeavyPick(nodeID string) (net.Conn, error) {
	b.pickCalls++
	b.lastNodeID = nodeID
	return nil, nil
}

func (b *fakeBackend) HeavyReturn(net.Conn) { b.returnCalls++ }

func (b *fakeBackend) HeavyRemove(net.Conn) { b.removeCalls++ }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// identityFixture builds a wire identity with a fresh user token.
func identityFixture(t *testing.T, a types.AuthProvider, nodeID string, scopes types.Scope) *pb.Identity {
	t.Helper()
	token, identity, err := a.IssueUserToken(nodeID, scopes)
	if err != nil {
		t.Fatalf("issue user token: %v", err)
	}
	return types.IdentityToPB(identity, token, 42)
}

var _ = fmt.Sprintf
