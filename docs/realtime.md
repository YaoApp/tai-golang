# Realtime Protocol

Realtime enables voice interaction over a persistent session. Commands flow Yao → Tai on the ctrl stream; lifecycle/control events flow Tai → Yao on the ctrl stream. A separate bidirectional audio stream carries PCM frames. A dedicated render uni stream carries transcript, audio lifecycle, and emotion events. An optional motion channel carries avatar motion via QUIC datagram — see [render.md](render.md) for the full render protocol.

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
| `realtime.started` | Realtime session started (capabilities + audio/motion channels) |
| `realtime.updated` | Realtime config updated |
| `realtime.stopped` | Realtime session stopped |
| `realtime.error` | Realtime-specific error |
| `motion.started` | Motion output started (profile, fps, channel_id) |
| `motion.stopped` | Motion output stopped (final_audio_seq, frames_sent) |

```json
// realtime.started — Realtime session started
{ "capabilities": { "audio": true, "motion": true, "transcript": true },
  "audio_channel_id": 1, "motion_channel_id": 2 }

// realtime.updated — config changed
{ "audio": { "voice": "alloy" } }

// realtime.stopped — (empty payload)

// realtime.error
{ "code": "provider_error", "message": "upstream timeout" }

// motion.started — avatar motion output started (blendshape_names present only for vendor profile)
{ "profile": "arkit_52", "fps": 30, "motion_channel_id": 2, "motion_version": 2 }

// motion.stopped — avatar motion output stopped
{ "final_audio_seq": 5200, "frames_sent": 312 }
```

For the full render protocol (motion frames, transcript, emotion, degradation), see [render.md](render.md).

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

## Audio Stream

When Realtime is active, a separate bidirectional audio stream carries PCM audio frames.

| Step | Direction | Kind | Payload |
|------|-----------|------|---------|
| 1 | Yao → Tai | `audio` | `SessionData { session_id }` (opens audio bidi stream after `realtime.started`) |
| — | Yao → Tai | PCM | Raw PCM audio frames (user microphone input) |
| — | Tai → Yao | PCM | Model speech audio frames (TTS output) |

## Render Channels

Render data flows through dedicated transports, independent of ctrl and data streams:

| Channel | Transport | Content | SDK Method | Availability |
|---------|-----------|---------|------------|-------------|
| Render | Dedicated uni stream (reliable, ordered) | `transcript.delta` / `transcript.done` / `audio.done` / `input_transcript.done` / `emotion` JSON | `RecvRender()` | Always (when Realtime is active) |
| Motion | QUIC datagram (95 bytes/frame) | MotionFrame at 30fps | `RecvMotion()` | Only when motion is enabled |

The render stream is always available when Realtime is active, even without motion. See [render.md](render.md) for the complete render protocol specification.
