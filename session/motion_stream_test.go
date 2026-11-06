package session

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// h2MotionTestEnv sets up an H2 session with separate streams for audio,
// render, and motion. OpenStreamFn dispatches by kind.
type h2MotionTestEnv struct {
	session *Session
	ctrl    *testPair
	data    *testPair
	audio   *testPair
	render  *testPair
	motion  *testPair
	codec   *transport.FrameCodec
}

func newH2MotionTestEnv() *h2MotionTestEnv {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	audio := newTestPair()
	render := newTestPair()
	motion := newTestPair()

	s := New("motion-sess", "motion-node", ctrl.stream, data.stream, codec, Options{
		OpenStreamFn: func(_ context.Context, _ string, first *pb.Payload) (io.ReadWriteCloser, error) {
			switch first.Kind {
			case "audio":
				return audio.stream, nil
			case "render":
				return render.stream, nil
			case "motion":
				return motion.stream, nil
			default:
				return nil, io.ErrUnexpectedEOF
			}
		},
	})

	return &h2MotionTestEnv{
		session: s,
		ctrl:    ctrl,
		data:    data,
		audio:   audio,
		render:  render,
		motion:  motion,
		codec:   codec,
	}
}

// cleanup cancels all realtime state (without ctrl exchange) then closes pipes.
func (env *h2MotionTestEnv) cleanup() {
	env.session.cleanupRealtimeLocal()
	env.ctrl.close()
	env.data.close()
	env.audio.close()
	env.render.close()
	env.motion.close()
}

// simulateTaiRealtimeStartWithMotion reads realtime.start and replies with
// realtime.started including MotionChannelID.
func (env *h2MotionTestEnv) simulateTaiRealtimeStartWithMotion(audioCh, motionCh uint32) {
	go func() {
		frame, err := env.codec.ReadFrame(env.ctrl.remoteR)
		if err != nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		if msg.Kind != "realtime.start" {
			return
		}
		startedData, _ := json.Marshal(map[string]any{
			"capabilities":      map[string]any{"audio": true},
			"audio_channel_id":  audioCh,
			"motion_channel_id": motionCh,
		})
		replyData, _ := proto.Marshal(&pb.SessionMessage{
			Kind: "realtime.started",
			Data: startedData,
		})
		env.codec.WriteFrame(env.ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "event",
			Data:      replyData,
		})
	}()
}

func TestMotionStreamH2_EndToEnd(t *testing.T) {
	env := newH2MotionTestEnv()
	defer env.cleanup()

	env.simulateTaiRealtimeStartWithMotion(1, 2)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	motionCh := env.session.RecvMotion()
	if motionCh == nil {
		t.Fatal("RecvMotion() returned nil after StartRealtime with MotionChannelID=2")
	}

	// Simulate Tai sending a motion frame through the motion stream.
	testFrame := &types.MotionFrame{
		Version:   types.MotionFrameVersion,
		Profile:   types.ProfileARKit52,
		State:     3,
		Emotion:   1,
		Intensity: 200,
		FrameSeq:  42,
		AudioSeq:  100,
		PtsMs:     3000,
	}
	testFrame.Face[0] = 180
	testFrame.Head = [3]int8{5, -3, 2}
	testFrame.Gaze = [2]int8{1, -1}
	testFrame.Viseme = [5]uint8{100, 50, 75, 30, 20}
	testFrame.GestureID = 1
	testFrame.GesturePhase = 128
	testFrame.Body[0] = 140

	packed := testFrame.Pack()
	env.codec.WriteFrame(env.motion.remoteW, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "motion_frame",
		Data:      packed[:],
	})

	select {
	case got := <-motionCh:
		if got.Version != testFrame.Version {
			t.Errorf("Version = %d, want %d", got.Version, testFrame.Version)
		}
		if got.FrameSeq != testFrame.FrameSeq {
			t.Errorf("FrameSeq = %d, want %d", got.FrameSeq, testFrame.FrameSeq)
		}
		if got.Face[0] != 180 {
			t.Errorf("Face[0] = %d, want 180", got.Face[0])
		}
		if got.Head != testFrame.Head {
			t.Errorf("Head = %v, want %v", got.Head, testFrame.Head)
		}
		if got.Viseme != testFrame.Viseme {
			t.Errorf("Viseme = %v, want %v", got.Viseme, testFrame.Viseme)
		}
		if got.GestureID != testFrame.GestureID {
			t.Errorf("GestureID = %d, want %d", got.GestureID, testFrame.GestureID)
		}
		if got.State != testFrame.State {
			t.Errorf("State = %d, want %d", got.State, testFrame.State)
		}
		if got.Emotion != testFrame.Emotion {
			t.Errorf("Emotion = %d, want %d", got.Emotion, testFrame.Emotion)
		}
		if got.Body[0] != testFrame.Body[0] {
			t.Errorf("Body[0] = %d, want %d", got.Body[0], testFrame.Body[0])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for motion frame")
	}
}

func TestMotionStreamH2_MultipleFrames(t *testing.T) {
	env := newH2MotionTestEnv()
	defer env.cleanup()

	env.simulateTaiRealtimeStartWithMotion(1, 2)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	motionCh := env.session.RecvMotion()
	if motionCh == nil {
		t.Fatal("RecvMotion() returned nil")
	}

	const frameCount = 10
	go func() {
		for i := range frameCount {
			f := &types.MotionFrame{
				Version:  types.MotionFrameVersion,
				Profile:  types.ProfileARKit52,
				FrameSeq: uint16(i + 1),
				PtsMs:    uint32((i + 1) * 33),
			}
			f.Face[0] = uint8(i * 10)
			packed := f.Pack()
			env.codec.WriteFrame(env.motion.remoteW, &pb.Payload{
				Namespace: types.NamespaceSession,
				Kind:      "motion_frame",
				Data:      packed[:],
			})
		}
	}()

	for i := range frameCount {
		select {
		case got := <-motionCh:
			if got.FrameSeq != uint16(i+1) {
				t.Errorf("frame %d: FrameSeq = %d, want %d", i, got.FrameSeq, i+1)
			}
			if got.Face[0] != uint8(i*10) {
				t.Errorf("frame %d: Face[0] = %d, want %d", i, got.Face[0], i*10)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for frame %d", i)
		}
	}
}

func TestMotionStreamH2_NoMotionChannel(t *testing.T) {
	env := newH2MotionTestEnv()
	defer env.cleanup()

	env.simulateTaiRealtimeStartWithMotion(1, 0)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	if env.session.RecvMotion() != nil {
		t.Error("RecvMotion() should be nil when MotionChannelID is 0")
	}
}

func TestMotionStreamH2_StopCleansUp(t *testing.T) {
	env := newH2MotionTestEnv()
	defer env.cleanup()

	env.simulateTaiRealtimeStartWithMotion(1, 2)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	if env.session.RecvMotion() == nil {
		t.Fatal("RecvMotion() should be non-nil before stop")
	}

	// Drain the realtime.stop ctrl command and close remote stream ends
	// (simulates Tai closing its side after receiving stop).
	go func() {
		env.codec.ReadFrame(env.ctrl.remoteR)
		env.motion.remoteW.Close()
		env.audio.remoteW.Close()
	}()

	if err := env.session.StopRealtime(); err != nil {
		t.Fatalf("StopRealtime: %v", err)
	}

	env.session.mu.Lock()
	motionCh := env.session.motionChannelID
	motionOut := env.session.motionOut
	motionStream := env.session.motionStream
	motionCancel := env.session.motionCancel
	env.session.mu.Unlock()

	if motionCh != 0 {
		t.Errorf("motionChannelID = %d, want 0", motionCh)
	}
	if motionOut != nil {
		t.Error("motionOut should be nil after stop")
	}
	if motionStream != nil {
		t.Error("motionStream should be nil after stop")
	}
	if motionCancel != nil {
		t.Error("motionCancel should be nil after stop")
	}
}

func TestMotionStreamH2_CleanupRealtimeLocal(t *testing.T) {
	env := newH2MotionTestEnv()
	defer env.cleanup()

	env.simulateTaiRealtimeStartWithMotion(1, 2)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	if env.session.RecvMotion() == nil {
		t.Fatal("RecvMotion() should be non-nil before cleanup")
	}

	env.session.cleanupRealtimeLocal()

	env.session.mu.Lock()
	motionCh := env.session.motionChannelID
	motionOut := env.session.motionOut
	motionStream := env.session.motionStream
	motionCancel := env.session.motionCancel
	env.session.mu.Unlock()

	if motionCh != 0 {
		t.Errorf("motionChannelID = %d, want 0", motionCh)
	}
	if motionOut != nil {
		t.Error("motionOut should be nil after cleanup")
	}
	if motionStream != nil {
		t.Error("motionStream should be nil after cleanup")
	}
	if motionCancel != nil {
		t.Error("motionCancel should be nil after cleanup")
	}
}

func TestMotionStreamH2_BadFrame(t *testing.T) {
	env := newH2MotionTestEnv()
	defer env.cleanup()

	env.simulateTaiRealtimeStartWithMotion(1, 2)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	motionCh := env.session.RecvMotion()
	if motionCh == nil {
		t.Fatal("RecvMotion() returned nil")
	}

	// Send a bad frame (too short) — should be skipped.
	env.codec.WriteFrame(env.motion.remoteW, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "motion_frame",
		Data:      []byte{0x01, 0x02, 0x03},
	})

	// Send a valid frame after the bad one.
	validFrame := &types.MotionFrame{
		Version:  types.MotionFrameVersion,
		Profile:  types.ProfileARKit52,
		FrameSeq: 99,
	}
	packed := validFrame.Pack()
	env.codec.WriteFrame(env.motion.remoteW, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "motion_frame",
		Data:      packed[:],
	})

	select {
	case got := <-motionCh:
		if got.FrameSeq != 99 {
			t.Errorf("FrameSeq = %d, want 99 (bad frame should be skipped)", got.FrameSeq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for valid motion frame after bad frame")
	}
}

func TestMotionStreamH2_StreamCloseClosesChannel(t *testing.T) {
	env := newH2MotionTestEnv()
	defer env.cleanup()

	env.simulateTaiRealtimeStartWithMotion(1, 2)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	motionCh := env.session.RecvMotion()
	if motionCh == nil {
		t.Fatal("RecvMotion() returned nil")
	}

	// Close the remote write end — readMotionStreamH2 should exit and close motionOut.
	env.motion.remoteW.Close()

	select {
	case _, ok := <-motionCh:
		if ok {
			t.Error("expected channel close, got a frame")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for motion channel close")
	}
}

func TestMotionStreamH2_ContextCancel(t *testing.T) {
	motion := newTestPair()
	defer motion.close()

	motionCtx, motionCancel := context.WithCancel(context.Background())
	motionOut := make(chan *types.MotionFrame, 8)

	s := &Session{} // minimal, only need readMotionStreamH2
	go s.readMotionStreamH2(motionCtx, motion.stream, motionOut)

	motionCancel()

	select {
	case _, ok := <-motionOut:
		if ok {
			t.Error("expected channel close after context cancel, got a frame")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for motion channel close after cancel")
	}
}

func TestMotionStreamH2_OpenStreamFnError(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	audio := newTestPair()
	render := newTestPair()

	s := New("motion-err-sess", "n1", ctrl.stream, data.stream, codec, Options{
		OpenStreamFn: func(_ context.Context, _ string, first *pb.Payload) (io.ReadWriteCloser, error) {
			switch first.Kind {
			case "audio":
				return audio.stream, nil
			case "render":
				return render.stream, nil
			case "motion":
				return nil, io.ErrUnexpectedEOF
			default:
				return nil, io.ErrUnexpectedEOF
			}
		},
	})
	defer func() {
		s.cleanupRealtimeLocal()
		ctrl.close()
		data.close()
		audio.close()
		render.close()
	}()

	go func() {
		frame, _ := codec.ReadFrame(ctrl.remoteR)
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		startedData, _ := json.Marshal(map[string]any{
			"capabilities":      map[string]any{},
			"audio_channel_id":  uint32(1),
			"motion_channel_id": uint32(2),
		})
		replyData, _ := proto.Marshal(&pb.SessionMessage{
			Kind: "realtime.started",
			Data: startedData,
		})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "event",
			Data:      replyData,
		})
	}()

	err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v (motion failure should be non-fatal)", err)
	}

	if s.RecvMotion() != nil {
		t.Error("RecvMotion() should be nil when openMotionStreamH2 fails")
	}
	if s.RecvAudio() == nil {
		t.Error("RecvAudio() should be non-nil")
	}
	if s.RecvRender() == nil {
		t.Error("RecvRender() should be non-nil")
	}
}
