package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// DemoTeamID is the team every demo account belongs to.
const DemoTeamID = "demo-team"

// Default demo account, seeded on startup so the console can be used without
// registration. These values are also prefilled in the login form.
const (
	DefaultDemoEmail    = "demo@example.com"
	DefaultDemoPassword = "demo1234"
	DefaultDemoName     = "Demo User"
)

// Session cookie for the demo login.
const (
	demoSessionCookie = "tai_demo_session"
	demoSessionTTL    = 24 * time.Hour
)

var (
	// ErrEmailTaken is returned when registering an existing email.
	ErrEmailTaken = errors.New("email already registered")
	// ErrInvalidEmail is returned for malformed addresses.
	ErrInvalidEmail = errors.New("invalid email")
	// ErrWeakPassword is returned when the password is shorter than the minimum.
	ErrWeakPassword = errors.New("password must be at least 8 characters")
	// ErrInvalidCredentials is returned for unknown email or wrong password.
	ErrInvalidCredentials = errors.New("invalid email or password")
)

// DemoUser is a demo account (email + password) used to attribute sessions to a
// real user identity during local end-to-end testing.
type DemoUser struct {
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"createdAt"`

	passwordHash []byte
}

type loginSession struct {
	email     string
	expiresAt time.Time
}

// UserStore keeps demo accounts and login sessions in memory.
type UserStore struct {
	mu       sync.RWMutex
	users    map[string]*DemoUser
	sessions map[string]loginSession
	now      func() time.Time
}

// NewUserStore creates an empty in-memory account store.
func NewUserStore() *UserStore {
	return &UserStore{
		users:    make(map[string]*DemoUser),
		sessions: make(map[string]loginSession),
		now:      time.Now,
	}
}

// normalizeEmail lowercases and trims an address.
func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// Register creates a new demo account. The email is the user identity.
func (s *UserStore) Register(email, password, name string) (*DemoUser, error) {
	email = normalizeEmail(email)
	if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") {
		return nil, ErrInvalidEmail
	}
	if len(password) < 8 {
		return nil, ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.users[email]; exists {
		return nil, ErrEmailTaken
	}
	user := &DemoUser{
		Email:        email,
		Name:         strings.TrimSpace(name),
		CreatedAt:    s.now(),
		passwordHash: hash,
	}
	s.users[email] = user
	return user, nil
}

// SeedDefaultUser ensures the built-in demo account exists. It is idempotent:
// calling it twice keeps the first account (and its password).
func (s *UserStore) SeedDefaultUser() error {
	_, err := s.Register(DefaultDemoEmail, DefaultDemoPassword, DefaultDemoName)
	if errors.Is(err, ErrEmailTaken) {
		return nil
	}
	return err
}

// Authenticate verifies email/password and returns the account on success.
func (s *UserStore) Authenticate(email, password string) (*DemoUser, error) {
	email = normalizeEmail(email)
	s.mu.RLock()
	user, ok := s.users[email]
	s.mu.RUnlock()
	if !ok {
		// Constant-time-ish: still run a comparison to avoid trivial user
		// enumeration timing differences.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5B0G1Z3a1nJ7X6o1F7hF1Dt1iJ0fK"), []byte(password))
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword(user.passwordHash, []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return user, nil
}

// Get returns an account by email.
func (s *UserStore) Get(email string) (*DemoUser, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[normalizeEmail(email)]
	return user, ok
}

// CreateSession issues an opaque login token for the account.
func (s *UserStore) CreateSession(email string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	s.mu.Lock()
	s.sessions[token] = loginSession{email: normalizeEmail(email), expiresAt: s.now().Add(demoSessionTTL)}
	s.mu.Unlock()
	return token, nil
}

// SessionUser resolves a login token to an account.
func (s *UserStore) SessionUser(token string) (*DemoUser, bool) {
	if token == "" {
		return nil, false
	}
	s.mu.Lock()
	sess, ok := s.sessions[token]
	if !ok {
		s.mu.Unlock()
		return nil, false
	}
	if s.now().After(sess.expiresAt) {
		delete(s.sessions, token)
		s.mu.Unlock()
		return nil, false
	}
	s.mu.Unlock()
	return s.Get(sess.email)
}

// userFromRequest resolves the logged-in account from the request cookie.
func (s *UserStore) userFromRequest(r *http.Request) (*DemoUser, bool) {
	cookie, err := r.Cookie(demoSessionCookie)
	if err != nil {
		return nil, false
	}
	return s.SessionUser(cookie.Value)
}

// requireLogin wraps a handler so only logged-in accounts can reach it. The
// account is placed in the request context; the session open path then passes
// it to AuthProvider.IssueUserTokenContext.
func (s *UserStore) requireLogin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.userFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		h(w, r.WithContext(withDemoUser(r.Context(), user)))
	}
}

// protect enforces login for the demo's authenticated API surface and injects
// the account into the request context.
func (s *UserStore) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !needsLogin(r) {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := s.userFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		next.ServeHTTP(w, r.WithContext(withDemoUser(r.Context(), user)))
	})
}

// needsLogin reports which API paths require a logged-in demo account.
func needsLogin(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return false
	}
	p := r.URL.Path
	switch {
	case p == "/api/auth/user-token":
		return true
	case strings.HasPrefix(p, "/api/sessions"):
		return true
	case strings.HasPrefix(p, "/api/workspaces"):
		return true
	}
	return false
}

// DeleteSession invalidates a login token.
func (s *UserStore) DeleteSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}
