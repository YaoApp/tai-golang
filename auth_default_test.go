package tai

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func newTestAuth(t *testing.T) (*defaultAuth, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := DefaultAuth(priv).(*defaultAuth)
	return auth, priv
}

func TestIssueAndValidateNodeToken(t *testing.T) {
	auth, _ := newTestAuth(t)

	token, err := auth.IssueNodeToken("node-001")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	info, err := auth.ValidateNodeToken(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if info.NodeID != "node-001" {
		t.Errorf("got NodeID=%q, want node-001", info.NodeID)
	}
}

func TestValidateNodeToken_InvalidToken(t *testing.T) {
	auth, _ := newTestAuth(t)

	_, err := auth.ValidateNodeToken("garbage.token.data")
	if err != ErrUnauthorized {
		t.Errorf("got %v, want ErrUnauthorized", err)
	}
}

func TestValidateNodeToken_WrongKey(t *testing.T) {
	auth1, _ := newTestAuth(t)
	auth2, _ := newTestAuth(t)

	token, _ := auth1.IssueNodeToken("node-001")
	_, err := auth2.ValidateNodeToken(token)
	if err != ErrUnauthorized {
		t.Errorf("got %v, want ErrUnauthorized", err)
	}
}

func TestRefreshNodeToken(t *testing.T) {
	auth, _ := newTestAuth(t)

	oldToken, _ := auth.IssueNodeToken("node-002")
	newToken, err := auth.RefreshNodeToken(oldToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if newToken == oldToken {
		t.Error("refreshed token should differ from original")
	}

	info, err := auth.ValidateNodeToken(newToken)
	if err != nil {
		t.Fatalf("validate refreshed: %v", err)
	}
	if info.NodeID != "node-002" {
		t.Errorf("NodeID=%q, want node-002", info.NodeID)
	}
}

func TestRefreshNodeToken_Invalid(t *testing.T) {
	auth, _ := newTestAuth(t)

	_, err := auth.RefreshNodeToken("bad-token")
	if err == nil {
		t.Error("expected error for invalid token refresh")
	}
}

func TestIssueOperationToken(t *testing.T) {
	auth, _ := newTestAuth(t)

	token, err := auth.IssueOperationToken("node-005", "sess-001", []string{"file.read", "file.write"}, nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
}

func TestIssueOperationToken_WithScope(t *testing.T) {
	auth, _ := newTestAuth(t)

	scope := &TokenScope{Paths: []string{"/tmp", "/home"}}
	token, err := auth.IssueOperationToken("node-006", "sess-002", []string{"file.read"}, scope)
	if err != nil {
		t.Fatalf("issue with scope: %v", err)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
}

func TestSigningPublicKey(t *testing.T) {
	auth, _ := newTestAuth(t)

	keyID, pubKey := auth.SigningPublicKey()
	if keyID == "" {
		t.Error("keyID should not be empty")
	}
	if len(pubKey) != ed25519.PublicKeySize {
		t.Errorf("pubKey length=%d, want %d", len(pubKey), ed25519.PublicKeySize)
	}
}

func TestWellKnownInfo(t *testing.T) {
	auth, _ := newTestAuth(t)

	// Without TLS there is no QUIC: only the TCP path is advertised.
	if plain := auth.WellKnownInfo(); len(plain.Protocols) != 1 || plain.Protocols[0] != "HTTP/2" {
		t.Errorf("Protocols without TLS=%v, want [HTTP/2]", plain.Protocols)
	}

	auth.SetTLS("pem", "sha256:abc")
	info := auth.WellKnownInfo()
	if info.TransportPort != 6099 {
		t.Errorf("TransportPort=%d, want 6099", info.TransportPort)
	}
	if len(info.Protocols) != 2 {
		t.Fatalf("Protocols=%v, want [HTTP/3 HTTP/2]", info.Protocols)
	}
	if info.Protocols[0] != "HTTP/3" {
		t.Errorf("Protocols[0]=%q, want HTTP/3 (primary)", info.Protocols[0])
	}
	if info.Protocols[1] != "HTTP/2" {
		t.Errorf("Protocols[1]=%q, want HTTP/2 (fallback)", info.Protocols[1])
	}
	if info.Version == "" {
		t.Error("Version should not be empty")
	}
}

func TestWellKnownConfigureConcurrently(t *testing.T) {
	auth, _ := newTestAuth(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			auth.SetTLS("pem", "sha256:abc")
			auth.SetTransportPort(6000 + i)
		}
	}()
	for i := 0; i < 200; i++ {
		_ = auth.WellKnownInfo()
	}
	<-done

	wk := auth.WellKnownInfo()
	if wk.TLS == nil || wk.TransportPort != 6199 {
		t.Fatalf("well-known after configure: %+v", wk)
	}
}

func TestGenerateID(t *testing.T) {
	id1 := generateID("test")
	id2 := generateID("test")
	if id1 == id2 {
		t.Error("IDs should be unique")
	}
	if len(id1) < 5 {
		t.Error("ID too short")
	}
}
