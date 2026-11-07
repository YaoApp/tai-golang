package node

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// recordingAuth wraps the default provider and records the user-credential
// calls made by the node transport layer.
type recordingAuth struct {
	types.AuthProvider

	mu         sync.Mutex
	issued     []types.Scope
	issueIDs   []string
	refreshed  []string
	issueErr   error
	refreshErr error
	userID     string
	teamID     string
	token      string
}

func newRecordingAuth(t *testing.T) *recordingAuth {
	t.Helper()
	return &recordingAuth{
		AuthProvider: testAuth(t),
		userID:       "user-1",
		teamID:       "team-1",
		token:        "user-token-1",
	}
}

func (a *recordingAuth) IssueUserToken(nodeID string, scopes types.Scope) (string, *types.UserIdentity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.issueErr != nil {
		return "", nil, a.issueErr
	}
	a.issued = append(a.issued, scopes)
	a.issueIDs = append(a.issueIDs, nodeID)
	return a.token, &types.UserIdentity{UserID: a.userID, TeamID: a.teamID, Scope: scopes}, nil
}

// UserTokenExpiresAt makes recordingAuth a types.UserTokenExpiry so tests can
// assert Identity.expires_at propagation.
func (a *recordingAuth) UserTokenExpiresAt(string) (time.Time, error) {
	return time.Unix(1762400000, 0), nil
}

func (a *recordingAuth) RefreshUserToken(oldToken string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refreshErr != nil {
		return "", a.refreshErr
	}
	a.refreshed = append(a.refreshed, oldToken)
	return "refreshed-token", nil
}

func (a *recordingAuth) issueCalls() []types.Scope {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]types.Scope, len(a.issued))
	copy(out, a.issued)
	return out
}

func (a *recordingAuth) refreshCalls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.refreshed))
	copy(out, a.refreshed)
	return out
}

// registryWithAuth registers a node and installs the recording auth provider.
func registryWithAuth(t *testing.T, nodeID string, a types.AuthProvider) *Registry {
	t.Helper()
	reg := testRegistry()
	reg.Register(nodeID, &pb.Register{Hardware: &pb.Hardware{Os: "linux"}}, testAuth(t))
	reg.SetAuth(a)
	return reg
}

// sessionOpenBackend returns a transport backend that answers the session open
// handshake and records every first frame the SDK sends.
type sessionOpenBackend struct {
	mu     sync.Mutex
	frames []*pb.Payload
	// response for "open"/"resume" first frames
	openKind string
	openData []byte
	failData bool
}

func (b *sessionOpenBackend) OpenLightStream(_ context.Context, _ string, firstFrame *pb.Payload) (io.ReadWriteCloser, error) {
	b.mu.Lock()
	b.frames = append(b.frames, proto.Clone(firstFrame).(*pb.Payload))
	b.mu.Unlock()

	switch firstFrame.Kind {
	case "open", "resume":
		kind := b.openKind
		if kind == "" {
			kind = "opened"
		}
		data := b.openData
		if kind == "opened" && data == nil {
			opened, _ := proto.Marshal(&pb.SessionOpened{SessionId: "s"})
			data = opened
		}
		if b.failData {
			return nil, fmt.Errorf("control stream unavailable")
		}
		return &mockNodeStream{Reader: bytes.NewReader(buildCodecFrame(types.NamespaceSession, kind, data)), buf: &bytes.Buffer{}}, nil
	default:
		return &mockNodeStream{Reader: bytes.NewReader(nil), buf: &bytes.Buffer{}}, nil
	}
}

func (b *sessionOpenBackend) HeavyPick(string) (net.Conn, error) {
	return nil, fmt.Errorf("no heavy")
}

func (b *sessionOpenBackend) HeavyReturn(net.Conn) {}
func (b *sessionOpenBackend) HeavyRemove(net.Conn) {}

func (b *sessionOpenBackend) frameByKind(kind string) *pb.Payload {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, f := range b.frames {
		if f.Kind == kind {
			return f
		}
	}
	return nil
}

// registration helper: existing tests use registerNode which installs a fresh
// default auth; identity tests need their own provider.
func TestIssueIdentityDefaultScopes(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-identity", rec)
	rn := reg.RemoteNodeByID("n-identity").(*remoteNode)

	identity, err := rn.issueIdentity(context.Background(), nil)
	if err != nil {
		t.Fatalf("issueIdentity: %v", err)
	}
	if identity == nil {
		t.Fatal("identity is nil")
	}
	if identity.UserId != "user-1" || identity.TeamId != "team-1" || identity.UserToken != "user-token-1" {
		t.Errorf("wire identity = %+v", identity)
	}
	calls := rec.issueCalls()
	if len(calls) != 1 {
		t.Fatalf("IssueUserToken calls = %d, want 1", len(calls))
	}
	if len(calls[0]) != 1 || calls[0][0] != types.SessionAccess {
		t.Errorf("default scopes = %v, want [session:access]", calls[0])
	}
	if rec.issueIDs[0] != "n-identity" {
		t.Errorf("nodeID = %q, want n-identity", rec.issueIDs[0])
	}
}

func TestIssueIdentityExplicitScopes(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-scopes", rec)
	rn := reg.RemoteNodeByID("n-scopes").(*remoteNode)

	scopes := types.Scope{types.SessionAccess, types.WorkspaceRead, types.WorkspaceWrite}
	if _, err := rn.issueIdentity(context.Background(), scopes); err != nil {
		t.Fatalf("issueIdentity: %v", err)
	}
	calls := rec.issueCalls()
	if len(calls) != 1 || fmt.Sprint(calls[0]) != fmt.Sprint(scopes) {
		t.Errorf("scopes = %v, want %v", calls, scopes)
	}
}

func TestIssueIdentityAuthNotConfigured(t *testing.T) {
	reg := testRegistry()
	reg.Register("n-noauth", &pb.Register{Hardware: &pb.Hardware{}}, testAuth(t))
	rn := reg.RemoteNodeByID("n-noauth").(*remoteNode)

	if _, err := rn.issueIdentity(context.Background(), nil); err == nil {
		t.Fatal("expected an error when no auth provider is configured")
	}
}

func TestIssueIdentityPropagatesError(t *testing.T) {
	rec := newRecordingAuth(t)
	rec.issueErr = fmt.Errorf("resolver failed")
	reg := registryWithAuth(t, "n-issue-err", rec)
	rn := reg.RemoteNodeByID("n-issue-err").(*remoteNode)

	_, err := rn.issueIdentity(context.Background(), nil)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("resolver failed")) {
		t.Errorf("err = %v, want resolver failed", err)
	}
}

// TestOpenSessionAttachesIdentity pins the SDK contract: open + data first
// frames carry Payload.identity and the requested scopes.
func TestOpenSessionAttachesIdentity(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-open-identity", rec)
	backend := &sessionOpenBackend{}
	transport.SetTestBackend(backend)

	rn := reg.RemoteNodeByID("n-open-identity")
	sess, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: "sess-identity",
		Scopes:    types.Scope{types.SessionAccess, types.WorkspaceRead},
		Request:   &types.SessionRequest{Runtime: types.RuntimeTai},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer sess.Close(context.Background())

	openFrame := backend.frameByKind("open")
	if openFrame == nil || openFrame.Identity == nil {
		t.Fatal("open first frame does not carry identity")
	}
	if openFrame.Identity.UserId != "user-1" || openFrame.Identity.TeamId != "team-1" ||
		openFrame.Identity.UserToken != "user-token-1" {
		t.Errorf("open identity = %+v", openFrame.Identity)
	}

	dataFrame := backend.frameByKind("data")
	if dataFrame == nil || dataFrame.Identity == nil {
		t.Fatal("data first frame does not carry identity")
	}
	if dataFrame.Identity.UserToken != "user-token-1" {
		t.Errorf("data identity = %+v", dataFrame.Identity)
	}

	calls := rec.issueCalls()
	if len(calls) == 0 {
		t.Fatal("IssueUserToken was never called")
	}
	// Background stream recovery may issue additional credentials; the first
	// issuance is the one that opened the session.
	if fmt.Sprint(calls[0]) != fmt.Sprint(types.Scope{types.SessionAccess, types.WorkspaceRead}) {
		t.Errorf("scopes = %v", calls[0])
	}
}

func TestOpenSessionAuthNotConfigured(t *testing.T) {
	reg := testRegistry()
	reg.Register("n-open-noauth", &pb.Register{Hardware: &pb.Hardware{}}, testAuth(t))
	transport.SetTestBackend(&mockTransportBackend{})

	rn := reg.RemoteNodeByID("n-open-noauth")
	if _, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{SessionID: "s"}); err == nil {
		t.Fatal("expected an error when no auth provider is configured")
	}
}

func TestOpenSessionIssueFailure(t *testing.T) {
	rec := newRecordingAuth(t)
	rec.issueErr = fmt.Errorf("signing key unavailable")
	reg := registryWithAuth(t, "n-open-sign", rec)
	transport.SetTestBackend(&mockTransportBackend{})

	rn := reg.RemoteNodeByID("n-open-sign")
	_, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{SessionID: "s"})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("issue user token")) {
		t.Errorf("err = %v, want issue user token error", err)
	}
}

// TestResumeSessionReissuesIdentity verifies resume issues a fresh credential
// and attaches it to both first frames.
func TestResumeSessionReissuesIdentity(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-resume-identity", rec)
	opened, _ := proto.Marshal(&pb.SessionOpened{SessionId: "sess-resume", LastDataSeq: 7})
	backend := &sessionOpenBackend{openData: opened}
	transport.SetTestBackend(backend)

	rn := reg.RemoteNodeByID("n-resume-identity")
	sess, err := rn.ResumeSession(context.Background(), &types.ResumeSessionRequest{
		SessionID:   "sess-resume",
		Scopes:      types.Scope{types.SessionAccess, types.WorkspaceWrite},
		LastCtrlSeq: 3,
		LastDataSeq: 7,
	})
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	defer sess.Close(context.Background())

	resumeFrame := backend.frameByKind("resume")
	if resumeFrame == nil || resumeFrame.Identity == nil {
		t.Fatal("resume first frame does not carry identity")
	}
	if resumeFrame.Identity.UserToken != "user-token-1" {
		t.Errorf("resume identity = %+v", resumeFrame.Identity)
	}
	var resume pb.SessionResume
	if err := proto.Unmarshal(resumeFrame.Data, &resume); err != nil {
		t.Fatalf("unmarshal resume: %v", err)
	}
	if resume.SessionId != "sess-resume" || resume.LastCtrlSeq != 3 || resume.LastDataSeq != 7 {
		t.Errorf("resume payload = (%q,%d,%d)", resume.SessionId, resume.LastCtrlSeq, resume.LastDataSeq)
	}

	dataFrame := backend.frameByKind("data")
	if dataFrame == nil || dataFrame.Identity == nil {
		t.Fatal("resume data first frame does not carry identity")
	}

	calls := rec.issueCalls()
	if len(calls) == 0 || fmt.Sprint(calls[0]) != fmt.Sprint(types.Scope{types.SessionAccess, types.WorkspaceWrite}) {
		t.Errorf("resume scopes = %v", calls)
	}
}

func TestResumeSessionIssueFailure(t *testing.T) {
	rec := newRecordingAuth(t)
	rec.issueErr = fmt.Errorf("resolver failed")
	reg := registryWithAuth(t, "n-resume-fail", rec)
	transport.SetTestBackend(&mockTransportBackend{})

	rn := reg.RemoteNodeByID("n-resume-fail")
	_, err := rn.ResumeSession(context.Background(), &types.ResumeSessionRequest{SessionID: "s"})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("issue user token")) {
		t.Errorf("err = %v, want issue user token error", err)
	}
}

func TestResumeSessionErrorResponses(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.Register("n-resume-err", &pb.Register{Hardware: &pb.Hardware{}}, a)
	reg.SetAuth(newRecordingAuth(t))
	rn := reg.RemoteNodeByID("n-resume-err")

	tests := []struct {
		name string
		kind string
		data []byte
	}{
		{name: "error frame", kind: "error", data: mustMarshal(t, &pb.SessionError{Code: "RECOVERY_GAP", Message: "lost"})},
		{name: "unexpected kind", kind: "something_else"},
		{name: "bad opened payload", kind: "opened", data: []byte{0xff, 0xff}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			transport.SetTestBackend(&mockTransportBackend{
				handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
					return &mockNodeStream{Reader: bytes.NewReader(buildCodecFrame(types.NamespaceSession, tc.kind, tc.data)), buf: &bytes.Buffer{}}, nil
				},
			})
			if _, err := rn.ResumeSession(context.Background(), &types.ResumeSessionRequest{SessionID: "s"}); err == nil {
				t.Error("expected resume error")
			}
		})
	}
}

func TestResumeSessionDataStreamError(t *testing.T) {
	a := testAuth(t)
	reg := testRegistry()
	reg.Register("n-resume-data", &pb.Register{Hardware: &pb.Hardware{}}, a)
	reg.SetAuth(newRecordingAuth(t))
	rn := reg.RemoteNodeByID("n-resume-data")

	opened, _ := proto.Marshal(&pb.SessionOpened{SessionId: "s"})
	transport.SetTestBackend(&mockTransportBackend{
		handler: func(ff *pb.Payload) (io.ReadWriteCloser, error) {
			if ff.Kind == "resume" {
				return &mockNodeStream{Reader: bytes.NewReader(buildCodecFrame(types.NamespaceSession, "opened", opened)), buf: &bytes.Buffer{}}, nil
			}
			return nil, fmt.Errorf("data stream unavailable")
		},
	})
	if _, err := rn.ResumeSession(context.Background(), &types.ResumeSessionRequest{SessionID: "s"}); err == nil {
		t.Error("expected data stream error")
	}
}

func TestMakeResumeFnReissuesIdentity(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-mk-resume", rec)
	opened, _ := proto.Marshal(&pb.SessionOpened{SessionId: "s", LastDataSeq: 7})
	backend := &sessionOpenBackend{openData: opened}
	transport.SetTestBackend(backend)

	rn := reg.RemoteNodeByID("n-mk-resume").(*remoteNode)
	fn := rn.makeResumeFn("s", types.Scope{types.SessionAccess, types.WorkspaceDelete}, false)
	ctrl, data, lastSeq, err := fn(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("resume func: %v", err)
	}
	defer ctrl.Close()
	defer data.Close()
	if lastSeq != 7 {
		t.Errorf("lastDataSeq = %d, want 7", lastSeq)
	}
	if frame := backend.frameByKind("resume"); frame == nil || frame.Identity == nil {
		t.Fatal("resume first frame missing identity")
	}
}

// TestUserTokenRefresher pins the renewal contract: prefer re-signing the
// existing credential, fall back to issuing a new one.
func TestUserTokenRefresher(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-refresh", rec)

	rn := reg.RemoteNodeByID("n-refresh").(*remoteNode)
	identity := &pb.Identity{UserId: "user-1", TeamId: "team-1", UserToken: "user-token-1"}
	refresh := rn.userTokenRefresher(identity, nil)

	token, _, err := refresh()
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if token != "refreshed-token" {
		t.Errorf("token = %q, want refreshed-token", token)
	}
	if got := rec.refreshCalls(); len(got) != 1 || got[0] != "user-token-1" {
		t.Errorf("RefreshUserToken calls = %v, want [user-token-1]", got)
	}
	// The renewed token becomes the next renewal input.
	if _, _, err := refresh(); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if got := rec.refreshCalls(); len(got) != 2 || got[1] != "refreshed-token" {
		t.Errorf("second RefreshUserToken call = %v", got)
	}

	// When renewal fails, a new credential is issued with default scopes.
	rec.refreshErr = fmt.Errorf("expired")
	identity2 := &pb.Identity{UserToken: "expired-token"}
	refresh2 := rn.userTokenRefresher(identity2, nil)
	token, _, err = refresh2()
	if err != nil {
		t.Fatalf("fallback refresh: %v", err)
	}
	if token != "user-token-1" {
		t.Errorf("fallback token = %q, want user-token-1", token)
	}
	calls := rec.issueCalls()
	if len(calls) == 0 || fmt.Sprint(calls[0]) != fmt.Sprint(types.Scope{types.SessionAccess}) {
		t.Errorf("fallback issue scopes = %v", calls)
	}

	// Refresher without an auth provider reports the configuration error.
	regNoAuth := testRegistry()
	regNoAuth.Register("n-refresh-noauth", &pb.Register{Hardware: &pb.Hardware{}}, testAuth(t))
	rn2 := regNoAuth.RemoteNodeByID("n-refresh-noauth").(*remoteNode)
	if _, _, err := rn2.userTokenRefresher(&pb.Identity{}, nil)(); err == nil {
		t.Error("expected auth-not-configured error")
	}
}

// TestOpenSessionWithoutAuthDoesNotRefresh guards the refresher wiring: when no
// identity could be issued the session is never created.
func TestSessionIdentityDefaults(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-default-scopes", rec)
	backend := &sessionOpenBackend{}
	transport.SetTestBackend(backend)

	rn := reg.RemoteNodeByID("n-default-scopes")
	sess, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: "sess-default",
		Request:   &types.SessionRequest{Runtime: types.RuntimeTai},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer sess.Close(context.Background())

	calls := rec.issueCalls()
	if len(calls) == 0 || len(calls[0]) != 1 || calls[0][0] != types.SessionAccess {
		t.Errorf("default scopes = %v, want [session:access]", calls)
	}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// TestOpenSessionAnonymousOmitsIdentity covers sessions without a user
// (system tasks, unattended nodes): identity.md §3.2 — identity is optional.
func TestOpenSessionAnonymousOmitsIdentity(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-anon-open", rec)
	backend := &sessionOpenBackend{}
	transport.SetTestBackend(backend)

	rn := reg.RemoteNodeByID("n-anon-open").(*remoteNode)
	sess, err := rn.OpenSession(context.Background(), &types.SessionOpenRequest{
		SessionID: "s",
		Anonymous: true,
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer sess.Close(context.Background())

	if frame := backend.frameByKind("open"); frame == nil || frame.Identity != nil {
		t.Fatalf("anonymous open first frame identity = %v, want nil", frame.GetIdentity())
	}
	if calls := rec.issueCalls(); len(calls) != 0 {
		t.Errorf("IssueUserToken calls = %d for anonymous session, want 0", len(calls))
	}
}

// TestResumeSessionAnonymousOmitsIdentity covers anonymous resume.
func TestResumeSessionAnonymousOmitsIdentity(t *testing.T) {
	rec := newRecordingAuth(t)
	reg := registryWithAuth(t, "n-anon-resume", rec)
	backend := &sessionOpenBackend{}
	transport.SetTestBackend(backend)

	rn := reg.RemoteNodeByID("n-anon-resume").(*remoteNode)
	sess, err := rn.ResumeSession(context.Background(), &types.ResumeSessionRequest{
		SessionID: "s",
		Anonymous: true,
	})
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	defer sess.Close(context.Background())

	if frame := backend.frameByKind("resume"); frame == nil || frame.Identity != nil {
		t.Fatalf("anonymous resume first frame identity = %v, want nil", frame.GetIdentity())
	}
	if calls := rec.issueCalls(); len(calls) != 0 {
		t.Errorf("IssueUserToken calls = %d for anonymous resume, want 0", len(calls))
	}
}
