package mock

import (
	"context"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// TestSessionResumeProtocol verifies the full resume protocol through real
// transport: open → events → disconnect → resume → opened(lastDataSeq).
func TestSessionResumeProtocol(t *testing.T) {
	ts := NewTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mn := NewMockNode("resume-node", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken("resume-node"))
	if err := mn.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mn.Stop)

	lc, err := mn.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect light: %v", err)
	}
	t.Cleanup(lc.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.LightConnCount("resume-node") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("light connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	sessionReady := make(chan *MockSession, 1)
	sh := NewSessionHandler(func(sess *MockSession) {
		sessionReady <- sess
	})
	go sh.AcceptAndHandle(ctx, lc)

	codec := transport.DefaultCodec()

	// Step 1: open session ctrl stream from SDK side.
	openData, _ := proto.Marshal(&pb.SessionOpen{
		SessionId: "s-resume-1",
		NodeId:    "resume-node",
		Request:   &pb.SessionRequest{Runtime: "process"},
	})
	ctrlStream, err := ts.OpenLightStream(ctx, "resume-node", &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "open",
		Data:      openData,
	})
	if err != nil {
		t.Fatalf("open ctrl: %v", err)
	}

	// Read opened response.
	openedFrame, err := codec.ReadFrame(ctrlStream)
	if err != nil {
		t.Fatalf("read opened: %v", err)
	}
	if openedFrame.Kind != "opened" {
		t.Fatalf("kind = %q, want opened", openedFrame.Kind)
	}

	// Wait for mock session.
	var mockSess *MockSession
	select {
	case mockSess = <-sessionReady:
	case <-time.After(3 * time.Second):
		t.Fatal("mock session not created")
	}

	// Step 2: bind data stream.
	dataData, _ := proto.Marshal(&pb.SessionData{SessionId: "s-resume-1"})
	dataStream, err := ts.OpenLightStream(ctx, "resume-node", &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "data",
		Data:      dataData,
	})
	if err != nil {
		t.Fatalf("open data: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // wait for data bind

	// Step 3: Tai sends events.
	for i := 0; i < 3; i++ {
		if err := mockSess.Send("text_delta", map[string]string{"text": "hello"}); err != nil {
			t.Fatalf("send event %d: %v", i, err)
		}
	}

	// SDK reads events.
	for i := 0; i < 3; i++ {
		frame, err := codec.ReadFrame(dataStream)
		if err != nil {
			t.Fatalf("read event %d: %v", i, err)
		}
		if frame.Kind != "event" {
			t.Errorf("event[%d] kind = %q, want event", i, frame.Kind)
		}
	}

	// Step 4: simulate disconnect — close in background (LightStream.Close
	// drains read side which blocks until the mock closes its write side).
	go ctrlStream.Close()
	go dataStream.Close()
	time.Sleep(200 * time.Millisecond)

	// Step 5: resume — open new ctrl with "resume" kind.
	resumeData, _ := proto.Marshal(&pb.SessionResume{
		SessionId:   "s-resume-1",
		Token:       "",
		LastCtrlSeq: 0,
		LastDataSeq: 2, // SDK received up to seq 2
	})
	resumeCtrl, err := ts.OpenLightStream(ctx, "resume-node", &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "resume",
		Data:      resumeData,
	})
	if err != nil {
		t.Fatalf("open resume ctrl: %v", err)
	}
	t.Cleanup(func() { go resumeCtrl.Close() })

	// Read opened response with lastDataSeq.
	resumeOpened, err := codec.ReadFrame(resumeCtrl)
	if err != nil {
		t.Fatalf("read resume opened: %v", err)
	}
	if resumeOpened.Kind != "opened" {
		t.Fatalf("resume kind = %q, want opened", resumeOpened.Kind)
	}
	var openedMsg pb.SessionOpened
	proto.Unmarshal(resumeOpened.Data, &openedMsg)
	if openedMsg.SessionId != "s-resume-1" {
		t.Errorf("session_id = %q, want s-resume-1", openedMsg.SessionId)
	}
	if openedMsg.LastDataSeq != 3 {
		t.Errorf("lastDataSeq = %d, want 3 (all events sent)", openedMsg.LastDataSeq)
	}

	// Step 6: bind new data stream.
	newDataData, _ := proto.Marshal(&pb.SessionData{SessionId: "s-resume-1"})
	newDataStream, err := ts.OpenLightStream(ctx, "resume-node", &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "data",
		Data:      newDataData,
	})
	if err != nil {
		t.Fatalf("open new data: %v", err)
	}
	t.Cleanup(func() { go newDataStream.Close() })
	time.Sleep(100 * time.Millisecond)

	// Step 7: Tai sends more events after resume.
	if err := mockSess.Send("text_delta", map[string]string{"text": "resumed!"}); err != nil {
		t.Fatalf("send post-resume event: %v", err)
	}

	frame, err := codec.ReadFrame(newDataStream)
	if err != nil {
		t.Fatalf("read post-resume event: %v", err)
	}
	if frame.Kind != "event" {
		t.Errorf("post-resume kind = %q, want event", frame.Kind)
	}
}

// TestSessionResumeUnknownSessionProtocol verifies resume for non-existent
// session returns error through real transport.
func TestSessionResumeUnknownSessionProtocol(t *testing.T) {
	ts := NewTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mn := NewMockNode("resume-unknown-node", ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken("resume-unknown-node"))
	if err := mn.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mn.Stop)

	lc, err := mn.ConnectLightH3(ctx)
	if err != nil {
		t.Fatalf("connect light: %v", err)
	}
	t.Cleanup(lc.Close)

	deadline := time.Now().Add(3 * time.Second)
	for ts.LightConnCount("resume-unknown-node") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("light connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	sh := NewSessionHandler(nil)
	go sh.AcceptAndHandle(ctx, lc)

	codec := transport.DefaultCodec()

	resumeData, _ := proto.Marshal(&pb.SessionResume{
		SessionId: "nonexistent",
		Token:     "",
	})
	stream, err := ts.OpenLightStream(ctx, "resume-unknown-node", &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "resume",
		Data:      resumeData,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { go stream.Close() })

	frame, err := codec.ReadFrame(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if frame.Kind != "error" {
		t.Fatalf("kind = %q, want error", frame.Kind)
	}
	var errMsg pb.SessionError
	proto.Unmarshal(frame.Data, &errMsg)
	if errMsg.Code != "session_not_found" {
		t.Errorf("code = %q, want session_not_found", errMsg.Code)
	}
}
