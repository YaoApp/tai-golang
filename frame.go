package tai

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// maxFrameSize is the maximum allowed frame payload size (16 MB).
const maxFrameSize = 16 * 1024 * 1024

// ErrFrameTooLarge is returned when a frame exceeds maxFrameSize.
var ErrFrameTooLarge = errors.New("frame too large")

// writeFrame writes a length-prefixed ControlMessage to w.
// Wire format: 4-byte big-endian length + protobuf payload.
func writeFrame(w io.Writer, msg *pb.ControlMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(data) > maxFrameSize {
		return ErrFrameTooLarge
	}

	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}

// readFrame reads a length-prefixed ControlMessage from r.
// Returns io.EOF when the stream ends cleanly.
func readFrame(r io.Reader) (*pb.ControlMessage, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header)
	if length > uint32(maxFrameSize) {
		return nil, ErrFrameTooLarge
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	msg := &pb.ControlMessage{}
	if err := proto.Unmarshal(payload, msg); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return msg, nil
}
