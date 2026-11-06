# tai-golang

Go SDK for the Tai Transport Protocol v2. This package implements the **server side** of the protocol, enabling any Go application to accept connections from Tai nodes, manage workspaces, run sessions, and execute agent workloads.

Tai is a computer agent installed on every machine. This SDK handles the transport layer so your application doesn't need to implement the protocol details.

## Status

**Phase 2 in progress** — Transport (L0–L2), Light/Heavy pool, Workspace (4 interfaces, 36 commands, Context API, Skills API), Session (17 methods + Realtime audio, Daemon mode, OperationToken validation, Recovery Buffer + resume), and the Demo App with React frontend are all working.

Not yet implemented: Server Tools, Tunnel, Realtime Video, ONNX model integration for balanced render preset.

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
    "github.com/yaoapp/tai-golang/node"
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

    node.OnRegister(func(n tai.RemoteNode) error {
        log.Printf("node registered: %s", n.Info().ID)
        return nil
    })

    go func() {
        if err := server.Start(context.Background()); err != nil {
            log.Fatal(err)
        }
    }()

    // Block until interrupt
    select {}
}
```

## Opening a Session

```go
// Issue an operation token for the session
opToken, _ := authProvider.IssueOperationToken("node-1", "sess-001", []string{"session"}, nil)

// Describe the models the session may use. Only "model" is required; the
// provider is inferred from base_url when provider_id is empty.
llm := json.RawMessage(`{
  "primary": {"provider_id": "deepseek", "model": "deepseek-chat"},
  "roles":   {"light": {"provider_id": "deepseek", "model": "deepseek-chat"}}
}`)

// Get a registered node and open a session
n := node.Get("node-1")
session, err := n.OpenSession(ctx, &types.SessionOpenRequest{
    SessionID: "sess-001",
    Token:     opToken,
    Request: &tai.SessionRequest{
        Runtime: "agent",
        LLM:     llm,
        Env:     map[string]string{"DEEPSEEK_API_KEY": "sk-..."},
    },
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

See [LLM Providers](docs/llm-providers.md) for the full endpoint fields and how
to switch models at runtime.

## Realtime (Voice)

```go
// Start realtime mode
session.StartRealtime(&tai.RealtimeRequest{
    Providers: tai.Providers{
        Primary: tai.Provider{
            ProviderID: "openai", Model: "gpt-4o-realtime-preview",
            BaseURL: "wss://api.openai.com/v1/realtime", APIKey: "sk-...",
        },
    },
    Audio: &tai.AudioConfig{OutputFormat: "pcm16", SampleRate: 24000},
})

// Send audio + receive speech
session.AudioAppend(pcmData)

go func() {
    for frame := range session.RecvAudio() {
        speaker.Play(frame.Data)
    }
}()

// Receive render stream (transcript, emotion)
go func() {
    for evt := range session.RecvRender() {
        switch evt.Type {
        case "transcript.delta":
            subtitle.Append(evt.Content)
        case "emotion":
            mood.Set(evt.Emotion, evt.Intensity)
        }
    }
}()

session.StopRealtime()
```

## Render (Avatar Motion)

```go
// Start with motion enabled
session.StartRealtime(&tai.RealtimeRequest{
    Providers: providers,
    Audio:  &tai.AudioConfig{OutputFormat: "pcm16", SampleRate: 24000},
    Motion: &tai.MotionConfig{Enabled: true, Provider: "fast", Profile: "arkit_52", FPS: 30},
})

// Receive motion frames (30fps, 87 bytes each)
go func() {
    for frame := range session.RecvMotion() {
        avatar.ApplyBlendshapes(frame.Face, frame.Head, frame.Gaze)
    }
}()

// Receive render stream events (transcript, audio lifecycle, emotion)
go func() {
    for evt := range session.RecvRender() {
        switch evt.Type {
        case "transcript.delta", "transcript.done":
            subtitle.Update(evt.Type, evt.Content, evt.Transcript)
        case "emotion":
            mood.Set(evt.State, evt.Emotion, evt.Intensity)
        }
    }
}()
```

See [docs/render.md](docs/render.md) for the complete render protocol.

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

// Check existence
exists, _ := bfs.Exists("hello.txt")

// Archive extraction (auto-detects gzip/zip)
bfs.WriteArchive(".skills/my-skill", archiveReader)

// Read manifest
manifest, _ := bfs.ReadManifest(".skills/my-skill")

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
session/              Session implementation (17 methods, dual-stream, recovery buffer)
workspace/            Remote workspace API (fs.FS + write, branch, history, watch, context, skills)
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
- [Session](docs/session.md)
- [LLM Providers](docs/llm-providers.md)
- [Session File Inputs](docs/session-inputs.md)
- [Session Context](docs/session-context.md)
- [Session Skills](docs/session-skills.md)
- [Session Attachments](docs/session-attachments.md)
- [Session Realtime](docs/session-realtime.md)
- [ACP Events Reference](docs/events.md)
- [Realtime Protocol](docs/realtime.md)
- [Render Protocol](docs/render.md)
- [Resource URI Convention](docs/uri.md)
- [Demo App Guide](docs/demo-guide.md)

Run the demo app to browse documentation with syntax highlighting:

```bash
cd app && make build && DEEPSEEK_API_KEY=<key> ./tai-demo -port 3000
```

## Implemented

- **Transport (L0–L2)**: HTTP API (:5099), TCP/UDP transport (:6099), H3 (QUIC) + H2 fallback, Light/Heavy connection pool
- **Node**: Registry, RemoteNode interface, online/offline lifecycle, GoAway
- **Session (28 methods)**: Prompt, Followup, Cancel, Steer, UpdateQueue, ResolvePermission, ResolveElicitation, SetConfigOption, UpdateSession, RecvEvent, RecvAction, StartRealtime, UpdateRealtime, StopRealtime, AudioAppend, AudioCommit, AudioClear, AudioInterrupt, RecvAudio, RecvMotion, RecvRender, SendVideoFrame (stub), RecvVideoFrame (stub), Close, ID, NodeID, Status, Done
- **Realtime Audio**: QUIC datagram (H3) + bidi stream fallback (H2), EncodeAudioFrame/DecodeAudioFrame, DatagramConn interface, H2 stale frame detection
- **Render (Avatar Motion)**: MotionFrame 87-byte wire format (Pack/Unpack), motion datagram encode/decode (95 bytes), RecvMotion (QUIC datagram), RecvRender (render stream), RenderEvent type, MotionConfig (provider/profile/fps)
- **Session Recovery**: Recovery Buffer (per-session 1000-message ring buffer), session.resume with seq replay, token.refresh periodic renewal
- **Workspace**: CRUD, branch management (create/delete/merge), file operations (fs.FS + write extensions + Exists/WriteArchive/ReadManifest), commit/rollback, history query, GC, watch — 36 transport command kinds
- **Context API**: Four-level scope model (bot → home → board → session), SeedContext, ReadContext, MergeContext
- **Skills API**: SkillPack, SkillHash, SyncSkills with manifest-based incremental sync
- **Auth**: Ed25519 JWT node_token (issue/validate/refresh), OperationToken (issue/sign/validate)
- **Types**: Full ACP v2 type coverage (Event, ContentBlock, Permission, Elicitation, Queue, Disposition, Config)
- **Proto**: 4 `.proto` files with generated Go (control, session, computer, servertool)
- **Demo App**: React frontend (nodes, sessions, workspaces, file manager, docs browser) + Go API server
- **Test Infrastructure**: MockNode, MockLLM, MockSession (+ Realtime auto-reply), MockLightConn/Stream, 400+ test functions

## Not Yet Implemented

- Server Tools (RegisterTool / tai call)
- Tunnel
- Realtime Video (Plan 7b)
- ONNX model integration for balanced render preset
- Container remote management

## License

Apache-2.0
