package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"
	"github.com/yaoapp/tai-golang/auth"
	"github.com/yaoapp/tai-golang/pb"
)

func TestNewServerAndAddr(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	mgr := NewNodeManager(&pb.PoolConfig{LightMinConns: 2, LightMaxConns: 8, LightMaxStreams: 256})
	codec := DefaultCodec()

	ts := NewServer(a, mgr, codec)
	if ts.TCPAddr() != "" {
		t.Errorf("tcpAddr should be empty before start, got %q", ts.TCPAddr())
	}

	if err := ts.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ts.Close(context.Background())

	if ts.TCPAddr() == "" {
		t.Error("tcpAddr should not be empty after start")
	}
}

func TestCloseNilServers(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	mgr := NewNodeManager(&pb.PoolConfig{})
	ts := NewServer(a, mgr, DefaultCodec())
	if err := ts.Close(context.Background()); err != nil {
		t.Errorf("close nil servers: %v", err)
	}
}

func TestStartH3RequiresTLS(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	ts := NewServer(a, NewNodeManager(&pb.PoolConfig{}), DefaultCodec())
	if err := ts.StartH3("127.0.0.1:0", nil); err == nil {
		t.Error("startH3 without TLS must fail")
	}
}

func TestH3AddrBeforeStart(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	ts := NewServer(a, NewNodeManager(&pb.PoolConfig{}), DefaultCodec())
	if ts.H3Addr() != "" {
		t.Errorf("H3Addr should be empty before start, got %q", ts.H3Addr())
	}
}

func TestServerGetters(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	mgr := NewNodeManager(&pb.PoolConfig{})
	c := DefaultCodec()
	ts := NewServer(a, mgr, c)

	if ts.NodeManager() != mgr {
		t.Error("NodeManager() mismatch")
	}
	if ts.Codec() != c {
		t.Error("Codec() mismatch")
	}
	if ts.Handler() == nil {
		t.Error("Handler() nil")
	}
}

func TestSetUnauthTimeout(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	ts := NewServer(a, NewNodeManager(&pb.PoolConfig{}), DefaultCodec())
	ts.SetUnauthTimeout(5 * time.Second)
}

func TestSetLightAndTaiHandler(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	ts := NewServer(a, NewNodeManager(&pb.PoolConfig{}), DefaultCodec())
	ts.SetLightHandler(func(nodeID string, sess *webtransport.Session, st *ConnState) {})
	ts.SetTaiHandler(func(conn net.Conn, st *ConnState) {})
}

func TestNodeManagerSetPoolConfig(t *testing.T) {
	mgr := NewNodeManager(&pb.PoolConfig{LightMinConns: 1})
	newCfg := &pb.PoolConfig{LightMinConns: 4, LightMaxConns: 16}
	mgr.SetPoolConfig(newCfg)
	if mgr.PoolConfig().LightMinConns != 4 {
		t.Error("SetPoolConfig did not take effect")
	}
}
