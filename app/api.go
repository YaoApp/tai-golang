package main

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/yaoapp/tai-golang/types"
)

//go:embed web/dist
var staticFS embed.FS

// NewAPI creates the HTTP mux with all API routes and static frontend.
func NewAPI(authProvider types.AuthProvider, store *Store, users *UserStore) http.Handler {
	mux := http.NewServeMux()

	authH := &AuthHandler{auth: authProvider, users: users}
	nodeH := &NodeHandler{store: store}
	sessionH := &SessionHandler{store: store, auth: authProvider}
	wsH := NewWorkspaceHandler(store)
	docsH := &DocsHandler{}
	eventH := NewEventHandler(store)
	ctxH := NewContextHandler()

	// Auth endpoints
	mux.HandleFunc("GET /api/auth/info", authH.Info)
	mux.HandleFunc("POST /api/auth/node-token", authH.IssueNodeToken)
	mux.HandleFunc("POST /api/auth/register", authH.Register)
	mux.HandleFunc("POST /api/auth/login", authH.Login)
	mux.HandleFunc("POST /api/auth/logout", authH.Logout)
	mux.HandleFunc("GET /api/auth/me", authH.Me)
	// v2 user credential: replaces the retired /api/auth/operation-token route.
	mux.HandleFunc("POST /api/auth/user-token", authH.IssueUserToken)

	// Node endpoints
	mux.HandleFunc("GET /api/nodes", nodeH.List)

	// Global event stream (WebSocket).
	//
	// Every global event (node.* / session.* / workspace.* / realtime.*) flows
	// through this single connection, matching yao 1.0's /v1/events design.
	// There is no page-specific SSE endpoint.
	mux.HandleFunc("GET /ws/events", eventH.WSEvents)

	// Session endpoints
	mux.HandleFunc("GET /api/sessions", sessionH.List)
	mux.HandleFunc("POST /api/sessions", sessionH.Create)
	mux.HandleFunc("DELETE /api/sessions/{id}", sessionH.Delete)
	mux.HandleFunc("POST /api/sessions/{id}/resume", sessionH.Resume)

	// Session WebSocket relay
	mux.HandleFunc("GET /ws/sessions/{id}/ctrl", sessionH.WSCtrl)
	mux.HandleFunc("GET /ws/sessions/{id}/data", sessionH.WSData)
	mux.HandleFunc("GET /ws/sessions/{id}/render", sessionH.WSRender)
	mux.HandleFunc("GET /ws/sessions/{id}/audio", sessionH.WSAudio)
	mux.HandleFunc("GET /ws/sessions/{id}/motion", sessionH.WSMotion)

	// Workspace endpoints
	mux.HandleFunc("GET /api/workspaces", wsH.ListWorkspaces)
	mux.HandleFunc("POST /api/workspaces", wsH.CreateWorkspace)
	mux.HandleFunc("DELETE /api/workspaces/{id}", wsH.DeleteWorkspace)
	mux.HandleFunc("PUT /api/workspaces/{id}/meta", wsH.UpdateMeta)

	// Branch endpoints
	mux.HandleFunc("GET /api/workspaces/{id}/branches", wsH.ListBranches)
	mux.HandleFunc("POST /api/workspaces/{id}/branches", wsH.CreateBranch)
	mux.HandleFunc("DELETE /api/workspaces/{id}/branches/{name}", wsH.DeleteBranch)
	mux.HandleFunc("POST /api/workspaces/{id}/branches/{name}/commit", wsH.CommitBranch)
	mux.HandleFunc("POST /api/workspaces/{id}/branches/{name}/rollback", wsH.RollbackBranch)
	mux.HandleFunc("POST /api/workspaces/{id}/merge", wsH.MergeWorkspace)

	// Context endpoints
	//
	// Wrap the SDK Context API (ReadContext / SeedContext / MergeContext).
	// Note: the Tai runtime does not read .context/, so these files never reach
	// the agent prompt.
	mux.HandleFunc("GET /api/workspaces/{id}/context", ctxH.GetContext)
	mux.HandleFunc("PUT /api/workspaces/{id}/context/{scope}/{scopeID}/{file}", ctxH.PutContextFile)
	mux.HandleFunc("POST /api/workspaces/{id}/context/{scope}/{scopeID}/seed", ctxH.SeedContext)
	mux.HandleFunc("GET /api/workspaces/{id}/context/merged", ctxH.GetMergedContext)

	// Skills endpoints (wrap SyncSkills + ReadManifest)
	mux.HandleFunc("GET /api/workspaces/{id}/skills", ctxH.ListSkills)
	mux.HandleFunc("POST /api/workspaces/{id}/skills", ctxH.UploadSkill)

	// History endpoints
	mux.HandleFunc("GET /api/workspaces/{id}/branches/{branch}/log", wsH.BranchLog)
	mux.HandleFunc("GET /api/workspaces/{id}/history", wsH.HistoryQuery)
	mux.HandleFunc("GET /api/workspaces/{id}/commits/{commitId}", wsH.GetCommit)

	// File endpoints
	mux.HandleFunc("GET /api/workspaces/{id}/branches/{branch}/files", wsH.FileStat)
	mux.HandleFunc("GET /api/workspaces/{id}/branches/{branch}/files/content", wsH.FileContent)
	mux.HandleFunc("PUT /api/workspaces/{id}/branches/{branch}/files/content", wsH.FileUpload)
	mux.HandleFunc("DELETE /api/workspaces/{id}/branches/{branch}/files", wsH.FileDelete)
	mux.HandleFunc("POST /api/workspaces/{id}/branches/{branch}/files/mkdir", wsH.FileMkdir)
	mux.HandleFunc("POST /api/workspaces/{id}/branches/{branch}/files/rename", wsH.FileRename)
	mux.HandleFunc("GET /api/workspaces/{id}/branches/{branch}/files/preview", wsH.FilePreview)

	// Chunked upload endpoints
	mux.HandleFunc("POST /api/workspaces/{id}/branches/{branch}/files/upload-init", wsH.ChunkedUploadInit)
	mux.HandleFunc("PUT /api/workspaces/{id}/branches/{branch}/files/upload-chunk", wsH.ChunkedUploadChunk)
	mux.HandleFunc("POST /api/workspaces/{id}/branches/{branch}/files/upload-complete", wsH.ChunkedUploadComplete)

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

	return corsMiddleware(users.protect(mux))
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
