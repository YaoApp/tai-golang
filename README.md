# tai-golang

Go SDK for the Tai Transport Protocol v2. This package implements the **server side** of the protocol, enabling any server to accept connections from [Tai](https://github.com/yaoapp/tai) nodes, manage sessions, and invoke tools.

Tai is a computer agent installed on every machine. This SDK handles the transport layer so your server doesn't need to implement the protocol details.

## Status

**Work in progress** — implemented: L0 discovery and connection-level authentication, the L1 Control stream, the 6099 TCP/UDP transport (HTTP/3 + WebTransport primary, HTTP/2 fallback), and `NewServer` / `Start`. Not yet implemented: Light and Heavy connection pools, sessions, server tools, tunnels, workspace and realtime (`OpenSession`, `RegisterTool` and `UnregisterTool` return `ErrNotImplemented`).

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
)

func main() {
    _, privateKey, err := ed25519.GenerateKey(rand.Reader) // persist the key in production
    if err != nil {
        log.Fatal(err)
    }

    server, err := tai.NewServer(&tai.ServerConfig{
        TransportPort: 6099,
        HTTPPort:      5099,
        TLS:           &tai.TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"},
        Auth:          tai.DefaultAuth(privateKey),
    })
    if err != nil {
        log.Fatal(err)
    }

    server.OnNodeRegister(func(node *tai.Node) error {
        log.Printf("node registered: %s (%s/%s)", node.ID, node.OS, node.Arch)
        return nil
    })

    if err := server.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

`NewServer` assembles the HTTP API, the TCP/UDP transport and the node manager. `Start` binds the listeners and blocks until the context is cancelled. Without `TLS` the server listens on plaintext TCP only (HTTP/2 cleartext and Tai frames, for trusted networks) and does not start HTTP/3.

## Project Layout

```
proto/                .proto source files (canonical protocol definitions)
pb/                   Generated Go code from protobuf (committed)
server.go             Server interface, ServerConfig, NewServer / Start (assembles HTTP API, transport and node manager)
auth.go               AuthProvider interface
auth_default.go       DefaultAuth — Ed25519 JWT node_token
http.go               HTTP API server (5099): well-known, token refresh, heartbeat
transport.go          Transport server (6099 UDP): HTTP/3 + WebTransport, /v1/tai/connect, /v1/tai/light, /v1/tai/control
transport_tcp.go      Transport server (6099 TCP): routes by ALPN / first bytes to HTTP/2, h2c or the Tai frame entry
conn_state.go         Per-connection authorization state and unauthorized timeout
control.go            Control stream handler
frame.go              FrameCodec (length-prefixed protobuf)
node_manager.go       Node state management
types.go              Node, Session, ToolDefinition, and related types
mocknode.go           MockNode: simulated Tai node for tests
mockllm.go            MockLLM: OpenAI-compatible mock model server for tests
testutil.go           NewTestServer helper for self-contained testing
*_test.go             Unit and integration tests
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
| `servertool.proto` | Server tool request/response for `tai call` |

To regenerate Go code after editing `.proto` files:

```bash
make proto
```

Requires `protoc` and `protoc-gen-go`.

## Implemented

- **Protobuf definitions** — 4 `.proto` files with generated Go code; every Control message is a `Payload{namespace, kind, data}`
- **AuthProvider interface** — pluggable auth, ships with `DefaultAuth`:
  - Ed25519 JWT `node_token` (issue / validate / refresh, 7-day expiry)
  - `OperationToken` for session-scoped authorization
- **HTTP API** (port 5099):
  - `GET /.well-known/yao-transport` — transport metadata discovery
  - `POST /v1/tai/token/refresh` — refresh node_token
  - `POST /v1/tai/heartbeat` — fallback heartbeat
- **Transport server** (port 6099, TCP + UDP):
  - **Connection-level auth** — the first request of a connection (`POST /v1/tai/connect`, `Authorization: Bearer node_token`) authorizes the whole connection; later streams carry no credentials. A connection that is not authorized within `UnauthTimeout` (default 5s) is closed
  - **HTTP/3 (QUIC)** — primary protocol, UDP on the same port number as TCP, started only with TLS; WebTransport enabled (datagrams, partial stream reset); `/v1/tai/light` authenticates in the WebTransport CONNECT request (`Authorization`, `X-Tai-Node-Id`). The Light session hook has no handler registered yet, so authorized sessions are closed
  - **TCP** — with TLS: ALPN `h2` → HTTP/2, `tai/2` → Tai frame entry; without TLS: HTTP/2 preface → h2c, anything else → Tai frame entry. The Tai frame entry has no handler registered yet, so those connections are closed
  - L1 Control stream: `POST /v1/tai/control` on an authorized connection; register → register_ack (with `pool_config` from `ServerConfig.Pool`), ping → pong, forward-compatible unknown namespaces and kinds
- **Node manager** — in-memory node state, onRegister callback, sendControl; go_away is sent through the internal `goAway` / `goAwayAll`, which `Server` does not expose yet
- **Frame codec** — length-prefixed protobuf (4-byte big-endian header, 16 MB default, configurable through `ServerConfig.MaxFrameSize`)
- **Test infrastructure** — `NewTestServer(t)` (built on the same assembly as `NewServer`) with ephemeral ports and a self-signed TLS certificate generated at runtime; `MockNode` and `MockLLM`; integration tests run as H2/H3 sub-tests; 90%+ coverage

## Architecture

```
Tai binary  ←── Transport (6099) ──→  Your Server (import tai-golang)
            ←── HTTP API (5099)  ──→
```

The SDK is the server side. Tai is the client. They communicate over **HTTP/3 (QUIC)** as the primary protocol, with **HTTP/2 (TCP)** as fallback, using length-prefixed protobuf messages on both. The HTTP API handles discovery and token refresh; the Transport layer authorizes each connection with the node_token and carries the Control stream (Light and Heavy connections for sessions and transfers are not implemented yet).

## License

Apache-2.0
