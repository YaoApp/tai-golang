package tai

import (
	"context"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// controlStream manages one bidirectional Control stream for a Tai node.
type controlStream struct {
	nodeID string
	r      io.Reader
	w      io.Writer
	f      http.Flusher
	codec  *FrameCodec
	mgr    *nodeManager
	auth   AuthProvider

	mu sync.Mutex // serializes writes
}

func newControlStream(nodeID string, r io.Reader, w http.ResponseWriter, mgr *nodeManager, auth AuthProvider, codec *FrameCodec) *controlStream {
	f, _ := w.(http.Flusher)
	return &controlStream{
		nodeID: nodeID,
		r:      r,
		w:      w,
		f:      f,
		codec:  codec,
		mgr:    mgr,
		auth:   auth,
	}
}

// run is the main message loop. It reads frames, dispatches by kind, and
// writes responses. It blocks until the stream closes or ctx is cancelled.
func (cs *controlStream) run(ctx context.Context) {
	defer cs.mgr.unregister(cs.nodeID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := cs.codec.ReadFrame(cs.r)
		if err != nil {
			if err != io.EOF {
				log.Printf("tai-sdk: control read error node=%s: %v", cs.nodeID, err)
			}
			return
		}

		cs.dispatch(msg)
	}
}

func (cs *controlStream) dispatch(msg *pb.ControlMessage) {
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

	ack, err := cs.mgr.register(cs.nodeID, reg, cs.auth)
	if err != nil {
		log.Printf("tai-sdk: register node=%s failed: %v", cs.nodeID, err)
		return
	}

	cs.mgr.setStream(cs.nodeID, cs)

	ackData, err := proto.Marshal(ack)
	if err != nil {
		log.Printf("tai-sdk: marshal register_ack: %v", err)
		return
	}

	cs.writeMsg(&pb.ControlMessage{Kind: "register_ack", Data: ackData})
	log.Printf("tai-sdk: node registered: id=%s tai_id=%s", cs.nodeID, ack.TaiId)
}

func (cs *controlStream) handlePing(data []byte) {
	ping := &pb.Ping{}
	if err := proto.Unmarshal(data, ping); err != nil {
		log.Printf("tai-sdk: unmarshal ping: %v", err)
		return
	}

	cs.mgr.updatePing(cs.nodeID, ping)

	pong := &pb.Pong{Timestamp: ping.Timestamp}
	pongData, err := proto.Marshal(pong)
	if err != nil {
		log.Printf("tai-sdk: marshal pong: %v", err)
		return
	}

	cs.writeMsg(&pb.ControlMessage{Kind: "pong", Data: pongData})
}

// writeMsg writes a ControlMessage frame and flushes.
func (cs *controlStream) writeMsg(msg *pb.ControlMessage) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := cs.codec.WriteFrame(cs.w, msg); err != nil {
		return err
	}
	if cs.f != nil {
		cs.f.Flush()
	}
	return nil
}

// marshalAny marshals any proto message to bytes.
func marshalAny(m proto.Message) ([]byte, error) {
	return proto.Marshal(m)
}
