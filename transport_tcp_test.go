package tai

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// newTCPTestServer starts a transportServer's TCP listener (TLS when tlsMode)
// and returns it with its address.
func newTCPTestServer(t *testing.T, tlsMode bool, unauthTimeout time.Duration) (*transportServer, string) {
	t.Helper()
	auth, _ := newTestAuth(t)
	ts := newTransportServer(auth, newNodeManager(), defaultCodec)
	ts.unauthTimeout = unauthTimeout

	var cfg *tls.Config
	if tlsMode {
		cfg, _, _ = generateTestTLS(t)
	}
	if err := ts.startTCP("127.0.0.1:0", cfg); err != nil {
		t.Fatalf("startTCP: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ts.close(ctx)
	})
	return ts, ts.tcpAddr()
}

func dialTLS(t *testing.T, addr string, protos ...string) *tls.Conn {
	t.Helper()
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, NextProtos: protos})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// expectClosed reports whether the server closes conn within d.
func expectClosed(t *testing.T, conn net.Conn, d time.Duration) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 16)
	for {
		if _, err := conn.Read(buf); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("connection not closed by server")
			}
			return
		}
	}
}

// taiRecorder is a taiConnHandler that reports each connection it receives.
type taiRecorder struct {
	got chan struct {
		conn net.Conn
		st   *connState
	}
	release chan struct{}
}

func newTaiRecorder() *taiRecorder {
	return &taiRecorder{
		got: make(chan struct {
			conn net.Conn
			st   *connState
		}, 4),
		release: make(chan struct{}),
	}
}

func (r *taiRecorder) handle(conn net.Conn, st *connState) {
	r.got <- struct {
		conn net.Conn
		st   *connState
	}{conn, st}
	<-r.release
}

func TestTCPTLSTaiALPNRoutesToTaiHandler(t *testing.T) {
	ts, addr := newTCPTestServer(t, true, time.Second)
	rec := newTaiRecorder()
	defer close(rec.release)
	ts.setTaiHandler(rec.handle)

	c := dialTLS(t, addr, alpnTai)
	if got := c.ConnectionState().NegotiatedProtocol; got != alpnTai {
		t.Fatalf("negotiated %q, want %q", got, alpnTai)
	}
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	select {
	case h := <-rec.got:
		buf := make([]byte, 5)
		if _, err := io.ReadFull(h.conn, buf); err != nil || string(buf) != "hello" {
			t.Errorf("handler read %q, %v", buf, err)
		}
		if h.st == nil {
			t.Error("handler got nil connState")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Tai handler not called")
	}
}

func TestTCPTLSTaiALPNWithoutHandlerCloses(t *testing.T) {
	_, addr := newTCPTestServer(t, true, time.Second)
	c := dialTLS(t, addr, alpnTai)
	expectClosed(t, c, 3*time.Second)
}

func TestTCPTLSUnsupportedALPNCloses(t *testing.T) {
	_, addr := newTCPTestServer(t, true, time.Second)
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Skipf("handshake without ALPN rejected: %v", err)
	}
	defer c.Close()
	expectClosed(t, c, 3*time.Second)
}

func TestTCPTLSHandshakeTimeout(t *testing.T) {
	_, addr := newTCPTestServer(t, true, 200*time.Millisecond)
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	expectClosed(t, raw, 3*time.Second)
}

func TestTCPTLSTaiUnauthorizedTimerClosesConn(t *testing.T) {
	ts, addr := newTCPTestServer(t, true, 300*time.Millisecond)
	rec := newTaiRecorder()
	defer close(rec.release)
	ts.setTaiHandler(rec.handle)

	c := dialTLS(t, addr, alpnTai)
	select {
	case <-rec.got:
	case <-time.After(3 * time.Second):
		t.Fatal("Tai handler not called")
	}
	expectClosed(t, c, 3*time.Second)
}

func TestTCPTLSTaiAuthorizedSurvivesTimer(t *testing.T) {
	ts, addr := newTCPTestServer(t, true, 300*time.Millisecond)
	rec := newTaiRecorder()
	defer close(rec.release)
	ts.setTaiHandler(rec.handle)

	c := dialTLS(t, addr, alpnTai)
	h := <-rec.got
	h.st.authorizeAs("n1", connTypeLight)

	time.Sleep(700 * time.Millisecond)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatalf("authorized connection closed: %v", err)
	}
}

func TestTCPPlaintextH2C(t *testing.T) {
	_, addr := newTCPTestServer(t, false, time.Second)
	req, _ := http.NewRequest("POST", "http://"+addr+"/v1/tai/control", nil)
	resp, err := h2cClient().Do(req)
	if err != nil {
		t.Fatalf("h2c request: %v", err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Errorf("proto=%s, want HTTP/2", resp.Proto)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}
}

func TestTCPPlaintextNonHTTPRoutesToTaiHandler(t *testing.T) {
	ts, addr := newTCPTestServer(t, false, time.Second)
	rec := newTaiRecorder()
	defer close(rec.release)
	ts.setTaiHandler(rec.handle)

	// A first byte equal to the preface's 'P' but a different continuation.
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	payload := "PING-not-http2-frame"
	c.Write([]byte(payload))

	select {
	case h := <-rec.got:
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(h.conn, buf); err != nil || string(buf) != payload {
			t.Errorf("handler must see the peeked bytes, got %q, %v", buf, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Tai handler not called")
	}
}

func TestTCPPlaintextNonPrefixByteRoutesToTaiHandler(t *testing.T) {
	ts, addr := newTCPTestServer(t, false, time.Second)
	rec := newTaiRecorder()
	defer close(rec.release)
	ts.setTaiHandler(rec.handle)

	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	c.Write([]byte{0, 0, 0, 5, 'a', 'b', 'c', 'd', 'e'})

	select {
	case <-rec.got:
	case <-time.After(3 * time.Second):
		t.Fatal("Tai handler not called")
	}
}

func TestTCPPlaintextNoHandlerCloses(t *testing.T) {
	_, addr := newTCPTestServer(t, false, time.Second)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	c.Write([]byte{0, 0, 0, 1, 'a'})
	expectClosed(t, c, 3*time.Second)
}

func TestTCPPlaintextSilentConnectionTimesOut(t *testing.T) {
	_, addr := newTCPTestServer(t, false, 200*time.Millisecond)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	expectClosed(t, c, 3*time.Second)
}

func TestTCPPlaintextShortPrefixThenEOFCloses(t *testing.T) {
	_, addr := newTCPTestServer(t, false, time.Second)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	c.Write([]byte("PRI"))
	c.(*net.TCPConn).CloseWrite()
	expectClosed(t, c, 3*time.Second)
}

func TestTCPCloseClosesTaiAndRoutingConnections(t *testing.T) {
	ts, addr := newTCPTestServer(t, false, 10*time.Second)
	rec := newTaiRecorder()
	ts.setTaiHandler(func(conn net.Conn, st *connState) {
		rec.handle(conn, st)
	})

	tai, _ := net.Dial("tcp", addr)
	defer tai.Close()
	tai.Write([]byte{0, 0, 0, 1, 'a'})
	<-rec.got

	idle, _ := net.Dial("tcp", addr)
	defer idle.Close()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ts.close(ctx) }()
	close(rec.release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not return")
	}
	expectClosed(t, idle, time.Second)
	expectClosed(t, tai, time.Second)
}

func TestTCPUnsupportedHTTP1OnTLSCloses(t *testing.T) {
	_, addr := newTCPTestServer(t, true, time.Second)
	c := dialTLS(t, addr, "http/1.1")
	// "http/1.1" is not offered by the server: handshake yields no ALPN
	// match or an alert; either way no HTTP/1.1 service exists.
	req := "GET / HTTP/1.1\r\nHost: x\r\n\r\n"
	c.Write([]byte(req))
	buf := make([]byte, 64)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := c.Read(buf)
	if strings.HasPrefix(string(buf[:n]), "HTTP/1.1 200") {
		t.Error("HTTP/1.1 must not be served on 6099")
	}
}

func TestChanListener(t *testing.T) {
	l := newChanListener(&net.TCPAddr{})
	if l.Addr() == nil {
		t.Error("Addr nil")
	}
	l.Close()
	l.Close()
	if _, err := l.Accept(); err == nil {
		t.Error("Accept after Close should fail")
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if l.push(a) {
		t.Error("push after Close should fail")
	}
}

func TestTCPAddrBeforeStart(t *testing.T) {
	auth, _ := newTestAuth(t)
	ts := newTransportServer(auth, newNodeManager(), defaultCodec)
	if ts.tcpAddr() != "" {
		t.Error("tcpAddr should be empty before start")
	}
	if err := ts.closeTCP(context.Background()); err != nil {
		t.Errorf("closeTCP before start: %v", err)
	}
}

func TestStartTCPListenError(t *testing.T) {
	auth, _ := newTestAuth(t)
	ts := newTransportServer(auth, newNodeManager(), defaultCodec)
	if err := ts.startTCP("256.256.256.256:0", nil); err == nil {
		t.Error("expected listen error")
	}
}

// flakyListener fails the first Accept calls with a temporary error.
type flakyListener struct {
	net.Listener
	failures int
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.failures > 0 {
		l.failures--
		return nil, errors.New("accept: too many open files")
	}
	return l.Listener.Accept()
}

func TestTCPAcceptRetriesTransientErrors(t *testing.T) {
	auth, _ := newTestAuth(t)
	ts := newTransportServer(auth, newNodeManager(), defaultCodec)
	if err := ts.startTCP("127.0.0.1:0", nil); err != nil {
		t.Fatal(err)
	}
	defer ts.close(context.Background())

	// Replace the listener before any connection arrives is racy with the
	// running accept loop, so drive a second loop on a flaky listener.
	inner, _ := net.Listen("tcp", "127.0.0.1:0")
	m := &tcpMux{ln: &flakyListener{Listener: inner, failures: 2}, h2Ln: newChanListener(inner.Addr()), conns: map[net.Conn]struct{}{}}
	rec := newTaiRecorder()
	defer close(rec.release)
	ts.setTaiHandler(rec.handle)
	go ts.acceptLoop(m)
	defer func() { inner.Close() }()

	c, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte{0, 0, 0, 1, 'a'})
	select {
	case <-rec.got:
	case <-time.After(3 * time.Second):
		t.Fatal("accept loop did not recover from transient errors")
	}
}
