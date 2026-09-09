# 21 — The git projection

> **Who this is for.** An operator pointing a project at a git repository, and anyone about to
> change the code that renders to it or imports from it.
>
> **Postgres-only.** Everything here needs `DATABASE_URL`. On the sqlite fallback the product layer
> is silently inert and the console panel hides itself rather than reporting an error.
>
> Design and the full findings log: `design/2026-09-09-git-projection.md`. Read its **Discovered
> Issues Log** before changing anything under `go/gitproj/` or `go/cmd/agentd/git*.go` — it records
> six things that look like tidying and are not.

---

## 1. The one rule

> **The database is the only place where writes are put in order. Git is a door out (render) and a
> door in (import). Neither door writes anything the store did not serialise.**

When a question arises that this document does not answer, answer it with that sentence.

This is the *opposite* of "git is the source of truth", and the inversion was considered and
rejected. Three reasons, all structural:

1. **Git has no total order.** `config_events.Seq` exists because millisecond timestamps plus a
   random uuid were not enough to order a log the spec calls authoritative
   (`go/agentdb/config_events.go:93-107`). Git offers second-resolution, committer-settable dates on
   a DAG that `pull --rebase` rewrites. The architect's "what changed since my last run" and narrow
   revert's "is this the newest change to this entity" both read an order git cannot promise.
2. **Provenance would become testimony.** `created_by_worker` / `created_by_session` are stamped
   server-side from the session token and cannot be forged. A commit message is text somebody typed.
   `docs/20-datasets.md` §9's trust rule depends on the stamp.
3. **The human-versus-agent collision has no safe resolution.** `merge -X ours` silently discards a
   merged pull request; under `rebase` git inverts the meaning of `ours` and discards the *agent's*
   write instead, while the config log says it happened; a plain merge freezes a project's
   configuration on a conflict no background loop can resolve.

Running the arrow the other way keeps all three and still delivers diffs, pull-request review of a
prompt change, humans editing configuration by hand, bootstrap-from-a-folder, and an off-site
readable record. **The only thing given up is the slogan.**

---

## 2. The two doors

### Out — render

1. Something changes configuration. `WithConfigEvent` writes the change and the log entry in one
   transaction, as it always has.
2. The post-commit hook fires and does **one thing**: marks the project dirty. No git work ever
   happens inside a model's tool call.
3. A background loop renders the project's whole state to files in a local clone and commits — one
   commit per config event, in `seq` order, with the **true actor** in trailers copied from the log:

   ```
   worker_prompt_write: copywriter

   <the rationale, verbatim>

   Orange-Project: wolf
   Orange-Seq: 1247
   Orange-Event: 7f3c…
   Orange-Action: worker_prompt_write
   Orange-Actor-Worker: architect
   Orange-Actor-Session: sess-…
   ```

   The commit **author** is a fixed bot identity on purpose, so nothing ever looks attributed to a
   person who did not do it. Who did it lives in the trailers, derived.
4. A separate loop pushes, **fast-forward only**. GitHub being down never blocks an agent: commits
   pile up locally and the loop catches up.

If the remote is lost or wrecked: **delete the clone and re-render from the database.** The database
can rebuild git. Git can never rebuild the database. That asymmetry is the design.

### In — import

1. A human edits a file and pushes, or merges a pull request.
2. A webhook says "look now"; a poll catches it when delivery is missed (GitHub does not retry
   failed deliveries, pushes coalesce, and order is not guaranteed).
3. The importer diffs **trees** — last-imported SHA against remote HEAD. It never walks or
   interprets git history; that is a DAG, not an order.
4. Every changed file is parsed and validated **first**. If any file fails, the whole push is
   **quarantined and nothing is written** — a malformed file cannot half-apply a project.
5. Each change calls **the same store method the HTTP API calls**, with the commit message as the
   rationale and an empty actor, which is already how the system encodes a human edit.

**Why conflicts cannot happen:** the database is the single serialisation point. An agent's 10:58
rewrite is config event N; your 11:00 merge is event N+1. Both are real, both are in the log, in
order, and neither is silently discarded.

**Why the loop terminates:** `RenderTree` is a pure function of database state. Re-rendering after an
import produces either the identical tree (no commit) or one normalising commit, after which it
matches.

---

## 3. Turning it on

Per project, in **Project settings → Git repository** (or `PUT /agent/project-settings`):

| Field | Meaning |
| --- | --- |
| `git_remote` | the repository URL. **Empty means projection is off** — a deliberate state, not a broken one |
| `git_branch` | default `main` when empty |
| `git_subfolder` | the folder Orange owns; default `orange` when empty. A single path segment |
| `git_token_env` | the **name** of an environment variable holding the push token — never the token |
| `git_webhook_secret_env` | the **name** of an environment variable holding the webhook secret |

**These five fields are NOT IMPORTABLE.** The importer never writes them. If it did, commit access
to the repository would become the power to redirect a project's projection at another repository,
another credential, or a forged-webhook secret.

`github_token_env` in the project map is a boot-time fallback for the push token; the settings column
wins when both are set. When neither resolves, the push **fails loudly naming both candidates**
rather than attempting an unauthenticated push.

### Environment (agentd)

| Variable | Default | What |
| --- | --- | --- |
| `AGENTKIT_GIT_CLONE_ROOT` | `/data/git-projection` | where per-project clones live. **Must be on a persistent volume** — on the standard deployment this is the existing `agentd-data` PVC. On ephemeral storage every restart re-clones and re-renders |
| `AGENTKIT_GIT_WEBHOOK_POLL_INTERVAL` | `5m` | the inbound poll. `off` disables it; a bare number is refused at boot rather than guessed at |
| *(the variables your projects name)* | — | the values behind `git_token_env` and `git_webhook_secret_env`. agentd reads them by the name the project stores; **the values are never written to the database** |

### Schema

Migration **048** — `git_projection_state` (the lease and the three watermarks) and
`project_settings.git_webhook_secret_env`. The state table is a numbered migration, not created at
boot: a table that appears by side effect is invisible in the migration list and can differ between a
fresh database and an upgraded one. A test greps for the `AutoMigrate` call so it cannot come back.

### Routes

| Route | Auth |
| --- | --- |
| `GET /agent/git-projection` | console JWT; project comes from the `customer` claim only — no path, query or body parameter, and a session-scoped embed token gets a 404 |
| `POST /agent/git/webhook` | **outside** the JWT middleware, authenticated by GitHub's HMAC-SHA256 signature. GitHub cannot hold a console JWT |

---

## 4. What renders, and what deliberately does not

```
orange/
  README.md              generated; skipped by the importer
  settings.md            project prompt as body, settings as frontmatter
  workers/<name>.md      system prompt as body, the rest as frontmatter
  skills/<name>.md
  subscriptions/<id>.md
  schedules/<id>.md
  images/<name>.md
  memory/<name>.md       NAMED memories only — see §6
```

**Not rendered, on purpose:**

- **The append-only memory log** — summaries, lessons, verdicts, retractions, prompt-revisions. They
  are written once and never edited, so a diff shows nothing, and they are where all the volume is.
  Confirmed and marked do-not-re-open in the design.
- **Datasets, artifacts and snapshots.** They are versioned blobs with their own storage and reaper;
  git would be a third tier for the same job.
- **Timestamps and runtime state** — `updated_at`, `last_evaluated`, `provision_failures`. These
  change without anyone deciding anything, so rendering them would commit once a minute forever and
  break the "equal state ⇒ no commit" property the whole loop rests on.
- **Identity and internal storage fields**, and **personal data** (`owner_email`, `promoted_by`): a
  repository has forks and permanent history.

---

## 5. Secrets

**The rule is "accept only a whole-value `${VAR}` reference", not "reject literals".** Refusal by
default. A value like `Bearer ${TOKEN}` is neither a safe reference nor an obvious secret, and is the
shape most likely to carry a real token.

A credential-bearing field holding anything else makes the project **unrenderable**: it does not
render at all, the field is named, and the offending value is **never quoted** — not in the error,
not in the log, not in the console. That is refusal, not redaction: a redacted value would teach an
operator that publishing it was safe.

Credential-bearing leaves today: `attention_channel.url` and its headers, and MCP server `url`,
`env.*` and `headers.*`. **MCP URLs count as credentials** because hosted MCP endpoints routinely
carry the secret in the URL path. A project with a plain non-secret MCP URL must move it into an
environment variable before it can render — one loud edit, against a permanent leak.

**`go/gitproj/allowlist.go` is the authority, and a reflection guard fails the build when any field
of the six rendered structs has no explicit decision.** A new field must be a decision, not an
accident. If you add a field and CI goes red, that is the guard working: add the field to the table
with a reason. Default to `Never` when unsure.

> ⚠️ `agentdb.MCPServerConfig.Validate` rejects only **partial** interpolation. A plain literal token
> has always been valid stored config. That configuration is safe to keep in a private database and
> is **not** safe to publish, which is exactly why the allowlist checks every leaf itself. Its doc
> comment used to claim otherwise; that claim was false and has been corrected.

---

## 6. Memory documents

A memory carrying a `name=` label is the substrate's existing "current value of X" convention. The
**newest** such memory renders to `orange/memory/<name>.md`.

- **Memories stay immutable.** A human editing `orange/memory/message-board.md` produces a **new**
  memory with the same `name=`, exactly as an agent's write does. Nothing is mutated, and every
  version keeps its stamp and stays searchable — git history is not searchable by label or meaning.
- **Concurrent writers should use `memory_create`'s `if_current`**: "append only if the newest memory
  with my name is still the one I read". Without it, two workers rewriting a shared document both
  succeed and the loser never learns it lost.
- **A memory whose name is not a legal filename is skipped and reported**, not fatal and not renamed.
  Label values allow `.` and `_`; path segments do not. Failing would let any worker holding
  `memory_create` stop a project projecting; renaming would publish a file under a name that is not
  the document's, which the importer would read back as a *different* document.

---

## 7. Project as code

Point a new project at a repository and subfolder and its whole configuration is created from the
files. It is the importer run once over the whole tree, not a second code path, and the round trip is
tested: render project A, bootstrap project B from that folder, fold both configurations, assert
equal.

**What bootstrap does not restore:** the memory log. Only named documents are in the repo, so a
bootstrapped project arrives with its whole configuration and its named documents and **no past**.
That is the intended trade — the repo is a current-state export, not an archive.

Also not restored, and reported rather than dropped silently: **images** (nothing in frontmatter
reconstructs a content-addressed blob), and a **deleted skill or memory file removes nothing**,
because both are append-only. A skill's `revision` restarts at 1, which is correct.

---

## 8. Known hazards

- 🔴 **A `contents:write` token to your source of truth is held by the process that also holds
  `DATABASE_URL`.** It is passed to git through a credential helper holding only the variable's
  *name*, so the value never enters the remote URL, argv, or `~/.git-credentials` — but the process
  can still push. Scope the token to the one repository.
- 🔴 **Git cannot forget.** Anything rendered is in history permanently, for everyone with repository
  access, including forks. The allowlist is what stands between a new field and that.
- 🔴 **A push failing repeatedly is the failure mode that hides.** Agents keep working, commits pile
  up locally, and the repository silently goes stale. The console says so; watch for it.
- 🔴 **A diverged remote never resolves itself.** Nothing is merged, rebased, reset or forced,
  because every automatic resolution silently destroys somebody's work. A human has to reconcile it.
- 🔴 **Quarantine and ignored results are not yet persisted** (design ticket G23), so those two
  console lists are empty on a real deployment until that lands. An operator whose push was rejected
  wholesale currently sees that it was, but not which file.
- **Editing an image file, or deleting a skill or memory file, does nothing.** The importer reports
  it as ignored rather than failing, so check that list before concluding the system is broken.
- **A jsonb null or a non-integer number inside `mcp_config` makes a project unrenderable** — refused
  with a named error rather than silently rounded or dropped.
- **One writer.** The deployment runs a single `agentd` (`replicas: 1`); a per-project lease enforces
  it. A second clone of the same remote elsewhere is a second writer, and a force-push from either
  loses the other's commits.

---

## 9. Where the code is

| Path | What |
| --- | --- |
| `go/gitproj/` | Pure layer: `RenderTree`, `ParseAgainst`, the allowlist and its guard, path validation, deterministic frontmatter, and `repo.go` — the only place that execs `git` |
| `go/cmd/agentd/gitprojection.go` | The render loop, the push loop, boot reconciliation, the lease, the credential helper |
| `go/cmd/agentd/gitimport.go` | Tree diff, trailer reading, quarantine, apply-through-store |
| `go/cmd/agentd/gitbackfill.go` | One commit per config event, replayed from seq 1 |
| `go/cmd/agentd/gitbootstrap.go` | Project as code |
| `go/cmd/agentd/gitdocuments.go` | Newest memory per `name=`, paged |
| `go/httpapi/gitwebhook.go` | The webhook and the poll |
| `go/httpapi/gitprojectionstatus.go` | What the console reads |

**If you change the trailer format, read DI4 first.** Rationales are model-written, git parses the
last paragraph of a message as trailers, and `Orange-Seq:` is the field that decides whose commit a
commit is. The defence is that our trailer block is always emitted last and is never empty. **Never
read trailers by grepping the commit message** — a grep passes every test written from our own
commits and re-opens the hole completely.
