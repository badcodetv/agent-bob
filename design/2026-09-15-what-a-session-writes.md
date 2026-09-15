# What a session writes into its container (measured 2026-09-15)

Evidence for the decision to stop snapshotting session containers (Kai, 2026-09-15:
persistence should be explicit — artifacts — and images should come from recipes, not from
`docker commit` of a running session).

## Why we looked

Production (`bob.box.badcode.tv`) answered "Hello from the agentd mock model proxy" from
reopened sessions. Cause: `docker commit` copies the container's **env** into the snapshot
image's config — not the copy-on-write layer, the image config. Sessions archived in API-key
mode kept `ANTHROPIC_BASE_URL=…/agent-proxy` after the box moved to subscription mode, and in
that mode the proxy serves the mock. Fixed in `44fafee` (each mode blanks the other's
variables). The same mechanism puts `CLAUDE_CODE_OAUTH_TOKEN` into every snapshot taken in
subscription mode, and those snapshots are pushed to Artifact Registry.

## How

- Image: `session-core:8ff1caa` (what production runs), plain `docker run`, no stack.
- Model: a scripted mock (`modelproxy.ScriptedMockHandler`) driving one user turn → a
  `Write` tool call → a `Bash` tool call → a text reply.
- `docker diff` after boot (baseline), then after the turn; compared.

## Result

After boot, before any message: only `/tmp/tsx-0/*` (the sandbox's TypeScript loader cache).

After one turn, 13 new entries, none of them the user's work:

| Path | What |
| --- | --- |
| `/root/.claude.json` (40K) | CLI state: a per-install `userID`, cached feature flags and experiments, migration markers, seen notifications |
| `/root/.claude/backups/.claude.json.backup.<ts>` | a backup copy of the above |
| `/root/.claude/session-env/<uuid>/`, `sessions/`, `shell-snapshots/` | per-CLI-session scratch folders |
| `/tmp/claude-0/-workspace/<uuid>/` | per-CLI-session task-output folder |

- No credential was found in any of those files; in this run the secrets live only in the
  image config (the env), which is the part `44fafee` addressed.
- `/workspace` was unchanged. The `Write` tool call returned an error in this run, so the
  test did not show a user file landing. The shell command ran (it showed `python3` is not
  installed; `git` is).

## What it means

- A snapshot carries Claude's own bookkeeping by default. Every image saved from a session,
  and every session started from that image, inherits it, including the same CLI `userID`.
- This was one short scripted turn. A real session with a real model reads, edits, installs
  and caches much more; this is the lower bound.
- Nothing here needs a snapshot to survive a restore: the conversation is already rebuilt
  from the database (`rehydrateConversation` in `go/runner.go`).

## Not measured

- A real-model session (billable).
- Package installs or caches (`npm`, `pip`) and skills installing into the container.
- Why the scripted `Write` call errored.
