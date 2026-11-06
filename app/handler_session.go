package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
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
			"status":    s.GetStatus(),
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

	// Pass API credentials to the agent subprocess via session env.
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
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" && req.Request.Env["OPENAI_API_KEY"] == "" {
		req.Request.Env["OPENAI_API_KEY"] = apiKey
	}

	// Convert frontend ProvidersMatrix {default,light,realtime} → runtime Providers {primary,roles}.
	// The runtime expects: primary = default role, roles = {light, realtime, ...}.
	if len(req.Request.LLM) > 0 {
		var matrix map[string]json.RawMessage
		if err := json.Unmarshal(req.Request.LLM, &matrix); err == nil {
			converted := make(map[string]json.RawMessage)
			roles := make(map[string]json.RawMessage)
			for role, endpoint := range matrix {
				if role == "default" || role == "primary" {
					converted["primary"] = endpoint
				} else {
					roles[role] = endpoint
				}
			}
			// If no explicit "default"/"primary", use first available non-realtime role.
			if _, ok := converted["primary"]; !ok {
				for role, endpoint := range matrix {
					if role != "realtime" {
						converted["primary"] = endpoint
						break
					}
				}
			}
			if len(roles) > 0 {
				rolesJSON, _ := json.Marshal(roles)
				converted["roles"] = rolesJSON
			}
			if rewritten, err := json.Marshal(converted); err == nil {
				req.Request.LLM = rewritten
			}
		}
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

	// Capture store reference for lifecycle callbacks.
	store := h.store

	session, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: sessionID,
		Token:     opToken,
		Request:   req.Request,
		OnEvent: func(sid string, event types.SessionEventKind) {
			si, ok := store.GetSession(sid)
			if !ok {
				return
			}
			switch event {
			case types.EventDisconnected:
				si.SetStatus(StatusDisconnected, store)
			case types.EventResumed:
				si.SetStatus(StatusRunning, store)
			case types.EventRecoveryGap:
				si.SetStatus(StatusRecoveryGap, store)
			case types.EventResumeError:
				si.SetStatus(StatusResumeError, store)
			}
		},
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
		Status:        StatusRunning,
		WorkspaceID:   wsID,
		WorkspaceName: wsName,
		Session:       session,
	}
	h.store.AddSession(info)

	// Monitor session lifecycle in the background.
	go h.watchSession(info, session)

	writeJSON(w, map[string]any{
		"id":            sessionID,
		"nodeId":        req.NodeID,
		"status":        string(StatusRunning),
		"workspaceId":   wsID,
		"workspaceName": wsName,
	})
}

// watchSession waits for the session to end and skips the status update when
// the instance has already been replaced.
//
// Two endings are distinguished: a deliberate Close sets StatusClosed first, so
// this watcher leaves it alone; an unintended end (node crash, connection lost
// for good) is recorded as StatusTerminated.
//
// The identity check is required: Resume creates a new SDK Session and swaps it
// into info.Session, after which the old object's Done() closes. Without the
// check the stale watcher would overwrite the freshly resumed running status
// with terminated.
//
// @param info the session record
// @param sess the instance snapshot this watcher is responsible for
func (h *SessionHandler) watchSession(info *SessionInfo, sess types.Session) {
	<-sess.Done()

	info.mu.Lock()
	stillCurrent := info.Session == sess
	current := info.Status
	info.mu.Unlock()

	if !stillCurrent {
		// Replaced by a Resume; this exit belongs to the old instance.
		return
	}
	if current.Terminal() {
		// Already final from a deliberate Close; do not overwrite.
		return
	}
	info.SetStatus(StatusTerminated, h.store)
}

// Delete closes a session.
func (h *SessionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := h.store.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Mark final before closing so watchSession does not record terminated.
	info.SetStatus(StatusClosed, h.store)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if sess := info.sess(); sess != nil {
		if err := sess.Close(ctx); err != nil {
			log.Printf("close session %s: %v", id, err)
		}
	}
	h.store.RemoveSession(id)
	writeJSON(w, map[string]string{"status": string(StatusClosed)})
}

// Resume reconnects to an existing session after a disconnect.
//
// This is the fallback for when the SDK's own reconnect gives up: tryResume
// reattaches automatically on a dropped transport, and only failure (reconnect
// error, recovery buffer overflow, node restart) needs a manual trigger here.
func (h *SessionHandler) Resume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := h.store.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	prev := info.GetStatus()
	if !prev.CanResume() {
		writeError(w, http.StatusConflict,
			"session status "+string(prev)+" is not resumable")
		return
	}

	rn := node.Get(info.NodeID)
	if rn == nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	store := h.store
	opToken, err := h.auth.IssueOperationToken(info.NodeID, id, []string{"session"}, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue operation token: "+err.Error())
		return
	}

	// Intermediate state: the frontend shows a spinner and blocks double clicks.
	info.SetStatus(StatusResuming, store)

	newSession, err := rn.ResumeSession(context.Background(), &types.ResumeSessionRequest{
		SessionID:   id,
		Token:       opToken,
		LastCtrlSeq: 0,
		LastDataSeq: 0,
		OnEvent: func(sid string, event types.SessionEventKind) {
			si, ok := store.GetSession(sid)
			if !ok {
				return
			}
			switch event {
			case types.EventDisconnected:
				si.SetStatus(StatusDisconnected, store)
			case types.EventResumed:
				si.SetStatus(StatusRunning, store)
			case types.EventRecoveryGap:
				si.SetStatus(StatusRecoveryGap, store)
			case types.EventResumeError:
				si.SetStatus(StatusResumeError, store)
			}
		},
	})
	if err != nil {
		// Roll back so the user can retry.
		info.SetStatus(prev, store)
		writeError(w, http.StatusInternalServerError, "resume session: "+err.Error())
		return
	}

	// Swap the instance. When the record was deleted while ResumeSession was
	// in flight, close the fresh instance instead of installing it; the
	// replacement refusal makes this race-free.
	old, deleted := info.replaceSession(newSession)
	if deleted {
		if cerr := newSession.Close(context.Background()); cerr != nil {
			log.Printf("close orphaned resumed session %s: %v", id, cerr)
		}
		writeError(w, http.StatusGone, "session was closed during resume")
		return
	}
	if old != nil {
		go func() {
			if cerr := old.Close(context.Background()); cerr != nil {
				log.Printf("close replaced session %s: %v", id, cerr)
			}
		}()
	}

	info.SetStatus(StatusRunning, store)
	go h.watchSession(info, newSession)

	writeJSON(w, map[string]any{
		"id":     id,
		"status": string(StatusRunning),
	})
}

// WSCtrl relays the ctrl stream between browser and session.
//
// Channel lifecycle (avoids RecvAction race with StartRealtime):
//
//   - Text mode: NO RecvAction goroutine. Prompt/Cancel/Steer send commands;
//     agent responses arrive on the data stream (RecvEvent via WSData).
//   - start_realtime: goroutine calls StartRealtime (blocking, exclusive
//     RecvAction access — no competing reader). On success, starts a
//     RecvAction relay goroutine for ctrl events (audio.speech_started, etc.).
//   - stop_realtime: calls StopRealtime, sends realtime.stopped to browser.
//     The RecvAction goroutine exits upon reading realtime.stopped from Tai.
//   - Subsequent start_realtime waits for the previous RecvAction goroutine
//     to exit (with timeout) before calling StartRealtime again.
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

	var wsMu sync.Mutex
	sendJSON := func(v any) {
		wsMu.Lock()
		defer wsMu.Unlock()
		if err := conn.WriteJSON(v); err != nil {
			log.Printf("ctrl ws write: %v", err)
		}
	}

	// Read browser commands into a channel so the main select can multiplex.
	type ctrlCmd struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}
	cmdCh := make(chan ctrlCmd, 8)
	go func() {
		defer close(cmdCh)
		defer cancel()
		for {
			var cmd ctrlCmd
			if err := conn.ReadJSON(&cmd); err != nil {
				return
			}
			cmdCh <- cmd
		}
	}()

	// Realtime lifecycle state (protected by rtMu).
	var (
		rtMu     sync.Mutex
		rtActive bool
		rtDone   chan struct{} // closed when the RecvAction goroutine exits; nil initially
	)

	// startActionRelay launches a RecvAction goroutine that relays ctrl events
	// to the browser. It returns a channel that closes when the goroutine exits.
	// Call ONLY when no other RecvAction reader exists (after StartRealtime returns).
	startActionRelay := func() chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				// Snapshot each round so a Resume swap is picked up; a nil
				// instance means the session is being torn down, so stop.
				sess := info.sess()
				if sess == nil {
					return
				}
				act, err := sess.RecvAction()
				if err != nil {
					return
				}
				msg := map[string]any{"seq": act.Seq, "kind": act.Kind}
				if len(act.Data) > 0 {
					var data any
					if uerr := json.Unmarshal(act.Data, &data); uerr == nil {
						msg["data"] = data
					}
				}
				sendJSON(msg)

				// Exit only when the realtime session definitively ends.
				// realtime.error is a transient notification (e.g. provider
				// error); realtime.stopped follows when the server finishes
				// cleanup. Exiting early on realtime.error would miss the
				// subsequent realtime.stopped and leave the SDK state dirty.
				if act.Kind == "realtime.stopped" {
					rtMu.Lock()
					rtActive = false
					rtMu.Unlock()
					return
				}
			}
		}()
		return done
	}

	for {
		// Snapshot each iteration so a Resume swap is picked up here.
		// select case expressions are evaluated on entry, so it must be read first.
		sess := info.sess()
		select {
		case <-ctx.Done():
			return
		case <-sess.Done():
			return
		case cmd, ok := <-cmdCh:
			if !ok {
				return
			}
			switch cmd.Kind {

			// ---- Text-mode commands ----

			case "prompt":
				var um types.UserMessage
				json.Unmarshal(cmd.Data, &um)
				if err := sess.Prompt(&um); err != nil {
					log.Printf("prompt: %v", err)
					sendJSON(map[string]any{
						"kind": "prompt_error",
						"data": map[string]string{"message": err.Error()},
					})
				}

			case "followup":
				var um types.UserMessage
				json.Unmarshal(cmd.Data, &um)
				go func() {
					disp, ferr := sess.Followup(&um)
					if ferr != nil {
						sendJSON(map[string]any{
							"kind": "followup_error",
							"data": map[string]string{"message": ferr.Error()},
						})
					} else {
						sendJSON(map[string]any{
							"kind": "followup_disposition",
							"data": map[string]string{"disposition": string(disp)},
						})
					}
				}()

			case "cancel":
				if err := sess.Cancel(); err != nil {
					log.Printf("cancel: %v", err)
				}

			case "steer":
				var um types.UserMessage
				json.Unmarshal(cmd.Data, &um)
				go func() {
					if _, serr := sess.Steer(&um); serr != nil {
						log.Printf("steer: %v", serr)
					}
				}()

			case "resolve_permission":
				var rp struct {
					RequestID string                   `json:"requestId"`
					Response  types.PermissionResponse `json:"response"`
				}
				json.Unmarshal(cmd.Data, &rp)
				sess.ResolvePermission(rp.RequestID, &rp.Response)

			case "resolve_elicitation":
				var re struct {
					ID       string                    `json:"id"`
					Response types.ElicitationResponse `json:"response"`
				}
				json.Unmarshal(cmd.Data, &re)
				sess.ResolveElicitation(re.ID, &re.Response)

			case "update_queue":
				var qa types.QueueAction
				json.Unmarshal(cmd.Data, &qa)
				if err := sess.UpdateQueue(&qa); err != nil {
					log.Printf("update_queue: %v", err)
				}

			case "set_config_option":
				var co struct {
					ConfigID string                  `json:"configId"`
					Value    types.ConfigOptionValue `json:"value"`
				}
				json.Unmarshal(cmd.Data, &co)
				if err := sess.SetConfigOption(co.ConfigID, &co.Value); err != nil {
					log.Printf("set_config_option: %v", err)
				}

			case "update_session":
				var opts types.SessionUpdateOptions
				json.Unmarshal(cmd.Data, &opts)
				if err := sess.UpdateSession(r.Context(), &opts); err != nil {
					log.Printf("update_session: %v", err)
				}

			// ---- Realtime lifecycle ----

			case "start_realtime":
				rtMu.Lock()
				if rtActive {
					rtMu.Unlock()
					sendJSON(map[string]any{
						"kind": "realtime.error",
						"data": map[string]string{"message": "realtime already active"},
					})
					continue
				}
				prevDone := rtDone
				rtMu.Unlock()

				var req types.RealtimeRequest
				json.Unmarshal(cmd.Data, &req)

				// Inject API keys from environment (frontend never sends keys).
				if req.Providers.Primary.APIKey == "" {
					switch req.Providers.Primary.ProviderID {
					case "openai":
						if k := os.Getenv("OPENAI_API_KEY"); k != "" {
							req.Providers.Primary.APIKey = k
						}
					case "deepseek":
						if k := os.Getenv("DEEPSEEK_API_KEY"); k != "" {
							req.Providers.Primary.APIKey = k
						}
					case "qwen":
						if k := os.Getenv("QWEN_API_KEY"); k != "" {
							req.Providers.Primary.APIKey = k
						}
					}
				}
				for role, prov := range req.Providers.Roles {
					if prov.APIKey == "" {
						switch prov.ProviderID {
						case "openai":
							if k := os.Getenv("OPENAI_API_KEY"); k != "" {
								prov.APIKey = k
								req.Providers.Roles[role] = prov
							}
						case "deepseek":
							if k := os.Getenv("DEEPSEEK_API_KEY"); k != "" {
								prov.APIKey = k
								req.Providers.Roles[role] = prov
							}
						}
					}
				}

				go func() {
					// Wait for any previous RecvAction goroutine to finish so we
					// have exclusive access to RecvAction inside StartRealtime.
					if prevDone != nil {
						select {
						case <-prevDone:
						case <-time.After(5 * time.Second):
							sendJSON(map[string]any{
								"kind": "realtime.error",
								"data": map[string]string{"message": "previous realtime cleanup timed out, try again"},
							})
							return
						case <-ctx.Done():
							return
						}
					}

					// StartRealtime blocks: sends realtime.start, waits for
					// realtime.started via RecvAction, opens audio/render streams.
					if err := sess.StartRealtime(&req); err != nil {
						log.Printf("StartRealtime: %v", err)
						sendJSON(map[string]any{
							"kind": "realtime.error",
							"data": map[string]string{"message": err.Error()},
						})
						return
					}

					rtMu.Lock()
					rtActive = true
					rtMu.Unlock()

					sendJSON(map[string]any{"kind": "realtime.started"})

					// Start RecvAction relay (the ONLY reader now).
					done := startActionRelay()
					rtMu.Lock()
					rtDone = done
					rtMu.Unlock()
				}()

			case "update_realtime":
				var opts types.RealtimeUpdateOptions
				json.Unmarshal(cmd.Data, &opts)
				if err := sess.UpdateRealtime(&opts); err != nil {
					log.Printf("update_realtime: %v", err)
				}

			case "stop_realtime":
				rtMu.Lock()
				wasActive := rtActive
				rtActive = false
				prevDone := rtDone
				rtDone = nil
				rtMu.Unlock()

				// StopRealtime closes audio/render channels and sends
				// realtime.stop on the ctrl stream. Run in a goroutine
				// with a timeout to prevent blocking the ctrl WS handler
				// if the H2 audio stream Close hangs.
				if wasActive {
					stopDone := make(chan error, 1)
					go func() { stopDone <- sess.StopRealtime() }()
					select {
					case err := <-stopDone:
						if err != nil {
							log.Printf("StopRealtime: %v", err)
						}
					case <-time.After(5 * time.Second):
						log.Printf("StopRealtime: timeout (5s)")
					case <-ctx.Done():
					}
				}

				// Wait for action relay to exit. Tai sends realtime.stopped
				// back on ctrl; the relay reads it and returns.
				if prevDone != nil {
					select {
					case <-prevDone:
					case <-time.After(3 * time.Second):
						log.Printf("stop_realtime: action relay exit timeout")
					case <-ctx.Done():
					}
				}
				sendJSON(map[string]any{"kind": "realtime.stopped"})

			// ---- Audio sub-commands (forwarded during active realtime) ----

			case "audio_commit":
				if err := sess.AudioCommit(); err != nil {
					log.Printf("audio_commit: %v", err)
				}
			case "audio_clear":
				if err := sess.AudioClear(); err != nil {
					log.Printf("audio_clear: %v", err)
				}
			case "audio_interrupt":
				if err := sess.AudioInterrupt(); err != nil {
					log.Printf("audio_interrupt: %v", err)
				}
			case "response_create":
				if err := sess.ResponseCreate(); err != nil {
					log.Printf("response_create: %v", err)
				}

			case "close":
				sess.Close(context.Background())
				return
			}
		}
	}
}

// WSAudio relays binary audio between browser and session (bidirectional).
// Connect AFTER receiving realtime.started — RecvAudio returns a non-nil
// channel only when StartRealtime has completed.
func (h *SessionHandler) WSAudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := h.store.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade audio: %v", err)
		return
	}
	defer conn.Close()

	sess := info.sess()
	if sess == nil {
		log.Printf("audio ws: session not ready")
		return
	}

	audioCh := sess.RecvAudio()
	if audioCh == nil {
		// Brief grace period in case the audio WS upgrade races with
		// StartRealtime setting up channels.
		time.Sleep(500 * time.Millisecond)
		audioCh = sess.RecvAudio()
		if audioCh == nil {
			log.Printf("audio ws: RecvAudio returned nil (realtime not active)")
			return
		}
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Session → Browser: send PCM audio frames as binary WS messages.
	go func() {
		defer cancel()
		sendCount := 0
		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-audioCh:
				if !ok {
					log.Printf("audio ws: audioCh closed after sending %d frames", sendCount)
					return
				}
				sendCount++
				if sendCount <= 3 || sendCount%100 == 0 {
					log.Printf("audio ws: send #%d (%d bytes) to browser", sendCount, len(frame.Data))
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, frame.Data); err != nil {
					log.Printf("audio ws: write error after %d sends: %v", sendCount, err)
					return
				}
			}
		}
	}()

	// Browser → Session: relay binary WS messages as AudioAppend.
	appendCount := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("audio ws: read done after %d appends: %v", appendCount, err)
			return
		}
		if msgType == websocket.BinaryMessage && len(data) > 0 {
			if err := sess.AudioAppend(data); err != nil {
				log.Printf("audio append: %v", err)
				return
			}
			appendCount++
			if appendCount <= 3 || appendCount%100 == 0 {
				log.Printf("audio append: #%d (%d bytes) OK", appendCount, len(data))
			}
		}
	}
}

// WSRender relays the render stream (transcript, emotion) from session to
// browser (read-only). Connect AFTER receiving realtime.started.
func (h *SessionHandler) WSRender(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := h.store.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade render: %v", err)
		return
	}
	defer conn.Close()

	sess := info.sess()
	if sess == nil {
		log.Printf("render ws: session not ready")
		conn.WriteJSON(map[string]string{"type": "_closed"})
		return
	}

	ch := sess.RecvRender()
	if ch == nil {
		time.Sleep(500 * time.Millisecond)
		ch = sess.RecvRender()
		if ch == nil {
			log.Printf("render ws: RecvRender returned nil (realtime not active)")
			conn.WriteJSON(map[string]string{"type": "_closed"})
			return
		}
	}

	// Drain WebSocket reads so close frames are detected.
	closeCh := make(chan struct{})
	go func() {
		defer close(closeCh)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				conn.WriteJSON(map[string]string{"type": "_closed"})
				return
			}
			if err := conn.WriteJSON(evt); err != nil {
				return
			}
		case <-closeCh:
			return
		case <-sess.Done():
			conn.WriteJSON(map[string]string{"type": "_closed"})
			return
		}
	}
}

// WSMotion relays motion frames (blendshape, head pose, viseme) from session
// to browser as JSON. Connect AFTER receiving motion.started on ctrl.
// Throttled: sends every 3rd frame (~10fps from 30fps source).
func (h *SessionHandler) WSMotion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := h.store.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade motion: %v", err)
		return
	}
	defer conn.Close()

	sess := info.sess()
	if sess == nil {
		conn.WriteJSON(map[string]string{"type": "_closed"})
		return
	}

	ch := sess.RecvMotion()
	if ch == nil {
		time.Sleep(500 * time.Millisecond)
		ch = sess.RecvMotion()
		if ch == nil {
			conn.WriteJSON(map[string]string{"type": "_closed"})
			return
		}
	}

	closeCh := make(chan struct{})
	go func() {
		defer close(closeCh)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	count := 0
	for {
		select {
		case frame, ok := <-ch:
			if !ok {
				conn.WriteJSON(map[string]string{"type": "_closed"})
				return
			}
			count++
			if count%3 != 0 {
				continue // ~10fps throttle
			}
			if err := conn.WriteJSON(frame); err != nil {
				return
			}
		case <-closeCh:
			return
		case <-sess.Done():
			conn.WriteJSON(map[string]string{"type": "_closed"})
			return
		}
	}
}

// WSData relays the data stream (events) from session to browser (read-only).
// Independent of ctrl/render/audio — runs for the entire session lifetime.
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

	sess := info.sess()
	if sess == nil {
		conn.WriteJSON(map[string]string{"kind": "_closed"})
		return
	}

	for {
		evt, err := sess.RecvEvent()
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
