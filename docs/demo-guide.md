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

`DEEPSEEK_API_KEY` covers the text agent. Realtime voice needs a provider key
too: the demo picks it from the environment by `provider_id`, and the built-in
Realtime preset uses OpenAI, so add `OPENAI_API_KEY=<your-key>` when you want to
test voice.

This starts:
- SDK transport server on `:6099`
- SDK HTTP server on `:5099`
- Demo API + frontend on `:3000`

## API Endpoints

The demo backend exposes JSON REST APIs for the UI and WebSocket relays for live
data. `/api/sessions/*`, `/api/workspaces/*`, and `/api/auth/user-token` require
a demo login; the remaining endpoints are open in the demo. The `Protocol`
column marks HTTP (`HTTP`) versus WebSocket (`WebSocket`) endpoints.

### Auth

| Method | Path | Protocol | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/auth/info` | HTTP | SDK ports, protocols, version, and LLM key state (`set` / `unset`, never the value) |
| `POST` | `/api/auth/register` | HTTP | Create a demo user |
| `POST` | `/api/auth/login` | HTTP | Log in and create the session cookie |
| `POST` | `/api/auth/logout` | HTTP | Log out |
| `GET` | `/api/auth/me` | HTTP | Current demo user |
| `POST` | `/api/auth/node-token` | HTTP | Issue a node token for `nodeId` |
| `POST` | `/api/auth/user-token` | HTTP | Issue a scoped user token for `nodeId` |

### Nodes and events

| Method | Path | Protocol | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/nodes` | HTTP | List registered Tai nodes |
| `GET` | `/ws/events` | WebSocket | WebSocket stream of global node/session/workspace/realtime events |

### Sessions

| Method | Path | Protocol | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/sessions` | HTTP | List sessions |
| `POST` | `/api/sessions` | HTTP | Create and open a session |
| `DELETE` | `/api/sessions/{id}` | HTTP | Close and delete a session |
| `POST` | `/api/sessions/{id}/resume` | HTTP | Resume a disconnected session |
| `GET` | `/ws/sessions/{id}/ctrl` | WebSocket | WebSocket ctrl relay (commands and ctrl events) |
| `GET` | `/ws/sessions/{id}/data` | WebSocket | WebSocket data relay (ACP events) |
| `GET` | `/ws/sessions/{id}/render` | WebSocket | WebSocket render relay (transcript/emotion) |
| `GET` | `/ws/sessions/{id}/audio` | WebSocket | WebSocket audio relay (PCM) |
| `GET` | `/ws/sessions/{id}/motion` | WebSocket | WebSocket motion relay (avatar frames) |

### Workspaces

| Method | Path | Protocol | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/workspaces` | HTTP | List workspaces |
| `POST` | `/api/workspaces` | HTTP | Create a workspace |
| `DELETE` | `/api/workspaces/{id}` | HTTP | Delete a workspace |
| `PUT` | `/api/workspaces/{id}/meta` | HTTP | Update name/owner |
| `GET` | `/api/workspaces/{id}/branches` | HTTP | List branches |
| `POST` | `/api/workspaces/{id}/branches` | HTTP | Create a branch |
| `DELETE` | `/api/workspaces/{id}/branches/{name}` | HTTP | Delete a branch |
| `POST` | `/api/workspaces/{id}/branches/{name}/commit` | HTTP | Commit a branch |
| `POST` | `/api/workspaces/{id}/branches/{name}/rollback` | HTTP | Roll back a branch |
| `POST` | `/api/workspaces/{id}/merge` | HTTP | Merge branches |

### Context, skills, and history

| Method | Path | Protocol | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/workspaces/{id}/context` | HTTP | Read context files |
| `PUT` | `/api/workspaces/{id}/context/{scope}/{scopeID}/{file}` | HTTP | Write a context file |
| `POST` | `/api/workspaces/{id}/context/{scope}/{scopeID}/seed` | HTTP | Seed context files |
| `GET` | `/api/workspaces/{id}/context/merged` | HTTP | Read merged context Markdown |
| `GET` | `/api/workspaces/{id}/skills` | HTTP | List skill packages |
| `POST` | `/api/workspaces/{id}/skills` | HTTP | Upload a skill package |
| `GET` | `/api/workspaces/{id}/branches/{branch}/log` | HTTP | Branch commit log |
| `GET` | `/api/workspaces/{id}/history` | HTTP | History query |
| `GET` | `/api/workspaces/{id}/commits/{commitId}` | HTTP | Commit details |

### Files and uploads

| Method | Path | Protocol | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/workspaces/{id}/branches/{branch}/files` | HTTP | File metadata |
| `GET` | `/api/workspaces/{id}/branches/{branch}/files/content` | HTTP | Read file |
| `PUT` | `/api/workspaces/{id}/branches/{branch}/files/content` | HTTP | Write/upload file |
| `DELETE` | `/api/workspaces/{id}/branches/{branch}/files` | HTTP | Delete file or directory |
| `POST` | `/api/workspaces/{id}/branches/{branch}/files/mkdir` | HTTP | Create directory |
| `POST` | `/api/workspaces/{id}/branches/{branch}/files/rename` | HTTP | Rename/move |
| `GET` | `/api/workspaces/{id}/branches/{branch}/files/preview` | HTTP | Preview text/image |
| `POST` | `/api/workspaces/{id}/branches/{branch}/files/upload-init` | HTTP | Start chunked upload |
| `PUT` | `/api/workspaces/{id}/branches/{branch}/files/upload-chunk` | HTTP | Upload chunk |
| `POST` | `/api/workspaces/{id}/branches/{branch}/files/upload-complete` | HTTP | Finish chunked upload |

### Documentation

| Method | Path | Protocol | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/docs` | HTTP | List SDK docs |
| `GET` | `/api/docs/{name}` | HTTP | Read one document |

## Pages

### Auth (`/`)

Displays SDK server information and provides token generation.

1. View transport port, protocols, version, and whether LLM keys are configured
2. Enter a node ID and click **Generate** to create a node token
3. Copy the generated `tai start` command

### Nodes (`/nodes`)

Shows registered Tai nodes with real-time WebSocket updates.

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
Supports syntax-highlighted code blocks, GFM tables, and Mermaid diagrams. A
diagram whose syntax fails to parse renders its source instead of breaking the
page.

## Development Mode

Run backend and frontend separately for hot-reload:

```bash
# Terminal 1: Go backend (needs DEEPSEEK_API_KEY for agent sessions;
# add OPENAI_API_KEY to test Realtime voice)
DEEPSEEK_API_KEY=<your-key> OPENAI_API_KEY=<your-key> go run . --port 3000

# Terminal 2: Vite dev server (proxies API to :3000)
cd web && npm run dev
```

## Architecture

```text
Browser (:5173 dev / :3000 prod)
  │
  ├── REST API ────────────► Go API Server (:3000)
  │                              │
  ├── WebSocket /ws/sessions/{id}/ctrl ┤──► session.Prompt/Cancel/...
  │                              │    session.RecvAction() → WebSocket
  │                              │
  └── WebSocket /ws/sessions/{id}/data ┤──► session.RecvEvent() → WebSocket
                                 │
                            SDK Server
                         (:5099 HTTP + :6099 Transport)
                                 │
                            Tai Node
                         (tai start --server ...)
```
