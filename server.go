// Package tai provides the server-side SDK for communicating with Tai nodes
// over the Tai Transport Protocol v2.
//
// Tai is a closed-source binary installed on every computer. This SDK
// implements the server side of the transport layer, enabling any server
// (e.g. Yao) to accept Tai connections, manage nodes, and run sessions.
//
// Quick start:
//
//	server, err := tai.NewServer(&tai.ServerConfig{
//	    TransportPort: 6099,
//	    HTTPPort:      5099,
//	    Auth:          tai.DefaultAuth(privateKey),
//	})
//	server.Start(ctx)
package tai

import "context"

// ServerConfig holds configuration for a Tai transport server.
type ServerConfig struct {
	// TransportPort is the port for Tai connections (TCP + UDP).
	// Default: 6099.
	TransportPort int

	// HTTPPort is the port for the HTTP API (well-known, connect, tai call).
	// Default: 5099.
	HTTPPort int

	// TLS configures TLS for transport connections.
	// Nil means no TLS (plaintext h2c, for trusted networks only).
	TLS *TLSConfig

	// Auth provides authentication and token management.
	// Use DefaultAuth() for a built-in JWT implementation.
	Auth AuthProvider

	// Pool sets initial connection pool parameters.
	// Nil uses built-in defaults (Light 2-8, Heavy 2-20).
	Pool *PoolSettings
}

// TLSConfig holds TLS certificate paths.
type TLSConfig struct {
	CertFile string // Path to TLS certificate PEM
	KeyFile  string // Path to TLS private key PEM
}

// PoolSettings configures Light and Heavy pool sizes.
type PoolSettings struct {
	LightMin uint32 // Minimum Light connections (default 2)
	LightMax uint32 // Maximum Light connections (default 8)
	HeavyMin uint32 // Minimum Heavy idle connections (default 2)
	HeavyMax uint32 // Maximum Heavy active connections (default 20)
}

// Server is a Tai transport server that accepts and manages Tai node connections.
type Server interface {
	// Start begins listening for Tai connections. Blocks until ctx is cancelled.
	Start(ctx context.Context) error

	// Addr returns the transport listener address (host:port).
	Addr() string

	// Nodes returns all currently connected nodes.
	Nodes() []*Node

	// NodeByID returns a connected node by its ID, or nil if not found.
	NodeByID(nodeID string) *Node

	// OpenSession opens a new session on a specific node.
	OpenSession(ctx context.Context, req *OpenSessionRequest) (*Session, error)

	// RegisterTool registers a server tool available for `tai call`.
	// Automatically pushes the updated tool list to all connected nodes.
	RegisterTool(tool *ToolDefinition) error

	// UnregisterTool removes a server tool by name.
	UnregisterTool(name string) error

	// SendControl sends a control message to a specific node.
	SendControl(nodeID string, msg *ControlMsg) error

	// OnNodeRegister sets a callback for node registration events.
	OnNodeRegister(fn func(node *Node) error)

	// OnSessionEvent sets a callback for session data stream events.
	OnSessionEvent(fn func(sessionID string, msg *SessionMsg))

	// Close gracefully shuts down the server.
	Close() error
}
