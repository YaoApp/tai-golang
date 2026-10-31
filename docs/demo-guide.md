# Demo App Guide

The demo app is a QA scaffold for manually testing the Tai SDK end-to-end.

## Starting the Demo

```bash
cd tai-golang/app

# Build frontend (first time or after frontend changes)
make build-web

# Run the demo
DEEPSEEK_API_KEY=<your-key> go run . --port 3000
```

This starts:
- SDK transport server on `:6099`
- SDK HTTP server on `:5099`
- Demo API + frontend on `:3000`

## Pages

### Auth (`/`)

Displays SDK server information and provides token generation.

1. View the transport port, protocols, and public key
2. Enter a node ID and click **Generate** to create a node token
3. Copy the generated `tai start` command

### Nodes (`/nodes`)

Shows registered Tai nodes with real-time SSE updates.

- Each card shows: Node ID, tai_id, registration time
- New registrations appear automatically via Server-Sent Events

### Sessions (`/sessions`)

Manage and interact with agent sessions.

1. Select a node from the dropdown
2. Click **Create Session** to open a new session
3. Click a session ID to open the detail view
4. In the detail view:
   - Type prompts in the input box and press Enter
   - View assistant responses in chat bubbles
   - Thinking content is shown in collapsible blocks
   - Tool calls and system events are displayed inline
   - Toggle **Raw** to see the raw ACP event stream
   - Click **Cancel** to cancel the current turn
   - Click **Close** on the sessions list to end a session

### Docs (`/docs`)

Browse SDK documentation rendered from Markdown files in `tai-golang/docs/`.
Supports syntax-highlighted code blocks and GFM tables.

## Development Mode

Run backend and frontend separately for hot-reload:

```bash
# Terminal 1: Go backend (needs DEEPSEEK_API_KEY for agent sessions)
DEEPSEEK_API_KEY=<your-key> go run . --port 3000

# Terminal 2: Vite dev server (proxies API to :3000)
cd web && npm run dev
```

## Architecture

```text
Browser (:5173 dev / :3000 prod)
  │
  ├── REST API ────────────► Go API Server (:3000)
  │                              │
  ├── WS /ws/sessions/{id}/ctrl ┤──► session.Prompt/Cancel/...
  │                              │    session.RecvAction() → WS
  │                              │
  └── WS /ws/sessions/{id}/data ┤──► session.RecvEvent() → WS
                                 │
                            SDK Server
                         (:5099 HTTP + :6099 Transport)
                                 │
                            Tai Node
                         (tai start --server ...)
```
