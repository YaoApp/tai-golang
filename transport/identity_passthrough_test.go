package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

// ---------------------------------------------------------------------------
// First-frame identity helpers
// ---------------------------------------------------------------------------

func TestFirstFrame_WithIdentity(t *testing.T) {
	identity := &pb.Identity{
		UserId:    "user-1",
		TeamId:    "team-1",
		UserToken: "opaque-token",
		ExpiresAt: 1700000000,
	}
	frame := FirstFrame(types.NamespaceSession, "open", []byte("data"), identity)

	if frame.Namespace != types.NamespaceSession || frame.Kind != "open" {
		t.Errorf("frame = %s/%s, want session/open", frame.Namespace, frame.Kind)
	}
	if string(frame.Data) != "data" {
		t.Errorf("data = %q, want data", frame.Data)
	}
	if frame.Identity != identity {
		t.Fatal("identity was not preserved on the first frame")
	}
	if got := IdentityFromFrame(frame); got != identity {
		t.Error("IdentityFromFrame did not return the frame identity")
	}
}

func TestFirstFrame_WithoutIdentity(t *testing.T) {
	frame := FirstFrame(types.NamespaceSession, "open", nil, nil)
	if frame.Identity != nil {
		t.Error("identity should be nil for a session without a user")
	}
	if got := IdentityFromFrame(frame); got != nil {
		t.Errorf("IdentityFromFrame = %v, want nil", got)
	}
}

func TestIdentityFromFrame_NilFrame(t *testing.T) {
	if got := IdentityFromFrame(nil); got != nil {
		t.Errorf("IdentityFromFrame(nil) = %v, want nil", got)
	}
}

// TestFrameCodecIdentityRoundTrip pins the wire contract: every Identity field
// survives the length-prefixed Payload codec, and later frames may omit it.
func TestFrameCodecIdentityRoundTrip(t *testing.T) {
	codec := DefaultCodec()
	identity := &pb.Identity{
		UserId:    "u-42",
		TeamId:    "t-42",
		UserToken: "jwt-value",
		ExpiresAt: 1234567890,
	}

	var buf bytes.Buffer
	if err := codec.WriteFrame(&buf, FirstFrame(types.NamespaceSession, "open", []byte("x"), identity)); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := codec.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Identity == nil {
		t.Fatal("identity lost in frame round trip")
	}
	if got.Identity.UserId != identity.UserId || got.Identity.TeamId != identity.TeamId ||
		got.Identity.UserToken != identity.UserToken || got.Identity.ExpiresAt != identity.ExpiresAt {
		t.Errorf("identity = %+v, want %+v", got.Identity, identity)
	}

	// Later frames of the same stream must not repeat the identity.
	var tail bytes.Buffer
	if err := codec.WriteFrame(&tail, FirstFrame(types.NamespaceSession, "message", []byte("y"), nil)); err != nil {
		t.Fatalf("WriteFrame tail: %v", err)
	}
	got, err = codec.ReadFrame(&tail)
	if err != nil {
		t.Fatalf("ReadFrame tail: %v", err)
	}
	if got.Identity != nil {
		t.Errorf("non-first frame identity = %+v, want nil", got.Identity)
	}
}

// ---------------------------------------------------------------------------
// Light over H3: identity passthrough
// ---------------------------------------------------------------------------

func TestLightH3PreservesFirstFrameIdentity(t *testing.T) {
	is := newIntegrationServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodeID := "light-h3-identity"
	sess := is.connectLightH3(t, ctx, nodeID)
	identity := identityFixture(t, is.auth, nodeID, types.Scope{types.SessionAccess})

	stream, err := is.srv.OpenLightStream(ctx, nodeID, FirstFrame(types.NamespaceSession, "open", []byte("payload"), identity))
	if err != nil {
		t.Fatalf("OpenLightStream: %v", err)
	}

	taiStream, first := acceptH3FirstFrame(t, ctx, sess)
	if first.Identity == nil {
		t.Fatal("Tai side received a first frame without identity")
	}
	if first.Identity.UserId != identity.UserId || first.Identity.UserToken != identity.UserToken {
		t.Errorf("identity = %+v, want %+v", first.Identity, identity)
	}
	if first.Kind != "open" {
		t.Errorf("kind = %q, want open", first.Kind)
	}

	// Data still flows in both directions after the first frame.
	if _, err := taiStream.Write([]byte("to-sdk")); err != nil {
		t.Fatalf("Tai write: %v", err)
	}
	if err := taiStream.Close(); err != nil {
		t.Fatalf("Tai close stream: %v", err)
	}
	reply, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("SDK read: %v", err)
	}
	if string(reply) != "to-sdk" {
		t.Errorf("SDK received %q, want to-sdk", reply)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// H3 streams expose QUIC datagrams through the ctrl stream.
	ls, ok := stream.(*LightStream)
	if !ok {
		t.Fatal("expected *LightStream")
	}
	dg := ls.DatagramConn()
	if dg == nil {
		t.Fatal("DatagramConn is nil for H3")
	}
	if err := dg.SendDatagram([]byte("dg")); err != nil {
		t.Fatalf("SendDatagram: %v", err)
	}
	gotDG, err := sess.ReceiveDatagram(ctx)
	if err != nil {
		t.Fatalf("ReceiveDatagram: %v", err)
	}
	if string(gotDG) != "dg" {
		t.Errorf("datagram = %q, want dg", gotDG)
	}
}

func TestLightH3WithoutIdentity(t *testing.T) {
	is := newIntegrationServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodeID := "light-h3-no-identity"
	sess := is.connectLightH3(t, ctx, nodeID)

	if _, err := is.srv.OpenLightStream(ctx, nodeID, FirstFrame(types.NamespaceSession, "open", nil, nil)); err != nil {
		t.Fatalf("OpenLightStream: %v", err)
	}

	_, first := acceptH3FirstFrame(t, ctx, sess)
	if first.Identity != nil {
		t.Errorf("identity = %+v, want nil (system task)", first.Identity)
	}
}

// ---------------------------------------------------------------------------
// Light over H2: identity passthrough
// ---------------------------------------------------------------------------

func TestLightH2PreservesFirstFrameIdentity(t *testing.T) {
	is := newIntegrationServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodeID := "light-h2-identity"
	fake := is.connectLightH2(t, ctx, nodeID)
	identity := identityFixture(t, is.auth, nodeID, types.Scope{types.SessionAccess, types.WorkspaceRead})

	stream, err := is.srv.OpenLightStream(ctx, nodeID, FirstFrame(types.NamespaceSession, "resume", []byte("resume-data"), identity))
	if err != nil {
		t.Fatalf("OpenLightStream: %v", err)
	}
	defer stream.Close()

	st := fake.accept(t, ctx)
	if st.first.Identity == nil {
		t.Fatal("Tai side received a first frame without identity")
	}
	if st.first.Identity.UserId != identity.UserId || st.first.Identity.TeamId != identity.TeamId {
		t.Errorf("identity = %+v, want %+v", st.first.Identity, identity)
	}
	if st.first.Kind != "resume" {
		t.Errorf("kind = %q, want resume", st.first.Kind)
	}

	// The SDK can read frames written by the Tai side.
	st.reply = FirstFrame(types.NamespaceSession, "opened", []byte("ok"), nil)
	close(st.done)
	reply, err := DefaultCodec().ReadFrame(stream)
	if err != nil {
		t.Fatalf("SDK read reply: %v", err)
	}
	if reply.Kind != "opened" {
		t.Errorf("reply kind = %q, want opened", reply.Kind)
	}
	if reply.Identity != nil {
		t.Errorf("reply identity = %+v, want nil", reply.Identity)
	}
}

// ---------------------------------------------------------------------------
// Heavy: identity passthrough
// ---------------------------------------------------------------------------

func TestHeavyPreservesFirstFrameIdentity(t *testing.T) {
	is := newIntegrationServer(t)
	nodeID := "heavy-identity"
	taiConn := is.connectHeavy(t, nodeID)
	identity := identityFixture(t, is.auth, nodeID, types.Scope{types.WorkspaceAll})

	conn, err := is.srv.HeavyPick(nodeID)
	if err != nil {
		t.Fatalf("HeavyPick: %v", err)
	}
	if is.srv.HeavyIdleCount(nodeID) != 0 {
		t.Errorf("HeavyIdleCount = %d, want 0 after pick", is.srv.HeavyIdleCount(nodeID))
	}

	if err := DefaultCodec().WriteFrame(conn, FirstFrame("workspace", "open", []byte(`{}`), identity)); err != nil {
		t.Fatalf("write heavy first frame: %v", err)
	}
	first, err := DefaultCodec().ReadFrame(taiConn)
	if err != nil {
		t.Fatalf("Tai read heavy frame: %v", err)
	}
	if first.Identity == nil || first.Identity.UserToken != identity.UserToken {
		t.Errorf("heavy identity = %+v, want %+v", first.Identity, identity)
	}

	// Tai replies and the SDK reads it back through the same Heavy connection.
	if err := DefaultCodec().WriteFrame(taiConn, FirstFrame("workspace", "open.result", []byte("ok"), nil)); err != nil {
		t.Fatalf("Tai write heavy reply: %v", err)
	}
	reply, err := DefaultCodec().ReadFrame(conn)
	if err != nil {
		t.Fatalf("SDK read heavy reply: %v", err)
	}
	if reply.Kind != "open.result" {
		t.Errorf("reply kind = %q, want open.result", reply.Kind)
	}

	is.srv.HeavyReturn(conn)
	if is.srv.HeavyIdleCount(nodeID) != 1 {
		t.Errorf("HeavyIdleCount = %d, want 1 after return", is.srv.HeavyIdleCount(nodeID))
	}
	is.srv.HeavyRemove(conn)
	if is.srv.HeavyConnCount(nodeID) != 0 {
		t.Errorf("HeavyConnCount = %d, want 0 after remove", is.srv.HeavyConnCount(nodeID))
	}
}

// ---------------------------------------------------------------------------
// Datagram error path on non-H3 streams
// ---------------------------------------------------------------------------

func TestDatagramUnsupportedOnH2(t *testing.T) {
	ls := NewLightStreamForTestWithProto(io.NopCloser(nil), nopWriteCloser{io.Discard}, "H2")
	if err := ls.DatagramConn().SendDatagram([]byte("x")); !errors.Is(err, ErrDatagramNotSupported) {
		t.Errorf("SendDatagram err = %v, want ErrDatagramNotSupported", err)
	}
	if _, err := ls.DatagramConn().ReceiveDatagram(context.Background()); !errors.Is(err, ErrDatagramNotSupported) {
		t.Errorf("ReceiveDatagram err = %v, want ErrDatagramNotSupported", err)
	}
	if ls.DatagramConn().Proto() != "H2" {
		t.Errorf("Proto = %q, want H2", ls.DatagramConn().Proto())
	}
}
