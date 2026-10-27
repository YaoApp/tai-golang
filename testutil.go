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
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"
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
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	auth := DefaultAuth(privKey)
	mgr := newNodeManager()
	httpSrv := newHTTPServer(auth)
	transportSrv := newTransportServer(auth, auth.(*defaultAuth), mgr)

	// Start HTTP API
	if err := httpSrv.start("127.0.0.1:0"); err != nil {
		t.Fatalf("start http server: %v", err)
	}

	// Start H2 (TCP)
	if err := transportSrv.startH2("127.0.0.1:0"); err != nil {
		httpSrv.close(context.Background())
		t.Fatalf("start h2 transport: %v", err)
	}

	// Generate self-signed TLS cert for H3
	tlsCfg := generateTestTLS(t)

	// Start H3 (QUIC) on an ephemeral UDP port
	udpAddr := findFreeUDPPort(t)
	if err := transportSrv.startH3(udpAddr, tlsCfg); err != nil {
		transportSrv.close(context.Background())
		httpSrv.close(context.Background())
		t.Fatalf("start h3 transport: %v", err)
	}

	ts := &TestServer{
		Auth:          auth,
		HTTP:          httpSrv,
		Transport:     transportSrv,
		Nodes:         mgr,
		TLSConfig:     tlsCfg,
		HTTPAddr:      httpSrv.addr(),
		TransportAddr: transportSrv.h2Addr(),
		H3Addr:        udpAddr,
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

// ---------------------------------------------------------------------------
// TLS helpers
// ---------------------------------------------------------------------------

// generateTestTLS creates a self-signed TLS certificate in memory.
func generateTestTLS(t *testing.T) *tls.Config {
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

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3"},
	}
}

// findFreeUDPPort binds to :0, records the port, then releases the socket.
func findFreeUDPPort(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free udp port: %v", err)
	}
	addr := conn.LocalAddr().String()
	conn.Close()
	return addr
}
