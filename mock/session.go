package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// MockSession is a server-side session handle used in tests.
// Receives commands on ctrl stream, sends events on data stream.
type MockSession struct {
	ID      string
	Request *types.SessionRequest
	ctrl    *MockLightStream
	data    *MockLightStream
	codec   *transport.FrameCodec
	dataSeq uint64
	mu      sync.Mutex
	closed  bool
}

// Send writes an event frame to the data stream.
func (ms *MockSession) Send(kind string, data any) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.closed {
		return fmt.Errorf("mock session closed")
	}
	if ms.data == nil {
		return fmt.Errorf("data stream not bound")
	}

	var payload []byte
	if data != nil {
		var err error
		payload, err = json.Marshal(data)
		if err != nil {
			return err
		}
	}

	ms.dataSeq++
	msgData, err := proto.Marshal(&pb.SessionMessage{
		Seq:  ms.dataSeq,
		Kind: kind,
		Data: payload,
	})
	if err != nil {
		return err
	}
	return ms.codec.WriteFrame(ms.data, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "event",
		Data:      msgData,
	})
}

// RecvCtrl reads the next command from the ctrl stream.
func (ms *MockSession) RecvCtrl() (*pb.SessionMessage, error) {
	frame, err := ms.ctrl.RecvFrame()
	if err != nil {
		return nil, err
	}
	var msg pb.SessionMessage
	if err := proto.Unmarshal(frame.Data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal ctrl: %w", err)
	}
	return &msg, nil
}

// SendClosed sends a "closed" handshake frame and closes streams.
func (ms *MockSession) SendClosed(reason string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.closed {
		return nil
	}
	ms.closed = true

	closedData, _ := proto.Marshal(&pb.SessionClosed{
		SessionId: ms.ID,
		Reason:    reason,
	})
	ms.codec.WriteFrame(ms.ctrl, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "closed",
		Data:      closedData,
	})
	ms.ctrl.Close()
	if ms.data != nil {
		ms.data.Close()
	}
	return nil
}

// Close closes the mock session streams.
func (ms *MockSession) Close() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.closed {
		return
	}
	ms.closed = true
	ms.ctrl.Close()
	if ms.data != nil {
		ms.data.Close()
	}
}

// bindData binds the data stream to this session.
func (ms *MockSession) bindData(data *MockLightStream) {
	ms.mu.Lock()
	ms.data = data
	ms.mu.Unlock()
}

// SessionHandler processes session Light streams on a MockNode.
// It handles "open" and "data" first frames, pairs them, and invokes onOpen.
type SessionHandler struct {
	codec  *transport.FrameCodec
	onOpen func(sess *MockSession)

	mu       sync.Mutex
	sessions map[string]*MockSession
}

// NewSessionHandler creates a handler with the given callback.
func NewSessionHandler(onOpen func(sess *MockSession)) *SessionHandler {
	return &SessionHandler{
		codec:    transport.DefaultCodec(),
		onOpen:   onOpen,
		sessions: make(map[string]*MockSession),
	}
}

// HandleStream dispatches a session-namespace MockLightStream by FirstFrame.Kind.
func (sh *SessionHandler) HandleStream(stream *MockLightStream) {
	switch stream.FirstFrame.Kind {
	case "open":
		sh.handleOpen(stream)
	case "data":
		sh.handleData(stream)
	default:
		stream.Close()
	}
}

func (sh *SessionHandler) handleOpen(ctrl *MockLightStream) {
	var open pb.SessionOpen
	if err := proto.Unmarshal(ctrl.FirstFrame.Data, &open); err != nil {
		ctrl.Close()
		return
	}

	req := &types.SessionRequest{
		Runtime:   open.Request.GetRuntime(),
		Isolation: open.Request.GetIsolation(),
	}

	ms := &MockSession{
		ID:      open.SessionId,
		Request: req,
		ctrl:    ctrl,
		codec:   sh.codec,
	}

	sh.mu.Lock()
	sh.sessions[open.SessionId] = ms
	sh.mu.Unlock()

	// Send opened acknowledgment
	openedData, _ := proto.Marshal(&pb.SessionOpened{
		SessionId: open.SessionId,
		Runtime:   req.Runtime,
		Isolation: req.Isolation,
	})
	sh.codec.WriteFrame(ctrl, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "opened",
		Data:      openedData,
	})

	if sh.onOpen != nil {
		sh.onOpen(ms)
	}
}

func (sh *SessionHandler) handleData(dataStream *MockLightStream) {
	var sd pb.SessionData
	if err := proto.Unmarshal(dataStream.FirstFrame.Data, &sd); err != nil {
		dataStream.Close()
		return
	}

	sh.mu.Lock()
	ms, ok := sh.sessions[sd.SessionId]
	sh.mu.Unlock()
	if !ok {
		dataStream.Close()
		return
	}

	ms.bindData(dataStream)
}

// Session returns the MockSession for the given ID, or nil.
func (sh *SessionHandler) Session(id string) *MockSession {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return sh.sessions[id]
}

// AcceptAndHandle accepts streams from a MockLightConn and routes session streams.
// Blocks until the connection is closed or ctx is cancelled.
func (sh *SessionHandler) AcceptAndHandle(ctx context.Context, lc *MockLightConn) {
	for {
		stream, err := lc.AcceptStream(ctx)
		if err != nil {
			return
		}
		if stream.FirstFrame.Namespace == types.NamespaceSession {
			go sh.HandleStream(stream)
		} else {
			stream.Close()
		}
	}
}
