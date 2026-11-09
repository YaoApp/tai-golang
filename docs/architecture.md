# Architecture

`tai-golang` is a Go SDK for building the **server side** of a Tai deployment.
Your application embeds the SDK as a library; Tai nodes connect to it, and your
application manages nodes, sessions, workspaces, tools, and user identities.

The SDK is not a Tai node and does not run an agent itself. The node owns the
Agent process, sandbox, and local workspace; the SDK owns connection identity,
session orchestration, business tools, and user-facing APIs.

## Deployment

```text
Your Go application
┌────────────────────────────────────────────┐
│ tai.NewServer(ServerConfig)                 │
│   HTTP API        :5099   discovery/token  │
│   Transport       :6099   H2 / H3           │
│                                            │
│   Node registry    Session objects         │
│   Workspace API    Remote Tools / Plugins  │
└──────────────────┬─────────────────────────┘
                   │  outbound node connection
        ┌──────────┴──────────┐
        │      Tai node       │
        │  Agent + sandbox    │
        │  local workspace    │
        └─────────────────────┘
```

- Nodes connect **outbound** to the SDK. Your application does not dial nodes.
- `HTTPPort` serves discovery and token refresh.
- `TransportPort` carries control, session, workspace, and tool traffic.
- The node registry is in memory. Treat node IDs as reconnectable handles, not
  durable identities.

## Core objects

| Object | Role | Documentation |
| --- | --- | --- |
| `Server` | Embeds transport, registries, and lifecycle | [API Reference](api-reference.md#server) |
| `Node` / `RemoteNode` | Connected node data and operations | [Node API](nodes.md) |
| `Session` | One agent session with commands and event streams | [Session](session.md) |
| `Workspace` | Remote filesystem, branches, history, watchers | [Workspace API](workspace.md) |
| `AuthProvider` | Node and user credential verification | [Auth](auth.md) |
| `RemoteTool` | Server-side tool exposed to nodes | [Remote Tools](remote-tools.md) |
| `PluginEntry` | Wasm step used by Remote Tool pipelines | [Remote Tool Plugins](remote-tool-plugins.md) |

## Startup flow

```go
srv, err := tai.NewServer(&tai.ServerConfig{
    HTTPPort:      5099,
    TransportPort: 6099,
    Auth:          authProvider,
})
if err != nil {
    log.Fatal(err)
}

go func() {
    if err := srv.Start(ctx); err != nil {
        log.Print(err)
    }
}()

node.OnRegister(func(rn types.RemoteNode) error {
    log.Printf("node connected: %s", rn.Info().ID)
    return nil
})

if err := srv.WaitReady(ctx); err != nil {
    log.Fatal(err)
}
```

The node then:

1. Discovers the SDK via `GET /.well-known/yao-transport`.
2. Opens the transport connection and authenticates with a node token.
3. Sends `register` with its ID, version, capabilities, and hardware.
4. Receives `register_ack` and becomes available through `node.List()`.

### Well-known response

`GET /.well-known/yao-transport` returns the transport discovery document:

| Field | Type | Meaning |
| --- | --- | --- |
| `transport_port` | `int` | Port for Control/Light/Heavy transport connections |
| `protocols` | `[]string` | Supported transports, e.g. `["HTTP/2"]` or `["HTTP/3","HTTP/2"]` |
| `tls` | `object`, optional | `ca_certificate` (PEM) and `fingerprint` for TLS verification |
| `tao_server` | `string`, optional | Optional upstream Tao server; normally omitted |
| `tool_endpoint` | `string`, optional | Tool H2 endpoint; derived from `transport_port` when omitted |
| `version` | `string` | Transport protocol version, e.g. `2.0.0` |

Example:

```json
{
  "transport_port": 6099,
  "protocols": ["HTTP/3", "HTTP/2"],
  "tls": {
    "ca_certificate": "-----BEGIN CERTIFICATE-----...",
    "fingerprint": "sha256:..."
  },
  "tool_endpoint": "https://node.example.com:6099/v1/tai/tool",
  "version": "2.0.0"
}
```

`tls` is present only when `ServerConfig.TLS` is configured. The SDK fills
`tool_endpoint` automatically when it is empty. `tao_server` is optional and
normally omitted; it is only populated by an `AuthProvider`/deployment that
advertises an upstream control plane, and the SDK does not require it.

## Sessions

A session is a logical agent conversation. It is opened on a specific node:

```go
rn := node.Get(nodeID)
sess, err := rn.OpenSession(ctx, &types.SessionOpenRequest{
    SessionID: "sess-001",
    Scopes:    types.Scope{types.SessionAccess, types.WorkspaceRead},
    Request: &types.SessionRequest{
        Cwd: "/workspace/project",
    },
})
```

The SDK opens two logical channels per session:

| Channel | Direction | Purpose |
| --- | --- | --- |
| Control | bidirectional | prompt, follow-up, cancel, steer, permissions, realtime control |
| Data | node → SDK | agent events, tool updates, render/audio output |

The separation keeps urgent control messages independent of high-volume event
data. From the application's perspective, use:

- `sess.Prompt` / `Followup` / `Steer` / `Cancel`
- `sess.RecvEvent` for the data stream
- `sess.RecvAction` for control-side actions
- `sess.Done` / `sess.Close`

Events are documented in [ACP Events](events.md).

### Single-reader rule

`RecvEvent` and `RecvAction` are **single-reader** APIs. Run one goroutine per
channel and dispatch to your application logic; do not call them from multiple
goroutines.

## Realtime

Realtime upgrades an open session into a voice conversation. It is not a new
session and does not replace the data stream; the same session gains audio,
render, and optionally motion channels.

```go
err := sess.StartRealtime(&types.RealtimeRequest{
    Providers: types.Providers{
        Primary: types.Provider{
            ProviderID: "openai",
            Model:      "gpt-realtime-mini",
            APIKey:     os.Getenv("OPENAI_API_KEY"),
        },
    },
    Audio: &types.AudioConfig{
        InputFormat:  "pcm16",
        OutputFormat: "pcm16",
        SampleRate:   24000,
        Channels:     1,
        Voice:        "alloy",
    },
})
if err != nil {
    return err
}
defer sess.StopRealtime()
```

`StartRealtime` blocks until the node confirms the upgrade. While it is running,
the session exposes:

| Channel / method | Direction | Purpose |
| --- | --- | --- |
| `AudioAppend(data)` | app → node | microphone PCM frames |
| `RecvAudio()` | node → app | model speech PCM frames |
| `RecvRender()` | node → app | transcript, emotion, audio lifecycle |
| `RecvMotion()` | node → app | avatar blendshapes, when enabled |
| `AudioCommit()` / `AudioClear()` | app → node | manual VAD control |
| `AudioInterrupt()` | app → node | barge-in / stop playback |
| `ResponseCreate()` | app → node | manual response trigger |
| `UpdateRealtime(opts)` | app → node | change voice or modality |
| `StopRealtime()` | app → node | leave realtime; session stays open |

Important constraints:

- `StartRealtime` reads the control stream internally while waiting for
  confirmation. Do not have another goroutine reading `RecvAction` at that
  moment.
- After `StartRealtime` returns, start exactly one `RecvAction` relay
  goroutine. Stop it before starting realtime again.
- `RecvAudio`, `RecvRender`, and `RecvMotion` are read by their own consumers;
  they are not part of the ACP event stream.
- Realtime provider endpoints and keys belong to `RealtimeRequest.Providers`
  (or the session environment), not to the SDK server config.
- Video methods exist in the interface but are not implemented.

Transport behavior is automatic: HTTP/3 uses datagrams for audio/motion, HTTP/2
uses dedicated streams. Application code uses the same methods on both. See
[Session Realtime](session-realtime.md) for the integration guide, and
[Render Protocol](render.md) for render payloads.

## Transport

The transport is the only part of the SDK that exposes node-facing wire
behavior. It uses two ports:

| Port | Default | Protocol | Purpose |
| --- | --- | --- | --- |
| HTTP | `5099` | HTTP | Discovery, token refresh |
| Transport | `6099` | HTTP/2 (TCP) or HTTP/3 (QUIC) | Control, Light, Heavy traffic |

The node chooses the transport protocol from discovery. Light streams carry
control and session data; Heavy connections carry large binary transfers.
Pool sizes and timeouts are configurable with `ServerConfig.Pool`.

Applications normally interact with the transport only through:

- `Server.Addr()` / `Server.WaitReady()`
- `ServerConfig.TransportPort`, `HTTPPort`, `TLS`, `MaxFrameSize`
- connection and authentication errors surfaced by the SDK

Detailed wire formats are intentionally not part of the public application API.

## Workspaces

Workspaces are addressed through `RemoteNode`:

```go
ws, err := rn.OpenWorkspace(ctx, "ws-001")
branch, err := ws.GetBranch(ctx, "main")
f, err := branch.FS().Open("README.md")
```

Workspace files use the `fs.FS` model, with branch, history, merge, and watch
extensions. See [Workspace API](workspace.md) and [Resource URI](uri.md).

## Remote Tools and Plugins

Remote Tools are business capabilities owned by your application:

```go
srv.RegisterRemoteTool(&remotetool.Tool{
    Entry: &pb.RemoteToolEntry{
        Name:  "demo.echo",
        Group: "demo",
    },
    Handler: func(ctx context.Context, in []byte) ([]byte, error) {
        return in, nil
    },
})
```

The node exposes the tool to its Agent and returns the result. The SDK only
runs the registered handler.

A Remote Tool pipeline can include a Wasm Plugin step. Plugin artifacts are
local files registered by path, then downloaded by nodes on demand. See
[Remote Tools](remote-tools.md) and [Remote Tool Plugins](remote-tool-plugins.md).

## Auth and tenancy

`ServerConfig.Auth` is required. The SDK uses `AuthProvider` for two purposes:

- verify the node token on every connection
- issue and verify a short-lived user token with scopes for each session

The `user_token` is carried in the first frame of a business stream; the node
stores and forwards it for Remote Tool validation. Agent processes and shared
workspace files do not receive it.

`auth.DefaultAuth` is a demo implementation for the complete local flow, not a
production user system. Add scopes only when the operation needs them. See
[Auth](auth.md).

## Scaling and operations

- The node registry is process-local; one SDK instance owns the nodes connected
  to it.
- Run multiple SDK instances behind a load balancer only when node connections
  are routed consistently to the instance that owns them.
- `Server.Close` is graceful and idempotent.
- Set `ShutdownTimeout` for in-flight work and use `WaitReady` in startup
  probes.
- Configure TLS for production; plaintext is intended for local development.
- Do not log node tokens, user tokens, or tool inputs that contain credentials.

## Where to go next

1. [Quick Start](quickstart.md) — run the demo and connect a node.
2. [API Reference](api-reference.md) — quick lookup of all public methods.
3. [Node API](nodes.md) — node lifecycle and operations.
4. [Session](session.md) — open, drive, and close sessions.
5. [Workspace API](workspace.md) — files, branches, history.
6. [Remote Tools](remote-tools.md) — expose business capabilities.
7. [Auth](auth.md) — connect your identity system.
