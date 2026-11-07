package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newRequestWithCookie(name, value string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.AddCookie(&http.Cookie{Name: name, Value: value})
	return req
}

func newRequestNoCookie() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
}

func TestUserStoreRegister(t *testing.T) {
	store := NewUserStore()

	user, err := store.Register("Alice@Example.com ", "secret123", "Alice")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.Email != "alice@example.com" {
		t.Errorf("email = %q, want normalized", user.Email)
	}
	if _, ok := store.Get("ALICE@example.com"); !ok {
		t.Error("Get should normalize the lookup email")
	}

	for _, tc := range []struct {
		name     string
		email    string
		password string
		wantErr  error
	}{
		{"invalid email", "not-an-email", "secret123", ErrInvalidEmail},
		{"weak password", "b@example.com", "short", ErrWeakPassword},
		{"duplicate", "alice@example.com", "secret123", ErrEmailTaken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Register(tc.email, tc.password, "")
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUserStoreAuthenticate(t *testing.T) {
	store := NewUserStore()
	if _, err := store.Register("alice@example.com", "secret123", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := store.Authenticate("alice@example.com", "secret123"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := store.Authenticate("alice@example.com", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password err = %v", err)
	}
	if _, err := store.Authenticate("missing@example.com", "x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("unknown user err = %v", err)
	}
}

func TestUserStoreSessions(t *testing.T) {
	store := NewUserStore()
	now := time.Now()
	store.now = func() time.Time { return now }
	if _, err := store.Register("alice@example.com", "secret123", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, err := store.CreateSession("alice@example.com")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if user, ok := store.SessionUser(token); !ok || user.Email != "alice@example.com" {
		t.Fatalf("SessionUser = %+v/%v", user, ok)
	}
	if _, ok := store.SessionUser("unknown"); ok {
		t.Error("unknown token must not resolve")
	}
	if _, ok := store.SessionUser(""); ok {
		t.Error("empty token must not resolve")
	}

	// Expired sessions are removed lazily.
	now = now.Add(demoSessionTTL + time.Minute)
	if _, ok := store.SessionUser(token); ok {
		t.Error("expired session must not resolve")
	}

	token2, _ := store.CreateSession("alice@example.com")
	store.DeleteSession(token2)
	if _, ok := store.SessionUser(token2); ok {
		t.Error("deleted session must not resolve")
	}
}

func TestUserStoreRequestHelpers(t *testing.T) {
	store := NewUserStore()
	if _, err := store.Register("alice@example.com", "secret123", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	token, _ := store.CreateSession("alice@example.com")

	req := newRequestWithCookie(demoSessionCookie, token)
	user, ok := store.userFromRequest(req)
	if !ok || user.Email != "alice@example.com" {
		t.Fatalf("userFromRequest = %+v/%v", user, ok)
	}
	if _, ok := store.userFromRequest(newRequestWithCookie("other", "x")); ok {
		t.Error("wrong cookie name must not resolve")
	}

	handler := store.requireLogin(func(w http.ResponseWriter, r *http.Request) {
		u, ok := demoUserFromContext(r.Context())
		if !ok || u.Email != "alice@example.com" {
			t.Errorf("context user = %+v/%v", u, ok)
		}
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("authenticated status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler(rec, newRequestNoCookie())
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", rec.Code)
	}
}

func TestSeedDefaultUser(t *testing.T) {
	store := NewUserStore()
	if err := store.SeedDefaultUser(); err != nil {
		t.Fatalf("SeedDefaultUser: %v", err)
	}
	// Idempotent: a second seed keeps the original account.
	if err := store.SeedDefaultUser(); err != nil {
		t.Fatalf("second SeedDefaultUser: %v", err)
	}
	user, ok := store.Get(DefaultDemoEmail)
	if !ok || user.Email != DefaultDemoEmail {
		t.Fatalf("default user = %+v/%v", user, ok)
	}
	if _, err := store.Authenticate(DefaultDemoEmail, DefaultDemoPassword); err != nil {
		t.Fatalf("Authenticate default: %v", err)
	}
}
