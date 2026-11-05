package transport

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
)

// ---------------------------------------------------------------------------
// LightStream Read / Write / Close / HasResponse
// ---------------------------------------------------------------------------

func TestLightStream_ReadWrite(t *testing.T) {
	pr, pw := io.Pipe()
	rr, rw := io.Pipe()
	ls := &LightStream{r: pr, w: rw, conn: &lightConn{}}

	go func() {
		pw.Write([]byte("hello"))
		pw.Close()
	}()

	buf := make([]byte, 5)
	n, err := ls.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("Read = %q, want hello", buf[:n])
	}

	go func() {
		ls.Write([]byte("world"))
	}()
	out := make([]byte, 5)
	n, err = rr.Read(out)
	if err != nil {
		t.Fatalf("remote Read: %v", err)
	}
	if string(out[:n]) != "world" {
		t.Errorf("Write = %q, want world", out[:n])
	}
}

func TestLightStream_Close_Idempotent(t *testing.T) {
	pr, pw := io.Pipe()
	ls := &LightStream{r: pr, w: pw, conn: &lightConn{}}
	ls.conn.active.Add(1)

	// Close the remote read end so drain doesn't block.
	pr.Close()

	ls.Close()
	ls.Close() // second close must not panic

	if ls.conn.active.Load() != 0 {
		t.Errorf("active = %d, want 0", ls.conn.active.Load())
	}
}

func TestLightStream_HasResponse(t *testing.T) {
	pr, pw := io.Pipe()
	ls := &LightStream{r: pr, w: pw, conn: &lightConn{}}

	if ls.HasResponse() {
		t.Error("HasResponse should be false initially")
	}

	go func() {
		pw.Write([]byte("x"))
	}()

	buf := make([]byte, 1)
	ls.Read(buf)

	if !ls.HasResponse() {
		t.Error("HasResponse should be true after Read")
	}
}

// ---------------------------------------------------------------------------
// LightStream DatagramConn / SendDatagram / ReceiveDatagram / Proto
// ---------------------------------------------------------------------------

func TestLightStream_DatagramConn(t *testing.T) {
	ls := &LightStream{conn: &lightConn{proto: "H2"}}
	dc := ls.DatagramConn()
	if dc == nil {
		t.Fatal("DatagramConn should not be nil")
	}
}

func TestLightConn_SendDatagram_H2(t *testing.T) {
	lc := &lightConn{proto: "H2"}
	err := lc.SendDatagram([]byte("data"))
	if !errors.Is(err, ErrDatagramNotSupported) {
		t.Errorf("err = %v, want ErrDatagramNotSupported", err)
	}
}

func TestLightConn_ReceiveDatagram_H2(t *testing.T) {
	lc := &lightConn{proto: "H2"}
	_, err := lc.ReceiveDatagram(context.Background())
	if !errors.Is(err, ErrDatagramNotSupported) {
		t.Errorf("err = %v, want ErrDatagramNotSupported", err)
	}
}

func TestLightConn_SendDatagram_H3_NoSession(t *testing.T) {
	lc := &lightConn{proto: "H3", session: nil}
	err := lc.SendDatagram([]byte("data"))
	if !errors.Is(err, ErrDatagramNotSupported) {
		t.Errorf("err = %v, want ErrDatagramNotSupported", err)
	}
}

func TestLightConn_Proto(t *testing.T) {
	for _, proto := range []string{"H3", "H2"} {
		lc := &lightConn{proto: proto}
		if lc.Proto() != proto {
			t.Errorf("Proto() = %q, want %q", lc.Proto(), proto)
		}
	}
}

// ---------------------------------------------------------------------------
// lightPool management: add/remove/connCount/close/maxStreams/updateConfig
// ---------------------------------------------------------------------------

func TestLightPool_AddRemoveConn(t *testing.T) {
	p := newLightPool(&pb.PoolConfig{}, DefaultCodec(), &testNodeRegistry{pool: &pb.PoolConfig{}}, &testAuth{})
	defer p.close()

	lc := &lightConn{nodeID: "n1", pool: p}
	if !p.addConn(lc) {
		t.Fatal("addConn returned false")
	}
	if p.connCount("n1") != 1 {
		t.Errorf("connCount = %d, want 1", p.connCount("n1"))
	}

	p.removeConn(lc)
	if p.connCount("n1") != 0 {
		t.Errorf("connCount after remove = %d, want 0", p.connCount("n1"))
	}
}

func TestLightPool_Close_RejectsAdd(t *testing.T) {
	p := newLightPool(&pb.PoolConfig{}, DefaultCodec(), &testNodeRegistry{pool: &pb.PoolConfig{}}, &testAuth{})
	p.close()

	lc := &lightConn{nodeID: "n1", pool: p}
	if p.addConn(lc) {
		t.Error("addConn should return false after close")
	}
}

func TestLightPool_DoubleClose(t *testing.T) {
	p := newLightPool(&pb.PoolConfig{}, DefaultCodec(), &testNodeRegistry{pool: &pb.PoolConfig{}}, &testAuth{})
	p.close()
	p.close() // should not panic
}

func TestLightPool_MaxStreams_Default(t *testing.T) {
	p := newLightPool(nil, DefaultCodec(), &testNodeRegistry{pool: &pb.PoolConfig{}}, &testAuth{})
	defer p.close()

	if p.maxStreams() != 256 {
		t.Errorf("maxStreams = %d, want 256", p.maxStreams())
	}
}

func TestLightPool_MaxStreams_Custom(t *testing.T) {
	p := newLightPool(&pb.PoolConfig{LightMaxStreams: 64}, DefaultCodec(), &testNodeRegistry{pool: &pb.PoolConfig{}}, &testAuth{})
	defer p.close()

	if p.maxStreams() != 64 {
		t.Errorf("maxStreams = %d, want 64", p.maxStreams())
	}
}

func TestLightPool_UpdateConfig(t *testing.T) {
	p := newLightPool(&pb.PoolConfig{LightMaxStreams: 100}, DefaultCodec(), &testNodeRegistry{pool: &pb.PoolConfig{}}, &testAuth{})
	defer p.close()

	p.updateConfig(&pb.PoolConfig{LightMaxStreams: 200})
	if p.maxStreams() != 200 {
		t.Errorf("maxStreams after update = %d, want 200", p.maxStreams())
	}
}

func TestLightPool_AddMultipleAndRemove(t *testing.T) {
	p := newLightPool(&pb.PoolConfig{}, DefaultCodec(), &testNodeRegistry{pool: &pb.PoolConfig{}}, &testAuth{})
	defer p.close()

	lc1 := &lightConn{nodeID: "n1", pool: p}
	lc2 := &lightConn{nodeID: "n1", pool: p}
	lc3 := &lightConn{nodeID: "n2", pool: p}

	p.addConn(lc1)
	p.addConn(lc2)
	p.addConn(lc3)

	if p.connCount("n1") != 2 {
		t.Errorf("n1 connCount = %d, want 2", p.connCount("n1"))
	}
	if p.connCount("n2") != 1 {
		t.Errorf("n2 connCount = %d, want 1", p.connCount("n2"))
	}

	p.removeConn(lc1)
	if p.connCount("n1") != 1 {
		t.Errorf("n1 connCount after remove = %d, want 1", p.connCount("n1"))
	}

	p.removeConn(lc2)
	if p.connCount("n1") != 0 {
		t.Errorf("n1 connCount after remove all = %d, want 0", p.connCount("n1"))
	}
}

func TestNewLightStreamForTest(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	ls := NewLightStreamForTest(pr, pw)
	if ls == nil {
		t.Fatal("NewLightStreamForTest returned nil")
	}
}

func TestNewLightStreamForTestWithProto(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	ls := NewLightStreamForTestWithProto(pr, pw, "H3")
	if ls == nil {
		t.Fatal("returned nil")
	}
	if ls.conn.proto != "H3" {
		t.Errorf("proto = %q, want H3", ls.conn.proto)
	}
}
