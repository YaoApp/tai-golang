package session

import (
	"context"
	"io"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// openAudioStreamH2 opens a dedicated audio bidi stream for H2 transport
// and starts read/write goroutines. Tai identifies the session by
// SessionData{SessionId} in the first frame; channelID comes from the
// prior realtime.started handshake stored in sessionBridge.
func (s *Session) openAudioStreamH2(ctx context.Context, channelID uint32, audioOut chan<- *types.AudioFrame) error {
	dataBytes, err := proto.Marshal(&pb.SessionData{SessionId: s.id})
	if err != nil {
		return err
	}

	stream, err := s.openStreamFn(ctx, s.nodeID, &pb.Payload{
		Namespace: types.NamespaceSession,
		Kind:      "audio",
		Data:      dataBytes,
	})
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.audioStream = stream
	s.mu.Unlock()

	go s.readAudioStreamH2(ctx, channelID, stream, audioOut)
	return nil
}

// readAudioStreamH2 reads framed audio data from the H2 audio bidi stream,
// decodes each frame, applies stale frame detection (seq regression), and
// pushes valid frames to audioOut.
func (s *Session) readAudioStreamH2(ctx context.Context, channelID uint32, stream io.Reader, audioOut chan<- *types.AudioFrame) {
	defer close(audioOut)
	codec := transport.DefaultCodec()
	var lastSeq uint32

	for {
		frame, err := codec.ReadFrame(stream)
		if err != nil {
			return
		}
		ch, seq, pcm, err := transport.DecodeAudioFrame(frame.Data)
		if err != nil || ch != channelID {
			continue
		}
		// Stale frame detection: drop frames with seq not advancing.
		if seq <= lastSeq && lastSeq > 0 {
			continue
		}
		lastSeq = seq

		select {
		case audioOut <- &types.AudioFrame{Data: pcm, Seq: seq}:
		case <-ctx.Done():
			return
		}
	}
}
