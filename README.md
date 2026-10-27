# tai-golang

Go SDK for the Tai Transport Protocol v2. This package implements the **server side** of the protocol, enabling any server to accept connections from [Tai](https://github.com/yaoapp/tai) nodes, manage sessions, and invoke tools.

Tai is a computer agent installed on every machine. This SDK handles the transport layer so your server doesn't need to implement the protocol details.

## Status

**Work in progress** — L0 connection management and L1 Control stream are implemented; HTTP/3 (QUIC) primary transport with HTTP/2 fallback is operational.

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

> **Note:** `NewServer` is not yet implemented. The above shows the target API.

## Project Layout

```
proto/                .proto source files (canonical protocol definitions)
pb/                   Generated Go code from protobuf (committed)
server.go             Server interface and configuration types
auth.go               AuthProvider interface
auth_default.go       DefaultAuth — Ed25519 JWT node_token + HMAC connect_ticket
http.go               HTTP API server (5099): well-known, connect, token refresh, heartbeat
transport.go          Transport server (6099): HTTP/3 (QUIC) primary + HTTP/2 (h2c) fallback, ticket auth middleware
types.go              Node, Session, Tool, and related types
testutil.go           NewTestServer helper for self-contained testing
.github/workflows/    CI: lint → unit tests (Linux/macOS/Windows)
Makefile              Build targets: proto, test, lint, clean
```

## Proto Definitions

The `proto/` directory contains the canonical protobuf definitions for the Tai Transport Protocol v2:

| File | Contents |
|------|----------|
| `control.proto` | L1 Control stream: registration (with container runtimes), heartbeat, pool, security |
| `session.proto` | Session lifecycle and stream messages |
| `computer.proto` | Hardware status, container runtime health, image/container/sandbox management |
| `tunnel.proto` | Tunnel binding lifecycle |
| `servertool.proto` | Server tool request/response for `tai call` |

To regenerate Go code after editing `.proto` files:

```bash
make proto
```

Requires `protoc` and `protoc-gen-go`.

## Implemented

- **Protobuf definitions** — all 5 `.proto` files with generated Go code
- **AuthProvider interface** — pluggable auth, ships with `DefaultAuth`:
  - Ed25519 JWT `node_token` (issue / validate / refresh, 7-day expiry)
  - HMAC `connect_ticket` (30s expiry, one-time use, random nonce)
  - `OperationToken` for session-scoped authorization
- **HTTP API** (port 5099):
  - `GET /.well-known/yao-transport` — transport metadata discovery
  - `POST /v1/tai/connect` — exchange node_token for connect_ticket
  - `POST /v1/tai/token/refresh` — refresh node_token
  - `POST /v1/tai/heartbeat` — fallback heartbeat
- **Transport server** (port 6099, TCP + UDP):
  - **HTTP/3 (QUIC)** — primary protocol, requires TLS
  - **HTTP/2 (h2c)** — fallback, cleartext via Go 1.24+ native `http.Protocols`
  - Shared `http.Handler` and ticket auth middleware across both protocols
  - L1 Control stream: register → register_ack, ping → pong, go_away, forward-compatible unknown kinds
  - Session Control / Session Data stream endpoints
- **Node manager** — in-memory node state, onRegister callback, sendControl / goAway / goAwayAll
- **Frame codec** — length-prefixed protobuf (4-byte big-endian header, 16 MB max)
- **Test infrastructure** — `NewTestServer(t)` with ephemeral ports, self-signed TLS cert generated at runtime, all integration tests run as H2/H3 sub-tests, 90%+ coverage

## Architecture

```
Tai binary  ←── Transport (6099) ──→  Your Server (import tai-golang)
            ←── HTTP API (5099)  ──→
```

The SDK is the server side. Tai is the client. They communicate over **HTTP/3 (QUIC)** as the primary protocol, with **HTTP/2 (TCP)** as fallback, using length-prefixed protobuf messages on both. The HTTP API handles discovery and authentication; the Transport layer carries Control and Session streams.

## License

Apache-2.0
