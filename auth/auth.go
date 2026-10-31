// Package auth provides the default JWT-based AuthProvider implementation.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/yaoapp/tai-golang/types"
)

// Sentinel errors returned by DefaultAuth.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrTokenExpired = errors.New("token expired")
)

// DefaultAuth creates an AuthProvider backed by Ed25519 JWT signing.
// The private key signs node_tokens and OperationTokens.
func DefaultAuth(privateKey ed25519.PrivateKey) types.AuthProvider {
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

	mu            sync.RWMutex
	tlsInfo       *types.TLSInfo
	transportPort int
}

// SetTLS configures TLS information returned in well-known responses.
func (a *defaultAuth) SetTLS(caCertPEM string, fingerprint string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tlsInfo = &types.TLSInfo{
		CACertificate: caCertPEM,
		Fingerprint:   fingerprint,
	}
}

// SetTransportPort overrides the transport_port in well-known responses.
func (a *defaultAuth) SetTransportPort(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transportPort = port
}

// ValidateNodeToken verifies an Ed25519-signed JWT node_token.
func (a *defaultAuth) ValidateNodeToken(token string) (*types.NodeInfo, error) {
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
	return &types.NodeInfo{NodeID: claims.NodeID, UserID: claims.UserID}, nil
}

// IssueNodeToken creates a new node_token valid for 7 days.
func (a *defaultAuth) IssueNodeToken(nodeID string) (string, error) {
	now := time.Now()
	claims := &nodeTokenClaims{
		NodeID: nodeID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
			ID:        GenerateID("nt"),
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
func (a *defaultAuth) IssueOperationToken(nodeID, sessionID string, ops []string, scope *types.TokenScope) (string, error) {
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
			ID:        GenerateID("op"),
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
func (a *defaultAuth) WellKnownInfo() *types.WellKnownResponse {
	a.mu.RLock()
	defer a.mu.RUnlock()
	port := 6099
	if a.transportPort != 0 {
		port = a.transportPort
	}
	protocols := []string{"HTTP/2"}
	if a.tlsInfo != nil {
		protocols = []string{"HTTP/3", "HTTP/2"}
	}
	return &types.WellKnownResponse{
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

// GenerateID creates a random prefixed identifier (e.g. "nt_a1b2c3...").
func GenerateID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
