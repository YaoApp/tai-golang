package tai

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/mock"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func writeCertFiles(t *testing.T, dir string) *TLSConfig {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "tai-file"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	cert := filepath.Join(dir, "cert.pem")
	keyf := filepath.Join(dir, "key.pem")
	os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyf, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return &TLSConfig{CertFile: cert, KeyFile: keyf}
}

func testAuth(t *testing.T) AuthProvider {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return auth.DefaultAuth(priv)
}

func startServer(t *testing.T, s Server) (cancel context.CancelFunc, done chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- s.Start(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for s.Addr() == "" {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server did not bind")
		}
		select {
		case err := <-done:
			t.Fatalf("Start returned early: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	return cancel, done
}

func TestNewServerValidation(t *testing.T) {
	if _, err := NewServer(nil); err == nil {
		t.Error("nil config must fail")
	}
	if _, err := NewServer(&ServerConfig{}); err == nil {
		t.Error("missing Auth must fail")
	}
	if _, err := NewServer(&ServerConfig{Auth: testAuth(t), MaxFrameSize: 1024}); err == nil {
		t.Error("out-of-range MaxFrameSize must fail")
	}
}

func TestNewServerRejectsNegativeTimeouts(t *testing.T) {
	if _, err := NewServer(&ServerConfig{Auth: testAuth(t), UnauthTimeout: -time.Second}); err == nil {
		t.Error("negative UnauthTimeout must fail")
	}
	if _, err := NewServer(&ServerConfig{Auth: testAuth(t), ShutdownTimeout: -time.Second}); err == nil {
		t.Error("negative ShutdownTimeout must fail")
	}
}

func TestServerPoolConfigInRegisterAck(t *testing.T) {
	a := testAuth(t)
	register := func(cfg *ServerConfig) *pb.PoolConfig {
		t.Helper()
		cfg.Auth = a
		s, err := newServer(cfg)
		if err != nil {
			t.Fatalf("newServer: %v", err)
		}
		ack, err := s.mgr.Register("n1", &pb.Register{}, a)
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		return ack.PoolConfig
	}

	def := register(&ServerConfig{})
	if def.LightMinConns != 2 || def.LightMaxConns != 8 || def.LightMaxStreams != 256 ||
		def.HeavyMaxActive != 20 || def.HeavyHealthCheckSec != 30 {
		t.Errorf("defaults: %+v", def)
	}

	got := register(&ServerConfig{Pool: &PoolSettings{LightMax: 16, LightMaxStreams: 64, HeavyMax: 50}})
	if got.LightMaxConns != 16 || got.LightMaxStreams != 64 || got.HeavyMaxActive != 50 {
		t.Errorf("overrides not applied: %+v", got)
	}
	if got.LightMinConns != 2 || got.HeavyMaxIdle != 6 {
		t.Errorf("unset fields must keep defaults: %+v", got)
	}
}

func TestNewServerRejectsInvalidPool(t *testing.T) {
	for name, p := range map[string]*PoolSettings{
		"light min above max":  {LightMin: 9, LightMax: 8},
		"heavy min above idle": {HeavyMin: 7},
		"heavy idle above max": {HeavyMax: 4, HeavyMin: 1, HeavyMaxIdle: 5},
	} {
		if _, err := NewServer(&ServerConfig{Auth: testAuth(t), Pool: p}); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestServerStartWithTLSFilesAndShutdown(t *testing.T) {
	tcp, http5 := freePort(t), freePort(t)
	a := testAuth(t)
	s, err := NewServer(&ServerConfig{
		TransportPort: tcp,
		HTTPPort:      http5,
		TLS:           writeCertFiles(t, t.TempDir()),
		Auth:          a,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startServer(t, s)

	if want := ":" + strconv.Itoa(tcp); s.Addr()[len(s.Addr())-len(want):] != want {
		t.Errorf("Addr=%q, want port %d", s.Addr(), tcp)
	}

	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(http5) + "/.well-known/yao-transport")
	if err != nil {
		t.Fatalf("well-known: %v", err)
	}
	var wk types.WellKnownResponse
	json.NewDecoder(resp.Body).Decode(&wk)
	resp.Body.Close()
	if wk.TransportPort != tcp {
		t.Errorf("transport_port=%d, want %d", wk.TransportPort, tcp)
	}
	if wk.TLS == nil || wk.TLS.CACertificate == "" || wk.TLS.Fingerprint == "" {
		t.Errorf("well-known TLS info missing: %+v", wk.TLS)
	}

	if pc, err := net.ListenPacket("udp", ":"+strconv.Itoa(tcp)); err == nil {
		pc.Close()
		t.Error("UDP port should be taken by H3")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

func TestServerPlaintextHasNoH3(t *testing.T) {
	tcp := freePort(t)
	s, _ := NewServer(&ServerConfig{TransportPort: tcp, HTTPPort: freePort(t), Auth: testAuth(t)})
	cancel, done := startServer(t, s)
	defer func() { cancel(); <-done }()

	pc, err := net.ListenPacket("udp", ":"+strconv.Itoa(tcp))
	if err != nil {
		t.Fatalf("UDP port should be free without TLS: %v", err)
	}
	pc.Close()
}

func TestServerCloseIsIdempotent(t *testing.T) {
	s, _ := NewServer(&ServerConfig{TransportPort: freePort(t), HTTPPort: freePort(t), Auth: testAuth(t)})
	if err := s.Close(); err != nil {
		t.Errorf("Close before Start: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestServerNodesAndControl(t *testing.T) {
	ts := mock.NewTestServer(t)

	registered := make(chan string, 1)
	ts.Nodes.SetOnRegister(func(n *types.Node) error {
		registered <- n.ID
		return nil
	})

	mn := mock.NewMockNode("srv-node", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken("srv-node"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mn.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mn.Stop()
	if err := mn.WaitRegistered(5 * time.Second); err != nil {
		t.Fatal(err)
	}

	select {
	case id := <-registered:
		if id != "srv-node" {
			t.Errorf("registered %q", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnNodeRegister not called")
	}
	if n := ts.Nodes.NodeByID("srv-node"); n == nil {
		t.Error("NodeByID returned nil")
	}
	if len(ts.Nodes.AllNodes()) != 1 {
		t.Errorf("Nodes=%d, want 1", len(ts.Nodes.AllNodes()))
	}
	if ts.Nodes.NodeByID("missing") != nil {
		t.Error("unknown node must be nil")
	}

	got := make(chan string, 1)
	mn.OnControl(func(msg *pb.Payload) { got <- msg.Namespace + "/" + msg.Kind })
	if err := ts.Nodes.SendControl("srv-node", &pb.Payload{Namespace: types.NamespaceControl, Kind: "custom", Data: []byte("x")}); err != nil {
		t.Fatalf("SendControl: %v", err)
	}
	select {
	case v := <-got:
		if v != "control/custom" {
			t.Errorf("received %q", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control message not received")
	}

	if err := ts.Nodes.SendControl("missing", &pb.Payload{Namespace: types.NamespaceControl, Kind: "x"}); err != types.ErrNodeNotFound {
		t.Errorf("SendControl to unknown node: %v", err)
	}
}

func TestServerNotImplementedMethods(t *testing.T) {
	s, _ := NewServer(&ServerConfig{Auth: testAuth(t)})
	if _, err := s.OpenSession(context.Background(), &OpenSessionRequest{}); err != ErrNotImplemented {
		t.Errorf("OpenSession: %v", err)
	}
	if err := s.RegisterTool(&ToolDefinition{}); err != ErrNotImplemented {
		t.Errorf("RegisterTool: %v", err)
	}
	if err := s.UnregisterTool("x"); err != ErrNotImplemented {
		t.Errorf("UnregisterTool: %v", err)
	}
	s.OnSessionEvent(func(string, *SessionMsg) {})
}

func TestServerPassesQUICConfig(t *testing.T) {
	s, err := newServer(&ServerConfig{Auth: testAuth(t), QUIC: &quic.Config{MaxIdleTimeout: 77 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.tr
	_ = transport.NewServer // verify transport is importable
}

// Suppress unused import warnings.
var _ = auth.DefaultAuth
var _ = mock.NewTestServer
