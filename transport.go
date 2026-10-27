package tai

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
)

// transportServer handles Tai connections on port 6099 over HTTP/2.
// Connections are authenticated via connect_ticket in the first request.
type transportServer struct {
	auth     ticketValidator
	mux      *http.ServeMux
	server   *http.Server
	listener net.Listener

	mu    sync.Mutex
	nodes map[string]*nodeConn // nodeID -> active connection state
}

// ticketValidator is the subset of AuthProvider needed by the transport layer.
type ticketValidator interface {
	ValidateConnectTicket(ticket string) (nodeID string, connType string, err error)
}

// nodeConn tracks a connected Tai node's transport state.
type nodeConn struct {
	nodeID   string
	connType string
}

// newTransportServer creates a transport server for Tai connections.
func newTransportServer(auth ticketValidator) *transportServer {
	t := &transportServer{
		auth:  auth,
		mux:   http.NewServeMux(),
		nodes: make(map[string]*nodeConn),
	}

	// Control stream endpoint
	t.mux.HandleFunc("POST /v1/tai/control", t.handleControl)

	// Session stream endpoints
	t.mux.HandleFunc("POST /v1/tai/session/{sessionID}/control", t.handleSessionControl)
	t.mux.HandleFunc("POST /v1/tai/session/{sessionID}/data", t.handleSessionData)

	return t
}

// start begins listening on the given address with h2c (HTTP/2 cleartext).
// TLS support will be added in a later phase.
func (t *transportServer) start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("transport listen: %w", err)
	}
	t.listener = ln

	var protos http.Protocols
	protos.SetHTTP1(true)
	protos.SetUnencryptedHTTP2(true)
	t.server = &http.Server{
		Handler:   t.authMiddleware(t.mux),
		Protocols: &protos,
	}

	go func() {
		log.Printf("tai-sdk: Transport server listening on %s (h2c)", ln.Addr())
		if err := t.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("tai-sdk: Transport server error: %v", err)
		}
	}()
	return nil
}

// addr returns the listener address.
func (t *transportServer) addr() string {
	if t.listener == nil {
		return ""
	}
	return t.listener.Addr().String()
}

// close gracefully shuts down the transport server.
func (t *transportServer) close(ctx context.Context) error {
	if t.server == nil {
		return nil
	}
	return t.server.Shutdown(ctx)
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

		nodeID, connType, err := t.auth.ValidateConnectTicket(ticket)
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
// Endpoint stubs (implementation in PLAN 3)
// ---------------------------------------------------------------------------

// handleControl handles the L1 Control stream.
func (t *transportServer) handleControl(w http.ResponseWriter, r *http.Request) {
	nodeID := r.Context().Value(ctxNodeID).(string)
	log.Printf("tai-sdk: Control stream opened for node %s", nodeID)

	// Flush headers to establish the bidirectional stream
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// PLAN 3 will implement the full Control stream message loop.
	// For now, keep the connection open until the client disconnects.
	<-r.Context().Done()
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
