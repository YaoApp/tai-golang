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

// TestServer is a self-contained test harness that starts HTTP API, H2, and H3
// transport servers on ephemeral ports. No external dependencies required.
type TestServer struct {
	Auth      AuthProvider
	HTTP      *httpServer
	Transport *transportServer
	Nodes     *nodeManager
	TLSConfig *tls.Config // Server TLS config (for H3 client to trust)

	// HTTPAddr is the listen address of the HTTP API.
	HTTPAddr string

	// TransportAddr is the H2 (TCP) transport address.
	TransportAddr string

	// H3Addr is the H3 (QUIC/UDP) transport address.
	H3Addr string
}

// NewTestServer creates and starts a TestServer with ephemeral ports,
// a fresh Ed25519 key pair (for JWT), and a self-signed TLS certificate (for H3).
// H2 (TCP) and H3 (QUIC/UDP) share the same port number, matching the
// production design where 6099 serves both TCP and UDP.
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	auth := DefaultAuth(privKey)
	mgr := newNodeManager()
	httpSrv := newHTTPServer(auth)
	transportSrv := newTransportServer(auth, auth.(*defaultAuth), mgr, defaultCodec)

	// Generate self-signed TLS cert for H3
	tlsCfg, certPEM, fingerprint := generateTestTLS(t)

	// Set TLS info so well-known includes ca_certificate and fingerprint
	auth.(*defaultAuth).SetTLS(string(certPEM), fingerprint)

	// Start HTTP API
	if err := httpSrv.start("127.0.0.1:0"); err != nil {
		t.Fatalf("start http server: %v", err)
	}

	// Start H2 (TCP/TLS) on ephemeral port — same TLS cert as H3
	if err := transportSrv.startH2("127.0.0.1:0", tlsCfg); err != nil {
		httpSrv.close(context.Background())
		t.Fatalf("start h2 transport: %v", err)
	}

	// Extract the port from H2 address, then bind H3 (UDP) to the same port.
	// TCP and UDP can share the same port number.
	h2Addr := transportSrv.h2Addr()
	_, h2Port, _ := net.SplitHostPort(h2Addr)
	h3Addr := "127.0.0.1:" + h2Port
	if err := transportSrv.startH3(h3Addr, tlsCfg); err != nil {
		transportSrv.close(context.Background())
		httpSrv.close(context.Background())
		t.Fatalf("start h3 transport: %v", err)
	}

	// Set transport_port in well-known to the shared port
	port := 0
	fmt.Sscanf(h2Port, "%d", &port)
	auth.(*defaultAuth).SetTransportPort(port)

	ts := &TestServer{
		Auth:          auth,
		HTTP:          httpSrv,
		Transport:     transportSrv,
		Nodes:         mgr,
		TLSConfig:     tlsCfg,
		HTTPAddr:      httpSrv.addr(),
		TransportAddr: h2Addr,
		H3Addr:        h3Addr,
	}

	t.Cleanup(func() { ts.Close() })
	return ts
}

// Close shuts down all servers.
func (ts *TestServer) Close() {
	ctx := context.Background()
	ts.Transport.close(ctx)
	ts.HTTP.close(ctx)
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
func (ts *TestServer) SendControl(nodeID string, msg *pb.ControlMessage) error {
	return ts.Nodes.sendControl(nodeID, msg)
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
