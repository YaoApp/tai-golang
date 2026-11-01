# API Reference

## Server

```go
func NewServer(cfg *ServerConfig) (Server, error)
```

### ServerConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `TransportPort` | `int` | `6099` | Transport (H3/H2) listen port |
| `HTTPPort` | `int` | `5099` | HTTP API listen port |
| `Auth` | `AuthProvider` | required | Authentication provider |
| `TLS` | `*TLSConfig` | `nil` | TLS cert/key (`nil` = plaintext) |
| `MaxFrameSize` | `int` | `16 MiB` | Maximum frame payload size (range: 64 KiB – 64 MiB) |
| `UnauthTimeout` | `Duration` | `5s` | Unauthenticated connection timeout |
| `QUIC` | `*quic.Config` | `nil` | Custom QUIC transport settings (H3 only) |
| `ShutdownTimeout` | `Duration` | `5s` | Graceful shutdown timeout |
| `Pool` | `*PoolSettings` | defaults | Light/Heavy pool configuration |

### Server Interface

| Method | Description |
|--------|-------------|
| `Start(ctx)` | Start and block until shutdown |
| `Addr()` | TCP listen address |
| `RegisterTool(tool)` | Register a server-side tool (not implemented) |
| `UnregisterTool(name)` | Unregister a server-side tool (not implemented) |
| `Close()` | Shutdown (idempotent) |

## RemoteNode

Node management uses the `node` package-level functions. After a Tai node connects and registers, it becomes a `RemoteNode` with workspace, session, and control operations.

```go
import "github.com/yaoapp/tai-golang/node"

// Callbacks
node.OnRegister(func(n types.RemoteNode) error { ... })
node.OnDisconnect(func(nodeID string) { ... })

// Lookup
n := node.Get("node-1")   // returns types.RemoteNode or nil
all := node.List()         // returns []types.RemoteNode
```

### RemoteNode Interface

| Method | Description |
|--------|-------------|
| `Info()` | Returns the `*Node` data struct |
| `Online()` | Whether the node is connected |
| `OpenWorkspace(ctx, wsID, branch...)` | Open an existing workspace |
| `CreateWorkspace(ctx, meta)` | Create a new workspace |
| `ListWorkspaces(ctx, opts)` | List workspaces |
| `DeleteWorkspace(ctx, wsID)` | Delete a workspace |
| `OpenSession(ctx, req)` | Open a session |
| `ResumeSession(ctx, req)` | Resume a session (not implemented) |
| `SendControl(msg)` | Send a control message |
| `GoAway(reason, reconnectAfter)` | Ask the node to disconnect and reconnect |

## Session (17 methods)

```go
n := node.Get("node-1")
session, err := n.OpenSession(ctx, &types.SessionOpenRequest{
    SessionID: "sess-001",
    Token:     operationToken,
    Request:   &types.SessionRequest{Runtime: "agent"},
})
```

### Lifecycle

| Method | Description |
|--------|-------------|
| `ID()` | Session identifier |
| `NodeID()` | Node running this session |
| `Status()` | `opening` / `running` / `closing` / `closed` |
| `Close(ctx)` | Close the session |

### Commands

| Method | Description |
|--------|-------------|
| `Prompt(msg)` | Send user input, start a turn |
| `Followup(msg)` | Append input (returns `Disposition`) |
| `Cancel()` | Cancel current turn |
| `Steer(msg)` | Guide direction (returns `Disposition`) |
| `UpdateQueue(action)` | Queue management (`remove` / `edit` / `clear` / `promote`) |

### Resolve

| Method | Description |
|--------|-------------|
| `ResolvePermission(requestID, resp)` | Reply to permission request |
| `ResolveElicitation(id, resp)` | Reply to elicitation request |
| `SetConfigOption(configID, val)` | Set a config option |

### Runtime

| Method | Description |
|--------|-------------|
| `UpdateSession(ctx, opts)` | Update session configuration |
| `StartRealtime(cfg)` | Start realtime mode (not implemented) |
| `StopRealtime()` | Stop realtime mode (not implemented) |

### Receive

| Method | Description |
|--------|-------------|
| `RecvEvent()` | Read next ACP event from data stream (blocks; underlying stream EOF on end) |
| `RecvAction()` | Read next action from ctrl stream (permissions, disposition) |

## Auth

```go
provider := auth.DefaultAuth(ed25519PrivateKey)

token, _ := provider.IssueNodeToken("node-1")
info, _ := provider.ValidateNodeToken(token)
opToken, _ := provider.IssueOperationToken("node-1", "sess-1", []string{"session"}, nil)
```

| Method | Description |
|--------|-------------|
| `IssueNodeToken(nodeID)` | Create a node token (7-day expiry) |
| `ValidateNodeToken(token)` | Verify and decode a node token |
| `RefreshNodeToken(oldToken)` | Validate old token and issue a new one |
| `IssueOperationToken(nodeID, sessionID, ops, scope)` | Create a session-scoped token (30-min expiry) |
| `SigningPublicKey()` | Return `(keyID, publicKey)` for token verification |
| `WellKnownInfo()` | Return transport discovery metadata |

## Key Types

- `SessionRequest` — Runtime, isolation, LLM config, MCP servers, environment
- `UserMessage` — Content blocks, message text, attachments
- `Event` — ACP update (`Type` + `Data` as raw JSON)
- `SessionMsg` — Wire message (`Seq`, `Kind`, `Data` bytes)
- `Disposition` — `"queued"` | `"handled"` (returned by `Followup` / `Steer`)
- `QueueAction` — Queue operation (`"remove"` / `"edit"` / `"clear"` / `"promote"`)
- `PermissionResponse` — Permission reply (`"selected"` / `"cancelled"`)
- `ElicitationResponse` — Elicitation reply (`"accept"` / `"decline"` / `"cancel"`)
- `ConfigOptionValue` — Config value (`"id"` / `"boolean"`)
- `SessionUpdateOptions` — Runtime updates (workspace, access policy, mounts, LLM, env, resources)

## Workspace

See [Workspace API](workspace.md) for full documentation. Workspace operations are accessed through `RemoteNode` methods:

```go
import "github.com/yaoapp/tai-golang/node"

n := node.Get("node-1")
ws, err := n.OpenWorkspace(ctx, "workspace-id")
ws, err := n.CreateWorkspace(ctx, types.Meta{Name: "proj"})
entries, err := n.ListWorkspaces(ctx, types.ListOptions{})
err := n.DeleteWorkspace(ctx, "workspace-id")
```

### Key Interfaces

| Interface | Description |
|-----------|-------------|
| `Workspace` | Top-level object: `WorkspaceFS` + branch management + history + GC |
| `WorkspaceFS` | `fs.FS` + `Create` / `Remove` / `RemoveAll` / `Rename` / `MkdirAll` / `Symlink` / `Watch` |
| `Branch` | Branch-scoped file operations (`FS()`) + version control (`Commit` / `Diff` / `Rollback`) |
| `History` | Commit queries: `Query` / `FileHistory` / `BranchLog` |
| `Watcher` | Real-time file change events via `Events()` channel |

`Open()` returns a file implementing `fs.File` + `io.ReadSeeker`. Consumer code is identical to a local `*os.File` — `Seek` records the offset without network calls; `Read` reopens the stream at the new offset only when needed. This enables `http.ServeContent` for Range requests out of the box. See [Workspace API — Open](workspace.md#open) for details.

### Key Types (in `types/`)

- `Meta` — Workspace metadata (`Name`, `Owner`)
- `BranchInfo` — Branch summary (`Name`, `Head`, `Meta`)
- `CommitInfo` — Commit details (`ID`, `Parent`, `Message`, `Time`)
- `FileDiff` — File change between commits (`Path`, `Type`, `OldPath`)
- `MergeOptions` / `MergeResult` — Merge configuration and outcome
- `GCOptions` / `GCResult` — Garbage collection configuration and outcome
- `WatchEvent` — File change notification (`Type`, `Path`)
- `ChannelPolicy` — Transport channel selection (in `workspace/`)
- `ErrorResponse` — Structured remote error (in `workspace/`)
