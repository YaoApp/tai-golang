// Package transport implements the Tai Transport Protocol v2 server:
// TCP (HTTP/2), QUIC (HTTP/3, WebTransport), and the Tai frame protocol.
package transport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// DefaultMaxFrameSize is the default maximum frame payload size (16 MB).
const DefaultMaxFrameSize = 16 * 1024 * 1024

const (
	minFrameSize = 64 * 1024        // 64 KB
	maxFrameSize = 64 * 1024 * 1024 // 64 MB upper bound
)

// ErrFrameTooLarge is returned when a frame exceeds the configured max size.
var ErrFrameTooLarge = errors.New("frame too large")

// FrameCodec reads and writes length-prefixed Payload frames.
type FrameCodec struct {
	maxSize int
}

// NewFrameCodec creates a FrameCodec with the given maximum frame payload size.
// A maxSize of 0 uses DefaultMaxFrameSize (16 MB).
// Returns an error if maxSize is outside the allowed range [64 KB, 64 MB].
func NewFrameCodec(maxSize int) (*FrameCodec, error) {
	if maxSize == 0 {
		maxSize = DefaultMaxFrameSize
	}
	if maxSize < minFrameSize || maxSize > maxFrameSize {
		return nil, fmt.Errorf("maxFrameSize %d out of range [%d, %d]", maxSize, minFrameSize, maxFrameSize)
	}
	return &FrameCodec{maxSize: maxSize}, nil
}

// WriteFrame writes a length-prefixed Payload to w.
func (fc *FrameCodec) WriteFrame(w io.Writer, msg *pb.Payload) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(data) > fc.maxSize {
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

// ReadFrame reads a length-prefixed Payload from r.
func (fc *FrameCodec) ReadFrame(r io.Reader) (*pb.Payload, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header)
	if length > uint32(fc.maxSize) {
		return nil, ErrFrameTooLarge
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	msg := &pb.Payload{}
	if err := proto.Unmarshal(payload, msg); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return msg, nil
}

// MaxSize returns the configured maximum frame payload size.
func (fc *FrameCodec) MaxSize() int {
	return fc.maxSize
}

// DefaultCodec returns a FrameCodec with DefaultMaxFrameSize.
func DefaultCodec() *FrameCodec {
	return &FrameCodec{maxSize: DefaultMaxFrameSize}
}

// WriteFrame writes a length-prefixed Payload using the default codec.
func WriteFrame(w io.Writer, msg *pb.Payload) error {
	return DefaultCodec().WriteFrame(w, msg)
}

// ReadFrame reads a length-prefixed Payload using the default codec.
func ReadFrame(r io.Reader) (*pb.Payload, error) {
	return DefaultCodec().ReadFrame(r)
}

// WriteProtoMsg writes a length-prefixed protobuf message.
// Used for Connect/ConnectAck frames on Light and Heavy connections.
func WriteProtoMsg(w io.Writer, msg proto.Message, maxSize int) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(data) > maxSize {
		return ErrFrameTooLarge
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// ReadProtoMsg reads a length-prefixed protobuf message.
// Used for Connect/ConnectAck frames on Light and Heavy connections.
func ReadProtoMsg(r io.Reader, msg proto.Message, maxSize int) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header)
	if int(length) > maxSize {
		return ErrFrameTooLarge
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	return proto.Unmarshal(data, msg)
}
