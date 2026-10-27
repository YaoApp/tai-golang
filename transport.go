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
	mux     *http.ServeMux
	handler http.Handler // authMiddleware(mux), shared by H2 and H3

	// H2 (TCP)
	h2Server   *http.Server
	h2Listener net.Listener

	// H3 (QUIC/UDP)
	h3Server *http3.Server
}

// ticketValidator is the subset of AuthProvider needed by the transport layer.
type ticketValidator interface {
	ValidateConnectTicket(ticket string) (nodeID string, connType string, err error)
}

// newTransportServer creates a transport server for Tai connections.
// auth provides signing keys for Control stream handlers; tickets validates connect_tickets.
func newTransportServer(auth AuthProvider, tickets ticketValidator, mgr *nodeManager) *transportServer {
	t := &transportServer{
		auth:    auth,
		tickets: tickets,
		mgr:     mgr,
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

// startH2 starts the HTTP/2 (h2c) listener on the given TCP address.
func (t *transportServer) startH2(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("h2 listen: %w", err)
	}
	t.h2Listener = ln

	var protos http.Protocols
	protos.SetHTTP1(true)
	protos.SetUnencryptedHTTP2(true)
	t.h2Server = &http.Server{
		Handler:   t.handler,
		Protocols: &protos,
	}

	go func() {
		log.Printf("tai-sdk: H2 transport listening on %s (TCP)", ln.Addr())
		if err := t.h2Server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("tai-sdk: H2 transport error: %v", err)
		}
	}()
	return nil
}

// startH3 starts the HTTP/3 (QUIC) listener on the given UDP address.
func (t *transportServer) startH3(addr string, tlsCfg *tls.Config) error {
	t.h3Server = &http3.Server{
		Addr:      addr,
		Handler:   t.handler,
		TLSConfig: tlsCfg,
	}

	go func() {
		log.Printf("tai-sdk: H3 transport listening on %s (QUIC)", addr)
		if err := t.h3Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("tai-sdk: H3 transport error: %v", err)
		}
	}()
	return nil
}

// start starts H2 only (for tests without TLS).
func (t *transportServer) start(addr string) error {
	return t.startH2(addr)
}

// h2Addr returns the H2 (TCP) listener address.
func (t *transportServer) h2Addr() string {
	if t.h2Listener == nil {
		return ""
	}
	return t.h2Listener.Addr().String()
}

// close gracefully shuts down both H2 and H3 servers.
func (t *transportServer) close(ctx context.Context) error {
	var firstErr error
	if t.h3Server != nil {
		if err := t.h3Server.Close(); err != nil {
			firstErr = err
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

	cs := newControlStream(nodeID, r.Body, w, t.mgr, t.auth)
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
