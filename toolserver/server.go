// Package toolserver serves the Remote Tool H2 endpoints.
package toolserver

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/remotetool"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// Server exposes health/call/plugins over HTTP/2.
type Server struct {
	Registry *remotetool.Registry
	// NodeAuth validates the connection-level node_token.
	NodeAuth func(*http.Request) bool
	// UserAuth validates the per-call user token.
	UserAuth func(token string) (*types.UserIdentity, error)
	// Artifacts returns a plugin artifact by digest.
	Artifacts func(digest string) ([]byte, error)
	codec     *transport.FrameCodec
	codecOnce sync.Once
}

// Handler returns the HTTP handler for all Tool endpoints.
func (s *Server) Handler() http.Handler {
	s.codecOnce.Do(func() {
		codec, err := transport.NewFrameCodec(16 * 1024 * 1024)
		if err != nil {
			panic(fmt.Sprintf("toolserver: frame codec: %v", err))
		}
		s.codec = codec
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tai/tool/health", s.health)
	mux.HandleFunc("/v1/tai/tool/call", s.call)
	mux.HandleFunc("/v1/tai/tool/plugins/", s.artifact)
	return mux
}

func (s *Server) auth(w http.ResponseWriter, r *http.Request) bool {
	if s.NodeAuth != nil && !s.NodeAuth(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) call(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	frame, err := s.codec.ReadFrame(r.Body)
	if err != nil || frame.Namespace != "remote_tool" || frame.Kind != "call" {
		writeErr(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var call pb.RemoteToolCall
	if err := proto.Unmarshal(frame.Data, &call); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if call.RequestId == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err == nil {
			call.RequestId = hex.EncodeToString(b[:])
		}
	}
	if s.Registry == nil {
		writeErr(w, http.StatusServiceUnavailable, "registry_unavailable")
		return
	}
	if s.UserAuth == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	token := ""
	if frame.Identity != nil {
		token = frame.Identity.UserToken
	}
	identity, err := s.UserAuth(token)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	startedAt := time.Now()
	log.Printf("tool call: tool=%s request_id=%s", call.Tool, call.RequestId)
	started := false
	startStream := func() {
		if started {
			return
		}
		started = true
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}
	emit := func(ev *pb.RemoteToolEvent) {
		if ev == nil {
			return
		}
		data, err := proto.Marshal(ev)
		if err != nil {
			return
		}
		startStream()
		_ = s.codec.WriteFrame(w, &pb.Payload{Namespace: "remote_tool", Kind: "progress", Data: data})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	res, err := s.Registry.CallWithEmitter(r.Context(), identity, &call, emit)
	if err != nil {
		if !started {
			switch {
			case errors.Is(err, remotetool.ErrUnauthorized):
				writeErr(w, http.StatusForbidden, "forbidden")
			case errors.Is(err, remotetool.ErrNotFound):
				writeErr(w, http.StatusNotFound, "not_found")
			case errors.Is(err, remotetool.ErrInvalidInput):
				writeErr(w, http.StatusBadRequest, "invalid_input")
			default:
				writeErr(w, http.StatusInternalServerError, "internal_error")
			}
			return
		}
		res = &pb.RemoteToolResult{RequestId: call.RequestId, Error: &pb.RemoteToolError{Code: "handler_error", Message: err.Error()}}
	}
	data, err := proto.Marshal(res)
	if err != nil {
		if !started {
			writeErr(w, http.StatusInternalServerError, "internal_error")
		}
		return
	}
	startStream()
	_ = s.codec.WriteFrame(w, &pb.Payload{Namespace: "remote_tool", Kind: "result", Data: data})
	log.Printf("tool result: tool=%s request_id=%s duration=%s", call.Tool, call.RequestId, time.Since(startedAt))
}

func (s *Server) artifact(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	if s.Artifacts == nil {
		http.NotFound(w, r)
		return
	}
	hex := strings.TrimPrefix(r.URL.Path, "/v1/tai/tool/plugins/")
	if len(hex) != 64 {
		writeErr(w, http.StatusBadRequest, "invalid_digest")
		return
	}
	for _, c := range hex {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			writeErr(w, http.StatusBadRequest, "invalid_digest")
			return
		}
	}
	data, err := s.Artifacts(hex)
	if err != nil {
		log.Printf("tool artifact miss: digest=%s err=%v", hex, err)
		http.NotFound(w, r)
		return
	}
	log.Printf("tool artifact served: digest=%s size=%d", hex, len(data))
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// ServeTLS runs the Tool H2 endpoint with ALPN h2 until ctx is done.
func (s *Server) ServeTLS(ctx context.Context, addr, certFile, keyFile string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.ServeTLSListener(ctx, ln, certFile, keyFile)
}

// ServeTLSListener serves on an existing listener.
func (s *Server) ServeTLSListener(ctx context.Context, ln net.Listener, certFile, keyFile string) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}},
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		case <-done:
		}
	}()
	err := srv.ServeTLS(ln, certFile, keyFile)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func writeErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": code}})
}
