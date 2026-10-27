package tai

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func decodeTicketRaw(ticket string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(ticket)
	return string(b), err
}

func encodeTicketRaw(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

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

func TestIssueAndValidateConnectTicket(t *testing.T) {
	auth, _ := newTestAuth(t)

	ticket, err := auth.IssueConnectTicket("node-003", "control")
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	if ticket.ExpiresIn != 30 {
		t.Errorf("ExpiresIn=%d, want 30", ticket.ExpiresIn)
	}

	nodeID, connType, err := auth.ValidateConnectTicket(ticket.Ticket)
	if err != nil {
		t.Fatalf("validate ticket: %v", err)
	}
	if nodeID != "node-003" {
		t.Errorf("nodeID=%q, want node-003", nodeID)
	}
	if connType != "control" {
		t.Errorf("connType=%q, want control", connType)
	}
}

func TestConnectTicket_OneTimeUse(t *testing.T) {
	auth, _ := newTestAuth(t)

	ticket, _ := auth.IssueConnectTicket("node-004", "light")
	auth.ValidateConnectTicket(ticket.Ticket) // first use OK

	_, _, err := auth.ValidateConnectTicket(ticket.Ticket)
	if err != ErrTicketUsed {
		t.Errorf("second use: got %v, want ErrTicketUsed", err)
	}
}

func TestConnectTicket_Invalid(t *testing.T) {
	auth, _ := newTestAuth(t)

	_, _, err := auth.ValidateConnectTicket("!!!not-base64!!!")
	if err != ErrTicketInvalid {
		t.Errorf("got %v, want ErrTicketInvalid", err)
	}
}

func TestConnectTicket_TamperedSignature(t *testing.T) {
	auth, _ := newTestAuth(t)

	ticket, _ := auth.IssueConnectTicket("node-tamper", "control")

	// Decode, corrupt the HMAC portion, re-encode
	raw, _ := decodeTicketRaw(ticket.Ticket)
	parts := splitLast(raw, "|")
	if parts == nil {
		t.Fatal("unexpected ticket format")
	}
	corrupted := parts[0] + "|" + "0000000000000000000000000000000000000000000000000000000000000000"
	tampered := encodeTicketRaw(corrupted)

	_, _, err := auth.ValidateConnectTicket(tampered)
	if err != ErrTicketInvalid {
		t.Errorf("got %v, want ErrTicketInvalid", err)
	}
}

func TestConnectTicket_NoSeparator(t *testing.T) {
	auth, _ := newTestAuth(t)
	// Base64 of a string with no pipe separator
	_, _, err := auth.ValidateConnectTicket("bm9waXBl") // "nopipe"
	if err != ErrTicketInvalid {
		t.Errorf("got %v, want ErrTicketInvalid", err)
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

func TestPruneExpiredTickets(t *testing.T) {
	auth, _ := newTestAuth(t)

	auth.mu.Lock()
	auth.usedTickets["expired-ticket"] = time.Now().Add(-1 * time.Hour)
	auth.usedTickets["valid-ticket"] = time.Now().Add(1 * time.Hour)
	auth.mu.Unlock()

	auth.mu.Lock()
	auth.pruneExpiredTickets()
	auth.mu.Unlock()

	auth.mu.Lock()
	defer auth.mu.Unlock()
	if _, exists := auth.usedTickets["expired-ticket"]; exists {
		t.Error("expired ticket should have been pruned")
	}
	if _, exists := auth.usedTickets["valid-ticket"]; !exists {
		t.Error("valid ticket should still exist")
	}
}

func TestSplitN_EdgeCases(t *testing.T) {
	// No separator found
	result := splitN("nosep", "|", 3)
	if len(result) != 1 || result[0] != "nosep" {
		t.Errorf("no sep: got %v", result)
	}

	// Fewer fields than n — splitN stops splitting when no more separators
	result = splitN("a|b", "|", 5)
	if len(result) != 2 || result[0] != "a" || result[1] != "b" {
		t.Errorf("fewer fields: got %v, want [a b]", result)
	}

	// Exact n
	result = splitN("x|y|z", "|", 3)
	if len(result) != 3 || result[0] != "x" || result[1] != "y" || result[2] != "z" {
		t.Errorf("exact n: got %v", result)
	}
}

func TestSplitLast(t *testing.T) {
	// Normal case
	result := splitLast("a|b|c", "|")
	if result == nil || result[0] != "a|b" || result[1] != "c" {
		t.Errorf("normal: got %v", result)
	}

	// No separator
	result = splitLast("nosep", "|")
	if result != nil {
		t.Errorf("no sep: got %v, want nil", result)
	}

	// Single separator
	result = splitLast("a|b", "|")
	if result == nil || result[0] != "a" || result[1] != "b" {
		t.Errorf("single: got %v", result)
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
