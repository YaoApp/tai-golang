package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// mockDatagramConn simulates QUIC datagram I/O for H3 tests.
type mockDatagramConn struct {
	proto  string
	sendCh chan []byte
	recvCh chan []byte
}

func newMockDatagramConn(proto string) *mockDatagramConn {
	return &mockDatagramConn{
		proto:  proto,
		sendCh: make(chan []byte, 64),
		recvCh: make(chan []byte, 64),
	}
}

func (m *mockDatagramConn) SendDatagram(data []byte) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	m.sendCh <- cp
	return nil
}

func (m *mockDatagramConn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case d := <-m.recvCh:
		return d, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *mockDatagramConn) Proto() string { return m.proto }

// realtimeTestEnv sets up a session with mock ctrl/data streams and
// a goroutine that simulates the Tai side for Realtime handshake.
type realtimeTestEnv struct {
	session *Session
	ctrl    *testPair
	data    *testPair
	dgConn  *mockDatagramConn
	codec   *transport.FrameCodec
}

func newRealtimeTestEnv(proto string) *realtimeTestEnv {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	dgConn := newMockDatagramConn(proto)

	s := New("rt-sess", "rt-node", ctrl.stream, data.stream, codec, Options{
		DatagramConn: dgConn,
	})

	return &realtimeTestEnv{
		session: s,
		ctrl:    ctrl,
		data:    data,
		dgConn:  dgConn,
		codec:   codec,
	}
}

func (env *realtimeTestEnv) close() {
	env.ctrl.close()
	env.data.close()
}

// simulateTaiRealtimeStart reads the realtime.start ctrl command from
// the remote end and sends back a realtime.started response.
func (env *realtimeTestEnv) simulateTaiRealtimeStart(channelID uint32) {
	go func() {
		// Read the realtime.start command.
		frame, err := env.codec.ReadFrame(env.ctrl.remoteR)
		if err != nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		if msg.Kind != "realtime.start" {
			return
		}

		// Send realtime.started response on ctrl stream.
		startedData, _ := json.Marshal(map[string]any{
			"capabilities":     map[string]any{"audio": true},
			"audio_channel_id": channelID,
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

// simulateTaiCtrlReply reads the next ctrl command and sends a ctrl event reply.
func (env *realtimeTestEnv) simulateTaiCtrlReply(replyKind string, replyData any) {
	go func() {
		env.codec.ReadFrame(env.ctrl.remoteR) // consume the command
		payload, _ := json.Marshal(replyData)
		msgData, _ := proto.Marshal(&pb.SessionMessage{
			Kind: replyKind,
			Data: payload,
		})
		env.codec.WriteFrame(env.ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "event",
			Data:      msgData,
		})
	}()
}

func TestStartRealtime_H3(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	// Verify audio channel is active.
	if env.session.RecvAudio() == nil {
		t.Error("RecvAudio() returned nil after StartRealtime")
	}
}

func TestStartRealtime_NilRequest(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	err := env.session.StartRealtime(nil)
	if err == nil {
		t.Error("StartRealtime(nil) should return error")
	}
}

func TestStartRealtime_AlreadyActive(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err == nil {
		t.Error("second StartRealtime should return error")
	}
}

func TestUpdateRealtime(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	// Read the update ctrl command in background.
	var gotKind string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame, err := env.codec.ReadFrame(env.ctrl.remoteR)
		if err != nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		gotKind = msg.Kind
	}()

	err := env.session.UpdateRealtime(&types.RealtimeUpdateOptions{
		Audio: &types.AudioConfig{Voice: "alloy"},
	})
	if err != nil {
		t.Fatalf("UpdateRealtime: %v", err)
	}

	wg.Wait()
	if gotKind != "realtime.update" {
		t.Errorf("ctrl kind = %q, want realtime.update", gotKind)
	}
}

func TestUpdateRealtime_NotActive(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	err := env.session.UpdateRealtime(&types.RealtimeUpdateOptions{})
	if err == nil {
		t.Error("UpdateRealtime without active realtime should error")
	}
}

func TestStopRealtime(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	// Read the stop ctrl command in background.
	var gotKind string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame, err := env.codec.ReadFrame(env.ctrl.remoteR)
		if err != nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		gotKind = msg.Kind
	}()

	err := env.session.StopRealtime()
	if err != nil {
		t.Fatalf("StopRealtime: %v", err)
	}

	wg.Wait()
	if gotKind != "realtime.stop" {
		t.Errorf("ctrl kind = %q, want realtime.stop", gotKind)
	}

	// RecvAudio() returns nil (s.audioOut was set to nil in StopRealtime).
}

func TestStopRealtime_NotActive(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	err := env.session.StopRealtime()
	if err == nil {
		t.Error("StopRealtime without active realtime should error")
	}
}

func TestAudioAppend_H3(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	pcm := []byte{0x10, 0x20, 0x30}
	if err := env.session.AudioAppend(pcm); err != nil {
		t.Fatalf("AudioAppend: %v", err)
	}

	// Read the datagram that was sent.
	select {
	case sent := <-env.dgConn.sendCh:
		ch, seq, gotPCM, err := transport.DecodeAudioFrame(sent)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ch != 1 {
			t.Errorf("channelID = %d, want 1", ch)
		}
		if seq != 1 {
			t.Errorf("seq = %d, want 1", seq)
		}
		if len(gotPCM) != 3 || gotPCM[0] != 0x10 {
			t.Errorf("pcm mismatch: %v", gotPCM)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for datagram")
	}
}

func TestAudioAppend_NotActive(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	err := env.session.AudioAppend([]byte{0x01})
	if err == nil {
		t.Error("AudioAppend without active realtime should error")
	}
}

func TestAudioCommit(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	var gotKind string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame, _ := env.codec.ReadFrame(env.ctrl.remoteR)
		if frame == nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		gotKind = msg.Kind
	}()

	env.session.AudioCommit()
	wg.Wait()
	if gotKind != "audio.commit" {
		t.Errorf("ctrl kind = %q, want audio.commit", gotKind)
	}
}

func TestAudioClear(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	var gotKind string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame, _ := env.codec.ReadFrame(env.ctrl.remoteR)
		if frame == nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		gotKind = msg.Kind
	}()

	env.session.AudioClear()
	wg.Wait()
	if gotKind != "audio.clear" {
		t.Errorf("ctrl kind = %q, want audio.clear", gotKind)
	}
}

func TestAudioInterrupt(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	var gotKind string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame, _ := env.codec.ReadFrame(env.ctrl.remoteR)
		if frame == nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		gotKind = msg.Kind
	}()

	env.session.AudioInterrupt()
	wg.Wait()
	if gotKind != "audio.interrupt" {
		t.Errorf("ctrl kind = %q, want audio.interrupt", gotKind)
	}
}

func TestRecvAudio_H3_Roundtrip(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	// Simulate Tai sending audio datagram.
	pcm := []byte{0xAA, 0xBB, 0xCC}
	frame := transport.EncodeAudioFrame(1, 42, pcm)
	env.dgConn.recvCh <- frame

	audioCh := env.session.RecvAudio()
	select {
	case af := <-audioCh:
		if af.Seq != 42 {
			t.Errorf("seq = %d, want 42", af.Seq)
		}
		if len(af.Data) != 3 || af.Data[0] != 0xAA {
			t.Errorf("data mismatch: %v", af.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for audio frame")
	}
}

func TestRecvAudio_H3_FilterWrongChannel(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	// Send a datagram with wrong channelID — should be filtered out.
	wrongFrame := transport.EncodeAudioFrame(99, 1, []byte{0xFF})
	env.dgConn.recvCh <- wrongFrame

	// Send correct one.
	goodFrame := transport.EncodeAudioFrame(1, 2, []byte{0x11})
	env.dgConn.recvCh <- goodFrame

	audioCh := env.session.RecvAudio()
	select {
	case af := <-audioCh:
		if af.Seq != 2 {
			t.Errorf("got seq=%d, want 2 (wrong channel should be filtered)", af.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestFullRoundtrip_H3(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	env.simulateTaiRealtimeStart(1)

	// Start realtime.
	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}

	// Send audio to Tai.
	env.session.AudioAppend([]byte{0x01, 0x02})
	select {
	case sent := <-env.dgConn.sendCh:
		ch, _, _, _ := transport.DecodeAudioFrame(sent)
		if ch != 1 {
			t.Errorf("sent channelID = %d, want 1", ch)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout sending audio")
	}

	// Receive audio from Tai.
	env.dgConn.recvCh <- transport.EncodeAudioFrame(1, 10, []byte{0xAB})
	audioCh := env.session.RecvAudio()
	select {
	case af := <-audioCh:
		if af.Seq != 10 || af.Data[0] != 0xAB {
			t.Errorf("unexpected frame: seq=%d data=%v", af.Seq, af.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout receiving audio")
	}

	// Stop realtime.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		env.codec.ReadFrame(env.ctrl.remoteR) // read realtime.stop
	}()

	env.session.StopRealtime()
	wg.Wait()

	// RecvAudio should be nil now.
	if env.session.RecvAudio() != nil {
		t.Error("RecvAudio should be nil after StopRealtime")
	}
}

func TestRecvAudio_NilBeforeStart(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	if env.session.RecvAudio() != nil {
		t.Error("RecvAudio should be nil before StartRealtime")
	}
}

func TestMotionVideoStubs(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	if env.session.RecvMotion() != nil {
		t.Error("RecvMotion should return nil (stub)")
	}
	if env.session.RecvVideoFrame() != nil {
		t.Error("RecvVideoFrame should return nil (stub)")
	}
	if err := env.session.SendVideoFrame(&types.VideoFrame{}); err == nil {
		t.Error("SendVideoFrame should return error (stub)")
	}
}

// h2TestEnv sets up a session with H2 transport and a mock audio stream.
type h2TestEnv struct {
	session *Session
	ctrl    *testPair
	data    *testPair
	audio   *testPair // mock H2 audio bidi stream
	codec   *transport.FrameCodec
}

func newH2TestEnv() *h2TestEnv {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	audio := newTestPair()
	dgConn := newMockDatagramConn("H2")

	s := New("h2-sess", "h2-node", ctrl.stream, data.stream, codec, Options{
		DatagramConn: dgConn,
		OpenStreamFn: func(_ context.Context, _ string, _ *pb.Payload) (io.ReadWriteCloser, error) {
			return audio.stream, nil
		},
	})

	return &h2TestEnv{
		session: s,
		ctrl:    ctrl,
		data:    data,
		audio:   audio,
		codec:   codec,
	}
}

func (env *h2TestEnv) close() {
	env.ctrl.close()
	env.data.close()
	env.audio.close()
}

func (env *h2TestEnv) simulateTaiRealtimeStart(channelID uint32) {
	go func() {
		frame, err := env.codec.ReadFrame(env.ctrl.remoteR)
		if err != nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)

		startedData, _ := json.Marshal(map[string]any{
			"capabilities":     map[string]any{"audio": true},
			"audio_channel_id": channelID,
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

func TestStartRealtime_H2(t *testing.T) {
	env := newH2TestEnv()
	defer env.close()

	env.simulateTaiRealtimeStart(1)

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err != nil {
		t.Fatalf("StartRealtime H2: %v", err)
	}

	if env.session.RecvAudio() == nil {
		t.Error("RecvAudio should not be nil after H2 StartRealtime")
	}
}

func TestRecvAudio_H2_Roundtrip(t *testing.T) {
	env := newH2TestEnv()
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	// Simulate Tai sending an audio frame via the H2 audio stream.
	// The readAudioStreamH2 goroutine reads Payload frames via codec.ReadFrame.
	audioData := transport.EncodeAudioFrame(1, 5, []byte{0xDE, 0xAD})
	env.codec.WriteFrame(env.audio.remoteW, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "audio",
		Data:      audioData,
	})

	audioCh := env.session.RecvAudio()
	select {
	case af := <-audioCh:
		if af.Seq != 5 {
			t.Errorf("seq = %d, want 5", af.Seq)
		}
		if len(af.Data) != 2 || af.Data[0] != 0xDE {
			t.Errorf("data mismatch: %v", af.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for H2 audio frame")
	}
}

func TestRecvAudio_H2_StaleFrameDetection(t *testing.T) {
	env := newH2TestEnv()
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	// Send frame seq=10.
	env.codec.WriteFrame(env.audio.remoteW, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "audio",
		Data:      transport.EncodeAudioFrame(1, 10, []byte{0x01}),
	})

	// Send stale frame seq=5 (should be dropped).
	env.codec.WriteFrame(env.audio.remoteW, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "audio",
		Data:      transport.EncodeAudioFrame(1, 5, []byte{0x02}),
	})

	// Send frame seq=11 (should arrive).
	env.codec.WriteFrame(env.audio.remoteW, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "audio",
		Data:      transport.EncodeAudioFrame(1, 11, []byte{0x03}),
	})

	audioCh := env.session.RecvAudio()

	// First frame: seq=10
	select {
	case af := <-audioCh:
		if af.Seq != 10 {
			t.Errorf("first frame seq = %d, want 10", af.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout on first frame")
	}

	// Second frame should be seq=11 (seq=5 was dropped).
	select {
	case af := <-audioCh:
		if af.Seq != 11 {
			t.Errorf("second frame seq = %d, want 11 (seq=5 should be dropped)", af.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout on second frame")
	}
}

func TestAudioAppend_H2(t *testing.T) {
	env := newH2TestEnv()
	defer env.close()

	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})

	// Start reading in a goroutine (pipe blocks until reader is ready).
	type readResult struct {
		frame *pb.Payload
		err   error
	}
	ch := make(chan readResult, 1)
	go func() {
		f, err := env.codec.ReadFrame(env.audio.remoteR)
		ch <- readResult{f, err}
	}()

	pcm := []byte{0xAA, 0xBB}
	if err := env.session.AudioAppend(pcm); err != nil {
		t.Fatalf("AudioAppend H2: %v", err)
	}

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read audio frame: %v", r.err)
		}
		if r.frame.Kind != "audio" {
			t.Errorf("frame.Kind = %q, want audio", r.frame.Kind)
		}
		gotCh, seq, gotPCM, err := transport.DecodeAudioFrame(r.frame.Data)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if gotCh != 1 {
			t.Errorf("channelID = %d, want 1", gotCh)
		}
		if seq != 1 {
			t.Errorf("seq = %d, want 1", seq)
		}
		if len(gotPCM) != 2 || gotPCM[0] != 0xAA {
			t.Errorf("pcm mismatch: %v", gotPCM)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for H2 audio frame")
	}
}

func TestStartRealtime_WrongResponseKind(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	// Tai replies with wrong kind.
	go func() {
		env.codec.ReadFrame(env.ctrl.remoteR) // consume realtime.start
		replyData, _ := proto.Marshal(&pb.SessionMessage{
			Kind: "something_else",
			Data: []byte(`{}`),
		})
		env.codec.WriteFrame(env.ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "event",
			Data:      replyData,
		})
	}()

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err == nil {
		t.Fatal("expected error for wrong response kind")
	}
	if env.session.RecvAudio() != nil {
		t.Error("RecvAudio should be nil after failed start")
	}
}

func TestStartRealtime_BadJSON(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	// Tai replies with invalid JSON in realtime.started.
	go func() {
		env.codec.ReadFrame(env.ctrl.remoteR)
		replyData, _ := proto.Marshal(&pb.SessionMessage{
			Kind: "realtime.started",
			Data: []byte(`{bad json`),
		})
		env.codec.WriteFrame(env.ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "event",
			Data:      replyData,
		})
	}()

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

func TestStartRealtime_CtrlClosed(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	// Close ctrl remote write → readCtrlLoop exits → RecvAction returns EOF.
	go func() {
		env.codec.ReadFrame(env.ctrl.remoteR) // consume realtime.start
		env.ctrl.remoteW.Close()              // Tai disappears
	}()

	err := env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err == nil {
		t.Fatal("expected error when ctrl stream closes")
	}
}

func TestStartRealtime_H2_OpenFails(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	dgConn := newMockDatagramConn("H2")
	defer ctrl.close()
	defer data.close()

	s := New("h2f-sess", "h2f-node", ctrl.stream, data.stream, codec, Options{
		DatagramConn: dgConn,
		OpenStreamFn: func(_ context.Context, _ string, _ *pb.Payload) (io.ReadWriteCloser, error) {
			return nil, fmt.Errorf("simulated open error")
		},
	})

	go func() {
		codec.ReadFrame(ctrl.remoteR) // consume realtime.start
		startedData, _ := json.Marshal(map[string]any{
			"capabilities":     map[string]any{},
			"audio_channel_id": 1,
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
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	if err == nil {
		t.Fatal("expected error when H2 audio open fails")
	}
	if s.RecvAudio() != nil {
		t.Error("RecvAudio should be nil after failed H2 open")
	}
}

func TestAudioCommit_NotActive(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()
	if err := env.session.AudioCommit(); err == nil {
		t.Error("AudioCommit: expected error when not active")
	}
}

func TestAudioClear_NotActive(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()
	if err := env.session.AudioClear(); err == nil {
		t.Error("AudioClear: expected error when not active")
	}
}

func TestAudioInterrupt_NotActive(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()
	if err := env.session.AudioInterrupt(); err == nil {
		t.Error("AudioInterrupt: expected error when not active")
	}
}

func TestAudioSeqReset_AfterStopStart(t *testing.T) {
	env := newRealtimeTestEnv("H3")
	defer env.close()

	// First session.
	env.simulateTaiRealtimeStart(1)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	env.session.AudioAppend([]byte{0x01})
	<-env.dgConn.sendCh // seq=1

	// Stop, drain ctrl.
	go func() { env.codec.ReadFrame(env.ctrl.remoteR) }()
	env.session.StopRealtime()

	// Second session.
	env.simulateTaiRealtimeStart(2)
	env.session.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{
			Primary: types.ProviderSpec{Provider: "openai", Model: "gpt-realtime-2.1"},
		},
	})
	env.session.AudioAppend([]byte{0x02})
	select {
	case sent := <-env.dgConn.sendCh:
		_, seq, _, _ := transport.DecodeAudioFrame(sent)
		if seq != 1 {
			t.Errorf("seq after restart = %d, want 1 (should reset)", seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

// Verify that the DatagramConn lightConn methods behave correctly.
func TestLightConnDatagramConn_H2(t *testing.T) {
	// H2 lightConn has no session, should return ErrDatagramNotSupported.
	ls := transport.NewLightStreamForTestWithProto(
		io.NopCloser(&emptyReader{}), nopWriteCloser{}, "H2",
	)
	dc := ls.DatagramConn()
	if dc.Proto() != "H2" {
		t.Errorf("Proto() = %q, want H2", dc.Proto())
	}
	if err := dc.SendDatagram([]byte{1}); err != transport.ErrDatagramNotSupported {
		t.Errorf("SendDatagram = %v, want ErrDatagramNotSupported", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := dc.ReceiveDatagram(ctx)
	if err != transport.ErrDatagramNotSupported {
		t.Errorf("ReceiveDatagram = %v, want ErrDatagramNotSupported", err)
	}
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }
