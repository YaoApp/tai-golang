package tai

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"golang.org/x/net/http2"
)

func TestToolEndpointOnTransportPort(t *testing.T) {
	a := testAuth(t)
	s, err := NewServer(&ServerConfig{Auth: a, TransportPort: freePort(t), HTTPPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	e := &pb.PluginEntry{Name: "demo.normalize", Version: "1.0.0", Runtime: "wasm", Artifact: []byte("wasm")}
	if err := s.RegisterPlugin(e); err != nil {
		t.Fatal(err)
	}
	token, err := a.IssueNodeToken("n1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Start(ctx) }()
	rc, rcCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer rcCancel()
	if err := s.WaitReady(rc); err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}}
	digest := e.Digest
	req, _ := http.NewRequest(http.MethodGet, "http://"+s.Addr()+"/v1/tai/tool/plugins/"+hex.EncodeToString([]byte("x")), nil)
	_ = req
	req, _ = http.NewRequest(http.MethodGet, "http://"+s.Addr()+"/v1/tai/tool/plugins/"+digest[len("sha256:"):], nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tai-Node-Id", "n1")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "wasm" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, b)
	}
	// A node token presented for a different node id must be rejected.
	badReq, _ := http.NewRequest(http.MethodGet, "http://"+s.Addr()+"/v1/tai/tool/plugins/"+digest[len("sha256:"):], nil)
	badReq.Header.Set("Authorization", "Bearer "+token)
	badReq.Header.Set("X-Tai-Node-Id", "other")
	badResp, err := hc.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("mismatched node id status=%d", badResp.StatusCode)
	}
}
