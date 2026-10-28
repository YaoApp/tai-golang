package tai

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/webtransport-go"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"

	"github.com/yaoapp/tai-golang/pb"
)

// NamespaceSession is the Payload namespace for session streams.
const NamespaceSession = "session"

// maxConnectFrameSize bounds the Connect/ConnectAck frame (tiny messages).
const maxConnectFrameSize = 4 * 1024

// ---------------------------------------------------------------------------
// lightPool — Yao side: caches authorized Light connections from Tai nodes
// and opens streams on them. Yao opens all streams; Tai only accepts.
// ---------------------------------------------------------------------------

type lightPool struct {
	mu      sync.Mutex
	conns   map[string][]*lightConn // nodeID → connections
	cfg     *pb.PoolConfig
	codec   *FrameCodec
	mgr     *nodeManager
	auth    AuthProvider
	changed chan struct{} // closed+replaced on each pool change (broadcast)
	done    chan struct{} // closed on pool shutdown
	closed  bool         // set by close(), guards idempotency
}

func newLightPool(cfg *pb.PoolConfig, codec *FrameCodec, mgr *nodeManager, auth AuthProvider) *lightPool {
	return &lightPool{
		conns:   make(map[string][]*lightConn),
		cfg:     cfg,
		codec:   codec,
		mgr:     mgr,
		auth:    auth,
		changed: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// close removes all connections from the pool, unblocking any handlers
// that are waiting on h2Done. Must be called before closeTCP so the
// serveTai goroutines can return. Safe to call multiple times.
func (p *lightPool) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	for _, conns := range p.conns {
		for _, lc := range conns {
			if lc.h2Done != nil {
				select {
				case <-lc.h2Done:
				default:
					close(lc.h2Done)
				}
			}
		}
	}
	p.conns = make(map[string][]*lightConn)
	ch := p.changed
	p.changed = make(chan struct{})
	p.mu.Unlock()
	close(p.done)
	close(ch)
}

// lightConn is one cached Light connection (H3 WebTransport or H2 reversed).
type lightConn struct {
	nodeID  string
	proto   string                // "H3" or "H2"
	session *webtransport.Session // H3 only
	h2cc    *http2.ClientConn     // H2 only (role-reversed: Yao is HTTP/2 client)
	h2conn  net.Conn              // underlying TCP for H2
	h2Done  chan struct{}          // H2 only: closed to unblock handleLightH2
	active  atomic.Int32
	closed  atomic.Bool
	pool    *lightPool
}

// updateConfig replaces pool config (e.g. from pong dynamic adjustment).
func (p *lightPool) updateConfig(cfg *pb.PoolConfig) {
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
}

// connCount returns the number of connections for a node.
func (p *lightPool) connCount(nodeID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns[nodeID])
}

// addConn adds a new authorized Light connection to the pool.
// Returns false if the pool is already closed; the caller must
// clean up the connection in that case.
func (p *lightPool) addConn(lc *lightConn) bool {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return false
	}
	p.conns[lc.nodeID] = append(p.conns[lc.nodeID], lc)
	ch := p.changed
	p.changed = make(chan struct{})
	p.mu.Unlock()
	close(ch) // wake all waiters
	return true
}

// removeConn removes a connection from the pool.
func (p *lightPool) removeConn(lc *lightConn) {
	p.mu.Lock()
	conns := p.conns[lc.nodeID]
	for i, c := range conns {
		if c == lc {
			p.conns[lc.nodeID] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	if len(p.conns[lc.nodeID]) == 0 {
		delete(p.conns, lc.nodeID)
	}
	ch := p.changed
	p.changed = make(chan struct{})
	p.mu.Unlock()
	close(ch)

	// For H2: unblock handleLightH2 so serveTai can close the conn.
	if lc.h2Done != nil {
		select {
		case <-lc.h2Done:
		default:
			close(lc.h2Done)
		}
	}
}

// maxStreams returns the per-connection stream limit from pool config.
func (p *lightPool) maxStreams() int32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cfg != nil && p.cfg.LightMaxStreams > 0 {
		return int32(p.cfg.LightMaxStreams)
	}
	return 256
}

// ---------------------------------------------------------------------------
// OpenStream — picks the least-busy connection, opens a stream, writes the
// Payload envelope. Retries on another connection when the stream fails
// before Tai's first response byte (close race).
// ---------------------------------------------------------------------------

// ErrNoLightConn is returned when no Light connection is available and
// pool.adjust timed out.
var ErrNoLightConn = errors.New("tai: no light connection available")

// OpenStream opens a bidirectional stream to nodeID. firstFrame is the
// Payload envelope (namespace + kind + data) written as the stream's first
// frame. The returned LightStream is ready for reading and writing
// subsequent SessionMessage frames.
func (p *lightPool) OpenStream(ctx context.Context, nodeID string, firstFrame *pb.Payload) (*LightStream, error) {
	for {
		lc, err := p.pickConn(ctx, nodeID)
		if err != nil {
			return nil, err
		}

		stream, err := lc.openStream(ctx)
		if err != nil {
			if lc.closed.Load() {
				p.removeConn(lc)
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}

		if err := p.codec.WriteFrame(stream, firstFrame); err != nil {
			if !stream.HasResponse() {
				stream.Close()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			stream.Close()
			return nil, fmt.Errorf("write stream envelope: %w", err)
		}
		return stream, nil
	}
}

// pickConn selects the connection with the fewest active streams that has
// capacity. When none is available, it sends pool.adjust via Control and
// blocks until a new connection appears or ctx expires.
func (p *lightPool) pickConn(ctx context.Context, nodeID string) (*lightConn, error) {
	adjustSent := false
	for {
		p.mu.Lock()
		ch := p.changed
		maxS := int32(256)
		if p.cfg != nil && p.cfg.LightMaxStreams > 0 {
			maxS = int32(p.cfg.LightMaxStreams)
		}

		var best *lightConn
		for _, lc := range p.conns[nodeID] {
			if lc.closed.Load() {
				continue
			}
			if lc.active.Load() >= maxS {
				continue
			}
			if best == nil || lc.active.Load() < best.active.Load() {
				best = lc
			}
		}

		if best != nil {
			p.mu.Unlock()
			return best, nil
		}

		if !adjustSent {
			adjustSent = true
			count := len(p.conns[nodeID])
			go p.requestAdjust(nodeID, int32(count+1))
		}
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %w", ErrNoLightConn, ctx.Err())
		case <-p.done:
			return nil, fmt.Errorf("%w: pool closed", ErrNoLightConn)
		case <-ch:
			// pool changed, re-check
		}
	}
}

// requestAdjust asks Tai to open more Light connections via Control.
func (p *lightPool) requestAdjust(nodeID string, targetSize int32) {
	data, _ := proto.Marshal(&pb.PoolAdjust{
		Pool:       "light",
		TargetSize: uint32(targetSize),
	})
	if err := p.mgr.sendControl(nodeID, &pb.Payload{
		Namespace: NamespaceControl,
		Kind:      "pool.adjust",
		Data:      data,
	}); err != nil {
		log.Printf("tai-sdk: pool.adjust for %s: %v", nodeID, err)
	}
}

// ---------------------------------------------------------------------------
// lightConn — open stream (H3 or H2)
// ---------------------------------------------------------------------------

func (lc *lightConn) openStream(ctx context.Context) (*LightStream, error) {
	if lc.proto == "H3" {
		return lc.openStreamH3(ctx)
	}
	return lc.openStreamH2(ctx)
}

func (lc *lightConn) openStreamH3(ctx context.Context) (*LightStream, error) {
	stream, err := lc.session.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	lc.active.Add(1)
	return &LightStream{r: stream, w: stream, conn: lc}, nil
}

// h2StreamResult carries the deferred HTTP/2 RoundTrip result.
type h2StreamResult struct {
	resp *http.Response
	err  error
}

func (lc *lightConn) openStreamH2(ctx context.Context) (*LightStream, error) {
	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://tai.local/v1/tai/stream", pr)
	if err != nil {
		pw.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")

	ch := make(chan h2StreamResult, 1)
	go func() {
		resp, err := lc.h2cc.RoundTrip(req)
		if err != nil {
			lc.closed.Store(true)
			pr.CloseWithError(err) // unblock any pw.Write waiting on the pipe
		}
		ch <- h2StreamResult{resp, err}
	}()

	lc.active.Add(1)
	return &LightStream{
		w:      pw,
		conn:   lc,
		h2Pend: ch,
	}, nil
}

// ---------------------------------------------------------------------------
// LightStream — bidirectional byte stream with drain-on-close semantics
// ---------------------------------------------------------------------------

// LightStream is a bidirectional byte stream on a Light connection. Close
// drains the peer's EOF before releasing the stream quota (active--).
type LightStream struct {
	w       io.WriteCloser
	r       io.ReadCloser         // nil for H2 until response headers arrive
	conn    *lightConn
	closed  atomic.Bool           // prevents double active decrement
	gotResp atomic.Bool           // true after first response byte
	h2Pend  <-chan h2StreamResult // H2: pending RoundTrip result
	h2Once  sync.Once
	h2Err   error
}

// resolveH2 blocks until the HTTP/2 response headers arrive, setting s.r.
func (s *LightStream) resolveH2() error {
	s.h2Once.Do(func() {
		if s.h2Pend == nil {
			return // H3: r is already set
		}
		res := <-s.h2Pend
		if res.err != nil {
			s.h2Err = res.err
			return
		}
		if res.resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(res.resp.Body, 512))
			res.resp.Body.Close()
			s.h2Err = fmt.Errorf("stream: status %d: %s", res.resp.StatusCode, body)
			return
		}
		s.r = res.resp.Body
		s.gotResp.Store(true)
	})
	return s.h2Err
}

func (s *LightStream) Read(p []byte) (int, error) {
	if err := s.resolveH2(); err != nil {
		return 0, err
	}
	n, err := s.r.Read(p)
	if n > 0 {
		s.gotResp.Store(true)
	}
	return n, err
}

func (s *LightStream) Write(p []byte) (int, error) {
	return s.w.Write(p)
}

// Close drains the read side, closes the write side, then decrements active.
// Both ends must read the other's EOF for the QUIC/HTTP2 stream to release
// its quota; skipping the drain leaks stream capacity.
func (s *LightStream) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	werr := s.w.Close()
	if s.r != nil {
		io.Copy(io.Discard, s.r)
		s.r.Close()
	} else if s.h2Pend != nil {
		if err := s.resolveH2(); err == nil && s.r != nil {
			io.Copy(io.Discard, s.r)
			s.r.Close()
		}
	}
	s.conn.active.Add(-1)
	return werr
}

// HasResponse reports whether the first response byte has arrived. Used by
// the close-race retry: a stream that failed before response can be retried
// because Tai has not processed it.
func (s *LightStream) HasResponse() bool {
	return s.gotResp.Load()
}

// ---------------------------------------------------------------------------
// initLightHandlers — register H3 and H2 handlers on transportServer
// ---------------------------------------------------------------------------

// initLightHandlers registers the Light connection handlers on the transport
// server so that incoming Light connections are cached in the pool.
func (p *lightPool) initLightHandlers(ts *transportServer) {
	// H3: WebTransport session handler
	ts.setLightHandler(func(nodeID string, sess *webtransport.Session, st *connState) {
		lc := &lightConn{
			nodeID:  nodeID,
			proto:   "H3",
			session: sess,
			pool:    p,
		}
		if !p.addConn(lc) {
			return // pool closed, session will be closed by caller
		}
		log.Printf("tai-sdk: Light H3 connection cached for node %s", nodeID)

		// Block until the WebTransport session ends, then remove.
		<-sess.Context().Done()
		lc.closed.Store(true)
		p.removeConn(lc)
		log.Printf("tai-sdk: Light H3 connection removed for node %s", nodeID)
	})

	// H2: Tai frame handler — routes by connect_type
	ts.setTaiHandler(func(conn net.Conn, st *connState) {
		p.handleTaiConn(conn, st)
	})
}

// handleTaiConn reads the Connect frame from a TCP connection that speaks
// the Tai frame protocol (ALPN tai/2), validates node_token, and routes by
// connect_type: "light" triggers HTTP/2 role reversal, "heavy" is deferred.
func (p *lightPool) handleTaiConn(conn net.Conn, st *connState) {
	var cm pb.Connect
	if err := ReadProtoMsg(conn, &cm, maxConnectFrameSize); err != nil {
		log.Printf("tai-sdk: Tai frame read connect: %v", err)
		return
	}

	info, err := p.auth.ValidateNodeToken(cm.NodeToken)
	if err != nil || info.NodeID != cm.NodeId {
		log.Printf("tai-sdk: Tai frame connect rejected: node=%s", cm.NodeId)
		return
	}

	if !st.authorizeAs(info.NodeID, cm.ConnectType) {
		log.Printf("tai-sdk: Tai frame authorize failed: node=%s type=%s", cm.NodeId, cm.ConnectType)
		return
	}

	switch cm.ConnectType {
	case connTypeLight:
		p.handleLightH2(conn, &cm)
	case connTypeHeavy:
		log.Printf("tai-sdk: Heavy connection from %s deferred to PLAN 9", cm.NodeId)
	default:
		log.Printf("tai-sdk: unknown connect_type %q from %s", cm.ConnectType, cm.NodeId)
	}
}

// handleLightH2 completes the HTTP/2 role reversal: sends ConnectAck, then
// creates an HTTP/2 client on the TCP connection. The connection is cached.
// This function blocks until the connection is removed from the pool or the
// pool shuts down; serveTai closes the underlying conn when it returns.
func (p *lightPool) handleLightH2(conn net.Conn, cm *pb.Connect) {
	if err := WriteProtoMsg(conn, &pb.ConnectAck{}, maxConnectFrameSize); err != nil {
		log.Printf("tai-sdk: Light H2 write connect_ack: %v", err)
		return
	}

	// Role reversal: Yao becomes HTTP/2 client on this TCP connection.
	t := &http2.Transport{
		AllowHTTP:       true,         // connection is already established, no TLS needed at this layer
		ReadIdleTimeout: 20 * time.Second, // send HTTP/2 PING when idle to keep NAT mapping alive
		PingTimeout:     10 * time.Second,
	}
	cc, err := t.NewClientConn(conn)
	if err != nil {
		log.Printf("tai-sdk: Light H2 create client conn: %v", err)
		return
	}

	done := make(chan struct{})
	lc := &lightConn{
		nodeID: cm.NodeId,
		proto:  "H2",
		h2cc:   cc,
		h2conn: conn,
		h2Done: done,
		pool:   p,
	}
	if !p.addConn(lc) {
		return // pool closed
	}
	log.Printf("tai-sdk: Light H2 connection cached for node %s (role reversed)", cm.NodeId)

	// Block until the connection is removed from the pool or the pool shuts
	// down. When this returns, serveTai closes the underlying conn.
	select {
	case <-done:
	case <-p.done:
		lc.closed.Store(true)
		p.removeConn(lc)
	}
	log.Printf("tai-sdk: Light H2 connection released for node %s", cm.NodeId)
}

// ---------------------------------------------------------------------------
// Length-prefixed protobuf helpers for Connect/ConnectAck frames.
// Same wire format as FrameCodec (4-byte big-endian length + protobuf) but
// for arbitrary message types, not just Payload.
// ---------------------------------------------------------------------------

// WriteProtoMsg writes a length-prefixed protobuf message. Used for
// Connect/ConnectAck frames on Light and Heavy connections.
func WriteProtoMsg(w io.Writer, msg proto.Message, maxSize int) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(data) > maxSize {
		return ErrFrameTooLarge
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// ReadProtoMsg reads a length-prefixed protobuf message. Used for
// Connect/ConnectAck frames on Light and Heavy connections.
func ReadProtoMsg(r io.Reader, msg proto.Message, maxSize int) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header)
	if int(length) > maxSize {
		return ErrFrameTooLarge
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	return proto.Unmarshal(data, msg)
}
