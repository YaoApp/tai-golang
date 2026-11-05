package transport

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// controlStream manages one bidirectional Control stream for a Tai node.
// It implements types.ControlWriter.
type controlStream struct {
	nodeID string
	r      io.Reader
	w      io.Writer
	f      http.Flusher
	codec  *FrameCodec
	reg    types.NodeRegistry
	auth   types.AuthProvider

	mu     sync.Mutex
	closed bool
}

// errControlClosed is returned by WriteControl after the Control stream's
// handler has returned.
var errControlClosed = errors.New("tai: control stream closed")

func newControlStream(nodeID string, r io.Reader, w http.ResponseWriter, reg types.NodeRegistry, auth types.AuthProvider, codec *FrameCodec) *controlStream {
	f, _ := w.(http.Flusher)
	return &controlStream{
		nodeID: nodeID,
		r:      r,
		w:      w,
		f:      f,
		codec:  codec,
		reg:    reg,
		auth:   auth,
	}
}

// run is the main message loop.
func (cs *controlStream) run(ctx context.Context) {
	defer cs.close()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := cs.codec.ReadFrame(cs.r)
		if err != nil {
			if !isNormalClose(err) {
				log.Printf("tai-sdk: control read error node=%s: %v", cs.nodeID, err)
			}
			return
		}

		cs.dispatch(msg)
	}
}

func (cs *controlStream) close() {
	cs.reg.UnbindStream(cs.nodeID, cs)
	cs.mu.Lock()
	cs.closed = true
	cs.mu.Unlock()
}

func (cs *controlStream) dispatch(msg *pb.Payload) {
	if msg.Namespace != types.NamespaceControl {
		log.Printf("tai-sdk: unknown control namespace=%q kind=%q node=%s", msg.Namespace, msg.Kind, cs.nodeID)
		return
	}
	switch msg.Kind {
	case "register":
		cs.handleRegister(msg.Data)
	case "ping":
		cs.handlePing(msg.Data)
	default:
		log.Printf("tai-sdk: unknown control kind=%q node=%s", msg.Kind, cs.nodeID)
	}
}

func (cs *controlStream) handleRegister(data []byte) {
	reg := &pb.Register{}
	if err := proto.Unmarshal(data, reg); err != nil {
		log.Printf("tai-sdk: unmarshal register: %v", err)
		return
	}

	ack, err := cs.reg.Register(cs.nodeID, reg, cs.auth)
	if err != nil {
		log.Printf("tai-sdk: register node=%s failed: %v", cs.nodeID, err)
		return
	}

	cs.reg.BindStream(cs.nodeID, cs)

	ackData, err := proto.Marshal(ack)
	if err != nil {
		log.Printf("tai-sdk: marshal register_ack: %v", err)
		return
	}

	cs.WriteControl(&pb.Payload{Namespace: types.NamespaceControl, Kind: "register_ack", Data: ackData})
	log.Printf("tai-sdk: node registered: id=%s tai_id=%s", cs.nodeID, ack.TaiId)
}

func (cs *controlStream) handlePing(data []byte) {
	ping := &pb.Ping{}
	if err := proto.Unmarshal(data, ping); err != nil {
		log.Printf("tai-sdk: unmarshal ping: %v", err)
		return
	}

	cs.reg.UpdatePing(cs.nodeID, ping)

	pong := &pb.Pong{Timestamp: ping.Timestamp}
	pongData, err := proto.Marshal(pong)
	if err != nil {
		log.Printf("tai-sdk: marshal pong: %v", err)
		return
	}

	cs.WriteControl(&pb.Payload{Namespace: types.NamespaceControl, Kind: "pong", Data: pongData})
}

// WriteControl implements types.ControlWriter.
func (cs *controlStream) WriteControl(msg *pb.Payload) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.closed {
		return errControlClosed
	}
	if err := cs.codec.WriteFrame(cs.w, msg); err != nil {
		return err
	}
	if cs.f != nil {
		cs.f.Flush()
	}
	return nil
}

// isNormalClose reports whether err represents a normal connection teardown
// that should not be logged as an error.
func isNormalClose(err error) bool {
	if err == nil || err == io.EOF {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	// QUIC/H3 normal close: "H3 error (0x0)" or application error code 0.
	s := err.Error()
	return strings.Contains(s, "(0x0)") || strings.Contains(s, "NO_ERROR")
}
