package workspace

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/yaoapp/tai-golang/types"
)

// NamespaceWorkspace is the Payload namespace for workspace commands.
const NamespaceWorkspace = "workspace"

// ---------- Channel policy ----------

// ChannelType indicates which transport channel to use.
type ChannelType int

const (
	ChannelLight ChannelType = iota // Light pool (H2 stream)
	ChannelHeavy                    // Heavy pool (raw TCP)
)

// ChannelPolicy decides whether a file operation uses Light or Heavy.
type ChannelPolicy struct {
	SizeThreshold int64    // files > threshold use Heavy (default 1 MB)
	TextExts      []string // always Light
	BinaryExts    []string // always Heavy
}

// DefaultPolicy returns the default channel policy per the design doc.
func DefaultPolicy() *ChannelPolicy {
	return &ChannelPolicy{
		SizeThreshold: 1 << 20, // 1 MB
		TextExts: []string{
			".go", ".ts", ".js", ".json", ".yaml", ".yml", ".md", ".txt",
			".css", ".html", ".xml", ".csv", ".sql", ".sh", ".py", ".rb",
			".rs", ".c", ".h", ".java", ".toml", ".ini", ".cfg", ".conf",
			".jsx", ".tsx", ".vue", ".svelte", ".sass", ".scss", ".less",
		},
		BinaryExts: []string{
			".bin", ".exe", ".dll", ".so", ".dylib",
			".zip", ".tar", ".gz", ".bz2", ".xz", ".zst",
			".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".svg",
			".mp4", ".avi", ".mkv", ".mov", ".webm",
			".mp3", ".wav", ".flac", ".ogg",
			".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
			".wasm", ".o", ".a", ".lib",
		},
	}
}

// channelForPath determines the channel for a file path, optionally with size.
// size < 0 means unknown.
func (p *ChannelPolicy) channelForPath(filePath string, size int64) ChannelType {
	ext := strings.ToLower(path.Ext(filePath))
	for _, e := range p.TextExts {
		if ext == e {
			return ChannelLight
		}
	}
	for _, e := range p.BinaryExts {
		if ext == e {
			return ChannelHeavy
		}
	}
	// Unknown extension: decide by size
	if size < 0 {
		return ChannelLight // unknown size, default to Light
	}
	if size > p.SizeThreshold {
		return ChannelHeavy
	}
	return ChannelLight
}

// channelForMeta returns ChannelLight for metadata-only operations.
func channelForMeta() ChannelType {
	return ChannelLight
}

// ---------- Command kinds (33 total) ----------

// File operations (9)
const (
	KindOpen      = "open"
	KindCreate    = "create"
	KindStat      = "stat"
	KindReadDir   = "read_dir"
	KindRemove    = "remove"
	KindRemoveAll = "remove_all"
	KindRename    = "rename"
	KindMkdirAll  = "mkdir_all"
	KindSymlink   = "symlink"
)

// Branch info + version control (8)
const (
	KindBranchHead       = "branch.head"
	KindBranchMeta       = "branch.meta"
	KindBranchUpdateMeta = "branch.update_meta"
	KindBranchAcquireW   = "branch.acquire_write"
	KindBranchReleaseW   = "branch.release_write"
	KindCommit           = "commit"
	KindDiff             = "diff"
	KindRollback         = "rollback"
)

// Workspace CRUD (10)
const (
	KindWSCreate      = "ws.create"
	KindWSOpen        = "ws.open"
	KindWSList        = "ws.list"
	KindWSDelete      = "ws.delete"
	KindWSUpdateMeta  = "ws.update_meta"
	KindWSGetCommit   = "ws.get_commit"
	KindWSHistory     = "ws.history"
	KindWSFileHistory = "ws.file_history"
	KindWSBranchLog   = "ws.branch_log"
	KindWSGC          = "ws.gc"
)

// Branch management (5)
const (
	KindListBranches      = "list_branches"
	KindCreateBranch      = "create_branch"
	KindGetOrCreateBranch = "get_or_create_branch"
	KindDeleteBranch      = "delete_branch"
	KindMerge             = "merge"
)

// Long-lived (1)
const (
	KindWatch = "watch"
)

// Result/error suffixes
const (
	SuffixResult = ".result"
	KindError    = "error"
)

// ---------- Request/Response types ----------

// FileRequest is the common request for file operations.
type FileRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Branch      string `json:"branch"`
	Path        string `json:"path,omitempty"`
	Perm        uint32 `json:"perm,omitempty"`
}

// RenameRequest is used for rename and symlink.
type RenameRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Branch      string `json:"branch"`
	Old         string `json:"old"`
	New         string `json:"new"`
}

// BranchRequest is used for branch info and version control.
type BranchRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Branch      string `json:"branch"`
	Holder      string `json:"holder,omitempty"`
	Message     string `json:"message,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	CommitID    string `json:"commit_id,omitempty"`
}

// WSCreateRequest is the request for ws.create.
type WSCreateRequest struct {
	Path string     `json:"path"`
	Meta types.Meta `json:"meta"`
}

// WSRequest is the request for ws.open / ws.delete / ws.update_meta.
type WSRequest struct {
	WorkspaceID string      `json:"workspace_id,omitempty"`
	Meta        *types.Meta `json:"meta,omitempty"`
}

// WSCommitRequest is the request for ws.get_commit.
type WSCommitRequest struct {
	WorkspaceID string `json:"workspace_id"`
	CommitID    string `json:"commit_id"`
}

// WSHistoryRequest is the request for ws.history / ws.branch_log / ws.file_history.
type WSHistoryRequest struct {
	WorkspaceID string          `json:"workspace_id"`
	Branch      string          `json:"branch,omitempty"`
	Path        string          `json:"path,omitempty"`
	Opts        json.RawMessage `json:"opts,omitempty"`
}

// WSListRequest is the request for ws.list.
type WSListRequest struct {
	Opts types.ListOptions `json:"opts"`
}

// WSGCRequest is the request for ws.gc.
type WSGCRequest struct {
	WorkspaceID string          `json:"workspace_id"`
	Opts        types.GCOptions `json:"opts"`
}

// BranchMgmtRequest is used for branch management commands.
type BranchMgmtRequest struct {
	WorkspaceID string              `json:"workspace_id"`
	Name        string              `json:"name,omitempty"`
	From        string              `json:"from,omitempty"`
	Into        string              `json:"into,omitempty"`
	Meta        map[string]any      `json:"meta,omitempty"`
	Opts        *types.MergeOptions `json:"opts,omitempty"`
}

// WatchRequest is the request for watch.
type WatchRequest struct {
	WorkspaceID string   `json:"workspace_id"`
	Branch      string   `json:"branch"`
	Patterns    []string `json:"patterns"`
}

// ErrorResponse is the standard error response.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
