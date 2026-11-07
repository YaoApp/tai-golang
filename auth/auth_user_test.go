package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/yaoapp/tai-golang/types"
)

func TestIssueUserToken_DefaultIdentity(t *testing.T) {
	auth, _ := newTestAuth(t)

	token, identity, err := auth.IssueUserToken("node-1", types.Scope{types.SessionAccess, types.WorkspaceRead})
	if err != nil {
		t.Fatalf("IssueUserToken: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}
	if identity == nil || identity.UserID == "" || identity.TeamID == "" {
		t.Fatalf("identity = %+v, want non-empty user/team", identity)
	}
	if len(identity.Scope) != 2 || identity.Scope[0] != types.SessionAccess {
		t.Errorf("identity.Scope = %v, want requested scopes", identity.Scope)
	}

	got, err := auth.ValidateUserToken(token)
	if err != nil {
		t.Fatalf("ValidateUserToken: %v", err)
	}
	if got.UserID != identity.UserID || got.TeamID != identity.TeamID {
		t.Errorf("validated identity = %+v, want %+v", got, identity)
	}
	if !got.Scope.Has(types.WorkspaceRead) {
		t.Errorf("validated scope = %v, want workspace:read", got.Scope)
	}
}

func TestIssueUserToken_CustomResolver(t *testing.T) {
	auth, _ := newTestAuth(t)
	var gotNode string
	var gotScopes types.Scope
	auth.SetUserResolver(func(nodeID string, scopes types.Scope) (*types.UserIdentity, error) {
		gotNode, gotScopes = nodeID, scopes
		return &types.UserIdentity{UserID: "u1", TeamID: "t1", Metadata: map[string]any{"k": "v"}}, nil
	})

	token, identity, err := auth.IssueUserToken("node-9", types.Scope{types.RemoteToolCall})
	if err != nil {
		t.Fatalf("IssueUserToken: %v", err)
	}
	if gotNode != "node-9" {
		t.Errorf("resolver nodeID = %q, want node-9", gotNode)
	}
	if len(gotScopes) != 1 || gotScopes[0] != types.RemoteToolCall {
		t.Errorf("resolver scopes = %v, want [remote_tool:call]", gotScopes)
	}
	if identity.UserID != "u1" || identity.TeamID != "t1" {
		t.Errorf("identity = %+v, want u1/t1", identity)
	}
	if identity.Scope[0] != types.RemoteToolCall {
		t.Errorf("identity.Scope = %v, want mirrored scopes", identity.Scope)
	}

	validated, err := auth.ValidateUserToken(token)
	if err != nil {
		t.Fatalf("ValidateUserToken: %v", err)
	}
	if validated.UserID != "u1" || validated.Metadata["k"] != "v" {
		t.Errorf("validated = %+v, want u1 with metadata", validated)
	}
}

func TestIssueUserToken_ResolverError(t *testing.T) {
	auth, _ := newTestAuth(t)
	auth.SetUserResolver(func(string, types.Scope) (*types.UserIdentity, error) {
		return nil, errors.New("no user in context")
	})
	if _, _, err := auth.IssueUserToken("node", types.Scope{types.SessionAccess}); err == nil {
		t.Fatal("expected resolver error")
	}
}

func TestIssueUserToken_EmptyIdentityRejected(t *testing.T) {
	auth, _ := newTestAuth(t)
	auth.SetUserResolver(func(string, types.Scope) (*types.UserIdentity, error) {
		return &types.UserIdentity{}, nil
	})
	if _, _, err := auth.IssueUserToken("node", types.Scope{types.SessionAccess}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestValidateUserToken_Invalid(t *testing.T) {
	auth, _ := newTestAuth(t)
	if _, err := auth.ValidateUserToken("not-a-token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestValidateUserToken_Expired(t *testing.T) {
	auth, _ := newTestAuth(t)
	expired := signUserTokenForTest(t, auth, &userTokenClaims{
		UserID:   "u1",
		TeamID:   "t1",
		Metadata: nil,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			ID:        "ut_expired",
		},
	})
	if _, err := auth.ValidateUserToken(expired); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

func TestValidateUserToken_MissingClaims(t *testing.T) {
	auth, _ := newTestAuth(t)
	token := signUserTokenForTest(t, auth, &userTokenClaims{
		UserID: "",
		TeamID: "t1",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			ID:        "ut_missing",
		},
	})
	if _, err := auth.ValidateUserToken(token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestRefreshUserToken(t *testing.T) {
	auth, _ := newTestAuth(t)
	auth.SetUserResolver(func(string, types.Scope) (*types.UserIdentity, error) {
		return &types.UserIdentity{UserID: "u1", TeamID: "t1"}, nil
	})
	old, _, err := auth.IssueUserToken("node", types.Scope{types.SessionAccess, types.WorkspaceWrite})
	if err != nil {
		t.Fatalf("IssueUserToken: %v", err)
	}

	fresh, err := auth.RefreshUserToken(old)
	if err != nil {
		t.Fatalf("RefreshUserToken: %v", err)
	}
	if fresh == old {
		t.Error("refreshed token must differ from the old one")
	}

	identity, err := auth.ValidateUserToken(fresh)
	if err != nil {
		t.Fatalf("ValidateUserToken(refreshed): %v", err)
	}
	if identity.UserID != "u1" || identity.TeamID != "t1" {
		t.Errorf("identity = %+v, want inherited u1/t1", identity)
	}
	if !identity.Scope.Has(types.WorkspaceWrite) || len(identity.Scope) != 2 {
		t.Errorf("scope = %v, want inherited {session:access, workspace:write}", identity.Scope)
	}
}

func TestRefreshUserToken_InvalidOldToken(t *testing.T) {
	auth, _ := newTestAuth(t)
	if _, err := auth.RefreshUserToken("garbage"); err == nil {
		t.Fatal("expected error for invalid old token")
	}
}

func TestRefreshUserToken_ExpiredOldToken(t *testing.T) {
	auth, _ := newTestAuth(t)
	expired := signUserTokenForTest(t, auth, &userTokenClaims{
		UserID: "u1",
		TeamID: "t1",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			ID:        "ut_old",
		},
	})
	if _, err := auth.RefreshUserToken(expired); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

// signUserTokenForTest signs arbitrary claims with the auth key.
func signUserTokenForTest(t *testing.T, auth *defaultAuth, claims *userTokenClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(auth.privateKey)
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return signed
}

func TestSignUserToken_InvalidIdentity(t *testing.T) {
	auth, _ := newTestAuth(t)
	now := time.Now()
	if _, err := auth.signUserToken(nil, types.Scope{types.SessionAccess}, now); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("nil identity err = %v, want ErrUnauthorized", err)
	}
	if _, err := auth.signUserToken(&types.UserIdentity{UserID: "", TeamID: "t"}, types.Scope{types.SessionAccess}, now); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("empty user err = %v, want ErrUnauthorized", err)
	}
}

func TestValidateUserToken_WrongSigningMethod(t *testing.T) {
	auth, _ := newTestAuth(t)
	claims := &userTokenClaims{UserID: "u", TeamID: "t"}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("hmac-key"))
	if err != nil {
		t.Fatalf("sign hmac token: %v", err)
	}
	if _, err := auth.ValidateUserToken(signed); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestValidateNodeToken_WrongSigningMethod(t *testing.T) {
	auth, _ := newTestAuth(t)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &nodeTokenClaims{NodeID: "n1"})
	signed, err := token.SignedString([]byte("hmac-key"))
	if err != nil {
		t.Fatalf("sign hmac token: %v", err)
	}
	if _, err := auth.ValidateNodeToken(signed); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestUserTokenExpiresAt(t *testing.T) {
	auth, _ := newTestAuth(t)
	token, _, err := auth.IssueUserToken("node", types.Scope{types.SessionAccess})
	if err != nil {
		t.Fatalf("IssueUserToken: %v", err)
	}
	expiry, err := auth.UserTokenExpiresAt(token)
	if err != nil {
		t.Fatalf("UserTokenExpiresAt: %v", err)
	}
	ttl := time.Until(expiry)
	if ttl <= 0 || ttl > 31*time.Minute {
		t.Errorf("expiry TTL = %v, want ~30m in the future", ttl)
	}

	if _, err := auth.UserTokenExpiresAt("garbage"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("invalid token err = %v, want ErrUnauthorized", err)
	}
}

func TestUserTokenExpiresAt_NoExpiry(t *testing.T) {
	auth, _ := newTestAuth(t)
	token := signUserTokenForTest(t, auth, &userTokenClaims{
		UserID: "u1",
		TeamID: "t1",
		RegisteredClaims: jwt.RegisteredClaims{
			ID: "ut_noexp",
		},
	})
	expiry, err := auth.UserTokenExpiresAt(token)
	if err != nil {
		t.Fatalf("UserTokenExpiresAt: %v", err)
	}
	if !expiry.IsZero() {
		t.Errorf("expiry = %v, want zero", expiry)
	}
}

type ctxUserKey struct{}

func TestIssueUserTokenContext(t *testing.T) {
	auth, _ := newTestAuth(t)
	auth.SetUserResolverContext(func(ctx context.Context, _ string, scopes types.Scope) (*types.UserIdentity, error) {
		email, _ := ctx.Value(ctxUserKey{}).(string)
		return &types.UserIdentity{UserID: email, TeamID: "ctx-team"}, nil
	})

	ctx := context.WithValue(context.Background(), ctxUserKey{}, "alice@example.com")
	token, identity, err := auth.IssueUserTokenContext(ctx, "node", types.Scope{types.SessionAccess})
	if err != nil {
		t.Fatalf("IssueUserTokenContext: %v", err)
	}
	if identity.UserID != "alice@example.com" || identity.TeamID != "ctx-team" {
		t.Fatalf("identity = %+v", identity)
	}
	validated, err := auth.ValidateUserToken(token)
	if err != nil {
		t.Fatalf("ValidateUserToken: %v", err)
	}
	if validated.UserID != "alice@example.com" {
		t.Errorf("validated user = %q", validated.UserID)
	}
}

func TestIssueUserTokenContext_Fallback(t *testing.T) {
	auth, _ := newTestAuth(t)
	// No context resolver installed: falls back to the shared fallback identity.
	_, identity, err := auth.IssueUserTokenContext(context.Background(), "node", nil)
	if err != nil {
		t.Fatalf("IssueUserTokenContext: %v", err)
	}
	if identity.UserID == "" || identity.TeamID == "" {
		t.Errorf("identity = %+v", identity)
	}
}

func TestIssueUserTokenContext_ResolverError(t *testing.T) {
	auth, _ := newTestAuth(t)
	auth.SetUserResolverContext(func(context.Context, string, types.Scope) (*types.UserIdentity, error) {
		return nil, ErrUnauthorized
	})
	if _, _, err := auth.IssueUserTokenContext(context.Background(), "node", nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestIssueUserTokenContext_NilContext(t *testing.T) {
	auth, _ := newTestAuth(t)
	auth.SetUserResolverContext(func(context.Context, string, types.Scope) (*types.UserIdentity, error) {
		return &types.UserIdentity{UserID: "u-ctx", TeamID: "t-ctx"}, nil
	})
	//nolint:staticcheck // nil context is deliberate: the SDK normalizes it.
	if _, identity, err := auth.IssueUserTokenContext(nil, "node", nil); err != nil || identity.UserID != "u-ctx" {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
}

func TestIssueUserTokenContext_LegacyResolverFallback(t *testing.T) {
	auth, _ := newTestAuth(t)
	auth.SetUserResolver(func(string, types.Scope) (*types.UserIdentity, error) {
		return &types.UserIdentity{UserID: "legacy", TeamID: "legacy-team"}, nil
	})
	_, identity, err := auth.IssueUserTokenContext(context.Background(), "node", nil)
	if err != nil {
		t.Fatalf("IssueUserTokenContext: %v", err)
	}
	if identity.UserID != "legacy" {
		t.Errorf("identity = %+v, want legacy resolver result", identity)
	}
}
