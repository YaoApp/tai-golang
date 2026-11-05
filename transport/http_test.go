package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yaoapp/tai-golang/types"
)

// failAuth returns errors for ValidateNodeToken and RefreshNodeToken.
type failAuth struct{ testAuth }

func (a *failAuth) ValidateNodeToken(string) (*types.NodeInfo, error) {
	return nil, types.ErrSessionClosed
}
func (a *failAuth) RefreshNodeToken(string) (string, error) {
	return "", types.ErrSessionClosed
}

// ---------------------------------------------------------------------------
// handleTokenRefresh
// ---------------------------------------------------------------------------

func TestHandleTokenRefresh_Success(t *testing.T) {
	s := NewHTTPServer(&testAuth{})
	body := `{"node_token":"old-tok"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/token/refresh", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "new-tok") {
		t.Errorf("body = %s, want new-tok", w.Body.String())
	}
}

func TestHandleTokenRefresh_BadJSON(t *testing.T) {
	s := NewHTTPServer(&testAuth{})
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/token/refresh", strings.NewReader("{bad"))
	w := httptest.NewRecorder()

	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleTokenRefresh_Unauthorized(t *testing.T) {
	s := NewHTTPServer(&failAuth{})
	body := `{"node_token":"bad-tok"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/token/refresh", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// ---------------------------------------------------------------------------
// handleHeartbeat
// ---------------------------------------------------------------------------

func TestHandleHeartbeat_Success(t *testing.T) {
	s := NewHTTPServer(&testAuth{})
	body := `{"tai_id":"t1","node_token":"tok","sessions":["s1"],"timestamp":1234567890}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/heartbeat", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandleHeartbeat_BadJSON(t *testing.T) {
	s := NewHTTPServer(&testAuth{})
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/heartbeat", strings.NewReader("{bad"))
	w := httptest.NewRecorder()

	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeat_Unauthorized(t *testing.T) {
	s := NewHTTPServer(&failAuth{})
	body := `{"tai_id":"t1","node_token":"bad","sessions":[],"timestamp":0}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/heartbeat", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// ---------------------------------------------------------------------------
// handleWellKnown
// ---------------------------------------------------------------------------

func TestHandleWellKnown(t *testing.T) {
	s := NewHTTPServer(&testAuth{})
	req := httptest.NewRequest(http.MethodGet, "/.well-known/yao-transport", nil)
	w := httptest.NewRecorder()

	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// ---------------------------------------------------------------------------
// ExtractBearer
// ---------------------------------------------------------------------------

func TestExtractBearer(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid", "Bearer abc123", "abc123"},
		{"no_prefix", "Token abc", ""},
		{"empty", "", ""},
		{"bearer_only", "Bearer ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			got := ExtractBearer(r)
			if got != tt.want {
				t.Errorf("ExtractBearer = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// HTTPError
// ---------------------------------------------------------------------------

func TestHTTPError(t *testing.T) {
	w := httptest.NewRecorder()
	HTTPError(w, http.StatusTeapot, "test error")

	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTeapot)
	}
	if !strings.Contains(w.Body.String(), "test error") {
		t.Errorf("body = %s, want test error", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// HTTPServer Start / Addr / Close
// ---------------------------------------------------------------------------

func TestHTTPServer_StartAndClose(t *testing.T) {
	s := NewHTTPServer(&testAuth{})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close(nil)

	addr := s.Addr()
	if addr == "" {
		t.Fatal("Addr empty after Start")
	}

	resp, err := http.Get("http://" + addr + "/.well-known/yao-transport")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHTTPServer_Addr_BeforeStart(t *testing.T) {
	s := NewHTTPServer(&testAuth{})
	if s.Addr() != "" {
		t.Errorf("Addr = %q, want empty before Start", s.Addr())
	}
}

func TestHTTPServer_Close_BeforeStart(t *testing.T) {
	s := NewHTTPServer(&testAuth{})
	if err := s.Close(nil); err != nil {
		t.Errorf("Close before Start: %v", err)
	}
}
