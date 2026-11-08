// Package session implements session lifecycle management over Tai Light streams.
// Each session occupies 2 Light streams: ctrl (bidirectional) and data (Tai→SDK unidirectional).
package session

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// Session represents an active agent session on a Tai node.
// Implements the types.Session interface.
type Session struct {
	id      string
	nodeID  string
	status  atomic.Value // types.SessionStatus
	ctrl    io.ReadWriteCloser
	data    io.ReadWriteCloser
	codec   *transport.FrameCodec
	ctrlSeq atomic.Uint64
	dataSeq atomic.Uint64 // tracks last received data stream seq

	// ctrl stream reads are split: handshake frames (opened/closed/error) are
	// consumed by Open, business frames go to the actions channel.
	actions chan *types.SessionMsg

	mu       sync.Mutex // protects closed, disconnected, ctrl, data
	writeMu  sync.Mutex // serializes ctrl stream writes (WriteFrame is not atomic)
	closed   bool
	done     chan struct{} // signals remote confirmed closure
	doneOnce sync.Once

	// Resume support
	resumeFn   types.ResumeFunc         // opens new streams (nil = resume disabled)
	onEvent    types.OnSessionEventFunc // lifecycle event callback (may be nil)
	buffer     *recvBuffer              // received events for seq verification
	resuming   atomic.Bool              // true while resume is in progress
	resumeDone chan struct{}            // closed when resume completes or fails

	// Realtime state (protected by mu unless otherwise noted)
	openStreamFn   OpenStreamFunc         // opens Light streams for H2 audio
	dgConn         transport.DatagramConn // H3 datagram I/O; nil on H2
	audioChannelID uint32
	audioSeq       atomic.Uint32          // outbound datagram sequence counter
	audioOut       chan *types.AudioFrame // downstream audio frames
	audioCancel    context.CancelFunc     // cancels H2 audio stream goroutine; nil on H3
	audioStream    io.ReadWriteCloser     // H2 audio bidi stream; nil on H3
	audioWriteMu   sync.Mutex             // serializes H2 audio stream writes

	// Datagram demux (H3 only): single goroutine dispatches by channel ID.
	dgCancel context.CancelFunc // cancels the shared datagram read loop

	// Motion + render stream state (protected by mu)
	motionChannelID uint32
	motionOut       chan *types.MotionFrame // downstream motion frames; nil when motion disabled
	motionStream    io.ReadWriteCloser      // H2 motion uni stream; nil on H3
	motionCancel    context.CancelFunc      // cancels H2 motion read goroutine; nil on H3
	renderOut       chan *types.RenderEvent // downstream render stream events (transcript + emotion)
	renderStream    io.ReadWriteCloser      // dedicated render uni stream; nil before StartRealtime
	renderCancel    context.CancelFunc
	realtimeReady   chan struct{} // closed once StartRealtime completed

	// identity is the caller identity carried by the first frame of the
	// session streams. It is optional: nil means the session has no user
	// (system task / unattended node). The value is fixed at construction.
	identity *pb.Identity
}

// OpenStreamFunc opens a Light stream to a node. Used by H2 audio to open
// the audio bidi stream. Defaults to transport.PkgOpenLightStream.
type OpenStreamFunc func(ctx context.Context, nodeID string, firstFrame *pb.Payload) (io.ReadWriteCloser, error)

// Options configures optional Session behavior.
type Options struct {
	ResumeFn     types.ResumeFunc         // enables automatic resume on disconnect
	OnEvent      types.OnSessionEventFunc // lifecycle event callback
	DatagramConn transport.DatagramConn   // QUIC datagram I/O for Realtime audio (nil on H2)
	OpenStreamFn OpenStreamFunc           // opens Light streams; nil = transport.PkgOpenLightStream
	Identity     *pb.Identity             // caller identity carried by sub-stream first frames (optional)
}

// New creates a Session with ctrl and data streams already established.
// Starts the background ctrl read loop.
func New(id, nodeID string, ctrl, data io.ReadWriteCloser, codec *transport.FrameCodec, opts ...Options) *Session {
	s := &Session{
		id:            id,
		nodeID:        nodeID,
		ctrl:          ctrl,
		data:          data,
		codec:         codec,
		actions:       make(chan *types.SessionMsg, 16),
		done:          make(chan struct{}),
		buffer:        newRecvBuffer(defaultRecvBufferCapacity),
		realtimeReady: make(chan struct{}),
	}
	if len(opts) > 0 {
		s.resumeFn = opts[0].ResumeFn
		s.onEvent = opts[0].OnEvent
		s.dgConn = opts[0].DatagramConn
		s.openStreamFn = opts[0].OpenStreamFn
		s.identity = opts[0].Identity
	}
	if s.openStreamFn == nil {
		s.openStreamFn = transport.PkgOpenLightStream
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

// Done returns a channel that is closed when the session terminates.
func (s *Session) Done() <-chan struct{} {
	return s.done
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

// StartRealtime upgrades the session to Realtime mode.
// Sends "realtime.start" on the ctrl stream and blocks until Tai replies
// with "realtime.started" carrying audio_channel_id and capabilities.
// On H3 transports, a datagram receive goroutine is started immediately.
// On H2 transports, an audio bidi stream is opened as fallback.
func (s *Session) StartRealtime(req *types.RealtimeRequest) error {
	if req == nil {
		return fmt.Errorf("StartRealtime: nil request")
	}

	s.mu.Lock()
	if s.audioChannelID != 0 {
		s.mu.Unlock()
		return fmt.Errorf("realtime already active")
	}
	s.mu.Unlock()

	// Drain actions buffered before we start the handshake.
	s.drainStaleActions()

	if err := s.sendCtrl("realtime.start", req); err != nil {
		return fmt.Errorf("send realtime.start: %w", err)
	}

	// Block until Tai confirms with realtime.started.
	// Skip unrelated actions (e.g. workspace_change) that arrive
	// asynchronously between sending realtime.start and receiving the reply.
	// A 30-second deadline guards against Tai never responding.
	deadline := time.After(30 * time.Second)
	var msg *types.SessionMsg
	for {
		select {
		case m, ok := <-s.actions:
			if !ok {
				return fmt.Errorf("wait realtime.started: %w", io.EOF)
			}
			switch m.Kind {
			case "realtime.started":
				msg = m
			case "realtime.error":
				return fmt.Errorf("realtime start rejected: %s", string(m.Data))
			default:
				continue
			}
		case <-s.done:
			return fmt.Errorf("wait realtime.started: session closed")
		case <-deadline:
			return fmt.Errorf("timeout waiting for realtime.started (30s)")
		}
		break
	}

	var started struct {
		Capabilities    json.RawMessage `json:"capabilities"`
		AudioChannelID  uint32          `json:"audio_channel_id"`
		MotionChannelID uint32          `json:"motion_channel_id"`
	}
	if err := json.Unmarshal(msg.Data, &started); err != nil {
		return fmt.Errorf("parse realtime.started: %w", err)
	}

	audioOut := make(chan *types.AudioFrame, 64)
	audioCtx, audioCancel := context.WithCancel(context.Background())
	renderOut := make(chan *types.RenderEvent, 16)
	renderCtx, renderCancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.audioChannelID = started.AudioChannelID
	s.audioOut = audioOut
	s.audioCancel = audioCancel
	s.renderOut = renderOut
	s.renderCancel = renderCancel
	s.mu.Unlock()

	// Start audio I/O based on transport protocol.
	if s.dgConn != nil && s.dgConn.Proto() == "H3" {
		// H3: single datagram read loop demuxes by channel ID.
		var motionOut chan *types.MotionFrame
		if started.MotionChannelID != 0 {
			motionOut = make(chan *types.MotionFrame, 8)
		}
		dgCtx, dgCancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.dgCancel = dgCancel
		if motionOut != nil {
			s.motionChannelID = started.MotionChannelID
			s.motionOut = motionOut
		}
		s.mu.Unlock()
		go s.recvDatagramLoop(dgCtx, started.AudioChannelID, audioOut, started.MotionChannelID, motionOut)
	} else {
		// H2: audio over a dedicated bidi stream.
		if err := s.openAudioStreamH2(audioCtx, started.AudioChannelID, audioOut); err != nil {
			audioCancel()
			renderCancel()
			s.mu.Lock()
			s.audioChannelID = 0
			s.audioOut = nil
			s.audioCancel = nil
			s.renderOut = nil
			s.renderCancel = nil
			s.mu.Unlock()
			return fmt.Errorf("open audio stream: %w", err)
		}

		// H2: motion over dedicated uni stream (render-protocol.md §2.13).
		if started.MotionChannelID != 0 {
			motionOut := make(chan *types.MotionFrame, 8)
			motionCtx, motionCancel := context.WithCancel(context.Background())
			s.mu.Lock()
			s.motionChannelID = started.MotionChannelID
			s.motionOut = motionOut
			s.motionCancel = motionCancel
			s.mu.Unlock()
			if err := s.openMotionStreamH2(motionCtx, motionOut); err != nil {
				motionCancel()
				s.mu.Lock()
				s.motionChannelID = 0
				s.motionOut = nil
				s.motionCancel = nil
				s.mu.Unlock()
				// Non-fatal: continue without motion.
			}
		}
	}

	// Open dedicated render uni stream (transcript + emotion).
	if err := s.openRenderStream(renderCtx, renderOut); err != nil {
		// Render stream failed — tear down audio and motion.
		audioCancel()
		renderCancel()
		if s.dgCancel != nil {
			s.dgCancel()
		}
		s.mu.Lock()
		motionCancelFn := s.motionCancel
		s.audioChannelID = 0
		s.audioOut = nil
		s.audioCancel = nil
		s.dgCancel = nil
		s.motionChannelID = 0
		s.motionOut = nil
		s.motionCancel = nil
		s.motionStream = nil
		s.renderOut = nil
		s.renderStream = nil
		s.renderCancel = nil
		s.mu.Unlock()
		if motionCancelFn != nil {
			motionCancelFn()
		}
		return fmt.Errorf("open render stream: %w", err)
	}

	s.mu.Lock()
	if s.realtimeReady != nil {
		close(s.realtimeReady)
		s.realtimeReady = nil
	}
	s.mu.Unlock()
	return nil
}

// WaitRealtime blocks until Realtime is active or ctx is done.
func (s *Session) WaitRealtime(ctx context.Context) error {
	s.mu.Lock()
	ready := s.realtimeReady
	active := s.audioChannelID != 0
	s.mu.Unlock()
	if active || ready == nil {
		return nil
	}
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return fmt.Errorf("session closed")
	}
}

// UpdateRealtime applies runtime configuration changes to the active Realtime session.
// The confirmation (realtime.updated) arrives asynchronously via RecvAction.
func (s *Session) UpdateRealtime(opts *types.RealtimeUpdateOptions) error {
	s.mu.Lock()
	active := s.audioChannelID != 0
	s.mu.Unlock()
	if !active {
		return fmt.Errorf("realtime not active")
	}
	return s.sendCtrl("realtime.update", opts)
}

// StopRealtime stops the active Realtime session.
// Cancels audio I/O goroutines, closes H2 audio stream if present,
// then notifies Tai via ctrl.
func (s *Session) StopRealtime() error {
	s.mu.Lock()
	cancelFn := s.audioCancel
	audioStream := s.audioStream
	channelID := s.audioChannelID
	if channelID == 0 {
		s.mu.Unlock()
		return fmt.Errorf("realtime not active")
	}
	s.audioCancel = nil
	s.audioStream = nil
	s.audioChannelID = 0
	s.audioOut = nil
	s.audioSeq.Store(0)

	dgCancelFn := s.dgCancel
	s.dgCancel = nil
	motionCancelFn := s.motionCancel
	motionStream := s.motionStream
	s.motionCancel = nil
	s.motionStream = nil
	s.motionChannelID = 0
	s.motionOut = nil

	renderCancelFn := s.renderCancel
	s.renderCancel = nil
	s.renderStream = nil
	s.renderOut = nil
	s.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	if dgCancelFn != nil {
		dgCancelFn()
	}
	if motionCancelFn != nil {
		motionCancelFn()
	}
	if renderCancelFn != nil {
		renderCancelFn() // triggers stream.Close() in readRenderStream's cancel goroutine
	}

	// Notify the server BEFORE closing streams. LightStream.Close() on H2
	// can block waiting for transport-level RST_STREAM; the server must
	// learn about the stop first so its state is clean for the next start.
	err := s.sendCtrl("realtime.stop", nil)

	// audioOut/motionOut/renderOut are closed by their I/O goroutines on exit
	// (via defer), not here, to avoid a send-on-closed-channel panic.
	// Close asynchronously: H2 LightStream.Close can wait for RST_STREAM.
	if motionStream != nil {
		go motionStream.Close()
	}
	if audioStream != nil {
		go audioStream.Close()
	}

	return err
}

// cleanupRealtimeLocal clears SDK-side realtime state without sending
// realtime.stop to the server. Called from readCtrlLoop when the server
// sends realtime.stopped (provider death auto-cleanup). Idempotent:
// skips if audioChannelID is already 0 (e.g. StopRealtime ran first).
func (s *Session) cleanupRealtimeLocal() {
	s.mu.Lock()
	if s.audioChannelID == 0 {
		s.mu.Unlock()
		return
	}
	cancelFn := s.audioCancel
	audioStream := s.audioStream
	s.audioCancel = nil
	s.audioStream = nil
	s.audioChannelID = 0
	s.audioOut = nil
	s.audioSeq.Store(0)

	dgCancelFn := s.dgCancel
	s.dgCancel = nil
	motionCancelFn := s.motionCancel
	motionStream := s.motionStream
	s.motionCancel = nil
	s.motionStream = nil
	s.motionChannelID = 0
	s.motionOut = nil

	renderCancelFn := s.renderCancel
	s.renderCancel = nil
	s.renderStream = nil
	s.renderOut = nil
	s.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	if dgCancelFn != nil {
		dgCancelFn()
	}
	if motionCancelFn != nil {
		motionCancelFn()
	}
	if renderCancelFn != nil {
		renderCancelFn()
	}
	// Close in a goroutine to avoid blocking readCtrlLoop — H2
	// audioStream.Close() can wait for transport-level RST_STREAM.
	if motionStream != nil {
		go motionStream.Close()
	}
	if audioStream != nil {
		go audioStream.Close()
	}
}

// AudioAppend sends PCM audio data to Tai.
// On H3: encodes as a datagram frame and sends via QUIC datagram.
// On H2: encodes and writes to the audio bidi stream.
func (s *Session) AudioAppend(data []byte) error {
	s.mu.Lock()
	channelID := s.audioChannelID
	audioStream := s.audioStream
	s.mu.Unlock()

	if channelID == 0 {
		return fmt.Errorf("realtime not active")
	}

	seq := s.audioSeq.Add(1)
	frame := transport.EncodeAudioFrame(channelID, seq, data)

	if s.dgConn != nil && s.dgConn.Proto() == "H3" {
		return s.dgConn.SendDatagram(frame)
	}

	// H2: write framed Payload to audio bidi stream.
	if audioStream == nil {
		return fmt.Errorf("audio stream not open")
	}
	s.audioWriteMu.Lock()
	defer s.audioWriteMu.Unlock()
	return s.codec.WriteFrame(audioStream, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "audio",
		Data:      frame,
	})
}

// AudioCommit commits the audio buffer (manual VAD mode).
func (s *Session) AudioCommit() error {
	s.mu.Lock()
	active := s.audioChannelID != 0
	s.mu.Unlock()
	if !active {
		return fmt.Errorf("realtime not active")
	}
	return s.sendCtrl("audio.commit", nil)
}

// AudioClear clears the audio buffer.
func (s *Session) AudioClear() error {
	s.mu.Lock()
	active := s.audioChannelID != 0
	s.mu.Unlock()
	if !active {
		return fmt.Errorf("realtime not active")
	}
	return s.sendCtrl("audio.clear", nil)
}

// AudioInterrupt interrupts the agent's current audio output.
func (s *Session) AudioInterrupt() error {
	s.mu.Lock()
	active := s.audioChannelID != 0
	s.mu.Unlock()
	if !active {
		return fmt.Errorf("realtime not active")
	}
	return s.sendCtrl("audio.interrupt", nil)
}

// ResponseCreate triggers model response generation (manual VAD mode).
func (s *Session) ResponseCreate() error {
	s.mu.Lock()
	active := s.audioChannelID != 0
	s.mu.Unlock()
	if !active {
		return fmt.Errorf("realtime not active")
	}
	return s.sendCtrl("response.create", nil)
}

// RecvAudio returns a channel that delivers decoded audio frames from Tai.
// Returns nil if Realtime is not active.
func (s *Session) RecvAudio() <-chan *types.AudioFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.audioOut
}

// RecvMotion returns a channel that delivers motion frames from Tai.
// Returns nil if motion is not enabled.
func (s *Session) RecvMotion() <-chan *types.MotionFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.motionOut
}

// RecvRender returns a channel that delivers render stream events
// (transcript, audio.done, input_transcript.done, emotion).
// Returns nil if Realtime is not active.
func (s *Session) RecvRender() <-chan *types.RenderEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renderOut
}

// recvDatagramLoop runs a single goroutine that reads all QUIC datagrams
// and dispatches them by channel ID to audio or motion consumers. This avoids
// the race where two independent goroutine readers could steal each other's
// datagrams from the same connection.
func (s *Session) recvDatagramLoop(ctx context.Context, audioCh uint32, audioOut chan<- *types.AudioFrame, motionCh uint32, motionOut chan<- *types.MotionFrame) {
	defer close(audioOut)
	if motionOut != nil {
		defer close(motionOut)
	}
	for {
		data, err := s.dgConn.ReceiveDatagram(ctx)
		if err != nil {
			return
		}
		if len(data) < 4 {
			continue
		}
		ch := binary.BigEndian.Uint32(data[0:4])
		switch ch {
		case audioCh:
			_, seq, pcm, err := transport.DecodeAudioFrame(data)
			if err != nil {
				continue
			}
			select {
			case audioOut <- &types.AudioFrame{Data: pcm, Seq: seq}:
			case <-ctx.Done():
				return
			}
		case motionCh:
			if motionOut == nil {
				continue
			}
			_, _, frame, err := transport.DecodeMotionFrame(data)
			if err != nil {
				continue
			}
			select {
			case motionOut <- frame:
			case <-ctx.Done():
				return
			}
		}
	}
}

// SendVideoFrame is a placeholder for the video extension (Plan 7).
func (s *Session) SendVideoFrame(_ *types.VideoFrame) error {
	return fmt.Errorf("tai: video not implemented")
}

// RecvVideoFrame is a placeholder for the video extension (Plan 7).
func (s *Session) RecvVideoFrame() <-chan *types.VideoFrame {
	return nil
}

// RecvEvent reads the next event from the data stream. Blocks.
// Returns io.EOF when the session ends.
// On stream error, triggers resume if configured; blocks until resume
// completes then retries on the new data stream.
func (s *Session) RecvEvent() (*types.SessionMsg, error) {
	for {
		s.mu.Lock()
		data := s.data
		s.mu.Unlock()

		frame, err := s.codec.ReadFrame(data)
		if err != nil {
			// Check if stream was replaced by a concurrent resume.
			s.mu.Lock()
			replaced := s.data != data
			s.mu.Unlock()
			if replaced {
				continue
			}
			if s.tryResume() {
				continue
			}
			return nil, err
		}
		// Handle error frames on data stream (e.g., recovery_gap).
		if frame.Kind == "error" {
			var se pb.SessionError
			if proto.Unmarshal(frame.Data, &se) == nil && se.Code == "recovery_gap" {
				s.notifyEvent(types.EventRecoveryGap)
			}
			s.markClosed()
			return nil, fmt.Errorf("server error: %s", se.Code)
		}

		var msg pb.SessionMessage
		if err := proto.Unmarshal(frame.Data, &msg); err != nil {
			return nil, fmt.Errorf("unmarshal event: %w", err)
		}
		s.dataSeq.Store(msg.Seq)
		s.buffer.Append(msg.Seq, msg.Kind, msg.Data)
		return &types.SessionMsg{
			Seq:  msg.Seq,
			Kind: msg.Kind,
			Data: msg.Data,
		}, nil
	}
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
	// Cancel Realtime audio goroutines if active.
	cancelFn := s.audioCancel
	audioStream := s.audioStream
	s.audioCancel = nil
	s.audioStream = nil
	s.audioChannelID = 0
	s.audioOut = nil
	s.audioSeq.Store(0)

	// Cancel datagram loop + motion/render goroutines if active.
	dgCancelFn := s.dgCancel
	s.dgCancel = nil
	motionCancelFn := s.motionCancel
	motionStream := s.motionStream
	s.motionCancel = nil
	s.motionStream = nil
	s.motionChannelID = 0
	s.motionOut = nil

	renderCancelFn := s.renderCancel
	s.renderCancel = nil
	s.renderStream = nil
	s.renderOut = nil
	s.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	if dgCancelFn != nil {
		dgCancelFn()
	}
	if motionCancelFn != nil {
		motionCancelFn()
	}
	if motionStream != nil {
		motionStream.Close()
	}
	if renderCancelFn != nil {
		renderCancelFn() // triggers stream.Close() in readRenderStream's cancel goroutine
	}
	if audioStream != nil {
		audioStream.Close()
	}

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
// If the session is disconnected and resuming, waits for resume to complete.
func (s *Session) sendCtrl(kind string, payload any) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return types.ErrSessionClosed
	}
	s.mu.Unlock()

	// Wait for any in-progress resume to finish before writing.
	if s.resuming.Load() {
		s.mu.Lock()
		done := s.resumeDone
		s.mu.Unlock()
		if done != nil {
			<-done
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return types.ErrSessionClosed
		}
		s.mu.Unlock()
	}

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

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	ctrl := s.ctrl
	s.mu.Unlock()
	return s.codec.WriteFrame(ctrl, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "message",
		Data:      msgData,
	})
}

// readCtrlLoop runs in a background goroutine. Reads ctrl stream frames,
// dispatches handshake frames (closed/error) and business frames (actions).
// On stream error, triggers resume; on successful resume, continues reading
// from the new ctrl stream.
func (s *Session) readCtrlLoop() {
	defer close(s.actions)
	for {
		s.mu.Lock()
		ctrl := s.ctrl
		s.mu.Unlock()

		frame, err := s.codec.ReadFrame(ctrl)
		if err != nil {
			// Check if stream was replaced by a concurrent resume.
			s.mu.Lock()
			replaced := s.ctrl != ctrl
			s.mu.Unlock()
			if replaced {
				continue
			}
			if s.tryResume() {
				continue
			}
			s.markClosed()
			return
		}

		switch frame.Kind {
		case "closed":
			s.markClosed()
			return
		case "error":
			var se pb.SessionError
			if proto.Unmarshal(frame.Data, &se) == nil && se.Code == "recovery_gap" {
				s.notifyEvent(types.EventRecoveryGap)
			}
			s.markClosed()
			return
		}

		var msg pb.SessionMessage
		if err := proto.Unmarshal(frame.Data, &msg); err != nil {
			continue
		}
		select {
		case s.actions <- &types.SessionMsg{Seq: msg.Seq, Kind: msg.Kind, Data: msg.Data}:
			// Server-initiated stop: clean SDK realtime state so that
			// a subsequent StartRealtime succeeds. The server already
			// tore down its side; we only clean local resources here.
			if msg.Kind == "realtime.stopped" {
				s.cleanupRealtimeLocal()
			}
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

// tryResume attempts to resume the session after a stream breaks.
// Returns true if resume succeeded and the caller should retry on new streams.
// Returns false if resume is not configured, already closed, or resume failed.
// Only one goroutine performs the actual resume; others wait.
func (s *Session) tryResume() bool {
	if s.resumeFn == nil {
		return false
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()

	// Only the first goroutine to detect disconnect performs the resume.
	if s.resuming.CompareAndSwap(false, true) {
		done := make(chan struct{})
		s.mu.Lock()
		s.resumeDone = done
		s.mu.Unlock()

		s.status.Store(types.SessionDisconnected)
		s.notifyEvent(types.EventDisconnected)

		lastCtrl := s.ctrlSeq.Load()
		lastData := s.dataSeq.Load()
		newCtrl, newData, _, err := s.resumeFn(context.Background(), lastCtrl, lastData)
		if err != nil {
			s.notifyEvent(types.EventResumeError)
			s.resuming.Store(false)
			close(done)
			return false
		}

		// Replace streams — close old ones after unlocking.
		s.mu.Lock()
		oldCtrl := s.ctrl
		oldData := s.data
		s.ctrl = newCtrl
		s.data = newData
		s.mu.Unlock()
		oldCtrl.Close()
		oldData.Close()

		s.status.Store(types.SessionRunning)
		s.notifyEvent(types.EventResumed)
		s.resuming.Store(false)
		close(done)
		return true
	}

	// Another goroutine is already resuming — wait for it.
	s.mu.Lock()
	done := s.resumeDone
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	s.mu.Lock()
	ok := !s.closed
	s.mu.Unlock()
	return ok
}

// notifyEvent calls the OnSessionEvent callback if configured.
func (s *Session) notifyEvent(event types.SessionEventKind) {
	if s.onEvent != nil {
		s.onEvent(s.id, event)
	}
}

// markClosed transitions to closed state and signals done.
func (s *Session) markClosed() {
	s.doneOnce.Do(func() { close(s.done) })
	s.mu.Lock()
	s.closed = true
	s.status.Store(types.SessionClosed)
	s.mu.Unlock()
}

// drainStaleActions discards any actions already buffered in the channel.
// Called before sending realtime.start so that stale events from prior text
// turns do not interfere with the realtime.started handshake.
func (s *Session) drainStaleActions() {
	for {
		select {
		case <-s.actions:
		default:
			return
		}
	}
}
