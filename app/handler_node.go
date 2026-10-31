package main

import (
	"fmt"
	"net/http"
)

// NodeHandler serves node-related endpoints.
type NodeHandler struct {
	store *Store
}

// List returns all registered nodes.
func (h *NodeHandler) List(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.store.Nodes())
}

// Events is an SSE endpoint that streams node registration events.
func (h *NodeHandler) Events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ch := h.store.Subscribe()
	defer h.store.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
