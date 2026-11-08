# Tai SDK Documentation

Tai SDK (`tai-golang`) is the Go SDK for building applications that connect to and manage Tai agent nodes.

## Contents

- [Quick Start](quickstart.md) — Install, run the demo, connect a node
- [Architecture](architecture.md) — Two-segment design, dual-stream session model
- [Remote Tools](remote-tools.md) — Tool registration, Tool H2, pipelines, Plugins, errors
- [API Reference](api-reference.md) — Server, Session, Types
- [Workspace API](workspace.md) — Remote workspace file operations, branching, history, watch
- [Session Protocol](session-protocol.md) — Handshake, ctrl/data frames, wire format
- [Session](session.md) — Building a session: open, prompt, steer, resolve, close
- [LLM Providers](llm-providers.md) — Session LLM matrix, endpoint fields, provider resolution, credentials
- [Session File Inputs](session-inputs.md) — The three fixed directories and what each delivers
- [Session Context](session-context.md) — Four scopes, the three context files, `_agents_md` delivery
- [Session Skills](session-skills.md) — Skill packages, manifest sync, key conventions
- [Session Attachments](session-attachments.md) — Attachment paths, `resource_link`, image blocks
- [Session Realtime](session-realtime.md) — Upgrading a session to voice, audio and render streams
- [ACP Events](events.md) — Event types, JSON payloads, parsing guide
- [Realtime Protocol](realtime.md) — Wire-level realtime kinds and payloads
- [Render Protocol](render.md) — Transcript, emotion, motion frame formats
- [Resource URI](uri.md) — Unified URI scheme, mount points, path resolution
- [Demo Guide](demo-guide.md) — Using the QA demo application
