# Session Protocol

## Wire Format

All frames use length-prefixed protobuf:

```text
[4 bytes BE uint32 length][N bytes protobuf Payload]
```

```protobuf
message Payload {
  string namespace = 1;  // "control" | "session"
  string kind      = 2;
  bytes  data      = 3;
}
```

## Session Handshake

The SDK opens both streams. Direction below refers to the **opening frame** only.

| Step | Stream | Direction | Kind | Payload |
|------|--------|-----------|------|---------|
| 1 | ctrl | Yao → Tai | `open` | `SessionOpen { session_id, node_id, token, request }` |
| 2 | ctrl | Tai → Yao | `opened` | `SessionOpened { session_id, runtime, isolation, last_data_seq }` |
| 2' | ctrl | Tai → Yao | `error` | `SessionError { session_id, code, message }` (on failure) |
| 3 | data | Yao → Tai | `data` | `SessionData { session_id }` (opens data stream) |
| — | data | Tai → Yao | events | ACP event frames flow back on the data stream |
| n | ctrl | Tai → Yao | `closed` | `SessionClosed { session_id, reason, exit_code }` |

After step 3, the data stream becomes **unidirectional Tai → Yao** for ACP events.

### Session Resume

On reconnect, the SDK opens a new ctrl stream with `resume` instead of `open`:

| Step | Stream | Direction | Kind | Payload |
|------|--------|-----------|------|---------|
| 1 | ctrl | Yao → Tai | `resume` | `SessionResume { session_id, token, last_ctrl_seq, last_data_seq }` |
| 2 | ctrl | Tai → Yao | `opened` | `SessionOpened { session_id, last_data_seq }` (server's buffered seq for recovery) |
| 3 | data | Yao → Tai | `data` | `SessionData { session_id }` (reopens data stream; server replays buffered events) |

## Control Stream Commands (Yao → Tai)

Business frames are wrapped in a `Payload { namespace: "session", kind: "message" }` envelope.
The `data` field contains a protobuf `SessionMessage { seq, kind, data(JSON) }`.

| Kind | Data | Session Method |
|------|------|----------------|
| `prompt` | `UserMessage` | `Prompt(msg)` |
| `followup` | `UserMessage` | `Followup(msg)` |
| `cancel` | `null` | `Cancel()` |
| `steer` | `UserMessage` | `Steer(msg)` |
| `update_queue` | `QueueAction` | `UpdateQueue(action)` |
| `resolve_permission` | `{ requestId, response }` | `ResolvePermission(id, resp)` |
| `resolve_elicitation` | `{ id, response }` | `ResolveElicitation(id, resp)` |
| `set_config_option` | `{ configId, value }` | `SetConfigOption(id, val)` |
| `update_session` | `SessionUpdateOptions` | `UpdateSession(ctx, opts)` |
| `close` | `null` | `Close(ctx)` |

Realtime commands (`realtime.start`, `realtime.update`, `realtime.stop`, `audio.commit`, `audio.clear`, `audio.interrupt`): see [realtime.md](realtime.md#commands-yao--tai).

## Control Stream Responses (Tai → Yao)

| Kind | Data |
|------|------|
| `disposition` | `{ disposition: "queued" \| "handled" }` |
| `request_permission` | `RequestPermissionRequest` (types defined, not yet wired) |
| `elicitation` | `CreateElicitationRequest` (types defined, not yet wired) |

## Ctrl Stream Events (Tai → Yao)

Server-initiated push events routed through the ctrl stream (reliable, unsequenced, no RecoveryBuffer).
Same frame format as data events (`SessionMessage { kind, data(JSON) }`).

| Category | Kinds |
|----------|-------|
| Realtime | See [realtime.md](realtime.md#ctrl-stream-events-tai--yao) |
| Render | `motion.started`, `motion.stopped` (ctrl stream); transcript/emotion via dedicated render uni stream — see [render.md](render.md) |
| Workspace | `workspace_change` |

### Workspace Change

```json
// workspace_change — debounced (5s), deduplicated, filtered through .wsignore
{ "files": ["src/main.go", "config.yaml"], "overflow": false }
```

`overflow: true` when more than 100 files changed; SDK should request a full diff.

## Data Stream Events (Tai → Yao)

ACP `UpdateType` values as `SessionMessage.kind`. Full JSON payloads and parsing guide: [events.md](events.md).

| Category | Kinds |
|----------|-------|
| Messages | `agent_message_chunk`, `agent_message`, `agent_thought_chunk`, `agent_thought`, `user_message_chunk`, `user_message` |
| Tools | `tool_call_update`, `tool_call_content_chunk` |
| State | `state_update`, `notice`, `usage_update`, `session_info_update`, `config_option_update`, `available_commands_update` |
| Terminal | `terminal_update`, `terminal_output_chunk` |
| Plan | `plan_update`, `plan_removed`, `compaction_update`, `compaction_summary_chunk` |
| Subagent | `subagent_update`, `session_message`, `session_message_chunk` |

Realtime does **not** add events to the data stream. Transcript, audio lifecycle, and emotion events flow through a dedicated render uni stream (`RecvRender()`). Audio and motion use their own channels. See [realtime.md](realtime.md) and [render.md](render.md).
