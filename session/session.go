// Package session implements session lifecycle management over Tai Light streams.
// Each session occupies 2 Light streams: ctrl (bidirectional) and data (Tai→SDK unidirectional).
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// Session represents an active agent session on a Tai node.
// Implements the tai.Session interface.
type Session struct {
	id      string
	nodeID  string
	status  atomic.Value // types.SessionStatus
	ctrl    *transport.LightStream
	data    *transport.LightStream
	codec   *transport.FrameCodec
	ctrlSeq atomic.Uint64

	// ctrl stream reads are split: handshake frames (opened/closed/error) are
	// consumed by Open, business frames go to the actions channel.
	actions chan *types.SessionMsg

	mu       sync.Mutex // protects closed flag
	writeMu  sync.Mutex // serializes ctrl stream writes (WriteFrame is not atomic)
	closed   bool
	done     chan struct{} // signals remote confirmed closure
	doneOnce sync.Once
}

// New creates a Session with ctrl and data streams already established.
// Starts the background ctrl read loop.
func New(id, nodeID string, ctrl, data *transport.LightStream, codec *transport.FrameCodec) *Session {
	s := &Session{
		id:      id,
		nodeID:  nodeID,
		ctrl:    ctrl,
		data:    data,
		codec:   codec,
		actions: make(chan *types.SessionMsg, 16),
		done:    make(chan struct{}),
	}
	s.status.Store(types.SessionRunning)
	go s.readCtrlLoop()
	return s
}

// ID returns the session identifier.
func (s *Session) ID() string { return s.id }

// NodeID returns the target node identifier.
func (s *Session) NodeID() string { return s.nodeID }

// Status returns the current lifecycle state.
func (s *Session) Status() types.SessionStatus {
	return s.status.Load().(types.SessionStatus)
}

// Prompt sends a user message to the agent.
func (s *Session) Prompt(msg *types.UserMessage) error {
	return s.sendCtrl("prompt", msg)
}

// Followup sends a follow-up message and waits for disposition.
func (s *Session) Followup(msg *types.UserMessage) (types.Disposition, error) {
	if err := s.sendCtrl("followup", msg); err != nil {
		return "", err
	}
	return s.waitDisposition()
}

// Cancel cancels the current turn.
func (s *Session) Cancel() error {
	return s.sendCtrl("cancel", nil)
}

// Steer sends a steering message and waits for disposition.
func (s *Session) Steer(msg *types.UserMessage) (types.Disposition, error) {
	if err := s.sendCtrl("steer", msg); err != nil {
		return "", err
	}
	return s.waitDisposition()
}

// UpdateQueue manages the Steer/Followup message queue.
func (s *Session) UpdateQueue(action *types.QueueAction) error {
	return s.sendCtrl("update_queue", action)
}

// ResolvePermission replies to a permission request from the agent.
func (s *Session) ResolvePermission(requestID string, resp *types.PermissionResponse) error {
	return s.sendCtrl("resolve_permission", map[string]any{
		"requestId": requestID,
		"response":  resp,
	})
}

// ResolveElicitation replies to a structured input request from the agent.
func (s *Session) ResolveElicitation(id string, resp *types.ElicitationResponse) error {
	return s.sendCtrl("resolve_elicitation", map[string]any{
		"id":       id,
		"response": resp,
	})
}

// SetConfigOption sets a runtime configuration option.
func (s *Session) SetConfigOption(configID string, value *types.ConfigOptionValue) error {
	return s.sendCtrl("set_config_option", map[string]any{
		"configId": configID,
		"value":    value,
	})
}

// UpdateSession applies runtime configuration changes.
func (s *Session) UpdateSession(_ context.Context, opts *types.SessionUpdateOptions) error {
	return s.sendCtrl("update_session", opts)
}

// StartRealtime is a placeholder for the Realtime extension.
func (s *Session) StartRealtime(_ *types.RealtimeConfig) error {
	return fmt.Errorf("tai: realtime not implemented")
}

// StopRealtime is a placeholder for the Realtime extension.
func (s *Session) StopRealtime() error {
	return fmt.Errorf("tai: realtime not implemented")
}

// RecvEvent reads the next event from the data stream. Blocks.
// Returns io.EOF when the session ends.
func (s *Session) RecvEvent() (*types.SessionMsg, error) {
	frame, err := s.codec.ReadFrame(s.data)
	if err != nil {
		return nil, err
	}
	var msg pb.SessionMessage
	if err := proto.Unmarshal(frame.Data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal event: %w", err)
	}
	return &types.SessionMsg{
		Seq:  msg.Seq,
		Kind: msg.Kind,
		Data: msg.Data,
	}, nil
}

// RecvAction reads the next action response from the ctrl stream. Blocks.
// Action responses include permission requests, elicitation requests, etc.
func (s *Session) RecvAction() (*types.SessionMsg, error) {
	select {
	case msg, ok := <-s.actions:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-s.done:
		return nil, types.ErrSessionClosed
	}
}

// Close terminates the session gracefully.
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// Send close before marking closed (sendCtrl checks the flag).
	s.status.Store(types.SessionClosing)
	_ = s.sendCtrl("close", nil)

	// Now mark closed to reject further commands.
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	select {
	case <-s.done:
	case <-ctx.Done():
	}

	s.ctrl.Close()
	s.data.Close()
	s.status.Store(types.SessionClosed)
	return nil
}

// sendCtrl is the unified send logic for all ctrl stream commands.
func (s *Session) sendCtrl(kind string, payload any) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return types.ErrSessionClosed
	}
	s.mu.Unlock()

	var data []byte
	if payload != nil {
		var err error
		data, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}

	seq := s.ctrlSeq.Add(1)
	msgData, err := proto.Marshal(&pb.SessionMessage{
		Seq:  seq,
		Kind: kind,
		Data: data,
	})
	if err != nil {
		return err
	}

	// Serialize writes: WriteFrame does 4-byte header + body in two calls;
	// concurrent writes without the lock can interleave and corrupt frames.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.codec.WriteFrame(s.ctrl, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "message",
		Data:      msgData,
	})
}

// readCtrlLoop runs in a background goroutine. Reads ctrl stream frames,
// dispatches handshake frames (closed/error) and business frames (actions).
func (s *Session) readCtrlLoop() {
	defer close(s.actions)
	for {
		frame, err := s.codec.ReadFrame(s.ctrl)
		if err != nil {
			s.markClosed()
			return
		}

		switch frame.Kind {
		case "closed":
			s.markClosed()
			return
		case "error":
			s.markClosed()
			return
		}

		var msg pb.SessionMessage
		if err := proto.Unmarshal(frame.Data, &msg); err != nil {
			continue
		}
		select {
		case s.actions <- &types.SessionMsg{Seq: msg.Seq, Kind: msg.Kind, Data: msg.Data}:
		case <-s.done:
			return
		}
	}
}

// waitDisposition reads the next action expecting a disposition response.
func (s *Session) waitDisposition() (types.Disposition, error) {
	msg, err := s.RecvAction()
	if err != nil {
		return "", err
	}
	if msg.Kind != "disposition" {
		return "", fmt.Errorf("expected disposition, got %q", msg.Kind)
	}
	var resp struct {
		Disposition types.Disposition `json:"disposition"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return "", err
	}
	return resp.Disposition, nil
}

// markClosed transitions to closed state and signals done.
func (s *Session) markClosed() {
	s.doneOnce.Do(func() { close(s.done) })
	s.mu.Lock()
	s.closed = true
	s.status.Store(types.SessionClosed)
	s.mu.Unlock()
}
