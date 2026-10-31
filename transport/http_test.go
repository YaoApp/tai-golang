package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yaoapp/tai-golang/auth"
)

func TestExtractBearerValid(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	tok := ExtractBearer(r)
	if tok != "abc123" {
		t.Errorf("got %q", tok)
	}
}

func TestExtractBearerInvalid(t *testing.T) {
	for _, hdr := range []string{"", "Basic abc", "bearer abc", "BearerNoSpace"} {
		r := httptest.NewRequest("GET", "/", nil)
		if hdr != "" {
			r.Header.Set("Authorization", hdr)
		}
		if tok := ExtractBearer(r); tok != "" {
			t.Errorf("ExtractBearer(%q) = %q, want empty", hdr, tok)
		}
	}
}

func TestHTTPErrorJSON(t *testing.T) {
	w := httptest.NewRecorder()
	HTTPError(w, http.StatusBadRequest, "bad input")
	if w.Code != 400 {
		t.Errorf("code=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad input") {
		t.Errorf("body=%s", w.Body.String())
	}
}

func TestHTTPServerStartAndWellKnown(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	hs := NewHTTPServer(a)
	if err := hs.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer hs.Close(context.Background())

	addr := hs.Addr()
	if addr == "" {
		t.Fatal("addr empty")
	}

	resp, err := http.Get("http://" + addr + "/.well-known/yao-transport")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status=%d", resp.StatusCode)
	}
}
