package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/yaoapp/tai-golang/types"
	"github.com/yaoapp/tai-golang/workspace"
)

// ContextHandler exposes workspace Context and Skills over HTTP.
//
// These endpoints wrap the SDK's package-level helpers (workspace.ReadContext,
// SeedContext, MergeContext, SyncSkills) so the demo app can exercise the
// Context API.
//
// Limitation: the Tai runtime does not read .context/ today, and AGENTS.md is
// not generated from it. Editing these files therefore does not change agent
// behavior. The frontend states this explicitly.
type ContextHandler struct{}

// NewContextHandler creates the Context handler.
//
// @returns the handler
func NewContextHandler() *ContextHandler {
	return &ContextHandler{}
}

// contextScopes is the merge order used by MergeContext, lowest priority first.
//
// Corresponds to the SDK's ContextScope constants:
// bot → home → board → session.
var contextScopes = []workspace.ContextScope{
	workspace.ScopeBot,
	workspace.ScopeHome,
	workspace.ScopeBoard,
	workspace.ScopeSession,
}

// contextFileNames are the standard files inside each scope directory.
var contextFileNames = []string{"INSTRUCTION.md", "MEMORY.md", "PREFERENCE.md"}

// scopeView is the read result for one scope.
type scopeView struct {
	Scope string            `json:"scope"`
	ID    string            `json:"id"`
	Files map[string]string `json:"files"` // filename → content; absent files are omitted
}

// GetContext returns the Context files for the requested scopes.
//
// The ids query parameter names each scope's ID, e.g.
// "bot:bot1,home:user1,session:sess1". Omitted scopes are skipped.
func (h *ContextHandler) GetContext(w http.ResponseWriter, r *http.Request) {
	ws := h.openWorkspace(w, r)
	if ws == nil {
		return
	}
	defer ws.Close()

	ids := parseScopeIDs(r.URL.Query().Get("ids"))

	views := make([]scopeView, 0, len(contextScopes))
	for _, scope := range contextScopes {
		id := ids[scope]
		if id == "" {
			continue
		}
		files, err := workspace.ReadContext(ws, scope, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read context: "+err.Error())
			return
		}
		view := scopeView{Scope: string(scope), ID: id, Files: map[string]string{}}
		if files.Instruction != nil {
			view.Files["INSTRUCTION.md"] = string(files.Instruction)
		}
		if files.Memory != nil {
			view.Files["MEMORY.md"] = string(files.Memory)
		}
		if files.Preference != nil {
			view.Files["PREFERENCE.md"] = string(files.Preference)
		}
		views = append(views, view)
	}

	writeJSON(w, map[string]any{"scopes": views})
}

// PutContextFile writes a single Context file.
//
// Path parameters: {id} workspace, {scope} bot/home/board/session, {scopeID},
// {file}. The request body is the file content as plain text, not JSON.
func (h *ContextHandler) PutContextFile(w http.ResponseWriter, r *http.Request) {
	ws := h.openWorkspace(w, r)
	if ws == nil {
		return
	}
	defer ws.Close()

	scope := workspace.ContextScope(r.PathValue("scope"))
	scopeID := r.PathValue("scopeID")
	file := r.PathValue("file")

	if !validScope(scope) {
		writeError(w, http.StatusBadRequest, "unknown scope: "+string(scope))
		return
	}
	if !validScopeID(scopeID) {
		writeError(w, http.StatusBadRequest, "invalid scopeID")
		return
	}
	if !validContextFile(file) {
		writeError(w, http.StatusBadRequest, "unknown context file: "+file)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB cap
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	dir := workspace.ContextDir(scope, scopeID)
	if err := ws.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "mkdir: "+err.Error())
		return
	}
	f, err := ws.Create(dir + "/" + file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create: "+err.Error())
		return
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		writeError(w, http.StatusInternalServerError, "write: "+err.Error())
		return
	}
	if err := f.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "close: "+err.Error())
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

// SeedContext writes default files into a scope without overwriting existing
// ones.
//
// The request body is JSON: {"files":{"INSTRUCTION.md":"...","MEMORY.md":"..."}}.
func (h *ContextHandler) SeedContext(w http.ResponseWriter, r *http.Request) {
	ws := h.openWorkspace(w, r)
	if ws == nil {
		return
	}
	defer ws.Close()

	scope := workspace.ContextScope(r.PathValue("scope"))
	scopeID := r.PathValue("scopeID")
	if !validScope(scope) {
		writeError(w, http.StatusBadRequest, "unknown scope: "+string(scope))
		return
	}
	if !validScopeID(scopeID) {
		writeError(w, http.StatusBadRequest, "invalid scopeID")
		return
	}

	var req struct {
		Files map[string]string `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	files := make(map[string][]byte, len(req.Files))
	for name, content := range req.Files {
		if !validContextFile(name) {
			writeError(w, http.StatusBadRequest, "unknown context file: "+name)
			return
		}
		files[name] = []byte(content)
	}

	if err := workspace.SeedContext(ws, scope, scopeID, files); err != nil {
		writeError(w, http.StatusInternalServerError, "seed: "+err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// GetMergedContext returns the merged Markdown for the requested scopes.
//
// Order is bot → home → board → session (lowest to highest priority).
func (h *ContextHandler) GetMergedContext(w http.ResponseWriter, r *http.Request) {
	ws := h.openWorkspace(w, r)
	if ws == nil {
		return
	}
	defer ws.Close()

	ids := parseScopeIDs(r.URL.Query().Get("ids"))
	entries := make([]workspace.ContextEntry, 0, len(contextScopes))
	for _, scope := range contextScopes {
		if id := ids[scope]; id != "" {
			entries = append(entries, workspace.ContextEntry{Scope: scope, ID: id})
		}
	}
	if len(entries) == 0 {
		writeError(w, http.StatusBadRequest, "no scope ids provided")
		return
	}

	merged, err := workspace.MergeContext(ws, entries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "merge: "+err.Error())
		return
	}
	writeJSON(w, map[string]string{"merged": merged})
}

// skillView is one synced skill for display.
type skillView struct {
	Key  string `json:"key"`
	Hash string `json:"hash"`
}

// ListSkills returns the skills synced under .skills/ and their hashes.
func (h *ContextHandler) ListSkills(w http.ResponseWriter, r *http.Request) {
	ws := h.openWorkspace(w, r)
	if ws == nil {
		return
	}
	defer ws.Close()

	manifest, err := ws.ReadManifest(".skills")
	if err != nil {
		// No manifest yet means no skills, not an error.
		writeJSON(w, map[string]any{"skills": []skillView{}})
		return
	}

	skills := make([]skillView, 0, len(manifest))
	for key, hash := range manifest {
		skills = append(skills, skillView{Key: key, Hash: hash})
	}
	writeJSON(w, map[string]any{"skills": skills})
}

// UploadSkill uploads and syncs one skill package.
//
// The request is multipart/form-data with:
//   - key: manifest key, e.g. "bots/postman"
//   - file: a tar.gz or zip archive
//
// Syncing is idempotent by hash: unchanged packages report pushed=0.
func (h *ContextHandler) UploadSkill(w http.ResponseWriter, r *http.Request) {
	ws := h.openWorkspace(w, r)
	if ws == nil {
		return
	}
	defer ws.Close()

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	key := r.FormValue("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required: "+err.Error())
		return
	}
	defer file.Close()

	// Hash first: a SkillPack's Archive can only be read once.
	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read archive: "+err.Error())
		return
	}
	hash, err := workspace.SkillHash(bytes.NewReader(data))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash: "+err.Error())
		return
	}

	pushed, err := workspace.SyncSkills(ws, []workspace.SkillPack{{
		Key:     key,
		Hash:    hash,
		Archive: bytes.NewReader(data),
	}})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sync: "+err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"status": "ok",
		"key":    key,
		"hash":   hash,
		"pushed": pushed,
	})
}

// ---------- helpers ----------

// openWorkspace resolves the node and workspace from the request, writing the
// error response itself on failure.
//
// Reuses the WorkspaceHandler helpers so error handling stays consistent with
// the other workspace endpoints.
func (h *ContextHandler) openWorkspace(w http.ResponseWriter, r *http.Request) types.Workspace {
	n := getNode(w, r)
	if n == nil {
		return nil
	}
	wsID := r.PathValue("id")
	return openWorkspace(r.Context(), w, n, wsID, "")
}

// validScopeID reports whether id is safe to use as a path segment.
//
// The ID is concatenated into ".context/<scope>/<id>", so path separators and
// traversal sequences must be rejected; without this an id of "../.." would
// escape the context directory.
//
// @param id the scope instance ID from the URL
// @returns true when the ID is non-empty and contains no path separators
func validScopeID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, `/\`) {
		return false
	}
	return !strings.Contains(id, "..")
}

// validScope reports whether s is one of the four known scopes.
func validScope(s workspace.ContextScope) bool {
	for _, v := range contextScopes {
		if v == s {
			return true
		}
	}
	return false
}

// validContextFile reports whether name is one of the three standard files.
//
// Restricting the name set also prevents path traversal.
func validContextFile(name string) bool {
	for _, v := range contextFileNames {
		if v == name {
			return true
		}
	}
	return false
}

// parseScopeIDs parses "bot:id1,home:id2" into a scope→ID map.
func parseScopeIDs(raw string) map[workspace.ContextScope]string {
	out := make(map[workspace.ContextScope]string)
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		out[workspace.ContextScope(parts[0])] = parts[1]
	}
	return out
}
