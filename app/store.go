package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yaoapp/tai-golang/types"
)

// NodeInfo is a snapshot of a registered node for the API.
type NodeInfo struct {
	ID           string    `json:"id"`
	TaiID        string    `json:"taiId"`
	Version      string    `json:"version,omitempty"`
	OS           string    `json:"os,omitempty"`
	Arch         string    `json:"arch,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	RegisteredAt time.Time `json:"registeredAt"`
}

// SessionInfo tracks an active session for the API.
type SessionInfo struct {
	ID            string        `json:"id"`
	NodeID        string        `json:"nodeId"`
	CreatedAt     time.Time     `json:"createdAt"`
	Status        SessionStatus `json:"status"`
	WorkspaceID   string        `json:"workspaceId,omitempty"`
	WorkspaceName string        `json:"workspaceName,omitempty"`
	Session       types.Session `json:"-"`
	mu            sync.Mutex    // protects Status, Session, and deleted
	// deleted marks the record as removed from the store. It guards the
	// Resume path: replaceSession refuses once the record is gone, so a resume
	// that finishes after Delete cannot install an ownerless instance.
	deleted bool
}

// GetStatus returns the current session status (thread-safe).
//
// @returns the current status
func (si *SessionInfo) GetStatus() SessionStatus {
	si.mu.Lock()
	defer si.mu.Unlock()
	return si.Status
}

// sess returns a snapshot of the current session instance.
//
// Resume replaces the Session field while the WSCtrl/WSAudio/WSRender/
// WSMotion goroutines keep reading it. Every read must go through here rather
// than touching the field outside the lock. The result may be nil while the
// session is still being created.
//
// @returns the current session instance
func (si *SessionInfo) sess() types.Session {
	si.mu.Lock()
	defer si.mu.Unlock()
	return si.Session
}

// replaceSession atomically swaps in a new session instance.
//
// Refuses when the record has been deleted, returning deleted=true so the
// caller can close the instance it was about to install. This closes the
// TOCTOU window between a Resume finishing and a concurrent Delete.
//
// @param next the new session instance
// @returns the displaced instance (nil when none or when deleted), and whether
// the record was already deleted
func (si *SessionInfo) replaceSession(next types.Session) (old types.Session, deleted bool) {
	si.mu.Lock()
	defer si.mu.Unlock()
	if si.deleted {
		return nil, true
	}
	old = si.Session
	si.Session = next
	return old, false
}

// markDeleted flags the record as removed.
//
// Called by Store.RemoveSession before dropping the entry so an in-flight
// Resume observes the deletion even if it already resolved the record.
func (si *SessionInfo) markDeleted() {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.deleted = true
}

// SetStatus updates the session status and publishes a status event.
//
// @param status the new status
// @param store used to publish the event
func (si *SessionInfo) SetStatus(status SessionStatus, store *Store) {
	si.mu.Lock()
	si.Status = status
	si.mu.Unlock()
	store.publish(EventSessionStatus, map[string]any{
		"sessionId": si.ID,
		"status":    string(status),
	})
}

// Store holds in-memory state for the demo app.
type Store struct {
	mu       sync.RWMutex
	nodes    map[string]*NodeInfo
	sessions map[string]*SessionInfo

	// Global event subscribers; each /ws/events connection owns one channel.
	subMu sync.Mutex
	subs  map[chan Event]struct{}
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{
		nodes:    make(map[string]*NodeInfo),
		sessions: make(map[string]*SessionInfo),
		subs:     make(map[chan Event]struct{}),
	}
}

// AddNode registers a node and publishes a registration event.
//
// The stored fields mirror what the node sent in its Register message, so the
// console can show the running Tai binary version and platform without asking
// the node again.
//
// @param n the node that registered
func (s *Store) AddNode(n types.RemoteNode) {
	info := n.Info()
	s.mu.Lock()
	s.nodes[info.ID] = &NodeInfo{
		ID:           info.ID,
		TaiID:        info.TaiID,
		Version:      info.Version,
		OS:           info.OS,
		Arch:         info.Arch,
		Capabilities: info.Capabilities,
		RegisteredAt: time.Now(),
	}
	s.mu.Unlock()
	s.publish(EventNodeRegistered, map[string]any{"nodeId": info.ID})
}

// RemoveNode removes a node and publishes a disconnect event.
//
// @param nodeID the node identifier
func (s *Store) RemoveNode(nodeID string) {
	s.mu.Lock()
	delete(s.nodes, nodeID)
	s.mu.Unlock()
	s.publish(EventNodeDisconnected, map[string]any{"nodeId": nodeID})
}

// Nodes returns all registered nodes.
func (s *Store) Nodes() []*NodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*NodeInfo, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	return out
}

// AddSession stores a session.
func (s *Store) AddSession(info *SessionInfo) {
	s.mu.Lock()
	s.sessions[info.ID] = info
	s.mu.Unlock()
}

// GetSession looks up a session by ID.
func (s *Store) GetSession(id string) (*SessionInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	si, ok := s.sessions[id]
	return si, ok
}

// RemoveSession deletes a session entry and publishes a closed event.
//
// The record is flagged deleted before it leaves the map so an in-flight
// Resume that already holds the pointer still sees the deletion.
//
// @param id the session identifier
func (s *Store) RemoveSession(id string) {
	s.mu.Lock()
	info, ok := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if ok {
		info.markDeleted()
	}
	s.publish(EventSessionClosed, map[string]any{"sessionId": id})
}

// Sessions returns all sessions.
func (s *Store) Sessions() []*SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*SessionInfo, 0, len(s.sessions))
	for _, si := range s.sessions {
		out = append(out, si)
	}
	return out
}

// SubscribeEvents returns a channel that receives global events.
//
// Called once per /ws/events connection. The channel is buffered; a slow
// consumer has events dropped by publish rather than blocking the broadcast.
//
// @returns the event channel; the caller must call UnsubscribeEvents on disconnect
func (s *Store) SubscribeEvents() chan Event {
	ch := make(chan Event, 64)
	s.subMu.Lock()
	s.subs[ch] = struct{}{}
	s.subMu.Unlock()
	return ch
}

// UnsubscribeEvents removes a subscriber and closes its channel.
//
// The close happens under subMu: publish reads the subscriber map under the
// same lock, so closing here guarantees no send is in flight to a closed
// channel. Closing outside the lock would leave a window where publish still
// holds the deleted channel and panics on send.
//
// @param ch the channel returned by SubscribeEvents
func (s *Store) UnsubscribeEvents(ch chan Event) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if _, ok := s.subs[ch]; !ok {
		// Already unsubscribed; closing twice would panic.
		return
	}
	delete(s.subs, ch)
	close(ch)
}

// publish broadcasts one event to every subscriber.
//
// Non-blocking: when a channel is full that subscriber misses this event so a
// slow consumer cannot stall the others. Global events are state
// notifications; clients re-read a snapshot after reconnecting, so dropping
// one or two does not affect eventual consistency.
//
// @param typ the event type
// @param data the event payload
func (s *Store) publish(typ string, data map[string]any) {
	ev := newEvent(typ, data)
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// EnsureWorkspace creates a minimal tai workspace structure at the given path
// if it doesn't already exist. This is for local QA testing where the demo app
// and tai node share the same filesystem.
func EnsureWorkspace(dir string) error {
	taiDir := filepath.Join(dir, ".tai-workspace")
	metaPath := filepath.Join(taiDir, "workspace.json")
	if _, err := os.Stat(metaPath); err == nil {
		return nil
	}

	dirs := []string{
		filepath.Join(taiDir, "commits"),
		filepath.Join(taiDir, "branches"),
		filepath.Join(taiDir, "packs"),
		filepath.Join(taiDir, "cache"),
		filepath.Join(taiDir, "workdirs"),
		filepath.Join(taiDir, "locks"),
		filepath.Join(taiDir, "tmp"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	meta := map[string]any{
		"id":         "demo-" + time.Now().Format("20060102150405"),
		"name":       "demo",
		"version":    1,
		"visibility": "private",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		return err
	}

	branch := map[string]any{
		"name":       "main",
		"head":       "",
		"created_at": time.Now().Format(time.RFC3339),
	}
	branchData, _ := json.MarshalIndent(branch, "", "  ")
	return os.WriteFile(filepath.Join(taiDir, "branches", "main.json"), branchData, 0o644)
}
