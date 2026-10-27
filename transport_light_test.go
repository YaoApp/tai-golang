package tai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

type lightArrival struct {
	nodeID string
	sess   *webtransport.Session
	st     *connState
}

// recordLight registers a Light handler on ts that reports each session.
func recordLight(ts *TestServer) chan lightArrival {
	ch := make(chan lightArrival, 4)
	ts.Transport.setLightHandler(func(nodeID string, sess *webtransport.Session, st *connState) {
		ch <- lightArrival{nodeID, sess, st}
	})
	return ch
}

// dialLight opens a WebTransport session to the test server's Light endpoint.
func dialLight(ts *TestServer, header http.Header) (*http.Response, *webtransport.Session, error) {
	pool := x509.NewCertPool()
	cert, _ := x509.ParseCertificate(ts.TLSConfig.Certificates[0].Certificate[0])
	pool.AddCert(cert)

	d := &webtransport.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, NextProtos: []string{http3.NextProtoH3}},
		QUICConfig: &quic.Config{
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.Dial(ctx, "https://"+ts.H3Addr+"/v1/tai/light", header)
}

func lightHeader(token, nodeID string) http.Header {
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	if nodeID != "" {
		h.Set("X-Tai-Node-Id", nodeID)
	}
	return h
}

func TestLightSessionAccepted(t *testing.T) {
	ts := NewTestServer(t)
	arrivals := recordLight(ts)

	rsp, sess, err := dialLight(ts, lightHeader(ts.IssueTestNodeToken("light-1"), "light-1"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer sess.CloseWithError(0, "")
	if rsp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", rsp.StatusCode)
	}

	var a lightArrival
	select {
	case a = <-arrivals:
	case <-time.After(3 * time.Second):
		t.Fatal("light handler not called")
	}
	if a.nodeID != "light-1" {
		t.Errorf("nodeID=%q", a.nodeID)
	}
	if id, ok := a.st.check(connTypeLight); !ok || id != "light-1" {
		t.Errorf("connection not authorized as light: %q %v", id, ok)
	}

	// Yao -> Tai stream.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		s, err := a.sess.OpenStreamSync(ctx)
		if err != nil {
			return
		}
		s.Write([]byte("hi"))
		s.Close()
	}()
	s, err := sess.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	b, _ := io.ReadAll(s)
	if string(b) != "hi" {
		t.Errorf("stream data=%q", b)
	}

	// Tai -> Yao stream.
	go func() {
		s, err := sess.OpenStreamSync(ctx)
		if err != nil {
			return
		}
		s.Write([]byte("yo"))
		s.Close()
	}()
	s2, err := a.sess.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("server AcceptStream: %v", err)
	}
	b, _ = io.ReadAll(s2)
	if string(b) != "yo" {
		t.Errorf("stream data=%q", b)
	}

	// Datagrams are negotiated.
	if err := sess.SendDatagram([]byte("dg")); err != nil {
		t.Fatalf("SendDatagram: %v", err)
	}
	dg, err := a.sess.ReceiveDatagram(ctx)
	if err != nil || string(dg) != "dg" {
		t.Errorf("datagram=%q err=%v", dg, err)
	}
}

func TestLightSessionRejected(t *testing.T) {
	ts := NewTestServer(t)
	recordLight(ts)
	valid := ts.IssueTestNodeToken("light-r")

	cases := []struct {
		name   string
		header http.Header
	}{
		{"no authorization", lightHeader("", "light-r")},
		{"invalid token", lightHeader("bad-token", "light-r")},
		{"node id mismatch", lightHeader(valid, "someone-else")},
		{"missing node id", lightHeader(valid, "")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rsp, sess, err := dialLight(ts, c.header)
			if err == nil {
				sess.CloseWithError(0, "")
				t.Fatal("expected dial to fail")
			}
			if rsp != nil && rsp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status=%d, want 401", rsp.StatusCode)
			}
		})
	}
}

func TestLightSessionWithoutHandlerIsClosed(t *testing.T) {
	ts := NewTestServer(t)

	_, sess, err := dialLight(ts, lightHeader(ts.IssueTestNodeToken("light-nh"), "light-nh"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	select {
	case <-sess.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("session not closed without a handler")
	}
}

func TestLightNotServedOverH2(t *testing.T) {
	ts := NewTestServer(t)
	client := h2TLSClient(ts.TLSConfig)

	req, _ := http.NewRequest("POST", "https://"+ts.TransportAddr+"/v1/tai/light", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 404", resp.StatusCode)
	}
}

func TestLightConnectionIsNotControl(t *testing.T) {
	ts := NewTestServer(t)
	arrivals := recordLight(ts)

	_, sess, err := dialLight(ts, lightHeader(ts.IssueTestNodeToken("light-c"), "light-c"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer sess.CloseWithError(0, "")
	a := <-arrivals
	if _, ok := a.st.check(connTypeControl); ok {
		t.Error("light connection must not pass the control check")
	}
}

func TestConnStateAuthorizeAs(t *testing.T) {
	s := newConnState(nil, 0)
	if !s.authorizeAs("n1", connTypeControl) {
		t.Fatal("first authorize failed")
	}
	if !s.authorizeAs("n1", connTypeControl) {
		t.Error("same authorization must be confirmed")
	}
	if s.authorizeAs("n2", connTypeControl) {
		t.Error("different node must be refused")
	}
	if s.authorizeAs("n1", connTypeLight) {
		t.Error("different type must be refused")
	}
}

func TestConnectRejectsDifferentAuthorization(t *testing.T) {
	ts := NewTestServer(t)
	for _, tc := range protocolCases(ts) {
		t.Run(tc.name, func(t *testing.T) {
			client := authorizedClient(t, ts, tc, "first-"+tc.name)
			status, err := postConnect(client, tc.url, ts.IssueTestNodeToken("second"), "second", "control")
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			if status != http.StatusUnauthorized {
				t.Errorf("status=%d, want 401", status)
			}
		})
	}
}
