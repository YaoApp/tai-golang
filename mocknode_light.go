package tai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"

	"github.com/yaoapp/tai-golang/pb"
)

// MockLightConn is a Light connection from MockNode to the SDK server.
// It wraps either an H3 WebTransport session or a reversed H2 connection.
type MockLightConn struct {
	proto    string                // "H3" or "H2"
	session  *webtransport.Session // H3 only
	h2Server *http2.Server         // H2 only (Tai is server after reversal)
	h2Conn   net.Conn              // H2 only

	mu       sync.Mutex
	streams  chan *MockLightStream // accepted streams
	closeFn  func()
	closed   bool
}

// MockLightStream wraps an accepted stream from Yao. Read/Write are the
// stream payload; FirstFrame is the Payload envelope Yao wrote first.
type MockLightStream struct {
	FirstFrame *pb.Payload
	r          io.ReadCloser
	w          io.WriteCloser
	codec      *FrameCodec
}

func (s *MockLightStream) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *MockLightStream) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *MockLightStream) Close() error {
	werr := s.w.Close()
	io.Copy(io.Discard, s.r)
	rerr := s.r.Close()
	if werr != nil {
		return werr
	}
	return rerr
}

// RecvFrame reads a length-prefixed Payload (SessionMessage etc.) from the stream.
func (s *MockLightStream) RecvFrame() (*pb.Payload, error) {
	return s.codec.ReadFrame(s.r)
}

// SendFrame writes a length-prefixed Payload to the stream.
func (s *MockLightStream) SendFrame(msg *pb.Payload) error {
	return s.codec.WriteFrame(s.w, msg)
}

// ConnectLightH3 opens an H3 (WebTransport) Light connection to the SDK.
func (m *MockNode) ConnectLightH3(ctx context.Context) (*MockLightConn, error) {
	if m.nodeToken == "" {
		return nil, fmt.Errorf("nodeToken not set")
	}
	if m.h3Addr == "" {
		return nil, fmt.Errorf("h3Addr not set")
	}

	pool := x509.NewCertPool()
	wk, _ := m.Discover()
	if wk != nil && wk.TLS != nil && wk.TLS.CACertificate != "" {
		pool.AppendCertsFromPEM([]byte(wk.TLS.CACertificate))
	}

	d := &webtransport.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			NextProtos: []string{http3.NextProtoH3},
		},
		QUICConfig: &quic.Config{
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
	}

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+m.nodeToken)
	hdr.Set("X-Tai-Node-Id", m.ID)

	_, sess, err := d.Dial(ctx, "https://"+m.h3Addr+"/v1/tai/light", hdr)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("dial H3 light: %w", err)
	}

	lc := &MockLightConn{
		proto:   "H3",
		session: sess,
		streams: make(chan *MockLightStream, 32),
		closeFn: func() {
			sess.CloseWithError(0, "close")
			d.Close()
		},
	}

	// Accept streams from Yao in background
	go lc.acceptH3Streams(m.codec)

	return lc, nil
}

func (lc *MockLightConn) acceptH3Streams(codec *FrameCodec) {
	for {
		stream, err := lc.session.AcceptStream(lc.session.Context())
		if err != nil {
			lc.mu.Lock()
			lc.closed = true
			lc.mu.Unlock()
			close(lc.streams)
			return
		}
		firstFrame, err := codec.ReadFrame(stream)
		if err != nil {
			log.Printf("mocknode: light H3 read first frame: %v", err)
			stream.Close()
			continue
		}
		lc.streams <- &MockLightStream{
			FirstFrame: firstFrame,
			r:          io.NopCloser(stream),
			w:          stream,
			codec:      codec,
		}
	}
}

// ConnectLightH2 opens an H2 (TCP, ALPN tai/2, reversed) Light connection.
func (m *MockNode) ConnectLightH2(ctx context.Context) (*MockLightConn, error) {
	if m.nodeToken == "" {
		return nil, fmt.Errorf("nodeToken not set")
	}
	if m.h2Addr == "" {
		return nil, fmt.Errorf("h2Addr not set")
	}

	wk, _ := m.Discover()
	var conn net.Conn
	if wk != nil && wk.TLS != nil && wk.TLS.CACertificate != "" {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(wk.TLS.CACertificate))
		var err error
		conn, err = tls.DialWithDialer(&net.Dialer{}, "tcp", m.h2Addr, &tls.Config{
			RootCAs:    pool,
			NextProtos: []string{alpnTai},
		})
		if err != nil {
			return nil, fmt.Errorf("dial H2 light TLS: %w", err)
		}
	} else {
		var err error
		conn, err = net.Dial("tcp", m.h2Addr)
		if err != nil {
			return nil, fmt.Errorf("dial H2 light: %w", err)
		}
	}

	// Send Connect frame
	connectMsg := &pb.Connect{
		NodeId:      m.ID,
		NodeToken:   m.nodeToken,
		ConnectType: connTypeLight,
	}
	if err := WriteProtoMsg(conn, connectMsg, maxConnectFrameSize); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write connect: %w", err)
	}

	// Read ConnectAck
	var ack pb.ConnectAck
	if err := ReadProtoMsg(conn, &ack, maxConnectFrameSize); err != nil {
		conn.Close()
		return nil, fmt.Errorf("read connect_ack: %w", err)
	}

	// Role reversal: Tai becomes HTTP/2 server on this connection.
	lc := &MockLightConn{
		proto:   "H2",
		h2Conn:  conn,
		streams: make(chan *MockLightStream, 32),
		closeFn: func() { conn.Close() },
	}

	h2s := &http2.Server{}
	lc.h2Server = h2s

	// Serve HTTP/2 in background — Yao sends POST /v1/tai/stream as requests
	go func() {
		h2s.ServeConn(conn, &http2.ServeConnOpts{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lc.handleH2Stream(w, r, m.codec)
			}),
		})
		lc.mu.Lock()
		lc.closed = true
		lc.mu.Unlock()
		close(lc.streams)
	}()

	return lc, nil
}

func (lc *MockLightConn) handleH2Stream(w http.ResponseWriter, r *http.Request, codec *FrameCodec) {
	firstFrame, err := codec.ReadFrame(r.Body)
	if err != nil {
		log.Printf("mocknode: light H2 read first frame: %v", err)
		http.Error(w, "bad first frame", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Pipe connects the MockLightStream.Write → ResponseWriter. When the
	// test closes pw (via stream.Close()), io.Copy returns, the handler
	// returns, and HTTP/2 sends END_STREAM to the SDK side.
	pr, pw := io.Pipe()
	stream := &MockLightStream{
		FirstFrame: firstFrame,
		r:          r.Body,
		w:          pw,
		codec:      codec,
	}

	lc.streams <- stream

	fw := flushWriter{w}
	io.Copy(fw, pr)
}

// AcceptStream waits for the next stream opened by Yao. Returns nil after Close.
func (lc *MockLightConn) AcceptStream(ctx context.Context) (*MockLightStream, error) {
	select {
	case s, ok := <-lc.streams:
		if !ok {
			return nil, fmt.Errorf("light connection closed")
		}
		return s, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close tears down the Light connection.
func (lc *MockLightConn) Close() {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.closed {
		return
	}
	lc.closed = true
	if lc.closeFn != nil {
		lc.closeFn()
	}
}

// Protocol returns "H3" or "H2".
func (lc *MockLightConn) Protocol() string { return lc.proto }

// SendSessionMessage writes a SessionMessage as a Payload frame to the stream.
func (s *MockLightStream) SendSessionMessage(seq uint64, kind string, data []byte) error {
	smData, err := proto.Marshal(&pb.SessionMessage{Seq: seq, Kind: kind, Data: data})
	if err != nil {
		return err
	}
	return s.codec.WriteFrame(s.w, &pb.Payload{
		Namespace: NamespaceSession,
		Kind:      kind,
		Data:      smData,
	})
}

// flushWriter wraps an http.ResponseWriter so each Write also flushes.
type flushWriter struct {
	w http.ResponseWriter
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if f, ok := fw.w.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}

func (fw flushWriter) Close() error { return nil }
