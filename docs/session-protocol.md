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
| 2 | ctrl | Tai → Yao | `opened` | `SessionOpened { session_id, runtime, isolation }` |
| 2' | ctrl | Tai → Yao | `error` | `SessionError { session_id, code, message }` (on failure) |
| 3 | data | Yao → Tai | `data` | `SessionData { session_id }` (opens data stream) |
| — | data | Tai → Yao | events | ACP event frames flow back on the data stream |
| n | ctrl | Tai → Yao | `closed` | `SessionClosed { session_id, reason, exit_code }` |

After step 3, the data stream becomes **unidirectional Tai → Yao** for ACP events.

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

## Control Stream Responses (Tai → Yao)

| Kind | Data |
|------|------|
| `disposition` | `{ disposition: "queued" \| "handled" }` |
| `request_permission` | `RequestPermissionRequest` |
| `elicitation` | `CreateElicitationRequest` |

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
