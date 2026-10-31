package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/yaoapp/tai-golang/types"
)

// HTTPServer handles the HTTP API on port 5099.
type HTTPServer struct {
	auth     types.AuthProvider
	mux      *http.ServeMux
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
}

// NewHTTPServer creates an HTTPServer wired to the given AuthProvider.
func NewHTTPServer(auth types.AuthProvider) *HTTPServer {
	s := &HTTPServer{
		auth: auth,
		mux:  http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /.well-known/yao-transport", s.handleWellKnown)
	s.mux.HandleFunc("POST /v1/tai/token/refresh", s.handleTokenRefresh)
	s.mux.HandleFunc("POST /v1/tai/heartbeat", s.handleHeartbeat)
	return s
}

// ListenAndServe starts the HTTP server on the given address (blocking).
func (s *HTTPServer) ListenAndServe(addr string) error {
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

// Start starts the HTTP server in the background.
func (s *HTTPServer) Start(addr string) error {
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

// Addr returns the listener address.
func (s *HTTPServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Close gracefully shuts down the HTTP server.
func (s *HTTPServer) Close(ctx context.Context) error {
	s.mu.Lock()
	srv := s.server
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	info := s.auth.WellKnownInfo()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// RefreshRequest is the JSON body of POST /v1/tai/token/refresh.
type RefreshRequest struct {
	NodeToken string `json:"node_token"`
}

// RefreshResponse is the JSON body of the token refresh response.
type RefreshResponse struct {
	NodeToken string `json:"node_token"`
	ExpiresIn int    `json:"expires_in"`
}

func (s *HTTPServer) handleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	newToken, err := s.auth.RefreshNodeToken(req.NodeToken)
	if err != nil {
		HTTPError(w, http.StatusUnauthorized, "refresh failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(RefreshResponse{
		NodeToken: newToken,
		ExpiresIn: 7 * 24 * 3600,
	})
}

// HeartbeatRequest is the JSON body of POST /v1/tai/heartbeat.
type HeartbeatRequest struct {
	TaiID        string   `json:"tai_id"`
	NodeToken    string   `json:"node_token"`
	Sessions     []string `json:"sessions"`
	Reconnecting bool     `json:"reconnecting"`
	Timestamp    int64    `json:"timestamp"`
}

func (s *HTTPServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if _, err := s.auth.ValidateNodeToken(req.NodeToken); err != nil {
		HTTPError(w, http.StatusUnauthorized, "invalid node_token")
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// ExtractBearer extracts the token from a "Bearer <token>" Authorization header.
func ExtractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

// ErrorResponse is the JSON error body.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HTTPError writes a JSON error response.
func HTTPError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: msg})
}
