# Remote Tool Plugins

A Remote Tool Plugin is a small `wasm32-wasip1` program that runs as a local
pipeline step for a Remote Tool inside a Tai node. It sees the current pipeline value, returns a new value, and
never talks to the SDK directly. The SDK registers the artifact; the node
downloads and executes it.

## Lifecycle

```text
SDK register artifact
  → Control remote_tool.sync (metadata only)
  → node downloads artifact over Tool H2
  → node validates size/digest
  → manifest applied
  → pipeline step `plugin:<name>@<version>` executes locally
```

Every step starts a fresh Wasm instance. Plugin state does not survive between
steps; persist state in the workspace if needed.

## Directory layout

A plugin source directory contains source code plus `plugin.json`:

```text
plugins/
  normalize/
    main.go
    plugin.json
```

`plugin.json` declares only build metadata. `artifact_path` is configured by the
application; `digest` and `size` are computed at registration time. Only
`plugin.json` is committed.

```json
{
  "name": "demo.normalize",
  "version": "1.0.0",
  "runtime": "wasm",
  "description": "Uppercase text",
  "capabilities": {
    "args": [],
    "env": [],
    "fs": [],
    "fetch": null
  }
}
```

Rules:

- `name` must match `^[a-z][a-z0-9_.-]{0,63}$`
- `version` must be SemVer, e.g. `1.0.0`
- `runtime` must be `wasm`
- `(name, version)` must be unique
- artifact must be at most 16 MiB

`demo.normalize` and `demo.tag` are separate plugins because one plugin is one
pipeline step. Split responsibilities instead of adding modes to one artifact.

## Wire contract

The node writes one JSON object to stdin:

```json
{
  "plugin": "demo.normalize@1.0.0",
  "flow": {
    "input": {},
    "last": {}
  },
  "input": {"text": "hello"},
  "args": {"mode": "upper"}
}
```

| Field | Meaning |
| --- | --- |
| `plugin` | `name@version` being executed |
| `flow` | Read-only snapshot of previous steps (`input`, `last`, aliases) |
| `input` | Current pipeline value for this step |
| `args` | `Step.Args` from the tool manifest |

The plugin writes one JSON object to stdout and exits `0`:

```json
{"value": {"text": "HELLO", "normalized": true}}
```

or on error:

```json
{"error": {"code": "step_failed", "message": "text is required"}}
```

- stdout must be exactly one JSON object
- stderr is captured as step logs; it is not parsed
- non-zero exit, invalid JSON, or output over the limit maps to `step_failed`
- a plugin must not print logs to stdout; use stderr

## Go example

```go
package main

import (
    "encoding/json"
    "os"
    "strings"
)

type request struct {
    Flow  map[string]any `json:"flow"`
    Input map[string]any `json:"input"`
    Args  map[string]any `json:"args"`
}

func main() {
    var req request
    if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
        _ = json.NewEncoder(os.Stdout).Encode(map[string]any{
            "error": map[string]string{"code": "step_failed", "message": err.Error()},
        })
        return
    }
    text, _ := req.Input["text"].(string)
    if text == "" {
        text, _ = req.Input["msg"].(string)
    }
    _ = json.NewEncoder(os.Stdout).Encode(map[string]any{
        "value": map[string]any{"text": strings.ToUpper(text), "normalized": true},
    })
}
```

Build:

```bash
GOOS=wasip1 GOARCH=wasm go build -o normalize.wasm .
```

Other supported toolchains:

```bash
cargo build --target wasm32-wasip1 --release
tinygo build -o normalize.wasm -target=wasi .
clang --target=wasm32-wasi -O2 -o normalize.wasm plugin.c
```

## Register with the SDK

Pass the compiled artifact **path**. `RegisterPlugin` opens the file once to
compute `digest` and `size`, then stores only the path. When the node requests
the artifact over Tool H2, the SDK opens the file again and streams it.

```go
err = server.RegisterPlugin(&pb.PluginEntry{
    Name:         "demo.normalize",
    Version:      "1.0.0",
    Runtime:      "wasm",
    Description:  "Uppercase text",
    ArtifactPath: "dist/wasm/normalize.wasm",
    Capabilities: &pb.Capabilities{
        Args: []string{},
        Env:  []string{},
        Fs:   []*pb.FSGrant{},
    },
})
if err != nil {
    log.Fatal(err)
}
```

Related APIs:

```go
RegisterPlugin(entry *pb.PluginEntry) error
UnregisterPlugin(name, version string) error
SetRemotePlugins(entries []*pb.PluginEntry) error
```

`SetRemotePlugins` replaces the whole set atomically. It rejects duplicate
`name@version`, non-Wasm runtime, missing/empty artifact files, and files over
16 MiB. `artifact_path` is SDK-local and is never sent in sync metadata.

If a plugin changes, register the new version, update the tool pipeline to
reference it, and let the node receive `remote_tool.changed`.

## Use in a pipeline

```json
{
  "name": "demo.wasm_echo",
  "group": "demo",
  "inputSchema": {
    "type": "object",
    "properties": {"text": {"type": "string"}}
  },
  "pipeline": [
    {"use": "plugin:demo.normalize@1.0.0"},
    {"use": "$request"},
    {"use": "plugin:demo.tag@1.0.0"}
  ]
}
```

Pipeline order is semantic:

- steps before `$request` prepare remote input
- `$request` calls the SDK handler
- steps after `$request` post-process the result

Use `as` to bind a step output under a name, and `args` to pass static arguments:

```json
{"use": "plugin:demo.normalize@1.0.0", "as": "normalized", "args": {"mode": "upper"}}
```

The built-in steps are separate: `path_normalize`, `image_args_coalesce`,
`image_to_workspace`, `image_output_args`, `image_resize`, `decision_files`,
`pdf_split`, `audio_split`.

## Capabilities

Capabilities are declared by the plugin and enforced by the node:

```json
{
  "capabilities": {
    "args": [],
    "env": ["HOME"],
    "fs": [{"mount": "workspace", "mode": "rw"}],
    "fetch": null
  }
}
```

| Capability | Behavior |
| --- | --- |
| `args` | static argv values; max 64 KiB |
| `env` | allowlist of environment variables; `TAI_*` cannot be requested |
| `fs` | preopens: `workspace` (ro/rw), `tmp` (rw), `plugin_dir` (ro) |
| `fetch` | HTTP(S) egress through host module `tai_fetch`; see below |

If a plugin imports a capability it did not declare, instantiation fails with
`wasm_import_not_allowed`, mapped to `plugin_capability_denied`.

### Fetch capability

`tai:fetch@1` uses the host module `tai_fetch`, function `fetch`. The node
validates the plugin's declared allowlist, methods, redirect policy, body limit,
timeout, and call count. Plugin code does not get network sockets directly.

```json
"fetch": {
  "allow": ["api.example.com"],
  "methods": ["GET"],
  "follow_redirects": false,
  "max_body_bytes": 1048576,
  "timeout_ms": 5000,
  "max_calls": 4
}
```

For the full ABI (memory allocation, request/response JSON, error codes), see
the Tai runtime design `docs/v2/runtime/wasm-plugin-runtime.md` §7.

## Errors

| Code | Cause |
| --- | --- |
| `plugin_artifact_unavailable` | artifact missing or download failed |
| `plugin_runtime_unsupported` | runtime is not `wasm` / not registered |
| `plugin_capability_denied` | undeclared or unavailable capability |
| `step_not_found` | `plugin:` reference not in manifest |
| `step_failed` | invalid stdin/stdout, non-zero exit, or declared error |

## Limits

- per artifact: 16 MiB
- total sync metadata: 64 MiB
- one Wasm instance per step, no shared state between steps
- no network access unless `fetch` is declared
- stdout is a single JSON object; put logs on stderr

## Testing

Unit-test the plugin as a normal Go program by feeding stdin and decoding
stdout. For node integration, build the Wasm artifact, register it, expose it in
a tool pipeline, and call the tool through `tai run` or an Agent session.

The demo fixtures under `app/plugins/demo/` are working examples:

- `normalize` — uppercase text
- `tag` — injects `source: wasm`
- `bad_fetch` — intentionally imports undeclared `tai_fetch`; E2E negative case

## Checklist

- [ ] `plugin.json` name/version are unique and SemVer
- [ ] plugin reads exactly the stdin contract
- [ ] plugin writes exactly one stdout JSON object
- [ ] logs only to stderr
- [ ] `artifact_path` exists and is at most 16 MiB
- [ ] requested capabilities are minimal
- [ ] pipeline references exact `name@version`
- [ ] integration test covers success and at least one error path
