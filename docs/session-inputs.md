# Session File Inputs

Context, Skills and Attachments reach an agent as files in the workspace. Each
lives in a fixed directory, is written by a specific SDK call, and is delivered
to the agent through a specific field. This page is the map; the three
following pages document each channel.

> **Scope.** These pages cover where files go, how they are named, and which
> field carries them. How a particular agent runtime discovers and consumes them
> is runtime-specific and out of scope. For general file operations on arbitrary
> paths, see [Workspace API](workspace.md).

## The three directories

| Channel | Directory | Written by | Delivered as |
|---------|-----------|------------|--------------|
| Context | `.context/<scope>/<id>/` | `SeedContext`, direct writes | `SessionRequest.Metadata["_agents_md"]` |
| Skills | `.skills/<key>/` | `SyncSkills` | on-disk; no extra field |
| Attachments | `.attachments/` | any workspace write | `UserMessage.Attachments` or `resource_link` |

## Directory naming rules

All three are dot-prefixed directories at the branch root:

| Directory | Number | Why |
|-----------|--------|-----|
| `.context/` | singular | one directory holding every scope |
| `.skills/` | plural | one directory holding every skill |
| `.attachments/` | plural | one directory holding every attachment |

Relative paths are resolved against the current branch working directory. A
path such as `.attachments/report.pdf` names a file in whichever branch the
session is using.

## Version control

`.context/` participates in workspace history. `.skills/` and `.attachments/`
do not.

The reason is lifetime: `MEMORY.md` inside a context scope holds knowledge that
must survive across sessions, so it belongs in commits and branch merges. Skills
and attachments are per-session material that the runtime writes into the branch
working directory as needed, so including them in history would leak session
state into the main branch.

## Session isolation

Each session gets its own branch, and the branch name is the session ID. A
non-main branch lives at `<workspace>/.tai-workspace/workdirs/<sessionID>/`, so
the three directories inside it already belong to exactly one session.

That is why `.attachments/` takes no session subdirectory: `<branch>/.attachments/`
is already session-scoped. See [Session Attachments](session-attachments.md) for
the full explanation.

## Next

- [Session Context](session-context.md) — four scopes and how they merge
- [Session Skills](session-skills.md) — skill packages and manifest sync
- [Session Attachments](session-attachments.md) — attachment paths and message fields
