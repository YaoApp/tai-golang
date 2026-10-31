package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/yaoapp/tai-golang/types"
)

// AuthHandler serves authentication-related endpoints.
type AuthHandler struct {
	auth types.AuthProvider
}

// Info returns server information including public key and ports.
func (h *AuthHandler) Info(w http.ResponseWriter, r *http.Request) {
	wk := h.auth.WellKnownInfo()
	keyID, pubKey := h.auth.SigningPublicKey()
	writeJSON(w, map[string]any{
		"transportPort": wk.TransportPort,
		"protocols":     wk.Protocols,
		"version":       wk.Version,
		"publicKeyId":   keyID,
		"publicKey":     base64.StdEncoding.EncodeToString(pubKey),
	})
}

// IssueNodeToken generates a node token for the given node ID.
func (h *AuthHandler) IssueNodeToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID string `json:"nodeId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId is required")
		return
	}
	token, err := h.auth.IssueNodeToken(req.NodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"token": token})
}

// IssueOperationToken generates an operation token for a session.
func (h *AuthHandler) IssueOperationToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID    string   `json:"nodeId"`
		SessionID string   `json:"sessionId"`
		Ops       []string `json:"ops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NodeID == "" || req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "nodeId and sessionId are required")
		return
	}
	if len(req.Ops) == 0 {
		req.Ops = []string{"session"}
	}
	token, err := h.auth.IssueOperationToken(req.NodeID, req.SessionID, req.Ops, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"token": token})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
