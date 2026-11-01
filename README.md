# tai-golang

Go SDK for the Tai Transport Protocol v2. This package implements the **server side** of the protocol, enabling any Go application to accept connections from Tai nodes, manage workspaces, run sessions, and execute agent workloads.

Tai is a computer agent installed on every machine. This SDK handles the transport layer so your application doesn't need to implement the protocol details.

## Status

**Full chain working end-to-end** — Transport (L0–L2), Light/Heavy pool, Workspace CRUD + branch + file operations, Session 17-method interface, ACP event stream, dual-stream architecture, and the Demo App with React frontend are all working.

Not yet implemented: OperationToken validation in session handshake, Server Tools, Tunnel, Reconnection, Realtime.

## Install

```bash
go get github.com/yaoapp/tai-golang
```

Requires Go 1.26+.

## Download Tai

The SDK requires a Tai node to connect. Download the nightly build (v2.x):

```bash
# macOS (Apple Silicon)
curl -sL https://get.yaoapps.com/tai/nightly/latest.json | jq -r '.assets["darwin-arm64"]' | xargs curl -L -o tai && chmod +x tai

# macOS (Intel)
curl -sL https://get.yaoapps.com/tai/nightly/latest.json | jq -r '.assets["darwin-amd64"]' | xargs curl -L -o tai && chmod +x tai

# Linux (x86_64)
curl -sL https://get.yaoapps.com/tai/nightly/latest.json | jq -r '.assets["linux-amd64"]' | xargs curl -L -o tai && chmod +x tai

# Linux (ARM64)
curl -sL https://get.yaoapps.com/tai/nightly/latest.json | jq -r '.assets["linux-arm64"]' | xargs curl -L -o tai && chmod +x tai

# Windows (PowerShell)
$url = (Invoke-RestMethod https://get.yaoapps.com/tai/nightly/latest.json).assets.'windows-amd64'
Invoke-WebRequest $url -OutFile tai.exe
```

All platforms: query [latest.json](https://get.yaoapps.com/tai/nightly/latest.json) for version info and download URLs. SHA256 checksums available at `<binary-url>.sha256`.

## Quick Start

```go
package main

import (
    "context"
    "crypto/ed25519"
    "crypto/rand"
    "log"

    tai "github.com/yaoapp/tai-golang"
    "github.com/yaoapp/tai-golang/auth"
)

func main() {
    _, privateKey, _ := ed25519.GenerateKey(rand.Reader)

    server, err := tai.NewServer(&tai.ServerConfig{
        TransportPort: 6099,
        HTTPPort:      5099,
        Auth:          auth.DefaultAuth(privateKey),
    })
    if err != nil {
        log.Fatal(err)
    }

    server.OnNodeRegister(func(node *tai.Node) error {
        log.Printf("node registered: %s", node.ID)
        return nil
    })

    if err := server.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

## Opening a Session

```go
// Issue tokens
nodeToken, _ := authProvider.IssueNodeToken("node-1")
opToken, _ := authProvider.IssueOperationToken("node-1", "sess-001", []string{"session"}, nil)

// Open session on a registered node
session, err := server.OpenSession(ctx, &tai.OpenSessionRequest{
    NodeID:    "node-1",
    SessionID: "sess-001",
    Token:     opToken,
    Request:   &tai.SessionRequest{Runtime: "agent"},
})

// Send a prompt
session.Prompt(&tai.UserMessage{Message: "Hello"})

// Receive events
for {
    msg, err := session.RecvEvent()
    if err != nil {
        break
    }
    fmt.Printf("[%s] %s\n", msg.Kind, string(msg.Data))
}
```

## Working with Workspaces

```go
// List workspaces on a node
entries, _ := node.ListWorkspaces(ctx, types.ListOptions{})

// Create a new workspace (path auto-generated on the Tai node)
ws, _ := node.CreateWorkspace(ctx, types.Meta{Name: "my-project", Owner: "user-1"})

// Open an existing workspace
ws, _ := node.OpenWorkspace(ctx, "workspace-id")

// Branch operations
branches, _ := ws.ListBranches(ctx)
branch, _ := ws.CreateBranch(ctx, "feature-1", "main", nil)
ws.Merge(ctx, "feature-1", "main", types.MergeOptions{})

// File operations via fs.FS interface
bfs := branch.FS()
data, _ := fs.ReadFile(bfs, "README.md")
entries, _ := fs.ReadDir(bfs, ".")

// Write files
w, _ := bfs.Create("hello.txt")
w.Write([]byte("Hello, World!"))
w.Close()

// Commit changes
branch.Commit(ctx, "initial commit")
```

## Architecture

```text
Your Application                    Tai Node
┌─────────────────────┐            ┌──────────┐
│  import tai-golang  │            │ tai start │
│                     │            │           │
│  SDK Server         │  ←─────── │  connects │
│    :5099  HTTP      │  outbound │  to SDK   │
│    :6099  Transport │            │           │
└─────────────────────┘            └──────────┘
```

The SDK embeds into your application as a library — no separate process. Tai nodes connect outbound to the SDK's two ports.

## Project Layout

```
server.go             Server + Session interfaces, NewServer, OpenSession
session/              Session implementation (17 methods, dual-stream)
workspace/            Remote workspace API (fs.FS + write, branch, history, watch)
node/                 Node registry and RemoteNode implementation
types/                Shared types (Session, Workspace, Event, Permission, etc.)
auth/                 AuthProvider interface + DefaultAuth (Ed25519 JWT)
transport/            Transport server (H2/H3, Light pool, Heavy pool, frame codec)
mock/                 Test doubles (MockNode, MockLLM, MockSession, MockLightConn)
proto/                Protobuf source files (control, session, computer, servertool)
pb/                   Generated Go code from protobuf
app/                  Demo application (Go API server + React frontend)
docs/                 SDK documentation (rendered in demo app)
```

## Documentation

Full SDK documentation is available in the [`docs/`](docs/) directory:

- [Quick Start](docs/quickstart.md)
- [Architecture](docs/architecture.md)
- [API Reference](docs/api-reference.md)
- [Workspace API](docs/workspace.md)
- [Session Protocol](docs/session-protocol.md)
- [ACP Events Reference](docs/events.md)
- [Resource URI Convention](docs/uri.md)
- [Demo App Guide](docs/demo-guide.md)

Run the demo app to browse documentation with syntax highlighting:

```bash
cd app && make build && DEEPSEEK_API_KEY=<key> ./tai-demo -port 3000
```

## Implemented

- **Transport (L0–L2)**: HTTP API (:5099), TCP/UDP transport (:6099), H3 (QUIC) + H2 fallback, Light/Heavy connection pool
- **Node**: Registry, RemoteNode interface, online/offline lifecycle, GoAway
- **Session (17 methods)**: Prompt, Followup, Cancel, Steer, UpdateQueue, ResolvePermission, ResolveElicitation, SetConfigOption, UpdateSession, RecvEvent, RecvAction, StartRealtime (stub), StopRealtime (stub), Close, ID, NodeID, Status
- **Workspace**: CRUD, branch management (create/delete/merge), file operations (fs.FS + write extensions), commit/rollback, history query, GC, watch — 34 transport command kinds
- **Auth**: Ed25519 JWT node_token (issue/validate/refresh), OperationToken (issue/sign)
- **Types**: Full ACP v2 type coverage (Event, ContentBlock, Permission, Elicitation, Queue, Disposition, Config)
- **Proto**: 4 `.proto` files with generated Go (control, session, computer, servertool)
- **Demo App**: React frontend (nodes, sessions, workspaces, file manager, docs browser) + Go API server
- **Test Infrastructure**: MockNode, MockLLM, MockSession, MockLightConn/Stream, 216 test functions

## Not Yet Implemented

- OperationToken validation in session handshake
- Server Tools (RegisterTool / tai call)
- Reconnection (recovery buffer + seq replay)
- Tunnel
- Realtime (voice)
- Container remote management

## License

Apache-2.0
