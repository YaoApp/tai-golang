package tai

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// ALPN protocol names on the 6099 TCP listener.
const (
	alpnH2  = "h2"
	alpnTai = "tai/2"
)

// h2cPreface is the beginning of the HTTP/2 client connection preface. A
// plaintext connection that starts with it is served as h2c; any other
// plaintext connection speaks the Tai frame protocol.
const h2cPreface = "PRI * HTTP/2.0"

// Accept retry delays after a transient listener error; same bounds as
// net/http.
const (
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// taiConnHandler serves one TCP connection that speaks the Tai frame
// protocol (ALPN "tai/2", or plaintext that is not HTTP/2). st carries the
// connection's authorization state and unauthorized timer; the handler owns
// conn and must return when conn is closed.
type taiConnHandler func(conn net.Conn, st *connState)

// tcpMux is the 6099 TCP side of the transport server. It accepts TCP
// connections, completes the TLS handshake or peeks the first bytes, then
// routes each connection to the HTTP/2 server or the Tai frame handler.
type tcpMux struct {
	ln       net.Listener
	tlsCfg   *tls.Config // nil: plaintext (h2c and Tai frames)
	h2Server *http.Server
	h2Ln     *chanListener
	h2Done   chan struct{}
	wg       sync.WaitGroup

	mu     sync.Mutex
	conns  map[net.Conn]struct{} // connections still owned by the mux
	closed bool
}

// startTCP starts the TCP listener on addr. With tlsCfg the listener speaks
// TLS and routes by ALPN ("h2", "tai/2"); without it the listener is
// plaintext and routes by the first bytes (trusted networks only).
func (t *transportServer) startTCP(addr string, tlsCfg *tls.Config) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp listen: %w", err)
	}

	m := &tcpMux{
		ln:    ln,
		h2Ln:  newChanListener(ln.Addr()),
		conns: make(map[net.Conn]struct{}),
	}
	if tlsCfg != nil {
		m.tlsCfg = tlsCfg.Clone()
		m.tlsCfg.NextProtos = []string{alpnH2, alpnTai}
	}

	var protos http.Protocols
	protos.SetHTTP2(true)
	protos.SetUnencryptedHTTP2(true)
	m.h2Server = &http.Server{
		Handler:     t.handler,
		Protocols:   &protos,
		ConnContext: t.tcpConnContext,
	}
	m.h2Done = make(chan struct{})
	t.tcp = m

	go func() {
		defer close(m.h2Done)
		if err := m.h2Server.Serve(m.h2Ln); err != nil && err != http.ErrServerClosed {
			log.Printf("tai-sdk: H2 transport error: %v", err)
		}
	}()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		mode := "TCP/h2c+tai"
		if tlsCfg != nil {
			mode = "TCP/TLS"
		}
		log.Printf("tai-sdk: TCP transport listening on %s (%s)", ln.Addr(), mode)
		t.acceptLoop(m)
	}()
	return nil
}

// start starts the TCP listener in plaintext mode (for tests without TLS).
func (t *transportServer) start(addr string) error {
	return t.startTCP(addr, nil)
}

// tcpAddr returns the TCP listener address, or "" when not started.
func (t *transportServer) tcpAddr() string {
	if t.tcp == nil {
		return ""
	}
	return t.tcp.ln.Addr().String()
}

// setTaiHandler registers the handler for Tai frame connections; it may be
// called while the server is running and applies to later connections.
func (t *transportServer) setTaiHandler(h taiConnHandler) {
	t.taiHandler.Store(&h)
}

func (t *transportServer) acceptLoop(m *tcpMux) {
	var delay time.Duration
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			if m.isClosed() || errors.Is(err, net.ErrClosed) {
				return
			}
			// Transient failures (for example running out of file
			// descriptors) must not stop the listener: retry with backoff.
			if delay == 0 {
				delay = acceptBackoffMin
			} else if delay *= 2; delay > acceptBackoffMax {
				delay = acceptBackoffMax
			}
			log.Printf("tai-sdk: TCP accept error: %v; retrying in %v", err, delay)
			time.Sleep(delay)
			continue
		}
		delay = 0
		if !m.track(conn) {
			conn.Close()
			return
		}
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			t.routeTCP(m, conn)
		}()
	}
}

// routeTCP identifies the protocol of an accepted connection and hands it to
// the HTTP/2 server or the Tai frame handler. The TLS handshake and the first
// bytes must arrive within the unauthorized timeout.
func (t *transportServer) routeTCP(m *tcpMux, raw net.Conn) {
	timeout := t.unauthTimeoutOrDefault()
	raw.SetDeadline(time.Now().Add(timeout))

	var (
		conn  net.Conn
		proto string
	)
	if m.tlsCfg != nil {
		tc := tls.Server(raw, m.tlsCfg)
		if err := tc.Handshake(); err != nil {
			m.release(raw)
			raw.Close()
			return
		}
		conn, proto = tc, tc.ConnectionState().NegotiatedProtocol
	} else {
		br := bufio.NewReader(raw)
		isH2, err := peekH2Preface(br)
		if err != nil {
			m.release(raw)
			raw.Close()
			return
		}
		conn, proto = &peekedConn{Conn: raw, r: br}, alpnTai
		if isH2 {
			proto = alpnH2
		}
	}
	conn.SetDeadline(time.Time{})

	switch proto {
	case alpnH2:
		m.release(raw)
		if !m.h2Ln.push(conn) {
			conn.Close()
		}
	case alpnTai:
		t.serveTai(m, raw, conn, timeout)
	default:
		log.Printf("tai-sdk: TCP connection from %s negotiated unsupported protocol %q", raw.RemoteAddr(), proto)
		m.release(raw)
		conn.Close()
	}
}

// serveTai runs the registered Tai frame handler on conn, or closes conn when
// none is registered.
func (t *transportServer) serveTai(m *tcpMux, raw, conn net.Conn, timeout time.Duration) {
	defer m.release(raw)
	defer conn.Close()

	h := t.taiHandler.Load()
	if h == nil {
		log.Printf("tai-sdk: Tai frame connection from %s rejected: no handler registered", raw.RemoteAddr())
		return
	}
	st := newConnState(func() { conn.Close() }, timeout)
	(*h)(conn, st)
}

func (t *transportServer) unauthTimeoutOrDefault() time.Duration {
	if t.unauthTimeout > 0 {
		return t.unauthTimeout
	}
	return DefaultUnauthTimeout
}

// closeTCP stops accepting, closes connections still owned by the mux, and
// shuts the HTTP/2 server down.
func (t *transportServer) closeTCP(ctx context.Context) error {
	m := t.tcp
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	owned := make([]net.Conn, 0, len(m.conns))
	for c := range m.conns {
		owned = append(owned, c)
	}
	m.mu.Unlock()

	m.ln.Close()
	for _, c := range owned {
		c.Close()
	}
	err := m.h2Server.Shutdown(ctx)
	if err != nil {
		m.h2Server.Close()
	}
	m.h2Ln.Close()
	m.wg.Wait()
	<-m.h2Done
	return err
}

func (m *tcpMux) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// track registers conn as owned by the mux; false when the mux is closed.
func (m *tcpMux) track(conn net.Conn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.conns[conn] = struct{}{}
	return true
}

func (m *tcpMux) release(conn net.Conn) {
	m.mu.Lock()
	delete(m.conns, conn)
	m.mu.Unlock()
}

// peekH2Preface reports whether the buffered connection starts with the
// HTTP/2 preface. It blocks until enough bytes arrive or the deadline hits.
func peekH2Preface(br *bufio.Reader) (bool, error) {
	b, err := br.Peek(1)
	if err != nil {
		return false, err
	}
	if b[0] != h2cPreface[0] {
		return false, nil
	}
	b, err = br.Peek(len(h2cPreface))
	if err != nil {
		return false, err
	}
	return string(b) == h2cPreface, nil
}

// peekedConn reads through a bufio.Reader that already holds the bytes
// consumed while identifying the protocol.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// chanListener is a net.Listener fed by routeTCP with already identified
// HTTP/2 connections.
type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{addr: addr, ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

// push hands conn to the HTTP/2 server; false when the listener is closed.
func (l *chanListener) push(conn net.Conn) bool {
	select {
	case l.ch <- conn:
		return true
	case <-l.done:
		return false
	}
}
