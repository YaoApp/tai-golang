// Package auth provides the default JWT-based AuthProvider implementation.
package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
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
		privateKey:   privateKey,
		publicKey:    privateKey.Public().(ed25519.PublicKey),
		keyID:        keyID,
		userTokenTTL: 30 * time.Minute,
	}
}

type defaultAuth struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	keyID      string

	mu              sync.RWMutex
	tlsInfo         *types.TLSInfo
	transportPort   int
	userResolver    func(nodeID string, scopes types.Scope) (*types.UserIdentity, error)
	userResolverCtx func(ctx context.Context, nodeID string, scopes types.Scope) (*types.UserIdentity, error)

	userTokenTTL time.Duration
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

// SigningPublicKey returns the current Ed25519 public key in PEM format
// for OperationToken verification on the Tai node side.
func (a *defaultAuth) SigningPublicKey() (keyID string, publicKey []byte) {
	pkix, err := x509.MarshalPKIXPublicKey(ed25519.PublicKey(a.publicKey))
	if err != nil {
		return a.keyID, []byte(a.publicKey)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pkix,
	})
	return a.keyID, pemBytes
}

// ProtocolVersion is the transport protocol revision advertised in well-known
// discovery. It is not the SDK's software version: the SDK is a library and
// carries no release number, while this value identifies the wire protocol a
// client must speak. Bump it only when the protocol changes incompatibly.
//
// Pre-release builds append a build stamp, e.g. "2.0.0-nightly.20261106".
const ProtocolVersion = "2.0.0"

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
		Version:       ProtocolVersion,
	}
}

// ---------------------------------------------------------------------------
// User credentials
// ---------------------------------------------------------------------------

// SetUserResolver installs the business-context resolver used by
// IssueUserToken. When nil, a single local identity (local-user/local-team)
// is returned so the demo app works out of the box.
func (a *defaultAuth) SetUserResolver(fn func(nodeID string, scopes types.Scope) (*types.UserIdentity, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.userResolver = fn
}

// SetUserResolverContext installs a request-scoped resolver used by
// IssueUserTokenContext. It wins over SetUserResolver when both are set.
func (a *defaultAuth) SetUserResolverContext(fn func(ctx context.Context, nodeID string, scopes types.Scope) (*types.UserIdentity, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.userResolverCtx = fn
}

// IssueUserTokenContext resolves the caller from the request context (login
// state) and signs a user credential. Falls back to IssueUserToken when no
// context resolver is installed.
func (a *defaultAuth) IssueUserTokenContext(ctx context.Context, nodeID string, scopes types.Scope) (string, *types.UserIdentity, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.RLock()
	resolverCtx := a.userResolverCtx
	resolver := a.userResolver
	a.mu.RUnlock()

	var identity *types.UserIdentity
	switch {
	case resolverCtx != nil:
		var err error
		identity, err = resolverCtx(ctx, nodeID, scopes)
		if err != nil {
			return "", nil, err
		}
	case resolver != nil:
		var err error
		identity, err = resolver(nodeID, scopes)
		if err != nil {
			return "", nil, err
		}
	}
	return a.signResolvedIdentity(identity, scopes)
}

// IssueUserToken resolves the caller identity from the business context and
// signs a user credential carrying the requested scopes.
func (a *defaultAuth) IssueUserToken(nodeID string, scopes types.Scope) (string, *types.UserIdentity, error) {
	a.mu.RLock()
	resolver := a.userResolver
	a.mu.RUnlock()

	var identity *types.UserIdentity
	if resolver != nil {
		var err error
		identity, err = resolver(nodeID, scopes)
		if err != nil {
			return "", nil, err
		}
	}
	return a.signResolvedIdentity(identity, scopes)
}

// signResolvedIdentity applies the shared fallback/validation and signs.
func (a *defaultAuth) signResolvedIdentity(identity *types.UserIdentity, scopes types.Scope) (string, *types.UserIdentity, error) {
	if identity == nil {
		identity = &types.UserIdentity{UserID: "local-user", TeamID: "local-team"}
	}
	if identity.UserID == "" || identity.TeamID == "" {
		return "", nil, ErrUnauthorized
	}
	// The returned identity must reflect exactly what was signed: when the
	// resolver does not populate Scope, mirror the requested scopes.
	if len(identity.Scope) == 0 {
		identity.Scope = scopes
	}
	token, err := a.signUserToken(identity, scopes, time.Now())
	if err != nil {
		return "", nil, err
	}
	return token, identity, nil
}

// RefreshUserToken re-signs the credential carried by oldToken, inheriting its
// identity and scopes and only renewing the validity window.
func (a *defaultAuth) RefreshUserToken(oldToken string) (string, error) {
	claims, err := a.parseUserToken(oldToken)
	if err != nil {
		return "", err
	}
	identity := &types.UserIdentity{
		UserID:   claims.UserID,
		TeamID:   claims.TeamID,
		Scope:    types.Scope(claims.Scope),
		Metadata: claims.Metadata,
	}
	return a.signUserToken(identity, identity.Scope, time.Now())
}

// ValidateUserToken verifies a user credential and returns its identity.
func (a *defaultAuth) ValidateUserToken(token string) (*types.UserIdentity, error) {
	claims, err := a.parseUserToken(token)
	if err != nil {
		return nil, err
	}
	return &types.UserIdentity{
		UserID:   claims.UserID,
		TeamID:   claims.TeamID,
		Scope:    types.Scope(claims.Scope),
		Metadata: claims.Metadata,
	}, nil
}

// UserTokenExpiresAt reports the expiry carried by token. It verifies the
// signature but returns the expiry even for an already-expired credential:
// the value is a GC hint for the Tai daemon, not an authorization decision.
func (a *defaultAuth) UserTokenExpiresAt(token string) (time.Time, error) {
	claims := &userTokenClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.publicKey, nil
	})
	if err != nil {
		return time.Time{}, ErrUnauthorized
	}
	if claims.ExpiresAt == nil {
		return time.Time{}, nil
	}
	return claims.ExpiresAt.Time, nil
}

func (a *defaultAuth) signUserToken(identity *types.UserIdentity, scopes types.Scope, now time.Time) (string, error) {
	if identity == nil || identity.UserID == "" || identity.TeamID == "" {
		return "", ErrUnauthorized
	}
	claims := &userTokenClaims{
		UserID:   identity.UserID,
		TeamID:   identity.TeamID,
		Scope:    []string(scopes),
		Metadata: identity.Metadata,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.userTokenTTL)),
			ID:        GenerateID("ut"),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(a.privateKey)
}

func (a *defaultAuth) parseUserToken(token string) (*userTokenClaims, error) {
	claims := &userTokenClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.publicKey, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrUnauthorized
	}
	if claims.UserID == "" || claims.TeamID == "" {
		return nil, ErrUnauthorized
	}
	return claims, nil
}

// ---------------------------------------------------------------------------
// JWT claim types
// ---------------------------------------------------------------------------

type nodeTokenClaims struct {
	NodeID string `json:"node_id"`
	UserID string `json:"user_id,omitempty"`
	jwt.RegisteredClaims
}

type userTokenClaims struct {
	UserID   string         `json:"user_id"`
	TeamID   string         `json:"team_id"`
	Scope    []string       `json:"scope,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
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
