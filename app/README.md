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

Open http://localhost:3000. A default account is seeded on startup and
prefilled in the login form:

```
demo@example.com / demo1234
```

Just press **Sign in** (or register another account, email + password ≥8 chars).
Accounts live in memory; the email becomes the session's `user_id` and
`demo-team` its `team_id`, so the console shows a real login identity instead of
the old `local-user` placeholder.

## Environment Variables

Provider API keys are read from the **server** environment and injected into
sessions / Realtime requests; the browser never sends them unless you paste one
into the Realtime panel on purpose.

| Variable | Required for | Notes |
|----------|--------------|-------|
| `DEEPSEEK_API_KEY` | Text chat (prompt / agent) | Injected into the session env as `DEEPSEEK_API_KEY` |
| `DEEPSEEK_BASE_URL` | Text chat | Optional; defaults to `https://api.deepseek.com/v1` |
| `OPENAI_API_KEY` | **Realtime** (OpenAI Realtime API, e.g. `gpt-realtime-mini`) | Without it `Start Realtime` returns an actionable error naming this variable; text chat still works |
| `QWEN_API_KEY` | Realtime with the `qwen` provider | Optional, same mechanism |

The demo logs which keys are configured at startup (values are never printed):

```text
env: DEEPSEEK_API_KEY=set OPENAI_API_KEY=unset QWEN_API_KEY=unset
```

Realtime and text chat use **different providers**: a DeepSeek key cannot drive
the OpenAI Realtime API. Start the demo with both when you need both:

```bash
DEEPSEEK_API_KEY=sk-... OPENAI_API_KEY=sk-... ./tai-demo --port 3000
```

You can also paste a key into the Realtime panel's **API KEY** field for a
per-session override (it is sent as `providers.primary.api_key`).

## Connect a Tai Node

1. Go to **Auth** page → generate a node token
2. Copy the `tai start` command
3. Run it in another terminal

## Features

| Page | Description |
|------|-------------|
| Auth | Server info, node token + v2 user token (with identity) issuance, `tai start` command builder |
| Nodes | Registered node list with real-time SSE updates |
| Sessions | Create sessions, dual WebSocket (ctrl+data) chat interface, session owner display |
| Docs | SDK documentation browser (Markdown from `docs/`) |

## API

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/auth/info` | Well-known transport metadata + signing public key |
| POST | `/api/auth/node-token` | Issue a node connection credential (`node_token`) |
| POST | `/api/auth/register` | Create a demo account `{email, password, name?}` (auto-login, sets HttpOnly cookie) |
| POST | `/api/auth/login` | Log in `{email, password}` → sets the session cookie |
| POST | `/api/auth/logout` | Invalidate the login session |
| GET | `/api/auth/me` | Current account (401 when not logged in) |
| POST | `/api/auth/user-token` | Issue a v2 user credential for the logged-in account: `{nodeId, scopes[]}` → `{token, identity{user_id,team_id,scope}}` |
| GET | `/api/nodes` | List registered nodes |
| GET/POST | `/api/sessions` | List / create sessions (**login required**; create returns the session's `userId`/`teamId` = login email / `demo-team`) |
| POST | `/api/sessions/{id}/resume` | Resume a disconnected session (identity is re-issued and may change) |
| DELETE | `/api/sessions/{id}` | Close a session |

`/api/auth/operation-token` is retired: v2 replaces the v1 operation credential
with the user credential defined in `docs/v2/runtime/identity.md`.

All `/api/sessions*` and `/api/workspaces*` endpoints require login. The SDK
passes the request context to `AuthProvider.IssueUserTokenContext`, so the
credential claims carry the logged-in email; the console displays the same value.

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
