package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"sync"
)

// Server handles Tai connections over HTTP/3 (QUIC) and TCP (HTTP/2 fallback
// plus the Tai frame protocol).
type Server struct {
	auth    types.AuthProvider
	mgr     *NodeManager
	codec   *FrameCodec
	mux     *http.ServeMux
	handler http.Handler

	unauthTimeout time.Duration

	// TCP
	tcp        *tcpMux
	taiHandler atomic.Pointer[taiConnHandler]

	// Light pool
	light *lightPool

	// H3 (QUIC/UDP)
	h3Server     *http3.Server
	wtServer     *webtransport.Server
	quicConfig   *quic.Config
	lightHandler atomic.Pointer[lightSessionHandler]
	h3Conn       net.PacketConn
	h3Listener   *quic.EarlyListener
	h3Cancel     context.CancelFunc
	h3Conns      sync.WaitGroup
	h3Done       chan struct{}
}

// NewServer creates a transport server.
func NewServer(auth types.AuthProvider, mgr *NodeManager, codec *FrameCodec) *Server {
	t := &Server{
		auth:  auth,
		mgr:   mgr,
		codec: codec,
		mux:   http.NewServeMux(),
	}

	t.mux.HandleFunc("POST /v1/tai/connect", t.handleConnect)
	t.mux.HandleFunc("/v1/tai/light", t.handleLight)
	t.mux.Handle("POST /v1/tai/control", t.requireAuthorized(ConnTypeControl, http.HandlerFunc(t.handleControl)))

	t.handler = t.mux
	return t
}

// SetUnauthTimeout sets the unauthorized connection timeout.
func (t *Server) SetUnauthTimeout(d time.Duration) { t.unauthTimeout = d }

// SetQUICConfig sets the QUIC configuration.
func (t *Server) SetQUICConfig(cfg *quic.Config) { t.quicConfig = cfg }

// NodeManager returns the node manager.
func (t *Server) NodeManager() *NodeManager { return t.mgr }

// Codec returns the frame codec.
func (t *Server) Codec() *FrameCodec { return t.codec }

// Handler returns the HTTP handler for testing.
func (t *Server) Handler() http.Handler { return t.handler }

// connContext creates the authorization state for a connection.
func (t *Server) connContext(ctx context.Context, closeFn func()) context.Context {
	return WithConnState(ctx, NewConnState(closeFn, t.unauthTimeoutOrDefault()))
}

// ---------------------------------------------------------------------------
// H3 (QUIC)
// ---------------------------------------------------------------------------

func (t *Server) startH3(addr string, tlsCfg *tls.Config) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("h3 listen udp: %w", err)
	}
	t.h3Conn = conn

	if tlsCfg == nil {
		conn.Close()
		t.h3Conn = nil
		return errors.New("h3 requires a TLS configuration")
	}
	h3TLS := tlsCfg.Clone()
	h3TLS.NextProtos = []string{http3.NextProtoH3}

	t.h3Server = &http3.Server{
		Handler:     t.handler,
		TLSConfig:   h3TLS,
		QUICConfig:  t.quicConfig,
		ConnContext: t.quicConnContext,
	}
	t.wtServer = &webtransport.Server{
		H3:          t.h3Server,
		CheckOrigin: func(*http.Request) bool { return true },
	}

	quicConf := &quic.Config{}
	if t.quicConfig != nil {
		quicConf = t.quicConfig.Clone()
	}
	quicConf.EnableDatagrams = true
	quicConf.EnableStreamResetPartialDelivery = true
	ln, err := quic.ListenEarly(conn, h3TLS, quicConf)
	if err != nil {
		conn.Close()
		t.h3Conn = nil
		return fmt.Errorf("h3 listen quic: %w", err)
	}
	t.h3Listener = ln

	ctx, cancel := context.WithCancel(context.Background())
	t.h3Cancel = cancel
	t.h3Done = make(chan struct{})
	go func() {
		defer close(t.h3Done)
		log.Printf("tai-sdk: H3 transport listening on %s (QUIC)", addr)
		for {
			qconn, err := ln.Accept(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("tai-sdk: H3 accept error: %v", err)
				}
				return
			}
			t.h3Conns.Go(func() {
				err := t.wtServer.ServeQUICConn(qconn)
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Printf("tai-sdk: H3 connection error: %v", err)
				}
			})
		}
	}()
	return nil
}

// StartH3 starts the H3 (QUIC) listener. Exported for root server usage.
func (t *Server) StartH3(addr string, tlsCfg *tls.Config) error {
	return t.startH3(addr, tlsCfg)
}

// StartTCP starts the TCP listener. Exported for root server usage.
func (t *Server) StartTCP(addr string, tlsCfg *tls.Config) error {
	return t.startTCP(addr, tlsCfg)
}

// H3Addr returns the H3 (UDP) listener address.
func (t *Server) H3Addr() string {
	if t.h3Conn == nil {
		return ""
	}
	return t.h3Conn.LocalAddr().String()
}

// TCPAddr returns the TCP listener address.
func (t *Server) TCPAddr() string {
	if t.tcp == nil {
		return ""
	}
	return t.tcp.ln.Addr().String()
}

// Close gracefully shuts down TCP and H3 servers.
func (t *Server) Close(ctx context.Context) error {
	var firstErr error
	if t.wtServer != nil && t.h3Listener != nil {
		t.h3Cancel()
		t.h3Listener.Close()
		<-t.h3Done
		if err := t.wtServer.Close(); err != nil {
			firstErr = err
		}
		t.h3Conns.Wait()
		t.h3Conn.Close()
	}
	if err := t.closeTCP(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// ---------------------------------------------------------------------------
// Connection authentication
// ---------------------------------------------------------------------------

type contextKey string

const ctxNodeID contextKey = "nodeID"

const h3CloseCode quic.ApplicationErrorCode = 0x101

func (t *Server) quicConnContext(ctx context.Context, c *quic.Conn) context.Context {
	return t.connContext(ctx, func() { c.CloseWithError(h3CloseCode, "unauthorized") })
}

const maxConnectBody = 4 * 1024

type connectRequest struct {
	NodeID      string `json:"node_id"`
	ConnectType string `json:"connect_type"`
}

func rejectConn(w http.ResponseWriter, st *ConnState, msg string) {
	HTTPError(w, http.StatusUnauthorized, msg)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if st != nil {
		st.CloseSoon()
	}
}

func (t *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	st := ConnStateFrom(r.Context())

	token := ExtractBearer(r)
	if token == "" {
		rejectConn(w, st, "missing authorization")
		return
	}
	info, err := t.auth.ValidateNodeToken(token)
	if err != nil {
		rejectConn(w, st, "invalid node_token")
		return
	}

	var req connectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectBody)).Decode(&req); err != nil {
		rejectConn(w, st, "invalid request body")
		return
	}
	if req.NodeID != info.NodeID {
		rejectConn(w, st, "node_id does not match node_token")
		return
	}
	if req.ConnectType != ConnTypeControl {
		rejectConn(w, st, "connect_type must be control")
		return
	}

	if st == nil {
		HTTPError(w, http.StatusInternalServerError, "no connection state")
		return
	}
	if !st.AuthorizeAs(info.NodeID, ConnTypeControl) {
		rejectConn(w, st, "connection already authorized differently")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (t *Server) requireAuthorized(connType string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := ConnStateFrom(r.Context())
		if st == nil {
			HTTPError(w, http.StatusUnauthorized, "connection not authorized")
			return
		}
		nodeID, ok := st.Check(connType)
		if !ok {
			rejectConn(w, st, "connection not authorized")
			return
		}
		ctx := context.WithValue(r.Context(), ctxNodeID, nodeID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---------------------------------------------------------------------------
// Stream handlers
// ---------------------------------------------------------------------------

func (t *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	nodeID := r.Context().Value(ctxNodeID).(string)
	log.Printf("tai-sdk: Control stream opened for node %s", nodeID)

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	cs := newControlStream(nodeID, r.Body, w, t.mgr, t.auth, t.codec)
	cs.run(r.Context())
	log.Printf("tai-sdk: Control stream closed for node %s", nodeID)
}

type lightSessionHandler func(nodeID string, sess *webtransport.Session, st *ConnState)

// SetLightHandler registers the handler for Light WebTransport sessions.
func (t *Server) SetLightHandler(h lightSessionHandler) {
	t.lightHandler.Store(&h)
}

// SetTaiHandler registers the handler for Tai frame connections.
func (t *Server) SetTaiHandler(h taiConnHandler) {
	t.taiHandler.Store(&h)
}

func (t *Server) handleLight(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 3 || t.wtServer == nil {
		HTTPError(w, http.StatusNotFound, "light sessions require HTTP/3 WebTransport")
		return
	}
	st := ConnStateFrom(r.Context())

	token := ExtractBearer(r)
	if token == "" {
		rejectConn(w, st, "missing authorization")
		return
	}
	info, err := t.auth.ValidateNodeToken(token)
	if err != nil {
		rejectConn(w, st, "invalid node_token")
		return
	}
	if r.Header.Get("X-Tai-Node-Id") != info.NodeID {
		rejectConn(w, st, "X-Tai-Node-Id does not match node_token")
		return
	}
	if st == nil || !st.AuthorizeAs(info.NodeID, ConnTypeLight) {
		rejectConn(w, st, "connection already authorized differently")
		return
	}

	sess, err := t.wtServer.Upgrade(w, r)
	if err != nil {
		log.Printf("tai-sdk: Light upgrade for node %s failed: %v", info.NodeID, err)
		st.Close()
		return
	}
	h := t.lightHandler.Load()
	if h == nil {
		log.Printf("tai-sdk: Light session from node %s closed: no handler registered", info.NodeID)
		sess.CloseWithError(0, "no light handler")
		return
	}
	(*h)(info.NodeID, sess, st)
}

// LightConnCount returns the number of Light connections for a node.
func (t *Server) LightConnCount(nodeID string) int {
	if t.light == nil {
		return 0
	}
	return t.light.connCount(nodeID)
}

// OpenLightStream opens a bidirectional stream on the Light pool.
func (t *Server) OpenLightStream(ctx context.Context, nodeID string, firstFrame *pb.Payload) (*LightStream, error) {
	if t.light == nil {
		return nil, errors.New("light pool not initialized")
	}
	return t.light.OpenStream(ctx, nodeID, firstFrame)
}

// InitLightPool creates and initializes the Light pool.
func (t *Server) InitLightPool(cfg *pb.PoolConfig) {
	pool := newLightPool(cfg, t.codec, t.mgr, t.auth)
	t.light = pool
	pool.initLightHandlers(t)
}

// CloseLightPool closes the Light pool. Must be called before Close.
func (t *Server) CloseLightPool() {
	if t.light != nil {
		t.light.close()
	}
}

// light is the Light connection pool.
var _ = (*Server)(nil) // compile-time reference
