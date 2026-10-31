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
| `Nodes()` | All registered nodes |
| `NodeByID(nodeID)` | Lookup node by ID |
| `OpenSession(ctx, req)` | Open a session on a node |
| `OnNodeRegister(fn)` | Register node callback |
| `OnSessionEvent(fn)` | Register session event callback |
| `RegisterTool(tool)` | Register a server-side tool (not implemented) |
| `UnregisterTool(name)` | Unregister a server-side tool (not implemented) |
| `SendControl(nodeID, msg)` | Send a control message to a node |
| `Close()` | Shutdown (idempotent) |

## Session (17 methods)

```go
session, err := server.OpenSession(ctx, &OpenSessionRequest{
    NodeID:    "node-1",
    SessionID: "sess-001",
    Token:     operationToken,
    Request:   &SessionRequest{Runtime: "agent"},
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
