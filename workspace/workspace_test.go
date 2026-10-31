package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// ---------- Mock transport ----------

type mockTransport struct {
	mu            sync.Mutex
	handler       func(kind string, data []byte) ([]byte, error)
	rawHandler    func(kind string, data []byte) ([]byte, error) // returns raw bytes (no framing)
	streams       []*mockStream
	heavyErr      error
	heavyConns    []net.Conn // pre-loaded heavy connections for Pick
	heavyReturned []net.Conn
}

func (m *mockTransport) OpenLightStream(_ context.Context, _ string, firstFrame *pb.Payload) (Stream, error) {
	m.mu.Lock()
	h := m.handler
	rh := m.rawHandler
	m.mu.Unlock()

	// Check raw handler first (for streaming ops like Open)
	if rh != nil {
		raw, err := rh(firstFrame.Kind, firstFrame.Data)
		if err == nil && raw != nil {
			ms := &mockStream{Reader: bytes.NewReader(raw), buf: &bytes.Buffer{}}
			m.mu.Lock()
			m.streams = append(m.streams, ms)
			m.mu.Unlock()
			return ms, nil
		}
		// fall through to framed handler
	}

	if h == nil {
		return nil, fmt.Errorf("no handler configured")
	}

	respData, err := h(firstFrame.Kind, firstFrame.Data)
	if err != nil {
		return nil, err
	}

	// Build a framed response: kind.result + data
	respPayload := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      firstFrame.Kind + SuffixResult,
		Data:      respData,
	}
	respBytes, _ := proto.Marshal(respPayload)
	size := len(respBytes)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}

	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(respBytes)

	ms := &mockStream{Reader: bytes.NewReader(buf.Bytes()), buf: &bytes.Buffer{}}
	m.mu.Lock()
	m.streams = append(m.streams, ms)
	m.mu.Unlock()
	return ms, nil
}

func (m *mockTransport) HeavyPick(_ string) (net.Conn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.heavyConns) > 0 {
		c := m.heavyConns[0]
		m.heavyConns = m.heavyConns[1:]
		return c, nil
	}
	if m.heavyErr != nil {
		return nil, m.heavyErr
	}
	return nil, fmt.Errorf("no heavy connection")
}

func (m *mockTransport) HeavyReturn(conn net.Conn) {
	m.mu.Lock()
	m.heavyReturned = append(m.heavyReturned, conn)
	m.mu.Unlock()
}

type mockStream struct {
	*bytes.Reader
	buf          *bytes.Buffer
	closed       bool
	writeClosed  bool
	halfCloseErr error
}

func (ms *mockStream) Write(p []byte) (int, error) {
	return ms.buf.Write(p)
}

func (ms *mockStream) Close() error {
	ms.closed = true
	return nil
}

func (ms *mockStream) CloseWrite() error {
	ms.writeClosed = true
	return ms.halfCloseErr
}

// ---------- Test helpers ----------

func setupMock(handler func(kind string, data []byte) ([]byte, error)) *mockTransport {
	mt := &mockTransport{handler: handler, heavyErr: fmt.Errorf("no heavy")}
	Init(mt, "test-node", nil)
	return mt
}

// ---------- Tests ----------

func TestOpenWorkspace(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSOpen {
			return json.Marshal(Meta{Name: "test-project", Owner: "user-1"})
		}
		return nil, fmt.Errorf("unexpected kind: %s", kind)
	})

	ws, err := Open(context.Background(), "ws_123")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ws.ID() != "ws_123" {
		t.Errorf("ID = %q, want ws_123", ws.ID())
	}
	if ws.Meta().Name != "test-project" {
		t.Errorf("Meta.Name = %q, want test-project", ws.Meta().Name)
	}
}

func TestOpenWorkspaceWithBranch(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return json.Marshal(Meta{Name: "proj", Owner: "u1"})
	})

	ws, err := Open(context.Background(), "ws_123", "feature-x")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The workspace should be bound to "feature-x"
	if ws.ID() != "ws_123" {
		t.Errorf("ID = %q, want ws_123", ws.ID())
	}
}

func TestOpenWorkspaceNotInitialized(t *testing.T) {
	defaultTransport = nil
	_, err := Open(context.Background(), "ws_123")
	if err == nil {
		t.Fatal("expected error when not initialized")
	}
}

func TestCreateWorkspace(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSCreate {
			return json.Marshal(struct {
				ID   string `json:"id"`
				Meta Meta   `json:"meta"`
			}{ID: "ws_new", Meta: Meta{Name: "new-proj", Owner: "u1"}})
		}
		return nil, fmt.Errorf("unexpected kind: %s", kind)
	})

	ws, err := Create(context.Background(), "/data/project", Meta{Name: "new-proj", Owner: "u1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ws.ID() != "ws_new" {
		t.Errorf("ID = %q, want ws_new", ws.ID())
	}
}

func TestCreateWorkspaceNotInitialized(t *testing.T) {
	defaultTransport = nil
	_, err := Create(context.Background(), "/tmp/x", Meta{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListWorkspaces(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSList {
			return json.Marshal([]RegistryEntry{
				{ID: "ws_1", Meta: Meta{Name: "p1"}},
				{ID: "ws_2", Meta: Meta{Name: "p2"}},
			})
		}
		return nil, fmt.Errorf("unexpected kind: %s", kind)
	})

	entries, err := List(context.Background(), ListOptions{Owner: "u1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("len = %d, want 2", len(entries))
	}
}

func TestListWorkspacesNotInitialized(t *testing.T) {
	defaultTransport = nil
	_, err := List(context.Background(), ListOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteWorkspace(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSDelete {
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected kind: %s", kind)
	})

	err := Delete(context.Background(), "ws_123")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestDeleteWorkspaceNotInitialized(t *testing.T) {
	defaultTransport = nil
	err := Delete(context.Background(), "ws_123")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWorkspaceClose(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return json.Marshal(Meta{Name: "proj"})
	})
	ws, _ := Open(context.Background(), "ws_1")
	if err := ws.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestReadResponseError(t *testing.T) {
	errPayload := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      KindError,
		Data:      []byte(`{"code":"NOT_FOUND","message":"workspace not found"}`),
	}
	respBytes, _ := proto.Marshal(errPayload)
	size := len(respBytes)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}

	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(respBytes)

	_, err := readResponse(&buf, KindWSOpen)
	if err == nil {
		t.Fatal("expected error")
	}
	errResp, ok := err.(*ErrorResponse)
	if !ok {
		t.Fatalf("error type = %T, want *ErrorResponse", err)
	}
	if errResp.Code != "NOT_FOUND" {
		t.Errorf("error code = %q, want NOT_FOUND", errResp.Code)
	}
}

func TestReadResponseUnexpectedKind(t *testing.T) {
	payload := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      "something.else",
		Data:      []byte("{}"),
	}
	respBytes, _ := proto.Marshal(payload)
	size := len(respBytes)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}

	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(respBytes)

	_, err := readResponse(&buf, KindWSOpen)
	if err == nil {
		t.Fatal("expected error for unexpected kind")
	}
}

func TestReadPayloadTooLarge(t *testing.T) {
	// Header claiming 128MB
	header := []byte{0x08, 0x00, 0x00, 0x00}
	buf := bytes.NewReader(header)

	_, err := readPayload(buf)
	if err == nil {
		t.Fatal("expected error for too-large payload")
	}
}

// ---------- Remote workspace method tests ----------

func TestWorkspaceUpdateMeta(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindWSUpdateMeta:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	err := ws.UpdateMeta(context.Background(), Meta{Name: "updated"})
	if err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	if ws.Meta().Name != "updated" {
		t.Errorf("Meta.Name = %q, want updated", ws.Meta().Name)
	}
}

func TestWorkspaceListBranches(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindListBranches:
			return json.Marshal([]BranchInfo{
				{Name: "main", Head: "abc"},
				{Name: "dev", Head: "def"},
			})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	branches, err := ws.ListBranches(context.Background())
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 2 {
		t.Errorf("len = %d, want 2", len(branches))
	}
}

func TestWorkspaceCreateBranch(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindCreateBranch:
			return json.Marshal(BranchInfo{Name: "feature", Head: "abc"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, err := ws.CreateBranch(context.Background(), "feature", "main", nil)
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if br.Name() != "feature" {
		t.Errorf("Name = %q, want feature", br.Name())
	}
}

func TestWorkspaceGetBranch(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "abc123", Message: "init"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, err := ws.GetBranch(context.Background(), "dev")
	if err != nil {
		t.Fatalf("GetBranch: %v", err)
	}
	if br.Name() != "dev" {
		t.Errorf("Name = %q, want dev", br.Name())
	}
}

func TestWorkspaceGetOrCreateBranch(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindGetOrCreateBranch:
			return json.Marshal(BranchInfo{Name: "session-1", Head: "abc"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, err := ws.GetOrCreateBranch(context.Background(), "session-1", "main", nil)
	if err != nil {
		t.Fatalf("GetOrCreateBranch: %v", err)
	}
	if br.Name() != "session-1" {
		t.Errorf("Name = %q, want session-1", br.Name())
	}
}

func TestWorkspaceDeleteBranch(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindDeleteBranch:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	err := ws.DeleteBranch(context.Background(), "old-branch")
	if err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
}

func TestWorkspaceMerge(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindMerge:
			return json.Marshal(MergeResult{CommitID: "merge-abc"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	result, err := ws.Merge(context.Background(), "feature", "main", MergeOptions{Message: "merge"})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if result.CommitID != "merge-abc" {
		t.Errorf("CommitID = %q", result.CommitID)
	}
}

func TestWorkspaceGetCommit(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindWSGetCommit:
			return json.Marshal(CommitInfo{ID: "abc", Message: "init"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	ci, err := ws.GetCommit(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if ci.ID != "abc" {
		t.Errorf("ID = %q, want abc", ci.ID)
	}
}

func TestWorkspaceLoadHistory(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindWSHistory:
			return json.Marshal([]CommitEntry{{CommitInfo: CommitInfo{ID: "c1"}, Branch: "main"}})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	hist, err := ws.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	entries, err := hist.Query(HistoryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("len = %d, want 1", len(entries))
	}
}

func TestWorkspaceGC(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindWSGC:
			return json.Marshal(GCResult{ReclaimedBytes: 1024, ReclaimedBlobs: 5})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	result, err := ws.GC(context.Background(), GCOptions{})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.ReclaimedBytes != 1024 {
		t.Errorf("ReclaimedBytes = %d", result.ReclaimedBytes)
	}
}

func TestWorkspaceMain(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "head-main"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, err := ws.Main(context.Background())
	if err != nil {
		t.Fatalf("Main: %v", err)
	}
	if br.Name() != "main" {
		t.Errorf("Name = %q, want main", br.Name())
	}
}

// ---------- Branch method tests ----------

func TestBranchCommit(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "abc"})
		case KindCommit:
			return json.Marshal(CommitInfo{ID: "new-commit", Message: "test commit"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, _ := ws.GetBranch(context.Background(), "dev")
	ci, err := br.Commit(context.Background(), "test commit")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if ci.ID != "new-commit" {
		t.Errorf("ID = %q", ci.ID)
	}
}

func TestBranchDiff(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "abc"})
		case KindDiff:
			return json.Marshal([]FileDiff{{Path: "a.go", Type: "add"}})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, _ := ws.GetBranch(context.Background(), "dev")
	diffs, err := br.Diff(context.Background(), "c1", "c2")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diffs) != 1 {
		t.Errorf("len = %d, want 1", len(diffs))
	}
}

func TestBranchRollback(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "abc"})
		case KindRollback:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, _ := ws.GetBranch(context.Background(), "dev")
	err := br.Rollback(context.Background(), "c1")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
}

func TestBranchWriteLock(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "abc"})
		case KindBranchAcquireW, KindBranchReleaseW:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, _ := ws.GetBranch(context.Background(), "dev")

	if err := br.AcquireWrite(context.Background(), "agent-1"); err != nil {
		t.Fatalf("AcquireWrite: %v", err)
	}
	if err := br.ReleaseWrite(context.Background()); err != nil {
		t.Fatalf("ReleaseWrite: %v", err)
	}
}

func TestBranchUpdateMeta(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "abc"})
		case KindBranchUpdateMeta:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, _ := ws.GetBranch(context.Background(), "dev")
	err := br.UpdateMeta(context.Background(), map[string]any{"key": "val"})
	if err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
}

func TestBranchHead(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "latest-head", Message: "latest"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, _ := ws.GetBranch(context.Background(), "dev")
	ci, err := br.Head(context.Background())
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if ci.ID != "latest-head" {
		t.Errorf("ID = %q", ci.ID)
	}
}

// ---------- File operation tests ----------

func TestBranchFSRemove(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindRemove:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	err := ws.Remove("tmp/cache.dat")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestBranchFSRemoveAll(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindRemoveAll:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	err := ws.RemoveAll("tmp/")
	if err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
}

func TestBranchFSRename(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindRename:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	err := ws.Rename("draft.md", "final.md")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
}

func TestBranchFSMkdirAll(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindMkdirAll:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	err := ws.MkdirAll("output/images", 0755)
	if err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
}

func TestBranchFSSymlink(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindSymlink:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	err := ws.Symlink("real.conf", "link.conf")
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
}

func TestRemoteFileInfo(t *testing.T) {
	fi := &remoteFileInfo{name: "test.go", size: 100, mode: 0644}
	if fi.Name() != "test.go" {
		t.Errorf("Name = %q", fi.Name())
	}
	if fi.Size() != 100 {
		t.Errorf("Size = %d", fi.Size())
	}
	if fi.Mode() != 0644 {
		t.Errorf("Mode = %v", fi.Mode())
	}
	if fi.IsDir() {
		t.Error("IsDir should be false")
	}
	if fi.Sys() != nil {
		t.Error("Sys should be nil")
	}
}

func TestRemoteFileReadClose(t *testing.T) {
	ms := &mockStream{
		Reader: bytes.NewReader([]byte("file content")),
		buf:    &bytes.Buffer{},
	}
	f := &remoteFile{name: "test.txt", stream: ms}

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "file content" {
		t.Errorf("content = %q", data)
	}

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Name() != "test.txt" {
		t.Errorf("Name = %q", fi.Name())
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteWriterStreamClose(t *testing.T) {
	// Build a create.result framed response for the half-close path
	respPayload := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      KindCreate + SuffixResult,
		Data:      []byte("{}"),
	}
	respBytes, _ := proto.Marshal(respPayload)
	size := len(respBytes)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	var respBuf bytes.Buffer
	respBuf.Write(header)
	respBuf.Write(respBytes)

	ms := &mockStream{
		Reader: bytes.NewReader(respBuf.Bytes()),
		buf:    &bytes.Buffer{},
	}
	w := &remoteWriter{stream: ms}
	n, err := w.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !ms.writeClosed {
		t.Error("CloseWrite was not called")
	}
}

func TestRemoteFileHeavyConn(t *testing.T) {
	s, c := net.Pipe()
	go func() {
		s.Write([]byte("heavy data"))
		s.Close()
	}()

	f := &remoteFile{name: "big.bin", heavyConn: c}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "heavy data" {
		t.Errorf("content = %q", data)
	}
	f.Close()
}

func TestRemoteWriterHeavyConn(t *testing.T) {
	s, c := net.Pipe()
	go func() {
		buf := make([]byte, 100)
		n, _ := s.Read(buf)
		if string(buf[:n]) != "upload" {
			panic("unexpected data")
		}
		s.Close()
	}()

	w := &remoteWriter{heavyConn: c}
	n, err := w.Write([]byte("upload"))
	if err != nil || n != 6 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	w.Close()
}

// ---------- History tests ----------

func TestHistoryFileHistory(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSFileHistory:
			return json.Marshal([]FileChange{{CommitID: "c1", Path: "a.go", Type: "add"}})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	h := &remoteHistory{wsID: "ws_1"}
	changes, err := h.FileHistory("a.go", FileHistoryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	if len(changes) != 1 {
		t.Errorf("len = %d, want 1", len(changes))
	}
}

func TestHistoryBranchLog(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSBranchLog:
			return json.Marshal([]CommitEntry{{CommitInfo: CommitInfo{ID: "c1"}, Branch: "main"}})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	h := &remoteHistory{wsID: "ws_1"}
	entries, err := h.BranchLog("main", LogOptions{Limit: 10})
	if err != nil {
		t.Fatalf("BranchLog: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("len = %d, want 1", len(entries))
	}
}

// ---------- remoteBranchFS.Stat / ReadDir tests ----------

func TestBranchFSStat(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindStat:
			return json.Marshal(map[string]any{
				"name": "hello.txt", "size": 42, "mode": 0644,
				"mod_time": int64(1700000000), "is_dir": false,
			})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	info, err := fs.Stat(ws, "hello.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Name() != "hello.txt" {
		t.Errorf("Name = %q", info.Name())
	}
	if info.Size() != 42 {
		t.Errorf("Size = %d", info.Size())
	}
	if info.IsDir() {
		t.Error("IsDir should be false")
	}
	if info.ModTime().Unix() != 1700000000 {
		t.Errorf("ModTime = %v", info.ModTime())
	}
}

func TestBranchFSReadDir(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindReadDir:
			return json.Marshal([]map[string]any{
				{"name": "src", "is_dir": true, "type": "d---------"},
				{"name": "main.go", "is_dir": false, "type": "----------"},
			})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	entries, err := fs.ReadDir(ws, ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2", len(entries))
	}
	if entries[0].Name() != "src" || !entries[0].IsDir() {
		t.Errorf("entry[0] = %q isDir=%v", entries[0].Name(), entries[0].IsDir())
	}
	if entries[1].Name() != "main.go" || entries[1].IsDir() {
		t.Errorf("entry[1] = %q isDir=%v", entries[1].Name(), entries[1].IsDir())
	}
	// Test Type()
	if entries[0].Type() != fs.ModeDir {
		t.Errorf("dir Type = %v, want ModeDir", entries[0].Type())
	}
	// Test Info()
	fi, err := entries[0].Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if fi.Name() != "src" {
		t.Errorf("Info.Name = %q", fi.Name())
	}
}

// ---------- remoteBranchFS.Open (streaming) ----------

func TestBranchFSOpenLight(t *testing.T) {
	mt := &mockTransport{
		heavyErr: fmt.Errorf("no heavy"),
		rawHandler: func(kind string, data []byte) ([]byte, error) {
			if kind == KindOpen {
				return []byte("file-content-here"), nil
			}
			return nil, fmt.Errorf("not raw")
		},
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(Meta{Name: "proj"})
		},
	}
	Init(mt, "test-node", nil)

	ws, _ := Open(context.Background(), "ws_1")
	f, err := ws.Open("readme.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(f)
	if string(data) != "file-content-here" {
		t.Errorf("content = %q", data)
	}
	f.Close()
}

func TestBranchFSOpenHeavyFallback(t *testing.T) {
	mt := &mockTransport{
		heavyErr: fmt.Errorf("no heavy"),
		rawHandler: func(kind string, data []byte) ([]byte, error) {
			if kind == KindOpen {
				return []byte("light-data"), nil
			}
			return nil, fmt.Errorf("not raw")
		},
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(Meta{Name: "proj"})
		},
	}
	Init(mt, "test-node", nil)

	ws, _ := Open(context.Background(), "ws_1")
	// .png triggers Heavy channel, but no heavy connection → falls back to Light
	f, err := ws.Open("image.png")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(f)
	if string(data) != "light-data" {
		t.Errorf("content = %q", data)
	}
	f.Close()
}

func TestBranchFSOpenHeavyConnection(t *testing.T) {
	s, c := net.Pipe()
	go func() {
		// Read the payload header (command frame) written by openHeavy
		hdr := make([]byte, 4)
		io.ReadFull(s, hdr)
		size := int(hdr[0])<<24 | int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
		discard := make([]byte, size)
		io.ReadFull(s, discard)
		// Write file content
		s.Write([]byte("heavy-file-data"))
		s.Close()
	}()

	mt := &mockTransport{
		heavyConns: []net.Conn{c},
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(Meta{Name: "proj"})
		},
	}
	Init(mt, "test-node", nil)

	ws, _ := Open(context.Background(), "ws_1")
	f, err := ws.Open("archive.zip") // .zip → Heavy
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(f)
	if string(data) != "heavy-file-data" {
		t.Errorf("content = %q", data)
	}
	f.Close()
	// Verify connection was returned to pool
	if len(mt.heavyReturned) != 1 {
		t.Errorf("heavyReturned = %d, want 1", len(mt.heavyReturned))
	}
}

// ---------- remoteBranchFS.Create ----------

func TestBranchFSCreateLight(t *testing.T) {
	mt := &mockTransport{
		heavyErr: fmt.Errorf("no heavy"),
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(Meta{Name: "proj"})
		},
	}
	Init(mt, "test-node", nil)

	ws, _ := Open(context.Background(), "ws_1")
	w, err := ws.Create("output.json")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	n, err := w.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	w.Close()
}

func TestBranchFSCreateHeavyConnection(t *testing.T) {
	s, c := net.Pipe()
	received := make(chan string, 1)
	go func() {
		// Read command frame header
		hdr := make([]byte, 4)
		io.ReadFull(s, hdr)
		size := int(hdr[0])<<24 | int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
		discard := make([]byte, size)
		io.ReadFull(s, discard)
		// Read the written data
		data, _ := io.ReadAll(s)
		received <- string(data)
	}()

	mt := &mockTransport{
		heavyConns: []net.Conn{c},
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(Meta{Name: "proj"})
		},
	}
	Init(mt, "test-node", nil)

	ws, _ := Open(context.Background(), "ws_1")
	w, err := ws.Create("big.bin") // .bin → Heavy
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w.Write([]byte("binary-data"))
	w.Close()

	if len(mt.heavyReturned) != 1 {
		t.Errorf("heavyReturned = %d, want 1", len(mt.heavyReturned))
	}
}

// ---------- remoteWriter.Close with HalfCloser ----------

func TestRemoteWriterCloseWithHalfCloser(t *testing.T) {
	// Build a create.result framed response
	respPayload := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      KindCreate + SuffixResult,
		Data:      []byte("{}"),
	}
	respBytes, _ := proto.Marshal(respPayload)
	size := len(respBytes)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	var respBuf bytes.Buffer
	respBuf.Write(header)
	respBuf.Write(respBytes)

	ms := &mockStream{Reader: bytes.NewReader(respBuf.Bytes()), buf: &bytes.Buffer{}}
	w := &remoteWriter{stream: ms}

	w.Write([]byte("data"))
	err := w.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !ms.writeClosed {
		t.Error("CloseWrite was not called")
	}
	if !ms.closed {
		t.Error("stream was not fully closed")
	}
}

func TestRemoteWriterCloseHalfCloseError(t *testing.T) {
	ms := &mockStream{
		Reader:       bytes.NewReader(nil),
		buf:          &bytes.Buffer{},
		halfCloseErr: fmt.Errorf("half-close failed"),
	}
	w := &remoteWriter{stream: ms}
	err := w.Close()
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------- remoteBranchFS.Watch + remoteWatcher ----------

func TestBranchFSWatch(t *testing.T) {
	// Build watch.ack + one watch.event
	var buf bytes.Buffer

	ackPayload := &pb.Payload{Namespace: NamespaceWorkspace, Kind: "watch.ack", Data: []byte("{}")}
	ackBytes, _ := proto.Marshal(ackPayload)
	ackHeader := []byte{byte(len(ackBytes) >> 24), byte(len(ackBytes) >> 16), byte(len(ackBytes) >> 8), byte(len(ackBytes))}
	buf.Write(ackHeader)
	buf.Write(ackBytes)

	evData, _ := json.Marshal(WatchEvent{Type: WatchCreate, Path: "new.txt"})
	evPayload := &pb.Payload{Namespace: NamespaceWorkspace, Kind: "watch.event", Data: evData}
	evBytes, _ := proto.Marshal(evPayload)
	evHeader := []byte{byte(len(evBytes) >> 24), byte(len(evBytes) >> 16), byte(len(evBytes) >> 8), byte(len(evBytes))}
	buf.Write(evHeader)
	buf.Write(evBytes)

	mt := &mockTransport{
		heavyErr: fmt.Errorf("no heavy"),
		rawHandler: func(kind string, data []byte) ([]byte, error) {
			if kind == KindWatch {
				return buf.Bytes(), nil
			}
			return nil, fmt.Errorf("not raw")
		},
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(Meta{Name: "proj"})
		},
	}
	Init(mt, "test-node", nil)

	ws, _ := Open(context.Background(), "ws_1")
	watcher, err := ws.Watch([]string{"*.go"})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer watcher.Close()

	ev := <-watcher.Events()
	if ev.Type != WatchCreate || ev.Path != "new.txt" {
		t.Errorf("event = %+v", ev)
	}
}

func TestRemoteWatcherClose(t *testing.T) {
	ms := &mockStream{Reader: bytes.NewReader(nil), buf: &bytes.Buffer{}}
	w := newRemoteWatcher(ms)

	// Close should be safe to call
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Double close should be safe
	if err := w.Close(); err != nil {
		t.Fatalf("Close2: %v", err)
	}
	// Events channel should be closed after recvLoop exits
	_, ok := <-w.Events()
	if ok {
		t.Error("events channel should be closed")
	}
}

func TestRemoteWatcherRecvLoopSkipsNonEvent(t *testing.T) {
	var buf bytes.Buffer
	// Write a non-event frame (should be skipped)
	otherPayload := &pb.Payload{Kind: "something.else", Data: []byte("{}")}
	otherBytes, _ := proto.Marshal(otherPayload)
	hdr := []byte{byte(len(otherBytes) >> 24), byte(len(otherBytes) >> 16), byte(len(otherBytes) >> 8), byte(len(otherBytes))}
	buf.Write(hdr)
	buf.Write(otherBytes)

	// Write a real event
	evData, _ := json.Marshal(WatchEvent{Type: WatchWrite, Path: "a.go"})
	evPayload := &pb.Payload{Kind: "watch.event", Data: evData}
	evBytes, _ := proto.Marshal(evPayload)
	evHdr := []byte{byte(len(evBytes) >> 24), byte(len(evBytes) >> 16), byte(len(evBytes) >> 8), byte(len(evBytes))}
	buf.Write(evHdr)
	buf.Write(evBytes)

	ms := &mockStream{Reader: bytes.NewReader(buf.Bytes()), buf: &bytes.Buffer{}}
	w := newRemoteWatcher(ms)
	defer w.Close()

	ev := <-w.Events()
	if ev.Type != WatchWrite || ev.Path != "a.go" {
		t.Errorf("event = %+v", ev)
	}
}

// ---------- writePayload ----------

func TestWritePayload(t *testing.T) {
	msg := &pb.Payload{Namespace: "test", Kind: "hello", Data: []byte("world")}
	var buf bytes.Buffer
	if err := writePayload(&buf, msg); err != nil {
		t.Fatalf("writePayload: %v", err)
	}
	// Read it back
	got, err := readPayload(&buf)
	if err != nil {
		t.Fatalf("readPayload: %v", err)
	}
	if got.Namespace != "test" || got.Kind != "hello" || string(got.Data) != "world" {
		t.Errorf("got = %+v", got)
	}
}

// ---------- remoteDirEntry ----------

func TestRemoteDirEntry(t *testing.T) {
	de := &remoteDirEntry{name: "src", isDir: true, mode: fs.ModeDir}
	if de.Name() != "src" {
		t.Errorf("Name = %q", de.Name())
	}
	if !de.IsDir() {
		t.Error("IsDir should be true")
	}
	if de.Type() != fs.ModeDir {
		t.Errorf("Type = %v", de.Type())
	}
	fi, err := de.Info()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Name() != "src" || !fi.IsDir() {
		t.Errorf("Info = %+v", fi)
	}

	// File entry
	de2 := &remoteDirEntry{name: "main.go", isDir: false, mode: 0}
	if de2.IsDir() {
		t.Error("file should not be dir")
	}
}

// ---------- remoteFileInfo.ModTime ----------

func TestRemoteFileInfoModTime(t *testing.T) {
	fi := &remoteFileInfo{name: "f.txt", modTime: time.Unix(1700000000, 0)}
	if fi.ModTime().Unix() != 1700000000 {
		t.Errorf("ModTime = %v", fi.ModTime())
	}
}

// ---------- Branch.FS() / Meta() ----------

func TestBranchFS(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			return json.Marshal(CommitInfo{ID: "abc"})
		case KindRemove:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws, _ := Open(context.Background(), "ws_1")
	br, _ := ws.GetBranch(context.Background(), "dev")

	// FS() should return a WorkspaceFS
	bfs := br.FS()
	if bfs == nil {
		t.Fatal("FS() returned nil")
	}

	// Meta() should work
	_ = br.Meta()

	// Use FS for an operation
	err := bfs.Remove("temp.txt")
	if err != nil {
		t.Fatalf("FS().Remove: %v", err)
	}
}

// ---------- Error path tests ----------

func TestWorkspaceMethodsRoundTripError(t *testing.T) {
	// Mock that returns success for Open, then errors for everything else
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSOpen || kind == KindBranchHead {
			if kind == KindWSOpen {
				return json.Marshal(Meta{Name: "proj"})
			}
			return json.Marshal(CommitInfo{ID: "abc"})
		}
		return nil, fmt.Errorf("transport error")
	})

	ws, _ := Open(context.Background(), "ws_1")
	ctx := context.Background()

	// Workspace methods
	if _, err := ws.ListBranches(ctx); err == nil {
		t.Error("ListBranches should fail")
	}
	if _, err := ws.CreateBranch(ctx, "x", "main", nil); err == nil {
		t.Error("CreateBranch should fail")
	}
	// GetBranch uses KindBranchHead which the mock lets succeed above
	// Test separately below
	if _, err := ws.GetOrCreateBranch(ctx, "x", "main", nil); err == nil {
		t.Error("GetOrCreateBranch should fail")
	}
	if err := ws.DeleteBranch(ctx, "x"); err == nil {
		t.Error("DeleteBranch should fail")
	}
	if _, err := ws.Merge(ctx, "a", "b", MergeOptions{}); err == nil {
		t.Error("Merge should fail")
	}
	if _, err := ws.GetCommit(ctx, "c1"); err == nil {
		t.Error("GetCommit should fail")
	}
	if _, err := ws.GC(ctx, GCOptions{}); err == nil {
		t.Error("GC should fail")
	}
	if err := ws.UpdateMeta(ctx, Meta{Name: "x"}); err == nil {
		t.Error("UpdateMeta should fail")
	}

	// GetBranch error (uses KindBranchHead — need special mock)
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSOpen {
			return json.Marshal(Meta{Name: "proj"})
		}
		return nil, fmt.Errorf("transport error")
	})
	ws1b, _ := Open(ctx, "ws_1b")
	if _, err := ws1b.GetBranch(ctx, "x"); err == nil {
		t.Error("GetBranch should fail")
	}

	// Branch methods (via GetBranch → uses a different error mock)
	branchHeadCalls := 0
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSOpen:
			return json.Marshal(Meta{Name: "proj"})
		case KindBranchHead:
			branchHeadCalls++
			if branchHeadCalls <= 1 {
				return json.Marshal(CommitInfo{ID: "abc"}) // for GetBranch
			}
			return nil, fmt.Errorf("transport error") // for Head
		default:
			return nil, fmt.Errorf("transport error")
		}
	})

	ws2, _ := Open(ctx, "ws_2")
	br, _ := ws2.GetBranch(ctx, "dev")

	if _, err := br.Commit(ctx, "msg"); err == nil {
		t.Error("Commit should fail")
	}
	if _, err := br.Diff(ctx, "a", "b"); err == nil {
		t.Error("Diff should fail")
	}
	if err := br.Rollback(ctx, "c1"); err == nil {
		t.Error("Rollback should fail")
	}
	if err := br.AcquireWrite(ctx, "h"); err == nil {
		t.Error("AcquireWrite should fail")
	}
	if err := br.ReleaseWrite(ctx); err == nil {
		t.Error("ReleaseWrite should fail")
	}
	if err := br.UpdateMeta(ctx, map[string]any{"k": "v"}); err == nil {
		t.Error("Branch.UpdateMeta should fail")
	}
	if _, err := br.Head(ctx); err == nil {
		t.Error("Head should fail")
	}
}

func TestWorkspaceMethodsUnmarshalError(t *testing.T) {
	ctx := context.Background()

	// Returns invalid JSON for non-essential kinds
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSOpen {
			return json.Marshal(Meta{Name: "proj"})
		}
		return []byte("not-valid-json{{{"), nil
	})

	ws, _ := Open(ctx, "ws_1")
	if _, err := ws.ListBranches(ctx); err == nil {
		t.Error("ListBranches should fail on bad JSON")
	}
	if _, err := ws.CreateBranch(ctx, "x", "main", nil); err == nil {
		t.Error("CreateBranch should fail on bad JSON")
	}
	if _, err := ws.GetOrCreateBranch(ctx, "x", "main", nil); err == nil {
		t.Error("GetOrCreateBranch should fail on bad JSON")
	}
	if _, err := ws.Merge(ctx, "a", "b", MergeOptions{}); err == nil {
		t.Error("Merge should fail on bad JSON")
	}
	if _, err := ws.GetCommit(ctx, "c1"); err == nil {
		t.Error("GetCommit should fail on bad JSON")
	}
	if _, err := ws.GC(ctx, GCOptions{}); err == nil {
		t.Error("GC should fail on bad JSON")
	}

	// Branch method unmarshal errors
	headCalls := 0
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSOpen {
			return json.Marshal(Meta{Name: "proj"})
		}
		if kind == KindBranchHead {
			headCalls++
			if headCalls <= 2 {
				return json.Marshal(CommitInfo{ID: "abc"}) // for GetBranch calls
			}
			return []byte("bad-json"), nil // for Head
		}
		return []byte("bad-json"), nil
	})
	ws2, _ := Open(ctx, "ws_2")
	br, _ := ws2.GetBranch(ctx, "dev")
	if _, err := br.Commit(ctx, "m"); err == nil {
		t.Error("Commit should fail on bad JSON")
	}
	if _, err := br.Diff(ctx, "a", "b"); err == nil {
		t.Error("Diff should fail on bad JSON")
	}
	br2, _ := ws2.GetBranch(ctx, "dev2")
	if _, err := br2.Head(ctx); err == nil {
		t.Error("Head should fail on bad JSON")
	}
}

func TestPackageFunctionsError(t *testing.T) {
	// Open with bad response JSON
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return []byte("bad-json"), nil
	})
	if _, err := Open(context.Background(), "ws_1"); err == nil {
		t.Error("Open with bad JSON should fail")
	}

	// Create with bad response JSON
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return []byte("bad-json"), nil
	})
	if _, err := Create(context.Background(), "/p", Meta{}); err == nil {
		t.Error("Create with bad JSON should fail")
	}

	// List with bad response JSON
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return []byte("bad-json"), nil
	})
	if _, err := List(context.Background(), ListOptions{}); err == nil {
		t.Error("List with bad JSON should fail")
	}

	// roundTrip error
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return nil, fmt.Errorf("fail")
	})
	if _, err := Open(context.Background(), "ws_1"); err == nil {
		t.Error("Open with transport error should fail")
	}
	if _, err := Create(context.Background(), "/p", Meta{}); err == nil {
		t.Error("Create with transport error should fail")
	}
	if _, err := List(context.Background(), ListOptions{}); err == nil {
		t.Error("List with transport error should fail")
	}
	if err := Delete(context.Background(), "ws_1"); err == nil {
		t.Error("Delete with transport error should fail")
	}
}

func TestFSOperationsError(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindWSOpen {
			return json.Marshal(Meta{Name: "proj"})
		}
		return nil, fmt.Errorf("transport error")
	})

	ws, _ := Open(context.Background(), "ws_1")

	if err := ws.Remove("x"); err == nil {
		t.Error("Remove should fail")
	}
	if err := ws.RemoveAll("x"); err == nil {
		t.Error("RemoveAll should fail")
	}
	if err := ws.Rename("a", "b"); err == nil {
		t.Error("Rename should fail")
	}
	if err := ws.MkdirAll("d", 0755); err == nil {
		t.Error("MkdirAll should fail")
	}
	if err := ws.Symlink("a", "b"); err == nil {
		t.Error("Symlink should fail")
	}

	// Stat error
	if _, err := fs.Stat(ws, "x"); err == nil {
		t.Error("Stat should fail")
	}
	// ReadDir error
	if _, err := fs.ReadDir(ws, "."); err == nil {
		t.Error("ReadDir should fail")
	}
}

func TestReadPayloadShortHeader(t *testing.T) {
	// Only 2 bytes (need 4)
	buf := bytes.NewReader([]byte{0x00, 0x01})
	_, err := readPayload(buf)
	if err == nil {
		t.Error("expected error for short header")
	}
}

func TestReadResponseUnparseableError(t *testing.T) {
	// Error payload with bad JSON in data
	errPayload := &pb.Payload{Kind: KindError, Data: []byte("not-json")}
	b, _ := proto.Marshal(errPayload)
	header := []byte{byte(len(b) >> 24), byte(len(b) >> 16), byte(len(b) >> 8), byte(len(b))}
	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(b)

	_, err := readResponse(&buf, KindWSOpen)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ErrorResponse); ok {
		t.Fatal("should NOT be *ErrorResponse for unparseable data")
	}
}

func TestWritePayloadError(t *testing.T) {
	msg := &pb.Payload{Namespace: "test", Kind: "k", Data: []byte("d")}
	// Write to a writer that fails
	w := &failWriter{}
	err := writePayload(w, msg)
	if err == nil {
		t.Error("expected error")
	}
}

type failWriter struct{}

func (fw *failWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("write failed")
}

func TestHistoryQueryError(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return nil, fmt.Errorf("fail")
	})
	h := &remoteHistory{wsID: "ws_1"}
	if _, err := h.Query(HistoryOptions{}); err == nil {
		t.Error("Query should fail")
	}
	if _, err := h.FileHistory("a.go", FileHistoryOptions{}); err == nil {
		t.Error("FileHistory should fail")
	}
	if _, err := h.BranchLog("main", LogOptions{}); err == nil {
		t.Error("BranchLog should fail")
	}
}

func TestHistoryUnmarshalError(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return []byte("bad-json"), nil
	})
	h := &remoteHistory{wsID: "ws_1"}
	if _, err := h.Query(HistoryOptions{}); err == nil {
		t.Error("Query should fail on bad JSON")
	}
	if _, err := h.FileHistory("a.go", FileHistoryOptions{}); err == nil {
		t.Error("FileHistory should fail on bad JSON")
	}
	if _, err := h.BranchLog("main", LogOptions{}); err == nil {
		t.Error("BranchLog should fail on bad JSON")
	}
}

func TestWatchOpenError(t *testing.T) {
	mt := &mockTransport{
		heavyErr: fmt.Errorf("no heavy"),
		handler:  nil, // → OpenLightStream fails
	}
	Init(mt, "test-node", nil)

	fsImpl := &remoteBranchFS{wsID: "ws_1", branch: "main"}
	if _, err := fsImpl.Watch([]string{"*.go"}); err == nil {
		t.Error("Watch should fail when stream open fails")
	}
}

func TestStatReadDirUnmarshalError(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return []byte("bad"), nil
	})
	fsImpl := &remoteBranchFS{wsID: "ws_1", branch: "main"}
	if _, err := fsImpl.Stat("f.go"); err == nil {
		t.Error("Stat should fail on bad JSON")
	}
	if _, err := fsImpl.ReadDir("."); err == nil {
		t.Error("ReadDir should fail on bad JSON")
	}
}

func TestOpenHeavyWritePayloadError(t *testing.T) {
	// Create a heavy conn that's already closed → writePayload fails → fallback to Light
	s, c := net.Pipe()
	s.Close() // close server side immediately

	mt := &mockTransport{
		heavyConns: []net.Conn{c},
		rawHandler: func(kind string, data []byte) ([]byte, error) {
			if kind == KindOpen {
				return []byte("fallback-content"), nil
			}
			return nil, fmt.Errorf("not raw")
		},
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(Meta{Name: "proj"})
		},
	}
	Init(mt, "test-node", nil)

	fsImpl := &remoteBranchFS{wsID: "ws_1", branch: "main"}
	f, err := fsImpl.Open("big.bin") // .bin → Heavy → fails → Light
	if err != nil {
		t.Fatalf("Open fallback: %v", err)
	}
	data, _ := io.ReadAll(f)
	if string(data) != "fallback-content" {
		t.Errorf("content = %q", data)
	}
	f.Close()
}

func TestCreateHeavyWritePayloadError(t *testing.T) {
	s, c := net.Pipe()
	s.Close() // closed → writePayload fails → fallback to Light

	mt := &mockTransport{
		heavyConns: []net.Conn{c},
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(Meta{Name: "proj"})
		},
	}
	Init(mt, "test-node", nil)

	fsImpl := &remoteBranchFS{wsID: "ws_1", branch: "main"}
	w, err := fsImpl.Create("big.bin") // .bin → Heavy → fails → Light
	if err != nil {
		t.Fatalf("Create fallback: %v", err)
	}
	w.Write([]byte("data"))
	w.Close()
}

func TestCreateLightStreamError(t *testing.T) {
	mt := &mockTransport{
		heavyErr: fmt.Errorf("no heavy"),
		handler:  nil, // → OpenLightStream fails
	}
	Init(mt, "test-node", nil)

	fsImpl := &remoteBranchFS{wsID: "ws_1", branch: "main"}
	if _, err := fsImpl.Create("test.txt"); err == nil {
		t.Error("Create should fail when stream open fails")
	}
}

func TestInitWithPolicy(t *testing.T) {
	mt := &mockTransport{heavyErr: fmt.Errorf("no heavy"), handler: func(kind string, data []byte) ([]byte, error) {
		return json.Marshal(Meta{Name: "proj"})
	}}
	customPolicy := &ChannelPolicy{SizeThreshold: 512, TextExts: []string{".txt"}, BinaryExts: []string{".bin"}}
	Init(mt, "node-1", customPolicy)

	if defaultPolicy.SizeThreshold != 512 {
		t.Errorf("SizeThreshold = %d, want 512", defaultPolicy.SizeThreshold)
	}
}

func TestOpenLightStreamError(t *testing.T) {
	mt := &mockTransport{
		heavyErr: fmt.Errorf("no heavy"),
		handler:  nil, // nil handler → OpenLightStream fails
	}
	Init(mt, "test-node", nil)

	if _, err := Open(context.Background(), "ws_1"); err == nil {
		t.Error("should fail with nil handler")
	}
}

// ---------- Interface compliance ----------

var _ Workspace = (*remoteWorkspace)(nil)
var _ Branch = (*remoteBranch)(nil)
var _ WorkspaceFS = (*remoteBranchFS)(nil)
var _ fs.StatFS = (*remoteBranchFS)(nil)
var _ fs.ReadDirFS = (*remoteBranchFS)(nil)
var _ fs.StatFS = (*remoteWorkspace)(nil)
var _ fs.ReadDirFS = (*remoteWorkspace)(nil)
var _ History = (*remoteHistory)(nil)
var _ fs.File = (*remoteFile)(nil)
var _ fs.FileInfo = (*remoteFileInfo)(nil)
