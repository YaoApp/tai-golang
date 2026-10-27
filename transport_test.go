package tai

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// Transport auth middleware tests — H2 and H3
// ---------------------------------------------------------------------------

func TestTransportNoTicket(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST",
				tc.url+"/v1/tai/control", nil)
			resp, err := tc.newClient().Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status=%d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestTransportInvalidTicket(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST",
				tc.url+"/v1/tai/control", nil)
			req.Header.Set("Authorization", "Bearer invalid-ticket")
			resp, err := tc.newClient().Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status=%d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestTransportSessionEndpoints(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			endpoints := []string{
				"/v1/tai/session/abc-123/control",
				"/v1/tai/session/abc-123/data",
			}
			for _, ep := range endpoints {
				t.Run(ep, func(t *testing.T) {
					// Each request needs its own ticket (one-time use).
					ticket, _ := ts.Auth.IssueConnectTicket("sess-node-"+tc.name, "session")

					pr, pw := io.Pipe()
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()

					req, _ := http.NewRequestWithContext(ctx, "POST",
						tc.url+ep, pr)
					req.Header.Set("Authorization", "Bearer "+ticket.Ticket)

					ch := make(chan *http.Response, 1)
					go func() {
						resp, _ := tc.newClient().Do(req)
						ch <- resp
					}()

					time.Sleep(200 * time.Millisecond)
					pw.Close()

					select {
					case resp := <-ch:
						if resp != nil {
							defer resp.Body.Close()
							if resp.StatusCode != http.StatusOK {
								body, _ := io.ReadAll(resp.Body)
								t.Errorf("status=%d, want 200, body=%s", resp.StatusCode, body)
							}
						}
					case <-time.After(3 * time.Second):
						t.Error("request timed out")
					}
				})
			}
		})
	}
}

func TestTransportClose(t *testing.T) {
	ts := NewTestServer(t)

	if ts.TransportAddr == "" {
		t.Error("H2 transport not started")
	}
	if ts.H3Addr == "" {
		t.Error("H3 transport not started")
	}

	ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST",
		"http://"+ts.TransportAddr+"/v1/tai/control", nil)
	_, err := h2cClient().Do(req)
	if err == nil {
		t.Error("expected error after server close")
	}
}

func TestTransportH2AddrBeforeStart(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()
	ts := newTransportServer(auth, auth, mgr)
	if ts.h2Addr() != "" {
		t.Errorf("h2Addr should be empty before start, got %q", ts.h2Addr())
	}
}

func TestTransportStartAndAddr(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()
	ts := newTransportServer(auth, auth, mgr)

	if err := ts.start("127.0.0.1:0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ts.close(context.Background())

	if ts.h2Addr() == "" {
		t.Error("h2Addr should not be empty after start")
	}
}

func TestTransportCloseNilServers(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()
	ts := newTransportServer(auth, auth, mgr)
	if err := ts.close(context.Background()); err != nil {
		t.Errorf("close nil servers: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Control stream error paths — H2 and H3
// ---------------------------------------------------------------------------

func TestControlStreamBadRegister(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			nodeID := "bad-reg-" + tc.name
			ticket, _ := ts.Auth.IssueConnectTicket(nodeID, "control")
			pr, pw := io.Pipe()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST",
				tc.url+"/v1/tai/control", pr)
			req.Header.Set("Authorization", "Bearer "+ticket.Ticket)

			ch := make(chan *http.Response, 1)
			go func() {
				resp, _ := tc.newClient().Do(req)
				ch <- resp
			}()

			// Send register with invalid protobuf data
			writeFrame(pw, &pb.ControlMessage{Kind: "register", Data: []byte("not-protobuf")})
			pw.Close()

			resp := <-ch
			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}

func TestControlStreamPingBeforeRegister(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			nodeID := "ping-first-" + tc.name
			ticket, _ := ts.Auth.IssueConnectTicket(nodeID, "control")
			pr, pw := io.Pipe()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST",
				tc.url+"/v1/tai/control", pr)
			req.Header.Set("Authorization", "Bearer "+ticket.Ticket)

			ch := make(chan *http.Response, 1)
			go func() {
				resp, _ := tc.newClient().Do(req)
				ch <- resp
			}()

			pingData, _ := proto.Marshal(&pb.Ping{Timestamp: 1234})
			writeFrame(pw, &pb.ControlMessage{Kind: "ping", Data: pingData})
			pw.Close()

			resp := <-ch
			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}
