package main

import (
	"net/http"
	"time"
)

// EventHandler serves the global event WebSocket endpoint.
//
// This differs from the /ws/sessions/{id}/* connections: those carry a single
// session's data plane (ctrl, data, render, audio, motion), while this one is
// the control-plane broadcast shared by every page.
//
// The design follows yao 1.0's /v1/events (yao/openapi/event/event.go): one
// message format {type, timestamp, data}, a 30 second heartbeat, and
// non-blocking delivery.
type EventHandler struct {
	store *Store
}

// NewEventHandler creates the global event handler.
//
// @param store event source
// @returns the handler
func NewEventHandler(store *Store) *EventHandler {
	return &EventHandler{store: store}
}

// heartbeatInterval matches yao 1.0 (30 seconds).
//
// The heartbeat keeps intermediary proxies from dropping an idle connection and
// lets the server notice dead clients (a failed write ends the loop).
const heartbeatInterval = 30 * time.Second

// WSEvents upgrades to WebSocket and pushes global events.
//
// Message format:
//
//	{"type":"node.registered","timestamp":1699999999999,"data":{"nodeId":"n1"}}
//	{"type":"ping"}                                    // heartbeat
//
// Clients ignore type=ping and dispatch everything else by type prefix.
func (h *EventHandler) WSEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := h.store.SubscribeEvents()
	defer h.store.UnsubscribeEvents(ch)

	// Reader goroutine: drain client messages (pongs) and report disconnects.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteJSON(map[string]any{"type": EventPing}); err != nil {
				return
			}
		case <-done:
			return
		case <-r.Context().Done():
			return
		}
	}
}
