package main

import (
	"encoding/json"
	"github.com/yaoapp/tai-golang/types"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAuthTestMux() (*UserStore, http.Handler) {
	users := NewUserStore()
	return users, NewAPI(ctxAwareFakeAuth{newFakeAuth()}, NewStore(), users)
}

func doJSON(t *testing.T, mux http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRegisterLoginMeLogout(t *testing.T) {
	_, mux := newAuthTestMux()

	// Register auto-logs-in.
	rec := doJSON(t, mux, http.MethodPost, "/api/auth/register",
		`{"email":"alice@example.com","password":"secret123","name":"Alice"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("register did not set a cookie")
	}
	var regOut struct {
		User struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &regOut); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	if regOut.User.Email != "alice@example.com" || regOut.User.Name != "Alice" {
		t.Errorf("user = %+v", regOut.User)
	}

	// Me works with the cookie.
	if rec := doJSON(t, mux, http.MethodGet, "/api/auth/me", "", cookies[0]); rec.Code != http.StatusOK {
		t.Fatalf("me status = %d", rec.Code)
	}

	// Logout invalidates the cookie.
	if rec := doJSON(t, mux, http.MethodPost, "/api/auth/logout", "", cookies[0]); rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d", rec.Code)
	}
	if rec := doJSON(t, mux, http.MethodGet, "/api/auth/me", "", cookies[0]); rec.Code != http.StatusUnauthorized {
		t.Errorf("me after logout status = %d, want 401", rec.Code)
	}

	// Login again.
	rec = doJSON(t, mux, http.MethodPost, "/api/auth/login",
		`{"email":"alice@example.com","password":"secret123"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body: %s", rec.Code, rec.Body.String())
	}
	loginCookies := rec.Result().Cookies()
	if len(loginCookies) == 0 {
		t.Fatal("login did not set a cookie")
	}
	rec = doJSON(t, mux, http.MethodGet, "/api/auth/me", "", loginCookies[0])
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alice@example.com") {
		t.Fatalf("me status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRegisterValidation(t *testing.T) {
	_, mux := newAuthTestMux()

	cases := []struct {
		name string
		body string
		want int
	}{
		{"invalid email", `{"email":"nope","password":"secret123"}`, http.StatusBadRequest},
		{"weak password", `{"email":"bob@example.com","password":"short"}`, http.StatusBadRequest},
		{"duplicate", `{"email":"dup@example.com","password":"secret123"}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, mux, http.MethodPost, "/api/auth/register", tc.body, nil)
			if tc.want != 0 {
				if rec.Code != tc.want {
					t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
				}
				return
			}
			// duplicate: first call succeeds, second conflicts
			if rec.Code != http.StatusCreated {
				t.Fatalf("first register status = %d", rec.Code)
			}
			rec = doJSON(t, mux, http.MethodPost, "/api/auth/register", tc.body, nil)
			if rec.Code != http.StatusConflict {
				t.Errorf("duplicate status = %d, want 409", rec.Code)
			}
		})
	}
}

func TestLoginWrongPassword(t *testing.T) {
	_, mux := newAuthTestMux()
	doJSON(t, mux, http.MethodPost, "/api/auth/register",
		`{"email":"carol@example.com","password":"secret123"}`, nil)

	rec := doJSON(t, mux, http.MethodPost, "/api/auth/login",
		`{"email":"carol@example.com","password":"wrong-password"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestProtectedEndpointsRequireLogin(t *testing.T) {
	_, mux := newAuthTestMux()
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/auth/me", ""},
		{http.MethodPost, "/api/auth/user-token", `{"nodeId":"node-1"}`},
		{http.MethodPost, "/api/sessions", `{"nodeId":"node-1"}`},
		{http.MethodGet, "/api/sessions", ""},
		{http.MethodGet, "/api/workspaces", ""},
	} {
		rec := doJSON(t, mux, tc.method, tc.path, tc.body, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestAuthHandlerBadRequests(t *testing.T) {
	_, mux := newAuthTestMux()

	if rec := doJSON(t, mux, http.MethodPost, "/api/auth/register", `{"email":`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("register bad body status = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, mux, http.MethodPost, "/api/auth/login", `{"email":`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("login bad body status = %d, want 400", rec.Code)
	}
	// Logout without a cookie is a no-op success.
	if rec := doJSON(t, mux, http.MethodPost, "/api/auth/logout", "", nil); rec.Code != http.StatusOK {
		t.Errorf("anonymous logout status = %d, want 200", rec.Code)
	}
}

// TestUserTokenFallbackProvider covers providers that do not implement the
// optional context-aware interface.
func TestUserTokenFallbackProvider(t *testing.T) {
	users := NewUserStore()
	mux := NewAPI(newFakeAuth(), NewStore(), users)

	rec := doJSON(t, mux, http.MethodPost, "/api/auth/register",
		`{"email":"dave@example.com","password":"secret123"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()

	rec = doJSON(t, mux, http.MethodPost, "/api/auth/user-token",
		`{"nodeId":"node-1","scopes":["session:access"]}`, cookies[0])
	if rec.Code != http.StatusOK {
		t.Fatalf("user-token status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "user-1") {
		t.Errorf("fallback provider identity missing: %s", rec.Body.String())
	}
}

func TestDefaultAccountLogin(t *testing.T) {
	users := NewUserStore()
	if err := users.SeedDefaultUser(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mux := NewAPI(ctxAwareFakeAuth{newFakeAuth()}, NewStore(), users)

	rec := doJSON(t, mux, http.MethodPost, "/api/auth/login",
		`{"email":"demo@example.com","password":"demo1234"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("default login status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("default login did not set a cookie")
	}

	// A session opened with the default account belongs to it.
	rec = doJSON(t, mux, http.MethodGet, "/api/auth/me", "", cookies[0])
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "demo@example.com") {
		t.Fatalf("me status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInjectRealtimeAPIKeys(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")

	mk := func(providerID, key string) *types.RealtimeRequest {
		return &types.RealtimeRequest{Providers: types.Providers{Primary: types.Provider{ProviderID: providerID, APIKey: key}}}
	}

	t.Run("missing openai key", func(t *testing.T) {
		err := injectRealtimeAPIKeys(mk("openai", ""))
		if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
			t.Fatalf("err = %v, want OPENAI_API_KEY hint", err)
		}
	})

	t.Run("key from env", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-test")
		req := mk("openai", "")
		if err := injectRealtimeAPIKeys(req); err != nil {
			t.Fatalf("inject: %v", err)
		}
		if req.Providers.Primary.APIKey != "sk-test" {
			t.Errorf("api key = %q", req.Providers.Primary.APIKey)
		}
	})

	t.Run("explicit key wins", func(t *testing.T) {
		req := mk("openai", "from-panel")
		if err := injectRealtimeAPIKeys(req); err != nil {
			t.Fatalf("inject: %v", err)
		}
		if req.Providers.Primary.APIKey != "from-panel" {
			t.Errorf("api key = %q", req.Providers.Primary.APIKey)
		}
	})

	t.Run("unknown provider untouched", func(t *testing.T) {
		req := mk("custom", "")
		if err := injectRealtimeAPIKeys(req); err != nil {
			t.Fatalf("inject: %v", err)
		}
	})

	t.Run("role key missing", func(t *testing.T) {
		req := &types.RealtimeRequest{Providers: types.Providers{
			Primary: types.Provider{ProviderID: "deepseek", APIKey: "k"},
			Roles:   map[string]types.Provider{"realtime": {ProviderID: "openai"}},
		}}
		err := injectRealtimeAPIKeys(req)
		if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
			t.Fatalf("err = %v, want role hint", err)
		}
	})

	t.Run("role key from env", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-role")
		req := &types.RealtimeRequest{Providers: types.Providers{
			Primary: types.Provider{ProviderID: "deepseek", APIKey: "k"},
			Roles:   map[string]types.Provider{"realtime": {ProviderID: "openai"}},
		}}
		if err := injectRealtimeAPIKeys(req); err != nil {
			t.Fatalf("inject: %v", err)
		}
		if req.Providers.Roles["realtime"].APIKey != "sk-role" {
			t.Errorf("role key = %q", req.Providers.Roles["realtime"].APIKey)
		}
	})
}
