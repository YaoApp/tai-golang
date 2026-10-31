package session

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// testPair holds both ends of a mock Light stream for tests.
type testPair struct {
	stream  *transport.LightStream
	remoteR io.ReadCloser  // reads what session wrote
	remoteW io.WriteCloser // writes to session's read side
}

// close shuts down both remote ends to unblock any pending reads.
func (tp *testPair) close() {
	tp.remoteW.Close()
	tp.remoteR.Close()
}

func newTestPair() *testPair {
	sessionWritePR, sessionWritePW := io.Pipe()
	remoteWritePR, remoteWritePW := io.Pipe()

	stream := transport.NewLightStreamForTest(remoteWritePR, sessionWritePW)
	return &testPair{
		stream:  stream,
		remoteR: sessionWritePR,
		remoteW: remoteWritePW,
	}
}

func TestSessionNewAndBasicFields(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-1", "node-1", ctrl.stream, data.stream, codec)
	if s.ID() != "sess-1" {
		t.Errorf("ID() = %q, want sess-1", s.ID())
	}
	if s.NodeID() != "node-1" {
		t.Errorf("NodeID() = %q, want node-1", s.NodeID())
	}
	if s.Status() != types.SessionRunning {
		t.Errorf("Status() = %q, want running", s.Status())
	}
}

func TestSessionClose(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-2", "node-2", ctrl.stream, data.stream, codec)

	go func() {
		// Read the close command properly (4-byte header + protobuf body)
		codec.ReadFrame(ctrl.remoteR)

		// Send closed response
		closedData, _ := proto.Marshal(&pb.SessionClosed{
			SessionId: "sess-2",
			Reason:    "client_close",
		})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: "session",
			Kind:      "closed",
			Data:      closedData,
		})
		// Simulate Tai closing both stream directions after session destroy.
		ctrl.remoteW.Close()
		data.remoteW.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if s.Status() != types.SessionClosed {
		t.Errorf("Status() = %q, want closed", s.Status())
	}
}

func TestSessionDoubleClose(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-3", "node-3", ctrl.stream, data.stream, codec)

	go func() {
		// Read close command then respond
		codec.ReadFrame(ctrl.remoteR)

		closedData, _ := proto.Marshal(&pb.SessionClosed{
			SessionId: "sess-3",
			Reason:    "client_close",
		})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: "session",
			Kind:      "closed",
			Data:      closedData,
		})
		ctrl.remoteW.Close()
		data.remoteW.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Close(ctx)

	err := s.Close(ctx)
	if err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

func TestSessionSendCtrlPrompt(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-4", "node-4", ctrl.stream, data.stream, codec)

	msg := &types.UserMessage{
		Message: "hello world",
	}

	var wg sync.WaitGroup
	wg.Add(1)

	var gotFrame *pb.Payload
	go func() {
		defer wg.Done()
		f, err := codec.ReadFrame(ctrl.remoteR)
		if err != nil {
			t.Errorf("read ctrl frame: %v", err)
			return
		}
		gotFrame = f
	}()

	if err := s.Prompt(msg); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	wg.Wait()

	if gotFrame == nil {
		t.Fatal("no frame received")
	}
	if gotFrame.Kind != "message" {
		t.Errorf("frame.Kind = %q, want message", gotFrame.Kind)
	}
	var sm pb.SessionMessage
	if err := proto.Unmarshal(gotFrame.Data, &sm); err != nil {
		t.Fatalf("unmarshal SessionMessage: %v", err)
	}
	if sm.Kind != "prompt" {
		t.Errorf("SessionMessage.Kind = %q, want prompt", sm.Kind)
	}
	if sm.Seq != 1 {
		t.Errorf("SessionMessage.Seq = %d, want 1", sm.Seq)
	}
	if !bytes.Contains(sm.Data, []byte("hello world")) {
		t.Errorf("SessionMessage.Data = %s, want to contain 'hello world'", sm.Data)
	}
}

func TestSessionRecvEvent(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-5", "node-5", ctrl.stream, data.stream, codec)

	eventData := []byte(`{"messageId":"m1","content":{"type":"text","text":"hi"}}`)
	msgData, _ := proto.Marshal(&pb.SessionMessage{
		Seq:  1,
		Kind: "agent_message_chunk",
		Data: eventData,
	})
	go func() {
		codec.WriteFrame(data.remoteW, &pb.Payload{
			Namespace: "session",
			Kind:      "event",
			Data:      msgData,
		})
	}()

	evt, err := s.RecvEvent()
	if err != nil {
		t.Fatalf("RecvEvent: %v", err)
	}
	if evt.Kind != "agent_message_chunk" {
		t.Errorf("Kind = %q, want agent_message_chunk", evt.Kind)
	}
	if evt.Seq != 1 {
		t.Errorf("Seq = %d, want 1", evt.Seq)
	}
}

func TestSessionRecvAction(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-6", "node-6", ctrl.stream, data.stream, codec)

	actionData := []byte(`{"title":"execute rm -rf","options":[]}`)
	msgData, _ := proto.Marshal(&pb.SessionMessage{
		Seq:  1,
		Kind: "request_permission",
		Data: actionData,
	})
	go func() {
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: "session",
			Kind:      "message",
			Data:      msgData,
		})
	}()

	action, err := s.RecvAction()
	if err != nil {
		t.Fatalf("RecvAction: %v", err)
	}
	if action.Kind != "request_permission" {
		t.Errorf("Kind = %q, want request_permission", action.Kind)
	}
}

// readCtrlMsg reads a SessionMessage from the ctrl remote side and verifies the kind.
func readCtrlMsg(t *testing.T, codec *transport.FrameCodec, remoteR io.Reader, wantKind string) *pb.SessionMessage {
	t.Helper()
	frame, err := codec.ReadFrame(remoteR)
	if err != nil {
		t.Fatalf("read ctrl frame: %v", err)
	}
	var sm pb.SessionMessage
	if err := proto.Unmarshal(frame.Data, &sm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sm.Kind != wantKind {
		t.Errorf("ctrl kind = %q, want %q", sm.Kind, wantKind)
	}
	return &sm
}

// sendDispositionReply writes a disposition response on the ctrl remote writer.
func sendDispositionReply(codec *transport.FrameCodec, remoteW io.Writer, seq uint64, disp string) {
	data := []byte(`{"disposition":"` + disp + `"}`)
	msgData, _ := proto.Marshal(&pb.SessionMessage{Seq: seq, Kind: "disposition", Data: data})
	codec.WriteFrame(remoteW, &pb.Payload{Namespace: "session", Kind: "message", Data: msgData})
}

func TestSessionCancel(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-cancel", "node-1", ctrl.stream, data.stream, codec)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		readCtrlMsg(t, codec, ctrl.remoteR, "cancel")
	}()

	if err := s.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	wg.Wait()
}

func TestSessionFollowupDisposition(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-followup", "node-1", ctrl.stream, data.stream, codec)

	go func() {
		sm := readCtrlMsg(t, codec, ctrl.remoteR, "followup")
		sendDispositionReply(codec, ctrl.remoteW, sm.Seq, "queued")
	}()

	disp, err := s.Followup(&types.UserMessage{Message: "fix bug"})
	if err != nil {
		t.Fatalf("Followup: %v", err)
	}
	if disp != types.DispositionQueued {
		t.Errorf("disposition = %q, want queued", disp)
	}
}

func TestSessionSteerDisposition(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-steer", "node-1", ctrl.stream, data.stream, codec)

	go func() {
		sm := readCtrlMsg(t, codec, ctrl.remoteR, "steer")
		sendDispositionReply(codec, ctrl.remoteW, sm.Seq, "handled")
	}()

	disp, err := s.Steer(&types.UserMessage{Message: "try another approach"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if disp != types.DispositionHandled {
		t.Errorf("disposition = %q, want handled", disp)
	}
}

func TestSessionUpdateQueue(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-uq", "node-1", ctrl.stream, data.stream, codec)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sm := readCtrlMsg(t, codec, ctrl.remoteR, "update_queue")
		if !bytes.Contains(sm.Data, []byte(`"remove"`)) {
			t.Errorf("data missing 'remove': %s", sm.Data)
		}
	}()

	err := s.UpdateQueue(&types.QueueAction{Action: "remove", MessageID: "msg-1"})
	if err != nil {
		t.Fatalf("UpdateQueue: %v", err)
	}
	wg.Wait()
}

func TestSessionResolvePermission(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-rp", "node-1", ctrl.stream, data.stream, codec)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sm := readCtrlMsg(t, codec, ctrl.remoteR, "resolve_permission")
		if !bytes.Contains(sm.Data, []byte(`"req-123"`)) {
			t.Errorf("data missing requestId: %s", sm.Data)
		}
	}()

	err := s.ResolvePermission("req-123", &types.PermissionResponse{Outcome: "selected", OptionID: "allow"})
	if err != nil {
		t.Fatalf("ResolvePermission: %v", err)
	}
	wg.Wait()
}

func TestSessionResolveElicitation(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-re", "node-1", ctrl.stream, data.stream, codec)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sm := readCtrlMsg(t, codec, ctrl.remoteR, "resolve_elicitation")
		if !bytes.Contains(sm.Data, []byte(`"elic-1"`)) {
			t.Errorf("data missing id: %s", sm.Data)
		}
	}()

	err := s.ResolveElicitation("elic-1", &types.ElicitationResponse{Action: "accept"})
	if err != nil {
		t.Fatalf("ResolveElicitation: %v", err)
	}
	wg.Wait()
}

func TestSessionSetConfigOption(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-sco", "node-1", ctrl.stream, data.stream, codec)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sm := readCtrlMsg(t, codec, ctrl.remoteR, "set_config_option")
		if !bytes.Contains(sm.Data, []byte(`"model"`)) {
			t.Errorf("data missing configId: %s", sm.Data)
		}
	}()

	err := s.SetConfigOption("model", &types.ConfigOptionValue{Type: "id", Value: "deepseek-r1"})
	if err != nil {
		t.Fatalf("SetConfigOption: %v", err)
	}
	wg.Wait()
}

func TestSessionUpdateSession(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-us", "node-1", ctrl.stream, data.stream, codec)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sm := readCtrlMsg(t, codec, ctrl.remoteR, "update_session")
		if !bytes.Contains(sm.Data, []byte(`"full-access"`)) {
			t.Errorf("data missing access policy: %s", sm.Data)
		}
	}()

	policy := "full-access"
	err := s.UpdateSession(context.Background(), &types.SessionUpdateOptions{AccessPolicy: &policy})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	wg.Wait()
}

func TestSessionStartStopRealtime(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-rt", "node-1", ctrl.stream, data.stream, codec)

	if err := s.StartRealtime(&types.RealtimeConfig{}); err == nil {
		t.Error("StartRealtime: expected error, got nil")
	}
	if err := s.StopRealtime(); err == nil {
		t.Error("StopRealtime: expected error, got nil")
	}
}

func TestSessionRecvEventEOF(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()

	s := New("sess-eof", "node-1", ctrl.stream, data.stream, codec)

	// Close data remote writer → RecvEvent gets EOF
	data.remoteW.Close()

	_, err := s.RecvEvent()
	if err == nil {
		t.Error("RecvEvent: expected error on closed stream, got nil")
	}
}

func TestSessionRecvActionAfterDone(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-done", "node-1", ctrl.stream, data.stream, codec)

	// Close ctrl remote writer → readCtrlLoop exits → actions closed
	ctrl.remoteW.Close()

	// Give readCtrlLoop time to process the EOF
	time.Sleep(50 * time.Millisecond)

	_, err := s.RecvAction()
	if err != io.EOF && err != types.ErrSessionClosed {
		t.Errorf("RecvAction after close = %v, want EOF or ErrSessionClosed", err)
	}
}

func TestSessionFollowupAfterClose(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-fac", "node-1", ctrl.stream, data.stream, codec)

	go func() {
		codec.ReadFrame(ctrl.remoteR)
		closedData, _ := proto.Marshal(&pb.SessionClosed{SessionId: "sess-fac"})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{Namespace: "session", Kind: "closed", Data: closedData})
		ctrl.remoteW.Close()
		data.remoteW.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Close(ctx)

	_, err := s.Followup(&types.UserMessage{Message: "nope"})
	if err != types.ErrSessionClosed {
		t.Errorf("Followup after Close = %v, want ErrSessionClosed", err)
	}
}

func TestSessionSteerAfterClose(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-sac", "node-1", ctrl.stream, data.stream, codec)

	go func() {
		codec.ReadFrame(ctrl.remoteR)
		closedData, _ := proto.Marshal(&pb.SessionClosed{SessionId: "sess-sac"})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{Namespace: "session", Kind: "closed", Data: closedData})
		ctrl.remoteW.Close()
		data.remoteW.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Close(ctx)

	_, err := s.Steer(&types.UserMessage{Message: "nope"})
	if err != types.ErrSessionClosed {
		t.Errorf("Steer after Close = %v, want ErrSessionClosed", err)
	}
}

func TestSessionReadCtrlLoopBadProto(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-badproto", "node-1", ctrl.stream, data.stream, codec)

	// Send a frame with invalid protobuf in Data — readCtrlLoop should skip it
	codec.WriteFrame(ctrl.remoteW, &pb.Payload{
		Namespace: "session",
		Kind:      "message",
		Data:      []byte("not valid protobuf"),
	})

	// Follow with a valid action to confirm the loop continued
	actionData := []byte(`{"test":true}`)
	msgData, _ := proto.Marshal(&pb.SessionMessage{Seq: 1, Kind: "test_action", Data: actionData})
	go func() {
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{Namespace: "session", Kind: "message", Data: msgData})
	}()

	action, err := s.RecvAction()
	if err != nil {
		t.Fatalf("RecvAction: %v", err)
	}
	if action.Kind != "test_action" {
		t.Errorf("Kind = %q, want test_action", action.Kind)
	}
}

func TestSessionRecvEventBadProto(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	_ = New("sess-bevt", "node-1", ctrl.stream, data.stream, codec)

	// Send invalid protobuf on data stream
	go func() {
		codec.WriteFrame(data.remoteW, &pb.Payload{
			Namespace: "session",
			Kind:      "event",
			Data:      []byte("garbage"),
		})
	}()

	// RecvEvent should return an error
	s := New("sess-bevt2", "node-1", ctrl.stream, data.stream, codec)
	_, err := s.RecvEvent()
	if err == nil {
		t.Error("RecvEvent with bad protobuf: expected error, got nil")
	}
}

func TestSessionCtrlErrorFrame(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-err", "node-1", ctrl.stream, data.stream, codec)

	// Send error frame → readCtrlLoop should call markClosed
	errData, _ := proto.Marshal(&pb.SessionError{SessionId: "sess-err", Code: "runtime_crash", Message: "oops"})
	go func() {
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{Namespace: "session", Kind: "error", Data: errData})
		ctrl.remoteW.Close()
	}()

	// Wait for readCtrlLoop to process
	time.Sleep(50 * time.Millisecond)

	if s.Status() != types.SessionClosed {
		t.Errorf("Status after error frame = %q, want closed", s.Status())
	}
}

func TestSessionConcurrentSendCtrl(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-conc", "node-1", ctrl.stream, data.stream, codec)

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			s.Prompt(&types.UserMessage{Message: "concurrent"})
		}()
	}

	// Consume all n frames on the remote side
	for i := 0; i < n; i++ {
		frame, err := codec.ReadFrame(ctrl.remoteR)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if frame.Kind != "message" {
			t.Errorf("frame %d kind = %q, want message", i, frame.Kind)
		}
		var sm pb.SessionMessage
		if err := proto.Unmarshal(frame.Data, &sm); err != nil {
			t.Fatalf("unmarshal frame %d: %v", i, err)
		}
		if sm.Kind != "prompt" {
			t.Errorf("frame %d sm.Kind = %q, want prompt", i, sm.Kind)
		}
	}
	wg.Wait()
}

func TestSessionPromptAfterClose(t *testing.T) {
	codec := transport.DefaultCodec()
	ctrl := newTestPair()
	data := newTestPair()
	defer ctrl.close()
	defer data.close()

	s := New("sess-7", "node-7", ctrl.stream, data.stream, codec)

	go func() {
		// Read close command then respond
		codec.ReadFrame(ctrl.remoteR)

		closedData, _ := proto.Marshal(&pb.SessionClosed{SessionId: "sess-7"})
		codec.WriteFrame(ctrl.remoteW, &pb.Payload{
			Namespace: "session",
			Kind:      "closed",
			Data:      closedData,
		})
		ctrl.remoteW.Close()
		data.remoteW.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Close(ctx)

	err := s.Prompt(&types.UserMessage{Message: "after close"})
	if err != types.ErrSessionClosed {
		t.Errorf("Prompt after Close = %v, want ErrSessionClosed", err)
	}
}
