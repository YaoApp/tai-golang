# tai-golang

Go SDK for the Tai Transport Protocol v2. This package implements the **server side** of the protocol, enabling any Go application to accept connections from [Tai](https://github.com/yaoapp/tai) nodes, manage sessions, and run agent workloads.

Tai is a computer agent installed on every machine. This SDK handles the transport layer so your application doesn't need to implement the protocol details.

## Status

**Session full chain implemented** — Transport (L0–L2), Light pool, Session 17-method interface, ACP event stream, dual-stream (ctrl + data) architecture, and the QA demo app are all working end-to-end.

Not yet implemented: OperationToken signing/validation in session handshake, Heavy pool, Workspace remote API, Server Tools, Tunnel, Reconnection, Realtime.

## Install

```bash
go get github.com/yaoapp/tai-golang
```

Requires Go 1.26+.

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
types/                Shared types (SessionRequest, Event, Message, Permission, etc.)
auth/                 AuthProvider interface + DefaultAuth (Ed25519 JWT)
transport/            Transport server (TCP/UDP, H2/H3, Light pool, frame codec)
proto/                Protobuf source files (control, session, computer, servertool)
pb/                   Generated Go code from protobuf
app/                  QA demo application (API server + React frontend)
docs/                 SDK documentation (rendered in demo app)
```

## Documentation

Full SDK documentation is available in the [`docs/`](docs/) directory:

- [Quick Start](docs/quickstart.md)
- [Architecture](docs/architecture.md)
- [API Reference](docs/api-reference.md)
- [Session Protocol](docs/session-protocol.md)
- [ACP Events Reference](docs/events.md)
- [Resource URI Convention](docs/uri.md)
- [Demo App Guide](docs/demo-guide.md)

Run the demo app to browse documentation with syntax highlighting:

```bash
cd app && make build && DEEPSEEK_API_KEY=<key> ./tai-demo --port 3000
```

## Implemented

- **Transport (L0–L2)**: HTTP API (5099), TCP/UDP transport (6099), H3 (QUIC) + H2 fallback, Light connection pool
- **Session (17 methods)**: Prompt, Followup, Cancel, Steer, UpdateQueue, ResolvePermission, ResolveElicitation, SetConfigOption, UpdateSession, RecvEvent, RecvAction, StartRealtime (stub), StopRealtime (stub), Close, ID, NodeID, Status
- **Auth**: Ed25519 JWT node_token (issue/validate/refresh), OperationToken
- **Types**: Full ACP v2 type coverage (Event, ContentBlock, Permission, Elicitation, Queue, Disposition, Config)
- **Proto**: 4 `.proto` files with generated Go (control, session, computer, servertool)
- **Demo App**: React frontend + Go API server for manual E2E testing
- **Test Infrastructure**: MockNode, MockLLM, TestServer, 90%+ coverage

## Not Yet Implemented

- OperationToken signing/validation in session handshake
- Heavy connection pool + large file transfer
- Workspace remote API (file CRUD, Watch, sync)
- Server Tools (RegisterTool / tai call)
- Reconnection (recovery buffer + seq replay)
- Tunnel
- Realtime (voice)
- Container remote management

## License

Apache-2.0
