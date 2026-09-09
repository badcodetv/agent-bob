# Git Projection — Design & Implementation Plan

> **EXECUTION RULES (for agents):** Work ONE ticket at a time, in order unless
> dependencies say otherwise. Only the orchestrator changes ticket Status;
> workers may only append to Notes and the Discovered Issues Log. A ticket's
> checkbox is checked only after its Validation commands have been re-run by
> the orchestrator and pass. Do not expand scope; log surprises in the
> Discovered Issues Log instead.

Status: **draft** (2026-09-09) — awaiting approval.
Revision: **rev1**.

Relates: `docs/product/17-product-spec.md` (§15 the config log, P8 "the log is
authoritative and the ordinary tables are projections of it"),
`docs/product/09-config-log.md`, `docs/product/03-memory.md` (§7.1 the `name=`
convention), `docs/20-datasets.md` (§9 the provenance trust rule, and
`dataset_put`'s `if_version` — the CAS precedent this plan copies),
`design/2026-09-08-memory-coordinated-organisation.md` (the architect loop that
every ordering guarantee below exists to serve; its **DI2** is a live trap for
the importer).

---

## Context

### The idea, and the idea that was rejected

The proposal on the table was an inversion: make a **git repository the
canonical source of truth** for a project, and demote the Postgres product
layer to a rebuildable search index that reacts to commits. Memories would
become named, mutable markdown files (`id` from the filename, frontmatter as
labels, body as text) and append-only immutability would be abandoned, on the
grounds that git's commit history supplies the history instead.

The motivation is good and survives intact: **a git repo is a canonical,
diffable, reviewable, cloneable record of how a project moved**, readable by
humans who will never open the console, and a natural bootstrap format.

An adversarial review on 2026-09-09 rejected the inversion. Three of its
findings are structural — not implementation problems, not fixable with a
better importer:

1. **Git has no total order.** `config_events.Seq` exists precisely because
   millisecond wall-clock plus a random uuid "is not tolerable for a log the
   spec calls authoritative" (`go/agentdb/config_events.go:93-107`), and the
   fold orders by it for the same reason (`go/agentdb/config_fold.go:17-24`).
   Git offers second-resolution, committer-settable dates on a DAG that
   `pull --rebase` rewrites. The architect's "what has changed since my last
   run", and narrow revert's refusal to revert anything but the newest change
   to an entity, both read an order git cannot promise.
2. **Provenance would become testimony.** `created_by_worker` /
   `created_by_session` are stamped server-side from the session token
   (`go/cmd/agentd/mcp_memory.go:336-337`) and `POST /agent/memories` stamps
   them **empty** and refuses a body that supplies them
   (`go/httpapi/memories.go:75-84`). That is the rule an embedding application
   holding authoritative state depends on (`docs/20-datasets.md` §9).
   Frontmatter and commit messages are written by whoever writes the file.
3. **The human-versus-agent collision has no safe resolution.** With
   `merge -X ours` a merged human PR is silently discarded; under `rebase`,
   git inverts the meaning of `ours` and the *architect's* write is discarded
   instead while the config log, the prompt-revision memory and the attention
   message all say it happened; plain merge freezes a project's configuration
   on a conflict a background loop cannot resolve.

Plus three that are severe but ordinary: secrets and exfiltration on a
`contents:write` push path, one `index.lock` serialising writes inside a
model's blocking tool call, and writer-chosen filenames silently overwriting
each other.

### What this plan builds instead

**The database keeps the writes. Git gets the publication.**

Every guarantee above is kept, and everything the proposal actually wanted —
diffs, blame, pull-request review of a prompt change, humans editing the org by
hand, bootstrap-from-a-folder, an off-site readable record — is delivered by
running the arrow the other way:

- **Outbound (render).** A renderer hangs off the existing post-commit config
  hook, writes markdown into a per-project local clone, commits with the
  **true** actor copied out of the config event, and a background loop pushes.
- **Inbound (import).** A human's commit is picked up by webhook or poll,
  diffed against the last-imported tree, and applied by calling **the same
  store methods the HTTP API calls**, with the commit message as the rationale.
  It becomes config event N+1.

Conflicts are impossible by construction: the database is the single
serialisation point, so two writers never edit the same thing at the same time.

The only thing given up is the sentence "git is the source of truth". Git is
the **published record**.

### What the substrate already gives us (verified — do not rebuild)

| Asset | Where | Why it matters here |
| --- | --- | --- |
| Per-project monotonic sequence, allocated inside the mutation transaction; **seq order is commit order** | `go/agentdb/config_events.go:93-107` | The renderer's commit order. Git history becomes a faithful linearisation for free. |
| Full **new state** in every payload, never a diff; deletes are tombstones | `go/agentdb/config_events.go` (`ConfigChange.Payload`), `config_fold.go:12-15` | The backfill can replay the whole history into commits without needing the tables. |
| `FoldConfig` — configuration at any instant T | `go/agentdb/config_fold.go` | Render is a pure function of a fold. Backfill = fold at each seq. |
| Post-commit hook, already carrying `config.changed` | `go/agentdb/config_events.go:327-338` | The renderer's trigger. Documented as: synchronous, must not error into the mutation, context already detached. |
| `WithConfigEvent` — one transaction for projection + log, with a write guard and `TestMutationsAreLogged` | `go/agentdb/config_events.go:356-420, 862-921` | The importer's front door. Nothing may bypass it. |
| Server-stamped provenance | `mcp_memory.go:336-337`, `httpapi/memories.go:75-84` | Rendered into commit trailers as *derived* fact, never asserted. |
| `agentd-data` PVC, `replicas: 1`, "scaling is a bigger node, not more replicas" | `deploy/k8s/20-agent-orange.yaml:28, 43-66, 73, 203` | The clone's home already exists. Single-writer posture is already the deployment's stated shape. |
| Project map **object form** naming per-project secrets by env var (`api_key_env`) | `go/cmd/agentd/googleauth.go:36-50` | `github_token_env` sits beside it. The map stays safe to commit; only variable names are written. |
| `dataset_put`'s `if_version` compare-and-swap | `go/agentdb/datasets.go` | The precedent for `memory_create`'s `if_current`. |
| Whole-value `${VAR}` env references, with partial interpolation refused | `go/cmd/agentd/attention.go:113-140` | The secret-safety rule the renderer enforces. |

### The gaps this plan closes

1. There is **no export or import of a project's configuration** anywhere in
   the codebase. Verified by search. Bootstrapping a project is entirely a
   console/API act.
2. A human cannot review a prompt change as a diff. The changelog shows
   payloads; nothing shows `-` and `+`.
3. `message-board`-style shared memories have **no concurrency control**: two
   workers rewriting "the current value of X" both succeed and the loser never
   learns it lost.
4. `attention_channel.url` accepts a **literal** webhook URL (`attention.go:99-103`)
   and a Slack/Discord incoming-webhook URL *is* a bearer token. Today that is
   contained by the database being private. Nothing may be rendered until it
   isn't.

---

## Architecture

### A. The one rule

> **The database is the only place where writes are put in order. Git is a door
> out (render) and a door in (import). Neither door writes anything the store
> did not serialise.**

Everything below follows from that sentence. When a question arises that this
document does not answer, answer it with that sentence.

### B. Outbound — the renderer

**Trigger.** `SetConfigEventHook` (already installed for `config.changed`;
becomes a small fan-out to both). The hook is synchronous and must not error
into the mutation, so it does exactly one thing: **enqueue the project** on a
per-project dirty set. All git work happens on the projection worker.

**Render is a pure function.** `gitproj.Render(state) → map[path][]byte`, where
`state` is the project's configuration as the store holds it (workers, skills,
images, subscriptions, schedules, settings, project prompt) plus the named-
document memories (§E). No git, no IO, no clock. This is what makes everything
else testable and what makes the loop terminate.

**Layout**, under a per-project configurable subfolder (default `orange/`):

```
orange/
  README.md                 generated; says "this folder is written by Agent
                            Orange; edit it and the change is applied"
  settings.md               project prompt as body; renderable settings as frontmatter
  workers/<name>.md         system prompt as body; the rest as frontmatter
  skills/<name>.md          skill body; name, labels, revision as frontmatter
  subscriptions/<id>.md     frontmatter only
  schedules/<id>.md         frontmatter only
  images/<name>.md          frontmatter only (the blob is not rendered)
  memory/<name>.md          named-document memories only — see §E
```

**Commit.** One commit per config event, in `seq` order, with the actor and
reason taken from the event, never from anything a model wrote:

```
worker_prompt_write: copywriter

<rationale, verbatim>

Orange-Project: wolf
Orange-Seq: 1247
Orange-Event: 7f3c…
Orange-Action: worker_prompt_write
Orange-Actor-Worker: architect
Orange-Actor-Session: sess-…
```

Commit author is a fixed identity (`Agent Orange <orange@…>`), because the
author field is the part humans trust by habit and it must not appear to
attribute the change to a person. **Who** is in the trailers, derived.

**Backfill.** Because every payload is the full new state and `FoldConfig`
already replays them, a project with existing history gets a **complete** git
history: fold from seq 1, emit one commit per event, in seq order. The result
is a repo whose `git log` is the config log, exactly. This is a one-shot job,
not a live path.

**Push.** A background loop, per project: fetch, and if the remote's branch tip
is an ancestor of ours, push. Never rebases our commits, never force-pushes,
and **never holds the lock the render path uses** (§F).

**Repair.** If the remote is lost, wrecked, or force-pushed by someone: delete
the clone and re-render from the database. The database can rebuild git. Git
can never rebuild the database. That asymmetry is the design.

### C. Inbound — the importer

**Trigger.** A GitHub webhook on push, with a slow poll as the fallback —
webhook delivery is best-effort, pushes coalesce many commits, and order is not
guaranteed, so the webhook is only a hint that says "look now".

**Diff, not replay.** The importer holds a watermark: the SHA it last imported.
It diffs `watermark → remote HEAD` as a **tree**, giving a set of changed paths.
It never walks individual commits and never reads git history semantically —
that would reintroduce kill #1.

**Skip our own output.** Commits carrying an `Orange-Seq:` trailer are ours.
They are skipped for rationale purposes, and the watermark advances past them.

**Apply through the front door.** Each changed path maps to exactly one
existing store method, called with `ConfigWrite{Worker: "", Session: "",
Rationale: <commit subject + body>}` — empty actor, which is already the
documented encoding for a human/UI/API edit
(`go/agentdb/config_events.go:234-241`):

| Path | Method |
| --- | --- |
| `workers/<n>.md`, body changed only | `SetWorkerPrompt` |
| `workers/<n>.md`, other fields changed | `UpsertWorker` — **field-merged, see below** |
| `workers/<n>.md` deleted | `DeleteWorker` |
| `settings.md`, body changed only | `SetProjectPrompt` |
| `settings.md`, other fields | `PutProjectSettings` |
| `skills/<n>.md` | `CreateSkill` (a new revision — skills are append-only) |
| `subscriptions/*`, `schedules/*` | `Create…` / `Update…` / `Delete…` |
| `memory/<n>.md` | `CreateMemory` (a NEW memory carrying `name=<n>`) |

🔴 **DI2 is a live trap.** `PUT /agent/workers/{name}` currently wipes any
omitted field (`design/2026-09-08-…` DI2, ticket T27). An importer that hands
`UpsertWorker` a struct built from frontmatter alone will silently strip
briefings on every human commit. The importer therefore **reads the current
row, applies only the fields the file actually changed, and writes that** —
and G8 has a test that proves it.

**Nothing is ever partially applied.** Every changed path is parsed and
validated first. If any file fails, the whole push is rejected into
**quarantine**: nothing is written, a `project.event` is raised, and the
console shows what failed and why. A malformed file must not be able to take
down a project's configuration.

**The loop terminates.** Import → store write → hook → render → commit. Because
render is a pure function of store state, the re-render either equals the
imported tree (no commit — the common case) or differs by normalisation
(exactly one commit, after which it equals). It cannot oscillate.

### D. Secrets — an allowlist, not a denylist

🔴 **Nothing renders until this ticket lands.** The rule:

- A field renders only if it is on an explicit allowlist with a decision
  recorded against it.
- A field that can hold a credential renders **only** as a whole-value
  `${VAR}` reference. A literal in such a field is a **render refusal** — the
  file is not written and the project is flagged — not a redaction, because a
  redaction teaches an operator that the value was published safely.
- A **guard test** in the style of `TestMutationsAreLogged`: enumerate the
  fields of `ProjectSettings`, `Worker` and `Skill` by reflection and fail the
  build when a field appears that no allowlist entry names. A new field must be
  a decision, not an accident.

`attention_channel.url` is the known blocker: it accepts a literal http(s) URL
today and Slack/Discord webhook URLs are bearer tokens. It needs the `${VAR}`
treatment its own headers already have (`attention.go:113-140`) before
`settings.md` may render at all.

**Path safety is a security invariant, not tidiness.** Every rendered path and
every imported path is validated against `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` per
segment, with a fixed prefix and a fixed depth. A name that escapes the
subfolder could write `.github/workflows/*.yml` into the project's own repo,
where it would execute with that repo's secrets on the next push.

**Git configuration is not importable.** The fields that say *which repo* and
*which token* (`git_remote`, `git_branch`, `git_subfolder`, `git_token_env`)
render as informational and are **ignored by the importer**. Otherwise anyone
with commit access could redirect a project's projection.

### E. Memory — what renders, and compare-and-swap

**Named documents render. The log does not.**

- A memory carrying a `name=` label is the substrate's existing "current value
  of X" convention (`docs/product/03-memory.md` §7.1, `NewestMemory`,
  `memory_current`). The **newest** such memory renders to
  `orange/memory/<name>.md`. That is the message-board, the label registry, the
  goal — the things a human wants to read and diff.
- Every other memory — summaries, lessons, verdicts, retractions,
  prompt-revisions — does **not** render in v1. They are written once and never
  edited, so a diff shows nothing, and they are the high-volume path where
  git's costs land. Revisit only for disaster recovery, and say so then.

**Confirmed 2026-09-09, do not re-open.** The alternative — render every memory
so the repo is a complete importable snapshot — was put and rejected. The
accepted consequence: a project bootstrapped from a folder (G15) arrives with
its whole configuration and its named documents, and with **no memory log**. It
starts with a brain-state, not a past. That is the intended trade: the repo is a
current-state export, not an archive, and the archive stays in the database
where it is ordered, stamped and searchable.

**Immutability stays.** A human editing `orange/memory/message-board.md`
produces a **new** memory carrying the same `name=`, with the commit message as
context — the same thing an agent does. Nothing is mutated, history stays
*searchable* (git history is not), and every version keeps its stamp.

**`memory_create` gains `if_current`.** The gap the message-board actually has
is not mutability, it is that two workers rewriting the current value both
succeed and neither learns it lost. New optional argument:

```
memory_create(content, labels, embed, if_current: "<memory id>")
```

"Append this only if the newest memory carrying my `name=` label is still that
id." On a mismatch: refuse, and return the id that won, so the caller re-reads
and retries. Same intent as `dataset_put`'s `if_version`; it is not a new atom.

**[corrected by G13]** This section previously said "it is a `WHERE` clause on
the insert". That is **wrong and was proven wrong**. `dataset_put` gets its
backstop from a unique index on `(project, name, version)`. Memories have no
such index — the name is a jsonb label and "newest" is an `ORDER BY` — so a
conditional insert alone still admits two winners under concurrency. The
implementation takes a `pg_advisory_xact_lock` keyed on project+name for the
transaction. **The lock is load-bearing, not belt-and-braces.** G13 proved it by
falsification: remove the lock and the 25-way concurrency test reports "2 writers
won" every time.

### F. Ordering, identity and the single writer

- **`Seq` is the authority.** Git commit order is derived from it. Nothing in
  the system ever asks git what order things happened in.
- **One writer per clone.** A per-project mutex around all working-tree work.
  The push loop takes it only for fetch/ff-check; the actual `push` is network
  work done outside it. No git operation is ever performed inside a model's
  tool call — the hook only enqueues.
- **The watermark is durable.** Last-rendered seq and last-imported SHA live in
  the database, not in the working tree, so a lost disk costs a re-render and
  not a divergence.
- **Boot reconciliation.** On start: if the clone is missing, clone; if
  last-rendered seq is behind the log, render forward; if HEAD is ahead of the
  last-imported SHA, import. A crash between "committed locally" and "wrote the
  watermark" is repaired by this, which is why the render must be idempotent.
- **Two agentd replicas would be two writers.** The deployment already says
  `replicas: 1` and "scaling is a bigger node". This plan does not change that,
  and G3 refuses to start a second projection worker for the same project via a
  store lease (`go/agentdb/leases.go` already exists).

### G. Out of scope, stated so nobody builds it

- Git as a write path. Ever. See kills 1–3.
- Rendering datasets, artifacts or snapshots. They are versioned blobs with
  their own reaper and their own byte paths (`docs/20-datasets.md`); git would
  be a third storage tier.
- Rendering the raw memory log (see §E).
- Any remote other than GitHub in v1. The driver is behind an interface; a
  second host is a later ticket, not a later refactor.
- Git-LFS.
- Reading git history to answer product questions.
- Restoring a project by checking out an old commit. Restore stays what §15.7
  already says it is: a forward compensating write.

---

## File Structure

### Create

| Path | What |
| --- | --- |
| `go/gitproj/render.go` | Pure: configuration + named memories → `map[path][]byte`. No git, no IO. |
| `go/gitproj/frontmatter.go` | The one YAML-frontmatter reader/writer. Deterministic key order. |
| `go/gitproj/allowlist.go` | Field allowlist + the `${VAR}`-only rule for credential-bearing fields. |
| `go/gitproj/allowlist_guard_test.go` | Reflection guard: a new struct field fails the build until a decision is recorded. |
| `go/gitproj/parse.go` | Inverse of render: file → a typed, validated change. |
| `go/gitproj/paths.go` | Path validation as a security invariant. |
| `go/gitproj/repo.go` | The git driver (exec `git`), per-project mutex, commit with trailers. |
| `go/cmd/agentd/gitprojection.go` | The worker: hook fan-out, dirty set, render loop, push loop, boot reconciliation. |
| `go/cmd/agentd/gitimport.go` | Webhook route, poll fallback, tree diff, apply-through-store, quarantine. |
| `docs/21-git-projection.md` | Operator's guide: setup, the two doors, what renders, secrets, hazards. |
| `e2e/features/git-projection.stack.spec.ts` | End-to-end against the stack, with a local bare repo as the remote. |

### Modify

| Path | Change |
| --- | --- |
| `deploy/agentd.Dockerfile` | `apk add --no-cache git` (the image is alpine and has none). |
| `go/agentdb/project_settings.go` | `git_remote`, `git_branch`, `git_subfolder`, `git_token_env`; not importable. |
| `go/agentdb/memories.go` | `if_current` compare-and-swap on `CreateMemory`. |
| `go/cmd/agentd/mcp_memory.go` | `if_current` argument, and the "you lost, here is the winner" error. |
| `go/cmd/agentd/attention.go` | `${VAR}` support for `url`, matching `resolveHeaders`. |
| `go/cmd/agentd/googleauth.go` | `github_token_env` in `projectConfig`, validated at boot like `api_key_env`. |
| `go/cmd/agentd/main.go` | Install the projection worker; fan the config hook out to two consumers. |
| `deploy/k8s/20-agent-orange.yaml` | Mount the clone root on the existing `agentd-data` PVC. |
| `web/src/…` | Repo link, last-push status, quarantine list on the project settings page. |
| `CLAUDE.md`, `docs/18-workers-memory-events.md` | Point at `docs/21`. |

---

## Interfaces

```go
// go/gitproj — leaf package. No agentd imports, no HTTP, no git in Render.

type ProjectState struct {
    Settings      *agentdb.ProjectSettings
    Workers       []*agentdb.Worker
    Skills        []*agentdb.Skill
    Subscriptions []*agentdb.Subscription
    Schedules     []*agentdb.Schedule
    Images        []*agentdb.CustomImage
    Documents     []*agentdb.Memory // newest per name= only
}

// Render is a pure function. Same state ⇒ byte-identical tree, always.
// Returns ErrUnrenderable naming the field when a credential-bearing field
// holds a literal (§D) — the tree is NOT partially returned.
// [corrected by G4] Shipped as RenderTree — `Render` was already a Decision
// const in allowlist.go.
func RenderTree(st ProjectState, subfolder string) (map[string][]byte, error)

// Parse is the inverse door. It returns a typed change, or an error naming the
// file and the line. It never touches a store.
func Parse(path string, content []byte) (Change, error)

type Change struct {
    Kind      Kind   // worker | skill | settings | subscription | schedule | document
    Name      string
    BodyOnly  bool   // only the markdown body differs ⇒ the prompt-write path
    Fields    map[string]any // ONLY the fields this file changed (DI2)
    Deleted   bool
}

// Commit metadata, derived from the config event — never from model text.
type Trailers struct{ Project, Event, Action, ActorWorker, ActorSession string; Seq int64 }
```

```go
// go/agentdb — one new optional argument, one new error.

// [corrected by G13] The variadic-option form sketched here is NOT viable: four
// interfaces (cmd/agentd memoryStore + managementStore, httpapi memoryStore, and
// the fakes) declare CreateMemory's exact non-variadic signature, and a variadic
// method does not satisfy them. It shipped as a second method instead, leaving
// CreateMemory and all its callers untouched.
func (s *Store) CreateMemoryIfCurrent(ctx context.Context, m *Memory, embedding []float32, ifCurrent string) (*Memory, bool, error)

type ErrMemoryNotCurrent struct{ Name, IfCurrent, Current string }
```

---

## Tickets

Model column is a suggestion for the executing agent, not a rule.

### G1: `gitproj` leaf package — frontmatter + paths   [Status: done | Model: sonnet]
Deterministic frontmatter writer/reader and the path validator. No git.
**Validation:** `cd go && go test ./gitproj/...` — round-trip table tests, and
path tests that reject `../`, absolute paths, uppercase, empty segments, and
anything resolving outside the subfolder.

### G2: the secret allowlist and its guard test   [Status: done | Model: opus]
`allowlist.go` + the reflection guard. Depends on G1.
**Validation:** `go test ./gitproj/...`; add a throwaway field to
`ProjectSettings` and confirm the guard test **fails**; remove it.

### G3: `${VAR}` for `attention_channel.url`   [Status: done | Model: sonnet]
Mirror `resolveHeaders`. A literal URL keeps working (no migration) but is
marked unrenderable by G2's allowlist.
**Validation:** `go test ./cmd/agentd/ -run Attention`.

### G4: `Render`   [Status: done | Model: opus]
The pure function, all entity kinds. Depends on G1, G2.
**Validation:** `go test ./gitproj/...` — golden-file tests; a determinism test
that renders the same state 100× and asserts byte equality.

### G5: `Parse` and the DI2 field-merge rule   [Status: done | Model: opus]
Inverse of G4. `Change.Fields` carries **only** what the file changed.
**Validation:** `go test ./gitproj/...` — a test that a worker file edited to
change only `enabled` produces a Change that does not mention `briefing`.

### G6: the git driver   [Status: done | Model: opus]
`repo.go`: clone, commit with trailers, fetch, fast-forward-only push, mutex.
Plus `apk add git` in `deploy/agentd.Dockerfile`.
**Validation:** `go test ./gitproj/ -run Repo` against a local bare repo in
`t.TempDir()`. No network.

### G7: project settings fields + `github_token_env`   [Status: done | Model: sonnet]
Migration for the four settings fields; `projectConfig.GitHubTokenEnv` validated
at boot exactly like `api_key_env`. The four fields are marked not-importable.
**Validation:** `go test ./agentdb/... ./cmd/agentd/ -run 'Settings|ProjectMap'`.

### G8: the projection worker   [Status: pending | Model: opus]
Hook fan-out (never error into the mutation), per-project dirty set, render+commit
in seq order, durable last-rendered-seq watermark, boot reconciliation, lease.
**Validation:** `go test ./cmd/agentd/ -run GitProjection`; a test that a config
mutation produces exactly one commit whose trailers match the config event.

### G9: the push loop   [Status: pending | Model: sonnet]
Background, fast-forward-only, never force, never inside the render mutex.
Backs off and surfaces failure without blocking writes.
**Validation:** `go test ./cmd/agentd/ -run GitPush`, including a remote that is
unreachable and a remote that has diverged.

### G10: backfill from the fold   [Status: pending | Model: opus]
One-shot: replay `config_events` from seq 1 via `FoldConfig`, one commit each.
**Validation:** a test that a project with N config events backfills to N
commits whose subjects and trailers match the log in order.

### G11: the importer   [Status: done | Model: opus]
Tree diff from the watermark, skip `Orange-Seq:` commits, parse-all-then-apply,
apply through the existing store methods with empty actor and the commit message
as rationale, quarantine on any failure. Depends on G5, G6.
**Validation:** `go test ./cmd/agentd/ -run GitImport` — including the
termination test (import → render produces no second commit) and a quarantine
test proving nothing was written.

### G12: webhook route + poll fallback   [Status: done | Model: sonnet]
`POST /agent/git/webhook` with signature verification, outside the JWT
middleware; a slow poll that does the same work.
**Validation:** `go test ./httpapi/ -run GitWebhook` — a bad signature is 401
and imports nothing.

### G13: `memory_create` `if_current`   [Status: done | Model: opus]
Compare-and-swap in the store, the MCP argument, and the loser's error naming
the winning id.
**Validation:** `go test ./agentdb/ -run IfCurrent` (live Postgres:
`AGENTKIT_TEST_POSTGRES_URL`) — a concurrent test where exactly one of two
writers wins and the loser is told who won.

### G14: named-document memory rendering   [Status: pending | Model: sonnet]
Newest-per-`name=` renders to `orange/memory/<name>.md`; an imported edit becomes
a new memory. Depends on G4, G11.
**Validation:** `go test ./gitproj/ ./cmd/agentd/ -run Document`.

### G15: bootstrap a project from a folder   [Status: done | Model: opus]
Point an empty project at a repo+subfolder; the importer runs once over every
file. Depends on G11.
**Validation:** round-trip test — render project A, bootstrap project B from it,
fold both, assert the configurations are equal.

### G16: console surface   [Status: pending | Model: sonnet]
Repo link, last-rendered seq, last-pushed SHA, push health, quarantine list with
reasons, on the project settings page.
**Validation:** `cd web && npm test && npm run typecheck`.

### G17: docs   [Status: pending | Model: sonnet]
`docs/21-git-projection.md`; pointers from `CLAUDE.md` and `docs/18` §9.
Must state plainly: what renders, what does not, that a literal secret refuses
to render, that git configuration is not importable, and that git is never the
write path.
**Validation:** human read.

### G18: end-to-end   [Status: pending | Model: opus]
Against the compose stack with a local bare repo as the remote: change a prompt
in the console → commit appears with the right trailers; edit the file and push
→ the change lands in the config log as event N+1 with the commit message as its
rationale; a malformed file quarantines and writes nothing.
**Validation:** `./e2e/run-stack-e2e.sh` with the new spec.

---

### G19: bring Subscription, Schedule and CustomImage under the allowlist guard   [Status: done | Model: sonnet]
Opened by G4. `allowlist.go`'s reflection guard covers `ProjectSettings`,
`Worker` and `Skill` only. G4 had to render subscriptions, schedules and images
against **deny-by-default field lists living in `render.go`**, outside the guard.
Nothing there can hold a credential today — that is exactly the assumption that
rots. Move those three into `allowlist.go`'s tables so the guard fails the build
when a field is added, and delete the local lists from `render.go`.
**Validation:** `cd go && go test ./gitproj/...`; then add a dummy field to
`agentdb.Subscription` and confirm the guard goes RED before removing it (say so
in the report — an unfalsified guard is decoration).

### G20: wire the webhook — the missing secret field, the resolver, and the mount   [Status: pending | Model: sonnet]
Opened by G12. Three loose ends it correctly refused to reach outside its own
files for:
1. **There is no per-project webhook secret anywhere.** G7 added `GitTokenEnv`
   (push, settings) and `GitHubTokenEnv` (push, project map). Neither is a
   webhook secret, and HMAC verification needs one. Add
   `git_webhook_secret_env` — a variable NAME, never a value, same rule as its
   two neighbours — and mark it NOT IMPORTABLE alongside them (DI3), or commit
   access becomes the power to accept forged webhooks.
2. **Implement `GitWebhookProjectResolver`** against `ProjectSettings.GitRemote`:
   payload repo identity → project + secret. The payload is used for ROUTING
   ONLY and must never reach what is imported.
3. **Mount the route on the root mux**, outside `apiAuthMiddleware`, the way
   `mcpSrv` is mounted — GitHub cannot hold a console JWT. `Endpoints.GitWebhook`
   is already declared; `Mux()` deliberately does not wire it.
Also read `AGENTKIT_GIT_WEBHOOK_POLL_INTERVAL` (`time.ParseDuration`, house style
per `gc.go`'s `AGENTKIT_SESSION_IDLE_TIMEOUT`, default 5m) and pass it as the
poll's `Interval`. `httpapi` reads no env vars itself and that must stay true.
**Validation:** `cd go && go test ./httpapi/ -run GitWebhook -race && go test ./cmd/agentd/ -run GitWebhook && go build ./...`

### G21: the importer drops a skill's `visibility` and `requires_build`   [Status: pending | Model: sonnet]
Opened by G15, pinned by its `TestGitBootstrapDoesNotRestoreServerOwnedSkillFields`.
Both fields **render** (they are on G2's allowlist) and the importer does not
apply them, so a skill does not round-trip: export a project, bootstrap a new
one, and every skill comes back with default visibility. Either apply them in
`gitimport.go`'s skill path, or mark them `Never` in the allowlist so the repo
stops claiming to carry state it cannot restore. **Applying them is the right
fix** — visibility is a real setting a human should be able to review in a diff.
Note `revision` restarting at 1 is correct and must NOT be "fixed": skills are
append-only and a bootstrapped project has no history to inherit.
**Validation:** `cd go && go test ./cmd/agentd/ -run 'GitImport|GitBootstrap'` —
extend the round-trip so a non-default visibility survives export→bootstrap.

## Discovered Issues Log

### DI1 (G3) — headers do NOT refuse partial interpolation; the design doc said they did

This plan's §D cites `attention.go:113-140` as already enforcing the whole-value
rule: "a header is either a literal or entirely one variable". The *comment*
says that. The *code* does not enforce it — a header value like
`Bearer ${TOKEN}` is passed through as a **literal**, unresolved, and
`TestAttentionChannelHeaderResolution` pins that behaviour.

G3 therefore refused partial interpolation for `url` only (which its ticket
explicitly required) and left header behaviour untouched, correctly: changing a
pinned behaviour was not its ticket.

**Consequence for G2 (the allowlist), which must handle this and not assume the
doc's claim:** a partially-interpolated value is neither a safe `${VAR}`
reference nor an obviously-literal secret, and it is the shape most likely to
carry a real token (`Bearer sk-…`, a URL with a token path segment). The
allowlist must treat **anything that is not a whole-value `${VAR}` reference**
as a literal, and therefore refuse to render it in a credential-bearing field.
Do not write the rule as "reject literals" — write it as "accept only whole-value
`${VAR}`", which is refusal-by-default and immune to this class of gap.

**Open, not for G2:** header partial-interpolation is a live oddity in its own
right — it sends a header that authenticates as nobody while looking configured.
That is `attention.go`'s bug, not this plan's, and it should be raised separately
rather than fixed here.

---

### DI2 (G1) — determinism is OUR property, not the YAML library's

`Render` being a pure function with byte-identical output is what makes the
import→render loop terminate (§C) and what lets `WriteTree` decide "nothing to
commit". That guarantee cannot be delegated.

G1 therefore does **not** hand a map to `yaml.Marshal` and trust its key order:
yaml.v3's own sort is natural/numeric-aware and is an implementation detail that
may change across versions. It builds the `yaml.Node` tree itself with
`sort.Strings` at every nesting level and lets the library encode scalars and
sequences only.

**Do not "simplify" this to `yaml.Marshal(map[string]any{...})`.** It will look
identical, pass a casual test, and reintroduce a dependency-version-sensitive
ordering into the one property the whole design rests on.

Related: `Frontmatter.Parse` deliberately tolerates hand-edited input — different
indentation, different quoting, a missing blank line after the closing fence —
because §C requires the importer to read commits written by humans, not only
files written by us.

---

### DI3 (G7) — two unenforced rules the projection worker must pick up

G7 added the four settings fields (migration **047**) and `github_token_env` to
the project map, and correctly stopped at the edge of its ticket twice. Both
gaps are silent — nothing fails, the wrong thing just happens.

**1. Token precedence is documented but NOT enforced.** Two places can name the
push credential: `ProjectSettings.GitTokenEnv` (the live, console-writable
column) and the project map's `github_token_env` (boot-time). G7's decision,
recorded in a comment: **the settings column wins; the map is the fallback for a
project whose settings row has not set one.** No code enforces that yet.
**G9 (the push loop) owns it** — resolve the effective token in exactly one
place, and fail loudly with a message naming both candidates when neither
resolves, rather than attempting an unauthenticated push.

**2. Defaults are not written, only declared.** `GitBranch` and `GitSubfolder`
stay empty in the database rather than being forced to `main` / `orange` on
write — matching how `BaseImage` and `SystemPrompt` behave. G7 supplied
`DefaultGitBranch` and `DefaultGitSubfolder` consts to be applied **at read
time**. **G4 (Render) and G8 (the projection worker) must apply them.** An
empty subfolder that reaches path construction would render at the repository
root — over the project's own files, and next to `.github/`.

**Also noted:** the env-var-name regexp is duplicated in `agentdb` because
`agentdb` cannot import `cmd/agentd` (the dependency direction is fixed by the
liftability invariant, CLAUDE.md rule 1). The duplication is correct; both
copies carry a keep-in-sync comment.

---

### DI4 (G6) — TRAILER FORGERY: a model-written rationale can forge `Orange-Seq:`

**This is a hole in the design as written (§B/§C), not in the implementation.**

The renderer puts a rationale into the commit body. Rationales are written by
models — the architect's prompt-rewrite reason is model text by definition. Git's
trailer parser reads the **last paragraph** of a commit message. So a rationale
ending in a line like:

```
Orange-Seq: 999999
```

is not text. It is a **trailer**, indistinguishable from one we wrote — and
`Orange-Seq:` is the exact field the importer uses to decide "this commit is
ours, skip it" (§C). A worker could mint commits the importer refuses to read,
or make its own write look like an engine write.

**The fix, already implemented in `Commit`:** the driver always emits its own
trailer paragraph **last**, which pushes any forged line into an earlier
paragraph where git's parser ignores it. There is a test proving it. The
guarantee holds only while `trailers` is non-empty — documented on the method.

**Binding rule for G11 (the importer), which does not exist yet:** read trailers
with git's own parser over the **last paragraph only** (`git interpret-trailers
--parse`, or `%(trailers)` in a `--format`). **Never `grep` the commit message
for `Orange-Seq:`.** A grep re-opens this hole completely and would look correct
in every test written from our own commits.

### DI5 (G6) — three smaller things the design did not say

- **`WriteTree` also sweeps untracked leftovers** in the subfolder. Without it, a
  crash mid-write leaves files that no later render ever removes, and the repo
  drifts from the database permanently. Ignored files are the one exception —
  `--exclude-standard` cannot see them.
- **The clone is verified by `git rev-parse --show-toplevel` matching the clone
  path**, not merely "am I inside a work tree". Otherwise a missing clone
  directory nested under any other repository silently attaches to the *parent*
  repo and commits the projection into it.
- **Every method takes `context.Context` first**, deviating from the Interfaces
  sketch in this document. **Accepted** — the render and push loops need
  cancellation. Later tickets should follow the code, not the sketch.

---

### DI6 (G13) — the CAS needs a LOCK, and two races it deliberately does not close

The design's "it is a `WHERE` clause on the insert" was wrong; §E is corrected
in place. Datasets get their backstop from a unique index; memories have none,
so a conditional insert alone admits two winners. A `pg_advisory_xact_lock` on
project+name makes it atomic, and G13 proved the lock (not the `WHERE`) is what
does the work by removing it and watching the concurrency test fail every time.

**Verified against a real database.** A throwaway `pgvector/pgvector:pg16`
container, never a shared one, removed afterwards. 8 live cases, 25-way
concurrency.

**Two races left open on purpose, documented in code:**
1. A plain `CreateMemory` with the same `name=` takes **no lock and always
   lands**. Opting out of the check stays the caller's choice — `if_current` is
   a tool for a writer that wants to coordinate, not a lock on the name.
2. There is **no "must not exist yet" form**, so two workers racing to create a
   name's *first* value can both win. Neither has a predecessor to name.
   Relevant to G14 (memory documents) if a rendered document can be created from
   two places at once.

**Also:** `go/cmd/agentd/mcp_memory_test.go` was edited outside G13's file list —
`fakeMemoryStore` had to implement the new interface method. Necessary and
minimal.

---

### DI7 (G5) — the `Parse` sketch in Interfaces was unimplementable; four rules now fixed

The Interfaces sketch could not be built as written and the shipped shape differs.
**Follow the code, not the sketch.** Binding on G11 (importer) and G14 (documents):

- **Signature.** `ParseAgainst(subfolder, path string, old, next []byte) (Change, error)`,
  with `Parse(...)` the create case (`old == nil`) and an explicit
  `ParseDelete(subfolder, path)`. `subfolder` is required because G1's
  `ParsePath` needs it; empty means `orange` (DI3's read-time default), so a
  root-level path is refused rather than accepted. The old bytes are required
  because "what changed" cannot be answered without them — which is the whole
  DI2 defence.
- **`Change` carries the body.** The sketch had none, which makes the `BodyOnly`
  path physically uncallable: the importer needs the body to call
  `SetWorkerPrompt`. Shipped: `Body`, `BodyChanged`, `Path`, `DroppedFields`,
  `HasChange()`. `BodyOnly` is exactly `BodyChanged && len(Fields) == 0`.
- **The kind is `KindMemory`, not `document`.** §C's table says "document";
  `paths.go` ships `KindMemory`. The code wins.
- **A removed frontmatter key means CLEAR that field.** The design said nothing,
  and it is a real thing a human does. The alternative — ignoring the removal —
  means a human can never clear a field through git and their edit vanishes
  silently. **G11 must not assume otherwise.**

Two more decisions, documented in code: scalars compare by textual form, so
requoting `enabled: "true"` is not a change; and a malformed *previous* version
is an error rather than a degradation to "create", because that degradation is
precisely the DI2 wipe wearing a different hat.

**The DI2 regression test was falsified**, not merely written: with the
same-value skip removed it fails with all six fields present, then passes again
when restored.

---

### DI8 (G2) — a FALSE safety claim in the codebase, now corrected; and the allowlist's calls

**The claim.** `agentdb.MCPServerConfig`'s doc comment asserted that Env and
Headers values "are never secret values, which is precisely what makes
persisting and displaying this config safe by construction". That sentence was
**false**: `Validate` rejects only *partial* interpolation (a value containing
`${` that is not a whole-value reference). A plain literal token has always been
valid stored config. This is DI1's twin, and worse — DI1 was a gap between a
comment and its code, this was an explicit invitation for the next agent to skip
a check. **Corrected in place** in `go/agentdb/sessions.go`, naming what is
actually true and pointing at the allowlist.

**Decisions G2 made that are worth knowing:**

- **`Project` and `UpdatedAt` render as `Never`.** `UpdatedAt` is the sharp one:
  a rendered timestamp changes on every write, so "equal state ⇒ no commit"
  would never hold and the loop would commit forever. Rendering a clock into a
  pure function is the bug that would have been hardest to find.
- **Skill `OwnerEmail` and `PromotedBy` are `Never` — PII.** A repo has forks
  and permanent history.
- **`Manifest` is `Never`** on the "unsure means Never" rule: unschema'd jsonb.
- **MCP server `url` is env-ref-only, going beyond the ticket. UPHELD.** Hosted
  MCP endpoints routinely carry the secret in the URL path (Zapier
  `…/mcp/s/<secret>/mcp`, Composio, Smithery) — the same "the URL *is* the
  token" shape as a Slack webhook. The cost is real: a project with a plain
  non-secret MCP URL must move it into an env var before it can render. That is
  a loud, one-edit error against a permanent leak, and §D's doctrine is refusal
  by default.
- **Empty/nil passes as "not set"** and the renderer omits the key rather than
  writing `url: ""`. Refusing it would make every project without an attention
  channel unrenderable.
- **`" ${VAR}"` with whitespace is refused**, because `resolveHeaders` trims
  before matching and `MCPServerConfig.Validate` does not — so its safety would
  depend on which reader picked it up.
- **`UnrenderableError` never quotes the offending value**, only its dotted
  path, because the error travels to logs and the console. A test asserts no
  secret substring reaches the message.

**The guard was falsified four ways**, each restored: a new secret-looking field
on `Worker`; a renamed field; a stale table entry; and downgrading `OwnerEmail`
from `Never` to `Render`. All four go red.

---

### DI9 (G4) — six things, one of them a landmine for the importer

1. **`Render` could not compile.** The name was already a `Decision` const in
   `allowlist.go`. Shipped as **`RenderTree`**, which reads well beside
   `Repo.WriteTree`. Interfaces sketch corrected above.
2. 🔴 **`ParsePath` rejects `orange/README.md`** — the renderer's own generated
   file is not an entity shape. **The importer MUST skip it explicitly.** Without
   that, every push touching the README quarantines the whole project, and the
   renderer rewrites the README, so the project would be permanently stuck. G11
   was messaged mid-flight. A test pins the rejection.
3. **Three structs are outside the guard.** `Subscription`, `Schedule` and
   `CustomImage` have no allowlist table, so G4 used deny-by-default lists in
   `render.go`. Nothing there can hold a credential *today* — precisely the
   assumption that rots silently. **Ticket G19 opened above.**
4. **Timestamps and runtime state are never rendered** — `updated_at`,
   `last_evaluated`, `provision_failures`. They change without anyone deciding
   anything, so rendering them would commit once a minute forever. Same reasoning
   as DI8's `UpdatedAt`.
5. **G1's frontmatter cannot express a JSON null or a non-integer number.** Both
   are refused with a named error rather than dropped or rounded — a silent
   round would corrupt configuration. Consequence: a jsonb null inside
   `mcp_config` makes a project unrenderable until a human fixes it. Acceptable,
   but it is a real way a project can stop projecting.
6. **Zero values: strings and containers omit when empty; numbers and bools
   always render.** `enabled: false`, `daily_tokens_soft: 0` and
   `max_firings_per_hour: 0` are *configuration*, and omitting them would publish
   a file that reads as "take the default" — which is a different setting.

**Five load-bearing tests were falsified**, each restored byte-identical:
breaking newest-per-name, skipping `CheckRenderable`, dropping subfolder
validation, omitting false bools, and dropping body-newline normalisation.

---

### DI10 (G11) — five things the importer had to decide, and one store weakness

**Trailers, hardened beyond DI4.** Read with `git log --format=%(trailers:key=Orange-Seq,valueonly,only)` — git's own parser, last paragraph only, no grep anywhere. A commit counts as ours only with **both** `Orange-Seq` and `Orange-Event` trailers **and** the fixed author email. It errs toward *importing* an ambiguous commit, which is the safe direction: importing our own render is a no-op, skipping a human's edit loses their work.

🔴 **`GetSubscription` and `GetSchedule` have no not-found sentinel** — they return a bare `fmt.Errorf("subscription not found")`, indistinguishable from a database outage. Guessing wrong there turns an outage into a **create**. G11 routed around it via `ListSubscriptions`/`ListSchedules` and id matching. The stores should grow real sentinels (`ErrSubscriptionNotFound`, `ErrScheduleNotFound`) like `ErrMemoryNotFound` already has. Not fixed here — logged for a follow-up.

**A mixed push re-imports our own values.** One of our commits plus one human's in the same push means the tree diff spans both, and the `Orange-Seq` skip cannot help — the diff is of trees, not commits. Closed with a **same-value suppression pass**: the parsed result is compared against the stored row and nothing is written when they already agree. This also strengthens loop termination from the inbound side.

**A partial apply is not a quarantine, and is not pretended to be.** If a store call fails mid-plan, the earlier writes are real config events and cannot be unwritten. G11 returns the error with the watermark unmoved and lets same-value suppression make the retry a no-op. Claiming atomicity across a sequence of independent config events would be a lie in the log.

**The importer targets the REMOTE tip and does not move the local branch** — see the note sent to G8, who owns fast-forwarding it in boot reconciliation. Without that the branches diverge on the first human push and G9's fast-forward-only push refuses forever.

**Three things a human can do in git that have no effect**, reported in `result.Ignored` rather than dropped silently so G16 can tell the operator: images are not importable (nothing in frontmatter reconstructs a content-addressed blob), and deleting a skill or a memory file removes nothing (both are append-only).

---

### DI11 (G12) — the webhook has no secret to verify against

🔴 **Nothing in the system holds a webhook secret.** `ProjectSettings` and the
project map's `projectConfig` were both checked, along with migration 047: G7
added `GitTokenEnv` and `GitHubTokenEnv`, and **both are push credentials**.
HMAC verification needs a different secret entirely. G12 designed
`GitWebhookProjectResolver` as the seam rather than inventing a field outside its
three files, which was right. **Ticket G20 closes it.**

**The design doc understated the webhook's danger, and G12 got it right anyway.**
The payload is attacker-influenced input even when signed — a repository's own
collaborators can push anything. G12's interface therefore carries **no ref,
commit list or file list at all**, only a project name: the payload has no
channel to influence what gets imported *even in principle*, rather than merely
being ignored by convention. What is imported still comes from the repository
itself via the tree diff. There is a test proving a forged ref inside a validly
signed payload changes nothing.

Other decisions: `hmac.Equal`, never a string compare; the body is bounded by
`http.MaxBytesReader` at 1 MiB **before** JSON parsing, resolution or hashing, so
an unauthenticated route cannot be made to buffer or hash arbitrary bytes;
non-push events get a quiet 2xx because GitHub hammers a non-2xx; and the route
is deliberately **not** in `Mux()` because it must sit outside the JWT
middleware — mounting is `main.go`'s job (G20).

---

### DI12 (G15) — bootstrap CANNOT reuse the import range, and the reason is inverted

🔴 **An exported folder is made entirely of the renderer's own commits.** So
`ImportRange`'s `Orange-Seq` skip — the rule that stops us re-importing our own
output — would skip **every commit in the folder**, import nothing, and **report
success**. A silent no-op that looks like a working bootstrap.

Bootstrap therefore reads the **tree** and never consults trailers at all. That
is safe for the reason the whole design is safe: the files still go through the
store's front door, so they become ordinary config events either way. The
authorship check is an optimisation against re-import, not an authorisation
check, and bootstrap does not need it. G15's harness commits the folder *as
ours* so that mistake fails loudly if anyone re-introduces it.

**The round-trip is real.** Render project A — three workers including a
disabled one, populated `mcp_config` on both project and worker, an attention
channel, briefing lists, a skill, two subscriptions, two schedules including a
session-mode one, and a named memory document — commit, bootstrap B, fold both,
`DeepEqual`. Then the other direction: re-rendering B reproduces the folder
byte-for-byte apart from `settings.md`, where the git fields are deliberately
absent.

**Falsification, and one honest negative result.** Removing the quarantine
return goes red; removing the order sort goes red; advancing the watermark on
quarantine goes red; **removing the README skip turns all eight tests red**,
confirming DI9 item 2 is exactly as dangerous as stated. And
`gitBootstrapCheckNotImportable` does **not** go red when removed — gitproj's
`DroppedFields` and the settings restore already hold the line. G15 kept it as
defence in depth at the most dangerous moment and **labelled it as such in the
code rather than claiming it was load-bearing**. That is the right way to report
a check that earns its place without earning a test.

**Two limits pinned rather than hidden:** a skill's `visibility` and
`requires_build` render but are not applied on import (**ticket G21**), and an
imported body keeps the renderer's trailing newline, so a prompt round-trips
modulo `\n` — idempotent under re-render, so loop termination still holds.

---

_(Append findings here; do not edit tickets to match reality.)_
