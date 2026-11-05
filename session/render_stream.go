package session

import (
	"context"
	"encoding/json"
	"io"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// openRenderStream opens a dedicated render uni stream (SDK→Tai handshake,
// Tai→SDK data) and starts a read goroutine that feeds renderOut.
func (s *Session) openRenderStream(ctx context.Context, renderOut chan<- *types.RenderEvent) error {
	dataBytes, err := proto.Marshal(&pb.SessionData{SessionId: s.id})
	if err != nil {
		return err
	}

	stream, err := s.openStreamFn(ctx, s.nodeID, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "render",
		Data:      dataBytes,
	})
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.renderStream = stream
	s.mu.Unlock()

	go s.readRenderStream(ctx, stream, renderOut)
	return nil
}

// cancelableReader wraps an io.Reader with a cancel mechanism. Closing the
// cancelableReader unblocks any pending Read call by closing the underlying
// pipe, without requiring LightStream.Close() (which drains and blocks).
type cancelableReader struct {
	pr *io.PipeReader
	pw *io.PipeWriter
}

func newCancelableReader(src io.Reader) *cancelableReader {
	pr, pw := io.Pipe()
	cr := &cancelableReader{pr: pr, pw: pw}
	go func() {
		io.Copy(pw, src)
		pw.Close()
	}()
	return cr
}

func (cr *cancelableReader) Read(p []byte) (int, error) { return cr.pr.Read(p) }
func (cr *cancelableReader) Close() error               { return cr.pr.Close() }

// readRenderStream reads render frames from the dedicated render uni stream.
// Each frame is a FrameCodec length-prefixed Payload where Kind is the render
// event type (transcript.delta, emotion, etc.) and Data is the JSON body.
// When ctx is cancelled, the cancelable reader is closed to unblock ReadFrame.
func (s *Session) readRenderStream(ctx context.Context, stream io.ReadWriteCloser, renderOut chan<- *types.RenderEvent) {
	defer close(renderOut)

	cr := newCancelableReader(stream)
	go func() {
		<-ctx.Done()
		cr.Close()
	}()

	codec := transport.DefaultCodec()
	for {
		frame, err := codec.ReadFrame(cr)
		if err != nil {
			return
		}

		var evt types.RenderEvent
		if err := json.Unmarshal(frame.Data, &evt); err != nil {
			continue
		}
		evt.Type = frame.Kind

		select {
		case renderOut <- &evt:
		case <-ctx.Done():
			return
		}
	}
}
