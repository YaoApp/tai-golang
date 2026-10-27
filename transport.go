package tai

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

// transportServer handles Tai connections over HTTP/3 (QUIC, primary) and
// TCP (HTTP/2 fallback plus the Tai frame protocol). HTTP/2 and HTTP/3 share
// the same http.Handler.
type transportServer struct {
	auth    AuthProvider
	mgr     *nodeManager
	codec   *FrameCodec
	mux     *http.ServeMux
	handler http.Handler // mux, shared by H2 and H3

	// unauthTimeout bounds how long a connection may stay unauthorized after
	// its handshake; 0 uses DefaultUnauthTimeout.
	unauthTimeout time.Duration

	// TCP (HTTP/2, h2c and Tai frames)
	tcp        *tcpMux
	taiHandler atomic.Pointer[taiConnHandler]

	// H3 (QUIC/UDP) with WebTransport
	h3Server     *http3.Server
	wtServer     *webtransport.Server
	quicConfig   *quic.Config // optional; datagrams and partial delivery are forced on
	lightHandler atomic.Pointer[lightSessionHandler]
	h3Conn       net.PacketConn // pre-bound UDP socket, closed after goroutines exit
	h3Listener   *quic.EarlyListener
	h3Cancel     context.CancelFunc
	h3Conns      sync.WaitGroup // per-connection serve goroutines
	h3Done       chan struct{}  // closed when the H3 accept goroutine returns
}

// newTransportServer creates a transport server for Tai connections.
// auth validates node_tokens and provides signing keys for Control stream handlers.
func newTransportServer(auth AuthProvider, mgr *nodeManager, codec *FrameCodec) *transportServer {
	t := &transportServer{
		auth:  auth,
		mgr:   mgr,
		codec: codec,
		mux:   http.NewServeMux(),
	}

	// Connection authentication: the first request of a Control connection.
	t.mux.HandleFunc("POST /v1/tai/connect", t.handleConnect)

	// Light session: WebTransport CONNECT over HTTP/3, authenticated by its own headers.
	t.mux.HandleFunc("/v1/tai/light", t.handleLight)

	// Control stream endpoint (requires an authorized control connection).
	t.mux.Handle("POST /v1/tai/control", t.requireAuthorized(connTypeControl, http.HandlerFunc(t.handleControl)))

	t.handler = t.mux
	return t
}

// connContext returns the ConnContext hook for the HTTP servers: it creates
// the connection's authorization state and starts the unauthorized timer.
func (t *transportServer) connContext(ctx context.Context, closeFn func()) context.Context {
	return withConnState(ctx, newConnState(closeFn, t.unauthTimeoutOrDefault()))
}

// startH3 starts the HTTP/3 (QUIC) listener on the given UDP address with
// WebTransport enabled. The UDP socket is bound synchronously so port
// conflicts are caught immediately.
func (t *transportServer) startH3(addr string, tlsCfg *tls.Config) error {
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
	// Tai is not a browser: any Origin is acceptable.
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

// h3Addr returns the H3 (UDP) listener address, or "" when not started.
func (t *transportServer) h3Addr() string {
	if t.h3Conn == nil {
		return ""
	}
	return t.h3Conn.LocalAddr().String()
}

// close gracefully shuts down the TCP and H3 servers.
// It waits for the H3 serve goroutine to exit and ensures the UDP socket is released.
func (t *transportServer) close(ctx context.Context) error {
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

// h3CloseCode is the application error code sent when closing an
// unauthorized QUIC connection (H3_GENERAL_PROTOCOL_ERROR).
const h3CloseCode quic.ApplicationErrorCode = 0x101

// tcpConnContext creates the authorization state for an accepted TCP
// connection; the unauthorized timer closes the connection.
func (t *transportServer) tcpConnContext(ctx context.Context, c net.Conn) context.Context {
	return t.connContext(ctx, func() { c.Close() })
}

// quicConnContext creates the authorization state for an accepted QUIC
// connection; the unauthorized timer closes the connection.
func (t *transportServer) quicConnContext(ctx context.Context, c *quic.Conn) context.Context {
	return t.connContext(ctx, func() { c.CloseWithError(h3CloseCode, "unauthorized") })
}

// maxConnectBody bounds the JSON body of POST /v1/tai/connect (node_id and
// connect_type only).
const maxConnectBody = 4 * 1024

// connectRequest is the JSON body of POST /v1/tai/connect.
type connectRequest struct {
	NodeID      string `json:"node_id"`
	ConnectType string `json:"connect_type"`
}

// rejectConn answers 401 and closes the connection shortly afterwards.
func rejectConn(w http.ResponseWriter, st *connState, msg string) {
	httpError(w, http.StatusUnauthorized, msg)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if st != nil {
		st.closeSoon()
	}
}

// handleConnect authenticates a Control connection: it validates the
// node_token, requires node_id to match the token's node and connect_type to
// be "control", and marks the connection authorized. Failure closes it.
func (t *transportServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	st := connStateFrom(r.Context())

	token := extractBearer(r)
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
	if req.ConnectType != connTypeControl {
		rejectConn(w, st, "connect_type must be control")
		return
	}

	if st == nil {
		httpError(w, http.StatusInternalServerError, "no connection state")
		return
	}
	if !st.authorizeAs(info.NodeID, connTypeControl) {
		rejectConn(w, st, "connection already authorized differently")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// requireAuthorized lets a request through only when its connection is
// authorized for connType; the node ID is stored in the request context.
// Other requests get 401 and the connection is closed.
func (t *transportServer) requireAuthorized(connType string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := connStateFrom(r.Context())
		if st == nil {
			httpError(w, http.StatusUnauthorized, "connection not authorized")
			return
		}
		nodeID, ok := st.check(connType)
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

// handleControl handles the L1 Control stream with full message loop.
func (t *transportServer) handleControl(w http.ResponseWriter, r *http.Request) {
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

// lightSessionHandler receives an authorized Light WebTransport session. The
// connection's state is already marked authorized as connTypeLight for nodeID.
type lightSessionHandler func(nodeID string, sess *webtransport.Session, st *connState)

// setLightHandler registers the handler for Light WebTransport sessions; it
// may be called while the server is running and applies to later sessions.
func (t *transportServer) setLightHandler(h lightSessionHandler) {
	t.lightHandler.Store(&h)
}

// handleLight accepts the WebTransport CONNECT at /v1/tai/light. The request
// authenticates itself: Authorization carries the node_token and
// X-Tai-Node-Id must name the token's node. Failure answers 401 and closes
// the connection.
func (t *transportServer) handleLight(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 3 || t.wtServer == nil {
		httpError(w, http.StatusNotFound, "light sessions require HTTP/3 WebTransport")
		return
	}
	st := connStateFrom(r.Context())

	token := extractBearer(r)
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
	if st == nil || !st.authorizeAs(info.NodeID, connTypeLight) {
		rejectConn(w, st, "connection already authorized differently")
		return
	}

	sess, err := t.wtServer.Upgrade(w, r)
	if err != nil {
		log.Printf("tai-sdk: Light upgrade for node %s failed: %v", info.NodeID, err)
		st.close()
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
