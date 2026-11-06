# Session Context

Context is layered instruction content: rule files written into `.context/`, read
back and merged into one Markdown document, then delivered to the session as
`metadata["_agents_md"]`.

## Directory layout

```
.context/
├── bot/<botID>/
│   ├── INSTRUCTION.md
│   ├── MEMORY.md
│   └── PREFERENCE.md
├── home/<userID>/
├── board/<boardID>/
└── session/<sessionID>/
```

`ContextDir(scope, id)` builds these paths:

```go
workspace.ContextDir(workspace.ScopeBoard, "board-42")
// ".context/board/board-42"
```

## The four scopes

| Scope | Constant | `<id>` is | Holds | Merge order |
|-------|----------|-----------|-------|-------------|
| `bot` | `ScopeBot` | bot ID | Runtime instance defaults | lowest |
| `home` | `ScopeHome` | user ID | User-level preferences | second |
| `board` | `ScopeBoard` | board ID | Board or project shared context | third |
| `session` | `ScopeSession` | session ID | Single session context | highest |

Merge order is the order you pass to `MergeContext`. Lowest priority goes first
so that later, narrower scopes appear further down the document.

## The three files

Every scope directory holds the same three filenames. Any other filename is
ignored by readers and rejected by `SeedContext`.

| File | Purpose | Lifetime |
|------|---------|----------|
| `INSTRUCTION.md` | Rules and instructions for this scope | while the scope exists |
| `MEMORY.md` | Accumulated project knowledge ("we hit this before") | across sessions |
| `PREFERENCE.md` | Output and interaction preferences | while the scope exists |

`MEMORY.md` is why `.context/` is committed: it is cross-session knowledge and
must be preserved in history. See [Session File Inputs](session-inputs.md#version-control).

## Writing defaults

`SeedContext` initializes a scope without clobbering anything a previous run
wrote. Files that already exist are left untouched, so calling it on every
startup is safe.

```go
err := workspace.SeedContext(ws, workspace.ScopeBot, "default", map[string][]byte{
    "INSTRUCTION.md": []byte("You are a helpful assistant."),
    "MEMORY.md":      []byte(""),
    "PREFERENCE.md":  []byte("Be concise and direct."),
})
```

The `files` map keys must be one of the three filenames. Unknown keys, unknown
scopes, and IDs containing a path separator or `..` are rejected before anything
is written.

## Reading one scope

```go
files, err := workspace.ReadContext(ws, workspace.ScopeBoard, "board-42")
if err != nil {
    return err
}
if files.Instruction != nil {
    fmt.Println(string(files.Instruction))
}
```

Missing files come back as `nil` fields rather than errors. `ReadContext` accepts
`fs.FS`, so any `WorkspaceFS` value works directly.

## Merging scopes

```go
merged, err := workspace.MergeContext(ws, []workspace.ContextEntry{
    {Scope: workspace.ScopeBot, ID: "default"},
    {Scope: workspace.ScopeHome, ID: "user-123"},
    {Scope: workspace.ScopeBoard, ID: "project-abc"},
    {Scope: workspace.ScopeSession, ID: "sess-xyz"},
})
```

Each non-empty scope contributes a section headed `## <scope>/<id>`, with one
`### INSTRUCTION.md`-style subsection per file present. Scopes with no files are
skipped. Every entry is validated before any read, so one bad entry fails the
whole merge rather than silently dropping that scope.

## Delivering to the session

Put the merged text in the session request metadata under `_agents_md` when
opening the session:

```go
sess, err := node.OpenSession(ctx, &types.SessionOpenRequest{
    SessionID: "sess-xyz",
    Token:     token,
    Request: &types.SessionRequest{
        Runtime: "tai",
        Metadata: map[string]string{
            "_agents_md": merged,
        },
    },
})
```

The session ID names the session on the node; `Request` carries what the runtime
needs. Keys starting with `_` are reserved by Tai. Use unprefixed keys for your
own metadata.

## Version control

`.context/` is part of workspace history: its contents appear in commits and
travel with branch merges. This is deliberate — `MEMORY.md` carries knowledge
that must survive across sessions.

## Verifying

Read a file back to confirm the layout:

```go
f, err := ws.Open(".context/bot/default/INSTRUCTION.md")
if err != nil {
    return err
}
defer f.Close()
```

## Common mistakes

- Misspelling a filename — `instruction.md` and `INSTRUCTION.MD` are both
  rejected. The names are exact.
- Reversing the entry order — passing session before bot inverts the intended
  priority.
- Writing files through `WorkspaceFS` instead of setting `Metadata["_agents_md"]` —
  the files are the storage, the metadata key is the delivery.
- Assuming a scope ID may contain a path separator — it is a single path segment.
