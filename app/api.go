package main

import (
	"embed"
	"io/fs"
	"net/http"

	tai "github.com/yaoapp/tai-golang"
	"github.com/yaoapp/tai-golang/types"
)

//go:embed web/dist
var staticFS embed.FS

// NewAPI creates the HTTP mux with all API routes and static frontend.
func NewAPI(sdkServer tai.Server, authProvider types.AuthProvider, store *Store) http.Handler {
	mux := http.NewServeMux()

	authH := &AuthHandler{auth: authProvider}
	nodeH := &NodeHandler{store: store}
	sessionH := &SessionHandler{server: sdkServer, store: store, auth: authProvider}
	docsH := &DocsHandler{}

	// Auth endpoints
	mux.HandleFunc("GET /api/auth/info", authH.Info)
	mux.HandleFunc("POST /api/auth/node-token", authH.IssueNodeToken)
	mux.HandleFunc("POST /api/auth/operation-token", authH.IssueOperationToken)

	// Node endpoints
	mux.HandleFunc("GET /api/nodes", nodeH.List)
	mux.HandleFunc("GET /api/nodes/events", nodeH.Events)

	// Session endpoints
	mux.HandleFunc("GET /api/sessions", sessionH.List)
	mux.HandleFunc("POST /api/sessions", sessionH.Create)
	mux.HandleFunc("DELETE /api/sessions/{id}", sessionH.Delete)

	// Session WebSocket relay
	mux.HandleFunc("GET /ws/sessions/{id}/ctrl", sessionH.WSCtrl)
	mux.HandleFunc("GET /ws/sessions/{id}/data", sessionH.WSData)

	// Docs endpoints
	mux.HandleFunc("GET /api/docs", docsH.List)
	mux.HandleFunc("GET /api/docs/{name}", docsH.Get)

	// Serve frontend static files (fallback to index.html for SPA routing).
	staticSub, err := fs.Sub(staticFS, "web/dist")
	if err != nil {
		panic("embed web/dist: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle("/", spaHandler(fileServer, staticSub))

	return corsMiddleware(mux)
}

// spaHandler serves static files; falls back to index.html for SPA routes.
func spaHandler(fileServer http.Handler, fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "index.html"
		} else if path[0] == '/' {
			path = path[1:]
		}
		if _, err := fs.Stat(fsys, path); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

// corsMiddleware adds permissive CORS headers for development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
