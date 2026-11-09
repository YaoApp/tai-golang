# API Reference

Quick lookup for the public SDK surface. Each section links to the detailed
guide for types, examples, and edge cases.

## Server

```go
srv, err := tai.NewServer(&tai.ServerConfig{
    HTTPPort:      5099,
    TransportPort: 6099,
    Auth:          authProvider,
})
```

### ServerConfig

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `TransportPort` | `int` | `6099` | HTTP/2 or HTTP/3 transport port |
| `HTTPPort` | `int` | `5099` | Discovery and token-refresh HTTP port |
| `Auth` | `AuthProvider` | required | Node/user credential provider |
| `TLS` | `*TLSConfig` | `nil` | `CertFile` + `KeyFile`; nil = plaintext |
| `MaxFrameSize` | `int` | `16 MiB` | Max transport frame payload, 64 KiB–64 MiB |
| `UnauthTimeout` | `time.Duration` | `5s` | Time allowed before authentication |
| `QUIC` | `*quic.Config` | `nil` | HTTP/3 transport settings |
| `ShutdownTimeout` | `time.Duration` | `5s` | Graceful shutdown budget |
| `Pool` | `*PoolSettings` | defaults | Light/Heavy pool sizes and idle timeouts |

### Server methods

| Method | Description |
| --- | --- |
| `Start(ctx)` | Start serving and block until shutdown |
| `WaitReady(ctx)` | Wait until listeners are ready |
| `Addr()` | Transport listen address |
| `Close()` | Graceful, idempotent shutdown |
| `RegisterTool(tool)` | Reserved; currently returns `ErrNotImplemented` |
| `UnregisterTool(name)` | Reserved; currently returns `ErrNotImplemented` |

### Remote Tool registration

| Method | Description |
| --- | --- |
| `RegisterRemoteTool(tool)` | Add one Remote Tool |
| `UnregisterRemoteTool(name)` | Remove one |
| `SetRemoteTools(tools)` | Replace the full set atomically |
| `RemoteToolRegistry()` | Access the underlying registry |

See [Remote Tools](remote-tools.md).

### Plugin registration

| Method | Description |
| --- | --- |
| `RegisterPlugin(entry)` | Register a Wasm plugin from `ArtifactPath`; computes digest/size |
| `UnregisterPlugin(name, version)` | Remove one |
| `SetRemotePlugins(entries)` | Replace the full set atomically |
| `PluginRegistry()` | Access the underlying registry |

See [Remote Tool Plugins](remote-tool-plugins.md).

## Node

```go
import "github.com/yaoapp/tai-golang/node"

node.OnRegister(func(rn types.RemoteNode) error { return nil })
node.OnDisconnect(func(id string) { ... })

rn := node.Get("node-1")
all := node.List()
```

| API | Description |
| --- | --- |
| `node.Get(id)` | `types.RemoteNode` or nil |
| `node.List()` | All connected nodes |
| `node.Default()` | Process-wide registry |
| `node.OnRegister(fn)` | Registration callback; return error to reject |
| `node.OnDisconnect(fn)` | Disconnect callback |

### RemoteNode

| Method | Description |
| --- | --- |
| `Info()` | Current `*types.Node` snapshot |
| `Online()` | Connected state |
| `OpenWorkspace(ctx, id, branch...)` | Open existing workspace |
| `CreateWorkspace(ctx, meta)` | Create workspace |
| `ListWorkspaces(ctx, opts)` | List workspaces |
| `DeleteWorkspace(ctx, id)` | Delete workspace |
| `OpenSession(ctx, req)` | Open a session |
| `ResumeSession(ctx, req)` | Resume after transport loss |
| `SendControl(msg)` | Send a control message |
| `GoAway(reason, reconnectAfter)` | Ask node to reconnect |

See [Node API](nodes.md).

## Session

```go
sess, err := rn.OpenSession(ctx, &types.SessionOpenRequest{
    SessionID: "sess-1",
    Scopes:    types.Scope{types.SessionAccess},
    Request:   &types.SessionRequest{Cwd: "/workspace"},
})
```

### Lifecycle

| Method | Description |
| --- | --- |
| `ID()` | Session identifier |
| `NodeID()` | Owning node |
| `Status()` | `opening` / `running` / `disconnected` / `closing` / `closed` |
| `Done()` | Closed when the session terminates |
| `Close(ctx)` | Close the session |

### Commands

| Method | Description |
| --- | --- |
| `Prompt(msg)` | Start a turn with user input |
| `Followup(msg)` | Append input; returns `queued` or `handled` |
| `Steer(msg)` | Guide the current turn; returns `queued` or `handled` |
| `Cancel()` | Cancel the current turn |
| `UpdateQueue(action)` | `remove` / `edit` / `clear` / `promote` |

### Resolve

| Method | Description |
| --- | --- |
| `ResolvePermission(requestID, resp)` | Approve/cancel a permission request |
| `ResolveElicitation(id, resp)` | Accept/decline/cancel form input |
| `SetConfigOption(id, value)` | Change a published session option (e.g. model) |

### Runtime update

| Method | Description |
| --- | --- |
| `UpdateSession(ctx, opts)` | Workspace, access policy, mounts, LLM matrix, env, resources |

See [LLM Providers](llm-providers.md#changing-models-at-runtime).

### Realtime

| Method | Description |
| --- | --- |
| `StartRealtime(req)` | Enter voice/realtime mode |
| `WaitRealtime(ctx)` | Wait for realtime setup |
| `UpdateRealtime(opts)` | Change voice/modality |
| `StopRealtime()` | Leave realtime mode |
| `AudioAppend(data)` | Append PCM audio |
| `AudioCommit()` | Commit buffered audio (manual VAD) |
| `AudioClear()` | Clear buffered audio |
| `AudioInterrupt()` | Interrupt playback |
| `ResponseCreate()` | Trigger a response (manual VAD) |
| `RecvAudio()` | `<-chan *AudioFrame` |
| `RecvRender()` | `<-chan *RenderEvent` |
| `RecvMotion()` | `<-chan *MotionFrame` |
| `SendVideoFrame(frame)` | Reserved; not implemented |
| `RecvVideoFrame()` | Reserved; not implemented |

See [Session Realtime](session-realtime.md) and [Realtime Protocol](realtime.md).

### Receive

| Method | Description |
| --- | --- |
| `RecvEvent()` | Next ACP event from the data stream; `io.EOF` on end |
| `RecvAction()` | Next control-side action |

Single reader per channel. See [Session](session.md#receiving-events-concurrently).

## Workspace

Workspaces are opened through `RemoteNode`:

```go
ws, err := rn.OpenWorkspace(ctx, "ws-1")
branch, err := ws.GetBranch(ctx, "main")
f, err := branch.FS().Open("README.md")
```

| Interface | Description |
| --- | --- |
| `Workspace` | Root filesystem, branches, history, GC |
| `Branch` | Branch-scoped `FS()`, `Commit`, `Diff`, `Rollback` |
| `History` | Commit/file queries and branch log |
| `Watcher` | `Events()` channel |

See [Workspace API](workspace.md) for methods, archive helpers, context, and
skills APIs.

## Remote Tools

```go
srv.RegisterRemoteTool(&remotetool.Tool{
    Entry: &pb.RemoteToolEntry{
        Name:  "demo.echo",
        Group: "demo",
        InputSchema: []byte(`{"type":"object"}`),
    },
    Handler: func(ctx context.Context, in []byte) ([]byte, error) {
        return in, nil
    },
})
```

| Type | Description |
| --- | --- |
| `remotetool.Tool` | `Entry`, `Handler`, optional `ProgressHandler` |
| `remotetool.User(ctx)` | Verified caller identity inside a Handler |
| `remotetool.ToolCallID(ctx)` | Request correlation ID |
| `remotetool.Emitter` | Progress event emitter for streamed progress |

See [Remote Tools](remote-tools.md).

## Plugins

```go
srv.RegisterPlugin(&pb.PluginEntry{
    Name:         "demo.normalize",
    Version:      "1.0.0",
    Runtime:      "wasm",
    ArtifactPath: "dist/wasm/normalize.wasm",
})
```

| API | Description |
| --- | --- |
| `RegisterPlugin(entry)` | Validate path, hash file, store path |
| `UnregisterPlugin(name, version)` | Remove |
| `SetRemotePlugins(entries)` | Atomic replacement |

See [Remote Tool Plugins](remote-tool-plugins.md).

## Auth

```go
provider := auth.DefaultAuth(privateKey)

nodeToken, _ := provider.IssueNodeToken("node-1")
userToken, identity, _ := provider.IssueUserToken("node-1",
    types.Scope{types.SessionAccess})
```

| Interface | Description |
| --- | --- |
| `AuthProvider` | Node/user token issue, refresh, validate |
| `UserTokenContextProvider` | Optional request-context resolver |
| `UserTokenExpiry` | Optional expiry hint for local GC |

`auth.DefaultAuth` is for the demo flow only; production uses a custom
`AuthProvider`. See [Auth](auth.md).

## Scopes

| Constant | Value |
| --- | --- |
| `types.SessionAccess` | `session:access` |
| `types.WorkspaceRead` | `workspace:read` |
| `types.WorkspaceWrite` | `workspace:write` |
| `types.WorkspaceDelete` | `workspace:delete` |
| `types.WorkspaceAll` | `workspace:*` |
| `types.RemoteToolCall` | `remote_tool:call` |

## Common types

| Type | Documentation |
| --- | --- |
| `ServerConfig`, `Server` | This page |
| `Node`, `NodeRuntime`, `RemoteNode` | [Node API](nodes.md) |
| `SessionRequest`, `SessionOpenRequest`, `ResumeSessionRequest` | [Session](session.md) |
| `UserMessage`, `QueueAction`, `PermissionResponse`, `ElicitationResponse` | [Session](session.md) |
| `Event`, `SessionMsg` | [ACP Events](events.md) |
| `RealtimeRequest`, `RealtimeUpdateOptions`, `AudioConfig`, `VADConfig` | [Session Realtime](session-realtime.md) |
| `Workspace`, `Branch`, `History`, `Watcher`, `Meta` | [Workspace API](workspace.md) |
| `AuthProvider`, `UserIdentity`, `Scope` | [Auth](auth.md) |
| `remotetool.Tool`, `RemoteToolEntry` | [Remote Tools](remote-tools.md) |
| `PluginEntry`, `Capabilities` | [Remote Tool Plugins](remote-tool-plugins.md) |
| `ControlMsg`, `ToolDefinition` | [Node API](nodes.md) / [Remote Tools](remote-tools.md) |

## Errors

| Error | Meaning |
| --- | --- |
| `types.ErrNodeNotFound` | Node is not connected to this SDK instance |
| `tai.ErrNotImplemented` | Reserved API called before implementation |
| `remotetool` errors | Invalid tool definition / duplicate name |
| `plugin.ErrInvalidPlugin` | Invalid name, version, runtime, or artifact path |
| `plugin.ErrNotFound` | Plugin digest not registered |
| `workspace.ErrorResponse` | Structured remote workspace failure |

Transport errors are returned by the operation that triggered them; inspect
`errors.Is`/`errors.As` before logging. Never log tokens or raw tool input.
