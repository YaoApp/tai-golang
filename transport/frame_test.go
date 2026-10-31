package transport

import (
	"bytes"
	"io"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

func TestFrameRoundTrip(t *testing.T) {
	msg := &pb.Payload{Namespace: types.NamespaceControl, Kind: "test", Data: []byte("hello")}

	var buf bytes.Buffer
	if err := WriteFrame(&buf, msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Kind != "test" || string(got.Data) != "hello" {
		t.Errorf("got kind=%q data=%q", got.Kind, got.Data)
	}
}

func TestFrameMultiple(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 5; i++ {
		msg := &pb.Payload{Namespace: types.NamespaceControl, Kind: "ping", Data: []byte{byte(i)}}
		if err := WriteFrame(&buf, msg); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	for i := 0; i < 5; i++ {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if got.Kind != "ping" {
			t.Errorf("frame %d kind=%q", i, got.Kind)
		}
	}

	_, err := ReadFrame(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestFrameMaxSize(t *testing.T) {
	huge := make([]byte, maxFrameSize+1)
	msg := &pb.Payload{Namespace: types.NamespaceControl, Kind: "big", Data: huge}

	var buf bytes.Buffer
	err := WriteFrame(&buf, msg)
	if err != ErrFrameTooLarge {
		t.Errorf("got %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameReadTruncated(t *testing.T) {
	msg := &pb.Payload{Namespace: types.NamespaceControl, Kind: "test", Data: []byte("data")}
	var buf bytes.Buffer
	WriteFrame(&buf, msg)

	truncated := buf.Bytes()[:6]
	_, err := ReadFrame(bytes.NewReader(truncated))
	if err == nil {
		t.Error("expected error for truncated frame")
	}
}

type errWriter struct{}

func (e *errWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestFrameWriteError(t *testing.T) {
	msg := &pb.Payload{Namespace: types.NamespaceControl, Kind: "test"}
	err := WriteFrame(&errWriter{}, msg)
	if err == nil {
		t.Error("expected error from broken writer")
	}
}

func TestNewFrameCodecDefault(t *testing.T) {
	c, err := NewFrameCodec(0)
	if err != nil {
		t.Fatalf("NewFrameCodec(0): %v", err)
	}
	if c.MaxSize() != DefaultMaxFrameSize {
		t.Errorf("MaxSize() = %d, want %d", c.MaxSize(), DefaultMaxFrameSize)
	}
}

func TestNewFrameCodecTooSmall(t *testing.T) {
	_, err := NewFrameCodec(1024)
	if err == nil {
		t.Error("expected error for frame size below minimum")
	}
}

func TestNewFrameCodecTooLarge(t *testing.T) {
	_, err := NewFrameCodec(128 * 1024 * 1024)
	if err == nil {
		t.Error("expected error for frame size above maximum")
	}
}

func TestFrameCodecWriteRead(t *testing.T) {
	c, _ := NewFrameCodec(256 * 1024)
	var buf bytes.Buffer

	msg := &pb.Payload{Namespace: types.NamespaceControl, Kind: "test", Data: []byte("hello")}
	if err := c.WriteFrame(&buf, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := c.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Kind != "test" {
		t.Errorf("Kind = %q, want test", got.Kind)
	}
}

func TestDefaultCodec(t *testing.T) {
	c := DefaultCodec()
	if c.MaxSize() != DefaultMaxFrameSize {
		t.Errorf("MaxSize() = %d, want %d", c.MaxSize(), DefaultMaxFrameSize)
	}
}

func TestWriteReadProtoMsg(t *testing.T) {
	msg := &pb.Connect{NodeId: "n1", NodeToken: "tok", ConnectType: "control"}
	var buf bytes.Buffer
	if err := WriteProtoMsg(&buf, msg, 4096); err != nil {
		t.Fatalf("write: %v", err)
	}

	var got pb.Connect
	if err := ReadProtoMsg(&buf, &got, 4096); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.NodeId != "n1" {
		t.Errorf("NodeId = %q", got.NodeId)
	}
}
