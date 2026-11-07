package types

import (
	"testing"

	"github.com/yaoapp/tai-golang/pb"
)

func TestScopeHas(t *testing.T) {
	cases := []struct {
		name   string
		scope  Scope
		action string
		want   bool
	}{
		{"empty", Scope{}, SessionAccess, false},
		{"exact", Scope{SessionAccess, WorkspaceRead}, WorkspaceRead, true},
		{"absent", Scope{SessionAccess}, WorkspaceWrite, false},
		{"wildcard matches", Scope{WorkspaceAll}, WorkspaceDelete, true},
		{"wildcard matches read", Scope{WorkspaceAll}, WorkspaceRead, true},
		{"wildcard is resource scoped", Scope{WorkspaceAll}, SessionAccess, false},
		{"wildcard does not cross resources", Scope{WorkspaceAll}, RemoteToolCall, false},
		{"no suffix match", Scope{"workspace"}, WorkspaceRead, false},
		{"nil scope", nil, SessionAccess, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scope.Has(tc.action); got != tc.want {
				t.Errorf("Has(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}

func TestIdentityToPB(t *testing.T) {
	if got := IdentityToPB(nil, "", 0); got != nil {
		t.Errorf("IdentityToPB(nil, empty) = %v, want nil", got)
	}

	wire := IdentityToPB(&UserIdentity{UserID: "u1", TeamID: "t1"}, "tok", 1762400000)
	if wire == nil {
		t.Fatal("IdentityToPB returned nil")
	}
	if wire.UserId != "u1" || wire.TeamId != "t1" || wire.UserToken != "tok" {
		t.Errorf("wire = %+v, want u1/t1/tok", wire)
	}
	if wire.ExpiresAt != 1762400000 {
		t.Errorf("ExpiresAt = %d, want 1762400000", wire.ExpiresAt)
	}

	// Token without identity still produces a wire identity (opaque caller).
	tokenOnly := IdentityToPB(nil, "tok2", 0)
	if tokenOnly == nil || tokenOnly.UserToken != "tok2" {
		t.Errorf("token-only identity = %+v, want token tok2", tokenOnly)
	}
}

func TestIdentityFromPB(t *testing.T) {
	if got := IdentityFromPB(nil); got != nil {
		t.Errorf("IdentityFromPB(nil) = %v, want nil", got)
	}
	got := IdentityFromPB(&pb.Identity{UserId: "u2", TeamId: "t2", UserToken: "tok"})
	if got == nil || got.UserID != "u2" || got.TeamID != "t2" {
		t.Errorf("IdentityFromPB = %+v, want u2/t2", got)
	}
}

func TestDefaultSessionScopes(t *testing.T) {
	scopes := DefaultSessionScopes()
	if len(scopes) != 1 || scopes[0] != SessionAccess {
		t.Errorf("DefaultSessionScopes() = %v, want [session:access]", scopes)
	}
	// Must be a fresh slice: mutating it must not affect the next call.
	scopes[0] = WorkspaceAll
	if next := DefaultSessionScopes(); next[0] != SessionAccess {
		t.Errorf("DefaultSessionScopes() is not a fresh slice")
	}
}
