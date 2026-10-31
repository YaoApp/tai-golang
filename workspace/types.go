// Package workspace provides remote workspace access via the Tai transport layer.
// All file operations use standard Go fs.FS interfaces; the transport channel
// (Light pool / Heavy pool) is chosen automatically and transparently.
package workspace

import (
	"context"
	"io"
	"io/fs"
)

// WorkspaceFS extends fs.FS with write operations and Watch.
// Mirrors the Tai-side workspace/types/branch.go definition.
type WorkspaceFS interface {
	fs.FS                                         // Open / Stat / ReadDir
	Create(name string) (io.WriteCloser, error)   // create or overwrite a file
	Remove(name string) error                     // delete a file
	RemoveAll(name string) error                  // recursive delete
	Rename(oldname, newname string) error         // rename / move
	MkdirAll(name string, perm fs.FileMode) error // create directory tree
	Symlink(oldname, newname string) error        // symbolic link
	Watch(patterns []string) (Watcher, error)     // file change notifications
}

// Watcher delivers file change events from the Tai side.
// Events are received via a long-lived Light stream (kind:"watch" → "watch.event").
type Watcher interface {
	Events() <-chan WatchEvent
	Close() error
}

// WatchEventType identifies the kind of file change.
type WatchEventType string

const (
	WatchCreate WatchEventType = "create"
	WatchWrite  WatchEventType = "write"
	WatchRemove WatchEventType = "remove"
	WatchRename WatchEventType = "rename"
)

// WatchEvent represents a single file change notification.
type WatchEvent struct {
	Type WatchEventType
	Path string
}

// Workspace is the top-level object returned by Open. It embeds WorkspaceFS
// for default-branch file operations and exposes branch management, history,
// and maintenance methods.
type Workspace interface {
	WorkspaceFS // embedded — fs.ReadFile(ws, path) operates on the bound branch

	// Metadata
	ID() string
	Meta() Meta
	UpdateMeta(ctx context.Context, meta Meta) error

	// Branch management
	Main(ctx context.Context) (Branch, error)
	ListBranches(ctx context.Context) ([]BranchInfo, error)
	CreateBranch(ctx context.Context, name, from string, meta map[string]any) (Branch, error)
	GetBranch(ctx context.Context, name string) (Branch, error)
	GetOrCreateBranch(ctx context.Context, name, from string, meta map[string]any) (Branch, error)
	DeleteBranch(ctx context.Context, name string) error
	Merge(ctx context.Context, from, into string, opts MergeOptions) (*MergeResult, error)

	// History
	GetCommit(ctx context.Context, id string) (*CommitInfo, error)
	LoadHistory(ctx context.Context) (History, error)

	// Maintenance
	GC(ctx context.Context, opts GCOptions) (*GCResult, error)
	Close() error
}

// Branch represents a named branch within a workspace. It provides file
// operations (via FS()) and version control methods.
type Branch interface {
	// File operations (including Watch, inherited from WorkspaceFS)
	FS() WorkspaceFS

	// Branch info
	Name() string
	Head(ctx context.Context) (*CommitInfo, error)
	Meta() map[string]any
	UpdateMeta(ctx context.Context, meta map[string]any) error

	// Version control
	Commit(ctx context.Context, message string) (*CommitInfo, error)
	Diff(ctx context.Context, from, to string) ([]FileDiff, error)
	Rollback(ctx context.Context, commitID string) error

	// Write lock
	AcquireWrite(ctx context.Context, holder string) error
	ReleaseWrite(ctx context.Context) error
}

// History provides commit and file history queries.
type History interface {
	Query(opts HistoryOptions) ([]CommitEntry, error)
	FileHistory(path string, opts FileHistoryOptions) ([]FileChange, error)
	BranchLog(branch string, opts LogOptions) ([]CommitEntry, error)
}

// ---------- Data types ----------

// Meta holds workspace metadata.
type Meta struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

// BranchInfo holds branch summary information.
type BranchInfo struct {
	Name string         `json:"name"`
	Head string         `json:"head"` // commit ID
	Meta map[string]any `json:"meta,omitempty"`
}

// CommitInfo describes a single commit.
type CommitInfo struct {
	ID      string `json:"id"`
	Parent  string `json:"parent,omitempty"`
	Message string `json:"message"`
	Time    int64  `json:"time"` // unix timestamp
}

// FileDiff describes a change to one file between two commits.
type FileDiff struct {
	Path    string `json:"path"`
	Type    string `json:"type"`               // "add", "modify", "delete", "rename"
	OldPath string `json:"old_path,omitempty"` // for rename
}

// MergeOptions configures a branch merge.
type MergeOptions struct {
	DeleteSource bool   `json:"delete_source,omitempty"`
	Message      string `json:"message,omitempty"`
}

// MergeResult reports the outcome of a merge.
type MergeResult struct {
	CommitID  string   `json:"commit_id"`
	Conflicts []string `json:"conflicts,omitempty"`
}

// GCOptions configures garbage collection.
type GCOptions struct {
	DryRun bool `json:"dry_run,omitempty"`
}

// GCResult reports the outcome of garbage collection.
type GCResult struct {
	ReclaimedBytes int64 `json:"reclaimed_bytes"`
	ReclaimedBlobs int   `json:"reclaimed_blobs"`
}

// CommitEntry is a commit with its position in the log.
type CommitEntry struct {
	CommitInfo
	Branch string `json:"branch"`
}

// FileChange describes a file-level change in history.
type FileChange struct {
	CommitID string `json:"commit_id"`
	Path     string `json:"path"`
	Type     string `json:"type"` // "add", "modify", "delete"
	Time     int64  `json:"time"`
}

// HistoryOptions configures a history query.
type HistoryOptions struct {
	Limit  int   `json:"limit,omitempty"`
	Before int64 `json:"before,omitempty"` // unix timestamp, 0 = no filter
	After  int64 `json:"after,omitempty"`  // unix timestamp, 0 = no filter
}

// FileHistoryOptions configures a file history query.
type FileHistoryOptions struct {
	Limit int `json:"limit,omitempty"`
}

// LogOptions configures a branch log query.
type LogOptions struct {
	Limit  int   `json:"limit,omitempty"`
	Before int64 `json:"before,omitempty"` // unix timestamp, 0 = no filter
}

// ListOptions configures workspace listing.
type ListOptions struct {
	Owner string `json:"owner,omitempty"`
}

// RegistryEntry is one entry in the workspace listing.
type RegistryEntry struct {
	ID   string `json:"id"`
	Meta Meta   `json:"meta"`
}
