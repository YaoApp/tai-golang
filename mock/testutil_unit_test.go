package mock

import (
	"strings"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

func TestTestServerHelpers(t *testing.T) {
	ts := NewTestServerWithTimeout(t, 50*time.Millisecond)

	if ts.HTTPURL() == "" {
		t.Error("HTTPURL is empty")
	}
	if !strings.HasPrefix(ts.H3URL(), "https://") {
		t.Errorf("H3URL = %q", ts.H3URL())
	}
	if ts.HTTPAddr == "" || ts.TransportAddr == "" || ts.H3Addr == "" {
		t.Errorf("addresses = (%q,%q,%q)", ts.HTTPAddr, ts.TransportAddr, ts.H3Addr)
	}
	if ts.Registry == nil || ts.Transport == nil || ts.HTTP == nil {
		t.Error("test server dependencies are nil")
	}

	// GoAwayAll with no connected nodes is a no-op.
	ts.GoAwayAll("shutdown")

	// SendControl to an unknown node returns a not-found error.
	if err := ts.SendControl("missing", &pb.Payload{Namespace: types.NamespaceControl, Kind: "x"}); err == nil {
		t.Error("SendControl to a missing node should fail")
	}

	// Pool counts are zero without connections.
	if got := ts.LightConnCount("nope"); got != 0 {
		t.Errorf("LightConnCount = %d, want 0", got)
	}
	if got := ts.HeavyConnCount("nope"); got != 0 {
		t.Errorf("HeavyConnCount = %d, want 0", got)
	}
}

func TestNewTestServerDefaultTimeout(t *testing.T) {
	ts := NewTestServer(t)
	if ts == nil {
		t.Fatal("NewTestServer returned nil")
	}
}
