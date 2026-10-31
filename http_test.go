package tai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/mock"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
)

func TestWellKnownEndpoint(t *testing.T) {
	ts := mock.NewTestServer(t)

	resp, err := http.Get(ts.HTTPURL() + "/.well-known/yao-transport")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	var info types.WellKnownResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if info.TransportPort <= 0 {
		t.Errorf("TransportPort=%d, want >0", info.TransportPort)
	}
	if info.Version == "" {
		t.Error("Version should not be empty")
	}
}

func TestConnectNotServedOnHTTPPort(t *testing.T) {
	ts := mock.NewTestServer(t)

	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/connect", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Errorf("connect must not be served on the HTTP API port, got %d", resp.StatusCode)
	}
}

func TestTokenRefreshEndpoint(t *testing.T) {
	ts := mock.NewTestServer(t)
	oldToken := ts.IssueTestNodeToken("refresh-node")

	body, _ := json.Marshal(transport.RefreshRequest{NodeToken: oldToken})
	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/token/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	var rr transport.RefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if rr.NodeToken == "" {
		t.Error("refreshed token should not be empty")
	}
	if rr.NodeToken == oldToken {
		t.Error("refreshed token should differ from old")
	}
}

func TestTokenRefreshEndpoint_InvalidToken(t *testing.T) {
	ts := mock.NewTestServer(t)

	body, _ := json.Marshal(transport.RefreshRequest{NodeToken: "bad-token"})
	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/token/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}
}

func TestTokenRefreshEndpoint_BadBody(t *testing.T) {
	ts := mock.NewTestServer(t)

	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/token/refresh", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHeartbeatEndpoint(t *testing.T) {
	ts := mock.NewTestServer(t)
	token := ts.IssueTestNodeToken("hb-node")

	body, _ := json.Marshal(transport.HeartbeatRequest{
		TaiID:     "hb-node",
		NodeToken: token,
		Timestamp: 1000000,
	})
	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", resp.StatusCode)
	}
}

func TestHeartbeatEndpoint_InvalidToken(t *testing.T) {
	ts := mock.NewTestServer(t)

	body, _ := json.Marshal(transport.HeartbeatRequest{
		TaiID:     "hb-node",
		NodeToken: "bad",
		Timestamp: 1000000,
	})
	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}
}

func TestHeartbeatEndpoint_BadBody(t *testing.T) {
	ts := mock.NewTestServer(t)

	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/heartbeat", "application/json", strings.NewReader("{{"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHTTPServerAddrBeforeStart(t *testing.T) {
	a := testAuth(t)
	s := transport.NewHTTPServer(a)
	if s.Addr() != "" {
		t.Errorf("addr should be empty before start, got %q", s.Addr())
	}
}

func TestHTTPListenAndServe(t *testing.T) {
	a := testAuth(t)
	s := transport.NewHTTPServer(a)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe("127.0.0.1:0")
	}()

	for i := 0; i < 50; i++ {
		if s.Addr() != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.Addr() == "" {
		t.Fatal("server did not start")
	}

	resp, err := http.Get("http://" + s.Addr() + "/.well-known/yao-transport")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d", resp.StatusCode)
	}

	s.Close(context.Background())

	if err := <-errCh; err != nil && err != http.ErrServerClosed {
		t.Errorf("listenAndServe: %v", err)
	}
}

func TestHTTPServerCloseNil(t *testing.T) {
	a := testAuth(t)
	s := transport.NewHTTPServer(a)
	if err := s.Close(context.Background()); err != nil {
		t.Errorf("close nil: %v", err)
	}
}

func TestExtractBearer(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", ""},
		{"Basic abc123", ""},
		{"", ""},
		{"Bearer ", ""},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest("GET", "/", nil)
		if tt.header != "" {
			req.Header.Set("Authorization", tt.header)
		}
		got := transport.ExtractBearer(req)
		if got != tt.want {
			t.Errorf("extractBearer(%q)=%q, want %q", tt.header, got, tt.want)
		}
	}
}
