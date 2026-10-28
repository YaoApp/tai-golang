package tai

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

	"github.com/yaoapp/tai-golang/pb"
)

// TestServer is a self-contained test harness that starts the HTTP API and
// the TCP and H3 transport on ephemeral ports through the production server
// assembly. No external dependencies required.
type TestServer struct {
	Server    Server
	Auth      AuthProvider
	HTTP      *httpServer
	Transport *transportServer
	Nodes     *nodeManager
	TLSConfig *tls.Config // Server TLS config (for H3 client to trust)

	// HTTPAddr is the listen address of the HTTP API.
	HTTPAddr string

	// TransportAddr is the TCP transport address (HTTP/2 and Tai frames).
	TransportAddr string

	// H3Addr is the H3 (QUIC/UDP) transport address.
	H3Addr string
}

// NewTestServer creates and starts a TestServer with ephemeral ports,
// a fresh Ed25519 key pair (for JWT), and a self-signed TLS certificate.
// TCP and H3 (QUIC/UDP) share the same port number, matching the
// production design where 6099 serves both TCP and UDP.
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()
	return newTestServer(t, 0)
}

// newTestServer is NewTestServer with a custom unauthorized-connection
// timeout (0 uses DefaultUnauthTimeout).
func newTestServer(t *testing.T, unauthTimeout time.Duration) *TestServer {
	t.Helper()

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	auth := DefaultAuth(privKey)
	tlsCfg, _, _ := generateTestTLS(t)

	s, err := newServer(&ServerConfig{Auth: auth, UnauthTimeout: unauthTimeout})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	s.httpListenAddr = "127.0.0.1:0"
	s.transportListenAddr = "127.0.0.1:0"
	s.tlsCfg = tlsCfg
	if err := s.bind(); err != nil {
		t.Fatalf("bind: %v", err)
	}

	ts := &TestServer{
		Server:        s,
		Auth:          auth,
		HTTP:          s.http,
		Transport:     s.tr,
		Nodes:         s.mgr,
		TLSConfig:     tlsCfg,
		HTTPAddr:      s.http.addr(),
		TransportAddr: s.tcpAddr,
		H3Addr:        s.udpAddr,
	}
	t.Cleanup(func() { ts.Close() })
	return ts
}

// Close shuts down all servers.
func (ts *TestServer) Close() {
	ts.Server.Close()
}

// HTTPURL returns the base URL of the HTTP API (no trailing slash).
func (ts *TestServer) HTTPURL() string {
	return fmt.Sprintf("http://%s", ts.HTTPAddr)
}

// H3URL returns the base URL of the H3 transport (https scheme).
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
	ts.Nodes.goAwayAll(reason, 0)
}

// SendControl sends an arbitrary control message to a specific node.
func (ts *TestServer) SendControl(nodeID string, msg *pb.Payload) error {
	return ts.Nodes.sendControl(nodeID, msg)
}

// LightConnCount returns the number of Light connections for a node.
func (ts *TestServer) LightConnCount(nodeID string) int {
	s := ts.Server.(*server)
	return s.light.connCount(nodeID)
}

// OpenLightStream opens a bidirectional stream on the Light pool to nodeID.
func (ts *TestServer) OpenLightStream(ctx context.Context, nodeID string, firstFrame *pb.Payload) (*LightStream, error) {
	s := ts.Server.(*server)
	return s.light.OpenStream(ctx, nodeID, firstFrame)
}

// ---------------------------------------------------------------------------
// TLS helpers
// ---------------------------------------------------------------------------

// generateTestTLS creates a self-signed TLS certificate in memory.
// Returns the tls.Config, the PEM-encoded certificate, and its SHA-256 fingerprint.
func generateTestTLS(t *testing.T) (cfg *tls.Config, certPEM []byte, fingerprint string) {
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
