package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/node"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"github.com/yaoapp/tai-golang/workspace"
	"google.golang.org/protobuf/proto"
)

// TestMain installs an empty default node registry.
//
// Session handlers look nodes up through node.Get, which dereferences the
// package-global registry. Without one, the "node not found" tests would panic
// instead of exercising the 404 path.
func TestMain(m *testing.M) {
	node.NewRegistry(&pb.PoolConfig{}, nil)
	// Install the transport double once. The SDK reads the package-global
	// backend from its own goroutines, so replacing it per test would race
	// with sessions still shutting down; the double is instead keyed by node
	// ID, and each test uses its own node.
	transport.SetTestBackend(sharedBackend)
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Transport test double
// ---------------------------------------------------------------------------

// fakeLightStream is an in-memory Light stream. It returns a scripted response
// for the first frame and then blocks, so an open session stays alive for the
// duration of the test. Writing a "close" ctrl command injects a "closed" reply,
// mirroring how a Tai node acknowledges a close, and Close unblocks every read.
type fakeLightStream struct {
	mu      sync.Mutex
	script  []byte
	wake    chan struct{}
	closed  chan struct{}
	once    sync.Once
	wrote   bytes.Buffer
	written []byte
}

func newFakeLightStream(script []byte) *fakeLightStream {
	return &fakeLightStream{
		script: script,
		wake:   make(chan struct{}, 1),
		closed: make(chan struct{}),
	}
}

// inject appends bytes to the read side and wakes a blocked reader.
func (s *fakeLightStream) inject(data []byte) {
	s.mu.Lock()
	s.script = append(s.script, data...)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *fakeLightStream) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.script) > 0 {
			n := copy(p, s.script)
			s.script = s.script[n:]
			s.mu.Unlock()
			return n, nil
		}
		s.mu.Unlock()

		select {
		case <-s.closed:
			s.mu.Lock()
			empty := len(s.script) == 0
			s.mu.Unlock()
			if empty {
				return 0, io.EOF
			}
		case <-s.wake:
		}
	}
}

// Write records everything the SDK sends. A "close" ctrl command gets a
// "closed" reply injected into the read side, which is how a Tai node
// acknowledges a session close and lets Session.Close finish.
func (s *fakeLightStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	n, err := s.wrote.Write(p)
	s.written = append(s.written, p...)
	frames := drainFrames(&s.written)
	s.mu.Unlock()

	for _, frame := range frames {
		if frame.GetKind() != "message" {
			continue
		}
		var msg pb.SessionMessage
		if proto.Unmarshal(frame.GetData(), &msg) != nil || msg.GetKind() != "close" {
			continue
		}
		var buf bytes.Buffer
		_ = transport.WriteFrame(&buf, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "closed",
		})
		s.inject(buf.Bytes())
	}
	return n, err
}

func (s *fakeLightStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// drainFrames decodes every complete length-prefixed frame in *buf and removes
// the consumed bytes, leaving a partial frame for the next write.
func drainFrames(buf *[]byte) []*pb.Payload {
	var frames []*pb.Payload
	for len(*buf) >= 4 {
		length := int(binary.BigEndian.Uint32((*buf)[:4]))
		if len(*buf) < 4+length {
			break
		}
		payload := (*buf)[4 : 4+length]
		*buf = (*buf)[4+length:]
		msg := &pb.Payload{}
		if err := proto.Unmarshal(payload, msg); err == nil {
			frames = append(frames, msg)
		}
	}
	return frames
}

// sharedBackend is the single transport double installed for the package.
// State is per node so tests cannot observe each other's sessions.
var sharedBackend = &fakeBackend{}

// fakeBackend is a transport.Backend test double. It records the first frame
// of every stream the SDK opens for a node, answers open/resume with an
// "opened" frame, and can be told to fail a specific stream kind.
type fakeBackend struct {
	mu          sync.Mutex
	kinds       map[string][]string
	frames      map[string][]*pb.Payload
	streams     map[string][]*fakeLightStream
	failKind    map[string]string
	closedNodes map[string]bool
}

func (b *fakeBackend) OpenLightStream(_ context.Context, nodeID string, firstFrame *pb.Payload) (io.ReadWriteCloser, error) {
	b.mu.Lock()
	if b.kinds == nil {
		b.kinds = map[string][]string{}
		b.frames = map[string][]*pb.Payload{}
		b.streams = map[string][]*fakeLightStream{}
		b.failKind = map[string]string{}
		b.closedNodes = map[string]bool{}
	}
	if b.closedNodes[nodeID] {
		b.mu.Unlock()
		return nil, errors.New("transport: node closed")
	}
	if firstFrame.GetNamespace() == workspace.NamespaceWorkspace {
		b.mu.Unlock()
		return workspaceStream(firstFrame)
	}
	b.kinds[nodeID] = append(b.kinds[nodeID], firstFrame.GetKind())
	b.frames[nodeID] = append(b.frames[nodeID], firstFrame)
	fail := b.failKind[nodeID] == firstFrame.GetKind()
	b.mu.Unlock()

	if fail {
		return nil, errors.New("transport: open stream failed")
	}

	var script []byte
	switch firstFrame.GetKind() {
	case "open", "resume":
		var buf bytes.Buffer
		if err := transport.WriteFrame(&buf, &pb.Payload{
			Namespace: types.NamespaceSession,
			Kind:      "opened",
		}); err != nil {
			return nil, err
		}
		script = buf.Bytes()
	}

	stream := newFakeLightStream(script)
	b.mu.Lock()
	b.streams[nodeID] = append(b.streams[nodeID], stream)
	b.mu.Unlock()
	return stream, nil
}

func (b *fakeBackend) HeavyPick(string) (net.Conn, error) {
	return nil, errors.New("transport: no heavy pool")
}

func (b *fakeBackend) HeavyReturn(net.Conn) {}

func (b *fakeBackend) HeavyRemove(net.Conn) {}

// setFailKind makes the next stream of the given kind for nodeID fail to open.
func (b *fakeBackend) setFailKind(nodeID, kind string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failKind == nil {
		b.failKind = map[string]string{}
	}
	b.failKind[nodeID] = kind
}

// closeNode closes every stream opened for nodeID and rejects new ones, so the
// node's sessions can no longer auto-resume.
func (b *fakeBackend) closeNode(nodeID string) {
	b.mu.Lock()
	if b.closedNodes == nil {
		b.closedNodes = map[string]bool{}
	}
	b.closedNodes[nodeID] = true
	streams := append([]*fakeLightStream(nil), b.streams[nodeID]...)
	b.mu.Unlock()
	for _, s := range streams {
		s.Close()
	}
}

// streamKinds returns the first-frame kinds of the streams opened for nodeID.
func (b *fakeBackend) streamKinds(nodeID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.kinds[nodeID]...)
}

// streamFrames returns the first frames of the streams opened for nodeID.
func (b *fakeBackend) streamFrames(nodeID string) []*pb.Payload {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*pb.Payload(nil), b.frames[nodeID]...)
}

// workspaceStream scripts the workspace namespace so Create's auto-create and
// name-resolution paths run without a real node.
func workspaceStream(firstFrame *pb.Payload) (io.ReadWriteCloser, error) {
	var data []byte
	switch firstFrame.GetKind() {
	case workspace.KindWSCreate:
		data, _ = json.Marshal(map[string]any{
			"id":   "ws_auto_1",
			"meta": map[string]any{"name": "auto"},
		})
	case workspace.KindWSList:
		data, _ = json.Marshal([]map[string]any{
			{"id": "ws_auto_1", "name": "auto-ws", "owner": "demo-user"},
		})
	}
	var buf bytes.Buffer
	if err := transport.WriteFrame(&buf, &pb.Payload{
		Namespace: workspace.NamespaceWorkspace,
		Kind:      firstFrame.GetKind() + workspace.SuffixResult,
		Data:      data,
	}); err != nil {
		return nil, err
	}
	return newFakeLightStream(buf.Bytes()), nil
}

// ---------------------------------------------------------------------------
// Handler harness
// ---------------------------------------------------------------------------

// demoHarness wires the session handler to a registered node and a scripted
// transport, so Create/Resume traverse the real SDK open/resume code paths
// without a network listener.
type demoHarness struct {
	handler *SessionHandler
	store   *Store
	auth    *fakeAuth
	backend *fakeBackend
	nodeID  string
	userID  string
	teamID  string
}

func newDemoHarness(t *testing.T, nodeID string) *demoHarness {
	t.Helper()

	provider := newFakeAuth()
	provider.identity = &types.UserIdentity{UserID: "demo-user", TeamID: "demo-team"}

	// Replaces the package-global registry, which is what node.Get reads.
	reg := node.NewRegistry(&pb.PoolConfig{}, nil)
	reg.SetAuth(provider)
	if _, err := reg.Register(nodeID, &pb.Register{Version: "test"}, provider); err != nil {
		t.Fatalf("register node: %v", err)
	}

	store := NewStore()
	backend := sharedBackend
	t.Cleanup(func() {
		// Stop every session this test opened: closing the node's streams makes
		// auto-resume fail, which lets the SDK read loops exit.
		backend.closeNode(nodeID)
		for _, si := range store.Sessions() {
			sess := si.sess()
			if sess == nil {
				continue
			}
			select {
			case <-sess.Done():
			case <-time.After(2 * time.Second):
				t.Errorf("session %s did not stop", si.ID)
			}
		}
	})

	return &demoHarness{
		handler: &SessionHandler{store: store, auth: provider},
		store:   store,
		auth:    provider,
		backend: backend,
		nodeID:  nodeID,
		userID:  "demo-user",
		teamID:  "demo-team",
	}
}

// create opens a session through the handler and returns the status and body.
func (h *demoHarness) create(t *testing.T) (int, map[string]any) {
	t.Helper()
	body := `{"nodeId":"` + h.nodeID + `","request":{"cwd":"/tmp"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req = withTestUser(req)
	rec := httptest.NewRecorder()
	h.handler.Create(rec, req)
	return rec.Code, decodeObject(t, rec)
}

// createOK opens a session and fails the test unless it succeeded.
func (h *demoHarness) createOK(t *testing.T) map[string]any {
	t.Helper()
	code, out := h.create(t)
	if code != http.StatusOK {
		t.Fatalf("create status = %d, body = %v", code, out)
	}
	return out
}

// resume calls the Resume handler for a stored session.
func (h *demoHarness) resume(t *testing.T, id string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/resume", nil)
	req = withTestUser(req)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.handler.Resume(rec, req)
	return rec.Code, decodeObject(t, rec)
}

// decodeObject decodes a JSON object response body.
func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode object %q: %v", rec.Body.String(), err)
	}
	return out
}

// fakeSession stubs the types.Session interface; only Done is used by
// watchSession. Embedding the interface keeps the rest nil on purpose.
type fakeSession struct {
	types.Session
	done chan struct{}
}

func (f *fakeSession) Done() <-chan struct{} { return f.done }

// errIssueFailed is the sentinel used by fakeAuth to simulate a signing failure.
var errIssueFailed = errors.New("issue user token failed")

// ---------------------------------------------------------------------------
// Scope and store
// ---------------------------------------------------------------------------

// TestDemoSessionScopes pins the demo's default session scope set: enough to
// drive a session, read/write/delete its workspace, and call remote tools.
func TestDemoSessionScopes(t *testing.T) {
	scopes := demoSessionScopes()
	want := []string{
		types.SessionAccess,
		types.WorkspaceRead,
		types.WorkspaceWrite,
		types.WorkspaceDelete,
		types.RemoteToolCall,
	}
	if len(scopes) != len(want) {
		t.Fatalf("len(scopes) = %d, want %d: %v", len(scopes), len(want), scopes)
	}
	for _, action := range want {
		if !scopes.Has(action) {
			t.Errorf("scopes %v missing %q", scopes, action)
		}
	}
	// Resource wildcard must not leak across resources; there is no global "*".
	if scopes.Has(types.WorkspaceAll) {
		t.Errorf("scopes %v must not contain workspace:*", scopes)
	}
}

// TestSessionInfoUser covers the default and updated identity accessors.
func TestSessionInfoUser(t *testing.T) {
	info := &SessionInfo{ID: "s1"}
	if userID, teamID := info.GetUser(); userID != "" || teamID != "" {
		t.Errorf("empty session user = %q/%q, want empty", userID, teamID)
	}
	info.SetUser("user-1", "team-1")
	if userID, teamID := info.GetUser(); userID != "user-1" || teamID != "team-1" {
		t.Errorf("session user = %q/%q, want user-1/team-1", userID, teamID)
	}
}

// TestSessionListIncludesUser verifies list rows carry the owning user.
func TestSessionListIncludesUser(t *testing.T) {
	store := NewStore()
	withUser := &SessionInfo{ID: "s-user", NodeID: "n1", Status: StatusRunning}
	withUser.SetUser("user-1", "team-1")
	store.AddSession(withUser)
	withWS := &SessionInfo{ID: "s-ws", NodeID: "n1", Status: StatusRunning,
		WorkspaceID: "ws-1", WorkspaceName: "demo-ws"}
	store.AddSession(withWS)
	store.AddSession(&SessionInfo{ID: "s-anon", NodeID: "n1", Status: StatusRunning})

	h := &SessionHandler{store: store, auth: newFakeAuth()}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode list %q: %v", rec.Body.String(), err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[row["id"].(string)] = row
	}
	if got := byID["s-user"]["userId"]; got != "user-1" {
		t.Errorf("userId = %v, want user-1", got)
	}
	if got := byID["s-user"]["teamId"]; got != "team-1" {
		t.Errorf("teamId = %v, want team-1", got)
	}
	if got := byID["s-anon"]["userId"]; got != "" {
		t.Errorf("anonymous userId = %v, want empty", got)
	}
	if got := byID["s-ws"]["workspaceId"]; got != "ws-1" {
		t.Errorf("workspaceId = %v, want ws-1", got)
	}
	if got := byID["s-ws"]["workspaceName"]; got != "demo-ws" {
		t.Errorf("workspaceName = %v, want demo-ws", got)
	}
}

// TestSessionListEmpty verifies the empty-store response is a JSON array.
func TestSessionListEmpty(t *testing.T) {
	h := &SessionHandler{store: NewStore(), auth: newFakeAuth()}
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body = %q, want []", body)
	}
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

// TestCreateSessionWithIdentity covers the successful create path: the session
// is opened through the SDK transport, the owner identity is stored, and both
// the response and the list expose it.
func TestCreateSessionWithIdentity(t *testing.T) {
	h := newDemoHarness(t, "node-create")

	out := h.createOK(t)
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("response missing id: %v", out)
	}
	if out["status"] != string(StatusRunning) {
		t.Errorf("status = %v, want %q", out["status"], StatusRunning)
	}
	if out["nodeId"] != h.nodeID {
		t.Errorf("nodeId = %v, want %q", out["nodeId"], h.nodeID)
	}
	if out["userId"] != h.userID || out["teamId"] != h.teamID {
		t.Errorf("identity = %v/%v, want %s/%s", out["userId"], out["teamId"], h.userID, h.teamID)
	}

	info, ok := h.store.GetSession(id)
	if !ok {
		t.Fatalf("session %q not stored", id)
	}
	if userID, teamID := info.GetUser(); userID != h.userID || teamID != h.teamID {
		t.Errorf("stored user = %q/%q, want %s/%s", userID, teamID, h.userID, h.teamID)
	}
	if info.GetStatus() != StatusRunning {
		t.Errorf("stored status = %q, want running", info.GetStatus())
	}

	// The transport must have opened ctrl + data streams for this session.
	if kinds := h.backend.streamKinds(h.nodeID); len(kinds) != 2 || kinds[0] != "open" || kinds[1] != "data" {
		t.Errorf("stream kinds = %v, want [open data]", kinds)
	}
	// The first frame must carry the issued identity (user_id/team_id/token).
	frames := h.backend.streamFrames(h.nodeID)
	if len(frames) == 0 || frames[0].GetIdentity() == nil {
		t.Fatalf("open first frame has no identity: %v", frames)
	}
	identity := frames[0].GetIdentity()
	if identity.GetUserId() != h.userID || identity.GetTeamId() != h.teamID {
		t.Errorf("wire identity = %s/%s, want %s/%s",
			identity.GetUserId(), identity.GetTeamId(), h.userID, h.teamID)
	}
	if identity.GetUserToken() != "user-token" {
		t.Errorf("wire user_token = %q, want user-token", identity.GetUserToken())
	}
	// Both the display resolution and the SDK transport must request the demo
	// scope set, not an empty or narrowed one.
	assertScopes := func() {
		t.Helper()
		calls := h.auth.calls()
		if len(calls) < 2 {
			t.Fatalf("IssueUserToken calls = %d, want handler + transport", len(calls))
		}
		for i, scopes := range calls {
			if len(scopes) != len(demoSessionScopes()) {
				t.Errorf("scope call %d = %v, want %v", i, scopes, demoSessionScopes())
			}
		}
	}
	assertScopes()

	// The API list must expose the same owner.
	rec := httptest.NewRecorder()
	h.handler.List(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(rows) != 1 || rows[0]["userId"] != h.userID {
		t.Errorf("list rows = %v, want one row owned by %s", rows, h.userID)
	}
}

// TestCreateSessionRequestNormalization covers runtime/isolation defaults, env
// key injection, and the providers-matrix rewrite applied before the session is
// opened.
func TestCreateSessionRequestNormalization(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek")
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("DEEPSEEK_BASE_URL", "")

	h := newDemoHarness(t, "node-normalize")
	body := `{"nodeId":"` + h.nodeID + `","request":{` +
		`"cwd":"/tmp",` +
		`"llm":{"default":{"provider_id":"deepseek","model":"deepseek-chat"},` +
		`"light":{"provider_id":"deepseek","model":"deepseek-flash"},` +
		`"realtime":{"provider_id":"openai","model":"gpt-realtime-mini"}},` +
		`"env":{"OPENAI_API_KEY":""}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req = withTestUser(req)
	rec := httptest.NewRecorder()
	h.handler.Create(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	frames := h.backend.streamFrames(h.nodeID)
	if len(frames) == 0 {
		t.Fatal("no open stream recorded")
	}
	var open pb.SessionOpen
	if err := proto.Unmarshal(frames[0].GetData(), &open); err != nil {
		t.Fatalf("unmarshal SessionOpen: %v", err)
	}
	if got := open.GetRequest().GetRuntime(); got != types.RuntimeTai {
		t.Errorf("runtime = %q, want %q", got, types.RuntimeTai)
	}
	if got := open.GetRequest().GetIsolation(); got != "host" {
		t.Errorf("isolation = %q, want host", got)
	}
	env := open.GetRequest().GetEnv()
	if env["DEEPSEEK_API_KEY"] != "sk-deepseek" {
		t.Errorf("DEEPSEEK_API_KEY = %q, want injected key", env["DEEPSEEK_API_KEY"])
	}
	if env["OPENAI_API_KEY"] != "sk-openai" {
		t.Errorf("OPENAI_API_KEY = %q, want injected key", env["OPENAI_API_KEY"])
	}
	if env["DEEPSEEK_BASE_URL"] != "https://api.deepseek.com/v1" {
		t.Errorf("DEEPSEEK_BASE_URL = %q, want default endpoint", env["DEEPSEEK_BASE_URL"])
	}
	var llm map[string]json.RawMessage
	if err := json.Unmarshal(open.GetRequest().GetLlm(), &llm); err != nil {
		t.Fatalf("unmarshal rewritten LLM: %v", err)
	}
	if _, ok := llm["primary"]; !ok {
		t.Errorf("LLM = %s, want a primary role", open.GetRequest().GetLlm())
	}
	if _, ok := llm["roles"]; !ok {
		t.Errorf("LLM = %s, want role endpoints", open.GetRequest().GetLlm())
	}
}

// TestCreateSessionAutoWorkspace covers the auto-create branch: when neither
// cwd nor workspaceId is given the handler creates a workspace, then resolves
// its display name for the response.
func TestCreateSessionAutoWorkspace(t *testing.T) {
	h := newDemoHarness(t, "node-auto-ws")
	body := `{"nodeId":"` + h.nodeID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req = withTestUser(req)
	rec := httptest.NewRecorder()
	h.handler.Create(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	out := decodeObject(t, rec)
	if out["workspaceId"] != "ws_auto_1" {
		t.Errorf("workspaceId = %v, want ws_auto_1", out["workspaceId"])
	}
	if out["workspaceName"] != "auto-ws" {
		t.Errorf("workspaceName = %v, want auto-ws", out["workspaceName"])
	}
	// Only session-namespace streams are recorded: ctrl + data.
	if kinds := h.backend.streamKinds(h.nodeID); len(kinds) != 2 || kinds[0] != "open" {
		t.Errorf("session stream kinds = %v, want [open data]", kinds)
	}
}

// TestCreateSessionBadRequests covers malformed bodies and missing nodeId.
func TestCreateSessionBadRequests(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{"nodeId":`},
		{name: "empty body", body: ``},
		{name: "missing nodeId", body: `{"request":{"cwd":"/tmp"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := NewStore()
			h := &SessionHandler{store: store, auth: newFakeAuth()}
			req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(c.body))
			req = withTestUser(req)
			rec := httptest.NewRecorder()
			h.Create(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if len(store.Sessions()) != 0 {
				t.Error("a session was stored after a rejected request")
			}
		})
	}
}

// TestCreateSessionNodeNotFound verifies the 404 when no node is registered.
func TestCreateSessionNodeNotFound(t *testing.T) {
	h := &SessionHandler{store: NewStore(), auth: newFakeAuth()}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions",
		strings.NewReader(`{"nodeId":"node-that-does-not-exist","request":{"cwd":"/tmp"}}`))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestCreateSessionIssueUserTokenFailure verifies the 500 and no stored session
// when the provider cannot issue the owner credential.
func TestCreateSessionIssueUserTokenFailure(t *testing.T) {
	h := newDemoHarness(t, "node-issue-fail")
	failing := newFakeAuth()
	failing.userErr = errIssueFailed
	h.handler.auth = failing

	code, out := h.create(t)
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %v)", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "issue user token") {
		t.Errorf("error = %q, want to mention issue user token", msg)
	}
	if len(h.store.Sessions()) != 0 {
		t.Error("a session was stored after the credential failed")
	}
}

// TestCreateSessionOpenFailure verifies a transport failure during open maps to
// 500 and leaves the store empty.
func TestCreateSessionOpenFailure(t *testing.T) {
	h := newDemoHarness(t, "node-open-fail")
	h.backend.setFailKind(h.nodeID, "open")

	code, out := h.create(t)
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %v)", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "open session") {
		t.Errorf("error = %q, want to mention open session", msg)
	}
	if len(h.store.Sessions()) != 0 {
		t.Error("a session was stored after open failed")
	}
}

// ---------------------------------------------------------------------------
// Resume
// ---------------------------------------------------------------------------

// TestResumeSessionReissuesIdentity covers the successful resume path: the SDK
// reattaches through a fresh ctrl+data pair carrying a new credential, and the
// owner identity is refreshed.
func TestResumeSessionReissuesIdentity(t *testing.T) {
	h := newDemoHarness(t, "node-resume")
	out := h.createOK(t)
	id, _ := out["id"].(string)

	info, ok := h.store.GetSession(id)
	if !ok {
		t.Fatalf("session %q not stored", id)
	}
	info.SetStatus(StatusDisconnected, h.store)

	// A resume re-issues the credential from its own business context: change
	// the identity so the test can observe the row being refreshed.
	h.handler.auth = &resumeIdentityAuth{fakeAuth: h.auth}

	code, body := h.resume(t, id)
	if code != http.StatusOK {
		t.Fatalf("resume status = %d, body = %v", code, body)
	}
	if body["status"] != string(StatusRunning) {
		t.Errorf("status = %v, want running", body["status"])
	}
	if got := info.GetStatus(); got != StatusRunning {
		t.Errorf("stored status = %q, want running", got)
	}
	if userID, teamID := info.GetUser(); userID != "resumed-user" || teamID != "resumed-team" {
		t.Errorf("stored user = %q/%q, want resumed-user/resumed-team", userID, teamID)
	}
	if body["userId"] != "resumed-user" || body["teamId"] != "resumed-team" {
		t.Errorf("response identity = %v/%v, want resumed-user/resumed-team", body["userId"], body["teamId"])
	}

	// Resume must open a resume ctrl stream plus its data stream, and the first
	// frame must carry the credential.
	kinds := h.backend.streamKinds(h.nodeID)
	if len(kinds) != 4 || kinds[2] != "resume" || kinds[3] != "data" {
		t.Fatalf("stream kinds = %v, want [open data resume data]", kinds)
	}
	frames := h.backend.streamFrames(h.nodeID)
	if frames[2].GetIdentity() == nil || frames[2].GetIdentity().GetUserId() == "" {
		t.Errorf("resume first frame identity = %v, want user id", frames[2].GetIdentity())
	}
}

// resumeIdentityAuth returns a different identity, used to prove a resume can
// change the session owner.
type resumeIdentityAuth struct {
	*fakeAuth
}

func (a *resumeIdentityAuth) IssueUserToken(_ string, scopes types.Scope) (string, *types.UserIdentity, error) {
	a.recordCall(scopes)
	return "resumed-token", &types.UserIdentity{UserID: "resumed-user", TeamID: "resumed-team"}, nil
}

// TestResumeSessionNotFound verifies the 404 for an unknown session ID.
func TestResumeSessionNotFound(t *testing.T) {
	h := &SessionHandler{store: NewStore(), auth: newFakeAuth()}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/missing/resume", nil)
	req = withTestUser(req)
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.Resume(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestResumeSessionNotResumable verifies the 409 for a running session.
func TestResumeSessionNotResumable(t *testing.T) {
	store := NewStore()
	store.AddSession(&SessionInfo{ID: "s-running", NodeID: "n1", Status: StatusRunning})
	h := &SessionHandler{store: store, auth: newFakeAuth()}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-running/resume", nil)
	req = withTestUser(req)
	req.SetPathValue("id", "s-running")
	rec := httptest.NewRecorder()
	h.Resume(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestResumeSessionNodeNotFound verifies the 404 when the session's node has
// gone away.
func TestResumeSessionNodeNotFound(t *testing.T) {
	store := NewStore()
	store.AddSession(&SessionInfo{ID: "s-orphan", NodeID: "node-gone", Status: StatusDisconnected})
	h := &SessionHandler{store: store, auth: newFakeAuth()}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-orphan/resume", nil)
	req = withTestUser(req)
	req.SetPathValue("id", "s-orphan")
	rec := httptest.NewRecorder()
	h.Resume(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestResumeSessionIssueUserTokenFailure verifies a credential failure aborts
// the resume before the status is touched, so the user can retry.
func TestResumeSessionIssueUserTokenFailure(t *testing.T) {
	h := newDemoHarness(t, "node-resume-fail")
	out := h.createOK(t)
	id := out["id"].(string)

	info, _ := h.store.GetSession(id)
	info.SetStatus(StatusDisconnected, h.store)

	failing := newFakeAuth()
	failing.userErr = errIssueFailed
	h.handler.auth = failing

	code, body := h.resume(t, id)
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %v)", code, body)
	}
	if got := info.GetStatus(); got != StatusDisconnected {
		t.Errorf("status = %q, want disconnected after failed credential", got)
	}
}

// TestResumeSessionTransportFailure verifies a transport failure rolls the
// status back so the user can retry.
func TestResumeSessionTransportFailure(t *testing.T) {
	h := newDemoHarness(t, "node-resume-transport-fail")
	out := h.createOK(t)
	id := out["id"].(string)

	info, _ := h.store.GetSession(id)
	info.SetStatus(StatusDisconnected, h.store)
	h.backend.setFailKind(h.nodeID, "resume")

	code, body := h.resume(t, id)
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %v)", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "resume session") {
		t.Errorf("error = %q, want to mention resume session", msg)
	}
	if got := info.GetStatus(); got != StatusDisconnected {
		t.Errorf("status = %q, want disconnected after failed resume", got)
	}
}

// TestResumeSessionClosedDuringResume verifies the 410 when the session record
// is deleted while the resume is in flight.
func TestResumeSessionClosedDuringResume(t *testing.T) {
	store := NewStore()
	h := &SessionHandler{store: store, auth: newFakeAuth()}
	sess := &fakeSession{done: make(chan struct{})}
	info := &SessionInfo{ID: "s-gone", NodeID: "node-gone-during", Status: StatusDisconnected, Session: sess}
	store.AddSession(info)

	// Register the node so the handler reaches the transport step, then remove
	// the record from the store before the resume installs the new instance.
	provider := newFakeAuth()
	reg := node.NewRegistry(&pb.PoolConfig{}, nil)
	reg.SetAuth(provider)
	if _, err := reg.Register("node-gone-during", &pb.Register{}, provider); err != nil {
		t.Fatalf("register node: %v", err)
	}
	t.Cleanup(func() { sharedBackend.closeNode("node-gone-during") })
	info.markDeleted()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-gone/resume", nil)
	req = withTestUser(req)
	req.SetPathValue("id", "s-gone")
	rec := httptest.NewRecorder()
	h.Resume(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410 (body: %s)", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

// TestDeleteSession covers the delete path against a live session.
func TestDeleteSession(t *testing.T) {
	h := newDemoHarness(t, "node-delete")
	out := h.createOK(t)
	id := out["id"].(string)

	if _, ok := h.store.GetSession(id); !ok {
		t.Fatalf("session %q not stored", id)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.handler.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if _, ok := h.store.GetSession(id); ok {
		t.Error("session still stored after delete")
	}
}

// TestDeleteSessionNotFound verifies the 404 for an unknown session ID.
func TestDeleteSessionNotFound(t *testing.T) {
	h := &SessionHandler{store: NewStore(), auth: newFakeAuth()}
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/missing", nil)
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Session lifecycle watcher
// ---------------------------------------------------------------------------

// TestWatchSessionTerminated verifies an unintended end marks the session
// terminated.
func TestWatchSessionTerminated(t *testing.T) {
	store := NewStore()
	h := &SessionHandler{store: store, auth: newFakeAuth()}
	sess := &fakeSession{done: make(chan struct{})}
	info := &SessionInfo{ID: "s1", Status: StatusRunning, Session: sess}
	store.AddSession(info)

	finished := make(chan struct{})
	go func() {
		h.watchSession(info, sess)
		close(finished)
	}()
	close(sess.done)

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("watchSession did not return after Done closed")
	}
	if got := info.GetStatus(); got != StatusTerminated {
		t.Errorf("status = %q, want terminated", got)
	}
}

// TestWatchSessionStaleInstance verifies the watcher for a replaced session
// instance leaves the current status alone (Resume swaps the instance).
func TestWatchSessionStaleInstance(t *testing.T) {
	store := NewStore()
	h := &SessionHandler{store: store, auth: newFakeAuth()}
	stored := &fakeSession{done: make(chan struct{})}
	watched := &fakeSession{done: make(chan struct{})}
	info := &SessionInfo{ID: "s1", Status: StatusRunning, Session: stored}
	store.AddSession(info)

	finished := make(chan struct{})
	go func() {
		h.watchSession(info, watched)
		close(finished)
	}()
	close(watched.done)

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("watchSession did not return after Done closed")
	}
	if got := info.GetStatus(); got != StatusRunning {
		t.Errorf("status = %q, want running (stale instance must not patch it)", got)
	}
}

// TestWatchSessionTerminalPreserved verifies a deliberate Close is not
// overwritten with terminated.
func TestWatchSessionTerminalPreserved(t *testing.T) {
	store := NewStore()
	h := &SessionHandler{store: store, auth: newFakeAuth()}
	sess := &fakeSession{done: make(chan struct{})}
	info := &SessionInfo{ID: "s1", Status: StatusClosed, Session: sess}
	store.AddSession(info)

	finished := make(chan struct{})
	go func() {
		h.watchSession(info, sess)
		close(finished)
	}()
	close(sess.done)

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("watchSession did not return after Done closed")
	}
	if got := info.GetStatus(); got != StatusClosed {
		t.Errorf("status = %q, want closed", got)
	}
}

// ---------------------------------------------------------------------------
// Identity resolution helper
// ---------------------------------------------------------------------------

// TestIssueSessionUser covers the identity-resolution helper directly.
func TestIssueSessionUser(t *testing.T) {
	scopes := demoSessionScopes()

	t.Run("success", func(t *testing.T) {
		provider := newFakeAuth()
		h := &SessionHandler{auth: provider}
		userID, teamID, err := sessionOwner(context.Background(), h.auth, "node-1", scopes)
		if err != nil {
			t.Fatalf("issueSessionUser: %v", err)
		}
		if userID != "user-1" || teamID != "team-1" {
			t.Errorf("identity = %q/%q, want user-1/team-1", userID, teamID)
		}
		if provider.gotNodeID != "node-1" {
			t.Errorf("nodeID = %q, want node-1", provider.gotNodeID)
		}
	})

	t.Run("provider error", func(t *testing.T) {
		provider := newFakeAuth()
		provider.userErr = errIssueFailed
		h := &SessionHandler{auth: provider}
		if _, _, err := sessionOwner(context.Background(), h.auth, "node-1", scopes); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("nil identity", func(t *testing.T) {
		provider := newFakeAuth()
		provider.identity = nil
		h := &SessionHandler{auth: provider}
		if _, _, err := sessionOwner(context.Background(), h.auth, "node-1", scopes); err == nil {
			t.Fatal("expected error for nil identity")
		}
	})

	t.Run("empty user id", func(t *testing.T) {
		provider := newFakeAuth()
		provider.identity = &types.UserIdentity{TeamID: "team-1"}
		h := &SessionHandler{auth: provider}
		if _, _, err := sessionOwner(context.Background(), h.auth, "node-1", scopes); err == nil {
			t.Fatal("expected error for empty user id")
		}
	})
}

// withTestUser simulates a logged-in demo account for handler-level tests.
func withTestUser(r *http.Request) *http.Request {
	return r.WithContext(withDemoUser(r.Context(), &DemoUser{Email: "tester@example.com", Name: "Tester"}))
}
