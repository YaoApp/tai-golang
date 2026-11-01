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
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// ---------- Mock transport backend ----------

type mockBackend struct {
	mu            sync.Mutex
	handler       func(kind string, data []byte) ([]byte, error)
	rawHandler    func(kind string, data []byte) ([]byte, error)
	streams       []*mockStream
	heavyErr      error
	heavyConns    []net.Conn
	heavyReturned []net.Conn
}

func (m *mockBackend) OpenLightStream(_ context.Context, _ string, firstFrame *pb.Payload) (io.ReadWriteCloser, error) {
	m.mu.Lock()
	h := m.handler
	rh := m.rawHandler
	m.mu.Unlock()

	if rh != nil {
		raw, err := rh(firstFrame.Kind, firstFrame.Data)
		if err == nil && raw != nil {
			ms := &mockStream{Reader: bytes.NewReader(raw), buf: &bytes.Buffer{}}
			m.mu.Lock()
			m.streams = append(m.streams, ms)
			m.mu.Unlock()
			return ms, nil
		}
	}

	if h == nil {
		return nil, fmt.Errorf("no handler configured")
	}

	respData, err := h(firstFrame.Kind, firstFrame.Data)
	if err != nil {
		return nil, err
	}

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

func (m *mockBackend) HeavyPick(_ string) (net.Conn, error) {
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

func (m *mockBackend) HeavyReturn(conn net.Conn) {
	m.mu.Lock()
	m.heavyReturned = append(m.heavyReturned, conn)
	m.mu.Unlock()
}

func (m *mockBackend) HeavyRemove(conn net.Conn) {
	conn.Close()
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

const testNodeID = "test-node"

func setupMock(handler func(kind string, data []byte) ([]byte, error)) *mockBackend {
	mb := &mockBackend{handler: handler, heavyErr: fmt.Errorf("no heavy")}
	transport.SetTestBackend(mb)
	return mb
}

// openTestWS creates a remoteWorkspace for testing.
func openTestWS(t *testing.T, wsID string, handler func(kind string, data []byte) ([]byte, error)) types.Workspace {
	t.Helper()
	mb := &mockBackend{heavyErr: fmt.Errorf("no heavy")}
	mb.handler = handler
	transport.SetTestBackend(mb)
	return NewRemoteWorkspace(testNodeID, wsID, "main", types.Meta{Name: "proj"}, nil)
}

// ---------- Tests ----------

func TestNewRemoteWorkspace(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		return json.Marshal(types.Meta{Name: "test-project", Owner: "user-1"})
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_123", "main", types.Meta{Name: "test-project", Owner: "user-1"}, nil)
	if ws.ID() != "ws_123" {
		t.Errorf("ID = %q, want ws_123", ws.ID())
	}
	if ws.Meta().Name != "test-project" {
		t.Errorf("Meta.Name = %q, want test-project", ws.Meta().Name)
	}
}

func TestWorkspaceClose(t *testing.T) {
	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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

	_, err := ReadResponse(&buf, KindWSOpen)
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

	_, err := ReadResponse(&buf, KindWSOpen)
	if err == nil {
		t.Fatal("expected error for unexpected kind")
	}
}

func TestReadPayloadTooLarge(t *testing.T) {
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
		case KindWSUpdateMeta:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	err := ws.UpdateMeta(context.Background(), types.Meta{Name: "updated"})
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
		case KindListBranches:
			return json.Marshal([]types.BranchInfo{
				{Name: "main", Head: "abc"},
				{Name: "dev", Head: "def"},
			})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindCreateBranch:
			return json.Marshal(types.BranchInfo{Name: "feature", Head: "abc"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindBranchHead:
			return json.Marshal(types.CommitInfo{ID: "abc123", Message: "init"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindGetOrCreateBranch:
			return json.Marshal(types.BranchInfo{Name: "session-1", Head: "abc"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindDeleteBranch:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	err := ws.DeleteBranch(context.Background(), "old-branch")
	if err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
}

func TestWorkspaceMerge(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindMerge:
			return json.Marshal(types.MergeResult{CommitID: "merge-abc"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	result, err := ws.Merge(context.Background(), "feature", "main", types.MergeOptions{Message: "merge"})
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
		case KindWSGetCommit:
			return json.Marshal(types.CommitInfo{ID: "abc", Message: "init"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindWSHistory:
			return json.Marshal([]types.CommitEntry{{CommitInfo: types.CommitInfo{ID: "c1"}, Branch: "main"}})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	hist, err := ws.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	entries, err := hist.Query(types.HistoryOptions{Limit: 10})
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
		case KindWSGC:
			return json.Marshal(types.GCResult{ReclaimedBytes: 1024, ReclaimedBlobs: 5})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	result, err := ws.GC(context.Background(), types.GCOptions{})
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
		case KindBranchHead:
			return json.Marshal(types.CommitInfo{ID: "head-main"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindBranchHead:
			return json.Marshal(types.CommitInfo{ID: "abc"})
		case KindCommit:
			return json.Marshal(types.CommitInfo{ID: "new-commit", Message: "test commit"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindBranchHead:
			return json.Marshal(types.CommitInfo{ID: "abc"})
		case KindDiff:
			return json.Marshal([]types.FileDiff{{Path: "a.go", Type: "add"}})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindBranchHead:
			return json.Marshal(types.CommitInfo{ID: "abc"})
		case KindRollback:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	br, _ := ws.GetBranch(context.Background(), "dev")
	err := br.Rollback(context.Background(), "c1")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
}

func TestBranchWriteLock(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindBranchHead:
			return json.Marshal(types.CommitInfo{ID: "abc"})
		case KindBranchAcquireW, KindBranchReleaseW:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindBranchHead:
			return json.Marshal(types.CommitInfo{ID: "abc"})
		case KindBranchUpdateMeta:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	br, _ := ws.GetBranch(context.Background(), "dev")
	err := br.UpdateMeta(context.Background(), map[string]any{"key": "val"})
	if err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
}

func TestBranchHead(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindBranchHead:
			return json.Marshal(types.CommitInfo{ID: "latest-head", Message: "latest"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
		case KindRemove:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	err := ws.Remove("tmp/cache.dat")
	if err != nil {
		t.Fatalf("Remove: %v", err)
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

// ---------- seekableRemoteFile tests ----------

func TestSeekableRemoteFileReadForward(t *testing.T) {
	inner := &mockFsFile{data: []byte("hello world"), pos: 0}
	sf := &seekableRemoteFile{
		bfs: &remoteBranchFS{}, name: "test.txt", size: -1,
		offset: 0, streamOffset: 0, r: inner,
	}
	buf := make([]byte, 5)
	n, err := sf.Read(buf)
	if err != nil || n != 5 || string(buf[:n]) != "hello" {
		t.Fatalf("Read(5) = %q err=%v, want hello", buf[:n], err)
	}
	if sf.offset != 5 || sf.streamOffset != 5 {
		t.Errorf("offset=%d streamOffset=%d, want 5,5", sf.offset, sf.streamOffset)
	}
}

func TestSeekableRemoteFileReadAfterSeek(t *testing.T) {
	opened := 0
	bfs := &remoteBranchFS{nodeID: testNodeID, wsID: "ws1", branch: "main"}

	origOpen := bfs.openInternal
	_ = origOpen

	inner := &mockFsFile{data: []byte("hello world"), pos: 0}
	sf := &seekableRemoteFile{
		bfs: bfs, name: "test.txt", size: 11,
		offset: 0, streamOffset: 0, r: inner,
	}

	// Seek to position 6 — doesn't close stream
	pos, err := sf.Seek(6, io.SeekStart)
	if err != nil || pos != 6 {
		t.Fatalf("Seek(6,Start) = %d err=%v", pos, err)
	}
	if sf.offset != 6 {
		t.Errorf("offset=%d, want 6", sf.offset)
	}
	// r is still the old stream (not closed yet)
	if sf.r == nil {
		t.Error("r should not be nil after Seek")
	}
	_ = opened
}

func TestSeekableRemoteFileSeekStart(t *testing.T) {
	sf := &seekableRemoteFile{size: 100, offset: 50}
	pos, _ := sf.Seek(25, io.SeekStart)
	if pos != 25 {
		t.Errorf("Seek(25, Start) = %d, want 25", pos)
	}
}

func TestSeekableRemoteFileSeekCurrent(t *testing.T) {
	sf := &seekableRemoteFile{size: 100, offset: 30}
	pos, _ := sf.Seek(10, io.SeekCurrent)
	if pos != 40 {
		t.Errorf("Seek(10, Current) = %d, want 40", pos)
	}
}

func TestSeekableRemoteFileSeekEnd(t *testing.T) {
	sf := &seekableRemoteFile{size: 100, offset: 0}
	pos, _ := sf.Seek(-10, io.SeekEnd)
	if pos != 90 {
		t.Errorf("Seek(-10, End) = %d, want 90", pos)
	}
}

func TestSeekableRemoteFileSeekEndUnknownSize(t *testing.T) {
	transport.SetTestBackend(&mockBackend{
		handler: func(kind string, data []byte) ([]byte, error) {
			return json.Marshal(map[string]any{
				"name": "f.txt", "size": int64(200), "mode": 0, "mod_time": 0, "is_dir": false,
			})
		},
		heavyErr: fmt.Errorf("no heavy"),
	})

	bfs := &remoteBranchFS{nodeID: testNodeID, wsID: "ws1", branch: "main"}
	sf := &seekableRemoteFile{bfs: bfs, name: "f.txt", size: -1, offset: 0}

	pos, err := sf.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatalf("Seek(0, End) err=%v", err)
	}
	if pos != 200 {
		t.Errorf("Seek(0, End) = %d, want 200", pos)
	}
	if sf.size != 200 {
		t.Errorf("size=%d, want 200 (populated by Stat)", sf.size)
	}
}

func TestSeekableRemoteFileSeekClampNegative(t *testing.T) {
	sf := &seekableRemoteFile{size: 100, offset: 10}
	pos, _ := sf.Seek(-50, io.SeekStart)
	if pos != 0 {
		t.Errorf("Seek(-50, Start) = %d, want 0 (clamped)", pos)
	}
}

func TestSeekableRemoteFileSeekClampBeyondSize(t *testing.T) {
	sf := &seekableRemoteFile{size: 100, offset: 0}
	pos, _ := sf.Seek(200, io.SeekStart)
	if pos != 100 {
		t.Errorf("Seek(200, Start) = %d, want 100 (clamped to size)", pos)
	}
}

func TestSeekableRemoteFileStatCaches(t *testing.T) {
	calls := 0
	transport.SetTestBackend(&mockBackend{
		handler: func(kind string, data []byte) ([]byte, error) {
			calls++
			return json.Marshal(map[string]any{
				"name": "f.txt", "size": int64(42), "mode": 0, "mod_time": 0, "is_dir": false,
			})
		},
		heavyErr: fmt.Errorf("no heavy"),
	})

	bfs := &remoteBranchFS{nodeID: testNodeID, wsID: "ws1", branch: "main"}
	sf := &seekableRemoteFile{bfs: bfs, name: "f.txt", size: -1, offset: 0}

	fi1, err := sf.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi1.Size() != 42 {
		t.Errorf("Stat size=%d, want 42", fi1.Size())
	}
	if sf.size != 42 {
		t.Errorf("cached size=%d, want 42", sf.size)
	}

	fi2, err := sf.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi2 != fi1 {
		t.Error("second Stat should return cached value")
	}
	if calls != 1 {
		t.Errorf("Stat called backend %d times, want 1", calls)
	}
}

func TestSeekableRemoteFileCloseNormal(t *testing.T) {
	inner := &mockFsFile{data: []byte("hello"), pos: 0}
	sf := &seekableRemoteFile{r: inner}
	if err := sf.Close(); err != nil {
		t.Fatalf("Close err=%v", err)
	}
	if !inner.closed {
		t.Error("inner file not closed")
	}
	if sf.r != nil {
		t.Error("r should be nil after Close")
	}
}

func TestSeekableRemoteFileCloseDouble(t *testing.T) {
	inner := &mockFsFile{data: []byte("hello"), pos: 0}
	sf := &seekableRemoteFile{r: inner}
	sf.Close()
	// Second close should not panic and should return nil
	if err := sf.Close(); err != nil {
		t.Fatalf("double Close err=%v", err)
	}
}

func TestSeekableRemoteFileCloseNilR(t *testing.T) {
	sf := &seekableRemoteFile{r: nil}
	if err := sf.Close(); err != nil {
		t.Fatalf("Close with nil r err=%v", err)
	}
}

// mockFsFile is a minimal fs.File for unit testing.
type mockFsFile struct {
	data   []byte
	pos    int
	closed bool
}

func (f *mockFsFile) Stat() (fs.FileInfo, error) {
	return &remoteFileInfo{name: "mock", size: int64(len(f.data))}, nil
}

func (f *mockFsFile) Read(p []byte) (int, error) {
	if f.pos >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *mockFsFile) Close() error {
	f.closed = true
	return nil
}

func TestSeekableRemoteFileReadReopensOnMismatch(t *testing.T) {
	// Simulate: read 3 bytes (offset=3, streamOffset=3), Seek to 0 (offset=0),
	// then Read → should close old stream and reopen at offset 0.
	transport.SetTestBackend(&mockBackend{
		heavyErr: fmt.Errorf("no heavy"),
		rawHandler: func(kind string, data []byte) ([]byte, error) {
			return nil, fmt.Errorf("intentional reopen failure")
		},
	})

	inner := &mockFsFile{data: []byte("hello world"), pos: 0}
	bfs := &remoteBranchFS{nodeID: testNodeID, wsID: "ws1", branch: "main"}
	sf := &seekableRemoteFile{
		bfs: bfs, name: "test.txt", size: 11,
		offset: 0, streamOffset: 0, r: inner,
	}

	// Read 3 bytes
	buf := make([]byte, 3)
	sf.Read(buf) // offset=3, streamOffset=3

	// Seek back to 0
	sf.Seek(0, io.SeekStart) // offset=0, streamOffset still 3

	// Read should detect mismatch and close old stream
	_, err := sf.Read(buf)
	if err == nil {
		t.Error("expected error from openInternal (backend returns error), got nil")
	}
	if !inner.closed {
		t.Error("old stream should be closed on mismatch")
	}
}

func TestSeekableRemoteFileReadEOF(t *testing.T) {
	inner := &mockFsFile{data: []byte("hi"), pos: 0}
	sf := &seekableRemoteFile{
		bfs: &remoteBranchFS{}, name: "test.txt", size: 2,
		offset: 0, streamOffset: 0, r: inner,
	}
	// Read all data
	buf := make([]byte, 10)
	n, _ := sf.Read(buf)
	if string(buf[:n]) != "hi" {
		t.Errorf("first read = %q", buf[:n])
	}
	// Next read should return EOF
	_, err := sf.Read(buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestSeekableRemoteFileSeekCurrentNegative(t *testing.T) {
	sf := &seekableRemoteFile{size: 100, offset: 50}
	pos, _ := sf.Seek(-10, io.SeekCurrent)
	if pos != 40 {
		t.Errorf("Seek(-10, Current) from 50 = %d, want 40", pos)
	}
}

func TestSeekableRemoteFileSeekEndZero(t *testing.T) {
	sf := &seekableRemoteFile{size: 100, offset: 0}
	pos, _ := sf.Seek(0, io.SeekEnd)
	if pos != 100 {
		t.Errorf("Seek(0, End) = %d, want 100 (= size)", pos)
	}
}

func TestSeekableRemoteFileSeekSizeUnknownNotEnd(t *testing.T) {
	// Seek with Start/Current when size is unknown (-1) should work without Stat
	sf := &seekableRemoteFile{size: -1, offset: 0}
	pos, err := sf.Seek(5, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek err=%v", err)
	}
	if pos != 5 {
		t.Errorf("Seek(5, Start) = %d, want 5", pos)
	}
	// Clamp above size doesn't apply when size == -1
}

// ---------- heavyFile tests ----------

func TestHeavyFileRead(t *testing.T) {
	s, c := net.Pipe()
	go func() {
		s.Write([]byte("data"))
		s.Close()
	}()

	transport.SetTestBackend(&mockBackend{heavyErr: fmt.Errorf("no heavy")})
	hf := &heavyFile{
		name: "video.mp4",
		size: 4,
		r:    io.LimitReader(c, 4),
		conn: c,
	}
	data, err := io.ReadAll(hf)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "data" {
		t.Errorf("content = %q, want data", data)
	}
}

func TestHeavyFileStat(t *testing.T) {
	hf := &heavyFile{name: "video.mp4", size: 12345}
	fi, err := hf.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Name() != "video.mp4" || fi.Size() != 12345 {
		t.Errorf("Stat = %s/%d, want video.mp4/12345", fi.Name(), fi.Size())
	}
}

func TestHeavyFileCloseEOFReuse(t *testing.T) {
	s, c := net.Pipe()
	go func() {
		s.Write([]byte("abcd"))
		// Don't close s — we want to test that after reading 4 bytes,
		// LimitReader returns EOF and conn is returned.
	}()
	defer s.Close()

	mb := &mockBackend{heavyErr: fmt.Errorf("no heavy")}
	transport.SetTestBackend(mb)

	hf := &heavyFile{
		name: "f.bin",
		size: 4,
		r:    io.LimitReader(c, 4),
		conn: c,
	}

	// Read all 4 bytes to exhaust LimitReader
	io.ReadAll(hf)

	// Close should detect EOF and return conn
	hf.Close()

	mb.mu.Lock()
	returned := len(mb.heavyReturned)
	mb.mu.Unlock()
	if returned != 1 {
		t.Errorf("heavyReturned = %d, want 1 (EOF → reuse)", returned)
	}
}

func TestHeavyFileCloseNotEOFDestroy(t *testing.T) {
	s, c := net.Pipe()
	go func() {
		s.Write([]byte("abcdefghij")) // 10 bytes
		// Don't close — simulates large file with remaining data
	}()
	defer s.Close()

	mb := &mockBackend{heavyErr: fmt.Errorf("no heavy")}
	transport.SetTestBackend(mb)

	hf := &heavyFile{
		name: "big.mp4",
		size: 10,
		r:    io.LimitReader(c, 10),
		conn: c,
	}

	// Read only 3 bytes — 7 remaining in LimitReader
	buf := make([]byte, 3)
	hf.Read(buf)

	// Close should detect NOT EOF (7 bytes remain) → destroy conn
	hf.Close()

	mb.mu.Lock()
	returned := len(mb.heavyReturned)
	mb.mu.Unlock()
	if returned != 0 {
		t.Errorf("heavyReturned = %d, want 0 (not EOF → destroy)", returned)
	}
}

func TestHeavyFileCloseDouble(t *testing.T) {
	s, c := net.Pipe()
	go func() {
		s.Write([]byte("ab"))
	}()
	defer s.Close()

	mb := &mockBackend{heavyErr: fmt.Errorf("no heavy")}
	transport.SetTestBackend(mb)

	hf := &heavyFile{
		name: "f.bin",
		size: 2,
		r:    io.LimitReader(c, 2),
		conn: c,
	}
	io.ReadAll(hf)

	hf.Close()
	hf.Close() // second close should not panic or double-return

	mb.mu.Lock()
	returned := len(mb.heavyReturned)
	mb.mu.Unlock()
	if returned != 1 {
		t.Errorf("heavyReturned = %d after double close, want 1", returned)
	}
}

// ---------- History tests ----------

func TestHistoryFileHistory(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		switch kind {
		case KindWSFileHistory:
			return json.Marshal([]types.FileChange{{CommitID: "c1", Path: "a.go", Type: "add"}})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	h := &remoteHistory{nodeID: testNodeID, wsID: "ws_1"}
	changes, err := h.FileHistory("a.go", types.FileHistoryOptions{Limit: 10})
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
			return json.Marshal([]types.CommitEntry{{CommitInfo: types.CommitInfo{ID: "c1"}, Branch: "main"}})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	h := &remoteHistory{nodeID: testNodeID, wsID: "ws_1"}
	entries, err := h.BranchLog("main", types.LogOptions{Limit: 10})
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
		case KindStat:
			return json.Marshal(map[string]any{
				"name": "hello.txt", "size": 42, "mode": 0644,
				"mod_time": int64(1700000000), "is_dir": false,
			})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
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
}

// ---------- writePayload ----------

func TestWritePayload(t *testing.T) {
	msg := &pb.Payload{Namespace: "test", Kind: "hello", Data: []byte("world")}
	var buf bytes.Buffer
	if err := writePayload(&buf, msg); err != nil {
		t.Fatalf("writePayload: %v", err)
	}
	got, err := readPayload(&buf)
	if err != nil {
		t.Fatalf("readPayload: %v", err)
	}
	if got.Namespace != "test" || got.Kind != "hello" || string(got.Data) != "world" {
		t.Errorf("got = %+v", got)
	}
}

// ---------- Workspace FS delegation tests ----------

func TestWorkspaceOpen(t *testing.T) {
	mb := setupMock(nil)
	mb.rawHandler = func(kind string, data []byte) ([]byte, error) {
		if kind == KindOpen {
			return []byte("file data here"), nil
		}
		return nil, nil
	}

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	f, err := ws.Open("readme.md")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	content, _ := io.ReadAll(f)
	if string(content) != "file data here" {
		t.Errorf("content=%q", content)
	}
}

func TestWorkspaceCreate(t *testing.T) {
	mb := setupMock(nil)
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
	rawResp := respBuf.Bytes()

	mb.rawHandler = func(kind string, data []byte) ([]byte, error) {
		if kind == KindCreate {
			return rawResp, nil
		}
		return nil, nil
	}

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	w, err := ws.Create("new-file.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w.Write([]byte("hello"))
	w.Close()
}

func TestWorkspaceRemoveAll(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindRemoveAll {
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	if err := ws.RemoveAll("tmp"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
}

func TestWorkspaceRename(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindRename {
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	if err := ws.Rename("old.txt", "new.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
}

func TestWorkspaceMkdirAll(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindMkdirAll {
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	if err := ws.MkdirAll("a/b/c", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
}

func TestWorkspaceSymlink(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindSymlink {
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	if err := ws.Symlink("target", "link"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
}

func TestWorkspaceReadDir(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindReadDir {
			return json.Marshal([]map[string]any{
				{"name": "file.go", "is_dir": false, "type": "file"},
				{"name": "sub", "is_dir": true, "type": "dir"},
			})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	entries, err := fs.ReadDir(ws, ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("len=%d, want 2", len(entries))
	}
	if entries[0].Name() != "file.go" || entries[0].IsDir() {
		t.Errorf("entry[0]=%v", entries[0])
	}
	if entries[1].Name() != "sub" || !entries[1].IsDir() {
		t.Errorf("entry[1]=%v", entries[1])
	}
	// Test DirEntry.Info()
	fi, err := entries[0].Info()
	if err != nil || fi.Name() != "file.go" {
		t.Errorf("Info()=%v, err=%v", fi, err)
	}
}

func TestRemoteFileInfoModTime(t *testing.T) {
	fi := &remoteFileInfo{name: "x", modTime: fixedTime}
	if fi.ModTime() != fixedTime {
		t.Errorf("ModTime=%v", fi.ModTime())
	}
}

var fixedTime = func() time.Time { t, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z"); return t }()

func TestBranchFS(t *testing.T) {
	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindBranchHead {
			return json.Marshal(types.CommitInfo{ID: "abc"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	br, _ := ws.GetBranch(context.Background(), "dev")
	bfs := br.FS()
	if bfs == nil {
		t.Fatal("FS() returned nil")
	}
	if br.Meta() != nil {
		// Meta may be nil if not set
	}
}

func TestBranchPolicyPropagation(t *testing.T) {
	custom := &ChannelPolicy{SizeThreshold: 5 << 20}
	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, custom)
	rw := ws.(*remoteWorkspace)
	if rw.policy.SizeThreshold != 5<<20 {
		t.Errorf("workspace policy not set")
	}

	setupMock(func(kind string, data []byte) ([]byte, error) {
		if kind == KindBranchHead {
			return json.Marshal(types.CommitInfo{ID: "abc"})
		}
		return nil, fmt.Errorf("unexpected: %s", kind)
	})

	br, _ := ws.GetBranch(context.Background(), "dev")
	rb := br.(*remoteBranch)
	if rb.fsImpl.policy == nil || rb.fsImpl.policy.SizeThreshold != 5<<20 {
		t.Error("branch should inherit workspace's channel policy")
	}
}

func TestWatcherLifecycle(t *testing.T) {
	evtPayload := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      "watch.event",
		Data:      []byte(`{"type":"write","path":"a.go"}`),
	}
	evtBytes, _ := proto.Marshal(evtPayload)
	size := len(evtBytes)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(evtBytes)

	mb := setupMock(nil)
	mb.rawHandler = func(kind string, data []byte) ([]byte, error) {
		if kind == KindWatch {
			return buf.Bytes(), nil
		}
		return nil, nil
	}

	ws := NewRemoteWorkspace(testNodeID, "ws_1", "main", types.Meta{Name: "proj"}, nil)
	w, err := ws.Watch([]string{"*.go"})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	ev := <-w.Events()
	if ev.Type != types.WatchWrite || ev.Path != "a.go" {
		t.Errorf("event=%+v", ev)
	}
	w.Close()
}

// ---------- Interface compliance ----------

var _ types.Workspace = (*remoteWorkspace)(nil)
var _ types.Branch = (*remoteBranch)(nil)
var _ types.WorkspaceFS = (*remoteBranchFS)(nil)
var _ fs.StatFS = (*remoteBranchFS)(nil)
var _ fs.ReadDirFS = (*remoteBranchFS)(nil)
var _ fs.StatFS = (*remoteWorkspace)(nil)
var _ fs.ReadDirFS = (*remoteWorkspace)(nil)
var _ types.History = (*remoteHistory)(nil)
var _ fs.File = (*remoteFile)(nil)
var _ fs.FileInfo = (*remoteFileInfo)(nil)
