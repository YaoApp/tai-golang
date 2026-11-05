package transport

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// unauthTimeoutOrDefault
// ---------------------------------------------------------------------------

func TestUnauthTimeoutOrDefault_Zero(t *testing.T) {
	s := &Server{}
	got := s.unauthTimeoutOrDefault()
	if got != DefaultUnauthTimeout {
		t.Errorf("got %v, want %v", got, DefaultUnauthTimeout)
	}
}

func TestUnauthTimeoutOrDefault_Custom(t *testing.T) {
	s := &Server{unauthTimeout: 10 * time.Second}
	got := s.unauthTimeoutOrDefault()
	if got != 10*time.Second {
		t.Errorf("got %v, want 10s", got)
	}
}

// ---------------------------------------------------------------------------
// tcpMux.isClosed / track / release
// ---------------------------------------------------------------------------

func TestTcpMux_IsClosedInitial(t *testing.T) {
	m := &tcpMux{conns: make(map[net.Conn]struct{})}
	if m.isClosed() {
		t.Error("isClosed should be false initially")
	}
}

func TestTcpMux_IsClosedAfterSet(t *testing.T) {
	m := &tcpMux{conns: make(map[net.Conn]struct{})}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	if !m.isClosed() {
		t.Error("isClosed should be true after setting closed")
	}
}

func TestTcpMux_TrackAndRelease(t *testing.T) {
	m := &tcpMux{conns: make(map[net.Conn]struct{})}
	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()

	if !m.track(s) {
		t.Error("track should return true")
	}
	m.mu.Lock()
	_, ok := m.conns[s]
	m.mu.Unlock()
	if !ok {
		t.Error("conn not found after track")
	}

	m.release(s)
	m.mu.Lock()
	_, ok = m.conns[s]
	m.mu.Unlock()
	if ok {
		t.Error("conn still found after release")
	}
}

func TestTcpMux_Track_Closed(t *testing.T) {
	m := &tcpMux{conns: make(map[net.Conn]struct{})}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()

	if m.track(s) {
		t.Error("track should return false when closed")
	}
}

// ---------------------------------------------------------------------------
// peekH2Preface
// ---------------------------------------------------------------------------

func TestPeekH2Preface_True(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(h2cPreface + "extra data"))
	got, err := peekH2Preface(r)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got {
		t.Error("expected true for H2 preface")
	}
}

func TestPeekH2Preface_False(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n"))
	got, err := peekH2Preface(r)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got {
		t.Error("expected false for non-H2 data")
	}
}

func TestPeekH2Preface_ShortRead(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	_, err := peekH2Preface(r)
	if err == nil {
		t.Error("expected error on empty reader")
	}
}

// ---------------------------------------------------------------------------
// chanListener
// ---------------------------------------------------------------------------

func TestChanListener_AcceptAndClose(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
	l := newChanListener(addr)

	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()

	go func() {
		l.push(s)
	}()

	got, err := l.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got != s {
		t.Error("Accept returned wrong conn")
	}

	l.Close()
	_, err = l.Accept()
	if err != net.ErrClosed {
		t.Errorf("Accept after Close: %v, want net.ErrClosed", err)
	}
}

func TestChanListener_DoubleClose(t *testing.T) {
	l := newChanListener(&net.TCPAddr{})
	l.Close()
	l.Close() // should not panic
}

func TestChanListener_PushAfterClose(t *testing.T) {
	l := newChanListener(&net.TCPAddr{})
	l.Close()

	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()

	if l.push(s) {
		t.Error("push should return false after Close")
	}
}

func TestChanListener_Addr(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 8080}
	l := newChanListener(addr)
	if l.Addr() != addr {
		t.Errorf("Addr = %v, want %v", l.Addr(), addr)
	}
}

// ---------------------------------------------------------------------------
// peekedConn
// ---------------------------------------------------------------------------

func TestPeekedConn_Read(t *testing.T) {
	c, s := net.Pipe()
	defer s.Close()

	go func() {
		c.Write([]byte("hello"))
		c.Close()
	}()

	br := bufio.NewReader(s)
	pc := &peekedConn{Conn: s, r: br}
	buf := make([]byte, 5)
	n, err := pc.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("Read = %q, want hello", buf[:n])
	}
}
