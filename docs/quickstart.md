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

Open http://localhost:3000 in your browser.

## Connect a Tai Node

1. Go to the **Auth** page and generate a node token for a node ID (e.g. `demo-node`)
2. Copy the `tai start` command shown on the page
3. In another terminal, run the command:

```bash
tai start --node-id demo-node --server http://localhost:5099 --node-token <token>
```

4. The node appears on the **Nodes** page
5. Go to **Sessions** to create a session and start interacting

## Use as a Library

```go
import (
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
    Auth: auth.DefaultAuth(privateKey),
})

server.Start(ctx)

// After a Tai node connects:
n := node.Get("node-1")
session, err := n.OpenSession(ctx, &types.SessionOpenRequest{
    SessionID: "sess-001",
    Token:     operationToken,
    Request:   &types.SessionRequest{Runtime: "agent"},
})
```
