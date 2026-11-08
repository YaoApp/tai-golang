package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang"
	"github.com/yaoapp/tai-golang/app/plugins"
	"github.com/yaoapp/tai-golang/auth"
	"golang.org/x/net/http2"
)

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestDemoPluginArtifactOnTransportPort(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"normalize", "tag", "bad_fetch"} {
		if err := os.WriteFile(filepath.Join(dir, n+".wasm"), []byte("wasm-"+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := auth.DefaultAuth(priv)
	srv, err := tai.NewServer(&tai.ServerConfig{TransportPort: freePort(t), HTTPPort: freePort(t), Auth: a})
	if err != nil {
		t.Fatal(err)
	}
	if err := plugins.Register(srv, dir); err != nil {
		t.Fatal(err)
	}
	meta := srv.PluginRegistry().Metadata()
	if len(meta) != 3 {
		t.Fatalf("metadata=%d", len(meta))
	}
	token, err := a.IssueNodeToken("n1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	rctx, rcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer rcancel()
	if err := srv.WaitReady(rctx); err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}}
	hexDigest := meta[0].Digest[len("sha256:"):]
	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/v1/tai/tool/plugins/"+hexDigest, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tai-Node-Id", "n1")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(b) == 0 {
		t.Fatalf("status=%d body=%q", resp.StatusCode, b)
	}
}
