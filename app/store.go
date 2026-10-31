package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	tai "github.com/yaoapp/tai-golang"
)

// NodeInfo is a snapshot of a registered node for the API.
type NodeInfo struct {
	ID           string    `json:"id"`
	TaiID        string    `json:"taiId"`
	RegisteredAt time.Time `json:"registeredAt"`
}

// SessionInfo tracks an active session for the API.
type SessionInfo struct {
	ID        string      `json:"id"`
	NodeID    string      `json:"nodeId"`
	CreatedAt time.Time   `json:"createdAt"`
	Status    string      `json:"status"`
	Session   tai.Session `json:"-"`
}

// Store holds in-memory state for the demo app.
type Store struct {
	mu       sync.RWMutex
	nodes    map[string]*NodeInfo
	sessions map[string]*SessionInfo

	// SSE subscribers for node events.
	subMu sync.Mutex
	subs  map[chan string]struct{}
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{
		nodes:    make(map[string]*NodeInfo),
		sessions: make(map[string]*SessionInfo),
		subs:     make(map[chan string]struct{}),
	}
}

// AddNode registers a node and broadcasts to SSE subscribers.
func (s *Store) AddNode(node *tai.Node) {
	s.mu.Lock()
	s.nodes[node.ID] = &NodeInfo{
		ID:           node.ID,
		TaiID:        node.TaiID,
		RegisteredAt: time.Now(),
	}
	s.mu.Unlock()
	s.broadcast(`{"event":"node_registered","nodeId":"` + node.ID + `"}`)
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

// RemoveSession deletes a session entry.
func (s *Store) RemoveSession(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
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

// Subscribe returns a channel that receives SSE event strings.
func (s *Store) Subscribe() chan string {
	ch := make(chan string, 16)
	s.subMu.Lock()
	s.subs[ch] = struct{}{}
	s.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (s *Store) Unsubscribe(ch chan string) {
	s.subMu.Lock()
	delete(s.subs, ch)
	s.subMu.Unlock()
	close(ch)
}

func (s *Store) broadcast(data string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- data:
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
