package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/yaoapp/tai-golang/node"
	"github.com/yaoapp/tai-golang/types"
)

// WorkspaceHandler serves workspace, branch, file, and history endpoints.
type WorkspaceHandler struct {
	store   *Store
	uploads *uploadManager
}

// NewWorkspaceHandler creates a WorkspaceHandler with upload state management.
func NewWorkspaceHandler(store *Store) *WorkspaceHandler {
	return &WorkspaceHandler{
		store:   store,
		uploads: newUploadManager(),
	}
}

// ---------- helpers ----------

func getNode(w http.ResponseWriter, r *http.Request) types.RemoteNode {
	nodeID := r.URL.Query().Get("nodeId")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId is required")
		return nil
	}
	n := node.Get(nodeID)
	if n == nil {
		writeError(w, http.StatusNotFound, "node not found")
		return nil
	}
	return n
}

func openWorkspace(ctx context.Context, w http.ResponseWriter, n types.RemoteNode, wsID, branch string) types.Workspace {
	if branch == "" {
		branch = "main"
	}
	ws, err := n.OpenWorkspace(ctx, wsID, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open workspace: "+err.Error())
		return nil
	}
	return ws
}

// ---------- Workspace CRUD ----------

// ListWorkspaces returns all workspaces for a node.
func (h *WorkspaceHandler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	entries, err := n.ListWorkspaces(r.Context(), types.ListOptions{
		Owner: r.URL.Query().Get("owner"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, entries)
}

// CreateWorkspace creates a new workspace.
func (h *WorkspaceHandler) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID string `json:"nodeId"`
		Name   string `json:"name"`
		Owner  string `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.NodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId is required")
		return
	}
	n := node.Get(req.NodeID)
	if n == nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	ws, err := n.CreateWorkspace(r.Context(), types.Meta{Name: req.Name, Owner: req.Owner})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": ws.ID(), "meta": ws.Meta()})
}

// DeleteWorkspace deletes a workspace by ID.
func (h *WorkspaceHandler) DeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	wsID := r.PathValue("id")
	if err := n.DeleteWorkspace(r.Context(), wsID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

// UpdateMeta updates workspace metadata.
func (h *WorkspaceHandler) UpdateMeta(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	wsID := r.PathValue("id")
	ws := openWorkspace(r.Context(), w, n, wsID, "")
	if ws == nil {
		return
	}
	var meta types.Meta
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := ws.UpdateMeta(r.Context(), meta); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "updated"})
}

// ---------- Branch CRUD ----------

// ListBranches returns all branches for a workspace.
func (h *WorkspaceHandler) ListBranches(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	branches, err := ws.ListBranches(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, branches)
}

// CreateBranch creates a new branch.
func (h *WorkspaceHandler) CreateBranch(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	var req struct {
		Name string         `json:"name"`
		From string         `json:"from"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.From == "" {
		req.From = "main"
	}
	branch, err := ws.CreateBranch(r.Context(), req.Name, req.From, req.Meta)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"name": branch.Name(), "meta": branch.Meta()})
}

// DeleteBranch deletes a branch.
func (h *WorkspaceHandler) DeleteBranch(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	if err := ws.DeleteBranch(r.Context(), r.PathValue("name")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

// CommitBranch commits on a branch.
func (h *WorkspaceHandler) CommitBranch(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	branch, err := ws.GetBranch(r.Context(), r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ci, err := branch.Commit(r.Context(), req.Message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, ci)
}

// RollbackBranch rolls back a branch to a commit.
func (h *WorkspaceHandler) RollbackBranch(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	branch, err := ws.GetBranch(r.Context(), r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req struct {
		CommitID string `json:"commitId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := branch.Rollback(r.Context(), req.CommitID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "rolled back"})
}

// MergeWorkspace merges two branches.
func (h *WorkspaceHandler) MergeWorkspace(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	var req struct {
		From         string `json:"from"`
		Into         string `json:"into"`
		DeleteSource bool   `json:"deleteSource"`
		Message      string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := ws.Merge(r.Context(), req.From, req.Into, types.MergeOptions{
		DeleteSource: req.DeleteSource,
		Message:      req.Message,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, result)
}

// ---------- Commit History ----------

// BranchLog returns commit log for a branch.
func (h *WorkspaceHandler) BranchLog(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	history, err := ws.LoadHistory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	entries, err := history.BranchLog(r.PathValue("branch"), types.LogOptions{Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, entries)
}

// HistoryQuery returns commit history across all branches.
func (h *WorkspaceHandler) HistoryQuery(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	history, err := ws.LoadHistory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	entries, err := history.Query(types.HistoryOptions{Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, entries)
}

// GetCommit returns a single commit.
func (h *WorkspaceHandler) GetCommit(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), "")
	if ws == nil {
		return
	}
	ci, err := ws.GetCommit(r.Context(), r.PathValue("commitId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, ci)
}

// ---------- File Operations ----------

// FileStat returns file info or directory listing.
func (h *WorkspaceHandler) FileStat(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	branch := r.PathValue("branch")
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), branch)
	if ws == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}

	branchObj, err := ws.GetBranch(r.Context(), branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bfs := branchObj.FS()

	info, err := fs.Stat(bfs, path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if info.IsDir() {
		entries, err := fs.ReadDir(bfs, path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			item := map[string]any{"name": e.Name(), "isDir": e.IsDir()}
			if fi, err := e.Info(); err == nil {
				item["size"] = fi.Size()
				item["modTime"] = fi.ModTime().Unix()
			}
			items = append(items, item)
		}
		writeJSON(w, map[string]any{"isDir": true, "path": path, "entries": items})
		return
	}

	writeJSON(w, map[string]any{
		"isDir":   false,
		"path":    path,
		"name":    info.Name(),
		"size":    info.Size(),
		"modTime": info.ModTime().Unix(),
	})
}

// FileContent reads file content with inferred Content-Type.
// Streams the file via io.Copy — O(1) memory regardless of file size.
func (h *WorkspaceHandler) FileContent(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	branch := r.PathValue("branch")
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), branch)
	if ws == nil {
		return
	}
	path := r.URL.Query().Get("path")

	branchObj, err := ws.GetBranch(r.Context(), branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bfs := branchObj.FS()

	f, err := bfs.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if fi.IsDir() {
		writeError(w, http.StatusBadRequest, "path is a directory")
		return
	}

	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if fi.Size() > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	}
	io.Copy(w, f)
}

// FileUpload writes file content from request body.
func (h *WorkspaceHandler) FileUpload(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	branch := r.PathValue("branch")
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), branch)
	if ws == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	branchObj, err := ws.GetBranch(r.Context(), branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bfs := branchObj.FS()

	writer, err := bfs.Create(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := io.Copy(writer, r.Body); err != nil {
		writer.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := writer.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "uploaded", "path": path})
}

// FileDelete removes a file or directory.
func (h *WorkspaceHandler) FileDelete(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	branch := r.PathValue("branch")
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), branch)
	if ws == nil {
		return
	}
	path := r.URL.Query().Get("path")

	branchObj, err := ws.GetBranch(r.Context(), branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bfs := branchObj.FS()

	if r.URL.Query().Get("recursive") == "true" {
		err = bfs.RemoveAll(path)
	} else {
		err = bfs.Remove(path)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

// FileMkdir creates a directory.
func (h *WorkspaceHandler) FileMkdir(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	branch := r.PathValue("branch")
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), branch)
	if ws == nil {
		return
	}
	path := r.URL.Query().Get("path")

	branchObj, err := ws.GetBranch(r.Context(), branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := branchObj.FS().MkdirAll(path, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "created"})
}

// FileRename renames a file or directory.
func (h *WorkspaceHandler) FileRename(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	branch := r.PathValue("branch")
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), branch)
	if ws == nil {
		return
	}
	var req struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	branchObj, err := ws.GetBranch(r.Context(), branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := branchObj.FS().Rename(req.OldPath, req.NewPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "renamed"})
}

// FilePreview serves file content with Content-Disposition: inline for browser
// preview. For video/audio, uses http.ServeContent with the seekable remote
// file — Range requests only transfer the requested interval.
func (h *WorkspaceHandler) FilePreview(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	branch := r.PathValue("branch")
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), branch)
	if ws == nil {
		return
	}
	path := r.URL.Query().Get("path")

	branchObj, err := ws.GetBranch(r.Context(), branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bfs := branchObj.FS()

	f, err := bfs.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("inline; filename=%q", filepath.Base(path)))

	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), rs)
	} else {
		io.Copy(w, f)
	}
}

// ---------- Chunked Upload ----------

// ChunkedUploadInit starts a chunked upload session.
func (h *WorkspaceHandler) ChunkedUploadInit(w http.ResponseWriter, r *http.Request) {
	n := getNode(w, r)
	if n == nil {
		return
	}
	branch := r.PathValue("branch")
	ws := openWorkspace(r.Context(), w, n, r.PathValue("id"), branch)
	if ws == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	branchObj, err := ws.GetBranch(r.Context(), branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writer, err := branchObj.FS().Create(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	uploadID := h.uploads.create(writer)
	writeJSON(w, map[string]string{"uploadId": uploadID})
}

// ChunkedUploadChunk writes a chunk to an active upload.
func (h *WorkspaceHandler) ChunkedUploadChunk(w http.ResponseWriter, r *http.Request) {
	uploadID := r.URL.Query().Get("uploadId")
	u := h.uploads.get(uploadID)
	if u == nil {
		writeError(w, http.StatusNotFound, "upload not found or expired")
		return
	}

	u.mu.Lock()
	_, err := io.Copy(u.writer, r.Body)
	u.lastActive = time.Now()
	u.mu.Unlock()

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "chunk_received"})
}

// ChunkedUploadComplete finishes a chunked upload.
func (h *WorkspaceHandler) ChunkedUploadComplete(w http.ResponseWriter, r *http.Request) {
	uploadID := r.URL.Query().Get("uploadId")
	u := h.uploads.remove(uploadID)
	if u == nil {
		writeError(w, http.StatusNotFound, "upload not found or expired")
		return
	}

	u.mu.Lock()
	err := u.writer.Close()
	u.mu.Unlock()

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "completed"})
}

// ---------- Upload Manager ----------

type activeUpload struct {
	mu         sync.Mutex
	writer     io.WriteCloser
	lastActive time.Time
}

type uploadManager struct {
	mu      sync.Mutex
	uploads map[string]*activeUpload
}

func newUploadManager() *uploadManager {
	m := &uploadManager{uploads: make(map[string]*activeUpload)}
	go m.cleanupLoop()
	return m
}

func (m *uploadManager) create(w io.WriteCloser) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("up_%d", time.Now().UnixNano())
	m.uploads[id] = &activeUpload{writer: w, lastActive: time.Now()}
	return id
}

func (m *uploadManager) get(id string) *activeUpload {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.uploads[id]
}

func (m *uploadManager) remove(id string) *activeUpload {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.uploads[id]
	delete(m.uploads, id)
	return u
}

func (m *uploadManager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		for id, u := range m.uploads {
			u.mu.Lock()
			if time.Since(u.lastActive) > 60*time.Second {
				u.writer.Close()
				delete(m.uploads, id)
			}
			u.mu.Unlock()
		}
		m.mu.Unlock()
	}
}
