# Render Protocol

The render channel carries all data needed to drive visual presentation during a Realtime session: avatar motion, transcript subtitles, and emotion state. It runs alongside the audio channel and is independent of the data stream (ACP events).

## Channel Overview

| Data Type | Transport | Reliability | Reason |
|-----------|-----------|-------------|--------|
| Motion | QUIC datagram | Unreliable, no retransmit | 95 bytes/frame, frame loss tolerable, low latency priority |
| Transcript + Audio.done + Input Transcript + Emotion | Dedicated render uni stream | Reliable, ordered | Text must not be lost, events must arrive in order; independent of ctrl stream |

## Enabling Render

The render stream (transcript + emotion) is **always available** when Realtime is active — no extra config needed. To also enable motion (avatar blendshapes via datagram), pass `Motion` config:

```go
session.StartRealtime(&tai.RealtimeRequest{
    Providers: providers,
    Audio: &tai.AudioConfig{
        OutputFormat: "pcm16",
        SampleRate:   24000,
    },
    Motion: &tai.MotionConfig{        // optional: enables motion datagram
        Enabled:  true,
        Provider: "fast",             // "fast" | "balanced"
        Profile:  "arkit_52",         // "arkit_52" | "live2d_16" | "viseme_15"
        FPS:      30,
    },
})
```

When `Motion.Enabled=true` but the server has no RenderProvider available, motion is silently disabled — transcript and emotion remain available via the render stream.

## Receiving Render Data

Two channels deliver render data:

```go
// Motion frames from QUIC datagram (30fps, 87 bytes each)
go func() {
    for frame := range session.RecvMotion() {
        applyBlendshapes(frame.Face, frame.Head, frame.Gaze)
        applyViseme(frame.Viseme)
        applyGesture(frame.GestureID, frame.GesturePhase)
    }
}()

// Render stream: transcript, audio lifecycle, emotion (reliable, ordered)
go func() {
    for evt := range session.RecvRender() {
        switch evt.Type {
        case "transcript.delta":
            appendSubtitle(evt.Content)
        case "transcript.done":
            finalizeSubtitle(evt.Transcript)
        case "audio.done":
            markAudioComplete(evt.ItemID, evt.DurationMs)
        case "input_transcript.done":
            showUserTranscript(evt.Transcript)
        case "emotion":
            updateMood(evt.State, evt.Emotion, evt.Intensity)
        }
    }
}()
```

Both channels are created when `realtime.started` is received and closed when `realtime.stopped` fires.

## MotionFrame (87 bytes)

Each frame carries a full snapshot of the avatar state:

| Field | Type | Range | Description |
|-------|------|-------|-------------|
| `Version` | uint8 | `2` | Wire format version |
| `Profile` | uint8 | 0–3 | Blendshape profile (see below) |
| `State` | uint8 | 0–3 | Session state: 0=idle, 1=listening, 2=thinking, 3=speaking |
| `Emotion` | uint8 | 0–4 | Emotion: 0=neutral, 1=happy, 2=surprised, 3=confused, 4=concerned |
| `Intensity` | uint8 | 0–255 | Emotion intensity |
| `FrameSeq` | uint16 | 0–65535 | Monotonically increasing, wraps around |
| `AudioSeq` | uint32 | | References the corresponding audio datagram seq |
| `PtsMs` | uint32 | | Millisecond timestamp relative to session start |
| `Face` | [52]uint8 | 0–255 each | Blendshape weights (ARKit 52 standard) |
| `Head` | [3]int8 | ±127 | Euler angles [yaw, pitch, roll], maps to ±30° |
| `Gaze` | [2]int8 | ±127 | Eye direction [x, y], 0,0 = looking at user |
| `Viseme` | [5]uint8 | 0–255 each | Vowel weights [aa, ih, ee, oh, ou] |
| `GestureID` | uint8 | 0–255 | Gesture type (0=none, 1=nod, 2=wave, 3=shake, 4=point) |
| `GesturePhase` | uint8 | 0–255 | Playback progress (0=inactive, 1–254=playing, 255=complete) |
| `Body` | [8]uint8 | 0–255 each | Body rotation; body[0]=breath (128 center ± 60 sine) |

### Wire Format

All multi-byte integers are **Big-Endian**.

```text
Offset  Size  Field
  0       1   version
  1       1   profile
  2       1   state
  3       1   emotion
  4       1   intensity
  5       2   frame_seq (BE)
  7       4   audio_seq (BE)
 11       4   pts_ms (BE)
 15      52   face[52]
 67       3   head[3]
 70       2   gaze[2]
 72       5   viseme[5]
 77       1   gesture_id
 78       1   gesture_phase
 79       8   body[8]
─────────────────────────
Total:   87 bytes
```

### Motion Datagram (95 bytes)

Over QUIC, motion frames are sent as datagrams with an 8-byte header:

```text
┌── Header (8 bytes) ──────────────────┐┌── Payload (87 bytes) ─┐
│ channel_id(4 BE) + seq(4 BE)         ││ MotionFrame           │
└──────────────────────────────────────┘└────────────────────────┘
```

The `channel_id` is assigned at `realtime.started` and delivered in the `motion_channel_id` field.

### Profile

| Value | Name | Valid Face Indices | Description |
|-------|------|--------------------|-------------|
| 0 | `arkit_52` | face[0:52] all | ARKit 52 blendshapes (standard) |
| 1 | `live2d_16` | face[0:16] | Live2D 16 parameters |
| 2 | `viseme_15` | face[0:15] | 15 standard visemes |
| 3 | `vendor` | declared in ctrl event | Custom blendshapes |

Wire format always transmits 52 bytes for `face[]`. Non-ARKit profiles use only the first N positions; the rest are zero.

### ARKit 52 Blendshape Index (profile=0)

| Index | Name | Region | Description |
|-------|------|--------|-------------|
| 0 | jawOpen | Mouth | Jaw opening |
| 1 | jawForward | Mouth | Jaw protrusion |
| 2 | jawLeft | Mouth | Jaw left shift |
| 3 | jawRight | Mouth | Jaw right shift |
| 4 | mouthClose | Mouth | Lip closure |
| 5 | mouthFunnel | Mouth | Lips rounded (as in "o") |
| 6 | mouthPucker | Mouth | Lips pursed (as in "u") |
| 7 | mouthLeft | Mouth | Mouth shifts left |
| 8 | mouthRight | Mouth | Mouth shifts right |
| 9 | mouthSmileLeft | Mouth/Expression | Left corner up (smile) |
| 10 | mouthSmileRight | Mouth/Expression | Right corner up |
| 11 | mouthFrownLeft | Expression | Left corner down (frown) |
| 12 | mouthFrownRight | Expression | Right corner down |
| 13 | mouthDimpleLeft | Expression | Left dimple |
| 14 | mouthDimpleRight | Expression | Right dimple |
| 15 | mouthStretchLeft | Mouth | Left corner stretch |
| 16 | mouthStretchRight | Mouth | Right corner stretch |
| 17 | mouthRollLower | Mouth | Lower lip roll inward |
| 18 | mouthRollUpper | Mouth | Upper lip roll inward |
| 19 | mouthShrugLower | Mouth | Lower lip shrug |
| 20 | mouthShrugUpper | Mouth | Upper lip shrug |
| 21 | mouthPressLeft | Mouth | Left lip press |
| 22 | mouthPressRight | Mouth | Right lip press |
| 23 | mouthLowerDownLeft | Mouth | Left lower lip pull down |
| 24 | mouthLowerDownRight | Mouth | Right lower lip pull down |
| 25 | mouthUpperUpLeft | Mouth | Left upper lip pull up |
| 26 | mouthUpperUpRight | Mouth | Right upper lip pull up |
| 27 | browDownLeft | Expression | Left brow down (frown) |
| 28 | browDownRight | Expression | Right brow down |
| 29 | browInnerUp | Expression | Inner brow raise (worry/surprise) |
| 30 | browOuterUpLeft | Expression | Left outer brow raise |
| 31 | browOuterUpRight | Expression | Right outer brow raise |
| 32 | eyeLookUpLeft | Gaze | Left eye look up |
| 33 | eyeLookUpRight | Gaze | Right eye look up |
| 34 | eyeLookDownLeft | Gaze | Left eye look down |
| 35 | eyeLookDownRight | Gaze | Right eye look down |
| 36 | eyeLookInLeft | Gaze | Left eye look inward (converge) |
| 37 | eyeLookInRight | Gaze | Right eye look inward |
| 38 | eyeLookOutLeft | Gaze | Left eye look outward |
| 39 | eyeLookOutRight | Gaze | Right eye look outward |
| 40 | eyeBlinkLeft | Eye | Left eye blink |
| 41 | eyeBlinkRight | Eye | Right eye blink |
| 42 | eyeSquintLeft | Expression | Left eye squint (smile) |
| 43 | eyeSquintRight | Expression | Right eye squint |
| 44 | eyeWideLeft | Expression | Left eye wide open (surprise) |
| 45 | eyeWideRight | Expression | Right eye wide open |
| 46 | cheekPuff | Expression | Cheek puff |
| 47 | cheekSquintLeft | Expression | Left cheek raise |
| 48 | cheekSquintRight | Expression | Right cheek raise |
| 49 | noseSneerLeft | Expression | Left nostril flare |
| 50 | noseSneerRight | Expression | Right nostril flare |
| 51 | tongueOut | Mouth | Tongue protrusion |

**Weight quantization**: 0 = inactive, 255 = fully activated. Linear mapping to floating point:

```go
floatWeight := float64(face[i]) / 255.0  // 0.0–1.0
```

### State

| Value | Name | Description | Typical Rendering |
|-------|------|-------------|-------------------|
| 0 | idle | Not in conversation | Subtle breathing animation, neutral expression, occasional blinks |
| 1 | listening | Hearing user speak | Slight nods, gazing at user, attentive expression |
| 2 | thinking | LLM inference in progress | Slight head tilt, gaze shift, may show thinking animation |
| 3 | speaking | TTS playback active | Lip-sync driven by audio, expressive face, head follows speech rhythm |

### Emotion

| Value | Name | Description | Associated Blendshape Tendency |
|-------|------|-------------|-------------------------------|
| 0 | neutral | Calm / no strong emotion | Face tends toward baseline |
| 1 | happy | Positive / joyful | mouthSmile up, cheekSquint up, eyeSquint up |
| 2 | surprised | Surprised / curious | browOuterUp up, eyeWide up, jawOpen up |
| 3 | confused | Confused / uncertain | browDown up (asymmetric), browInnerUp up, mouthFrown slight |
| 4 | concerned | Worried / caring | browInnerUp up, browDown up, mouthPress up |

The `Emotion` field is the RenderProvider's processed result. Clients can use it directly or ignore it in favor of the raw `Face[]` blendshape weights for more precise rendering.

### Intensity

| Range | Meaning |
|-------|---------|
| 0 | No emotion (equivalent to neutral) |
| 1–85 | Mild |
| 86–170 | Moderate |
| 171–255 | Intense |

The provider has already factored intensity into the `Face[]` output, but clients may use this field for additional scaling or UI indicators.

### Head

3 int8 values representing Euler angles:

| Index | Axis | Range | Mapping | Positive Direction |
|-------|------|-------|---------|--------------------|
| 0 | yaw | -127 to 127 | ±30° | Positive = turn right |
| 1 | pitch | -127 to 127 | ±30° | Positive = tilt up |
| 2 | roll | -127 to 127 | ±30° | Positive = lean right |

All zeros = facing forward. Head motion beyond ±30° is outside the protocol range.

### Gaze

2 int8 values representing eye direction relative to forward:

| Index | Axis | Range | Positive Direction |
|-------|------|-------|--------------------|
| 0 | x | -127 to 127 | Positive = look right |
| 1 | y | -127 to 127 | Positive = look up |

All zeros = looking straight at the user. Gaze is independent of head — gaze controls iris/eyeball direction, head controls skull rotation. Clients may map gaze to ARKit `eyeLookUp/Down/In/Out` blendshapes, or drive eyeball bone rotation directly.

### Viseme

5 uint8 values representing vowel articulation weights:

| Index | Name | IPA | Mouth Shape |
|-------|------|-----|-------------|
| 0 | aa | /ɑ/ | Wide open, "ah" |
| 1 | ih | /ɪ/ | Slightly open, corners pulled, "ih" |
| 2 | ee | /e/ | Corners stretched, "eh" |
| 3 | oh | /ɔ/ | Lips rounded open, "oh" |
| 4 | ou | /u/ | Lips pursed round, "oo" |

- Range: 0–255. 0 = not producing this vowel, 255 = full match.
- The five values do not necessarily sum to 255 (all may be zero during consonants).
- **Relationship with `Face[]`**: `Face[]` already contains complete mouth blendshape weights. Viseme is a supplementary vowel-level reference. Prefer `Face[]`; use viseme as fallback when `Face[]` mouth weights lack precision (e.g., with the fast preset).

### Gesture

#### gesture_id

| Value | Name | Description |
|-------|------|-------------|
| 0 | none | No gesture |
| 1 | nod | Head nod |
| 2 | wave | Hand wave |
| 3 | shake | Head shake |
| 4 | point | Pointing (directional) |
| 5–254 | reserved | Future expansion |
| 255 | vendor | Custom (meaning declared via ctrl event) |

#### gesture_phase

| Range | Meaning |
|-------|---------|
| 0 | Gesture not started or already ended |
| 1–254 | Playback progress (1=start, 254=near end) |
| 255 | Gesture complete frame |

When `gesture_id=0`, `gesture_phase` is meaningless. Clients play a predefined gesture animation based on `gesture_id` and use `gesture_phase` to control progress. Phase is not uniformly spaced — the provider determines the animation curve.

### Body

8 uint8 values for coarse torso and arm rotation:

| Index | Name | Description |
|-------|------|-------------|
| 0 | breath | Breathing. 128 = center, sine wave 68–188 |
| 1 | spineX | Spine left/right. 128 = center |
| 2 | spineY | Spine forward/back. 128 = center |
| 3 | spineZ | Spine rotation. 128 = center |
| 4 | leftArmX | Left arm. 128 = center |
| 5 | leftArmY | Left arm. 128 = center |
| 6 | rightArmX | Right arm. 128 = center |
| 7 | rightArmY | Right arm. 128 = center |

128 = midpoint (rest position). 0–127 = negative direction, 129–255 = positive direction.

The current version primarily uses `body[0]` (breathing). Indices 1–7 are reserved and set to 128. Clients should handle all-zero body values (legacy compatibility: treat as all-128).

### Angle Conversion

```go
degrees := float64(head[i]) / 127.0 * 30.0  // int8 → ±30°
weight  := float64(face[i]) / 255.0          // uint8 → 0.0–1.0
```

## Transcript Messages

Delivered on the render stream (reliable uni stream), JSON-encoded.

### transcript.delta

Streaming text fragment, arrives as the model speaks:

```json
{"type": "transcript.delta", "content": "Hello", "audio_seq": 42}
```

### transcript.done

Full transcript when the speaking turn completes:

```json
{"type": "transcript.done", "transcript": "Hello, how can I help?", "audio_seq": 58}
```

### audio.done

Sent when the model response item's audio output is fully generated. Carries the item ID and total duration, used by the recorder to write `model.reply` entries.

```json
{"type": "audio.done", "item_id": "item_001", "duration_ms": 5200, "audio_seq": 60}
```

| Field | Type | Description |
|-------|------|-------------|
| `item_id` | string | Response item identifier |
| `duration_ms` | int64 | Total audio duration in milliseconds |
| `audio_seq` | uint32 | Audio sequence counter at generation time |

### input_transcript.done

Sent when the user's audio input transcript is finalized (speech-to-text complete).

```json
{"type": "input_transcript.done", "transcript": "What's the weather today?", "audio_seq": 15}
```

| Field | Type | Description |
|-------|------|-------------|
| `transcript` | string | Finalized user speech transcript |
| `audio_seq` | uint32 | Audio sequence counter at recognition time |

> **Note:** All render stream events (`transcript.delta`, `transcript.done`, `audio.done`, `input_transcript.done`, `emotion`) arrive on `RecvRender()`, not on `RecvEvent()`. The data stream does not carry any realtime rendering events.

### Transcript Source

The transcript source is transparent to consumers — the message format is identical regardless of provider:

| LLM Provider | Transcript Support | Perceived Latency |
|---|---|---|
| OpenAI Realtime | Native | ~0ms (synchronized with audio) |
| Qwen Omni | Native | ~0ms |
| Providers without transcript | No | ~500ms (Tai internal ASR fallback) |

### Subtitle Rendering Recommendations

- Append `transcript.delta` content immediately as it arrives
- On `transcript.done`, apply final correction (the `transcript` field is the complete text, which may differ slightly from concatenated deltas)
- Use `audio_seq` to synchronize subtitle appearance with audio playback
- Transcript does **not** appear on the data stream; the data stream only carries ACP text events (`text_delta`, etc.)

## Emotion Messages

Delivered on the same render stream, JSON-encoded. Sent only on state changes (event-driven, not per-frame).

```json
{"type": "emotion", "state": "speaking", "emotion": "happy", "intensity": 200}
```

| Field | Type | Values |
|-------|------|--------|
| `state` | string | `"idle"`, `"listening"`, `"thinking"`, `"speaking"` |
| `emotion` | string | `"neutral"`, `"happy"`, `"surprised"`, `"confused"`, `"concerned"` |
| `intensity` | uint8 | 0–255 |

Emotion is also embedded in every MotionFrame (`State`, `Emotion`, `Intensity` fields at 30fps). When motion is active, the MotionFrame fields are the primary source of emotion data. Standalone emotion messages on the render stream are event-driven and designed for non-motion scenarios, but are not yet active in the current implementation.

## Ctrl Events

### motion.started

Sent when motion output begins:

```json
{
    "type": "motion.started",
    "profile": "arkit_52",
    "fps": 30,
    "motion_channel_id": 2,
    "motion_version": 2
}
```

The `blendshape_names` field is present only when `profile` is `"vendor"` (custom blendshape set).

On receipt:
1. Register a datagram handler filtering by `motion_channel_id`
2. Initialize blendshape mapping per the declared `profile`
3. Begin receiving and rendering motion frames

### motion.stopped

Sent when motion output ends:

```json
{
    "type": "motion.stopped",
    "final_audio_seq": 5200,
    "frames_sent": 312
}
```

On receipt:
1. Unregister the datagram handler
2. Fade the avatar to idle over 150ms

### realtime.started (render fields)

The `realtime.started` ctrl event includes render capability information:

```json
{
    "capabilities": {
        "audio": true,
        "motion": true,
        "transcript": true
    },
    "audio_channel_id": 1,
    "motion_channel_id": 2
}
```

- `motion=true` → a `motion.started` event will follow
- `transcript=true` → the render stream will emit transcript messages
- `motion=false` → no motion frames, but transcript + emotion are still available

## Lifecycle

```text
realtime.started (ctrl)
  │  motion=true → wait for motion.started
  │  transcript=true → open render stream receivers
  │
  ▼
motion.started (ctrl)
  │  register datagram handler
  │
  ▼ ─── active ───
  │  motion datagram (30fps)
  │  transcript.delta / transcript.done (render stream)
  │  emotion (render stream, event-driven)
  │
  ▼
motion.stopped (ctrl)        ← interruption or turn end
  │  unregister datagram handler
  │  render stream continues (transcript + emotion unaffected)
  │
  ▼
realtime.stopped (ctrl)      ← entire Realtime session ends
  │  all render channels closed
```

## Degradation

| Scenario | Motion | Transcript | Emotion |
|----------|--------|------------|---------|
| RenderProvider active | ✅ | ✅ | ✅ (embedded in MotionFrame per-frame) |
| No RenderProvider | ❌ | ✅ | ⚠️ Not yet enabled via render stream |
| Provider crashes at runtime | ❌ (sends motion.stopped) | ✅ | ⚠️ Same as above |

**Transcript is always available**, regardless of whether a RenderProvider is running. With motion active, emotion data is embedded in every MotionFrame (`State`, `Emotion`, `Intensity` fields at 30fps). Standalone emotion messages on the render stream for non-motion scenarios are planned but not yet active.

## Frame Handling

| Situation | Recommended Action |
|-----------|--------------------|
| `frame_seq` consecutive | Render normally |
| `frame_seq` gap (dropped frames) | Skip to the new frame; do not interpolate |
| No frames for > 500ms | Transition to idle (fade face to zero, center head/gaze) |
| `frame_seq` wraps (65535 → 0) | Treat as consecutive |

### Jitter Buffer

A 100–150ms jitter buffer is recommended:

- Sort incoming frames by `pts_ms`
- Buffer early frames until their playback time
- Drop frames arriving later than the buffer window
- Hold (repeat) the last frame when no new frame is available

## Render Stream Transport

Render stream messages are delivered via a dedicated render uni stream, independent of the ctrl and data streams. The SDK opens this stream after receiving `realtime.started`, and the server writes render frames to it:

```text
SDK → Tai: openStreamFn(kind="render", data=SessionData{session_id})
Tai → SDK: sendRenderPump → FrameCodec length-prefixed → Payload{ kind=type, data=JSON }
SDK:       readRenderStream → codec.ReadFrame → parse JSON → renderOut channel
```

Each message is distinguished by its `type` field (the frame `Kind` directly, no prefix):

| type | Message | Section |
|------|---------|---------|
| `"transcript.delta"` | Streaming transcript fragment | Transcript Messages |
| `"transcript.done"` | Transcript complete | Transcript Messages |
| `"audio.done"` | Model audio output complete | Transcript Messages |
| `"input_transcript.done"` | User speech transcript complete | Transcript Messages |
| `"emotion"` | Emotion state change | Emotion Messages |

The ctrl stream is reserved for low-frequency lifecycle events (`realtime.started/stopped`, `motion.started/stopped`, `audio.speech_*`), ensuring time-sensitive commands like `audio.interrupt` are never blocked by high-frequency transcript deltas.

## Audio–Motion Synchronization

Audio is the master clock. Motion synchronizes via two fields:

- `audio_seq` — coarse alignment: associates each motion frame with the audio datagram being spoken at that moment
- `pts_ms` — fine alignment: millisecond-precision timestamp for jitter buffer scheduling

### Interruption Handling

When the client receives an `audio.interrupted` ctrl event:

1. The server immediately stops emitting motion frames for the interrupted audio
2. The client should fade `Face[]` mouth blendshapes and `Viseme[]` to zero within **150ms**
3. `Head` and `Gaze` should return to neutral (all zeros) over the same 150ms transition
4. The avatar transitions to idle state until the next speech segment begins

## SDK Types

```go
// types/session.go

type MotionFrame struct {
    Version      uint8     // frame format version (2)
    Profile      uint8     // blendshape profile
    State        uint8     // 0=idle, 1=listening, 2=thinking, 3=speaking
    Emotion      uint8     // 0=neutral, 1=happy, 2=surprised, 3=confused, 4=concerned
    Intensity    uint8     // 0–255
    FrameSeq     uint16    // monotonically increasing
    AudioSeq     uint32    // reference to audio datagram seq
    PtsMs        uint32    // millisecond timestamp
    Face         [52]uint8 // blendshape weights
    Head         [3]int8   // [yaw, pitch, roll]
    Gaze         [2]int8   // [x, y]
    Viseme       [5]uint8  // [aa, ih, ee, oh, ou]
    GestureID    uint8     // gesture type
    GesturePhase uint8     // playback progress
    Body         [8]uint8  // body rotation
}

type RenderEvent struct {
    Type       string // "transcript.delta" | "transcript.done" | "audio.done" | "input_transcript.done" | "emotion"
    Content    string // transcript.delta: text fragment
    Transcript string // transcript.done / input_transcript.done: full text
    AudioSeq   uint32 // sequence counter
    ItemID     string // audio.done: response item ID
    DurationMs int64  // audio.done: audio duration in ms
    State      string // emotion: session state
    Emotion    string // emotion: emotion label
    Intensity  uint8  // emotion: 0–255
}

type MotionConfig struct {
    Enabled  bool
    Provider string            // "fast" | "balanced"
    Profile  string            // "arkit_52" | "live2d_16" | "viseme_15"
    FPS      int               // default 30
    Options  map[string]string // provider-specific parameters
}
```

## Session Methods

| Method | Returns | Description |
|--------|---------|-------------|
| `RecvMotion()` | `<-chan *MotionFrame` | Motion frames from QUIC datagram. `nil` when motion is disabled or before `StartRealtime`. Channel closed when realtime stops. |
| `RecvRender()` | `<-chan *RenderEvent` | Render stream events (transcript, audio lifecycle, emotion). `nil` before `StartRealtime`. Channel closed when realtime stops. |
