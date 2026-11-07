// Package types defines shared data types for the Tai SDK.
// It has no dependencies beyond the standard library.
package types

import (
	"context"
	"strings"
	"time"
)

// AuthProvider defines the authentication interface for the transport server.
// The SDK ships a default JWT-based implementation via auth.DefaultAuth().
// Servers (e.g. Yao) can supply their own implementation to integrate with
// their user database and token infrastructure.
type AuthProvider interface {
	// ValidateNodeToken verifies a node_token and returns node information.
	// Called with the first message of every Control, Light and Heavy connection.
	ValidateNodeToken(token string) (*NodeInfo, error)

	// IssueNodeToken creates a new node_token for a node.
	// Called during initial node registration (e.g. Web UI "add this machine").
	IssueNodeToken(nodeID string) (string, error)

	// RefreshNodeToken issues a replacement for an expiring node_token.
	RefreshNodeToken(oldToken string) (string, error)

	// IssueOperationToken creates a session-scoped JWT for authorizing
	// operations on a Tai node. Deprecated: v1 path only.
	IssueOperationToken(nodeID, sessionID string, ops []string, scope *TokenScope) (string, error)

	// IssueUserToken issues a user credential for one business operation.
	// The caller knows the target node and required scopes; the implementation
	// resolves the user from its own business context and returns the identity.
	IssueUserToken(nodeID string, scopes Scope) (token string, identity *UserIdentity, err error)

	// RefreshUserToken re-signs the old credential, inheriting its identity and
	// scopes and only renewing the validity window. It returns the new token.
	RefreshUserToken(oldToken string) (string, error)

	// ValidateUserToken verifies a user credential and returns its identity.
	ValidateUserToken(token string) (*UserIdentity, error)

	// SigningPublicKey returns the current public key for OperationToken
	// verification. Distributed to Tai nodes via RegisterAck.
	SigningPublicKey() (keyID string, publicKey []byte)

	// WellKnownInfo returns the well-known transport endpoint metadata.
	WellKnownInfo() *WellKnownResponse
}

// NodeInfo is returned by ValidateNodeToken on success.
type NodeInfo struct {
	NodeID string
	UserID string // Optional: associated user
}

// TokenScope restricts an OperationToken to specific paths.
type TokenScope struct {
	Paths []string // Glob patterns (e.g. "/workspace/**")
}

// WellKnownResponse is served at GET /.well-known/yao-transport.
type WellKnownResponse struct {
	TransportPort int      `json:"transport_port"`
	Protocols     []string `json:"protocols"`
	TLS           *TLSInfo `json:"tls,omitempty"`
	TaoServer     string   `json:"tao_server,omitempty"`
	Version       string   `json:"version"`
}

// TLSInfo describes TLS configuration in the well-known response.
type TLSInfo struct {
	CACertificate string `json:"ca_certificate,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
}

// ---------------------------------------------------------------------------
// User identity credentials (v2)
// ---------------------------------------------------------------------------

// Scope is a set of action identifiers, formatted as "<resource>:<operation>".
// A resource-level wildcard ("workspace:*") covers every action of that
// resource; there is no global wildcard.
type Scope []string

const (
	SessionAccess   = "session:access"
	WorkspaceRead   = "workspace:read"
	WorkspaceWrite  = "workspace:write"
	WorkspaceDelete = "workspace:delete"
	WorkspaceAll    = "workspace:*"
	RemoteToolCall  = "remote_tool:call"
)

// Has reports whether the scope contains action (exact or resource wildcard).
func (s Scope) Has(action string) bool {
	for _, a := range s {
		if a == action {
			return true
		}
		if strings.HasSuffix(a, ":*") && strings.HasPrefix(action, strings.TrimSuffix(a, "*")) {
			return true
		}
	}
	return false
}

// UserTokenContextProvider is optionally implemented by AuthProviders that
// resolve the caller from the request context (login state). The SDK passes the
// context of the Session open/resume so implementations can read the current
// user without global state.
type UserTokenContextProvider interface {
	IssueUserTokenContext(ctx context.Context, nodeID string, scopes Scope) (token string, identity *UserIdentity, err error)
}

// UserTokenExpiry is optionally implemented by AuthProviders that can report
// the expiry of a credential they issued. The transport uses it only to fill
// the local GC hint carried in Identity.expires_at; Tai never enforces it.
type UserTokenExpiry interface {
	UserTokenExpiresAt(token string) (time.Time, error)
}

// UserClaims is the SDK-defined payload contract of a user credential.
// Consumers implementing AuthProvider may map their own JWT claims onto it.
type UserClaims struct {
	UserID    string         `json:"user_id"`
	TeamID    string         `json:"team_id"`
	Scope     []string       `json:"scope,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	ExpiresAt time.Time      `json:"expires_at"`
	IssuedAt  time.Time      `json:"issued_at"`
	ID        string         `json:"jti"`
}

// UserIdentity is the verified in-memory form of a user credential.
// Tai SDK never consumes this type; it exists for SDK consumers.
type UserIdentity struct {
	UserID   string
	TeamID   string
	Scope    Scope
	Metadata map[string]any
}
