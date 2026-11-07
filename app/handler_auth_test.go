package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/types"
)

// fakeAuth is a controllable types.AuthProvider for handler tests. Only the
// credential-issuing methods matter here; the rest return inert values.
type fakeAuth struct {
	// mu guards the fields below: IssueUserToken is also called from SDK
	// background goroutines that re-issue credentials during auto-resume.
	mu sync.Mutex

	nodeToken string
	nodeErr   error

	userToken string
	identity  *types.UserIdentity
	userErr   error

	// Recorded from IssueUserToken calls, in call order.
	gotNodeID  string
	gotScopes  types.Scope
	scopeCalls []types.Scope
}

// newFakeAuth returns a provider that successfully issues both credential kinds.
func newFakeAuth() *fakeAuth {
	return &fakeAuth{
		nodeToken: "node-token",
		userToken: "user-token",
		identity:  &types.UserIdentity{UserID: "user-1", TeamID: "team-1"},
	}
}

func (f *fakeAuth) ValidateNodeToken(string) (*types.NodeInfo, error) {
	return &types.NodeInfo{NodeID: "node-1"}, nil
}

func (f *fakeAuth) IssueNodeToken(string) (string, error) { return f.nodeToken, f.nodeErr }

func (f *fakeAuth) RefreshNodeToken(string) (string, error) { return "", errors.New("not implemented") }

func (f *fakeAuth) IssueOperationToken(string, string, []string, *types.TokenScope) (string, error) {
	return "", errors.New("retired")
}

func (f *fakeAuth) IssueUserToken(nodeID string, scopes types.Scope) (string, *types.UserIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotNodeID = nodeID
	f.gotScopes = scopes
	f.scopeCalls = append(f.scopeCalls, scopes)
	return f.userToken, f.identity, f.userErr
}

// calls snapshots the recorded scope sets.
func (f *fakeAuth) calls() []types.Scope {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]types.Scope(nil), f.scopeCalls...)
}

// recordCall appends one recorded scope set.
func (f *fakeAuth) recordCall(scopes types.Scope) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopeCalls = append(f.scopeCalls, scopes)
}

func (f *fakeAuth) RefreshUserToken(string) (string, error) { return "", errors.New("not implemented") }

func (f *fakeAuth) ValidateUserToken(string) (*types.UserIdentity, error) {
	return f.identity, nil
}

func (f *fakeAuth) SigningPublicKey() (string, []byte) { return "key-1", []byte("public-key") }

func (f *fakeAuth) WellKnownInfo() *types.WellKnownResponse {
	return &types.WellKnownResponse{
		TransportPort: 6099,
		Protocols:     []string{"HTTP/2"},
		Version:       "test-version",
	}
}

// userTokenResponse is the wire shape of POST /api/auth/user-token.
type userTokenResponse struct {
	Token    string `json:"token"`
	Identity struct {
		UserID string   `json:"user_id"`
		TeamID string   `json:"team_id"`
		Scope  []string `json:"scope"`
	} `json:"identity"`
}

// postAuthHandler runs one handler with a JSON body.
func postAuthHandler(t *testing.T, handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/user-token", strings.NewReader(body))
	// The handler requires a logged-in account; provider-level behavior is what
	// these tests exercise, so inject the context the middleware would add.
	req = withTestUser(req)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// decodeUserToken decodes a successful user-token response.
func decodeUserToken(t *testing.T, rec *httptest.ResponseRecorder) userTokenResponse {
	t.Helper()
	var out userTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

// TestIssueUserTokenSuccess covers the happy path: token plus resolved identity
// and the scopes echoed back in identity.scope.
func TestIssueUserTokenSuccess(t *testing.T) {
	provider := newFakeAuth()
	h := &AuthHandler{auth: provider}

	rec := postAuthHandler(t, h.IssueUserToken,
		`{"nodeId":"node-1","scopes":["session:access","workspace:read"]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	out := decodeUserToken(t, rec)
	if out.Token != "user-token" {
		t.Errorf("token = %q, want user-token", out.Token)
	}
	if out.Identity.UserID != "user-1" || out.Identity.TeamID != "team-1" {
		t.Errorf("identity = %+v, want user-1/team-1", out.Identity)
	}
	if len(out.Identity.Scope) != 2 || out.Identity.Scope[0] != "session:access" || out.Identity.Scope[1] != "workspace:read" {
		t.Errorf("identity.scope = %v, want the requested scopes", out.Identity.Scope)
	}
	if provider.gotNodeID != "node-1" {
		t.Errorf("provider nodeID = %q, want node-1", provider.gotNodeID)
	}
	if !provider.gotScopes.Has(types.SessionAccess) || !provider.gotScopes.Has(types.WorkspaceRead) {
		t.Errorf("provider scopes = %v, want session:access + workspace:read", provider.gotScopes)
	}
}

// TestIssueUserTokenDefaultScopes verifies that omitting scopes grants the
// minimum set (session:access) rather than an empty credential.
func TestIssueUserTokenDefaultScopes(t *testing.T) {
	provider := newFakeAuth()
	h := &AuthHandler{auth: provider}

	rec := postAuthHandler(t, h.IssueUserToken, `{"nodeId":"node-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if len(provider.gotScopes) != 1 || provider.gotScopes[0] != types.SessionAccess {
		t.Errorf("provider scopes = %v, want [session:access]", provider.gotScopes)
	}
	out := decodeUserToken(t, rec)
	if len(out.Identity.Scope) != 1 || out.Identity.Scope[0] != types.SessionAccess {
		t.Errorf("identity.scope = %v, want [session:access]", out.Identity.Scope)
	}
}

// TestIssueUserTokenIdentityScopeWins verifies that a provider which narrows
// the scope set has its version reported, not the requested one.
func TestIssueUserTokenIdentityScopeWins(t *testing.T) {
	provider := newFakeAuth()
	provider.identity = &types.UserIdentity{
		UserID: "user-2",
		TeamID: "team-2",
		Scope:  types.Scope{types.WorkspaceAll},
	}
	h := &AuthHandler{auth: provider}

	rec := postAuthHandler(t, h.IssueUserToken,
		`{"nodeId":"node-1","scopes":["session:access","workspace:write"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	out := decodeUserToken(t, rec)
	if len(out.Identity.Scope) != 1 || out.Identity.Scope[0] != types.WorkspaceAll {
		t.Errorf("identity.scope = %v, want [workspace:*] from the provider", out.Identity.Scope)
	}
}

// TestIssueUserTokenBadRequests covers malformed and incomplete bodies.
func TestIssueUserTokenBadRequests(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{"nodeId":`},
		{name: "empty body", body: ``},
		{name: "missing nodeId", body: `{"scopes":["session:access"]}`},
		{name: "empty nodeId", body: `{"nodeId":"","scopes":["session:access"]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &AuthHandler{auth: newFakeAuth()}
			rec := postAuthHandler(t, h.IssueUserToken, c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			var out map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["error"] == "" {
				t.Errorf("error body = %q, want {\"error\": ...}", rec.Body.String())
			}
		})
	}
}

// TestIssueUserTokenProviderError verifies a signing/internal failure maps to 500.
func TestIssueUserTokenProviderError(t *testing.T) {
	provider := newFakeAuth()
	provider.userErr = errors.New("sign: private key unavailable")
	h := &AuthHandler{auth: provider}

	rec := postAuthHandler(t, h.IssueUserToken, `{"nodeId":"node-1"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "private key unavailable") {
		t.Errorf("body = %q, want provider error surfaced", rec.Body.String())
	}
}

// TestIssueUserTokenUnauthorized verifies that a provider reporting no business
// context maps to 401 rather than an internal error.
func TestIssueUserTokenUnauthorized(t *testing.T) {
	provider := newFakeAuth()
	provider.userErr = auth.ErrUnauthorized
	h := &AuthHandler{auth: provider}

	rec := postAuthHandler(t, h.IssueUserToken, `{"nodeId":"node-1"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestIssueUserTokenNilIdentity verifies a provider that returns no identity is
// treated as unauthenticated, not as a successful issuance.
func TestIssueUserTokenNilIdentity(t *testing.T) {
	provider := newFakeAuth()
	provider.identity = nil
	h := &AuthHandler{auth: provider}

	rec := postAuthHandler(t, h.IssueUserToken, `{"nodeId":"node-1"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "identity not resolved") {
		t.Errorf("body = %q, want identity error", rec.Body.String())
	}
}

// TestIssueNodeTokenHandler covers the retained v1 node-token endpoint.
func TestIssueNodeTokenHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := &AuthHandler{auth: newFakeAuth()}
		rec := postAuthHandler(t, h.IssueNodeToken, `{"nodeId":"node-1"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var out map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out["token"] != "node-token" {
			t.Errorf("token = %q, want node-token", out["token"])
		}
	})

	t.Run("missing nodeId", func(t *testing.T) {
		h := &AuthHandler{auth: newFakeAuth()}
		rec := postAuthHandler(t, h.IssueNodeToken, `{"nodeId":""}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("issuance failure", func(t *testing.T) {
		provider := newFakeAuth()
		provider.nodeErr = errors.New("sign failed")
		h := &AuthHandler{auth: provider}
		rec := postAuthHandler(t, h.IssueNodeToken, `{"nodeId":"node-1"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// TestAuthInfoHandler checks the well-known info projection.
func TestAuthInfoHandler(t *testing.T) {
	h := &AuthHandler{auth: newFakeAuth()}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/info", nil)
	rec := httptest.NewRecorder()
	h.Info(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"transportPort", "protocols", "version", "publicKeyId", "publicKey"} {
		if _, ok := out[key]; !ok {
			t.Errorf("response missing %q", key)
		}
	}
}

// TestUserTokenRouteWired verifies the mux exposes user-token and no longer
// serves the retired operation-token route.
// ctxAwareFakeAuth resolves the account from the request context like the real
// default AuthProvider does.
type ctxAwareFakeAuth struct{ *fakeAuth }

func (a ctxAwareFakeAuth) IssueUserTokenContext(ctx context.Context, nodeID string, scopes types.Scope) (string, *types.UserIdentity, error) {
	if user, ok := demoUserFromContext(ctx); ok {
		return "ctx-token", &types.UserIdentity{UserID: user.Email, TeamID: DemoTeamID, Scope: scopes}, nil
	}
	return a.fakeAuth.IssueUserToken(nodeID, scopes)
}

func TestUserTokenRouteWired(t *testing.T) {
	users := NewUserStore()
	mux := NewAPI(ctxAwareFakeAuth{newFakeAuth()}, NewStore(), users)

	// The endpoint is authenticated: anonymous callers get 401.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/user-token",
		strings.NewReader(`{"nodeId":"node-1"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous user-token status = %d, want 401", rec.Code)
	}

	// Register (auto-login) and retry with the session cookie.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/register",
		strings.NewReader(`{"email":"alice@example.com","password":"secret123","name":"Alice"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("register did not set a session cookie")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/user-token",
		strings.NewReader(`{"nodeId":"node-1","scopes":["session:access"]}`))
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/auth/user-token status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var tokenOut struct {
		Identity struct {
			UserID string `json:"user_id"`
			TeamID string `json:"team_id"`
		} `json:"identity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tokenOut); err != nil {
		t.Fatalf("decode user-token: %v", err)
	}
	if tokenOut.Identity.UserID != "alice@example.com" || tokenOut.Identity.TeamID != DemoTeamID {
		t.Errorf("identity = %+v, want alice@example.com/%s", tokenOut.Identity, DemoTeamID)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("user-token Content-Type = %q, want JSON", ct)
	}

	// The SPA fallback owns every unmatched path, so a retired API route is
	// observable as an HTML response rather than a JSON credential or error.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/operation-token",
		strings.NewReader(`{"nodeId":"node-1","sessionId":"sess-1"}`)))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("operation-token Content-Type = %q, want SPA HTML (route retired)", ct)
	}
	if strings.Contains(rec.Body.String(), `"token"`) {
		t.Error("operation-token still returns a token")
	}

	// node-token remains available.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/node-token",
		strings.NewReader(`{"nodeId":"node-1"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/auth/node-token status = %d, want 200", rec.Code)
	}
	var nodeOut map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &nodeOut); err != nil || nodeOut["token"] != "node-token" {
		t.Errorf("node-token response = %q, want JSON token", rec.Body.String())
	}
}
