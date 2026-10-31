package transport

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

// AlpnH2 is the TLS ALPN protocol name for HTTP/2.
const AlpnH2 = "h2"

// AlpnTai is the TLS ALPN protocol name for the Tai frame protocol.
const AlpnTai = "tai/2"

const h2cPreface = "PRI * HTTP/2.0"

const (
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// taiConnHandler serves one TCP connection speaking the Tai frame protocol.
type taiConnHandler func(conn net.Conn, st *ConnState)

// tcpMux is the TCP side of the transport server.
type tcpMux struct {
	ln       net.Listener
	tlsCfg   *tls.Config
	h2Server *http.Server
	h2Ln     *chanListener
	h2Done   chan struct{}
	wg       sync.WaitGroup

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

func (t *Server) startTCP(addr string, tlsCfg *tls.Config) error {
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
		m.tlsCfg.NextProtos = []string{AlpnH2, AlpnTai}
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

// Start starts the TCP listener in plaintext mode.
func (t *Server) Start(addr string) error {
	return t.startTCP(addr, nil)
}

func (t *Server) tcpConnContext(ctx context.Context, c net.Conn) context.Context {
	return t.connContext(ctx, func() { c.Close() })
}

func (t *Server) acceptLoop(m *tcpMux) {
	var delay time.Duration
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			if m.isClosed() || errors.Is(err, net.ErrClosed) {
				return
			}
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

func (t *Server) routeTCP(m *tcpMux, raw net.Conn) {
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
		conn, proto = &peekedConn{Conn: raw, r: br}, AlpnTai
		if isH2 {
			proto = AlpnH2
		}
	}
	conn.SetDeadline(time.Time{})

	switch proto {
	case AlpnH2:
		m.release(raw)
		if !m.h2Ln.push(conn) {
			conn.Close()
		}
	case AlpnTai:
		t.serveTai(m, raw, conn, timeout)
	default:
		log.Printf("tai-sdk: TCP connection from %s negotiated unsupported protocol %q", raw.RemoteAddr(), proto)
		m.release(raw)
		conn.Close()
	}
}

func (t *Server) serveTai(m *tcpMux, raw, conn net.Conn, timeout time.Duration) {
	defer m.release(raw)
	defer conn.Close()

	h := t.taiHandler.Load()
	if h == nil {
		log.Printf("tai-sdk: Tai frame connection from %s rejected: no handler registered", raw.RemoteAddr())
		return
	}
	st := NewConnState(func() { conn.Close() }, timeout)
	(*h)(conn, st)
}

func (t *Server) unauthTimeoutOrDefault() time.Duration {
	if t.unauthTimeout > 0 {
		return t.unauthTimeout
	}
	return DefaultUnauthTimeout
}

func (t *Server) closeTCP(ctx context.Context) error {
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

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

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

func (l *chanListener) push(conn net.Conn) bool {
	select {
	case l.ch <- conn:
		return true
	case <-l.done:
		return false
	}
}
