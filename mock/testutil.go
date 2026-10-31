package mock

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
)

// TestServer is a self-contained test harness that starts the HTTP API and
// the transport on ephemeral ports.
type TestServer struct {
	Server    interface{ Close() error }
	Auth      types.AuthProvider
	HTTP      *transport.HTTPServer
	Transport *transport.Server
	Nodes     *transport.NodeManager
	TLSConfig *tls.Config

	HTTPAddr      string
	TransportAddr string
	H3Addr        string

	// closeFn releases all resources. Populated by NewTestServer.
	closeFn func()
}

// NewTestServer creates and starts a TestServer with ephemeral ports.
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()
	return newTestServer(t, 0)
}

// NewTestServerWithTimeout creates a TestServer with a custom unauthorized timeout.
func NewTestServerWithTimeout(t *testing.T, unauthTimeout time.Duration) *TestServer {
	t.Helper()
	return newTestServer(t, unauthTimeout)
}

func newTestServer(t *testing.T, unauthTimeout time.Duration) *TestServer {
	t.Helper()

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	a := auth.DefaultAuth(privKey)
	tlsCfg, _, _ := GenerateTestTLS(t)

	codec, err := transport.NewFrameCodec(0)
	if err != nil {
		t.Fatalf("NewFrameCodec: %v", err)
	}

	pool, _ := DefaultPoolConfig()
	mgr := transport.NewNodeManager(pool)

	tr := transport.NewServer(a, mgr, codec)
	if unauthTimeout > 0 {
		tr.SetUnauthTimeout(unauthTimeout)
	}

	httpSrv := transport.NewHTTPServer(a)
	light := pool // pool config for light pool init

	// Initialize light pool before starting listeners.
	tr.InitLightPool(light)

	// Start TCP+H3 transport.
	if err := tr.StartTCP("127.0.0.1:0", tlsCfg); err != nil {
		t.Fatalf("startTCP: %v", err)
	}
	tcpAddr := tr.TCPAddr()
	host, port, _ := net.SplitHostPort(tcpAddr)
	if err := tr.StartH3(net.JoinHostPort(host, port), tlsCfg); err != nil {
		tr.Close(context.Background())
		t.Fatalf("startH3: %v", err)
	}

	// Configure well-known info.
	if wk, ok := a.(interface {
		SetTLS(string, string)
		SetTransportPort(int)
	}); ok {
		certPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: tlsCfg.Certificates[0].Certificate[0],
		})
		hash := sha256.Sum256(tlsCfg.Certificates[0].Certificate[0])
		wk.SetTLS(string(certPEM), fmt.Sprintf("sha256:%x", hash))
		var p int
		fmt.Sscanf(port, "%d", &p)
		wk.SetTransportPort(p)
	}

	// Start HTTP API.
	if err := httpSrv.Start("127.0.0.1:0"); err != nil {
		tr.Close(context.Background())
		t.Fatalf("http start: %v", err)
	}

	ts := &TestServer{
		Auth:          a,
		HTTP:          httpSrv,
		Transport:     tr,
		Nodes:         mgr,
		TLSConfig:     tlsCfg,
		HTTPAddr:      httpSrv.Addr(),
		TransportAddr: tcpAddr,
		H3Addr:        tr.H3Addr(),
		closeFn: func() {
			tr.CloseLightPool()
			tr.Close(context.Background())
			httpSrv.Close(context.Background())
		},
	}
	t.Cleanup(func() { ts.Close() })
	return ts
}

// Close shuts down all servers.
func (ts *TestServer) Close() {
	if ts.closeFn != nil {
		ts.closeFn()
	}
}

// HTTPURL returns the base URL of the HTTP API.
func (ts *TestServer) HTTPURL() string {
	return fmt.Sprintf("http://%s", ts.HTTPAddr)
}

// H3URL returns the base URL of the H3 transport.
func (ts *TestServer) H3URL() string {
	return fmt.Sprintf("https://%s", ts.H3Addr)
}

// IssueTestNodeToken creates a node_token for testing.
func (ts *TestServer) IssueTestNodeToken(nodeID string) string {
	token, err := ts.Auth.IssueNodeToken(nodeID)
	if err != nil {
		panic("IssueTestNodeToken: " + err.Error())
	}
	return token
}

// GoAwayAll sends a go_away message to all connected nodes.
func (ts *TestServer) GoAwayAll(reason string) {
	ts.Nodes.GoAwayAll(reason, 0)
}

// SendControl sends a control message to a specific node.
func (ts *TestServer) SendControl(nodeID string, msg *pb.Payload) error {
	return ts.Nodes.SendControl(nodeID, msg)
}

// LightConnCount returns the number of Light connections for a node.
func (ts *TestServer) LightConnCount(nodeID string) int {
	return ts.Transport.LightConnCount(nodeID)
}

// OpenLightStream opens a bidirectional stream on the Light pool.
func (ts *TestServer) OpenLightStream(ctx context.Context, nodeID string, firstFrame *pb.Payload) (*transport.LightStream, error) {
	return ts.Transport.OpenLightStream(ctx, nodeID, firstFrame)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// DefaultPoolConfig returns the default pool configuration.
func DefaultPoolConfig() (*pb.PoolConfig, error) {
	return &pb.PoolConfig{
		LightMinConns:       2,
		LightMaxConns:       8,
		LightMaxStreams:     256,
		LightIdleTimeoutSec: 300,
		HeavyMinIdle:        2,
		HeavyMaxActive:      20,
		HeavyMaxIdle:        6,
		HeavyIdleTimeoutSec: 300,
		HeavyHealthCheckSec: 30,
	}, nil
}

// GenerateTestTLS creates a self-signed TLS certificate in memory.
func GenerateTestTLS(t *testing.T) (cfg *tls.Config, certPEM []byte, fingerprint string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "tai-test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	hash := sha256.Sum256(certDER)
	fingerprint = fmt.Sprintf("sha256:%x", hash)

	cfg = &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3"},
	}
	return cfg, certPEM, fingerprint
}
