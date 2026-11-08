package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/types"
)

// AuthHandler serves authentication-related endpoints.
type AuthHandler struct {
	auth  types.AuthProvider
	users *UserStore
}

// Info returns server information including public key and ports.
func (h *AuthHandler) Info(w http.ResponseWriter, r *http.Request) {
	wk := h.auth.WellKnownInfo()
	writeJSON(w, map[string]any{
		"transportPort": wk.TransportPort,
		"protocols":     wk.Protocols,
		"version":       wk.Version,
		"llmKeys": map[string]string{
			"DEEPSEEK_API_KEY": keyState("DEEPSEEK_API_KEY"),
			"OPENAI_API_KEY":   keyState("OPENAI_API_KEY"),
		},
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

// IssueUserToken issues a v2 user credential for one business operation.
//
// This is the demo counterpart of AuthProvider.IssueUserToken: the caller only
// declares the target node and the action scopes it needs, and the provider
// resolves the user from the server-side business context. Operation scopes are
// declared by the caller; omitting them grants the minimum set needed to open
// and drive a session.
//
// Request:  {"nodeId": "demo-node", "scopes": ["session:access", ...]}
// Response: {"token": "...", "identity": {"user_id": "...", "team_id": "...", "scope": [...]}}
func (h *AuthHandler) IssueUserToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := demoUserFromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}

	var req struct {
		NodeID string   `json:"nodeId"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.NodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId is required")
		return
	}
	scopes := types.Scope(req.Scopes)
	if len(scopes) == 0 {
		scopes = types.DefaultSessionScopes()
	}

	token, identity, err := issueUserTokenContext(r.Context(), h.auth, req.NodeID, scopes)
	if err != nil {
		// A provider that cannot resolve a user from its business context
		// reports ErrUnauthorized; everything else is a signing/internal fault.
		code := http.StatusInternalServerError
		if errors.Is(err, auth.ErrUnauthorized) {
			code = http.StatusUnauthorized
		}
		writeError(w, code, err.Error())
		return
	}
	if identity == nil {
		writeError(w, http.StatusUnauthorized, "issue user token: identity not resolved")
		return
	}

	writeJSON(w, map[string]any{
		"token": token,
		"identity": map[string]any{
			"user_id": identity.UserID,
			"team_id": identity.TeamID,
			"scope":   identityScope(identity, scopes),
		},
	})
}

// identityScope returns the actions carried by the issued credential.
//
// The provider may fill UserIdentity.Scope when it resolves the user (for
// example a provider that narrows the requested scopes); otherwise the token
// carries exactly the requested set, so that is what we report back.
//
// @param identity the identity returned by the provider
// @param requested the scopes requested by the caller
// @returns the effective scope of the credential
func identityScope(identity *types.UserIdentity, requested types.Scope) types.Scope {
	if len(identity.Scope) > 0 {
		return identity.Scope
	}
	return requested
}

// Register creates a demo account and logs it in.
//
// Request:  {"email": "a@b.com", "password": "secret123", "name": "Alice"}
// Response: 201 {"user": {"email": "...", "name": "..."}}
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	user, err := h.users.Register(req.Email, req.Password, req.Name)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, ErrEmailTaken):
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	if err := h.startLogin(w, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"user": user})
}

// Login authenticates an existing demo account and sets the session cookie.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	user, err := h.users.Authenticate(req.Email, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err := h.startLogin(w, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"user": user})
}

// Logout clears the login session.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(demoSessionCookie); err == nil {
		h.users.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     demoSessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, map[string]bool{"ok": true})
}

// Me returns the currently logged-in account.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := h.users.userFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	writeJSON(w, map[string]any{"user": user})
}

// startLogin creates a login session and sets the cookie.
func (h *AuthHandler) startLogin(w http.ResponseWriter, user *DemoUser) error {
	token, err := h.users.CreateSession(user.Email)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     demoSessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(demoSessionTTL.Seconds()),
	})
	return nil
}

// issueUserTokenContext issues a credential through the optional context-aware
// interface so the provider sees the logged-in account.
func issueUserTokenContext(ctx context.Context, provider types.AuthProvider, nodeID string, scopes types.Scope) (string, *types.UserIdentity, error) {
	if ctp, ok := provider.(types.UserTokenContextProvider); ok {
		return ctp.IssueUserTokenContext(ctx, nodeID, scopes)
	}
	return provider.IssueUserToken(nodeID, scopes)
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
