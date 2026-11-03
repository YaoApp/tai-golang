# Workspace API

The `workspace` package provides remote workspace file operations and version control over the Tai transport layer. All file operations use standard Go `fs.FS` interfaces; the transport channel (Light pool / Heavy pool) is selected automatically based on file type and size.

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "io"
    "io/fs"

    tai "github.com/yaoapp/tai-golang"
    "github.com/yaoapp/tai-golang/node"
    "github.com/yaoapp/tai-golang/types"
)

func main() {
    // 1. Start the SDK server (see Quick Start guide)
    server, _ := tai.NewServer(&tai.ServerConfig{ /* ... */ })
    go server.Start(context.Background())

    // 2. Register a callback to know when nodes connect
    node.OnRegister(func(n types.RemoteNode) error {
        fmt.Println("node registered:", n.Info().ID)
        return nil
    })

    // Wait for a Tai node to connect...
    ctx := context.Background()
    n := node.Get("node-1") // returns types.RemoteNode

    // 3. Create a workspace on the Tai node
    ws, err := n.CreateWorkspace(ctx, types.Meta{
        Name:  "my-project",
        Owner: "user-1",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println("workspace ID:", ws.ID())

    // 4. Write a file
    w, _ := ws.Create("hello.txt")
    w.Write([]byte("hello world"))
    w.Close()

    // 5. Read it back
    f, _ := ws.Open("hello.txt")
    data, _ := io.ReadAll(f)
    f.Close()
    fmt.Println(string(data)) // "hello world"

    // 6. Stat and ReadDir use standard fs functions
    info, _ := fs.Stat(ws, "hello.txt")
    fmt.Println(info.Name(), info.Size()) // "hello.txt 11"

    entries, _ := fs.ReadDir(ws, ".")
    for _, e := range entries {
        fmt.Println(e.Name(), e.IsDir())
    }

    // 6b. Seek — remote file behaves like local *os.File
    f2, _ := ws.Open("hello.txt")
    seeker := f2.(io.ReadSeeker)
    seeker.Seek(6, io.SeekStart)        // skip to offset 6 (no network)
    tail, _ := io.ReadAll(f2)
    fmt.Println(string(tail))           // "world"
    f2.Close()

    // 6c. Serve video with Range support — identical to local file
    // http.HandleFunc("/preview", func(w http.ResponseWriter, r *http.Request) {
    //     f, _ := ws.Open("video.mp4")
    //     defer f.Close()
    //     fi, _ := f.Stat()
    //     http.ServeContent(w, r, fi.Name(), fi.ModTime(), f.(io.ReadSeeker))
    // })

    // 7. Branch management
    branch, _ := ws.CreateBranch(ctx, "feature-x", "main", nil)
    bfs := branch.FS()

    w2, _ := bfs.Create("new-file.go")
    w2.Write([]byte("package main"))
    w2.Close()

    commit, _ := branch.Commit(ctx, "add new-file.go")
    fmt.Println("commit:", commit.ID)

    // 8. Merge back to main
    result, _ := ws.Merge(ctx, "feature-x", "main", types.MergeOptions{
        DeleteSource: true,
        Message:      "merge feature-x",
    })
    fmt.Println("merge commit:", result.CommitID)

    // 9. Watch for file changes
    watcher, _ := ws.Watch([]string{"*.go"})
    defer watcher.Close()
    go func() {
        for ev := range watcher.Events() {
            fmt.Printf("[%s] %s\n", ev.Type, ev.Path)
        }
    }()

    // 10. Query history
    history, _ := ws.LoadHistory(ctx)
    log, _ := history.BranchLog("main", types.LogOptions{Limit: 10})
    for _, entry := range log {
        fmt.Printf("%s %s\n", entry.ID[:8], entry.Message)
    }
}
```

## Workspace Access

Workspace operations are accessed through `types.RemoteNode` methods. After a Tai node registers, get a `RemoteNode` via `node.Get(nodeID)`:

```go
import (
    "github.com/yaoapp/tai-golang/node"
    "github.com/yaoapp/tai-golang/types"
)

n := node.Get("node-1")

ws, err := n.OpenWorkspace(ctx, "abc123")
ws, err := n.OpenWorkspace(ctx, "abc123", "dev")         // open on "dev" branch
ws, err := n.CreateWorkspace(ctx, types.Meta{Name: "proj", Owner: "user-1"})
entries, err := n.ListWorkspaces(ctx, types.ListOptions{Owner: "user-1"})
err := n.DeleteWorkspace(ctx, "abc123")
```

### HalfCloser

```go
type HalfCloser interface {
    CloseWrite() error
}
```

`HalfCloser` is optionally implemented by streams to support closing the write side while keeping the read side open. When a `Create` writer's stream implements `HalfCloser`, `Close()` will half-close the write side and read the server's response to detect errors (e.g., permission denied, disk full).

## Workspace Interface

```go
type Workspace interface {
    WorkspaceFS

    ID() string
    Meta() Meta
    UpdateMeta(ctx context.Context, meta Meta) error

    Main(ctx context.Context) (Branch, error)
    ListBranches(ctx context.Context) ([]BranchInfo, error)
    CreateBranch(ctx context.Context, name, from string, meta map[string]any) (Branch, error)
    GetBranch(ctx context.Context, name string) (Branch, error)
    GetOrCreateBranch(ctx context.Context, name, from string, meta map[string]any) (Branch, error)
    DeleteBranch(ctx context.Context, name string) error
    Merge(ctx context.Context, from, into string, opts MergeOptions) (*MergeResult, error)

    GetCommit(ctx context.Context, id string) (*CommitInfo, error)
    LoadHistory(ctx context.Context) (History, error)

    GC(ctx context.Context, opts GCOptions) (*GCResult, error)
    Close() error
}
```

`Workspace` embeds `WorkspaceFS`, so all file operations (Open, Create, Remove, etc.) are available directly on the workspace object and operate on the bound branch.

### Metadata

#### `ID() string`

Returns the workspace ID (a content-hash string assigned by the Tai side).

#### `Meta() Meta`

Returns the workspace metadata, cached from the initial `Open` or `Create` call.

#### `UpdateMeta(ctx, meta)`

```go
func (ws) UpdateMeta(ctx context.Context, meta Meta) error
```

Updates the workspace metadata on the Tai side and caches the new value locally.

| Parameter | Type | Description |
|-----------|------|-------------|
| `meta` | `Meta` | New metadata to set |

**Wire:** `ws.update_meta` with `WSRequest{WorkspaceID, Meta}`.

### Branch Management

#### `Main(ctx)`

```go
func (ws) Main(ctx context.Context) (Branch, error)
```

Shorthand for `GetBranch(ctx, "main")`.

#### `ListBranches(ctx)`

```go
func (ws) ListBranches(ctx context.Context) ([]BranchInfo, error)
```

Returns all branches in the workspace.

**Wire:** `list_branches` with `BranchMgmtRequest{WorkspaceID}`, returns `[]BranchInfo`.

#### `CreateBranch(ctx, name, from, meta)`

```go
func (ws) CreateBranch(ctx context.Context, name, from string, meta map[string]any) (Branch, error)
```

Creates a new branch.

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | `string` | New branch name |
| `from` | `string` | Source branch to fork from |
| `meta` | `map[string]any` | Optional branch metadata; `nil` for none |

**Wire:** `create_branch` with `BranchMgmtRequest{WorkspaceID, Name, From, Meta}`, returns `BranchInfo`.

```go
branch, err := ws.CreateBranch(ctx, "feature-x", "main", map[string]any{
    "author": "alice",
    "ticket": "PROJ-42",
})
```

#### `GetBranch(ctx, name)`

```go
func (ws) GetBranch(ctx context.Context, name string) (Branch, error)
```

Gets an existing branch by name. Internally fetches the branch HEAD commit.

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | `string` | Branch name |

**Wire:** `branch.head` with `BranchRequest{WorkspaceID, Branch}`, returns `CommitInfo`.

#### `GetOrCreateBranch(ctx, name, from, meta)`

```go
func (ws) GetOrCreateBranch(ctx context.Context, name, from string, meta map[string]any) (Branch, error)
```

Idempotent variant: returns the existing branch if it exists, otherwise creates it from `from`. Parameters are identical to `CreateBranch`.

**Wire:** `get_or_create_branch` with `BranchMgmtRequest{WorkspaceID, Name, From, Meta}`, returns `BranchInfo`.

#### `DeleteBranch(ctx, name)`

```go
func (ws) DeleteBranch(ctx context.Context, name string) error
```

Deletes a branch.

**Wire:** `delete_branch` with `BranchMgmtRequest{WorkspaceID, Name}`.

#### `Merge(ctx, from, into, opts)`

```go
func (ws) Merge(ctx context.Context, from, into string, opts MergeOptions) (*MergeResult, error)
```

Merges one branch into another.

| Parameter | Type | Description |
|-----------|------|-------------|
| `from` | `string` | Source branch name |
| `into` | `string` | Target branch name |
| `opts` | `MergeOptions` | Merge configuration |

**`MergeOptions` fields:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `DeleteSource` | `bool` | `"delete_source,omitempty"` | Delete source branch after merge |
| `Message` | `string` | `"message,omitempty"` | Merge commit message |

**Returns `*MergeResult`:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `CommitID` | `string` | `"commit_id"` | Merge commit ID |
| `Conflicts` | `[]string` | `"conflicts,omitempty"` | Conflicting file paths, if any |

**Wire:** `merge` with `BranchMgmtRequest{WorkspaceID, From, Into, Opts}`.

```go
result, err := ws.Merge(ctx, "feature-x", "main", workspace.MergeOptions{
    DeleteSource: true,
    Message:      "merge feature-x into main",
})
fmt.Println(result.CommitID)
fmt.Println(result.Conflicts) // nil if clean merge
```

### History

#### `GetCommit(ctx, id)`

```go
func (ws) GetCommit(ctx context.Context, id string) (*CommitInfo, error)
```

Retrieves a single commit by ID.

**Wire:** `ws.get_commit` with `WSCommitRequest{WorkspaceID, CommitID}`, returns `CommitInfo`.

#### `LoadHistory(ctx)`

```go
func (ws) LoadHistory(ctx context.Context) (History, error)
```

Returns a `History` query interface bound to this workspace. This is a lightweight operation; the actual queries are sent on demand (see [History Interface](#history-interface)).

### Maintenance

#### `GC(ctx, opts)`

```go
func (ws) GC(ctx context.Context, opts GCOptions) (*GCResult, error)
```

Runs garbage collection on the workspace.

**`GCOptions` fields:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `DryRun` | `bool` | `"dry_run,omitempty"` | If true, report what would be reclaimed without deleting |

**Returns `*GCResult`:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `ReclaimedBytes` | `int64` | `"reclaimed_bytes"` | Bytes freed |
| `ReclaimedBlobs` | `int` | `"reclaimed_blobs"` | Number of blobs removed |

**Wire:** `ws.gc` with `WSGCRequest{WorkspaceID, Opts}`.

#### `Close()`

```go
func (ws) Close() error
```

Closes the workspace. Currently a no-op; reserved for future resource cleanup.

## WorkspaceFS Interface

```go
type WorkspaceFS interface {
    fs.FS                                                  // Open(name) (fs.File, error)
    Create(name string) (io.WriteCloser, error)
    Remove(name string) error
    RemoveAll(name string) error
    Rename(oldname, newname string) error
    MkdirAll(name string, perm fs.FileMode) error
    Symlink(oldname, newname string) error
    Watch(patterns []string) (Watcher, error)
    Exists(name string) (bool, error)                      // check file/directory existence
    WriteArchive(dir string, r io.Reader) error            // extract tar.gz/zip into dir
    ReadManifest(dir string) (map[string]string, error)    // read manifest.json hash map
}
```

The `WorkspaceFS` interface embeds `fs.FS` (which defines `Open` only). The concrete implementation additionally satisfies `fs.StatFS` and `fs.ReadDirFS`, so `fs.Stat(ws, path)` and `fs.ReadDir(ws, path)` work on any `Workspace` or branch `FS()` value.

### Open

```go
func (wfs) Open(name string) (fs.File, error)
```

Opens a file for reading. The transport channel is selected by `ChannelPolicy`:

- **Light:** Opens a Light stream with the `open` command; file content is streamed directly.
- **Heavy:** Picks a Heavy connection, writes the command frame, reads a result frame with the file size, then reads exactly that many bytes via `LimitReader`. Falls back to Light on failure.

The returned file implements **`fs.File` + `io.ReadSeeker`**. Consumer code is identical to operating on a local `*os.File`:

```go
f, _ := bfs.Open("video.mp4")
fi, _ := f.Stat()               // lazy roundtrip, result cached
rs := f.(io.ReadSeeker)          // always succeeds
http.ServeContent(w, r, fi.Name(), fi.ModTime(), rs)
f.Close()
```

**Seek behavior:**

- `Seek` only records the logical offset — no network call, no stream close.
- `Read` checks whether the underlying stream position matches the logical offset. If they match, the existing stream is reused. If not, the old stream is closed and a new one is opened at the requested offset (via the `Offset` field in `FileRequest`).
- `Stat` performs a single network roundtrip on first call and caches the full `fs.FileInfo`.

This design avoids wasting the initial stream. For example, `http.ServeContent` calls `Seek(0, End)` → `Seek(0, Start)` → `Read` — the stream position is still 0 on `Read`, so the initial stream is reused with zero overhead.

For Range requests (e.g., video seek to 5 MB), `http.ServeContent` calls `Seek(5000000, Start)` → `Read` — the stream position (0) does not match the offset (5000000), so a new stream is opened at offset 5000000. Only the requested interval is transmitted.

**Wire:** `open` with `FileRequest{WorkspaceID, Branch, Path, Offset}`.

**Heavy open protocol:**

```text
SDK → Tai:  WriteFrame({namespace:"workspace", kind:"open", data: FileRequest})
Tai → SDK:  WriteFrame({namespace:"workspace", kind:"open.result", data: {"size": N}})
Tai → SDK:  N bytes of file data (io.CopyN)
```

On `Close()`, the Heavy file probes for EOF (reads 1 byte from the `LimitReader`):
- EOF → connection returned to pool (`HeavyReturn`)
- Not EOF → connection destroyed (`HeavyRemove`), pool auto-replenishes

### Create

```go
func (wfs) Create(name string) (io.WriteCloser, error)
```

Creates or overwrites a file. Returns a writer; call `Write` to send content, then `Close` to finalize. **Create always uses the Light channel** — the data flows from SDK to Tai, so the SDK sends `END_STREAM` via `CloseWrite` and there is no H2 deadlock risk.

On `Close()`, if the underlying stream implements `HalfCloser`, the writer half-closes the write side and reads the `create.result` response to detect server-side errors.

**Wire:** `create` with `FileRequest{WorkspaceID, Branch, Path}`.

```go
w, err := ws.Create("output/result.json")
if err != nil { ... }
w.Write([]byte(`{"status": "ok"}`))
err = w.Close() // may return server-side error
```

### Stat

```go
func (wfs) Stat(name string) (fs.FileInfo, error)
```

Returns file metadata. Also accessible via `fs.Stat(ws, name)`.

**Response fields:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Name` | `string` | `"name"` | Base name |
| `Size` | `int64` | `"size"` | Size in bytes |
| `Mode` | `uint32` | `"mode"` | File mode bits |
| `ModTime` | `int64` | `"mod_time"` | Modification time (unix timestamp) |
| `IsDir` | `bool` | `"is_dir"` | Whether it is a directory |

**Wire:** `stat` with `FileRequest{WorkspaceID, Branch, Path}`.

```go
info, err := fs.Stat(ws, "config.json")
fmt.Println(info.Name(), info.Size(), info.ModTime(), info.IsDir())
```

### ReadDir

```go
func (wfs) ReadDir(name string) ([]fs.DirEntry, error)
```

Lists directory contents. Also accessible via `fs.ReadDir(ws, name)`.

Each entry in the response has:

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Name` | `string` | `"name"` | Entry name |
| `IsDir` | `bool` | `"is_dir"` | Whether it is a directory |
| `Type` | `string` | `"type"` | File type hint |

**Wire:** `read_dir` with `FileRequest{WorkspaceID, Branch, Path}`.

```go
entries, err := fs.ReadDir(ws, "src")
for _, e := range entries {
    fmt.Println(e.Name(), e.IsDir())
}
```

### Remove

```go
func (wfs) Remove(name string) error
```

Deletes a single file.

**Wire:** `remove` with `FileRequest{WorkspaceID, Branch, Path}`.

### RemoveAll

```go
func (wfs) RemoveAll(name string) error
```

Recursively deletes a file or directory tree.

**Wire:** `remove_all` with `FileRequest{WorkspaceID, Branch, Path}`.

### Rename

```go
func (wfs) Rename(oldname, newname string) error
```

Renames or moves a file.

**Wire:** `rename` with `RenameRequest{WorkspaceID, Branch, Old, New}`.

### MkdirAll

```go
func (wfs) MkdirAll(name string, perm fs.FileMode) error
```

Creates a directory tree, including all intermediate directories.

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | `string` | Directory path |
| `perm` | `fs.FileMode` | Permission bits (sent as `uint32` on the wire) |

**Wire:** `mkdir_all` with `FileRequest{WorkspaceID, Branch, Path, Perm}`.

### Symlink

```go
func (wfs) Symlink(oldname, newname string) error
```

Creates a symbolic link at `newname` pointing to `oldname`.

**Wire:** `symlink` with `RenameRequest{WorkspaceID, Branch, Old, New}`.

### Watch

```go
func (wfs) Watch(patterns []string) (Watcher, error)
```

Starts watching for file changes matching the given glob patterns. Opens a long-lived Light stream; events are pushed as `watch.event` frames.

**Wire:** `watch` with `WatchRequest{WorkspaceID, Branch, Patterns}`.

**Returns `Watcher`:**

```go
type Watcher interface {
    Events() <-chan WatchEvent
    Close() error
}
```

`Events()` returns a buffered channel (capacity 64). The receive loop runs in a background goroutine and closes the channel when the stream ends or `Close()` is called.

**`WatchEvent`:**

| Field | Type | Description |
|-------|------|-------------|
| `Type` | `WatchEventType` | One of `"create"`, `"write"`, `"remove"`, `"rename"` |
| `Path` | `string` | Affected file path |

```go
watcher, err := ws.Watch([]string{"*.go", "src/**"})
if err != nil { ... }
defer watcher.Close()

for ev := range watcher.Events() {
    fmt.Printf("[%s] %s\n", ev.Type, ev.Path)
}
```

## Exists / WriteArchive / ReadManifest

Three methods on the `WorkspaceFS` interface for file existence checks, archive bulk writes, and manifest queries. Available on any `WorkspaceFS` value, including `Workspace` itself and `branch.FS()`.

### Exists

```go
Exists(name string) (bool, error)
```

Reports whether a file or directory exists at the given path.

**Wire:** `exists` command (File operations group). Request payload: `FileRequest{workspace_id, branch, path}`. Response: `{"exists":true/false}`.

```go
ok, err := ws.Exists(".skills/my-skill")
if err != nil { ... }
if ok {
    fmt.Println("skill already deployed")
}
```

### WriteArchive

```go
WriteArchive(dir string, r io.Reader) error
```

Streams a tar.gz or zip archive to the Tai side, which extracts it into the target directory. The **SDK** auto-detects the format from the first two bytes of `r` (`0x1f 0x8b` = gzip, `0x50 0x4b` = zip) and sends the `format` field to Tai.

When writing under `.skills/`, Tai automatically updates `.skills/manifest.json` with the SHA-256 hash of the archive.

**Wire:** `write_archive` command. Request payload: `ArchiveRequest{workspace_id, branch, dir, format}`. Body is streamed over the Light stream.

**ArchiveRequest (wire type):**

```go
type ArchiveRequest struct {
    WorkspaceID string `json:"workspace_id"`
    Branch      string `json:"branch"`
    Dir         string `json:"dir"`    // destination directory, e.g. ".skills/bots/postman"
    Format      string `json:"format"` // "tar.gz" or "zip" (auto-detected by SDK)
}
```

**Security:**
- Path traversal guard: `filepath.Clean(target)` must stay within the branch directory
- Zip-slip guard: extracted file paths must not escape the destination directory

```go
archive, _ := os.Open("skills-pack.tar.gz")
defer archive.Close()

err := ws.WriteArchive(".skills/code-review", archive)
```

### ReadManifest

```go
ReadManifest(dir string) (map[string]string, error)
```

Reads the `manifest.json` under the given directory. Returns `map[relPath]hash` for SDK-side `SyncSkills` incremental comparison. Returns an empty map (not an error) when no manifest file exists on the Tai side.

**Wire:** `read_manifest` command. Request payload: `FileRequest{workspace_id, branch, path}`. Response: JSON `map[string]string`.

```go
manifest, err := ws.ReadManifest(".skills")
if err != nil { ... }
for name, hash := range manifest {
    fmt.Printf("skill=%s hash=%s\n", name, hash)
}
```

## Context API

Four-level context scope model for managing hierarchical Agent Instructions composition.

### Data Model

```go
type ContextScope string

const (
    ScopeBot     ContextScope = "bot"     // runtime instance defaults
    ScopeHome    ContextScope = "home"    // user-level preferences
    ScopeBoard   ContextScope = "board"   // board/project shared context
    ScopeSession ContextScope = "session" // single session context
)

type ContextEntry struct {
    Scope ContextScope
    ID    string // e.g. botID, userID, boardID, sessionID
}

type ContextFiles struct {
    Instruction []byte // INSTRUCTION.md content (nil if absent)
    Memory      []byte // MEMORY.md content (nil if absent)
    Preference  []byte // PREFERENCE.md content (nil if absent)
}
```

Each scope stores exactly 3 files (`INSTRUCTION.md`, `MEMORY.md`, `PREFERENCE.md`) under `.context/{scope}/{id}/`.

`ContextFiles` provides two methods:
- `Empty() bool` — whether all three fields are nil
- `FormatSection(label string) string` — formats non-nil files as a Markdown section (`## label` with each file as `### filename`)

### ContextDir

```go
func ContextDir(scope ContextScope, id string) string
```

Returns the context directory path: `.context/{scope}/{id}`.

### SeedContext

```go
func SeedContext(fsys types.WorkspaceFS, scope ContextScope, id string, files map[string][]byte) error
```

Initializes context files for a given scope. Creates the directory and writes each file, **skipping files that already exist** (designed for seeding bot defaults on first run without overwriting files the agent may have updated).

```go
err := workspace.SeedContext(bfs, workspace.ScopeBot, "default", map[string][]byte{
    "INSTRUCTION.md": []byte("You are a helpful assistant."),
    "MEMORY.md":      []byte(""),
    "PREFERENCE.md":  []byte("Be concise and direct."),
})
```

### ReadContext

```go
func ReadContext(fsys fs.FS, scope ContextScope, id string) (*ContextFiles, error)
```

Reads the 3 standard files from a scope directory. Missing files result in nil fields (not errors). Accepts `fs.FS` (not `WorkspaceFS`), so any `WorkspaceFS` value (which satisfies `fs.FS`) can be passed directly.

### MergeContext

```go
func MergeContext(fsys fs.FS, entries []ContextEntry) (string, error)
```

Reads multiple scope directories in order and **concatenates** them into a single Markdown string. Entries are ordered from lowest to highest priority (typically bot → home → board → session). Each non-empty scope produces a `## scope/id` section. Empty scopes (all three files absent) are skipped.

```go
merged, err := workspace.MergeContext(bfs, []workspace.ContextEntry{
    {Scope: workspace.ScopeBot, ID: "default"},
    {Scope: workspace.ScopeHome, ID: "user-123"},
    {Scope: workspace.ScopeBoard, ID: "project-abc"},
    {Scope: workspace.ScopeSession, ID: "sess-xyz"},
})
// merged can be set as metadata["_agents_md"]
```

## Skills API

Manifest-based incremental skill synchronization.

### SkillPack

```go
type SkillPack struct {
    Key     string    // manifest key, e.g. "bots/postman", "home", "board/dashboard-x"
    Hash    string    // pre-computed hash via SkillHash ("sha256:hex...")
    Archive io.Reader // tar.gz or zip archive; only read when hash differs
}
```

Callers should compute the hash with `SkillHash` before constructing `SkillPack` to avoid consuming the `Archive` Reader twice.

### SkillHash

```go
func SkillHash(r io.Reader) (string, error)
```

Computes the SHA-256 hash of a skill archive. Returns `"sha256:<hex>"`, matching the format used by Tai's `handleWriteArchive` for manifest comparison.

### SyncSkills

```go
func SyncSkills(fsys types.WorkspaceFS, packs []SkillPack) (int, error)
```

Incrementally syncs skills to the `.skills/` directory. Returns the number of packs actually pushed:
1. `ReadManifest(".skills")` fetches the remote hash map
2. Compares each `pack.Hash` against `manifest[pack.Key]`
3. Matching hash → skip; different → `RemoveAll` old content → `WriteArchive` new archive
4. Returns `(pushed, nil)`

```go
packs := []workspace.SkillPack{
    {Key: "bots/postman", Hash: postmanHash, Archive: postmanArchive},
    {Key: "home",         Hash: homeHash,    Archive: homeArchive},
}
pushed, err := workspace.SyncSkills(ws, packs)
fmt.Printf("pushed %d skills\n", pushed)
```

> **Note:** SyncSkills integration tests require a live transport connection and are deferred to the demo app phase. Only `SkillHash` unit tests exist currently.

## Branch Interface

```go
type Branch interface {
    FS() WorkspaceFS
    Name() string
    Head(ctx context.Context) (*CommitInfo, error)
    Meta() map[string]any
    UpdateMeta(ctx context.Context, meta map[string]any) error
    Commit(ctx context.Context, message string) (*CommitInfo, error)
    Diff(ctx context.Context, from, to string) ([]FileDiff, error)
    Rollback(ctx context.Context, commitID string) error
    AcquireWrite(ctx context.Context, holder string) error
    ReleaseWrite(ctx context.Context) error
}
```

### `FS()`

Returns a branch-scoped `WorkspaceFS`. File operations on this FS target the branch, not the workspace's bound branch.

```go
branch, _ := ws.GetBranch(ctx, "feature-x")
bfs := branch.FS()
f, _ := bfs.Open("README.md")
w, _ := bfs.Create("new-file.txt")
watcher, _ := bfs.Watch([]string{"*.go"})
```

### `Name()`

Returns the branch name.

### `Head(ctx)`

Fetches the current HEAD commit from the Tai side.

**Wire:** `branch.head` with `BranchRequest{WorkspaceID, Branch}`, returns `CommitInfo`.

### `Meta()` / `UpdateMeta(ctx, meta)`

`Meta()` returns the cached branch metadata (`map[string]any`).

`UpdateMeta` replaces the branch metadata on the Tai side and updates the local cache.

**Wire:** `branch.update_meta` with `{workspace_id, branch, meta}`.

```go
err := branch.UpdateMeta(ctx, map[string]any{
    "author": "alice",
    "status": "review",
})
```

### `Commit(ctx, message)`

Creates a commit on the branch with the given message.

| Parameter | Type | Description |
|-----------|------|-------------|
| `message` | `string` | Commit message |

**Returns `*CommitInfo`** with the new commit's `ID`, `Parent`, `Message`, and `Time`.

**Wire:** `commit` with `BranchRequest{WorkspaceID, Branch, Message}`.

```go
commit, err := branch.Commit(ctx, "add feature implementation")
fmt.Println(commit.ID, commit.Time)
```

### `Diff(ctx, from, to)`

Returns file-level differences between two commit IDs.

| Parameter | Type | Description |
|-----------|------|-------------|
| `from` | `string` | Base commit ID |
| `to` | `string` | Target commit ID |

**Returns `[]FileDiff`:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Path` | `string` | `"path"` | File path |
| `Type` | `string` | `"type"` | `"add"`, `"modify"`, `"delete"`, or `"rename"` |
| `OldPath` | `string` | `"old_path,omitempty"` | Previous path (rename only) |

**Wire:** `diff` with `BranchRequest{WorkspaceID, Branch, From, To}`.

### `Rollback(ctx, commitID)`

Rolls the branch back to the given commit.

**Wire:** `rollback` with `BranchRequest{WorkspaceID, Branch, CommitID}`.

### `AcquireWrite(ctx, holder)` / `ReleaseWrite(ctx)`

Exclusive write lock on the branch. Only one holder can write at a time.

| Parameter | Type | Description |
|-----------|------|-------------|
| `holder` | `string` | Holder identifier (e.g., session ID) |

**Wire:** `branch.acquire_write` with `BranchRequest{WorkspaceID, Branch, Holder}`; `branch.release_write` with `BranchRequest{WorkspaceID, Branch}`.

```go
err := branch.AcquireWrite(ctx, "session-42")
defer branch.ReleaseWrite(ctx)
// ... write operations ...
```

## History Interface

```go
type History interface {
    Query(opts HistoryOptions) ([]CommitEntry, error)
    FileHistory(path string, opts FileHistoryOptions) ([]FileChange, error)
    BranchLog(branch string, opts LogOptions) ([]CommitEntry, error)
}
```

Obtained via `ws.LoadHistory(ctx)`. Each method sends its own request to the Tai side.

### `Query(opts)`

Queries commits across the workspace.

**`HistoryOptions` fields:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Limit` | `int` | `"limit,omitempty"` | Maximum number of entries; 0 = unlimited |
| `Before` | `int64` | `"before,omitempty"` | Unix timestamp upper bound; 0 = no filter |
| `After` | `int64` | `"after,omitempty"` | Unix timestamp lower bound; 0 = no filter |

**Returns `[]CommitEntry`:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `ID` | `string` | `"id"` | Commit ID |
| `Parent` | `string` | `"parent,omitempty"` | Parent commit ID |
| `Message` | `string` | `"message"` | Commit message |
| `Time` | `int64` | `"time"` | Unix timestamp |
| `Branch` | `string` | `"branch"` | Branch name |

**Wire:** `ws.history` with `WSHistoryRequest{WorkspaceID, Opts}`.

```go
history, _ := ws.LoadHistory(ctx)
entries, err := history.Query(workspace.HistoryOptions{
    Limit:  50,
    Before: time.Now().Unix(),
    After:  time.Now().Add(-24 * time.Hour).Unix(),
})
```

### `FileHistory(path, opts)`

Queries change history for a single file.

**`FileHistoryOptions` fields:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Limit` | `int` | `"limit,omitempty"` | Maximum entries; 0 = unlimited |

**Returns `[]FileChange`:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `CommitID` | `string` | `"commit_id"` | Commit that changed this file |
| `Path` | `string` | `"path"` | File path |
| `Type` | `string` | `"type"` | `"add"`, `"modify"`, or `"delete"` |
| `Time` | `int64` | `"time"` | Unix timestamp |

**Wire:** `ws.file_history` with `WSHistoryRequest{WorkspaceID, Path, Opts}`.

```go
changes, err := history.FileHistory("src/main.go", workspace.FileHistoryOptions{Limit: 20})
```

### `BranchLog(branch, opts)`

Queries the commit log for a specific branch.

**`LogOptions` fields:**

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Limit` | `int` | `"limit,omitempty"` | Maximum entries; 0 = unlimited |
| `Before` | `int64` | `"before,omitempty"` | Unix timestamp upper bound; 0 = no filter |

**Wire:** `ws.branch_log` with `WSHistoryRequest{WorkspaceID, Branch, Opts}`.

```go
log, err := history.BranchLog("main", workspace.LogOptions{Limit: 100})
```

## Channel Policy

```go
type ChannelPolicy struct {
    SizeThreshold int64    // files > threshold use Heavy (default 1 MB)
    TextExts      []string // always Light
    BinaryExts    []string // always Heavy
}
```

### Selection Logic

`channelForPath(filePath, size)` applies these rules in order:

1. If the file extension (lowercased) is in `TextExts` → **Light**
2. If the file extension (lowercased) is in `BinaryExts` → **Heavy**
3. If the extension is unknown and `size < 0` (unknown) → **Light**
4. If the extension is unknown and `size > SizeThreshold` → **Heavy**
5. Otherwise → **Light**

Metadata-only operations (branch management, history queries, etc.) always use Light.

### Default Policy

`DefaultPolicy()` returns:

| Field | Value |
|-------|-------|
| `SizeThreshold` | `1 << 20` (1 MB) |
| `TextExts` | `.go` `.ts` `.js` `.json` `.yaml` `.yml` `.md` `.txt` `.css` `.html` `.xml` `.csv` `.sql` `.sh` `.py` `.rb` `.rs` `.c` `.h` `.java` `.toml` `.ini` `.cfg` `.conf` `.jsx` `.tsx` `.vue` `.svelte` `.sass` `.scss` `.less` |
| `BinaryExts` | `.bin` `.exe` `.dll` `.so` `.dylib` `.zip` `.tar` `.gz` `.bz2` `.xz` `.zst` `.png` `.jpg` `.jpeg` `.gif` `.webp` `.bmp` `.ico` `.svg` `.mp4` `.avi` `.mkv` `.mov` `.webm` `.mp3` `.wav` `.flac` `.ogg` `.pdf` `.doc` `.docx` `.xls` `.xlsx` `.ppt` `.pptx` `.wasm` `.o` `.a` `.lib` |

### Heavy Fallback

When a Heavy connection is selected but `HeavyPick` fails, `writePayload` fails, or `readPayload` fails, the operation automatically falls back to Light. Callers do not need to handle this.

### Heavy Open Protocol

Heavy open uses a structured two-phase protocol (command frame → result frame + data):

1. SDK picks an idle Heavy connection and sends a command frame (`open` kind)
2. Tai reads the frame via `serveHeavy`, dispatches to `handleHeavyOpen`
3. Tai opens the file, stats it, optionally seeks to `Offset`, then sends a result frame with `{"size": N}`
4. Tai streams exactly `N` bytes of file data via `io.CopyN`
5. SDK reads the result frame, creates a `LimitReader(conn, N)` to bound the read
6. On `Close()`, the EOF probe determines connection reuse (see [Open](#open))

Error responses use the `"error"` kind with `{"code": "...", "message": "..."}`. After an error frame, the connection is returned to the pool (no data follows).

Supported error codes: `BAD_REQUEST`, `NOT_FOUND`, `OPEN_FAILED`, `STAT_FAILED`, `OFFSET_OUT_OF_RANGE`, `SEEK_UNSUPPORTED`, `SEEK_FAILED`, `UNKNOWN_KIND`.

## Error Handling

Remote errors are returned as `*ErrorResponse`:

```go
type ErrorResponse struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}

func (e *ErrorResponse) Error() string  // "code: message"
```

Use `errors.As` to detect remote errors:

```go
n := node.Get("node-1")
ws, err := n.OpenWorkspace(ctx, "nonexistent")
if err != nil {
    var errResp *workspace.ErrorResponse  // workspace.ErrorResponse stays in workspace/
    if errors.As(err, &errResp) {
        fmt.Printf("remote error: code=%s message=%s\n", errResp.Code, errResp.Message)
    }
}
```

## Data Types

### Meta

```go
type Meta struct {
    Name  string `json:"name"`
    Owner string `json:"owner"`
}
```

### BranchInfo

```go
type BranchInfo struct {
    Name string         `json:"name"`
    Head string         `json:"head"`           // commit ID
    Meta map[string]any `json:"meta,omitempty"`
}
```

### CommitInfo

```go
type CommitInfo struct {
    ID      string `json:"id"`
    Parent  string `json:"parent,omitempty"`
    Message string `json:"message"`
    Time    int64  `json:"time"`              // unix timestamp
}
```

### RegistryEntry

```go
type RegistryEntry struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    NodeID    string `json:"node_id,omitempty"`
    Owner     string `json:"owner"`
    CreatedAt string `json:"created_at,omitempty"`
    UpdatedAt string `json:"updated_at,omitempty"`
}
```

`NodeID` identifies the Tai node that created this workspace, used for node-level isolation when multiple nodes share the same machine.

## Wire Protocol

All commands use the `"workspace"` namespace. Frame format: **4-byte big-endian length prefix + Protobuf `Payload`**. Responses use `<kind>.result` suffix; errors use `"error"` kind.

### 36 Command Kinds

**File operations (10):**

| Kind | Wire Request Type | Description |
|------|-------------------|-------------|
| `open` | `FileRequest` | Open file for reading |
| `create` | `FileRequest` | Create/overwrite file |
| `stat` | `FileRequest` | File metadata |
| `exists` | `FileRequest` | Check file/directory existence |
| `read_dir` | `FileRequest` | List directory |
| `remove` | `FileRequest` | Delete file |
| `remove_all` | `FileRequest` | Recursive delete |
| `rename` | `RenameRequest` | Rename/move |
| `mkdir_all` | `FileRequest` | Create directory tree |
| `symlink` | `RenameRequest` | Create symbolic link |

**Branch info + version control (8):**

| Kind | Wire Request Type | Description |
|------|-------------------|-------------|
| `branch.head` | `BranchRequest` | Get HEAD commit |
| `branch.meta` | `BranchRequest` | Get branch metadata |
| `branch.update_meta` | `{workspace_id, branch, meta}` | Update branch metadata |
| `branch.acquire_write` | `BranchRequest` | Acquire write lock |
| `branch.release_write` | `BranchRequest` | Release write lock |
| `commit` | `BranchRequest` | Create commit |
| `diff` | `BranchRequest` | Diff between commits |
| `rollback` | `BranchRequest` | Rollback to commit |

**Workspace CRUD (10):**

| Kind | Wire Request Type | Description |
|------|-------------------|-------------|
| `ws.create` | `WSCreateRequest` | Create workspace |
| `ws.open` | `WSRequest` | Open workspace |
| `ws.list` | `WSListRequest` | List workspaces |
| `ws.delete` | `WSRequest` | Delete workspace |
| `ws.update_meta` | `WSRequest` | Update workspace metadata |
| `ws.get_commit` | `WSCommitRequest` | Get commit by ID |
| `ws.history` | `WSHistoryRequest` | Query commit history |
| `ws.file_history` | `WSHistoryRequest` | Query file history |
| `ws.branch_log` | `WSHistoryRequest` | Query branch log |
| `ws.gc` | `WSGCRequest` | Garbage collection |

**Branch management (5):**

| Kind | Wire Request Type | Description |
|------|-------------------|-------------|
| `list_branches` | `BranchMgmtRequest` | List branches |
| `create_branch` | `BranchMgmtRequest` | Create branch |
| `get_or_create_branch` | `BranchMgmtRequest` | Get or create branch |
| `delete_branch` | `BranchMgmtRequest` | Delete branch |
| `merge` | `BranchMgmtRequest` | Merge branches |

**Archives + Manifest (2):**

| Kind | Wire Request Type | Description |
|------|-------------------|-------------|
| `write_archive` | `ArchiveRequest` | Write tar.gz/zip archive to directory |
| `read_manifest` | `FileRequest` | Read manifest.json hash map |

**Long-lived (1):**

| Kind | Wire Request Type | Description |
|------|-------------------|-------------|
| `watch` | `WatchRequest` | File change notifications |

### Wire Request Types

```go
type FileRequest struct {
    WorkspaceID string `json:"workspace_id"`
    Branch      string `json:"branch"`
    Path        string `json:"path,omitempty"`
    Perm        uint32 `json:"perm,omitempty"`
    Offset      int64  `json:"offset,omitempty"` // byte offset for open (Seek support)
}

type RenameRequest struct {
    WorkspaceID string `json:"workspace_id"`
    Branch      string `json:"branch"`
    Old         string `json:"old"`
    New         string `json:"new"`
}

type BranchRequest struct {
    WorkspaceID string `json:"workspace_id"`
    Branch      string `json:"branch"`
    Holder      string `json:"holder,omitempty"`
    Message     string `json:"message,omitempty"`
    From        string `json:"from,omitempty"`
    To          string `json:"to,omitempty"`
    CommitID    string `json:"commit_id,omitempty"`
}

type BranchMgmtRequest struct {
    WorkspaceID string         `json:"workspace_id"`
    Name        string         `json:"name,omitempty"`
    From        string         `json:"from,omitempty"`
    Into        string         `json:"into,omitempty"`
    Meta        map[string]any `json:"meta,omitempty"`
    Opts        *MergeOptions  `json:"opts,omitempty"`
}

type WSCreateRequest struct {
    Path string `json:"path"`
    Meta Meta   `json:"meta"`
}

type WSRequest struct {
    WorkspaceID string `json:"workspace_id,omitempty"`
    Meta        *Meta  `json:"meta,omitempty"`
}

type WSCommitRequest struct {
    WorkspaceID string `json:"workspace_id"`
    CommitID    string `json:"commit_id"`
}

type WSHistoryRequest struct {
    WorkspaceID string          `json:"workspace_id"`
    Branch      string          `json:"branch,omitempty"`
    Path        string          `json:"path,omitempty"`
    Opts        json.RawMessage `json:"opts,omitempty"`
}

type WSListRequest struct {
    Opts ListOptions `json:"opts"`
}

type WSGCRequest struct {
    WorkspaceID string    `json:"workspace_id"`
    Opts        GCOptions `json:"opts"`
}

type WatchRequest struct {
    WorkspaceID string   `json:"workspace_id"`
    Branch      string   `json:"branch"`
    Patterns    []string `json:"patterns"`
}

type ArchiveRequest struct {
    WorkspaceID string `json:"workspace_id"`
    Branch      string `json:"branch"`
    Dir         string `json:"dir"`
}
```

See the design document for protocol details: [05-channels-workspace.md](https://github.com/yaoapp/tai/blob/main/docs/v2/05-channels-workspace.md)
