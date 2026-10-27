package tai

// AuthProvider defines the authentication interface for the transport server.
// The SDK ships a default JWT-based implementation via DefaultAuth().
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
	// operations on a Tai node.
	IssueOperationToken(nodeID, sessionID string, ops []string, scope *TokenScope) (string, error)

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
