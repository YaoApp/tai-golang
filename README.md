# tai-golang

Go SDK for the Tai Transport Protocol v2. This package implements the **server side** of the protocol, enabling any server to accept connections from [Tai](https://github.com/yaoapp/tai) nodes, manage sessions, and invoke tools.

Tai is a computer agent installed on every machine. This SDK handles the transport layer so your server doesn't need to implement the protocol details.

## Status

**Work in progress** — Protocol definitions are stable; server implementation is under active development.

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
    tai "github.com/yaoapp/tai-golang"
)

func main() {
    server, err := tai.NewServer(&tai.ServerConfig{
        TransportPort: 6099,
        HTTPPort:      5099,
        Auth:          tai.DefaultAuth(privateKey),
    })
    if err != nil {
        panic(err)
    }

    server.OnNodeRegister(func(node *tai.Node) error {
        log.Printf("node registered: %s (%s/%s)", node.ID, node.OS, node.Arch)
        return nil
    })

    server.Start(context.Background())
}
```

> **Note:** `NewServer` and `DefaultAuth` are not yet implemented. The above shows the target API.

## Project Layout

```
proto/          .proto source files (canonical protocol definitions)
pb/             Generated Go code from protobuf (committed)
server.go       Server interface and configuration types
auth.go         AuthProvider interface
types.go        Node, Session, Tool, and related types
Makefile        Build targets: proto, test, lint, clean
```

## Proto Definitions

The `proto/` directory contains the canonical protobuf definitions for the Tai Transport Protocol v2:

| File | Contents |
|------|----------|
| `control.proto` | L1 Control stream: registration, heartbeat, computer management, security |
| `session.proto` | Session lifecycle and stream messages |
| `computer.proto` | Hardware status, image/container/sandbox management |
| `tunnel.proto` | Tunnel binding lifecycle |
| `servertool.proto` | Server tool request/response for `tai call` |

To regenerate Go code after editing `.proto` files:

```bash
make proto
```

Requires `protoc` and `protoc-gen-go`.

## Architecture

```
Tai binary  ←── Transport (6099) ──→  Your Server (import tai-golang)
closed-source                          open-source SDK
```

The SDK is the server side. Tai is the client. They communicate over HTTP/2 (with HTTP/3 QUIC upgrade) using length-prefixed protobuf messages.

## License

Apache-2.0
