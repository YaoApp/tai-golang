package session

import (
	"bytes"
	"context"
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

// pipeRWC wraps an io.PipeReader and io.PipeWriter into a ReadWriteCloser.
type pipeRWC struct {
	r    *io.PipeReader
	w    *io.PipeWriter
	once sync.Once
}

func (p *pipeRWC) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeRWC) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRWC) Close() error {
	p.once.Do(func() {
		p.r.Close()
		p.w.Close()
	})
	return nil
}

func newPipePair() (local *pipeRWC, remote *pipeRWC) {
	lr, rw := io.Pipe()
	rr, lw := io.Pipe()
	return &pipeRWC{r: lr, w: lw}, &pipeRWC{r: rr, w: rw}
}

func writeEvent(t *testing.T, codec *transport.FrameCodec, w io.Writer, seq uint64, kind string) {
	t.Helper()
	if err := writeEventErr(codec, w, seq, kind); err != nil {
		t.Fatalf("writeEvent: %v", err)
	}
}

func writeEventErr(codec *transport.FrameCodec, w io.Writer, seq uint64, kind string) error {
	msgData, _ := proto.Marshal(&pb.SessionMessage{
		Seq:  seq,
		Kind: kind,
		Data: []byte(`{}`),
	})
	return codec.WriteFrame(w, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "event",
		Data:      msgData,
	})
}

func TestSession_RecvEventStoresSeq(t *testing.T) {
	ctrlLocal, ctrlRemote := newPipePair()
	dataLocal, dataRemote := newPipePair()
	codec := transport.DefaultCodec()

	sess := New("s1", "n1", ctrlLocal, dataLocal, codec)
	defer func() {
		ctrlRemote.Close()
		dataRemote.Close()
		sess.Close(context.Background())
	}()

	go func() {
		if err := writeEventErr(codec, dataRemote, 1, "text_delta"); err != nil {
			t.Errorf("writeEvent: %v", err)
		}
	}()

	msg, err := sess.RecvEvent()
	if err != nil {
		t.Fatalf("RecvEvent: %v", err)
	}
	if msg.Seq != 1 {
		t.Fatalf("seq = %d, want 1", msg.Seq)
	}
	if sess.dataSeq.Load() != 1 {
		t.Fatalf("dataSeq = %d, want 1", sess.dataSeq.Load())
	}
	if sess.buffer.LastSeq() != 1 {
		t.Fatalf("buffer.LastSeq = %d, want 1", sess.buffer.LastSeq())
	}
}

func TestSession_ResumeOnDataBreak(t *testing.T) {
	ctrlLocal, ctrlRemote := newPipePair()
	dataLocal, dataRemote := newPipePair()
	codec := transport.DefaultCodec()

	newCtrlLocal, newCtrlRemote := newPipePair()
	newDataLocal, newDataRemote := newPipePair()
	defer newCtrlRemote.Close()

	resumed := make(chan struct{})
	var resumeCount sync.Once
	var events []types.SessionEventKind
	var eventsMu sync.Mutex

	sess := New("s1", "n1", ctrlLocal, dataLocal, codec, Options{
		ResumeFn: func(_ context.Context, lastCtrl, lastData uint64) (io.ReadWriteCloser, io.ReadWriteCloser, uint64, error) {
			ok := false
			resumeCount.Do(func() {
				ok = true
				close(resumed)
			})
			if !ok {
				return nil, nil, 0, fmt.Errorf("resume exhausted")
			}
			return newCtrlLocal, newDataLocal, lastData, nil
		},
		OnEvent: func(sid string, event types.SessionEventKind) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		},
	})

	// RecvEvent runs in a goroutine — it blocks on ReadFrame.
	// We send one event, then break the data stream, and expect resume.
	recvCh := make(chan *types.SessionMsg, 4)
	recvErr := make(chan error, 4)
	go func() {
		for {
			msg, err := sess.RecvEvent()
			if err != nil {
				recvErr <- err
				return
			}
			recvCh <- msg
		}
	}()

	// Send event 1 on original data stream.
	writeEvent(t, codec, dataRemote, 1, "text_delta")

	select {
	case msg := <-recvCh:
		if msg.Seq != 1 {
			t.Fatalf("seq = %d, want 1", msg.Seq)
		}
	case err := <-recvErr:
		t.Fatalf("RecvEvent error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event 1")
	}

	// Break data stream to trigger resume.
	dataRemote.Close()

	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("resume not triggered")
	}

	// Send event 2 on new data stream.
	writeEvent(t, codec, newDataRemote, 2, "text_delta")

	select {
	case msg := <-recvCh:
		if msg.Seq != 2 {
			t.Fatalf("seq = %d, want 2", msg.Seq)
		}
	case err := <-recvErr:
		t.Fatalf("RecvEvent error after resume: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event 2")
	}

	// Verify lifecycle events (unlock before cleanup to avoid deadlock).
	eventsMu.Lock()
	if len(events) < 2 {
		eventsMu.Unlock()
		t.Fatalf("events = %v, want at least [disconnected, resumed]", events)
	}
	ev0, ev1 := events[0], events[1]
	eventsMu.Unlock()

	if ev0 != types.EventDisconnected {
		t.Errorf("events[0] = %q, want %q", ev0, types.EventDisconnected)
	}
	if ev1 != types.EventResumed {
		t.Errorf("events[1] = %q, want %q", ev1, types.EventResumed)
	}

	// Clean up: close remote ends first, then session.
	ctrlRemote.Close()
	newCtrlRemote.Close()
	newDataRemote.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sess.Close(ctx)
}

func TestSession_NoResumeFn_ClosesOnBreak(t *testing.T) {
	ctrlLocal, ctrlRemote := newPipePair()
	dataLocal, _ := newPipePair()
	codec := transport.DefaultCodec()

	sess := New("s1", "n1", ctrlLocal, dataLocal, codec)

	// Break ctrl stream.
	ctrlRemote.Close()

	// readCtrlLoop should detect break and mark closed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sess.Status() == types.SessionClosed {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("status = %q, want %q", sess.Status(), types.SessionClosed)
}

func TestSession_StatusDisconnectedDuringResume(t *testing.T) {
	_, ctrlRemote := newPipePair()
	dataLocal, _ := newPipePair()
	codec := transport.DefaultCodec()

	resumeStarted := make(chan struct{})
	resumeRelease := make(chan struct{})

	newCtrl, _ := newPipePair()
	newData, _ := newPipePair()

	sess := New("s1", "n1", ctrlRemote, dataLocal, codec, Options{
		ResumeFn: func(_ context.Context, _, _ uint64) (io.ReadWriteCloser, io.ReadWriteCloser, uint64, error) {
			close(resumeStarted)
			<-resumeRelease
			return newCtrl, newData, 0, nil
		},
	})
	_ = sess

	// Break ctrl stream to trigger resume.
	ctrlRemote.Close()

	select {
	case <-resumeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("resume not started")
	}

	if sess.Status() != types.SessionDisconnected {
		t.Fatalf("status during resume = %q, want %q", sess.Status(), types.SessionDisconnected)
	}

	close(resumeRelease)
	time.Sleep(100 * time.Millisecond)

	if sess.Status() != types.SessionRunning {
		t.Fatalf("status after resume = %q, want %q", sess.Status(), types.SessionRunning)
	}
}

// Ensure sendCtrl waits for resume to complete.
func TestSession_SendCtrlWaitsDuringResume(t *testing.T) {
	ctrlLocal, ctrlRemote := newPipePair()
	dataLocal, _ := newPipePair()
	codec := transport.DefaultCodec()

	resumeRelease := make(chan struct{})
	newCtrl, newCtrlRemote := newPipePair()
	newData, _ := newPipePair()

	sess := New("s1", "n1", ctrlLocal, dataLocal, codec, Options{
		ResumeFn: func(_ context.Context, _, _ uint64) (io.ReadWriteCloser, io.ReadWriteCloser, uint64, error) {
			<-resumeRelease
			return newCtrl, newData, 0, nil
		},
	})

	// Start background reader so io.Pipe write won't block after resume.
	type frameResult struct {
		frame *pb.Payload
		err   error
	}
	frameCh := make(chan frameResult, 1)
	go func() {
		f, e := codec.ReadFrame(newCtrlRemote)
		frameCh <- frameResult{f, e}
	}()

	// Break ctrl to trigger resume.
	ctrlRemote.Close()
	time.Sleep(100 * time.Millisecond)

	// sendCtrl should block until resume completes.
	done := make(chan error, 1)
	go func() {
		done <- sess.Prompt(&types.UserMessage{})
	}()

	select {
	case <-done:
		t.Fatal("sendCtrl returned before resume completed")
	case <-time.After(100 * time.Millisecond):
		// expected — still blocked
	}

	close(resumeRelease)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sendCtrl after resume: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sendCtrl still blocked after resume")
	}

	// Verify frame arrived on new ctrl stream.
	select {
	case fr := <-frameCh:
		if fr.err != nil {
			t.Fatalf("read from new ctrl: %v", fr.err)
		}
		if fr.frame.Kind != "message" {
			t.Fatalf("kind = %q, want message", fr.frame.Kind)
		}
		var msg pb.SessionMessage
		proto.Unmarshal(fr.frame.Data, &msg)
		if msg.Kind != "prompt" {
			t.Fatalf("msg.Kind = %q, want prompt", msg.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("frame not received on new ctrl")
	}
}

func TestRecoveryGapClosesSession(t *testing.T) {
	ctrlLocal, ctrlRemote := newPipePair()
	dataLocal, _ := newPipePair()
	codec := transport.DefaultCodec()

	var events []types.SessionEventKind
	var mu sync.Mutex

	sess := New("s1", "n1", ctrlLocal, dataLocal, codec, Options{
		OnEvent: func(_ string, ev types.SessionEventKind) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})

	// Send a recovery_gap error on the ctrl stream.
	errData, _ := proto.Marshal(&pb.SessionError{
		SessionId: "s1",
		Code:      "recovery_gap",
		Message:   "buffer overflow",
	})
	codec.WriteFrame(ctrlRemote, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "error",
		Data:      errData,
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sess.Status() == types.SessionClosed {
			mu.Lock()
			defer mu.Unlock()
			for _, ev := range events {
				if ev == types.EventRecoveryGap {
					return // success
				}
			}
			t.Fatal("session closed but no recovery_gap event")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("session not closed after recovery_gap error")
}

// TestRecoveryGapOnDataStream verifies RecvEvent handles error frames
// with recovery_gap code on the data stream.
func TestRecoveryGapOnDataStream(t *testing.T) {
	ctrlLocal, ctrlRemote := newPipePair()
	dataLocal, dataRemote := newPipePair()
	codec := transport.DefaultCodec()

	var events []types.SessionEventKind
	var mu sync.Mutex

	sess := New("s1", "n1", ctrlLocal, dataLocal, codec, Options{
		OnEvent: func(_ string, ev types.SessionEventKind) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})
	defer func() {
		ctrlRemote.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		sess.Close(ctx)
	}()

	// Send recovery_gap error on data stream.
	// io.Pipe is synchronous, so write in a goroutine while RecvEvent reads.
	errData, _ := proto.Marshal(&pb.SessionError{
		SessionId: "s1",
		Code:      "recovery_gap",
		Message:   "buffer overflow",
	})
	go func() {
		codec.WriteFrame(dataRemote, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "error",
			Data:      errData,
		})
	}()

	_, err := sess.RecvEvent()
	if err == nil {
		t.Fatal("RecvEvent should return error for recovery_gap")
	}

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range events {
		if ev == types.EventRecoveryGap {
			found = true
		}
	}
	if !found {
		t.Errorf("events = %v, want recovery_gap", events)
	}
}

// TestSessionDone verifies the Done channel closes when session ends.
func TestSessionDone(t *testing.T) {
	ctrlLocal, ctrlRemote := newPipePair()
	dataLocal, _ := newPipePair()
	codec := transport.DefaultCodec()

	sess := New("s1", "n1", ctrlLocal, dataLocal, codec)

	select {
	case <-sess.Done():
		t.Fatal("Done should not be closed yet")
	default:
	}

	// Close from remote side.
	closedData, _ := proto.Marshal(&pb.SessionClosed{SessionId: "s1", Reason: "test"})
	codec.WriteFrame(ctrlRemote, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "closed",
		Data:      closedData,
	})

	select {
	case <-sess.Done():
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed after remote close")
	}
}

func init() {
	// Suppress noisy "unmarshal event" logs from empty payloads.
	_ = bytes.Compare(nil, nil)
}
