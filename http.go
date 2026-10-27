package tai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
)

// httpServer handles the HTTP API on port 5099.
// Endpoints: well-known, token refresh, heartbeat, tai call.
// Connection authentication (`POST /v1/tai/connect`) lives on the transport
// port (6099), not here.
type httpServer struct {
	auth     AuthProvider
	mux      *http.ServeMux
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
}

// newHTTPServer creates an httpServer wired to the given AuthProvider.
func newHTTPServer(auth AuthProvider) *httpServer {
	s := &httpServer{
		auth: auth,
		mux:  http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /.well-known/yao-transport", s.handleWellKnown)
	s.mux.HandleFunc("POST /v1/tai/token/refresh", s.handleTokenRefresh)
	s.mux.HandleFunc("POST /v1/tai/heartbeat", s.handleHeartbeat)
	return s
}

// listenAndServe starts the HTTP server on the given address (blocking).
func (s *httpServer) listenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("http listen: %w", err)
	}

	s.mu.Lock()
	s.listener = ln
	s.server = &http.Server{Handler: s.mux}
	s.mu.Unlock()

	log.Printf("tai-sdk: HTTP server listening on %s", ln.Addr())
	return s.server.Serve(ln)
}

// start starts the HTTP server in the background.
func (s *httpServer) start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("http listen: %w", err)
	}

	s.mu.Lock()
	s.listener = ln
	s.server = &http.Server{Handler: s.mux}
	s.mu.Unlock()

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("tai-sdk: HTTP server error: %v", err)
		}
	}()
	return nil
}

// addr returns the listener address.
func (s *httpServer) addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// close gracefully shuts down the HTTP server.
func (s *httpServer) close(ctx context.Context) error {
	s.mu.Lock()
	srv := s.server
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// GET /.well-known/yao-transport
// ---------------------------------------------------------------------------

func (s *httpServer) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	info := s.auth.WellKnownInfo()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// ---------------------------------------------------------------------------
// POST /v1/tai/token/refresh
// ---------------------------------------------------------------------------

type refreshRequest struct {
	NodeToken string `json:"node_token"`
}

type refreshResponse struct {
	NodeToken string `json:"node_token"`
	ExpiresIn int    `json:"expires_in"` // seconds until token expiry
}

func (s *httpServer) handleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	newToken, err := s.auth.RefreshNodeToken(req.NodeToken)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "refresh failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(refreshResponse{
		NodeToken: newToken,
		ExpiresIn: 7 * 24 * 3600, // matches IssueNodeToken TTL
	})
}

// ---------------------------------------------------------------------------
// POST /v1/tai/heartbeat
// ---------------------------------------------------------------------------

type heartbeatRequest struct {
	TaiID        string   `json:"tai_id"`
	NodeToken    string   `json:"node_token"`
	Sessions     []string `json:"sessions"`
	Reconnecting bool     `json:"reconnecting"`
	Timestamp    int64    `json:"timestamp"`
}

func (s *httpServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if _, err := s.auth.ValidateNodeToken(req.NodeToken); err != nil {
		httpError(w, http.StatusUnauthorized, "invalid node_token")
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

type errorResponse struct {
	Error string `json:"error"`
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
