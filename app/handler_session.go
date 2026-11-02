package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/node"
	"github.com/yaoapp/tai-golang/types"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

// SessionHandler serves session endpoints and WebSocket relay.
type SessionHandler struct {
	store *Store
	auth  types.AuthProvider
}

// List returns all active sessions.
func (h *SessionHandler) List(w http.ResponseWriter, _ *http.Request) {
	sessions := h.store.Sessions()
	out := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		entry := map[string]any{
			"id":        s.ID,
			"nodeId":    s.NodeID,
			"createdAt": s.CreatedAt,
			"status":    s.Status,
		}
		if s.WorkspaceID != "" {
			entry["workspaceId"] = s.WorkspaceID
			entry["workspaceName"] = s.WorkspaceName
		}
		out = append(out, entry)
	}
	writeJSON(w, out)
}

// Create opens a new session on a registered node.
func (h *SessionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID  string                `json:"nodeId"`
		Request *types.SessionRequest `json:"request"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.NodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId is required")
		return
	}
	if req.Request == nil {
		req.Request = &types.SessionRequest{}
	}
	if req.Request.Runtime == "" {
		req.Request.Runtime = "agent"
	}
	if req.Request.Isolation == "" {
		req.Request.Isolation = "host"
	}

	// Pass DeepSeek credentials to the agent subprocess via session env.
	if req.Request.Env == nil {
		req.Request.Env = make(map[string]string)
	}
	if apiKey := os.Getenv("DEEPSEEK_API_KEY"); apiKey != "" && req.Request.Env["DEEPSEEK_API_KEY"] == "" {
		req.Request.Env["DEEPSEEK_API_KEY"] = apiKey
	}
	if req.Request.Env["DEEPSEEK_BASE_URL"] == "" {
		baseURL := os.Getenv("DEEPSEEK_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.deepseek.com/v1"
		}
		req.Request.Env["DEEPSEEK_BASE_URL"] = baseURL
	}

	sessionID := auth.GenerateID("sess")

	opToken, err := h.auth.IssueOperationToken(req.NodeID, sessionID, []string{"session"}, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue operation token: "+err.Error())
		return
	}

	rn := node.Get(req.NodeID)
	if rn == nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	// Auto-create workspace when neither cwd nor workspaceId is specified.
	if req.Request.Cwd == "" && req.Request.WorkspaceID == "" {
		ws, err := rn.CreateWorkspace(r.Context(), types.Meta{Name: "session-" + sessionID[:12]})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "auto-create workspace: "+err.Error())
			return
		}
		req.Request.WorkspaceID = ws.ID()
	}

	session, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: sessionID,
		Token:     opToken,
		Request:   req.Request,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open session: "+err.Error())
		return
	}

	// Resolve workspace name for display.
	wsID := req.Request.WorkspaceID
	wsName := ""
	if wsID != "" && rn != nil {
		entries, err := rn.ListWorkspaces(r.Context(), types.ListOptions{})
		if err == nil {
			for _, e := range entries {
				if e.ID == wsID {
					wsName = e.Name
					break
				}
			}
		}
	}

	info := &SessionInfo{
		ID:            sessionID,
		NodeID:        req.NodeID,
		CreatedAt:     time.Now(),
		Status:        "running",
		WorkspaceID:   wsID,
		WorkspaceName: wsName,
		Session:       session,
	}
	h.store.AddSession(info)

	writeJSON(w, map[string]any{
		"id":            sessionID,
		"nodeId":        req.NodeID,
		"status":        "running",
		"workspaceId":   wsID,
		"workspaceName": wsName,
	})
}

// Delete closes a session.
func (h *SessionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := h.store.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := info.Session.Close(ctx); err != nil {
		log.Printf("close session %s: %v", id, err)
	}
	h.store.RemoveSession(id)
	writeJSON(w, map[string]string{"status": "closed"})
}

// WSCtrl relays the ctrl stream between browser and session.
func (h *SessionHandler) WSCtrl(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := h.store.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade ctrl: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Read actions from session → send to browser.
	go func() {
		defer cancel()
		for {
			act, err := info.Session.RecvAction()
			if err != nil {
				return
			}
			msg := map[string]any{
				"seq":  act.Seq,
				"kind": act.Kind,
			}
			if len(act.Data) > 0 {
				var data any
				json.Unmarshal(act.Data, &data)
				msg["data"] = data
			}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}()

	// Read commands from browser → dispatch to session.
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var cmd struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := conn.ReadJSON(&cmd); err != nil {
			return
		}

		switch cmd.Kind {
		case "prompt":
			var um types.UserMessage
			json.Unmarshal(cmd.Data, &um)
			if err := info.Session.Prompt(&um); err != nil {
				log.Printf("prompt: %v", err)
			}
		case "followup":
			var um types.UserMessage
			json.Unmarshal(cmd.Data, &um)
			info.Session.Followup(&um)
		case "cancel":
			info.Session.Cancel()
		case "steer":
			var um types.UserMessage
			json.Unmarshal(cmd.Data, &um)
			info.Session.Steer(&um)
		case "resolve_permission":
			var rp struct {
				RequestID string                   `json:"requestId"`
				Response  types.PermissionResponse `json:"response"`
			}
			json.Unmarshal(cmd.Data, &rp)
			info.Session.ResolvePermission(rp.RequestID, &rp.Response)
		case "resolve_elicitation":
			var re struct {
				ID       string                    `json:"id"`
				Response types.ElicitationResponse `json:"response"`
			}
			json.Unmarshal(cmd.Data, &re)
			info.Session.ResolveElicitation(re.ID, &re.Response)
		case "close":
			info.Session.Close(context.Background())
			return
		}
	}
}

// WSData relays the data stream (events) from session to browser (read-only).
func (h *SessionHandler) WSData(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := h.store.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade data: %v", err)
		return
	}
	defer conn.Close()

	for {
		evt, err := info.Session.RecvEvent()
		if err != nil {
			if err != io.EOF {
				log.Printf("recv event: %v", err)
			}
			conn.WriteJSON(map[string]string{"kind": "_closed"})
			return
		}
		msg := map[string]any{
			"seq":  evt.Seq,
			"kind": evt.Kind,
		}
		if len(evt.Data) > 0 {
			var data any
			json.Unmarshal(evt.Data, &data)
			msg["data"] = data
		}
		if err := conn.WriteJSON(msg); err != nil {
			return
		}
	}
}
