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
//	    Auth:          auth.DefaultAuth(privateKey),
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

	"github.com/yaoapp/tai-golang/node"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"github.com/yaoapp/tai-golang/workspace"
)

// Re-export types from sub-packages for backward compatibility.
type (
	Node               = types.Node
	NodeRuntime        = types.NodeRuntime
	RemoteNode         = types.RemoteNode
	AuthProvider       = types.AuthProvider
	NodeInfo           = types.NodeInfo
	TokenScope         = types.TokenScope
	WellKnownResponse  = types.WellKnownResponse
	TLSInfo            = types.TLSInfo
	Session            = types.Session
	SessionMsg         = types.SessionMsg
	ControlMsg         = types.ControlMsg
	OpenSessionRequest = types.OpenSessionRequest
	SessionRequest     = types.SessionRequest
	UserMessage        = types.UserMessage
	ToolDefinition     = types.ToolDefinition
	ToolHandler        = types.ToolHandler
	TunnelProvider     = types.TunnelProvider
	TunnelBindRequest  = types.TunnelBindRequest
	RealtimeConfig     = types.RealtimeConfig
)

// Re-export constants.
const (
	NamespaceControl = types.NamespaceControl
	NamespaceSession = types.NamespaceSession
)

// Re-export errors.
var (
	ErrNodeNotFound = types.ErrNodeNotFound
)

// ServerConfig holds configuration for a Tai transport server.
type ServerConfig struct {
	TransportPort   int
	HTTPPort        int
	TLS             *TLSConfig
	Auth            AuthProvider
	MaxFrameSize    int
	UnauthTimeout   time.Duration
	QUIC            *quic.Config
	ShutdownTimeout time.Duration
	Pool            *PoolSettings
}

// TLSConfig holds TLS certificate paths.
type TLSConfig struct {
	CertFile string
	KeyFile  string
}

// PoolSettings configures the Light and Heavy pools.
type PoolSettings struct {
	LightMin            uint32
	LightMax            uint32
	LightMaxStreams     uint32
	LightIdleTimeoutSec uint32
	HeavyMin            uint32
	HeavyMax            uint32
	HeavyMaxIdle        uint32
	HeavyIdleTimeoutSec uint32
	HeavyHealthCheckSec uint32
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
	Start(ctx context.Context) error
	Addr() string
	RegisterTool(tool *ToolDefinition) error
	UnregisterTool(name string) error
	Close() error
}

// ErrNotImplemented is returned by Server methods not yet available.
var ErrNotImplemented = errors.New("tai: not implemented")

// DefaultShutdownTimeout is how long Close waits for in-flight requests.
const DefaultShutdownTimeout = 5 * time.Second

const (
	defaultTransportPort = 6099
	defaultHTTPPort      = 5099
)

// wellKnownConfigurer is implemented by the default AuthProvider.
type wellKnownConfigurer interface {
	SetTLS(caCertPEM string, fingerprint string)
	SetTransportPort(port int)
}

// server is the Server implementation.
type server struct {
	cfg  ServerConfig
	auth AuthProvider
	reg  *node.Registry
	http *transport.HTTPServer
	tr   *transport.Server

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
}

// NewServer creates a transport server from cfg.
func NewServer(cfg *ServerConfig) (Server, error) {
	return newServer(cfg)
}

func newServer(cfg *ServerConfig) (*server, error) {
	if cfg == nil || cfg.Auth == nil {
		return nil, errors.New("tai: ServerConfig.Auth is required")
	}
	codec, err := transport.NewFrameCodec(cfg.MaxFrameSize)
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

	// Two-step assembly: registry, then transport server + global
	reg := node.NewRegistry(pool, workspace.DefaultPolicy())
	tr := transport.NewServer(c.Auth, reg, codec)
	transport.SetDefault(tr)

	s := &server{
		cfg:  c,
		auth: c.Auth,
		reg:  reg,
		http: transport.NewHTTPServer(c.Auth),
		tr:   tr,
		done: make(chan struct{}),
	}
	s.tr.SetUnauthTimeout(c.UnauthTimeout)
	s.tr.SetQUICConfig(c.QUIC)
	return s, nil
}

// bind opens every listener.
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

	pool, _ := s.cfg.Pool.poolConfig()
	s.tr.InitLightPool(pool)
	s.tr.InitHeavyPool(pool)

	tcpAddr := s.transportListenAddr
	if tcpAddr == "" {
		tcpAddr = ":" + strconv.Itoa(s.cfg.TransportPort)
	}
	if err := s.tr.StartTCP(tcpAddr, tlsCfg); err != nil {
		return err
	}
	s.tcpAddr = s.tr.TCPAddr()
	host, port, _ := net.SplitHostPort(s.tcpAddr)

	if tlsCfg != nil {
		if err := s.tr.StartH3(net.JoinHostPort(host, port), tlsCfg); err != nil {
			s.tr.Close(context.Background())
			return err
		}
		s.udpAddr = s.tr.H3Addr()
	}

	if wk, ok := s.auth.(wellKnownConfigurer); ok {
		if tlsCfg != nil {
			wk.SetTLS(string(certPEM), fingerprint)
		}
		if p, err := strconv.Atoi(port); err == nil {
			wk.SetTransportPort(p)
		}
	}

	httpAddr := s.httpListenAddr
	if httpAddr == "" {
		httpAddr = ":" + strconv.Itoa(s.cfg.HTTPPort)
	}
	if err := s.http.Start(httpAddr); err != nil {
		s.tr.Close(context.Background())
		return err
	}
	s.bound = true
	return nil
}

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

func (s *server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tcpAddr
}

func (s *server) RegisterTool(tool *ToolDefinition) error { return ErrNotImplemented }

func (s *server) UnregisterTool(name string) error { return ErrNotImplemented }

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
		s.tr.CloseHeavyPool()
		s.tr.CloseLightPool()

		timeout := s.cfg.ShutdownTimeout
		if timeout == 0 {
			timeout = DefaultShutdownTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := s.tr.Close(ctx)
		if herr := s.http.Close(ctx); herr != nil && err == nil {
			err = herr
		}
		s.closeErr = err
	})
	return s.closeErr
}
