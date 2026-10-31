# Tai SDK Demo App

QA scaffold for manual end-to-end testing of the Tai SDK (`tai-golang`).

## Quick Start

```bash
# Build frontend + run
make build
./tai-demo --port 3000

# Or run from source
make build-web
go run . --port 3000
```

Open http://localhost:3000.

## Connect a Tai Node

1. Go to **Auth** page → generate a node token
2. Copy the `tai start` command
3. Run it in another terminal

## Features

| Page | Description |
|------|-------------|
| Auth | Server info, token generation, `tai start` command builder |
| Nodes | Registered node list with real-time SSE updates |
| Sessions | Create sessions, dual WebSocket (ctrl+data) chat interface |
| Docs | SDK documentation browser (Markdown from `docs/`) |

## Development

```bash
# Backend (terminal 1)
go run . --port 3000

# Frontend with hot-reload (terminal 2)
cd web && npm run dev
```

Vite dev server on `:5173` proxies `/api` and `/ws` to `:3000`.

## Ports

| Port | Service |
|------|---------|
| 3000 | Demo API + frontend |
| 5099 | SDK HTTP (well-known, token refresh) |
| 6099 | SDK Transport (Control, Light streams) |
