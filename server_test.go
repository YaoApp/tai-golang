package tai

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
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
	"github.com/yaoapp/tai-golang/pb"
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

// writeCertFiles writes a self-signed certificate and key into dir.
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
	return DefaultAuth(priv)
}

// startServer runs s.Start in the background and waits until it is bound.
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
	auth := testAuth(t)
	register := func(cfg *ServerConfig) *pb.PoolConfig {
		t.Helper()
		cfg.Auth = auth
		s, err := newServer(cfg)
		if err != nil {
			t.Fatalf("newServer: %v", err)
		}
		ack, err := s.mgr.register("n1", &pb.Register{}, auth)
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
	auth := testAuth(t)
	s, err := NewServer(&ServerConfig{
		TransportPort: tcp,
		HTTPPort:      http5,
		TLS:           writeCertFiles(t, t.TempDir()),
		Auth:          auth,
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
	var wk WellKnownResponse
	json.NewDecoder(resp.Body).Decode(&wk)
	resp.Body.Close()
	if wk.TransportPort != tcp {
		t.Errorf("transport_port=%d, want %d", wk.TransportPort, tcp)
	}
	if wk.TLS == nil || wk.TLS.CACertificate == "" || wk.TLS.Fingerprint == "" {
		t.Errorf("well-known TLS info missing: %+v", wk.TLS)
	}

	// UDP side is bound on the same port: a second UDP listener must fail.
	if pc, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(tcp)); err == nil {
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

	pc, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(tcp))
	if err != nil {
		t.Fatalf("UDP port should be free without TLS: %v", err)
	}
	pc.Close()
}

func TestServerBindErrors(t *testing.T) {
	t.Run("bad TLS files", func(t *testing.T) {
		s, _ := NewServer(&ServerConfig{
			TransportPort: freePort(t), HTTPPort: freePort(t), Auth: testAuth(t),
			TLS: &TLSConfig{CertFile: "/nonexistent/cert", KeyFile: "/nonexistent/key"},
		})
		if err := s.Start(context.Background()); err == nil {
			t.Error("expected TLS load error")
		}
	})

	t.Run("http port busy", func(t *testing.T) {
		ln, _ := net.Listen("tcp", ":0")
		defer ln.Close()
		s, _ := NewServer(&ServerConfig{
			TransportPort: freePort(t), Auth: testAuth(t),
			HTTPPort: ln.Addr().(*net.TCPAddr).Port,
		})
		if err := s.Start(context.Background()); err == nil {
			t.Error("expected HTTP bind error")
		}
	})

	t.Run("transport port busy", func(t *testing.T) {
		ln, _ := net.Listen("tcp", ":0")
		defer ln.Close()
		s, _ := NewServer(&ServerConfig{
			HTTPPort: freePort(t), Auth: testAuth(t),
			TransportPort: ln.Addr().(*net.TCPAddr).Port,
		})
		if err := s.Start(context.Background()); err == nil {
			t.Error("expected transport bind error")
		}
	})

	t.Run("udp port busy", func(t *testing.T) {
		tcp := freePort(t)
		pc, err := net.ListenPacket("udp", ":"+strconv.Itoa(tcp))
		if err != nil {
			t.Skip("udp port taken")
		}
		defer pc.Close()
		s, _ := NewServer(&ServerConfig{
			TransportPort: tcp, HTTPPort: freePort(t), Auth: testAuth(t),
			TLS: writeCertFiles(t, t.TempDir()),
		})
		if err := s.Start(context.Background()); err == nil {
			t.Error("expected H3 bind error")
		}
	})

	t.Run("start twice", func(t *testing.T) {
		s, _ := NewServer(&ServerConfig{TransportPort: freePort(t), HTTPPort: freePort(t), Auth: testAuth(t)})
		cancel, done := startServer(t, s)
		defer func() { cancel(); <-done }()
		if err := s.Start(context.Background()); err == nil {
			t.Error("second Start must fail")
		}
	})

	t.Run("start after close", func(t *testing.T) {
		s, _ := NewServer(&ServerConfig{TransportPort: freePort(t), HTTPPort: freePort(t), Auth: testAuth(t)})
		s.Close()
		if err := s.Start(context.Background()); err == nil {
			t.Error("Start after Close must fail")
		}
	})
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
	ts := NewTestServer(t)
	srv := ts.Server

	registered := make(chan string, 1)
	srv.OnNodeRegister(func(n *Node) error {
		registered <- n.ID
		return nil
	})

	mock := NewMockNode("srv-node", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mock.SetNodeToken(ts.IssueTestNodeToken("srv-node"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mock.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mock.Stop()
	if err := mock.WaitRegistered(5 * time.Second); err != nil {
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
	if n := srv.NodeByID("srv-node"); n == nil {
		t.Error("NodeByID returned nil")
	}
	if len(srv.Nodes()) != 1 {
		t.Errorf("Nodes=%d, want 1", len(srv.Nodes()))
	}
	if srv.NodeByID("missing") != nil {
		t.Error("unknown node must be nil")
	}

	got := make(chan string, 1)
	mock.OnControl(func(msg *pb.Payload) { got <- msg.Namespace + "/" + msg.Kind })
	if err := srv.SendControl("srv-node", &ControlMsg{Kind: "custom", Data: []byte("x")}); err != nil {
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

	if err := srv.SendControl("missing", &ControlMsg{Kind: "x"}); err != ErrNodeNotFound {
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

func TestLoadTLSRejectsEmptyConfig(t *testing.T) {
	s, _ := newServer(&ServerConfig{Auth: testAuth(t)})
	s.tlsCfg = &tls.Config{}
	if _, _, _, err := s.loadTLS(); err == nil {
		t.Error("expected error for TLS config without certificate")
	}
}

func TestServerPassesQUICConfig(t *testing.T) {
	s, err := newServer(&ServerConfig{Auth: testAuth(t), QUIC: &quic.Config{MaxIdleTimeout: 77 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if s.tr.quicConfig == nil || s.tr.quicConfig.MaxIdleTimeout != 77*time.Second {
		t.Errorf("QUIC config not passed to the transport: %+v", s.tr.quicConfig)
	}
}
