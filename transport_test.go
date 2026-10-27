package tai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// Connection authorization tests — H2 and H3
// ---------------------------------------------------------------------------

// controlStatus posts to /v1/tai/control on client and returns the status code.
func controlStatus(t *testing.T, client *http.Client, url string) (int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", url+"/v1/tai/control", nil)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func TestTransportControlRequiresConnect(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			status, err := controlStatus(t, tc.newClient(), tc.url)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			if status != http.StatusUnauthorized {
				t.Errorf("status=%d, want 401", status)
			}
		})
	}
}

func TestTransportConnectRejections(t *testing.T) {
	ts := NewTestServer(t)
	valid := ts.IssueTestNodeToken("rej-node")

	cases := []struct {
		name     string
		token    string
		nodeID   string
		connType string
	}{
		{"missing token", "", "rej-node", "control"},
		{"invalid token", "invalid-token", "rej-node", "control"},
		{"node id mismatch", valid, "other-node", "control"},
		{"light type", valid, "rej-node", "light"},
		{"empty type", valid, "rej-node", ""},
	}

	for _, tc := range protocolCases(ts) {
		for _, c := range cases {
			t.Run(tc.name+"/"+c.name, func(t *testing.T) {
				status, err := postConnect(tc.newClient(), tc.url, c.token, c.nodeID, c.connType)
				if err != nil {
					t.Fatalf("request: %v", err)
				}
				if status != http.StatusUnauthorized {
					t.Errorf("status=%d, want 401", status)
				}
			})
		}
	}
}

func TestTransportConnectBadBody(t *testing.T) {
	ts := NewTestServer(t)
	token := ts.IssueTestNodeToken("bad-body")

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", tc.url+"/v1/tai/connect", io.NopCloser(&errReader{}))
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := tc.newClient().Do(req)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("status=%d, want 401", resp.StatusCode)
				}
			}
		})
	}
}

// errReader fails every read so the request body cannot be decoded.
type errReader struct{}

func (*errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestTransportAuthorizationIsPerConnection(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			client := authorizedClient(t, ts, tc, "per-conn-"+tc.name)

			// Same connection: authorized, a second connect is accepted too.
			status, err := postConnect(client, tc.url, ts.IssueTestNodeToken("per-conn-"+tc.name), "per-conn-"+tc.name, "control")
			if err != nil || status != http.StatusOK {
				t.Fatalf("second connect on authorized connection: status=%d err=%v", status, err)
			}

			// A new connection carries no authorization.
			status, err = controlStatus(t, tc.newClient(), tc.url)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			if status != http.StatusUnauthorized {
				t.Errorf("new connection status=%d, want 401", status)
			}
		})
	}
}

func TestTransportRejectedConnectionIsClosed(t *testing.T) {
	ts := NewTestServer(t)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			client := tc.newClient()
			status, err := postConnect(client, tc.url, "invalid-token", "x", "control")
			if err != nil || status != http.StatusUnauthorized {
				t.Fatalf("connect: status=%d err=%v", status, err)
			}

			// After the grace period the server has closed the connection, so
			// the client must open a new (unauthorized) one.
			time.Sleep(closeGrace + 300*time.Millisecond)
			status, err = controlStatus(t, client, tc.url)
			if err == nil && status == http.StatusOK {
				t.Errorf("control accepted after rejected connect")
			}
		})
	}
}

func TestTransportUnauthorizedTimeout(t *testing.T) {
	ts := newTestServer(t, 300*time.Millisecond)

	pool := x509.NewCertPool()
	c, _ := x509.ParseCertificate(ts.TLSConfig.Certificates[0].Certificate[0])
	pool.AddCert(c)

	t.Run("H2", func(t *testing.T) {
		conn, err := tls.Dial("tcp", ts.TransportAddr, &tls.Config{RootCAs: pool, NextProtos: []string{"h2"}})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()

		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					t.Fatal("connection not closed after unauthorized timeout")
				}
				return
			}
		}
	})

	t.Run("H3", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, err := quic.DialAddr(ctx, ts.H3Addr, &tls.Config{RootCAs: pool, NextProtos: []string{"h3"}}, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		select {
		case <-conn.Context().Done():
		case <-ctx.Done():
			t.Fatal("QUIC connection not closed after unauthorized timeout")
		}
	})
}

func TestTransportAuthorizedSurvivesTimeout(t *testing.T) {
	ts := newTestServer(t, 300*time.Millisecond)

	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			client := authorizedClient(t, ts, tc, "survivor-"+tc.name)
			time.Sleep(700 * time.Millisecond)

			// The connection is still authorized and open: /connect works
			// again without a new handshake.
			status, err := postConnect(client, tc.url, ts.IssueTestNodeToken("survivor-"+tc.name), "survivor-"+tc.name, "control")
			if err != nil || status != http.StatusOK {
				t.Fatalf("connect after timeout window: status=%d err=%v", status, err)
			}
		})
	}
}

func TestConnStateWithoutContext(t *testing.T) {
	if connStateFrom(context.Background()) != nil {
		t.Error("connStateFrom on empty context should be nil")
	}
	ts := newTransportServer(nil, newNodeManager(), defaultCodec)
	rec := &statusRecorder{header: http.Header{}}
	req, _ := http.NewRequest("POST", "/v1/tai/control", nil)
	ts.handler.ServeHTTP(rec, req)
	if rec.code != http.StatusUnauthorized {
		t.Errorf("control without connection state: status=%d, want 401", rec.code)
	}
}

// statusRecorder is a minimal http.ResponseWriter recording the status code.
type statusRecorder struct {
	header http.Header
	code   int
}

func (r *statusRecorder) Header() http.Header         { return r.header }
func (r *statusRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *statusRecorder) WriteHeader(code int)        { r.code = code }

func TestConnectWithoutConnectionState(t *testing.T) {
	auth, _ := newTestAuth(t)
	token, _ := auth.IssueNodeToken("n1")
	ts := newTransportServer(auth, newNodeManager(), defaultCodec)

	body := `{"node_id":"n1","connect_type":"control"}`
	req, _ := http.NewRequest("POST", "/v1/tai/connect", stringsReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := &statusRecorder{header: http.Header{}}
	ts.handler.ServeHTTP(rec, req)
	if rec.code != http.StatusInternalServerError {
		t.Errorf("connect without connection state: status=%d, want 500", rec.code)
	}
}

func stringsReader(s string) io.Reader { return io.NopCloser(&stringReader{s: s}) }

type stringReader struct {
	s string
	i int
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}

func TestConnStateUnauthorizedTimerClosesConnection(t *testing.T) {
	closed := make(chan struct{})
	s := newConnState(func() { close(closed) }, 50*time.Millisecond)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("unauthorized timer did not close the connection")
	}
	if _, ok := s.check(connTypeControl); ok {
		t.Error("unauthorized state must not pass check")
	}
}

func TestConnStateAuthorizeCancelsTimer(t *testing.T) {
	closed := make(chan struct{}, 1)
	s := newConnState(func() { closed <- struct{}{} }, 50*time.Millisecond)
	s.authorizeAs("n1", connTypeControl)
	time.Sleep(200 * time.Millisecond)
	select {
	case <-closed:
		t.Fatal("authorized connection was closed by the timer")
	default:
	}
	if nodeID, ok := s.check(connTypeControl); !ok || nodeID != "n1" {
		t.Errorf("check = (%q, %v), want (n1, true)", nodeID, ok)
	}
	if _, ok := s.check(connTypeLight); ok {
		t.Error("check must fail for a different connection type")
	}
}

func TestConnStateTimerRaceWithAuthorize(t *testing.T) {
	closed := make(chan struct{}, 1)
	s := newConnState(func() { closed <- struct{}{} }, time.Nanosecond)
	s.authorizeAs("n1", connTypeControl)
	time.Sleep(50 * time.Millisecond)
	// Either the timer or authorize wins; the state must stay consistent:
	// the connection is closed exactly when authorization was refused.
	_, authorized := s.check(connTypeControl)
	select {
	case <-closed:
		if authorized {
			t.Error("connection closed although authorized")
		}
	default:
		if !authorized {
			t.Error("connection neither closed nor authorized")
		}
	}
}

func TestConnStateAuthorizeAfterExpiryFails(t *testing.T) {
	closed := make(chan struct{})
	s := newConnState(func() { close(closed) }, 20*time.Millisecond)
	<-closed
	if s.authorizeAs("n1", connTypeControl) {
		t.Error("authorize after the unauthorized timer fired must fail")
	}
}

func TestConnectBodyTooLarge(t *testing.T) {
	ts := NewTestServer(t)
	token := ts.IssueTestNodeToken("big-body")
	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			big := strings.NewReader(`{"node_id":"big-body","connect_type":"control","x":"` + strings.Repeat("a", 64*1024) + `"}`)
			req, _ := http.NewRequest("POST", tc.url+"/v1/tai/connect", big)
			req.Header.Set("Authorization", "Bearer "+token)
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

func TestStartH3RequiresTLS(t *testing.T) {
	auth, _ := newTestAuth(t)
	ts := newTransportServer(auth, newNodeManager(), defaultCodec)
	if err := ts.startH3("127.0.0.1:0", nil); err == nil {
		t.Error("startH3 without TLS must fail")
	}
}

func TestConnStateCloseSoon(t *testing.T) {
	closed := make(chan struct{})
	s := newConnState(func() { close(closed) }, 0)
	s.closeSoon()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("closeSoon did not close the connection")
	}
}

func TestConnStateNilClose(t *testing.T) {
	s := newConnState(nil, 0)
	s.close() // must not panic
}

// ---------------------------------------------------------------------------
// Server lifecycle
// ---------------------------------------------------------------------------

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
	ts := newTransportServer(auth, mgr, defaultCodec)
	if ts.tcpAddr() != "" {
		t.Errorf("tcpAddr should be empty before start, got %q", ts.tcpAddr())
	}
}

func TestTransportStartAndAddr(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()
	ts := newTransportServer(auth, mgr, defaultCodec)

	if err := ts.start("127.0.0.1:0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ts.close(context.Background())

	if ts.tcpAddr() == "" {
		t.Error("tcpAddr should not be empty after start")
	}
}

func TestTransportCloseNilServers(t *testing.T) {
	auth, _ := newTestAuth(t)
	mgr := newNodeManager()
	ts := newTransportServer(auth, mgr, defaultCodec)
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
			client := authorizedClient(t, ts, tc, nodeID)
			pr, pw := io.Pipe()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST",
				tc.url+"/v1/tai/control", pr)

			ch := make(chan *http.Response, 1)
			go func() {
				resp, _ := client.Do(req)
				ch <- resp
			}()

			// Send register with invalid protobuf data
			writeFrame(pw, &pb.Payload{Namespace: NamespaceControl, Kind: "register", Data: []byte("not-protobuf")})
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
			client := authorizedClient(t, ts, tc, nodeID)
			pr, pw := io.Pipe()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST",
				tc.url+"/v1/tai/control", pr)

			ch := make(chan *http.Response, 1)
			go func() {
				resp, _ := client.Do(req)
				ch <- resp
			}()

			pingData, _ := proto.Marshal(&pb.Ping{Timestamp: 1234})
			writeFrame(pw, &pb.Payload{Namespace: NamespaceControl, Kind: "ping", Data: pingData})
			pw.Close()

			resp := <-ch
			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}
