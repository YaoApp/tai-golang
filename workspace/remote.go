package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"sync"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// ---------- remoteWorkspace ----------

// remoteWorkspace implements types.Workspace by forwarding all operations to
// the Tai side via the Light/Heavy transport.
type remoteWorkspace struct {
	nodeID string
	wsID   string
	branch string // bound branch (default "main")
	meta   types.Meta
	policy *ChannelPolicy
	fsImpl *remoteBranchFS
}

// --- WorkspaceFS (embedded, delegates to fsImpl) ---

func (rw *remoteWorkspace) Open(name string) (fs.File, error)     { return rw.fsImpl.Open(name) }
func (rw *remoteWorkspace) Stat(name string) (fs.FileInfo, error) { return rw.fsImpl.Stat(name) }
func (rw *remoteWorkspace) ReadDir(name string) ([]fs.DirEntry, error) {
	return rw.fsImpl.ReadDir(name)
}
func (rw *remoteWorkspace) Create(name string) (io.WriteCloser, error) { return rw.fsImpl.Create(name) }
func (rw *remoteWorkspace) Remove(name string) error                   { return rw.fsImpl.Remove(name) }
func (rw *remoteWorkspace) RemoveAll(name string) error                { return rw.fsImpl.RemoveAll(name) }
func (rw *remoteWorkspace) Rename(old, new string) error               { return rw.fsImpl.Rename(old, new) }
func (rw *remoteWorkspace) MkdirAll(name string, perm fs.FileMode) error {
	return rw.fsImpl.MkdirAll(name, perm)
}
func (rw *remoteWorkspace) Symlink(old, new string) error { return rw.fsImpl.Symlink(old, new) }
func (rw *remoteWorkspace) Watch(patterns []string) (types.Watcher, error) {
	return rw.fsImpl.Watch(patterns)
}

// --- Workspace metadata ---

func (rw *remoteWorkspace) ID() string       { return rw.wsID }
func (rw *remoteWorkspace) Meta() types.Meta { return rw.meta }

func (rw *remoteWorkspace) UpdateMeta(ctx context.Context, meta types.Meta) error {
	req := WSRequest{WorkspaceID: rw.wsID, Meta: &meta}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(ctx, rw.nodeID, KindWSUpdateMeta, data)
	if err != nil {
		return fmt.Errorf("update meta: %w", err)
	}
	rw.meta = meta
	return nil
}

// --- Branch management ---

func (rw *remoteWorkspace) Main(ctx context.Context) (types.Branch, error) {
	return rw.GetBranch(ctx, "main")
}

func (rw *remoteWorkspace) ListBranches(ctx context.Context) ([]types.BranchInfo, error) {
	req := BranchMgmtRequest{WorkspaceID: rw.wsID}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rw.nodeID, KindListBranches, data)
	if err != nil {
		return nil, err
	}
	var branches []types.BranchInfo
	if err := json.Unmarshal(resp, &branches); err != nil {
		return nil, err
	}
	return branches, nil
}

func (rw *remoteWorkspace) CreateBranch(ctx context.Context, name, from string, meta map[string]any) (types.Branch, error) {
	req := BranchMgmtRequest{WorkspaceID: rw.wsID, Name: name, From: from, Meta: meta}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rw.nodeID, KindCreateBranch, data)
	if err != nil {
		return nil, err
	}
	var info types.BranchInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		return nil, err
	}
	return newRemoteBranch(rw.nodeID, rw.wsID, info, rw.policy), nil
}

func (rw *remoteWorkspace) GetBranch(ctx context.Context, name string) (types.Branch, error) {
	req := BranchRequest{WorkspaceID: rw.wsID, Branch: name}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rw.nodeID, KindBranchHead, data)
	if err != nil {
		return nil, err
	}
	var info types.CommitInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		return nil, err
	}
	bi := types.BranchInfo{Name: name, Head: info.ID}
	return newRemoteBranch(rw.nodeID, rw.wsID, bi, rw.policy), nil
}

func (rw *remoteWorkspace) GetOrCreateBranch(ctx context.Context, name, from string, meta map[string]any) (types.Branch, error) {
	req := BranchMgmtRequest{WorkspaceID: rw.wsID, Name: name, From: from, Meta: meta}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rw.nodeID, KindGetOrCreateBranch, data)
	if err != nil {
		return nil, err
	}
	var info types.BranchInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		return nil, err
	}
	return newRemoteBranch(rw.nodeID, rw.wsID, info, rw.policy), nil
}

func (rw *remoteWorkspace) DeleteBranch(ctx context.Context, name string) error {
	req := BranchMgmtRequest{WorkspaceID: rw.wsID, Name: name}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(ctx, rw.nodeID, KindDeleteBranch, data)
	return err
}

func (rw *remoteWorkspace) Merge(ctx context.Context, from, into string, opts types.MergeOptions) (*types.MergeResult, error) {
	req := BranchMgmtRequest{WorkspaceID: rw.wsID, From: from, Into: into, Opts: &opts}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rw.nodeID, KindMerge, data)
	if err != nil {
		return nil, err
	}
	var result types.MergeResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- History ---

func (rw *remoteWorkspace) GetCommit(ctx context.Context, id string) (*types.CommitInfo, error) {
	req := WSCommitRequest{WorkspaceID: rw.wsID, CommitID: id}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rw.nodeID, KindWSGetCommit, data)
	if err != nil {
		return nil, err
	}
	var ci types.CommitInfo
	if err := json.Unmarshal(resp, &ci); err != nil {
		return nil, err
	}
	return &ci, nil
}

func (rw *remoteWorkspace) LoadHistory(ctx context.Context) (types.History, error) {
	return &remoteHistory{nodeID: rw.nodeID, wsID: rw.wsID}, nil
}

// --- Maintenance ---

func (rw *remoteWorkspace) GC(ctx context.Context, opts types.GCOptions) (*types.GCResult, error) {
	req := WSGCRequest{WorkspaceID: rw.wsID, Opts: opts}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rw.nodeID, KindWSGC, data)
	if err != nil {
		return nil, err
	}
	var result types.GCResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (rw *remoteWorkspace) Close() error {
	return nil
}

// ---------- remoteBranch ----------

// remoteBranch implements types.Branch by forwarding to the Tai side.
type remoteBranch struct {
	nodeID string
	wsID   string
	info   types.BranchInfo
	fsImpl *remoteBranchFS
}

func newRemoteBranch(nodeID, wsID string, info types.BranchInfo, policy *ChannelPolicy) *remoteBranch {
	return &remoteBranch{
		nodeID: nodeID,
		wsID:   wsID,
		info:   info,
		fsImpl: &remoteBranchFS{nodeID: nodeID, wsID: wsID, branch: info.Name, policy: policy},
	}
}

func (rb *remoteBranch) FS() types.WorkspaceFS { return rb.fsImpl }
func (rb *remoteBranch) Name() string          { return rb.info.Name }
func (rb *remoteBranch) Meta() map[string]any  { return rb.info.Meta }

func (rb *remoteBranch) Head(ctx context.Context) (*types.CommitInfo, error) {
	req := BranchRequest{WorkspaceID: rb.wsID, Branch: rb.info.Name}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rb.nodeID, KindBranchHead, data)
	if err != nil {
		return nil, err
	}
	var ci types.CommitInfo
	if err := json.Unmarshal(resp, &ci); err != nil {
		return nil, err
	}
	return &ci, nil
}

func (rb *remoteBranch) UpdateMeta(ctx context.Context, meta map[string]any) error {
	req := struct {
		WorkspaceID string         `json:"workspace_id"`
		Branch      string         `json:"branch"`
		Meta        map[string]any `json:"meta"`
	}{
		WorkspaceID: rb.wsID,
		Branch:      rb.info.Name,
		Meta:        meta,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(ctx, rb.nodeID, KindBranchUpdateMeta, data)
	if err != nil {
		return err
	}
	rb.info.Meta = meta
	return nil
}

func (rb *remoteBranch) Commit(ctx context.Context, message string) (*types.CommitInfo, error) {
	req := BranchRequest{WorkspaceID: rb.wsID, Branch: rb.info.Name, Message: message}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rb.nodeID, KindCommit, data)
	if err != nil {
		return nil, err
	}
	var ci types.CommitInfo
	if err := json.Unmarshal(resp, &ci); err != nil {
		return nil, err
	}
	return &ci, nil
}

func (rb *remoteBranch) Diff(ctx context.Context, from, to string) ([]types.FileDiff, error) {
	req := BranchRequest{WorkspaceID: rb.wsID, Branch: rb.info.Name, From: from, To: to}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(ctx, rb.nodeID, KindDiff, data)
	if err != nil {
		return nil, err
	}
	var diffs []types.FileDiff
	if err := json.Unmarshal(resp, &diffs); err != nil {
		return nil, err
	}
	return diffs, nil
}

func (rb *remoteBranch) Rollback(ctx context.Context, commitID string) error {
	req := BranchRequest{WorkspaceID: rb.wsID, Branch: rb.info.Name, CommitID: commitID}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(ctx, rb.nodeID, KindRollback, data)
	return err
}

func (rb *remoteBranch) AcquireWrite(ctx context.Context, holder string) error {
	req := BranchRequest{WorkspaceID: rb.wsID, Branch: rb.info.Name, Holder: holder}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(ctx, rb.nodeID, KindBranchAcquireW, data)
	return err
}

func (rb *remoteBranch) ReleaseWrite(ctx context.Context) error {
	req := BranchRequest{WorkspaceID: rb.wsID, Branch: rb.info.Name}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(ctx, rb.nodeID, KindBranchReleaseW, data)
	return err
}

// ---------- remoteBranchFS ----------

// remoteBranchFS implements types.WorkspaceFS for a specific workspace + branch.
type remoteBranchFS struct {
	nodeID string
	wsID   string
	branch string
	policy *ChannelPolicy
}

// Stat implements fs.StatFS, sending a dedicated "stat" command.
func (f *remoteBranchFS) Stat(name string) (fs.FileInfo, error) {
	req := FileRequest{WorkspaceID: f.wsID, Branch: f.branch, Path: name}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(context.Background(), f.nodeID, KindStat, data)
	if err != nil {
		return nil, err
	}
	var info struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		Mode    uint32 `json:"mode"`
		ModTime int64  `json:"mod_time"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := json.Unmarshal(resp, &info); err != nil {
		return nil, err
	}
	return &remoteFileInfo{
		name:    info.Name,
		size:    info.Size,
		mode:    fs.FileMode(info.Mode),
		modTime: time.Unix(info.ModTime, 0),
		isDir:   info.IsDir,
	}, nil
}

// ReadDir implements fs.ReadDirFS.
func (f *remoteBranchFS) ReadDir(name string) ([]fs.DirEntry, error) {
	req := FileRequest{WorkspaceID: f.wsID, Branch: f.branch, Path: name}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(context.Background(), f.nodeID, KindReadDir, data)
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal(resp, &entries); err != nil {
		return nil, err
	}
	result := make([]fs.DirEntry, len(entries))
	for i, e := range entries {
		mode := fs.FileMode(0)
		if e.IsDir {
			mode = fs.ModeDir
		}
		result[i] = &remoteDirEntry{name: e.Name, isDir: e.IsDir, mode: mode}
	}
	return result, nil
}

func (f *remoteBranchFS) Open(name string) (fs.File, error) {
	policy := f.policy
	if policy == nil {
		policy = DefaultPolicy()
	}
	ch := policy.channelForPath(name, -1)
	ctx := context.Background()

	req := FileRequest{WorkspaceID: f.wsID, Branch: f.branch, Path: name}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	if ch == ChannelHeavy {
		return f.openHeavy(ctx, data, name)
	}
	return f.openLight(ctx, data, name)
}

func (f *remoteBranchFS) openLight(ctx context.Context, reqData []byte, name string) (fs.File, error) {
	firstFrame := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      KindOpen,
		Data:      reqData,
	}
	stream, err := transport.PkgOpenLightStream(ctx, f.nodeID, firstFrame)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	return &remoteFile{name: name, stream: stream}, nil
}

func (f *remoteBranchFS) openHeavy(ctx context.Context, reqData []byte, name string) (fs.File, error) {
	conn, err := transport.PkgHeavyPick(f.nodeID)
	if err != nil {
		return f.openLight(ctx, reqData, name)
	}
	firstFrame := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      KindOpen,
		Data:      reqData,
	}
	if err := writePayload(conn, firstFrame); err != nil {
		transport.PkgHeavyReturn(conn)
		return f.openLight(ctx, reqData, name)
	}
	return &remoteFile{name: name, heavyConn: conn}, nil
}

func (f *remoteBranchFS) Create(name string) (io.WriteCloser, error) {
	policy := f.policy
	if policy == nil {
		policy = DefaultPolicy()
	}
	ch := policy.channelForPath(name, -1)
	ctx := context.Background()

	req := FileRequest{WorkspaceID: f.wsID, Branch: f.branch, Path: name}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	firstFrame := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      KindCreate,
		Data:      data,
	}

	if ch == ChannelHeavy {
		conn, err := transport.PkgHeavyPick(f.nodeID)
		if err == nil {
			if err := writePayload(conn, firstFrame); err == nil {
				return &remoteWriter{heavyConn: conn, nodeID: f.nodeID}, nil
			}
			transport.PkgHeavyReturn(conn)
		}
	}

	stream, err := transport.PkgOpenLightStream(ctx, f.nodeID, firstFrame)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	return &remoteWriter{stream: stream}, nil
}

func (f *remoteBranchFS) Remove(name string) error {
	req := FileRequest{WorkspaceID: f.wsID, Branch: f.branch, Path: name}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(context.Background(), f.nodeID, KindRemove, data)
	return err
}

func (f *remoteBranchFS) RemoveAll(name string) error {
	req := FileRequest{WorkspaceID: f.wsID, Branch: f.branch, Path: name}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(context.Background(), f.nodeID, KindRemoveAll, data)
	return err
}

func (f *remoteBranchFS) Rename(oldname, newname string) error {
	req := RenameRequest{WorkspaceID: f.wsID, Branch: f.branch, Old: oldname, New: newname}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(context.Background(), f.nodeID, KindRename, data)
	return err
}

func (f *remoteBranchFS) MkdirAll(name string, perm fs.FileMode) error {
	req := FileRequest{WorkspaceID: f.wsID, Branch: f.branch, Path: name, Perm: uint32(perm)}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(context.Background(), f.nodeID, KindMkdirAll, data)
	return err
}

func (f *remoteBranchFS) Symlink(oldname, newname string) error {
	req := RenameRequest{WorkspaceID: f.wsID, Branch: f.branch, Old: oldname, New: newname}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = roundTrip(context.Background(), f.nodeID, KindSymlink, data)
	return err
}

func (f *remoteBranchFS) Watch(patterns []string) (types.Watcher, error) {
	req := WatchRequest{WorkspaceID: f.wsID, Branch: f.branch, Patterns: patterns}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	firstFrame := &pb.Payload{
		Namespace: NamespaceWorkspace,
		Kind:      KindWatch,
		Data:      data,
	}
	stream, err := transport.PkgOpenLightStream(context.Background(), f.nodeID, firstFrame)
	if err != nil {
		return nil, fmt.Errorf("open watch stream: %w", err)
	}
	return newRemoteWatcher(stream), nil
}

// ---------- remoteFile ----------

// remoteFile implements fs.File for streaming file reads.
type remoteFile struct {
	name      string
	stream    io.ReadWriteCloser
	heavyConn net.Conn // Heavy pool connection (must be returned, not closed)
}

func (f *remoteFile) Stat() (fs.FileInfo, error) {
	return &remoteFileInfo{name: f.name}, nil
}

func (f *remoteFile) Read(p []byte) (int, error) {
	if f.heavyConn != nil {
		return f.heavyConn.Read(p)
	}
	return f.stream.Read(p)
}

func (f *remoteFile) Close() error {
	if f.heavyConn != nil {
		transport.PkgHeavyReturn(f.heavyConn)
		return nil
	}
	return f.stream.Close()
}

// ---------- remoteFileInfo ----------

type remoteFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

// ---------- remoteDirEntry ----------

type remoteDirEntry struct {
	name  string
	isDir bool
	mode  fs.FileMode
}

func (de *remoteDirEntry) Name() string      { return de.name }
func (de *remoteDirEntry) IsDir() bool       { return de.isDir }
func (de *remoteDirEntry) Type() fs.FileMode { return de.mode }
func (de *remoteDirEntry) Info() (fs.FileInfo, error) {
	return &remoteFileInfo{name: de.name, isDir: de.isDir, mode: de.mode}, nil
}

func (fi *remoteFileInfo) Name() string       { return fi.name }
func (fi *remoteFileInfo) Size() int64        { return fi.size }
func (fi *remoteFileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi *remoteFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *remoteFileInfo) IsDir() bool        { return fi.isDir }
func (fi *remoteFileInfo) Sys() any           { return nil }

// ---------- remoteWriter ----------

// remoteWriter wraps a stream or heavy connection for file writes.
type remoteWriter struct {
	stream    io.ReadWriteCloser
	heavyConn net.Conn // Heavy pool connection (must be returned, not closed)
	nodeID    string   // only needed for Heavy return
}

func (w *remoteWriter) Write(p []byte) (int, error) {
	if w.heavyConn != nil {
		return w.heavyConn.Write(p)
	}
	return w.stream.Write(p)
}

func (w *remoteWriter) Close() error {
	if w.heavyConn != nil {
		transport.PkgHeavyReturn(w.heavyConn)
		return nil
	}
	// If the stream supports half-close, signal end-of-write and read the
	// response to detect Tai-side errors (e.g. permission denied, disk full).
	if hc, ok := w.stream.(HalfCloser); ok {
		if err := hc.CloseWrite(); err != nil {
			w.stream.Close()
			return err
		}
		_, err := ReadResponse(w.stream, KindCreate)
		w.stream.Close()
		return err
	}
	return w.stream.Close()
}

// ---------- remoteWatcher ----------

// remoteWatcher implements types.Watcher over a long-lived Light stream.
type remoteWatcher struct {
	stream    io.ReadWriteCloser
	events    chan types.WatchEvent
	done      chan struct{}
	closeOnce sync.Once
}

func newRemoteWatcher(stream io.ReadWriteCloser) *remoteWatcher {
	w := &remoteWatcher{
		stream: stream,
		events: make(chan types.WatchEvent, 64),
		done:   make(chan struct{}),
	}
	go w.recvLoop()
	return w
}

func (w *remoteWatcher) Events() <-chan types.WatchEvent {
	return w.events
}

func (w *remoteWatcher) Close() error {
	w.closeOnce.Do(func() { close(w.done) })
	return w.stream.Close()
}

func (w *remoteWatcher) recvLoop() {
	defer close(w.events)
	for {
		msg, err := readPayload(w.stream)
		if err != nil {
			return
		}
		if msg.Kind != "watch.event" {
			continue
		}
		var ev types.WatchEvent
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			continue
		}
		select {
		case w.events <- ev:
		case <-w.done:
			return
		}
	}
}

// ---------- remoteHistory ----------

// remoteHistory implements types.History by forwarding queries to the Tai side.
type remoteHistory struct {
	nodeID string
	wsID   string
}

func (h *remoteHistory) Query(opts types.HistoryOptions) ([]types.CommitEntry, error) {
	optsData, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	req := WSHistoryRequest{WorkspaceID: h.wsID, Opts: optsData}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(context.Background(), h.nodeID, KindWSHistory, data)
	if err != nil {
		return nil, err
	}
	var entries []types.CommitEntry
	if err := json.Unmarshal(resp, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (h *remoteHistory) FileHistory(path string, opts types.FileHistoryOptions) ([]types.FileChange, error) {
	optsData, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	req := WSHistoryRequest{WorkspaceID: h.wsID, Path: path, Opts: optsData}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(context.Background(), h.nodeID, KindWSFileHistory, data)
	if err != nil {
		return nil, err
	}
	var changes []types.FileChange
	if err := json.Unmarshal(resp, &changes); err != nil {
		return nil, err
	}
	return changes, nil
}

func (h *remoteHistory) BranchLog(branch string, opts types.LogOptions) ([]types.CommitEntry, error) {
	optsData, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	req := WSHistoryRequest{WorkspaceID: h.wsID, Branch: branch, Opts: optsData}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := roundTrip(context.Background(), h.nodeID, KindWSBranchLog, data)
	if err != nil {
		return nil, err
	}
	var entries []types.CommitEntry
	if err := json.Unmarshal(resp, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// ---------- Payload write helper ----------

// writePayload writes a length-prefixed Payload to w.
func writePayload(w io.Writer, msg *pb.Payload) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	size := len(data)
	header := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
