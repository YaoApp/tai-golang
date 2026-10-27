package tai

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"testing"
)

// TestServer is a self-contained test harness that starts both HTTP and
// transport servers on ephemeral ports. No external dependencies required.
type TestServer struct {
	Auth      AuthProvider
	HTTP      *httpServer
	Transport *transportServer

	// HTTPAddr is the listen address of the HTTP API (e.g., "127.0.0.1:xxxxx").
	HTTPAddr string

	// TransportAddr is the listen address of the transport layer.
	TransportAddr string
}

// NewTestServer creates and starts a TestServer with ephemeral ports and a
// fresh Ed25519 key pair. Call ts.Close() when done.
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	auth := DefaultAuth(privKey)
	httpSrv := newHTTPServer(auth)
	transportSrv := newTransportServer(auth.(*defaultAuth))

	if err := httpSrv.start("127.0.0.1:0"); err != nil {
		t.Fatalf("start http server: %v", err)
	}

	if err := transportSrv.start("127.0.0.1:0"); err != nil {
		httpSrv.close(context.Background())
		t.Fatalf("start transport server: %v", err)
	}

	ts := &TestServer{
		Auth:          auth,
		HTTP:          httpSrv,
		Transport:     transportSrv,
		HTTPAddr:      httpSrv.addr(),
		TransportAddr: transportSrv.addr(),
	}

	t.Cleanup(func() { ts.Close() })
	return ts
}

// Close shuts down both servers.
func (ts *TestServer) Close() {
	ctx := context.Background()
	ts.Transport.close(ctx)
	ts.HTTP.close(ctx)
}

// HTTPURL returns the base URL of the HTTP API (no trailing slash).
func (ts *TestServer) HTTPURL() string {
	return fmt.Sprintf("http://%s", ts.HTTPAddr)
}

// IssueTestNodeToken creates a node_token for testing.
func (ts *TestServer) IssueTestNodeToken(nodeID string) string {
	token, err := ts.Auth.IssueNodeToken(nodeID)
	if err != nil {
		panic("IssueTestNodeToken: " + err.Error())
	}
	return token
}
