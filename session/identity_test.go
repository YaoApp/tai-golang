package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// Identity carried by session sub-stream first frames
// ---------------------------------------------------------------------------

// identityStreams records the first frame of every stream the session opens and
// hands back inert pipes.
type identityStreams struct {
	mu     sync.Mutex
	frames map[string]*pb.Payload
	pairs  []*testPair
	failOn string
}

func newIdentityStreams() *identityStreams {
	return &identityStreams{frames: make(map[string]*pb.Payload)}
}

func (is *identityStreams) open(_ context.Context, _ string, first *pb.Payload) (io.ReadWriteCloser, error) {
	is.mu.Lock()
	defer is.mu.Unlock()
	if is.failOn == first.Kind {
		return nil, errors.New("stream unavailable")
	}
	is.frames[first.Kind] = first
	tp := newTestPair()
	is.pairs = append(is.pairs, tp)
	return tp.stream, nil
}

func (is *identityStreams) frame(kind string) *pb.Payload {
	is.mu.Lock()
	defer is.mu.Unlock()
	return is.frames[kind]
}

func (is *identityStreams) close() {
	is.mu.Lock()
	defer is.mu.Unlock()
	for _, tp := range is.pairs {
		tp.close()
	}
}

func testIdentity() *pb.Identity {
	return &pb.Identity{UserId: "user-9", TeamId: "team-9", UserToken: "token-9", ExpiresAt: 99}
}

func TestNewStoresIdentity(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	identity := testIdentity()
	s := New("sess-id", "node-id", ctrl.stream, data.stream, codec, Options{Identity: identity})
	if s.identity != identity {
		t.Errorf("session identity = %+v, want %+v", s.identity, identity)
	}

	// Without the option the session carries no identity (system task).
	plain := New("sess-plain", "node-id", ctrl.stream, data.stream, codec)
	if plain.identity != nil {
		t.Errorf("plain identity = %+v, want nil", plain.identity)
	}
}

// realtimeReply simulates the Tai side of the realtime.start handshake with an
// optional motion channel and a custom reply kind/data.
func realtimeReply(t *testing.T, codec *transport.FrameCodec, ctrl *testPair, kind string, data any) {
	t.Helper()
	go func() {
		frame, err := codec.ReadFrame(ctrl.remoteR)
		if err != nil {
			return
		}
		var msg pb.SessionMessage
		if err := proto.Unmarshal(frame.Data, &msg); err != nil {
			return
		}
		payload, _ := json.Marshal(map[string]any{
			"capabilities":      map[string]any{"audio": true},
			"audio_channel_id":  1,
			"motion_channel_id": 2,
		})
		if kind != "realtime.started" {
			payload = []byte(`"boom"`)
		}
		reply, _ := proto.Marshal(&pb.SessionMessage{Kind: kind, Data: payload})
		_ = codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "event",
			Data:      reply,
		})
	}()
}

func TestH2SubStreamsCarryIdentity(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	streams := newIdentityStreams()
	defer ctrl.close()
	defer data.close()
	defer streams.close()

	identity := testIdentity()
	s := New("sess-h2-id", "node-h2", ctrl.stream, data.stream, codec, Options{
		OpenStreamFn: streams.open,
		Identity:     identity,
	})

	realtimeReply(t, codec, ctrl, "realtime.started", nil)
	err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime"}},
	})
	if err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}
	defer s.cleanupRealtimeLocal()

	for _, kind := range []string{"audio", "motion", "render"} {
		frame := streams.frame(kind)
		if frame == nil {
			t.Fatalf("%s first frame was never opened", kind)
		}
		if frame.Namespace != types.NamespaceSession {
			t.Errorf("%s namespace = %q", kind, frame.Namespace)
		}
		if frame.Identity == nil {
			t.Fatalf("%s first frame lost the identity", kind)
		}
		if frame.Identity.UserId != identity.UserId || frame.Identity.TeamId != identity.TeamId ||
			frame.Identity.UserToken != identity.UserToken || frame.Identity.ExpiresAt != identity.ExpiresAt {
			t.Errorf("%s identity = %+v, want %+v", kind, frame.Identity, identity)
		}
	}
}

func TestH3RenderStreamCarriesIdentity(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	streams := newIdentityStreams()
	defer ctrl.close()
	defer data.close()
	defer streams.close()

	identity := testIdentity()
	s := New("sess-h3-id", "node-h3", ctrl.stream, data.stream, codec, Options{
		DatagramConn: newMockDatagramConn("H3"),
		OpenStreamFn: streams.open,
		Identity:     identity,
	})

	realtimeReply(t, codec, ctrl, "realtime.started", nil)
	if err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime"}},
	}); err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}
	defer s.cleanupRealtimeLocal()

	render := streams.frame("render")
	if render == nil || render.Identity == nil {
		t.Fatal("render stream first frame lost the identity")
	}
	if render.Identity.UserToken != identity.UserToken {
		t.Errorf("render identity = %+v", render.Identity)
	}
}

func TestSubStreamsWithoutIdentity(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	streams := newIdentityStreams()
	defer ctrl.close()
	defer data.close()
	defer streams.close()

	s := New("sess-no-id", "node-no-id", ctrl.stream, data.stream, codec, Options{
		OpenStreamFn: streams.open,
	})

	realtimeReply(t, codec, ctrl, "realtime.started", nil)
	if err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime"}},
	}); err != nil {
		t.Fatalf("StartRealtime: %v", err)
	}
	defer s.cleanupRealtimeLocal()

	for _, kind := range []string{"audio", "motion", "render"} {
		frame := streams.frame(kind)
		if frame == nil {
			t.Fatalf("%s first frame was never opened", kind)
		}
		if frame.Identity != nil {
			t.Errorf("%s identity = %+v, want nil", kind, frame.Identity)
		}
	}
}

// ---------------------------------------------------------------------------
// StartRealtime failure paths
// ---------------------------------------------------------------------------

func TestStartRealtimeRejectedByTai(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-reject", "node-r", ctrl.stream, data.stream, codec)
	realtimeReply(t, codec, ctrl, "realtime.error", nil)
	err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime"}},
	})
	if err == nil {
		t.Fatal("expected realtime rejection error")
	}
}

func TestStartRealtimeBadStartedPayload(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-bad", "node-b", ctrl.stream, data.stream, codec)
	// Reply handler that sends malformed JSON for realtime.started.
	go func() {
		frame, err := codec.ReadFrame(ctrl.remoteR)
		if err != nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		reply, _ := proto.Marshal(&pb.SessionMessage{Kind: "realtime.started", Data: []byte("{not-json")})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "event",
			Data:      reply,
		})
	}()
	err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime"}},
	})
	if err == nil {
		t.Fatal("expected parse error for malformed realtime.started")
	}
}

func TestStartRealtimeAudioStreamFailure(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	streams := newIdentityStreams()
	streams.failOn = "audio"
	defer ctrl.close()
	defer data.close()
	defer streams.close()

	s := New("sess-audio-fail", "node-a", ctrl.stream, data.stream, codec, Options{OpenStreamFn: streams.open})
	realtimeReply(t, codec, ctrl, "realtime.started", nil)
	err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime"}},
	})
	if err == nil {
		t.Fatal("expected audio stream error")
	}
}

func TestStartRealtimeRenderStreamFailure(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	streams := newIdentityStreams()
	streams.failOn = "render"
	defer ctrl.close()
	defer data.close()
	defer streams.close()

	s := New("sess-render-fail", "node-r", ctrl.stream, data.stream, codec, Options{OpenStreamFn: streams.open})
	realtimeReply(t, codec, ctrl, "realtime.started", nil)
	err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime"}},
	})
	if err == nil {
		t.Fatal("expected render stream error")
	}
}

func TestStartRealtimeSessionClosedWhileWaiting(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-wait-closed", "node-w", ctrl.stream, data.stream, codec)
	go func() {
		frame, err := codec.ReadFrame(ctrl.remoteR)
		if err != nil {
			return
		}
		var msg pb.SessionMessage
		proto.Unmarshal(frame.Data, &msg)
		// Tell the session it is over instead of acknowledging.
		closed, _ := proto.Marshal(&pb.SessionClosed{SessionId: "sess-wait-closed", Reason: "shutdown"})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "closed",
			Data:      closed,
		})
	}()
	start := time.Now()
	err := s.StartRealtime(&types.RealtimeRequest{
		Providers: types.Providers{Primary: types.Provider{ProviderID: "openai", Model: "gpt-realtime"}},
	})
	if err == nil {
		t.Fatal("expected session-closed error")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("StartRealtime should not wait for the 30s deadline when the session closes")
	}
}

// ---------------------------------------------------------------------------
// Disposition / action error paths
// ---------------------------------------------------------------------------

func TestFollowupUnexpectedAction(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-followup", "node-f", ctrl.stream, data.stream, codec)
	env := &realtimeTestEnv{session: s, ctrl: ctrl, data: data, codec: codec}
	env.simulateTaiCtrlReply("unexpected", map[string]any{})

	if _, err := s.Followup(&types.UserMessage{Message: "hi"}); err == nil {
		t.Fatal("expected disposition error for an unexpected action kind")
	}
}

func TestFollowupBadDispositionJSON(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-followup-bad", "node-f", ctrl.stream, data.stream, codec)
	go func() {
		if _, err := codec.ReadFrame(ctrl.remoteR); err != nil {
			return
		}
		reply, _ := proto.Marshal(&pb.SessionMessage{Kind: "disposition", Data: []byte("{bad")})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "event",
			Data:      reply,
		})
	}()

	if _, err := s.Followup(&types.UserMessage{Message: "hi"}); err == nil {
		t.Fatal("expected JSON error for malformed disposition")
	}
}

func TestRecvActionSessionClosed(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-action-closed", "node-a", ctrl.stream, data.stream, codec)
	s.markClosed()

	if _, err := s.RecvAction(); !errors.Is(err, types.ErrSessionClosed) {
		t.Errorf("RecvAction err = %v, want ErrSessionClosed", err)
	}
	if err := s.Prompt(&types.UserMessage{}); !errors.Is(err, types.ErrSessionClosed) {
		t.Errorf("Prompt err = %v, want ErrSessionClosed", err)
	}
}
