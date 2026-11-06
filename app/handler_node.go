package main

import "net/http"

// NodeHandler serves node-related endpoints.
type NodeHandler struct {
	store *Store
}

// List returns all registered nodes.
//
// Node registration changes are announced on the global event stream (the
// node.* types on /ws/events); there is no page-specific SSE endpoint.
func (h *NodeHandler) List(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.store.Nodes())
}
