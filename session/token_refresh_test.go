package session

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

func TestTokenRefresher_TickerSendsRefresh(t *testing.T) {
	var mu sync.Mutex
	var captured *types.ControlMsg

	tr := newTokenRefresher("sess-tr-1",
		func() (string, error) { return "fresh-token", nil },
		func(msg *types.ControlMsg) error {
			mu.Lock()
			captured = msg
			mu.Unlock()
			return nil
		},
		10*time.Millisecond,
	)

	// Wait for at least one tick.
	time.Sleep(50 * time.Millisecond)
	tr.Stop()

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("expected at least one refresh message")
	}
	if captured.Kind != "token.refresh" {
		t.Errorf("kind = %q, want token.refresh", captured.Kind)
	}
	var parsed pb.TokenRefresh
	if err := proto.Unmarshal(captured.Data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.SessionId != "sess-tr-1" {
		t.Errorf("session_id = %q, want sess-tr-1", parsed.SessionId)
	}
	if parsed.Token != "fresh-token" {
		t.Errorf("token = %q, want fresh-token", parsed.Token)
	}
}

func TestTokenRefresher_IssueTokenError(t *testing.T) {
	var sent atomic.Int32
	callCount := atomic.Int32{}

	tr := newTokenRefresher("sess-tr-err",
		func() (string, error) {
			callCount.Add(1)
			return "", fmt.Errorf("key unavailable")
		},
		func(msg *types.ControlMsg) error {
			sent.Add(1)
			return nil
		},
		10*time.Millisecond,
	)

	time.Sleep(50 * time.Millisecond)
	tr.Stop()

	if callCount.Load() == 0 {
		t.Fatal("issueToken was never called")
	}
	if sent.Load() != 0 {
		t.Errorf("sent %d messages, want 0 (issueToken always errors)", sent.Load())
	}
}

func TestTokenRefresher_StopBeforeTick(t *testing.T) {
	var sent atomic.Int32
	tr := newTokenRefresher("sess-tr-2",
		func() (string, error) { return "tok", nil },
		func(msg *types.ControlMsg) error {
			sent.Add(1)
			return nil
		},
		10*time.Minute, // Long interval — ticker never fires.
	)
	tr.Stop()
	time.Sleep(30 * time.Millisecond)

	if sent.Load() != 0 {
		t.Errorf("sent %d messages after immediate stop, want 0", sent.Load())
	}
}

func TestTokenRefresher_StopIdempotent(t *testing.T) {
	tr := NewTokenRefresher("sess-tr-3",
		func() (string, error) { return "t", nil },
		func(msg *types.ControlMsg) error { return nil },
	)
	tr.Stop()
	tr.Stop() // Must not panic.
}

func TestTokenRefresher_MessageFormat(t *testing.T) {
	data, err := proto.Marshal(&pb.TokenRefresh{
		SessionId: "sess-fmt",
		Token:     "jwt-abc",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed pb.TokenRefresh
	if err := proto.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.SessionId != "sess-fmt" {
		t.Errorf("session_id = %q, want sess-fmt", parsed.SessionId)
	}
	if parsed.Token != "jwt-abc" {
		t.Errorf("token = %q, want jwt-abc", parsed.Token)
	}
}
