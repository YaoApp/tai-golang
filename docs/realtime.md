# Realtime Protocol

Realtime enables voice interaction over a persistent session. Commands flow Yao → Tai on the ctrl stream; events flow Tai → Yao on the ctrl stream (lifecycle/control) or data stream (transcripts). A separate bidirectional audio stream carries PCM frames.

## Commands (Yao → Tai)

Sent as `SessionMessage { kind, data(JSON) }` on the ctrl stream.

| Kind | Data | Session Method |
|------|------|----------------|
| `realtime.start` | `RealtimeRequest` | `StartRealtime(req)` |
| `realtime.update` | `RealtimeUpdateOptions` | `UpdateRealtime(opts)` |
| `realtime.stop` | `null` | `StopRealtime()` |
| `audio.commit` | `null` | `AudioCommit()` |
| `audio.clear` | `null` | `AudioClear()` |
| `audio.interrupt` | `null` | `AudioInterrupt()` |

## Ctrl Stream Events (Tai → Yao)

Server-initiated push events routed through the ctrl stream (reliable, unsequenced, no RecoveryBuffer).

### Realtime Lifecycle

| Kind | Description |
|------|-------------|
| `realtime.started` | Realtime session started (capabilities + audio channel) |
| `realtime.updated` | Realtime config updated |
| `realtime.stopped` | Realtime session stopped |
| `realtime.error` | Realtime-specific error |

```json
// realtime.started — Realtime session started
{ "capabilities": { "audio": true, "motion": false }, "audio_channel_id": 1 }

// realtime.updated — config changed
{ "audio": { "voice": "alloy" } }

// realtime.stopped — (empty payload)

// realtime.error
{ "code": "provider_error", "message": "upstream timeout" }
```

### Audio Control

| Kind | ID Field | Description |
|------|----------|-------------|
| `audio.speech_started` | `item_id` | Model speech output started |
| `audio.speech_stopped` | `item_id` | Model speech output stopped |
| `audio.committed` | `item_id` | Audio input committed |
| `audio.interrupted` | `item_id` | Model audio output interrupted |

```json
// audio.speech_started / audio.speech_stopped / audio.committed
{ "item_id": "item_001" }

// audio.interrupted
{ "item_id": "item_001", "position_ms": 3200 }
```

### Task Lifecycle

| Kind | ID Field | Description |
|------|----------|-------------|
| `realtime.task_start` | `task_id` | Realtime task dispatched to agent |
| `realtime.task_end` | `task_id` | Realtime task completed |
| `realtime.task_cancel` | `task_id` | Realtime task cancelled |

```json
// realtime.task_start — task dispatched to agent
{ "task_id": "task_abc", "source": "realtime", "instruction": "查询数据库" }

// realtime.task_end — task completed
{ "task_id": "task_abc", "source": "realtime", "status": "complete" }

// realtime.task_cancel — task cancelled
{ "task_id": "task_def", "source": "realtime", "reason": "user_interrupt" }
```

## Data Stream Events (Tai → Yao)

Audio transcript events are delivered on the data stream (reliable, ordered).

| Kind | ID Field | Description |
|------|----------|-------------|
| `audio.transcript.delta` | — | Model speech transcript streaming fragment |
| `audio.transcript.done` | — | Model speech transcript complete |
| `audio.done` | `item_id` | Model audio output complete |
| `input_transcript.done` | — | User audio input transcript complete |

```json
// audio.transcript.delta — model speech transcript fragment
{ "content": "Hello" }

// audio.transcript.done — model speech transcript complete
{ "transcript": "Hello, how can I help?" }

// audio.done — model audio output complete
{ "item_id": "item_001", "duration_ms": 5200 }

// input_transcript.done — user audio input transcript complete
{ "transcript": "What's the weather today?" }
```

## Audio Stream

When Realtime is active, a separate bidirectional audio stream carries PCM audio frames.

| Step | Direction | Kind | Payload |
|------|-----------|------|---------|
| 1 | Yao → Tai | `audio` | `SessionData { session_id }` (opens audio bidi stream after `realtime.started`) |
| — | Yao → Tai | PCM | Raw PCM audio frames (user microphone input) |
| — | Tai → Yao | PCM | Model speech audio frames (TTS output) |
