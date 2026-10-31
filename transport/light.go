package transport

import (
	"context"
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
	"github.com/yaoapp/tai-golang/types"
)

// maxConnectFrameSize bounds the Connect/ConnectAck frame.
const maxConnectFrameSize = 4 * 1024

// lightPool caches authorized Light connections from Tai nodes and opens
// streams on them.
type lightPool struct {
	mu      sync.Mutex
	conns   map[string][]*lightConn
	cfg     *pb.PoolConfig
	codec   *FrameCodec
	mgr     *NodeManager
	auth    types.AuthProvider
	changed chan struct{}
	done    chan struct{}
	closed  bool
}

func newLightPool(cfg *pb.PoolConfig, codec *FrameCodec, mgr *NodeManager, auth types.AuthProvider) *lightPool {
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

type lightConn struct {
	nodeID  string
	proto   string
	session *webtransport.Session
	h2cc    *http2.ClientConn
	h2conn  net.Conn
	h2Done  chan struct{}
	active  atomic.Int32
	closed  atomic.Bool
	pool    *lightPool
}

func (p *lightPool) updateConfig(cfg *pb.PoolConfig) {
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
}

func (p *lightPool) connCount(nodeID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns[nodeID])
}

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
	close(ch)
	return true
}

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

	if lc.h2Done != nil {
		select {
		case <-lc.h2Done:
		default:
			close(lc.h2Done)
		}
	}
}

func (p *lightPool) maxStreams() int32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cfg != nil && p.cfg.LightMaxStreams > 0 {
		return int32(p.cfg.LightMaxStreams)
	}
	return 256
}

// OpenStream opens a bidirectional stream to nodeID.
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
			return nil, fmt.Errorf("%w: %w", types.ErrNoLightConn, ctx.Err())
		case <-p.done:
			return nil, fmt.Errorf("%w: pool closed", types.ErrNoLightConn)
		case <-ch:
		}
	}
}

func (p *lightPool) requestAdjust(nodeID string, targetSize int32) {
	data, _ := proto.Marshal(&pb.PoolAdjust{
		Pool:       "light",
		TargetSize: uint32(targetSize),
	})
	if err := p.mgr.SendControl(nodeID, &pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      "pool.adjust",
		Data:      data,
	}); err != nil {
		log.Printf("tai-sdk: pool.adjust for %s: %v", nodeID, err)
	}
}

// ---------------------------------------------------------------------------
// lightConn — open stream
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
			pr.CloseWithError(err)
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
// LightStream
// ---------------------------------------------------------------------------

// LightStream is a bidirectional byte stream on a Light connection.
type LightStream struct {
	w       io.WriteCloser
	r       io.ReadCloser
	conn    *lightConn
	closed  atomic.Bool
	gotResp atomic.Bool
	h2Pend  <-chan h2StreamResult
	h2Once  sync.Once
	h2Err   error
}

func (s *LightStream) resolveH2() error {
	s.h2Once.Do(func() {
		if s.h2Pend == nil {
			return
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

// Close drains the read side, closes write, and decrements active.
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

// HasResponse reports whether the first response byte has arrived.
func (s *LightStream) HasResponse() bool {
	return s.gotResp.Load()
}

// ---------------------------------------------------------------------------
// initLightHandlers
// ---------------------------------------------------------------------------

func (p *lightPool) initLightHandlers(ts *Server) {
	ts.SetLightHandler(func(nodeID string, sess *webtransport.Session, st *ConnState) {
		lc := &lightConn{
			nodeID:  nodeID,
			proto:   "H3",
			session: sess,
			pool:    p,
		}
		if !p.addConn(lc) {
			return
		}
		log.Printf("tai-sdk: Light H3 connection cached for node %s", nodeID)

		<-sess.Context().Done()
		lc.closed.Store(true)
		p.removeConn(lc)
		log.Printf("tai-sdk: Light H3 connection removed for node %s", nodeID)
	})

	ts.SetTaiHandler(func(conn net.Conn, st *ConnState) {
		p.handleTaiConn(conn, st)
	})
}

func (p *lightPool) handleTaiConn(conn net.Conn, st *ConnState) {
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

	if !st.AuthorizeAs(info.NodeID, cm.ConnectType) {
		log.Printf("tai-sdk: Tai frame authorize failed: node=%s type=%s", cm.NodeId, cm.ConnectType)
		return
	}

	switch cm.ConnectType {
	case ConnTypeLight:
		p.handleLightH2(conn, &cm)
	case ConnTypeHeavy:
		log.Printf("tai-sdk: Heavy connection from %s deferred to PLAN 9", cm.NodeId)
	default:
		log.Printf("tai-sdk: unknown connect_type %q from %s", cm.ConnectType, cm.NodeId)
	}
}

func (p *lightPool) handleLightH2(conn net.Conn, cm *pb.Connect) {
	if err := WriteProtoMsg(conn, &pb.ConnectAck{}, maxConnectFrameSize); err != nil {
		log.Printf("tai-sdk: Light H2 write connect_ack: %v", err)
		return
	}

	t := &http2.Transport{
		AllowHTTP:       true,
		ReadIdleTimeout: 20 * time.Second,
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
		return
	}
	log.Printf("tai-sdk: Light H2 connection cached for node %s (role reversed)", cm.NodeId)

	select {
	case <-done:
	case <-p.done:
		lc.closed.Store(true)
		p.removeConn(lc)
	}
	log.Printf("tai-sdk: Light H2 connection released for node %s", cm.NodeId)
}

// ---------------------------------------------------------------------------
// Length-prefixed protobuf helpers (reused by mock)
// ---------------------------------------------------------------------------

// NewLightStreamForTest creates a LightStream from raw io handles.
// Intended for unit tests only.
func NewLightStreamForTest(r io.ReadCloser, w io.WriteCloser) *LightStream {
	return &LightStream{
		r:    r,
		w:    w,
		conn: &lightConn{},
	}
}
