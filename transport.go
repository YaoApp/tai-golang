package tai

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/quic-go/quic-go/http3"
)

// transportServer handles Tai connections over HTTP/3 (QUIC, primary) and
// HTTP/2 (TCP, fallback). Both protocols share the same http.Handler.
type transportServer struct {
	auth    AuthProvider
	tickets ticketValidator
	mgr     *nodeManager
	codec   *FrameCodec
	mux     *http.ServeMux
	handler http.Handler // authMiddleware(mux), shared by H2 and H3

	// H2 (TCP)
	h2Server   *http.Server
	h2Listener net.Listener

	// H3 (QUIC/UDP)
	h3Server *http3.Server
	h3Conn   net.PacketConn // pre-bound UDP socket, closed after goroutine exits
	h3Done   chan struct{}  // closed when the H3 serve goroutine returns
}

// ticketValidator is the subset of AuthProvider needed by the transport layer.
type ticketValidator interface {
	ValidateConnectTicket(ticket string) (nodeID string, connType string, err error)
}

// newTransportServer creates a transport server for Tai connections.
// auth provides signing keys for Control stream handlers; tickets validates connect_tickets.
func newTransportServer(auth AuthProvider, tickets ticketValidator, mgr *nodeManager, codec *FrameCodec) *transportServer {
	t := &transportServer{
		auth:    auth,
		tickets: tickets,
		mgr:     mgr,
		codec:   codec,
		mux:     http.NewServeMux(),
	}

	// Control stream endpoint
	t.mux.HandleFunc("POST /v1/tai/control", t.handleControl)

	// Session stream endpoints
	t.mux.HandleFunc("POST /v1/tai/session/{sessionID}/control", t.handleSessionControl)
	t.mux.HandleFunc("POST /v1/tai/session/{sessionID}/data", t.handleSessionData)

	t.handler = t.authMiddleware(t.mux)
	return t
}

// startH2 starts the HTTP/2 listener on the given TCP address.
// When tlsCfg is non-nil, the listener uses TLS (HTTP/2 over TLS);
// otherwise it uses cleartext h2c (trusted networks only).
func (t *transportServer) startH2(addr string, tlsCfg *tls.Config) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("h2 listen: %w", err)
	}
	t.h2Listener = ln

	if tlsCfg != nil {
		h2TLS := tlsCfg.Clone()
		h2TLS.NextProtos = nil // ServeTLS adds "h2" and "http/1.1"
		t.h2Server = &http.Server{
			Handler:   t.handler,
			TLSConfig: h2TLS,
		}
		go func() {
			log.Printf("tai-sdk: H2 transport listening on %s (TCP/TLS)", ln.Addr())
			if err := t.h2Server.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
				log.Printf("tai-sdk: H2 transport error: %v", err)
			}
		}()
	} else {
		var protos http.Protocols
		protos.SetHTTP1(true)
		protos.SetUnencryptedHTTP2(true)
		t.h2Server = &http.Server{
			Handler:   t.handler,
			Protocols: &protos,
		}
		go func() {
			log.Printf("tai-sdk: H2 transport listening on %s (TCP/h2c)", ln.Addr())
			if err := t.h2Server.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("tai-sdk: H2 transport error: %v", err)
			}
		}()
	}
	return nil
}

// startH3 starts the HTTP/3 (QUIC) listener on the given UDP address.
// The UDP socket is bound synchronously so port conflicts are caught immediately.
func (t *transportServer) startH3(addr string, tlsCfg *tls.Config) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("h3 listen udp: %w", err)
	}
	t.h3Conn = conn

	t.h3Server = &http3.Server{
		Handler:   t.handler,
		TLSConfig: tlsCfg,
	}

	t.h3Done = make(chan struct{})
	go func() {
		defer close(t.h3Done)
		log.Printf("tai-sdk: H3 transport listening on %s (QUIC)", addr)
		if err := t.h3Server.Serve(conn); err != nil && err != http.ErrServerClosed {
			log.Printf("tai-sdk: H3 transport error: %v", err)
		}
	}()
	return nil
}

// start starts H2 only in h2c mode (for tests without TLS).
func (t *transportServer) start(addr string) error {
	return t.startH2(addr, nil)
}

// h2Addr returns the H2 (TCP) listener address.
func (t *transportServer) h2Addr() string {
	if t.h2Listener == nil {
		return ""
	}
	return t.h2Listener.Addr().String()
}

// close gracefully shuts down both H2 and H3 servers.
// It waits for the H3 serve goroutine to exit and ensures the UDP socket is released.
func (t *transportServer) close(ctx context.Context) error {
	var firstErr error
	if t.h3Server != nil {
		if err := t.h3Server.Close(); err != nil {
			firstErr = err
		}
		if t.h3Done != nil {
			<-t.h3Done
		}
		if t.h3Conn != nil {
			t.h3Conn.Close()
		}
	}
	if t.h2Server != nil {
		if err := t.h2Server.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ---------------------------------------------------------------------------
// Middleware: connect_ticket authentication
// ---------------------------------------------------------------------------

type contextKey string

const (
	ctxNodeID   contextKey = "nodeID"
	ctxConnType contextKey = "connType"
)

// authMiddleware validates the connect_ticket on every request.
func (t *transportServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ticket := extractBearer(r)
		if ticket == "" {
			http.Error(w, "missing ticket", http.StatusUnauthorized)
			return
		}

		nodeID, connType, err := t.tickets.ValidateConnectTicket(ticket)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ctxNodeID, nodeID)
		ctx = context.WithValue(ctx, ctxConnType, connType)
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

// handleSessionControl handles a per-session Control stream.
func (t *transportServer) handleSessionControl(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	nodeID := r.Context().Value(ctxNodeID).(string)
	log.Printf("tai-sdk: Session Control stream opened: node=%s session=%s", nodeID, sessionID)

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	<-r.Context().Done()
}

// handleSessionData handles a per-session Data stream (Tai -> Server).
func (t *transportServer) handleSessionData(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	nodeID := r.Context().Value(ctxNodeID).(string)
	log.Printf("tai-sdk: Session Data stream opened: node=%s session=%s", nodeID, sessionID)

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	<-r.Context().Done()
}
