# Quick Start

## Prerequisites

- Go 1.26+
- Node.js 22+ (for the demo frontend)
- A Tai binary (`tai start`)

## Run the Demo App

```bash
cd app

# Build frontend + Go binary
make build

# Start demo (SDK server + API + frontend)
DEEPSEEK_API_KEY=<your-key> ./tai-demo --port 3000
```

Or run directly without building a binary:

```bash
cd app && make build-web
DEEPSEEK_API_KEY=<your-key> go run . --port 3000
```

Add `OPENAI_API_KEY=<your-key>` to either command to use Realtime voice: the
demo selects the key by `provider_id`, and the built-in Realtime preset uses
OpenAI.

Open http://localhost:3000 in your browser.

## Connect a Tai Node

1. Go to the **Auth** page and generate a node token for a node ID (e.g. `demo-node`)
2. Copy the `tai start` command shown on the page
3. In another terminal, run the command:

```bash
tai start --node-id demo-node --server http://localhost:5099 --node-token <token> --data ./tai-volumns
```

`--data` is the workspace storage root. Omit it and the node falls back to
`~/.tai/nodes/<node-id>`, which is fine for a one-off test but scatters state
across your home directory. Pointing it at a project-local directory keeps
credentials and workspaces together and makes them easy to delete.

4. The node appears on the **Nodes** page
5. Go to **Sessions** to create a session and start interacting

## Use as a Library

```go
import (
    "encoding/json"

    tai "github.com/yaoapp/tai-golang"
    "github.com/yaoapp/tai-golang/auth"
    "github.com/yaoapp/tai-golang/node"
    "github.com/yaoapp/tai-golang/types"
)

// Node registration callback
node.OnRegister(func(n types.RemoteNode) error {
    log.Printf("node %s registered", n.Info().ID)
    return nil
})

server, err := tai.NewServer(&tai.ServerConfig{
    TransportPort: 6099,
    HTTPPort:      5099,
    Auth:          auth.DefaultAuth(privateKey),
})

go server.Start(ctx)

// After a Tai node connects:
n := node.Get("node-1")
opToken, _ := authProvider.IssueOperationToken("node-1", "sess-001", []string{"session"}, nil)

// The models the session may use. Only "model" is required; the provider is
// inferred from base_url when provider_id is empty.
llm := json.RawMessage(`{
  "primary": {"provider_id": "deepseek", "model": "deepseek-chat"},
  "roles":   {"light": {"provider_id": "deepseek", "model": "deepseek-chat"}}
}`)

session, err := n.OpenSession(ctx, &types.SessionOpenRequest{
    SessionID: "sess-001",
    Token:     opToken,
    Request: &types.SessionRequest{
        Runtime: "tai",
        LLM:     llm,
        Env:     map[string]string{"DEEPSEEK_API_KEY": os.Getenv("DEEPSEEK_API_KEY")},
    },
})
```

See [LLM Providers](llm-providers.md) for every endpoint field and how to switch
models at runtime.

## Realtime (Voice)

Realtime needs a provider key. The demo reads it from the environment by
`provider_id`, so start it with `OPENAI_API_KEY` when the Realtime role points at
OpenAI (the built-in preset does):

```bash
OPENAI_API_KEY=<your-key> DEEPSEEK_API_KEY=<your-key> ./tai-demo --port 3000
```

```go
// Start realtime mode
err := session.StartRealtime(&types.RealtimeRequest{
    Providers: types.Providers{
        Primary: types.Provider{
            ProviderID: "openai", Model: "gpt-realtime-mini",
            BaseURL: "wss://api.openai.com/v1/realtime", APIKey: "sk-...",
        },
    },
    Audio: &types.AudioConfig{
        OutputFormat: "pcm16", SampleRate: 24000, Channels: 1,
        VAD: &types.VADConfig{Mode: "server_vad"},
    },
})

// Send microphone audio
session.AudioAppend(pcmData)
session.AudioCommit() // manual VAD: signal end of speech

// Receive model speech
go func() {
    for frame := range session.RecvAudio() {
        speaker.Play(frame.Data) // PCM playback
    }
}()

// Receive render stream events (transcript, emotion)
go func() {
    for evt := range session.RecvRender() {
        switch evt.Type {
        case "transcript.delta":
            subtitle.Append(evt.Content)
        case "transcript.done":
            subtitle.Finalize(evt.Transcript)
        }
    }
}()

// Receive ctrl events
go func() {
    for {
        action, err := session.RecvAction()
        if err != nil { break }
        switch action.Kind {
        case "audio.speech_started":
            // model started speaking
        case "realtime.task_start":
            // agent started processing a task
        }
    }
}()

// Stop realtime
session.StopRealtime()
```
