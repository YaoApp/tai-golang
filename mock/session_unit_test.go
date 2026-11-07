package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// newPipeStream builds a MockLightStream backed by pipes: the test writes
// commands to cmdW and reads events from outR.
func newPipeStream() (stream *MockLightStream, cmdW io.WriteCloser, outR io.ReadCloser) {
	cmdPR, cmdPW := io.Pipe()
	outPR, outPW := io.Pipe()
	return &MockLightStream{
		r:     cmdPR,
		w:     outPW,
		codec: transport.DefaultCodec(),
	}, cmdPW, outPR
}

// bufWriteCloser captures frames written by a mock session handler.
type bufWriteCloser struct{ bytes.Buffer }

func (b *bufWriteCloser) Close() error { return nil }

// newInertStream returns a MockLightStream with an already-EOF read side and a
// buffer for the write side, so handler tests never block on pipes.
func newInertStream(first *pb.Payload) (*MockLightStream, *bufWriteCloser) {
	w := &bufWriteCloser{}
	return &MockLightStream{
		FirstFrame: first,
		r:          io.NopCloser(strings.NewReader("")),
		w:          w,
		codec:      transport.DefaultCodec(),
	}, w
}

func TestMockLightStreamFrameIO(t *testing.T) {
	codec := transport.DefaultCodec()

	// SendSessionMessage → wire frame readable on the remote end.
	t.Run("send session message", func(t *testing.T) {
		stream, cmdW, outR := newPipeStream()
		defer cmdW.Close()
		defer outR.Close()
		go func() { _ = stream.SendSessionMessage(1, "prompt", []byte(`{"m":1}`)) }()
		frame, err := codec.ReadFrame(outR)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if frame.Namespace != types.NamespaceSession || frame.Kind != "prompt" {
			t.Errorf("frame = %s/%s", frame.Namespace, frame.Kind)
		}
		var sm pb.SessionMessage
		if err := proto.Unmarshal(frame.Data, &sm); err != nil {
			t.Fatalf("unmarshal session message: %v", err)
		}
		if sm.Seq != 1 || sm.Kind != "prompt" {
			t.Errorf("session message = (%d,%q)", sm.Seq, sm.Kind)
		}
	})

	// SendFrame → wire frame.
	t.Run("send frame", func(t *testing.T) {
		stream, cmdW, outR := newPipeStream()
		defer cmdW.Close()
		defer outR.Close()
		go func() {
			_ = stream.SendFrame(&pb.Payload{Namespace: types.NamespaceControl, Kind: "custom", Data: []byte("x")})
		}()
		got, err := codec.ReadFrame(outR)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if got.Kind != "custom" || string(got.Data) != "x" {
			t.Errorf("sent frame = %+v", got)
		}
	})

	// Remote frame → RecvFrame.
	t.Run("recv frame", func(t *testing.T) {
		stream, cmdW, outR := newPipeStream()
		defer cmdW.Close()
		defer outR.Close()
		go func() {
			_ = codec.WriteFrame(cmdW, &pb.Payload{Namespace: types.NamespaceControl, Kind: "inbound", Data: []byte("y")})
		}()
		got, err := stream.RecvFrame()
		if err != nil {
			t.Fatalf("RecvFrame: %v", err)
		}
		if got.Kind != "inbound" || string(got.Data) != "y" {
			t.Errorf("recv frame = %+v", got)
		}
	})

	// Raw Read/Write proxy the underlying stream.
	t.Run("raw io", func(t *testing.T) {
		stream, cmdW, outR := newPipeStream()
		defer cmdW.Close()
		defer outR.Close()
		go func() { _, _ = stream.Write([]byte("raw")) }()
		buf := make([]byte, 3)
		if _, err := io.ReadFull(outR, buf); err != nil {
			t.Fatalf("remote read: %v", err)
		}
		if string(buf) != "raw" {
			t.Errorf("raw = %q, want raw", buf)
		}
		go func() { _, _ = cmdW.Write([]byte("cmd")) }()
		readBuf := make([]byte, 3)
		if _, err := io.ReadFull(stream, readBuf); err != nil {
			t.Fatalf("stream read: %v", err)
		}
		if string(readBuf) != "cmd" {
			t.Errorf("stream read = %q, want cmd", readBuf)
		}
	})
}

func TestMockLightConnHelpers(t *testing.T) {
	lc := &MockLightConn{proto: "H2", streams: make(chan *MockLightStream)}
	if lc.Protocol() != "H2" {
		t.Errorf("Protocol = %q", lc.Protocol())
	}
	lc.Close()
	lc.Close() // idempotent

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lc2 := &MockLightConn{streams: make(chan *MockLightStream)}
	if _, err := lc2.AcceptStream(ctx); err == nil {
		t.Error("AcceptStream should return ctx error")
	}
	// Closed stream channel returns an error.
	closedCh := make(chan *MockLightStream)
	close(closedCh)
	if _, err := (&MockLightConn{streams: closedCh}).AcceptStream(context.Background()); err == nil {
		t.Error("AcceptStream on closed channel should error")
	}

	// A closed connection (done channel) rejects further streams.
	doneCh := make(chan *MockLightStream)
	lc3 := &MockLightConn{streams: doneCh, done: make(chan struct{})}
	lc3.signalDone()
	if _, err := lc3.AcceptStream(context.Background()); err == nil {
		t.Error("AcceptStream on a closed connection should error")
	}
	lc3.signalDone() // idempotent

	// A stream arriving after the connection is gone must not be published.
	lc4 := &MockLightConn{streams: doneCh, done: make(chan struct{})}
	lc4.signalDone()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tai/stream", bytes.NewReader(buildFrameBytes(t, &pb.Payload{Kind: "open"})))
	lc4.handleH2Stream(rec, req, transport.DefaultCodec())
	if rec.Code != http.StatusOK {
		t.Errorf("late stream status = %d, want 200", rec.Code)
	}
}

// buildFrameBytes encodes a length-prefixed Payload frame.
func buildFrameBytes(t *testing.T, frame *pb.Payload) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := transport.DefaultCodec().WriteFrame(&buf, frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	return buf.Bytes()
}

func TestFlushWriter(t *testing.T) {
	fw := flushWriter{w: &recordingResponseWriter{}}
	if _, err := fw.Write([]byte("x")); err != nil {
		t.Errorf("Write: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

type recordingResponseWriter struct {
	header http.Header
	body   []byte
}

func (w *recordingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *recordingResponseWriter) WriteHeader(int) {}
func (w *recordingResponseWriter) Write(p []byte) (int, error) {
	w.body = append(w.body, p...)
	return len(p), nil
}

// ---------------------------------------------------------------------------
// Session handler
// ---------------------------------------------------------------------------

func TestSessionHandlerRoutingErrors(t *testing.T) {
	cases := []struct {
		name  string
		first *pb.Payload
	}{
		{name: "unknown kind", first: &pb.Payload{Kind: "bogus"}},
		{name: "open bad payload", first: &pb.Payload{Namespace: types.NamespaceSession, Kind: "open", Data: []byte("bad")}},
		{name: "resume bad payload", first: &pb.Payload{Namespace: types.NamespaceSession, Kind: "resume", Data: []byte("bad")}},
		{name: "data bad payload", first: &pb.Payload{Namespace: types.NamespaceSession, Kind: "data", Data: []byte("bad")}},
		{name: "audio bad payload", first: &pb.Payload{Namespace: types.NamespaceSession, Kind: "audio", Data: []byte("bad")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr, pw := io.Pipe()
			stream := &MockLightStream{FirstFrame: tc.first, r: pr, w: pw, codec: transport.DefaultCodec()}
			sh := NewSessionHandler(nil)
			sh.HandleStream(stream)
			pw.Close()
		})
	}
}

func TestSessionHandlerHandleDataAndAudio(t *testing.T) {
	sh := NewSessionHandler(nil)
	ctrl, ctrlW, ctrlR := newPipeStream()
	defer ctrlW.Close()
	defer ctrlR.Close()
	ms := &MockSession{ID: "s-bind", ctrl: ctrl, codec: transport.DefaultCodec()}
	sh.sessions["s-bind"] = ms

	dataStream, _, dataR := newPipeStream()
	defer dataR.Close()
	dataData, _ := proto.Marshal(&pb.SessionData{SessionId: "s-bind"})
	dataStream.FirstFrame = &pb.Payload{Namespace: types.NamespaceSession, Kind: "data", Data: dataData}
	sh.handleData(dataStream)
	if ms.data != dataStream {
		t.Error("handleData did not bind the data stream")
	}

	audioStream, _, audioR := newPipeStream()
	defer audioR.Close()
	audioStream.FirstFrame = &pb.Payload{Namespace: types.NamespaceSession, Kind: "audio", Data: dataData}
	sh.handleAudio(audioStream)
	if ms.audio != audioStream {
		t.Error("handleAudio did not bind the audio stream")
	}

	if sh.Session("s-bind") != ms {
		t.Error("Session did not return the registered session")
	}
	if sh.Session("missing") != nil {
		t.Error("Session should return nil for unknown ids")
	}
}

func TestSessionHandlerUnknownSessionStreams(t *testing.T) {
	sh := NewSessionHandler(nil)
	for _, kind := range []string{"data", "audio"} {
		stream, _ := newInertStream(&pb.Payload{Kind: kind, Data: mustProto(t, &pb.SessionData{SessionId: "unknown"})})
		switch kind {
		case "data":
			sh.handleData(stream)
		case "audio":
			sh.handleAudio(stream)
		}
	}
}

func TestSessionHandlerResumeUnknownAndKnown(t *testing.T) {
	sh := NewSessionHandler(nil)
	codec := transport.DefaultCodec()

	// Unknown session → error frame.
	stream, w := newInertStream(&pb.Payload{Kind: "resume", Data: mustProto(t, &pb.SessionResume{SessionId: "nope"})})
	sh.handleResume(stream)
	frame, err := codec.ReadFrame(w)
	if err != nil {
		t.Fatalf("read error frame: %v", err)
	}
	if frame.Kind != "error" {
		t.Errorf("kind = %q, want error", frame.Kind)
	}

	// Known session → opened with lastDataSeq and identity refresh.
	ctrl, _ := newInertStream(&pb.Payload{})
	old := &MockSession{ID: "s-resume", ctrl: ctrl, dataSeq: 42, codec: codec}
	sh.sessions["s-resume"] = old
	identity := &pb.Identity{UserId: "u", TeamId: "t", UserToken: "tok"}
	newCtrl, w := newInertStream(&pb.Payload{
		Kind:     "resume",
		Data:     mustProto(t, &pb.SessionResume{SessionId: "s-resume"}),
		Identity: identity,
	})
	sh.handleResume(newCtrl)
	openedFrame, err := codec.ReadFrame(w)
	if err != nil {
		t.Fatalf("read opened: %v", err)
	}
	if openedFrame.Kind != "opened" {
		t.Fatalf("kind = %q, want opened", openedFrame.Kind)
	}
	var opened pb.SessionOpened
	if err := proto.Unmarshal(openedFrame.Data, &opened); err != nil {
		t.Fatalf("unmarshal opened: %v", err)
	}
	if opened.LastDataSeq != 42 {
		t.Errorf("lastDataSeq = %d, want 42", opened.LastDataSeq)
	}
	if old.Identity != identity {
		t.Errorf("resume identity = %+v, want %+v", old.Identity, identity)
	}
}

func TestSessionHandlerOpenSetsIdentity(t *testing.T) {
	sh := NewSessionHandler(nil)
	identity := &pb.Identity{UserId: "u1", TeamId: "t1", UserToken: "tok1"}
	stream, w := newInertStream(&pb.Payload{
		Kind:     "open",
		Data:     mustProto(t, &pb.SessionOpen{SessionId: "s-open-id", Request: &pb.SessionRequest{Runtime: "process"}}),
		Identity: identity,
	})
	sh.HandleStream(stream)
	sess := sh.Session("s-open-id")
	if sess == nil {
		t.Fatal("session not registered")
	}
	if sess.Identity != identity {
		t.Errorf("identity = %+v, want %+v", sess.Identity, identity)
	}
	if sess.Request == nil || sess.Request.Runtime != "process" {
		t.Errorf("request = %+v", sess.Request)
	}
	if _, err := transport.DefaultCodec().ReadFrame(w); err != nil {
		t.Errorf("read opened frame: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MockSession commands
// ---------------------------------------------------------------------------

func TestMockSessionCloseAndSend(t *testing.T) {
	codec := transport.DefaultCodec()

	// Send writes an event frame on the data stream.
	stream, _, _ := newPipeStream()
	outPR, outPW := io.Pipe()
	data := &MockLightStream{r: io.NopCloser(strings.NewReader("")), w: outPW, codec: codec}
	ms := &MockSession{ID: "s-cmd", ctrl: stream, data: data, codec: codec}
	go func() { _ = ms.Send("text_delta", map[string]string{"text": "hi"}) }()
	frame, err := codec.ReadFrame(outPR)
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	if frame.Kind != "event" {
		t.Errorf("event frame kind = %q, want event", frame.Kind)
	}
	var sm pb.SessionMessage
	if err := proto.Unmarshal(frame.Data, &sm); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if sm.Kind != "text_delta" {
		t.Errorf("event kind = %q", sm.Kind)
	}
	// Marshal failure path.
	if err := ms.Send("bad", func() {}); err == nil {
		t.Error("Send with unencodable payload should fail")
	}

	// SendClosed emits the closed handshake frame then closes.
	closedCtrl, closedW := newInertStream(&pb.Payload{})
	ms3 := &MockSession{ID: "s-closed-3", ctrl: closedCtrl, codec: codec}
	if err := ms3.SendClosed("done"); err != nil {
		t.Fatalf("SendClosed: %v", err)
	}
	closedFrame, err := codec.ReadFrame(closedW)
	if err != nil {
		t.Fatalf("read closed: %v", err)
	}
	if closedFrame.Kind != "closed" {
		t.Errorf("closed kind = %q", closedFrame.Kind)
	}
	if err := ms3.SendClosed("again"); err != nil {
		t.Errorf("second SendClosed: %v", err)
	}

	// A session with no data stream rejects Send.
	ms4 := &MockSession{ID: "s-nodata", ctrl: closedCtrl, codec: codec}
	if err := ms4.Send("x", nil); err == nil {
		t.Error("Send without data stream should fail")
	}
	ms4.Close()
	ms4.Close() // idempotent
	outPR.Close()
	outPW.Close()
}

func TestMockSessionSendAction(t *testing.T) {
	codec := transport.DefaultCodec()
	cmdPR, cmdPW := io.Pipe()
	outPR, outPW := io.Pipe()
	ctrl := &MockLightStream{r: cmdPR, w: outPW, codec: codec}
	ms := &MockSession{ID: "s-action", ctrl: ctrl, codec: codec}

	go func() { _ = ms.SendAction("realtime.started", map[string]any{"ok": true}) }()
	frame, err := codec.ReadFrame(outPR)
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	if frame.Kind != "event" {
		t.Errorf("action frame kind = %q, want event", frame.Kind)
	}
	var msg pb.SessionMessage
	if err := proto.Unmarshal(frame.Data, &msg); err != nil {
		t.Fatalf("unmarshal action: %v", err)
	}
	if msg.Kind != "realtime.started" {
		t.Errorf("action kind = %q", msg.Kind)
	}
	if err := ms.SendAction("bad", func() {}); err == nil {
		t.Error("SendAction with unencodable payload should fail")
	}
	cmdPW.Close()
}

func TestMockSessionRecvCtrl(t *testing.T) {
	codec := transport.DefaultCodec()
	cmdPR, cmdPW := io.Pipe()
	outPR, outPW := io.Pipe()
	ctrl := &MockLightStream{r: cmdPR, w: outPW, codec: codec}
	ms := &MockSession{ID: "s-recv", ctrl: ctrl, codec: codec}

	go func() {
		payload, _ := proto.Marshal(&pb.SessionMessage{Seq: 5, Kind: "prompt", Data: []byte("hi")})
		_ = codec.WriteFrame(cmdPW, &pb.Payload{Namespace: types.NamespaceSession, Kind: "message", Data: payload})
	}()
	msg, err := ms.RecvCtrl()
	if err != nil {
		t.Fatalf("RecvCtrl: %v", err)
	}
	if msg.Seq != 5 || msg.Kind != "prompt" {
		t.Errorf("RecvCtrl = %+v", msg)
	}
	cmdPW.Close()
	outPR.Close()
}

func TestMockSessionRealtimeAutoReply(t *testing.T) {
	codec := transport.DefaultCodec()
	cmdR, cmdW := io.Pipe()
	outR, outW := io.Pipe()
	ctrl := &MockLightStream{r: cmdR, w: outW, codec: codec}
	ms := &MockSession{ID: "s-auto", ctrl: ctrl, codec: codec}
	ms.RealtimeAutoReply(7)

	payload, _ := proto.Marshal(&pb.SessionMessage{Kind: "realtime.start", Data: []byte("{}")})
	if err := codec.WriteFrame(cmdW, &pb.Payload{Namespace: types.NamespaceSession, Kind: "message", Data: payload}); err != nil {
		t.Fatalf("write command: %v", err)
	}

	frame, err := codec.ReadFrame(outR)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	var reply pb.SessionMessage
	if err := proto.Unmarshal(frame.Data, &reply); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if reply.Kind != "realtime.started" {
		t.Errorf("reply kind = %q", reply.Kind)
	}
	var started struct {
		AudioChannelID uint32 `json:"audio_channel_id"`
	}
	if err := json.Unmarshal(reply.Data, &started); err != nil {
		t.Fatalf("unmarshal started: %v", err)
	}
	if started.AudioChannelID != 7 {
		t.Errorf("audio_channel_id = %d, want 7", started.AudioChannelID)
	}

	for _, kind := range []string{"realtime.update", "realtime.stop", "audio.commit", "audio.clear", "audio.interrupt", "response.create"} {
		payload, _ := proto.Marshal(&pb.SessionMessage{Kind: kind, Data: []byte("{}")})
		if err := codec.WriteFrame(cmdW, &pb.Payload{Namespace: types.NamespaceSession, Kind: "message", Data: payload}); err != nil {
			t.Fatalf("write %s: %v", kind, err)
		}
		if kind != "response.create" {
			if _, err := codec.ReadFrame(outR); err != nil {
				t.Fatalf("read reply for %s: %v", kind, err)
			}
		}
	}
	cmdW.Close()
}

func TestMockSessionFeedAudio(t *testing.T) {
	codec := transport.DefaultCodec()
	stream, _, remoteR := newPipeStream()
	defer remoteR.Close()
	ms := &MockSession{ID: "s-feed", codec: codec}
	go func() { _ = ms.FeedAudio(stream, []byte{1, 2, 3}) }()
	frame, err := codec.ReadFrame(remoteR)
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	if frame.Kind != "audio" || len(frame.Data) != 3 {
		t.Errorf("audio frame = %+v", frame)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func mustProto(t *testing.T, m proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestMockLightStreamWriteErrors(t *testing.T) {
	stream := &MockLightStream{
		r:     io.NopCloser(strings.NewReader("")),
		w:     failWriter{},
		codec: transport.DefaultCodec(),
	}
	if err := stream.SendSessionMessage(1, "prompt", nil); err == nil {
		t.Error("SendSessionMessage should surface write errors")
	}
	if err := stream.SendFrame(&pb.Payload{}); err == nil {
		t.Error("SendFrame should surface write errors")
	}
}

func TestMockSessionRecvCtrlBadFrame(t *testing.T) {
	codec := transport.DefaultCodec()
	cmdPR, cmdPW := io.Pipe()
	ctrl := &MockLightStream{r: cmdPR, w: nopWriteCloser{}, codec: codec}
	ms := &MockSession{ID: "s-bad", ctrl: ctrl, codec: codec}

	go func() {
		_ = codec.WriteFrame(cmdPW, &pb.Payload{Namespace: types.NamespaceSession, Kind: "message", Data: []byte{0xff, 0xff}})
	}()
	if _, err := ms.RecvCtrl(); err == nil {
		t.Error("RecvCtrl should surface unmarshal errors")
	}
	cmdPW.Close()
}
