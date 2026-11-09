# Node API

The SDK maintains an in-memory registry of Tai nodes connected to the server.
A registered node is represented by `types.Node`; runtime operations are exposed
through `types.RemoteNode`.

`tai.NewServer` creates and installs the default registry. Most applications use
the package-level helpers from `node`:

```go
import "github.com/yaoapp/tai-golang/node"
```

## Data structures

### Node

```go
type Node struct {
    ID                string
    TaiID             string
    Version           string
    Capabilities      []string
    ContainerRuntimes []NodeRuntime
    OS                string
    Arch              string
    CPUCores          uint32
    MemoryMB          uint64
    DiskFreeMB        uint64
    Online            bool
}
```

| Field | Meaning |
| --- | --- |
| `ID` | Stable node identifier supplied by the node |
| `TaiID` | Server-generated identifier returned in `register_ack` |
| `Version` | Tai binary version |
| `Capabilities` | Advertised features, e.g. `session`, `sandbox:native`, `sandbox:docker` |
| `ContainerRuntimes` | Docker/Podman/containerd runtimes available on the node |
| `OS` / `Arch` | Platform |
| `CPUCores` / `MemoryMB` / `DiskFreeMB` | Hardware snapshot at registration |
| `Online` | True while the node is registered |

### NodeRuntime

```go
type NodeRuntime struct {
    Name    string // "docker", "podman", "containerd", "nsjail"
    Version string
    Socket  string
}
```

### RemoteNode

`RemoteNode` wraps a `Node` with operations. It is valid while the node is
registered; look it up again after reconnect.

```go
type RemoteNode interface {
    Info() *Node
    Online() bool

    OpenWorkspace(ctx context.Context, wsID string, branch ...string) (Workspace, error)
    CreateWorkspace(ctx context.Context, meta Meta) (Workspace, error)
    ListWorkspaces(ctx context.Context, opts ListOptions) ([]RegistryEntry, error)
    DeleteWorkspace(ctx context.Context, wsID string) error

    OpenSession(ctx context.Context, req *SessionOpenRequest) (Session, error)
    ResumeSession(ctx context.Context, req *ResumeSessionRequest) (Session, error)

    SendControl(msg *ControlMsg) error
    GoAway(reason string, reconnectAfter uint32) error
}
```

## Listing nodes

```go
for _, rn := range node.List() {
    info := rn.Info()
    fmt.Printf("node=%s version=%s caps=%v online=%v\n",
        info.ID, info.Version, info.Capabilities, rn.Online())
}
```

Look up one node:

```go
rn := node.Get("demo-container")
if rn == nil {
    log.Fatal("node not connected")
}
```

Data-only access:

```go
nodes := node.Default().AllNodes()       // []*types.Node
info := node.Default().NodeByID("demo-container")
```

## Paths and operations

### Workspace

```go
ws, err := rn.OpenWorkspace(ctx, "workspace-id")
if err != nil {
    log.Fatal(err)
}

ws2, err := rn.CreateWorkspace(ctx, types.Meta{Name: "demo"})
if err != nil {
    log.Fatal(err)
}

entries, err := rn.ListWorkspaces(ctx, types.ListOptions{})
if err != nil {
    log.Fatal(err)
}

if err := rn.DeleteWorkspace(ctx, "workspace-id"); err != nil {
    log.Fatal(err)
}
```

`OpenWorkspace` accepts an optional branch; the default is `main`.

### Session

```go
sess, err := rn.OpenSession(ctx, &types.SessionOpenRequest{
    SessionID: "session-1",
    Request: &types.SessionRequest{
        Cwd: "/workspace",
    },
})
if err != nil {
    log.Fatal(err)
}
defer sess.Close(context.Background())
```

After connection loss, resume from the last sequence numbers:

```go
sess, err := rn.ResumeSession(ctx, &types.ResumeSessionRequest{
    SessionID:   "session-1",
    LastCtrlSeq: lastCtrl,
    LastDataSeq: lastData,
})
```

Scopes and anonymous sessions:

- `Scopes` empty defaults to `session:access`
- `Anonymous: true` opens without a user identity for system/unattended use

### Control

```go
if err := rn.SendControl(&types.ControlMsg{
    Kind: "custom",
    Data: payload,
}); err != nil {
    log.Fatal(err)
}
```

Ask a node to reconnect:

```go
if err := rn.GoAway("maintenance", 5); err != nil {
    log.Fatal(err)
}
```

All nodes:

```go
node.Default().GoAwayAll("server restart", 10)
```

## Registry

Use `node.Default()` for the server-owned registry. A custom registry is mainly
for embedding and tests:

```go
reg := node.NewRegistry(&pb.PoolConfig{}, workspace.DefaultPolicy())
node.SetDefault(reg)
```

Registry operations:

```go
reg.AllNodes()             // []*types.Node
reg.NodeByID(id)           // *types.Node or nil
reg.RemoteNodeByID(id)     // types.RemoteNode or nil
reg.AllRemoteNodes()       // []types.RemoteNode
reg.GoAway(id, reason, s)
reg.GoAwayAll(reason, s)
reg.SetOnRegister(fn)
reg.SetOnDisconnect(fn)
```

Package-level callbacks:

```go
node.OnRegister(func(rn types.RemoteNode) error {
    log.Printf("node connected: %s", rn.Info().ID)
    return nil
})

node.OnDisconnect(func(nodeID string) {
    log.Printf("node disconnected: %s", nodeID)
})
```

## Lifecycle

1. Node opens the Control stream and sends `register`.
2. Registry stores `types.Node`, assigns `TaiID`, and returns `register_ack`.
3. `OnRegister` runs; returning an error rejects the registration.
4. Node may open workspaces and sessions through `RemoteNode`.
5. Disconnect removes the node and calls `OnDisconnect`.

`RemoteNode` is a lightweight wrapper; do not cache it across reconnects. Cache
the node ID and resolve it with `node.Get` when needed.

## Notes

- The registry is in-memory; it is not a persistent node inventory.
- `Info()` returns a copy of the current node data.
- `Online()` is a convenient check; it returns false after the node is removed.
- `SendControl` returns an error when the node is unknown or has no active
  stream.
