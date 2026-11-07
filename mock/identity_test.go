package mock

import (
	"context"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/types"
)

// TestOpenSessionCarriesIdentityToTai drives a real session open through the
// SDK node layer and verifies the mock Tai side receives Payload.identity with
// a credential that validates for the requested scopes.
func TestOpenSessionCarriesIdentityToTai(t *testing.T) {
	ts := NewTestServer(t)

	nodeID := "identity-e2e"
	mn := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken(nodeID))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := mn.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mn.Stop)

	lc, err := mn.ConnectLightH2(ctx)
	if err != nil {
		t.Fatalf("connect light: %v", err)
	}
	t.Cleanup(lc.Close)
	waitForLightConn(t, ts, nodeID)

	opened := make(chan *MockSession, 1)
	sh := NewSessionHandler(func(sess *MockSession) { opened <- sess })
	go sh.AcceptAndHandle(ctx, lc)

	rn := ts.Registry.RemoteNodeByID(nodeID)
	if rn == nil {
		t.Fatal("node not in registry")
	}
	sess, err := rn.OpenSession(ctx, &types.SessionOpenRequest{
		SessionID: "sess-identity-e2e",
		Scopes:    types.Scope{types.SessionAccess, types.WorkspaceRead},
		Request:   &types.SessionRequest{Runtime: types.RuntimeTai},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer closeCancel()
		_ = sess.Close(closeCtx)
	})

	var taiSess *MockSession
	select {
	case taiSess = <-opened:
	case <-ctx.Done():
		t.Fatal("mock session was not opened")
	}

	if taiSess.Identity == nil {
		t.Fatal("Tai side received no identity on the session open")
	}
	identity := taiSess.Identity
	if identity.UserId == "" || identity.TeamId == "" || identity.UserToken == "" {
		t.Errorf("incomplete identity = %+v", identity)
	}

	// Tai treats the token as opaque; the SDK consumer (Yao) can validate it.
	user, err := ts.Auth.ValidateUserToken(identity.UserToken)
	if err != nil {
		t.Fatalf("ValidateUserToken: %v", err)
	}
	if user.UserID != identity.UserId || user.TeamID != identity.TeamId {
		t.Errorf("validated identity = %+v, wire = %+v", user, identity)
	}
	if !user.Scope.Has(types.SessionAccess) {
		t.Errorf("scope = %v, missing session:access", user.Scope)
	}
	if !user.Scope.Has(types.WorkspaceRead) {
		t.Errorf("scope = %v, missing workspace:read", user.Scope)
	}

	// The session has no workspace write scope.
	if user.Scope.Has(types.WorkspaceWrite) {
		t.Error("scope should not include workspace:write")
	}
}

// TestDefaultSessionScopesOnOpen pins the default scope set when the caller
// declares none.
func TestDefaultSessionScopesOnOpen(t *testing.T) {
	ts := NewTestServer(t)

	nodeID := "identity-default"
	mn := NewMockNode(nodeID, ts.HTTPURL(), ts.TransportAddr, ts.H3Addr)
	mn.SetNodeToken(ts.IssueTestNodeToken(nodeID))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := mn.Start(ctx); err != nil {
		t.Fatalf("mock start: %v", err)
	}
	t.Cleanup(mn.Stop)

	lc, err := mn.ConnectLightH2(ctx)
	if err != nil {
		t.Fatalf("connect light: %v", err)
	}
	t.Cleanup(lc.Close)
	waitForLightConn(t, ts, nodeID)

	opened := make(chan *MockSession, 1)
	sh := NewSessionHandler(func(sess *MockSession) { opened <- sess })
	go sh.AcceptAndHandle(ctx, lc)

	sess, err := ts.Registry.RemoteNodeByID(nodeID).OpenSession(ctx, &types.SessionOpenRequest{
		SessionID: "sess-identity-default",
		Request:   &types.SessionRequest{Runtime: types.RuntimeTai},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer closeCancel()
		_ = sess.Close(closeCtx)
	})

	var taiSess *MockSession
	select {
	case taiSess = <-opened:
	case <-ctx.Done():
		t.Fatal("mock session was not opened")
	}
	if taiSess.Identity == nil {
		t.Fatal("no identity on the default-scope session")
	}
	user, err := ts.Auth.ValidateUserToken(taiSess.Identity.UserToken)
	if err != nil {
		t.Fatalf("ValidateUserToken: %v", err)
	}
	if len(user.Scope) != 1 || !user.Scope.Has(types.SessionAccess) {
		t.Errorf("default scope = %v, want [session:access]", user.Scope)
	}
}

func waitForLightConn(t *testing.T, ts *TestServer, nodeID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for ts.LightConnCount(nodeID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("light connection not cached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
