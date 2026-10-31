# ACP Events Reference

SDK consumers receive events via `session.RecvEvent()`, which returns a `SessionMsg`. Use `Kind` to identify the event type and `Data` for the JSON payload.

```go
for {
    msg, err := session.RecvEvent()
    if err != nil {
        break
    }
    // msg.Kind  → event type (e.g. "agent_message_chunk")
    // msg.Data  → JSON payload (deserialize based on Kind)
}
```

## Event Summary

| Category | Kind | ID Field | Description |
|----------|------|----------|-------------|
| **Messages** | `agent_message_chunk` | `messageId` | LLM text streaming fragment |
| | `agent_message` | `messageId` | Complete/replace agent message |
| | `agent_thought_chunk` | `messageId` | LLM reasoning (thinking) streaming fragment |
| | `agent_thought` | `messageId` | Complete/replace reasoning message |
| | `user_message_chunk` | `messageId` | User message streaming fragment |
| | `user_message` | `messageId` | Echo user input |
| **Tools** | `tool_call_update` | `toolCallId` | Tool lifecycle (pending → in_progress → completed/failed) |
| | `tool_call_content_chunk` | `toolCallId` | Tool output streaming fragment |
| **State** | `state_update` | — | Activity state: `running` / `idle` / `requires_action` |
| **Notice** | `notice` | — | Transient notification (not persisted in history) |
| **Usage** | `usage_update` | — | Context window usage + cumulative cost |
| **Info** | `session_info_update` | — | Session metadata (e.g. title) |
| **Config** | `config_option_update` | — | Agent runtime config option change |
| **Commands** | `available_commands_update` | — | Available slash commands change |
| **Terminal** | `terminal_update` | `terminalId` | Agent-owned terminal |
| | `terminal_output_chunk` | `terminalId` | Terminal output streaming fragment |
| **Plan** | `plan_update` | `planId` | Execution plan |
| | `plan_removed` | `planId` | Plan deletion |
| **Compaction** | `compaction_update` | `compactionId` | Context compaction status |
| | `compaction_summary_chunk` | `compactionId` | Compaction summary streaming fragment |
| **Subagent** | `subagent_update` | `sessionId` | Declare/update child session |
| | `session_message` | `messageId` | Parent ↔ child explicit message |
| | `session_message_chunk` | `messageId` | session_message streaming fragment |

## Design Principles

The event system is based on [ACP (Agent Client Protocol) v2](https://agentclientprotocol.com):

1. **Entity model**: Messages, tool calls, terminals, and plans each have an ID. Addressed by `Kind + ID` with **upsert semantics**
2. **Chunk/Update dual mode**: `*_chunk` appends content; events without `_chunk` suffix replace/patch the entity
3. **Patch semantics**: In non-chunk events, omitted fields = no change, `null` = clear

## Message Events

Chunks with the same `messageId` append content; message events replace content.

```json
// agent_message_chunk — streaming text fragment
{
  "messageId": "msg_001",
  "content": { "type": "text", "text": "Hello" }
}

// agent_message — complete message (replace)
{
  "messageId": "msg_001",
  "content": [
    { "type": "text", "text": "Hello, how can I help?" }
  ]
}
```

`agent_thought_chunk` / `agent_thought` share the same structure, used for LLM reasoning (thinking) content.

### ContentBlock Types

| type | Required Fields | Description |
|------|-----------------|-------------|
| `text` | `text` | Plain text |
| `image` | `data`, `mimeType` | Base64-encoded image |
| `audio` | `data`, `mimeType` | Base64-encoded audio |
| `resource_link` | `uri`, `name` | Resource link |
| `resource` | `resource.uri` | Embedded resource |

## Tool Events

Tools progress through their lifecycle via the `status` field:

```text
tool_call_update  {status:"pending"}     → created
tool_call_update  {status:"in_progress"} → executing
tool_call_content_chunk                  → output fragment (may repeat)
tool_call_update  {status:"completed"}   → done
```

```json
// tool_call_update
{
  "toolCallId": "call_001",
  "name": "read_file",
  "kind": "read",
  "status": "in_progress",
  "locations": [{ "path": "/src/main.go" }]
}

// tool_call_content_chunk
{
  "toolCallId": "call_001",
  "content": { "type": "text", "text": "file content..." }
}
```

### ToolKind Values

| kind | Description |
|------|-------------|
| `read` | Read file/data |
| `edit` | Modify file |
| `delete` | Delete |
| `move` | Move/rename |
| `search` | Search |
| `execute` | Execute command |
| `think` | Internal reasoning |
| `fetch` | Fetch external data |
| `switch_mode` | Mode switch |
| `other` | Default |

### ToolCallStatus Values

`pending` → `in_progress` → `completed` / `failed` / `cancelled`

## State Events

`state_update` is one of the most important events, indicating the agent's foreground activity state.

```json
// Agent starts working
{ "state": "running" }

// Agent finished normally
{ "state": "idle", "stopReason": "end_turn" }

// Agent finished with error
{
  "state": "idle",
  "stopReason": "error",
  "error": { "code": -1, "message": "model timeout" }
}
```

### SessionState Values

| state | Description |
|-------|-------------|
| `running` | Foreground work in progress |
| `idle` | No foreground work |
| `requires_action` | Waiting for user action (permission/elicitation) |

### StopReason Values (only when `idle`)

| stopReason | Description |
|------------|-------------|
| `end_turn` | Normal completion |
| `max_tokens` | Token limit reached |
| `max_turn_requests` | Turn request limit reached |
| `refusal` | Agent refused to continue |
| `cancelled` | Cancelled by user |
| `error` | Execution failed (includes `error` field) |

## Notice Events

Transient notifications, not persisted in session history. Clients may ignore.

```json
{
  "severity": "info",
  "title": "Thinking..."
}
```

Severity values: `info` / `warning` / `error`

## Usage Events

```json
{
  "used": 8500,
  "size": 128000,
  "cost": { "amount": 0.035, "currency": "USD" }
}
```

## Session Info Events

```json
{ "title": "Fix login bug" }
```

Patch semantics: omitted = no change, `null` = clear.

## Terminal Events

```json
// terminal_update — terminal upsert
{
  "terminalId": "term_001",
  "command": "npm test",
  "cwd": "/workspace",
  "exitStatus": { "exitCode": 0 }
}

// terminal_output_chunk — output append
{
  "terminalId": "term_001",
  "data": "dGVzdCBwYXNzZWQ="
}
```

The `data` field is base64-encoded.

## Plan Events

```json
// plan_update
{
  "plan": {
    "type": "items",
    "planId": "plan_001",
    "entries": [
      { "content": "Read config", "priority": "high", "status": "completed" },
      { "content": "Fix handler", "priority": "high", "status": "in_progress" }
    ]
  }
}

// plan_removed
{ "planId": "plan_001" }
```

## Compaction Events

Context compaction progress notifications.

```json
// compaction_update
{ "compactionId": "c1", "status": "in_progress" }

// compaction_summary_chunk
{ "compactionId": "c1", "content": { "type": "text", "text": "Summary..." } }
```

Status values: `in_progress` / `completed` / `failed` / `cancelled`

## Subagent Events

Child agents have their own independent sessionId and full event stream.

```json
// subagent_update — declare child session
{
  "sessionId": "sess_child",
  "title": "Code review subtask",
  "capabilities": { "cancel": {} }
}

// session_message — parent ↔ child explicit message
{
  "messageId": "im_001",
  "senderSessionId": "sess_child",
  "recipientSessionId": "sess_parent",
  "content": [{ "type": "text", "text": "Review complete, found 2 issues" }]
}
```

After `subagent_update` is sent, all standard events (message, tool_call, thought, etc.) from the child session are automatically delivered with the child's `sessionId`.

## SDK Consumer Example

```go
for {
    msg, err := session.RecvEvent()
    if err != nil {
        break
    }
    switch msg.Kind {
    case "state_update":
        var s StateUpdateData
        json.Unmarshal(msg.Data, &s)
        fmt.Printf("State: %s\n", s.State)

    case "agent_message_chunk":
        var c MessageChunkData
        json.Unmarshal(msg.Data, &c)
        fmt.Print(c.Content.Text)

    case "tool_call_update":
        var t ToolCallUpdateData
        json.Unmarshal(msg.Data, &t)
        fmt.Printf("Tool %s: %s\n", t.Name, t.Status)

    case "notice":
        var n NoticeData
        json.Unmarshal(msg.Data, &n)
        fmt.Printf("[%s] %s\n", n.Severity, n.Title)
    }
}
```
