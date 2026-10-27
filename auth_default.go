package tai

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Sentinel errors returned by DefaultAuthProvider.
var (
	ErrUnauthorized  = errors.New("unauthorized")
	ErrTokenExpired  = errors.New("token expired")
	ErrTicketUsed    = errors.New("ticket already used")
	ErrTicketInvalid = errors.New("invalid ticket")
	ErrTicketExpired = errors.New("ticket expired")
	ErrNodeNotFound  = errors.New("node not found")
)

// DefaultAuth creates a DefaultAuthProvider with the given Ed25519 private key.
// The private key is used for signing node_tokens and OperationTokens.
func DefaultAuth(privateKey ed25519.PrivateKey) AuthProvider {
	keyID := hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)[:8])
	ticketSecret := make([]byte, 32)
	if _, err := rand.Read(ticketSecret); err != nil {
		panic("tai: failed to generate ticket secret: " + err.Error())
	}
	return &defaultAuth{
		privateKey:   privateKey,
		publicKey:    privateKey.Public().(ed25519.PublicKey),
		keyID:        keyID,
		ticketSecret: ticketSecret,
		usedTickets:  make(map[string]time.Time),
	}
}

type defaultAuth struct {
	privateKey    ed25519.PrivateKey
	publicKey     ed25519.PublicKey
	keyID         string
	ticketSecret  []byte
	tlsInfo       *TLSInfo
	transportPort int // overrides 6099 when non-zero (for tests with ephemeral ports)

	mu          sync.Mutex
	usedTickets map[string]time.Time // ticket -> expiry, for one-time use enforcement
}

// SetTLS configures TLS information returned in well-known responses.
// Call this after DefaultAuth() if the transport uses TLS.
func (a *defaultAuth) SetTLS(caCertPEM string, fingerprint string) {
	a.tlsInfo = &TLSInfo{
		CACertificate: caCertPEM,
		Fingerprint:   fingerprint,
	}
}

// SetTransportPort overrides the transport_port in well-known responses.
// Used by TestServer to advertise ephemeral ports instead of the default 6099.
func (a *defaultAuth) SetTransportPort(port int) {
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

// IssueConnectTicket creates a 30-second HMAC-signed ticket with a random nonce.
func (a *defaultAuth) IssueConnectTicket(nodeID string, connType string) (*ConnectTicket, error) {
	expiry := time.Now().Add(30 * time.Second)
	nonce := generateID("tk")
	payload := fmt.Sprintf("%s|%s|%d|%s", nodeID, connType, expiry.Unix(), nonce)
	sig := a.hmacSign([]byte(payload))
	ticket := base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig))
	return &ConnectTicket{Ticket: ticket, ExpiresIn: 30}, nil
}

// ValidateConnectTicket verifies the HMAC signature, expiry, and one-time use.
func (a *defaultAuth) ValidateConnectTicket(ticket string) (nodeID string, connType string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil {
		return "", "", ErrTicketInvalid
	}

	parts := splitLast(string(raw), "|")
	if parts == nil {
		return "", "", ErrTicketInvalid
	}
	payload, sig := parts[0], parts[1]

	if !a.hmacVerify([]byte(payload), sig) {
		return "", "", ErrTicketInvalid
	}

	fields := splitN(payload, "|", 4)
	if len(fields) < 3 {
		return "", "", ErrTicketInvalid
	}
	nodeID = fields[0]
	connType = fields[1]
	var expiryUnix int64
	fmt.Sscanf(fields[2], "%d", &expiryUnix)

	if time.Now().Unix() > expiryUnix {
		return "", "", ErrTicketExpired
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneExpiredTickets()
	if _, used := a.usedTickets[ticket]; used {
		return "", "", ErrTicketUsed
	}
	a.usedTickets[ticket] = time.Unix(expiryUnix, 0)

	return nodeID, connType, nil
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
	port := 6099
	if a.transportPort != 0 {
		port = a.transportPort
	}
	return &WellKnownResponse{
		TransportPort: port,
		Protocols:     []string{"HTTP/3", "HTTP/2"},
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (a *defaultAuth) hmacSign(payload []byte) string {
	mac := hmac.New(sha256.New, a.ticketSecret)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *defaultAuth) hmacVerify(payload []byte, sig string) bool {
	expected := a.hmacSign(payload)
	return hmac.Equal([]byte(expected), []byte(sig))
}

func (a *defaultAuth) pruneExpiredTickets() {
	now := time.Now()
	for k, exp := range a.usedTickets {
		if now.After(exp) {
			delete(a.usedTickets, k)
		}
	}
}

func generateID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func splitLast(s, sep string) []string {
	for i := len(s) - 1; i >= 0; i-- {
		if string(s[i]) == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return nil
}

func splitN(s, sep string, n int) []string {
	result := make([]string, 0, n)
	for i := 0; i < n-1; i++ {
		idx := -1
		for j := 0; j < len(s); j++ {
			if string(s[j]) == sep {
				idx = j
				break
			}
		}
		if idx < 0 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+1:]
	}
	result = append(result, s)
	return result
}
