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

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/yaoapp/tai-golang/pb"
)

// ServerConfig holds configuration for a Tai transport server.
type ServerConfig struct {
	// TransportPort is the port for Tai connections (TCP + UDP).
	// Default: 6099.
	TransportPort int

	// HTTPPort is the port for the HTTP API (well-known, token refresh, heartbeat, tai call).
	// Default: 5099.
	HTTPPort int

	// TLS configures TLS for transport connections.
	// Nil means no TLS (plaintext h2c, for trusted networks only).
	TLS *TLSConfig

	// Auth provides authentication and token management.
	// Use DefaultAuth() for a built-in JWT implementation.
	Auth AuthProvider

	// MaxFrameSize sets the maximum Control stream frame payload size in bytes.
	// 0 uses DefaultMaxFrameSize (16 MB). Range: [64 KB, 64 MB].
	MaxFrameSize int

	// UnauthTimeout bounds how long a connection may stay unauthorized after
	// its transport handshake. 0 uses DefaultUnauthTimeout (5s).
	UnauthTimeout time.Duration

	// QUIC tunes the QUIC listener (keep-alive period, idle timeout, stream
	// limits). Datagrams and stream reset partial delivery are always
	// enabled because WebTransport requires them. Nil uses quic-go defaults.
	QUIC *quic.Config

	// ShutdownTimeout bounds how long Close waits for in-flight HTTP/2
	// requests before closing their connections. 0 uses
	// DefaultShutdownTimeout (5s).
	ShutdownTimeout time.Duration

	// Pool sets the connection pool parameters sent in register_ack.
	// Nil, or a zero field, uses the default of that field.
	Pool *PoolSettings
}

// TLSConfig holds TLS certificate paths.
type TLSConfig struct {
	CertFile string // Path to TLS certificate PEM
	KeyFile  string // Path to TLS private key PEM
}

// PoolSettings configures the Light and Heavy pools the node maintains. A
// zero field selects its default; NewServer rejects a minimum above its
// maximum.
type PoolSettings struct {
	LightMin            uint32 // Minimum Light connections (default 2)
	LightMax            uint32 // Maximum Light connections (default 8)
	LightMaxStreams     uint32 // Maximum streams per Light connection (default 256)
	LightIdleTimeoutSec uint32 // Idle Light connection reclaim (default 300)
	HeavyMin            uint32 // Minimum Heavy idle connections (default 2)
	HeavyMax            uint32 // Maximum Heavy active connections (default 20)
	HeavyMaxIdle        uint32 // Maximum Heavy idle connections (default 6)
	HeavyIdleTimeoutSec uint32 // Idle Heavy connection reclaim (default 300)
	HeavyHealthCheckSec uint32 // Heavy health check interval (default 30)
}

// poolConfig returns the register_ack pool_config with defaults applied.
func (p *PoolSettings) poolConfig() (*pb.PoolConfig, error) {
	var s PoolSettings
	if p != nil {
		s = *p
	}
	orDefault := func(v, def uint32) uint32 {
		if v == 0 {
			return def
		}
		return v
	}
	cfg := &pb.PoolConfig{
		LightMinConns:       orDefault(s.LightMin, 2),
		LightMaxConns:       orDefault(s.LightMax, 8),
		LightMaxStreams:     orDefault(s.LightMaxStreams, 256),
		LightIdleTimeoutSec: orDefault(s.LightIdleTimeoutSec, 300),
		HeavyMinIdle:        orDefault(s.HeavyMin, 2),
		HeavyMaxActive:      orDefault(s.HeavyMax, 20),
		HeavyMaxIdle:        orDefault(s.HeavyMaxIdle, 6),
		HeavyIdleTimeoutSec: orDefault(s.HeavyIdleTimeoutSec, 300),
		HeavyHealthCheckSec: orDefault(s.HeavyHealthCheckSec, 30),
	}
	if cfg.LightMinConns > cfg.LightMaxConns {
		return nil, fmt.Errorf("tai: Pool.LightMin %d exceeds LightMax %d", cfg.LightMinConns, cfg.LightMaxConns)
	}
	if cfg.HeavyMinIdle > cfg.HeavyMaxIdle {
		return nil, fmt.Errorf("tai: Pool.HeavyMin %d exceeds HeavyMaxIdle %d", cfg.HeavyMinIdle, cfg.HeavyMaxIdle)
	}
	if cfg.HeavyMaxIdle > cfg.HeavyMaxActive {
		return nil, fmt.Errorf("tai: Pool.HeavyMaxIdle %d exceeds HeavyMax %d", cfg.HeavyMaxIdle, cfg.HeavyMaxActive)
	}
	return cfg, nil
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

// ErrNotImplemented is returned by Server methods whose behavior is not yet
// available in this SDK version.
var ErrNotImplemented = errors.New("tai: not implemented")

// DefaultShutdownTimeout is how long Close waits for in-flight HTTP/2
// requests (such as Control streams) before closing their connections.
const DefaultShutdownTimeout = 5 * time.Second

const (
	defaultTransportPort = 6099
	defaultHTTPPort      = 5099
)

// wellKnownConfigurer is implemented by the default AuthProvider so the
// server can advertise its TLS certificate and bound transport port.
type wellKnownConfigurer interface {
	SetTLS(caCertPEM string, fingerprint string)
	SetTransportPort(port int)
}

// server is the Server implementation: it wires the HTTP API (5099), the
// TCP/UDP transport (6099) and the node manager.
type server struct {
	cfg  ServerConfig
	auth AuthProvider
	mgr  *nodeManager
	http *httpServer
	tr   *transportServer

	// Overrides used by the test harness: listen addresses (default
	// ":<port>") and an in-memory TLS configuration (default: cfg.TLS files).
	httpListenAddr      string
	transportListenAddr string
	tlsCfg              *tls.Config

	mu        sync.Mutex
	bound     bool
	closed    bool
	tcpAddr   string
	udpAddr   string
	closeOnce sync.Once
	closeErr  error
	done      chan struct{}

	sessionEvent func(sessionID string, msg *SessionMsg)
}

// NewServer creates a transport server from cfg. Nothing is bound until
// Start. It fails when cfg.Auth is nil or cfg.MaxFrameSize is out of range.
func NewServer(cfg *ServerConfig) (Server, error) {
	return newServer(cfg)
}

func newServer(cfg *ServerConfig) (*server, error) {
	if cfg == nil || cfg.Auth == nil {
		return nil, errors.New("tai: ServerConfig.Auth is required")
	}
	codec, err := NewFrameCodec(cfg.MaxFrameSize)
	if err != nil {
		return nil, err
	}
	if cfg.UnauthTimeout < 0 || cfg.ShutdownTimeout < 0 {
		return nil, errors.New("tai: UnauthTimeout and ShutdownTimeout must not be negative")
	}

	c := *cfg
	if c.TransportPort == 0 {
		c.TransportPort = defaultTransportPort
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = defaultHTTPPort
	}

	pool, err := c.Pool.poolConfig()
	if err != nil {
		return nil, err
	}
	mgr := newNodeManager()
	mgr.pool = pool
	s := &server{
		cfg:  c,
		auth: c.Auth,
		mgr:  mgr,
		http: newHTTPServer(c.Auth),
		tr:   newTransportServer(c.Auth, mgr, codec),
		done: make(chan struct{}),
	}
	s.tr.unauthTimeout = c.UnauthTimeout
	s.tr.quicConfig = c.QUIC
	return s, nil
}

// bind opens every listener: the HTTP API, TCP, and (with TLS) UDP/H3 on the
// same port number as TCP.
func (s *server) bind() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bound {
		return errors.New("tai: server already started")
	}
	if s.closed {
		return errors.New("tai: server closed")
	}

	tlsCfg, certPEM, fingerprint, err := s.loadTLS()
	if err != nil {
		return err
	}

	tcpAddr := s.transportListenAddr
	if tcpAddr == "" {
		tcpAddr = ":" + strconv.Itoa(s.cfg.TransportPort)
	}
	if err := s.tr.startTCP(tcpAddr, tlsCfg); err != nil {
		return err
	}
	s.tcpAddr = s.tr.tcpAddr()
	host, port, _ := net.SplitHostPort(s.tcpAddr)

	if tlsCfg != nil {
		if err := s.tr.startH3(net.JoinHostPort(host, port), tlsCfg); err != nil {
			s.tr.close(context.Background())
			return err
		}
		s.udpAddr = s.tr.h3Addr()
	}

	if wk, ok := s.auth.(wellKnownConfigurer); ok {
		if tlsCfg != nil {
			wk.SetTLS(string(certPEM), fingerprint)
		}
		if p, err := strconv.Atoi(port); err == nil {
			wk.SetTransportPort(p)
		}
	}

	// The HTTP API starts last so well-known never advertises a stale port or
	// missing TLS information.
	httpAddr := s.httpListenAddr
	if httpAddr == "" {
		httpAddr = ":" + strconv.Itoa(s.cfg.HTTPPort)
	}
	if err := s.http.start(httpAddr); err != nil {
		s.tr.close(context.Background())
		return err
	}
	s.bound = true
	return nil
}

// loadTLS returns the TLS configuration, the certificate PEM advertised in
// well-known, and the leaf certificate fingerprint. All are zero when TLS is
// not configured.
func (s *server) loadTLS() (*tls.Config, []byte, string, error) {
	cfg := s.tlsCfg
	var certPEM []byte
	if cfg == nil {
		if s.cfg.TLS == nil {
			return nil, nil, "", nil
		}
		pair, err := tls.LoadX509KeyPair(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
		if err != nil {
			return nil, nil, "", fmt.Errorf("tai: load TLS key pair: %w", err)
		}
		certPEM, err = os.ReadFile(s.cfg.TLS.CertFile)
		if err != nil {
			return nil, nil, "", fmt.Errorf("tai: read TLS certificate: %w", err)
		}
		cfg = &tls.Config{Certificates: []tls.Certificate{pair}}
	}
	if len(cfg.Certificates) == 0 || len(cfg.Certificates[0].Certificate) == 0 {
		return nil, nil, "", errors.New("tai: TLS configuration has no certificate")
	}
	leaf := cfg.Certificates[0].Certificate[0]
	if certPEM == nil {
		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf})
	}
	sum := sha256.Sum256(leaf)
	return cfg, certPEM, fmt.Sprintf("sha256:%x", sum), nil
}

// Start binds the listeners and blocks until ctx is cancelled, then shuts the
// server down. Bind failures are returned immediately.
func (s *server) Start(ctx context.Context) error {
	if err := s.bind(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-s.done:
	}
	return s.Close()
}

// Addr returns the TCP transport address, or "" before Start has bound it.
func (s *server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tcpAddr
}

func (s *server) Nodes() []*Node { return s.mgr.allNodes() }

func (s *server) NodeByID(nodeID string) *Node { return s.mgr.nodeByID(nodeID) }

func (s *server) OpenSession(ctx context.Context, req *OpenSessionRequest) (*Session, error) {
	return nil, ErrNotImplemented
}

func (s *server) RegisterTool(tool *ToolDefinition) error { return ErrNotImplemented }

func (s *server) UnregisterTool(name string) error { return ErrNotImplemented }

func (s *server) SendControl(nodeID string, msg *ControlMsg) error {
	return s.mgr.sendControl(nodeID, &pb.Payload{
		Namespace: NamespaceControl,
		Kind:      msg.Kind,
		Data:      msg.Data,
	})
}

func (s *server) OnNodeRegister(fn func(node *Node) error) { s.mgr.setOnRegister(fn) }

func (s *server) OnSessionEvent(fn func(sessionID string, msg *SessionMsg)) {
	s.mu.Lock()
	s.sessionEvent = fn
	s.mu.Unlock()
}

// Close shuts down the transport and HTTP servers. It is safe to call more
// than once and before Start.
func (s *server) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		bound := s.bound
		s.mu.Unlock()
		close(s.done)
		if !bound {
			return
		}
		timeout := s.cfg.ShutdownTimeout
		if timeout == 0 {
			timeout = DefaultShutdownTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := s.tr.close(ctx)
		if herr := s.http.close(ctx); herr != nil && err == nil {
			err = herr
		}
		s.closeErr = err
	})
	return s.closeErr
}
