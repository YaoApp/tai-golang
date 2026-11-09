# Remote Tools

Remote Tools let the SDK define callable tools for Tai nodes. The node does not
execute the handler; it executes the tool's optional local `pipeline` and sends
the `$request` step to the SDK over the Tool H2 channel.

## How it works

```text
Tai node                         Yao SDK
  tai run <tool>  ── Tool H2 ──▶  RemoteTool handler
  pipeline steps                   verifies user_token + scope
  (plugins / built-ins)            returns output or progress
```

- Tools, groups, input schemas, pipelines, and Plugin metadata travel from the
  SDK to the node on the Control channel.
- Plugin artifacts are downloaded by the node from
  `GET /v1/tai/tool/plugins/<sha256-hex>`.
- Tool calls use the SDK `transport_port`; there is no separate Tool port.
- The SDK registers the Tool H2 endpoints on the same HTTP/2 server as the
  transport:
  - `GET /v1/tai/tool/health`
  - `POST /v1/tai/tool/call`
  - `GET /v1/tai/tool/plugins/<hex>`

The node does not fall back to the Control channel for tool calls.

## Register a tool

```go
import (
    "context"
    "encoding/json"

    tai "github.com/yaoapp/tai-golang"
    "github.com/yaoapp/tai-golang/auth"
    "github.com/yaoapp/tai-golang/pb"
    "github.com/yaoapp/tai-golang/remotetool"
)

server, err := tai.NewServer(&tai.ServerConfig{
    TransportPort: 6099,
    HTTPPort:      5099,
    Auth:          auth.DefaultAuth(privateKey),
})
if err != nil {
    log.Fatal(err)
}

err = server.SetRemoteTools([]*remotetool.Tool{
    {
        Entry: &pb.RemoteToolEntry{
            Name:        "ping",
            Group:       "dev",
            Description: "Return the caller's identity and optional message.",
            InputSchema: []byte(`{
                "type": "object",
                "properties": {"message": {"type": "string"}}
            }`),
        },
        Handler: func(ctx context.Context, input []byte) ([]byte, error) {
            var req struct {
                Message string `json:"message"`
            }
            _ = json.Unmarshal(input, &req)
            user := remotetool.User(ctx)
            return json.Marshal(map[string]any{
                "message":  req.Message,
                "user_id":  user.UserID,
                "team_id":  user.TeamID,
                "tool_call_id": remotetool.ToolCallID(ctx),
            })
        },
    },
})
```

`RegisterRemoteTool` adds one tool; `UnregisterRemoteTool(name)` removes it;
`SetRemoteTools` replaces the full set atomically.

The node receives an initial sync after `register_ack`. Later changes emit
`remote_tool.changed`; the node asks for `remote_tool.list` and applies the new
manifest only after all required Plugin artifacts validate.

## Progress events

Use `ProgressHandler` when the node should display progress:

```go
ProgressHandler: func(ctx context.Context, input []byte, emit remotetool.Emitter) ([]byte, error) {
    emit(&pb.RemoteToolEvent{
        Kind: "tool_call_update",
        Data: []byte(`{"status":"running"}`),
    })
    // ... do work ...
    return result, nil
},
```

Events arrive as `progress` frames before the final `result` frame. The final
`result` is always last. Tool H2 health checks run every 15s with a 5s timeout;
three consecutive failures mark the channel degraded and reconnect.

## Plugins and pipelines

A tool can run local Wasm Plugin steps before and after `$request`:

```go
err := server.RegisterPlugin(&pb.PluginEntry{
    Name:         "demo.normalize",
    Version:      "1.0.0",
    Runtime:      "wasm",
    ArtifactPath: "dist/wasm/normalize.wasm",
})
if err != nil {
    log.Fatal(err)
}

err = server.SetRemoteTools([]*remotetool.Tool{
    {
        Entry: &pb.RemoteToolEntry{
            Name:  "demo.wasm_echo",
            Group: "demo",
            Pipeline: []*pb.Step{
                {Use: "plugin:demo.normalize@1.0.0"},
                {Use: "$request"},
            },
        },
        Handler: func(ctx context.Context, input []byte) ([]byte, error) {
            return input, nil
        },
    },
})
```

Plugins are registered with a local Wasm artifact path. The SDK computes and
publishes the digest and size; the node downloads the bytes from the Tool H2
artifact endpoint, opened and streamed on demand. `SetRemotePlugins` replaces the full set; `UnregisterPlugin` removes
one. A Plugin artifact is limited to 16 MiB.

Pipeline steps run in the node:

- `$request` invokes the SDK handler over Tool H2.
- `plugin:<name>@<version>` executes a Wasm Plugin step.
- Other names are local built-in steps.

## Node CLI

The node uses the manifest it received from Control:

```bash
# List cached tools
tai run --list

# Show one tool definition
tai run ping --describe

# Call a tool; the node resolves the endpoint and carries user_token
tai run ping --input '{"message":"hello"}'

# Print progress frames to stderr
tai run ping --input '{}' --progress

# Host-direct mode (no sandbox bridge)
TAI_TOOL_URL=https://yao.example.com:6099/v1/tai/tool \
TAI_NODE_TOKEN=<node-token> TAI_NODE_ID=<node-id> \
tai run ping --token <user-token>
```

## Errors

| Code | Meaning |
|------|---------|
| `invalid_input` | Malformed input or frame |
| `not_found` | Tool does not exist in the remote registry |
| `ambiguous_tool` | Short name exists in multiple groups; use `<group>.<name>` |
| `handler_error` | Handler returned an error |
| `tool_channel_unavailable` | Tool H2 cannot be reached / degraded |
| `tool_channel_busy` | Concurrent stream limit reached and wait timed out |
| `timeout` | Call exceeded `timeout_ms` |
| `unauthorized` | Missing/expired token or wrong node identity |
| `plugin_artifact_unavailable` | Plugin bytes missing or digest/size mismatch |
| `plugin_runtime_unsupported` | Plugin runtime is not supported |

The server verifies `Authorization: Bearer <node_token>` and
`X-Tai-Node-Id` on every Tool H2 request. The `call` request's first frame
carries `user_token`; handlers read the verified identity with
`remotetool.User(ctx)`.

## Server-side API summary

```go
SetRemoteTools(tools []*remotetool.Tool) error
RegisterRemoteTool(tool *remotetool.Tool) error
UnregisterRemoteTool(name string) error

RegisterPlugin(entry *pb.PluginEntry) error
UnregisterPlugin(name, version string) error
SetRemotePlugins(entries []*pb.PluginEntry) error
```

## Agent tools

Node sessions expose synced Remote Tools to the Text Agent. Provider-safe names
are sanitized (`demo.echo` becomes `demo_echo`) and mapped back to the real tool
name when the Agent calls it.

Two execution paths are supported:

- **Host/native session:** Agent → host tool bridge → Tool H2 with the
  session-scoped user token held by the node.
- **Container session:** Agent → container broker `/tmp/tai-remote-tool.sock` →
  the node fills the user token and forwards to Tool H2.

The Agent never receives the user token.

## Session isolation

The session `isolation` value is honored by the node:

| isolation | provider | Agent binary |
| --- | --- | --- |
| `container` | Docker using the node's sandbox image | `/usr/local/bin/tai` (`TAI_CONTAINER_BIN`) |
| `host` / `native` | platform-native sandbox | node executable |
| `auto` / empty | node default | by effective sandbox |

For a convenient two-node demo, start one node without an image (`sandbox:native`)
and one with `--sandbox-image` (`sandbox:docker`). Node capabilities expose which
mode each node provides.

## CLI and progress

`tai run` supports `--list`, `--groups`, `--describe`, `--group`, `--key`,
`-f`, `--session-id`, `--sandbox-id`, `--socat-addr`, `--timeout-ms` and `-v`.
Progress frames are streamed from Tool H2 through the host bridge/broker and
printed live by `-v`.

See [Remote Tool Plugins](remote-tool-plugins.md) for the plugin source
layout, stdin/stdout ABI, capabilities, build commands, and pipeline examples.

## Tunnel-like capabilities

The SDK does not expose a Tunnel API. Applications that need tunnel-style
exposure should implement it as a Remote Tool on the application side; the SDK
only provides tool registration, caller identity, and transport.
