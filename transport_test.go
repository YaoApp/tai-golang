package tai

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestTransportListensAndAccepts(t *testing.T) {
	ts := NewTestServer(t)

	if ts.TransportAddr == "" {
		t.Fatal("transport address should not be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+ts.TransportAddr+"/v1/tai/control", nil)
	req.Header.Set("Authorization", "Bearer fake-ticket")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401 for invalid ticket", resp.StatusCode)
	}
}

func TestTransportRejectsNoAuth(t *testing.T) {
	ts := NewTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+ts.TransportAddr+"/v1/tai/control", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}
}

func TestTransportControlWithValidTicket(t *testing.T) {
	ts := NewTestServer(t)

	ticket, err := ts.Auth.IssueConnectTicket("node-ctrl-1", "control")
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+ts.TransportAddr+"/v1/tai/control", nil)
	req.Header.Set("Authorization", "Bearer "+ticket.Ticket)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", resp.StatusCode)
	}
}

func TestTransportTicketReplay(t *testing.T) {
	ts := NewTestServer(t)

	ticket, err := ts.Auth.IssueConnectTicket("node-replay", "control")
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}

	// First use
	ctx1, cancel1 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel1()
	req1, _ := http.NewRequestWithContext(ctx1, "POST", "http://"+ts.TransportAddr+"/v1/tai/control", nil)
	req1.Header.Set("Authorization", "Bearer "+ticket.Ticket)

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first use: status=%d, want 200", resp1.StatusCode)
	}

	// Replay should be rejected
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	req2, _ := http.NewRequestWithContext(ctx2, "POST", "http://"+ts.TransportAddr+"/v1/tai/control", nil)
	req2.Header.Set("Authorization", "Bearer "+ticket.Ticket)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("replay: status=%d, want 401", resp2.StatusCode)
	}
}

func TestTransportSessionControl(t *testing.T) {
	ts := NewTestServer(t)

	ticket, err := ts.Auth.IssueConnectTicket("node-sess-ctrl", "light")
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+ts.TransportAddr+"/v1/tai/session/sess-001/control", nil)
	req.Header.Set("Authorization", "Bearer "+ticket.Ticket)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", resp.StatusCode)
	}
}

func TestTransportSessionData(t *testing.T) {
	ts := NewTestServer(t)

	ticket, err := ts.Auth.IssueConnectTicket("node-sess-data", "light")
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+ts.TransportAddr+"/v1/tai/session/sess-002/data", nil)
	req.Header.Set("Authorization", "Bearer "+ticket.Ticket)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", resp.StatusCode)
	}
}

func TestTransportSessionControl_NoAuth(t *testing.T) {
	ts := NewTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+ts.TransportAddr+"/v1/tai/session/s1/control", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}
}

func TestTransportAddr_BeforeStart(t *testing.T) {
	auth, _ := newTestAuth(t)
	ts := newTransportServer(auth)
	if ts.addr() != "" {
		t.Errorf("addr before start should be empty, got %q", ts.addr())
	}
}

func TestTransportClose_BeforeStart(t *testing.T) {
	auth, _ := newTestAuth(t)
	ts := newTransportServer(auth)
	if err := ts.close(context.Background()); err != nil {
		t.Errorf("close before start should not error: %v", err)
	}
}

func TestHTTPAddr_BeforeStart(t *testing.T) {
	auth, _ := newTestAuth(t)
	hs := newHTTPServer(auth)
	if hs.addr() != "" {
		t.Errorf("addr before start should be empty, got %q", hs.addr())
	}
}

func TestHTTPClose_BeforeStart(t *testing.T) {
	auth, _ := newTestAuth(t)
	hs := newHTTPServer(auth)
	if err := hs.close(context.Background()); err != nil {
		t.Errorf("close before start should not error: %v", err)
	}
}
