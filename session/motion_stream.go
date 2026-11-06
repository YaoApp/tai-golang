package session

import (
	"context"
	"io"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// openMotionStreamH2 opens a dedicated motion uni stream for H2 transport
// (SDK→Tai handshake, Tai→SDK data) and starts a read goroutine that feeds
// motionOut. Symmetric to openRenderStream (render-protocol.md §2.13).
func (s *Session) openMotionStreamH2(ctx context.Context, motionOut chan<- *types.MotionFrame) error {
	dataBytes, err := proto.Marshal(&pb.SessionData{SessionId: s.id})
	if err != nil {
		return err
	}

	stream, err := s.openStreamFn(ctx, s.nodeID, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "motion",
		Data:      dataBytes,
	})
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.motionStream = stream
	s.mu.Unlock()

	go s.readMotionStreamH2(ctx, stream, motionOut)
	return nil
}

// readMotionStreamH2 reads motion frames from the H2 motion uni stream.
// Each frame is a FrameCodec length-prefixed Payload where Data is the
// 87-byte MotionFrame.Pack() binary (render-protocol.md §2.13).
// When ctx is cancelled, the cancelable reader is closed to unblock ReadFrame.
func (s *Session) readMotionStreamH2(ctx context.Context, stream io.ReadWriteCloser, motionOut chan<- *types.MotionFrame) {
	defer close(motionOut)

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
		mf, err := types.UnpackMotionFrame(frame.Data)
		if err != nil {
			continue
		}
		select {
		case motionOut <- mf:
		case <-ctx.Done():
			return
		}
	}
}
