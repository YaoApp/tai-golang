package tai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWellKnownEndpoint(t *testing.T) {
	ts := NewTestServer(t)

	resp, err := http.Get(ts.HTTPURL() + "/.well-known/yao-transport")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	var info WellKnownResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if info.TransportPort != 6099 {
		t.Errorf("TransportPort=%d, want 6099", info.TransportPort)
	}
	if info.Version == "" {
		t.Error("Version should not be empty")
	}
}

func TestConnectEndpoint(t *testing.T) {
	ts := NewTestServer(t)
	token := ts.IssueTestNodeToken("test-node-1")

	body, _ := json.Marshal(connectRequest{NodeID: "test-node-1", ConnectType: "control"})
	req, _ := http.NewRequest("POST", ts.HTTPURL()+"/v1/tai/connect", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d, body=%s", resp.StatusCode, bodyBytes)
	}

	var cr connectResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if cr.ConnectTicket == "" {
		t.Error("ConnectTicket should not be empty")
	}
	if cr.ExpiresIn != 30 {
		t.Errorf("ExpiresIn=%d, want 30", cr.ExpiresIn)
	}
	if len(cr.Protocols) == 0 {
		t.Error("Protocols should not be empty")
	}
}

func TestConnectEndpoint_DefaultConnectType(t *testing.T) {
	ts := NewTestServer(t)
	token := ts.IssueTestNodeToken("node-default")

	// Omit ConnectType; should default to "control"
	body, _ := json.Marshal(map[string]string{"node_id": "node-default"})
	req, _ := http.NewRequest("POST", ts.HTTPURL()+"/v1/tai/connect", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d, body=%s", resp.StatusCode, bodyBytes)
	}
}

func TestConnectEndpoint_NoAuth(t *testing.T) {
	ts := NewTestServer(t)

	body, _ := json.Marshal(connectRequest{NodeID: "n", ConnectType: "control"})
	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/connect", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}
}

func TestConnectEndpoint_InvalidToken(t *testing.T) {
	ts := NewTestServer(t)

	body, _ := json.Marshal(connectRequest{NodeID: "n", ConnectType: "control"})
	req, _ := http.NewRequest("POST", ts.HTTPURL()+"/v1/tai/connect", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer invalid-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}
}

func TestConnectEndpoint_BadBody(t *testing.T) {
	ts := NewTestServer(t)
	token := ts.IssueTestNodeToken("node-bad")

	req, _ := http.NewRequest("POST", ts.HTTPURL()+"/v1/tai/connect", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestTokenRefreshEndpoint(t *testing.T) {
	ts := NewTestServer(t)
	oldToken := ts.IssueTestNodeToken("refresh-node")

	body, _ := json.Marshal(refreshRequest{NodeToken: oldToken})
	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/token/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	var rr refreshResponse
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
	ts := NewTestServer(t)

	body, _ := json.Marshal(refreshRequest{NodeToken: "bad-token"})
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
	ts := NewTestServer(t)

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
	ts := NewTestServer(t)
	token := ts.IssueTestNodeToken("hb-node")

	body, _ := json.Marshal(heartbeatRequest{
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
	ts := NewTestServer(t)

	body, _ := json.Marshal(heartbeatRequest{
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
	ts := NewTestServer(t)

	resp, err := http.Post(ts.HTTPURL()+"/v1/tai/heartbeat", "application/json", strings.NewReader("{{"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
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
		got := extractBearer(req)
		if got != tt.want {
			t.Errorf("extractBearer(%q)=%q, want %q", tt.header, got, tt.want)
		}
	}
}
