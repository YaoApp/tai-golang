package types

// TunnelProvider defines the interface for tunnel authorization and domain assignment.
// The SDK provides a default implementation; servers can supply their own.
type TunnelProvider interface {
	// Authorize checks whether a tunnel binding is allowed.
	// Returns the assigned domain on success, or an error on rejection.
	Authorize(nodeID string, req *TunnelBindRequest) (domain string, err error)
}

// TunnelBindRequest is the input for TunnelProvider.Authorize().
type TunnelBindRequest struct {
	TunnelID  string
	Target    string // e.g. "localhost:3000"
	Protocol  string // "http" | "tcp"
	TLS       bool
	SessionID string // Optional owning session
}
