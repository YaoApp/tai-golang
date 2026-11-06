# Session Attachments

An attachment is a file in `.attachments/` in the current branch. Its path is
carried on the message; the bytes never travel inline unless you deliberately
base64-encode them into an `image` block.

**A path is only a reference.** The agent reads the file from the workspace, so
the file must exist there before the prompt arrives. Upload first, reference
second — an `Attachments` entry or `resource_link` URI pointing at a file that
was never uploaded resolves to nothing.

## Uploading with the Workspace API

Attachments use the same file operations as any other workspace file. Open the
workspace bound to the session's branch, then call `Create` with the target
path:

```go
// Bind the workspace to the session's branch so the upload lands where the
// agent will look for it.
ws, err := node.OpenWorkspace(ctx, workspaceID, sessionID)
if err != nil {
    return err
}
defer ws.Close()

path := fmt.Sprintf(".attachments/%d_%s", time.Now().UnixNano(), file.Name())

w, err := ws.Create(path)
if err != nil {
    return err
}
if _, err := w.Write(data); err != nil {
    w.Close()
    return err
}
// Close finalizes the upload and returns any server-side error.
if err := w.Close(); err != nil {
    return err
}
```

`Create` streams the bytes over the Light channel and the call to `Close`
half-closes the write side and reads the `create.result` response, so a failed
write surfaces at `Close` rather than being silently dropped.

For an `*os.File` source, `io.Copy` avoids holding the whole file in memory:

```go
w, err := ws.Create(path)
if err != nil {
    return err
}
if _, err := io.Copy(w, srcFile); err != nil {
    w.Close()
    return err
}
if err := w.Close(); err != nil {
    return err
}
```

The rest of this page wraps those steps in a helper:

```go
// upload writes data to path in the workspace and reports the server-side
// error from Close, which is where a rejected upload surfaces.
func upload(ws types.Workspace, path string, data []byte) error {
    w, err := ws.Create(path)
    if err != nil {
        return err
    }
    if _, err := w.Write(data); err != nil {
        w.Close()
        return err
    }
    return w.Close()
}
```

## Directory layout

```
<branch root>/
└── .attachments/
    ├── 1731049200_screenshot.png
    └── 1731049201_data.csv
```

Two levels, no session subdirectory. Session isolation already comes from the
branch: each session gets a branch named after its session ID, and a non-main
branch lives at `<workspace>/.tai-workspace/workdirs/<sessionID>/`. The
`.attachments/` inside it belongs to exactly one session.

A per-session subdirectory would therefore be a second layer of the same
isolation, so paths stay flat: `.attachments/<filename>`.

## Naming

The filename is yours to choose. The only requirement is uniqueness within the
directory, since two attachments with the same name overwrite each other.

A timestamp prefix is the simplest way to guarantee that:

```go
path := fmt.Sprintf(".attachments/%d_%s", time.Now().UnixNano(), file.Name())
```

Keeping the original name alone works until two uploads collide.

## Delivering as a path list

The simplest form: upload the file, then list its path on the message.

```go
// 1. Upload.
w, err := ws.Create(".attachments/1731049201_data.csv")
if err != nil {
    return err
}
if _, err := w.Write(csvData); err != nil {
    w.Close()
    return err
}
if err := w.Close(); err != nil {
    return err
}

// 2. Reference.
err = session.Prompt(&types.UserMessage{
    ID:      "msg-001",
    Message: "Analyze the attached CSV.",
    Attachments: []string{
        ".attachments/1731049201_data.csv",
    },
})
```

`Attachments` is a path list. Each entry is resolved against the branch working
directory.

## Delivering as a resource link

Use `resource_link` when the message should carry display metadata — a title, a
MIME type, a file size. The file must already be in the workspace; `URI` is a
reference, not a transport:

```go
// Upload first, exactly as above.
if err := upload(ws, ".attachments/1731049200_report.pdf", pdfData); err != nil {
    return err
}

err = session.Prompt(&types.UserMessage{
    ID: "msg-002",
    Content: []types.ContentBlock{
        {Type: "text", Text: "What changed in this report?"},
        {
            Type:     "resource_link",
            URI:      ".attachments/1731049200_report.pdf",
            Name:     "report.pdf",
            Title:    "Q4 Report",
            MimeType: "application/pdf",
        },
    },
})
```

`resource_link` requires `URI` and `Name`. `Title`, `Description`, `MimeType`,
`Icons` and `Size` are optional.

## Images with vision

For an image the model should actually see, base64-encode it into an `image`
block. The bytes travel inside the block, so no upload is required for delivery
— but uploading first is still worth doing when you want `URI` to resolve later
or when the same image is referenced again by path:

```go
data, err := os.ReadFile(imagePath)
if err != nil {
    return err
}

// Optional: keep the file reachable so URI resolves for later turns.
_ = upload(ws, ".attachments/1731049200_screenshot.png", data)

err = session.Prompt(&types.UserMessage{
    ID: "msg-003",
    Content: []types.ContentBlock{
        {Type: "text", Text: "Describe this screenshot."},
        {
            Type:     "image",
            Data:     base64.StdEncoding.EncodeToString(data),
            MimeType: "image/png",
            URI:      ".attachments/1731049200_screenshot.png",
        },
    },
})
```

Note that the `image` block carries the bytes, not a path — the file in
`.attachments/` is not needed for delivery once the block is built. Keep it
anyway so the reference in `URI` resolves.

## Size guidance

Inline base64 grows the payload by about a third. Beyond roughly 2 MB of raw
image data, prefer a path reference and let the agent read the file, or resize
before encoding.

## URI forms

Bare paths are relative to the branch root. A `workspace://<id>/<path>` URI
addresses another workspace. See [Resource URI Convention](uri.md) for the full
resolution chain and the reserved prefixes.

## Version control

`.attachments/` stays out of workspace history. Attachment bytes never appear in
commits and never merge between branches, which keeps session payloads out of
the main branch.

## Common mistakes

- Using `.attachment/` (singular). The directory is `.attachments/`; the singular
  form does not exist and an upload creates an unrelated directory.
- Passing an absolute sandbox path such as `/workspace/.attachments/x.png`. Paths
  are relative to the branch root; `/workspace` only exists inside the container.
- Referencing a path before uploading the bytes. A path is a pointer, not a
  payload — the agent finds nothing at a path that was never written.
- Forgetting to check the error from `w.Close()`. The upload is finalized there,
  so a failure at that point is the only signal that the server rejected it.
- Assuming a `<session-id>` subdirectory — there is none.
