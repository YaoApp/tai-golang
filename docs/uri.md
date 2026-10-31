# Resource URI Convention

Tai defines a unified URI scheme and reserved path convention that maps directly to the 4 sandbox mount points. All file references (ContentBlock, tool output, attachments) use this convention.

ACP's `resource_link.uri` field type is `format: "uri"` (RFC 3986), which does not restrict schemes — custom schemes are fully valid.

## 4 Mount Points

Each mount point has a **path form** and a **URI scheme form**. Both resolve to the same location:

| Mount Point | Path Form | URI Scheme | Description |
|-------------|-----------|------------|-------------|
| Workspace | `/path/to/file` (no reserved prefix) | `workspace://<ws-id>/path` | Project files |
| Temp | `/tmp/path/to/file` | `tmp://path/to/file` | Temporary files |
| Home | `/home/path/to/file` | `home://path/to/file` | User home directory |
| Shared | `/shared/<name>/path` | `shared://<name>/path` | Shared mounts |

URI scheme aliases (equivalent, normalized to canonical form on parse):

| Canonical | Alias |
|-----------|-------|
| `tmp://` | `tmpfs://` |
| `home://` | `homefs://` |
| `shared://` | `sharedfs://` |

## Reserved Prefix Rules

Bare paths (no scheme) are resolved to mount points by prefix:

- `/tmp/...` → Temp root
- `/home/...` → Home root
- `/shared/<name>/...` → Shared mount
- **All other paths** → Workspace root

```text
/src/main.go          → Workspace root / src/main.go
/tmp/cache/model.bin  → Temp root / cache/model.bin
/home/.bashrc         → Home root / .bashrc
/shared/lib/utils.go  → Shared["lib"] root / utils.go
/etc/passwd           → Workspace root / etc/passwd  ← cannot escape
```

Security: bare paths from agent output can only resolve to the 4 sandbox mount points. Paths without a reserved prefix always resolve to the Workspace root — host system files are inaccessible.

## workspace:// — Cross-Workspace References

`workspace://` is the only scheme requiring an authority (`<ws-id>`), used for cross-workspace references:

```text
workspace://<ws-id>/path/to/file
```

Files within the current workspace use bare paths (`/src/main.go`) — no `workspace://` needed.

### Relation to ACP `cwd` / `additionalDirectories`

ACP's `cwd` and `additionalDirectories` are `AbsolutePath` (absolute path strings), not URIs. `workspace://` is used in ContentBlock resource references, not in session lifecycle fields.

```text
session/new:
  cwd: "/data/workspaces/ws-456"                   ← AbsolutePath
  additionalDirectories: ["/data/shared/lib"]       ← AbsolutePath

session/prompt → ContentBlock:
  uri: "workspace://ws-456/src/main.go"             ← URI (cross-workspace)
  uri: "/src/main.go"                               ← bare path (current workspace)
```

## Attachments

Attachments are simply files under `/.attachment/` in the workspace:

```text
/.attachment/screenshot.png                     → current workspace attachment
/.attachment/data.csv                           → current workspace attachment
workspace://ws-456/.attachment/report.pdf       → cross-workspace attachment
/src/main.go                                    → regular source file
```

The SDK writes attachment files to `/.attachment/` in the workspace before sending a Prompt. Reference them using bare paths or `workspace://` URIs.

## Usage in ACP ContentBlock

```json
{ "type": "resource_link", "uri": "/src/main.go", "name": "main.go" }

{ "type": "resource_link", "uri": "/.attachment/data.csv", "name": "data.csv" }

{ "type": "resource_link", "uri": "workspace://ws-456/src/main.go", "name": "main.go" }

{ "type": "image", "data": "<base64>", "mimeType": "image/png",
  "uri": "workspace://ws-456/.attachment/screenshot.png" }

{ "type": "resource_link", "uri": "/tmp/build-output.log", "name": "build-output.log" }

{ "type": "resource_link", "uri": "tmp://build-output.log", "name": "build-output.log" }
```

For image attachments requiring vision, the SDK reads the file and converts it to an `image` ContentBlock (base64 data), preserving the `uri` field for provenance.

## Resolution Chain

```text
/.attachment/screenshot.png       (bare path, current workspace)
  ├─ no reserved prefix → Workspace root
  ├─ Runtime: WorkspacePath + "/.attachment/screenshot.png"
  └─ Sandbox: /workspace/.attachment/screenshot.png (Docker)
              <Branch.Path()>/.attachment/screenshot.png (Native)

workspace://ws-456/src/main.go    (cross-workspace URI)
  ├─ SDK: ws-id → lookup workspace path
  ├─ Runtime: WorkspaceManager.Get("ws-456") → Branch.Path()
  └─ Sandbox: accessed via Shared mount or API

/tmp/cache/model.bin              (reserved prefix)
  ├─ prefix /tmp → Temp root
  └─ Sandbox: /tmp/cache/model.bin (Docker)
              <DataRoot>/tmp/<wsID>/cache/model.bin (Native)
```

## Provider Path Mapping

Virtual paths are resolved to actual paths by the Sandbox provider. Docker container paths match virtual paths directly; Native provider requires mapping:

| Virtual Path | Docker Provider | Native Provider |
|-------------|-----------------|-----------------|
| `/` (workspace) | `/workspace/` | `MountDir.HostPath` (= `Branch.Path()`) |
| `/tmp/` | `/tmp/` | `<DataRoot>/tmp/<wsID>/` |
| `/home/` | `/home/sandbox/` | `<DataRoot>/homes/<userID>/` |
| `/shared/<name>/` | `/shared/<name>/` | `MountDir.HostPath` (per mount declaration) |

Resolution is handled by the Sandbox layer. The protocol and agent only see virtual paths, unaware of provider differences.
