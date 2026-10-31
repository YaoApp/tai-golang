# Architecture

## Two-Segment Design

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

- **Your Application**: Any Go application that imports `tai-golang`, creates an SDK `Server`, and manages nodes and sessions
- **Tai Node**: Runs `tai start`, connects outbound to the SDK server's `:5099` and `:6099`

The SDK embeds into your application as a library — no separate process.

## Transport Layers

| Port | Protocol | Purpose |
|------|----------|---------|
| `:5099` | HTTP | Well-known discovery (`/.well-known/yao-transport`), token refresh, heartbeat |
| `:6099` | H3 (QUIC) / H2 (TCP) | Control stream, Light pool (sessions) |

Both ports are configurable via `ServerConfig.HTTPPort` and `ServerConfig.TransportPort`.

## Connection Flow

1. Tai node discovers SDK server via `GET http://<host>:5099/.well-known/yao-transport`
2. Opens Control stream to `:6099` (bidirectional, long-lived)
3. Sends `register` with `node_id`, `version`, hardware, capabilities, container runtimes
4. SDK returns `register_ack` with `tai_id`, `public_key`, `key_id`, `pool_config`
5. Light pool established (H3 WebTransport or reversed H2)
6. Session streams opened on Light connections

## Session Dual-Stream Model

Each session occupies two Light streams:

- **ctrl stream**: Bidirectional, low-volume commands (prompt, cancel, permission requests)
- **data stream**: Tai → SDK, high-volume ACP event stream

Separation ensures urgent control messages (cancel, permission) are not blocked by large data frames.

```text
Your App (SDK)                    Tai Node
──────────                        ────────
OpenLightStream(open)    →    handleOpen → SessionManager.Open
                         ←    opened
OpenLightStream(data)    →    handleData → close(dataReady)
                              runDataPump (RecvEvent loop)
                              runCtrlPump unblocks
sendCtrl(prompt)         →    handlePrompt → session.Prompt
                              agent turn → events
                         ←    event frames on data stream
sendCtrl(close)          →    handleClose → Destroy → sendClosed
```
