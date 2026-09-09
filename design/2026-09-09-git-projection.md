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
func Render(st ProjectState, subfolder string) (map[string][]byte, error)

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

### G2: the secret allowlist and its guard test   [Status: pending | Model: opus]
`allowlist.go` + the reflection guard. Depends on G1.
**Validation:** `go test ./gitproj/...`; add a throwaway field to
`ProjectSettings` and confirm the guard test **fails**; remove it.

### G3: `${VAR}` for `attention_channel.url`   [Status: done | Model: sonnet]
Mirror `resolveHeaders`. A literal URL keeps working (no migration) but is
marked unrenderable by G2's allowlist.
**Validation:** `go test ./cmd/agentd/ -run Attention`.

### G4: `Render`   [Status: pending | Model: opus]
The pure function, all entity kinds. Depends on G1, G2.
**Validation:** `go test ./gitproj/...` — golden-file tests; a determinism test
that renders the same state 100× and asserts byte equality.

### G5: `Parse` and the DI2 field-merge rule   [Status: pending | Model: opus]
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

### G11: the importer   [Status: pending | Model: opus]
Tree diff from the watermark, skip `Orange-Seq:` commits, parse-all-then-apply,
apply through the existing store methods with empty actor and the commit message
as rationale, quarantine on any failure. Depends on G5, G6.
**Validation:** `go test ./cmd/agentd/ -run GitImport` — including the
termination test (import → render produces no second commit) and a quarantine
test proving nothing was written.

### G12: webhook route + poll fallback   [Status: pending | Model: sonnet]
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

### G15: bootstrap a project from a folder   [Status: pending | Model: opus]
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

_(Append findings here; do not edit tickets to match reality.)_
