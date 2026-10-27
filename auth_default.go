package tai

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Sentinel errors returned by DefaultAuthProvider.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrTokenExpired = errors.New("token expired")
	ErrNodeNotFound = errors.New("node not found")
)

// DefaultAuth creates a DefaultAuthProvider with the given Ed25519 private key.
// The private key is used for signing node_tokens and OperationTokens.
func DefaultAuth(privateKey ed25519.PrivateKey) AuthProvider {
	keyID := hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)[:8])
	return &defaultAuth{
		privateKey: privateKey,
		publicKey:  privateKey.Public().(ed25519.PublicKey),
		keyID:      keyID,
	}
}

type defaultAuth struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	keyID      string

	mu            sync.RWMutex // guards tlsInfo and transportPort
	tlsInfo       *TLSInfo
	transportPort int // overrides 6099 when non-zero (for tests with ephemeral ports)
}

// SetTLS configures TLS information returned in well-known responses.
// Call this after DefaultAuth() if the transport uses TLS.
func (a *defaultAuth) SetTLS(caCertPEM string, fingerprint string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tlsInfo = &TLSInfo{
		CACertificate: caCertPEM,
		Fingerprint:   fingerprint,
	}
}

// SetTransportPort overrides the transport_port in well-known responses.
// Used by TestServer to advertise ephemeral ports instead of the default 6099.
func (a *defaultAuth) SetTransportPort(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transportPort = port
}

// ValidateNodeToken verifies an Ed25519-signed JWT node_token.
func (a *defaultAuth) ValidateNodeToken(token string) (*NodeInfo, error) {
	claims := &nodeTokenClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.publicKey, nil
	})
	if err != nil {
		return nil, ErrUnauthorized
	}
	return &NodeInfo{NodeID: claims.NodeID, UserID: claims.UserID}, nil
}

// IssueNodeToken creates a new node_token valid for 7 days.
func (a *defaultAuth) IssueNodeToken(nodeID string) (string, error) {
	now := time.Now()
	claims := &nodeTokenClaims{
		NodeID: nodeID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
			ID:        generateID("nt"),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(a.privateKey)
}

// RefreshNodeToken validates the old token and issues a fresh one.
func (a *defaultAuth) RefreshNodeToken(oldToken string) (string, error) {
	info, err := a.ValidateNodeToken(oldToken)
	if err != nil {
		return "", err
	}
	return a.IssueNodeToken(info.NodeID)
}

// IssueOperationToken creates a session-scoped JWT valid for 30 minutes.
func (a *defaultAuth) IssueOperationToken(nodeID, sessionID string, ops []string, scope *TokenScope) (string, error) {
	now := time.Now()
	var paths []string
	if scope != nil {
		paths = scope.Paths
	}
	claims := &opTokenClaims{
		TaiID:     nodeID,
		SessionID: sessionID,
		Ops:       ops,
		Paths:     paths,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(30 * time.Minute)),
			ID:        generateID("op"),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(a.privateKey)
}

// SigningPublicKey returns the current Ed25519 public key for OperationToken verification.
func (a *defaultAuth) SigningPublicKey() (keyID string, publicKey []byte) {
	return a.keyID, []byte(a.publicKey)
}

// WellKnownInfo returns default transport metadata.
// HTTP/3 is listed first as the primary protocol.
// TLS info is included when configured via SetTLS.
func (a *defaultAuth) WellKnownInfo() *WellKnownResponse {
	a.mu.RLock()
	defer a.mu.RUnlock()
	port := 6099
	if a.transportPort != 0 {
		port = a.transportPort
	}
	// QUIC requires TLS: without TLS only the TCP (HTTP/2) path exists.
	protocols := []string{"HTTP/2"}
	if a.tlsInfo != nil {
		protocols = []string{"HTTP/3", "HTTP/2"}
	}
	return &WellKnownResponse{
		TransportPort: port,
		Protocols:     protocols,
		TLS:           a.tlsInfo,
		Version:       "0.1.0",
	}
}

// ---------------------------------------------------------------------------
// JWT claim types
// ---------------------------------------------------------------------------

type nodeTokenClaims struct {
	NodeID string `json:"node_id"`
	UserID string `json:"user_id,omitempty"`
	jwt.RegisteredClaims
}

type opTokenClaims struct {
	TaiID     string   `json:"tai_id"`
	SessionID string   `json:"session_id"`
	Ops       []string `json:"ops"`
	Paths     []string `json:"paths,omitempty"`
	jwt.RegisteredClaims
}

func generateID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
