# CLAUDE.md — operating guide for Agent Bob

Agent Bob (formerly Agent Orange).

Read this first. It tells an agent what Agent Bob is, how the repo is laid out, how to build and
run it, and the rules to keep it healthy.

## Who

BadCode is two people: **Kai Davenport** (main developer) and **Jack** (lead creative
designer). Either may be the user in a session. BadCode's marketing manager is the first real
Agent Bob use case (`docs/product/17-product-spec.md` §8.8).

## What Agent Bob is

Agent Bob is a **reusable runtime for container-backed AI agent sessions**. You create a
**session** configured with a system prompt, a base image, MCP tools, and skills; the runtime
provisions a container, runs an in-image agent harness inside it, and streams the conversation back
over SSE. Sessions are interactive (message back and forth), durable (state persisted), and
snapshot-able (commit a running session to a new image).

On top of that runtime sits the **product layer**: a project is a namespace holding *workers*
(rows of prompt + tools + wiring), an append-only labeled *memory*, an *event* spine with
subscriptions and cron *schedules*, named *images* and *skills*, and a *config log* recording
every management mutation. Workers are woken by events or schedules, composed into jobs, and can
rewrite each other's prompts through MCP tools — that self-improvement loop is the design's
definition of done. Spec: `docs/product/`. Operating guide: `docs/18-workers-memory-events.md`.

It was forked wholesale from an in-house Go runtime ("agentkit"); this repo is now the canonical
Agent Bob. Three pieces:

- **Go orchestration core** (`go/`) — module `github.com/badcodetv/agent-bob`. The
  `Runner`, session lifecycle, container control, image registry, persistence, event pipeline. This
  is the library a host app embeds.
- **In-image agent** (`sandbox/`, TypeScript) — the control server that runs *inside* each session
  container and wraps the model harness (`@anthropic-ai/claude-agent-sdk`).
- **Chat UI** (`web/`, React) — a component *library* (no router, no app shell) rendering live or
  replayed sessions from the canonical event stream, plus the product-layer pages. The shell that
  composes it is `examples/web/`, which is what the stack serves.

> **Status / provenance.** MIT-licensed (`LICENSE`, copyright BadCode). All three packages build and test; the gates are
> `go build ./... && go vet ./... && go test ./...` from `go/` (Go floor is now **1.25**, raised by
> the GCP SDK) and each JS package's own `typecheck` + `test` scripts. Don't quote test counts
> here — `.github/workflows/ci.yml` runs all three and is the live number.
> The **product layer is built**: every item in `docs/product/06-work-plan.md` is ticked (37 of 37;
> G3 was the last). The acceptance loop (§8.7: a worker rewrites another worker's prompt, with a
> rationale, and the next job runs improved) closes offline in `e2e/features/acceptance-loop.spec.ts`,
> and the same loop was **observed against the real Anthropic API on 2026-07-26** — the composed
> prompt changed the model's behaviour, `request_human_attention` parked the delivery at
> `awaiting_human`, and the §6.2.4 prompt-injection boundary held against event text ordering the
> model to ignore its prompt. What is deliberately **not** done is the first production seeding
> (§8.8's BadCode marketing manager): that is a production act, left to Kai.
> GCP support is implemented and **wired into `agentd`**: a GCS `BlobStore` (`extension/gcsblob`),
> a pluggable registry-auth seam with an ADC provider (`imageregistry/auth`, `auth.GCP`), and
> config-driven backend selection (`cmd/agentd/backends.go`, env: `AGENTKIT_BLOB_BACKEND`,
> `AGENTKIT_REGISTRY_BACKEND`, …). Defaults preserve the local fs stack. Blob upload and snapshot
> push+pull are **recorded** verified against the live project on 2026-06-25 in MIGRATION.md
> §4a/§4b; that record is two dated commits and nothing re-runnable, so treat it as testimony, not
> as a green test — the caveat is stated in MIGRATION.md.
> The plan and current state live in **`MIGRATION.md`** — read it before doing migration work.
>
> **Embeddable Bob** (`design/2026-08-06-embeddable-agent-bob.md`, in progress on this
> branch) makes Agent Bob a singleton service other applications embed. Built so far: project
> **API keys** (`X-API-Key`, resolved at boot from env vars named by the project map's new object
> form — no table, no UI); **embed tokens** (`POST /agent/embed-token`, ≤1h, session-scoped) and an
> **embed page** (`GET /embed/session/{name}#token=…`) served with CSP `frame-ancestors`;
> **session names** (`name` on `POST /agent/session`, `GET /agent/sessions/by-name/{name}`);
> **session-mode schedules** (`schedules.target_session`); the **artifact download route**
> (`GET /agent/artifacts/{id}/download` and two by-name twins) that three console components had
> been calling into a 404; **full-content memory reads** over HTTP (`GET /agent/memories/{id}`,
> `…/current?name=`); tenancy enforced on every session-by-ID route; and the **core MCP server now
> reaching HTTP-created sessions**, so a chat session can call `memory_current`. Migrations
> `035_session_names` + `036_schedule_target_session`. **Postgres-only, and unavailable in dev-open
> mode.** The integration guide, and a hazard list you should read before exposing the stack to
> another application, is **`docs/19-embedding.md`**. Still open at the time of writing: the live
> stack checks for T13/T15 and the end-to-end spec (T17).
>
> **Datasets and the memory append route** (`design/2026-08-20-agent-wolf.md`) close the two gaps
> that stopped an application from embedding Bob as a system of record. A **dataset** is a
> project-scoped, named, **versioned blob** (migration `045_datasets`, `go/agentdb/datasets.go`):
> every write is compare-and-swap, the bytes are pulled out of a container by `Exec`+`cat` and read
> back through a short-lived scoped download URL, so a shared numeric series never crosses the
> model's context — three MCP tools (`dataset_list`/`_get`/`_put`), four HTTP read routes, and a
> version reaper whose blob prefix (`_datasets/bytes/`) is load-bearing because agentd runs **one
> global `BlobStore`** shared with artifacts and snapshots. **`POST /agent/memories`** is the
> trust anchor: an API key or console JWT may now append a memory, provenance is server-stamped
> **empty** and a body supplying it is refused, so a reader can tell the application's own word from
> anything written inside a container (`?include_retracted=1` is the matching audit view). Both are
> Postgres-only. Documented in **`docs/20-datasets.md`**.
>
> **The memory-coordinated organisation** (`design/2026-09-08-memory-coordinated-organisation.md`,
> **complete on this branch — 27 of 27 tickets**) makes a new project start with an *interview*
> rather than a roster.
> Creating a project takes a name and a **required goal**; an `interviewer` worker drives a
> conversation towards a goal, a measure and a first set of labelling rules, and deposits that as
> a **charter** — an append-only memory the console renders and a human approves ONCE. Approving
> it creates exactly one worker, the **architect**, plus a daily schedule, an `architect.run`
> subscription, the project background prose, a **project-wide briefing** carrying the label
> registry, and two memory seeds. The architect designs the actual roster on its first run.
> Built: `go/orgprompts` (the three prompts, as embedded files), `go/charter` (schema, parser,
> validator, `Resolve`), `onboarding@v1`, the `charter_validate` core tool, the two
> `/agent/charter` routes, the onboarding screen and charter panel in `web/`, the shell wiring in
> `examples/web`, and **narrow revert**: `GET /agent/config-events/{id}`, `POST
> /agent/config-events/{id}/revert`, and a "Revert to this version" action on every changelog
> entry (a forward compensating write — nothing is ever erased, and only the newest change to a
> thing may be reverted).
>
> The whole journey was walked end to end against the mock stack on 2026-09-09 (T26): interview →
> charter → approve → architect with an ENABLED daily schedule → an `architect.run` event whose
> job's composed prompt carries the label registry → revert, including a non-newest revert refused
> with a readable reason. **What is deliberately not proved offline** is that a model reads its own
> session id out of the seed and labels the charter with it — mock mode proves transmission, never
> discovery (`docs/product/25-cooperative-patterns.md` §5). That was observed against the real
> model in T1.
>
> Two behaviours changed late and are easy to trip over. `PUT /agent/workers/{name}` now **keeps
> every field a body omits** — it is create-or-keep, not create-or-replace (T27 for the content
> fields, DI11 for `max_instances`/`enabled`/`frozen`, which used to reset and could silently thaw a
> frozen worker). And on that route a briefing of `null` means *leave it alone* while `[]` means
> *clear it*; coercing one to the other breaks something real in either direction (DI14).
>
> **The git projection** (`design/2026-09-09-git-projection.md`, **complete on this branch — 27 of
> 27 tickets**) makes a project's configuration readable, diffable and editable as markdown in a git
> repository. The inversion Kai first proposed — git as the source of truth, the database as a
> rebuildable index — was **rejected** by an adversarial review on three structural grounds: git has
> no total order (`config_events.Seq` exists because ms + uuid was not enough), provenance would
> become testimony rather than a server stamp, and the human-vs-agent collision has no safe merge
> (`-X ours` discards a merged PR; under rebase git inverts it and discards the *agent's* write).
> So the arrow runs the other way: **the database takes the writes, git gets the publication.**
> A renderer hangs off the existing post-commit config hook and commits one commit per config event
> in seq order, with the true actor in trailers derived from the log; a push loop pushes
> fast-forward-only. Human commits come back **in** via webhook or poll, are diffed as trees, and
> are applied through the **same store methods the HTTP API calls** — becoming config event N+1, so
> conflicts are impossible by construction. Backfill replays the whole log into a real history;
> `POST /agent/git-bootstrap` builds a project from a folder. Named memories render as documents;
> the append-only log does not. Migrations `047`–`049`. **Postgres-only.**
> Operator's guide: **`docs/21-git-projection.md`**.
>
> ⚠️ **Two things to know before pointing this at a repository you care about.** Secrets are an
> **allowlist with a reflection guard** — a field with no rendering decision fails the build, and a
> credential-bearing field holding anything but a whole-value `${VAR}` **refuses to render** (not
> redacts). Add a field to `ProjectSettings`/`Worker`/`Skill`/`Subscription`/`Schedule`/`CustomImage`
> and CI goes red until you decide; that is the guard working. And **git cannot forget**: anything
> rendered is in history permanently, for everyone with repository access, forks included.
> If you change the commit trailer format, read **DI4** first — rationales are model-written, git
> parses the last paragraph as trailers, and `Bob-Seq:` decides whose commit a commit is. Never
> read trailers by grepping the message.

> ⚠️ **Read this before running it on a project you care about.** Approving a charter creates an
> **enabled** daily schedule, and *the architect's loop has no mechanical brake: every rule in its
> prompt is an instruction it may choose to delete, revert is the entire control, and the failure
> mode to watch for is the system quietly ceasing to tell you what it is doing.* That is a
> deliberate decision, not an oversight. The operator's guide, including the known limits, is
> **`docs/18-workers-memory-events.md` §9a**.

## Reading path

If you need to understand the system rather than patch one file, read in this order:

1. `README.md` — what it is, in one screen.
2. `docs/01-architecture.md` — the runtime: layers, and how Go owns orchestration.
3. `docs/product/00-overview.md` (the map) → `docs/product/17-product-spec.md` (goal, atoms,
   binding principles P1–P8, non-goals) → the component designs `docs/product/01`–`09`.
4. `docs/18-workers-memory-events.md` — the product layer from an operator's seat.
   Then `docs/19-embedding.md` if another application will be embedding this one.
5. `docs/product/06-work-plan.md`'s **Discovered Issues Log** — ~100 entries recording what was
   actually built and what surprised us. When a design doc and this log disagree, the log is
   closer to the code; when the log and the code disagree, the code wins.
6. `docs/product/25-cooperative-patterns.md` — 38 cooperative workflow patterns from the wider
   world, each judged against *this* code by an adversarial fit pass (9 expressible, 25 partial,
   4 blocked). Its §5 is the sharpest existing statement of what the substrate cannot do;
   `26-work-plan-cooperative-tests.md` is the unbuilt test plan and gap list that follows from it.
7. The engine reference docs (`02`, `03`, `05`, `06`, `07`, `13`, `14`, `15`) by seam, as needed.

`README-stack.md` at any point: running the thing is cheap and answers a lot.

## Repo map

| Path | What |
| --- | --- |
| `go/` | The engine (Go module). Entry type: `Runner` in `go/agentkit.go`. CLIs in `go/cmd/` (`agentd`, `imagetree`). Runnable examples in `go/examples/` (`standalone`, `mockproxy`, `exampleimage`). |
| `sandbox/` | In-image agent (TS). The HTTP/SSE control server + harness adapter that runs inside a session container. `sandbox/Dockerfile` builds the harness image. |
| `web/` | React component library: chat (one event reducer drives live + replay identically) plus the product-layer pages — project settings, workers, events/jobs, subscriptions + schedules editors, changelog. No router; the app shell is `examples/web/`. |
| `installations/` | **Example** base images (`core`, `example`) — see `installations/README.md`. Real per-project images live in their own project repos. |
| `docs/` | Numbered architecture docs, consolidated 2026-07-22 (numbering has deliberate gaps): `01-architecture`, `02-execution-environment`, `03-image-registry`, `05-event-streaming`, `06-artifacts`, `07-in-image-agent`, `13-fleet-placement`, `14-host-adapters`, `15-standalone-stack`, `18-workers-memory-events` (the product layer, from an operator's seat — read it before touching workers/memory/events code; **§9a** is onboarding, the charter, the architect's loop, and the unhedged statement that the loop has no mechanical brake), `19-embedding` (integration guide + hazard log for an application embedding Bob: the three credentials, the project map's object form, named sessions, session schedules, embed tokens/iframe, artifact + memory reads), `20-datasets` (the dataset atom — CAS writes, the two byte paths, the canonical CSV, retention and the shared-blob-prefix warning — plus `POST /agent/memories` and the provenance trust rule an embedder holding authoritative state depends on). Order: see **Reading path** above. The authoritative product spec is `docs/product/17-product-spec.md` (entry point: goal, atoms, principles, § map) + `docs/product/00`–`09` (`00-overview` = quick map; component designs; original § numbers preserved). The research trail and executed plan records live beside the spec as dated files in the same folder. |
| `deploy/`, `docker-compose*.yml`, `README-stack.md` | The standalone stack (run it with one command — below). `deploy/gcp/setup.sh` provisions the GCP side (idempotent, safe to re-run). |
| `e2e/`, `examples/` | End-to-end tests — **`e2e/features/` + `playwright.stack.config.ts` is the only rig**, run against the compose stack (the legacy Vite rig under `e2e/tests/` was deleted 2026-08-08). `e2e/experiments/` is the offline comparison rig, run with `./e2e/experiments/run.sh test`. Example host + `examples/web/` (the app shell the stack actually serves — `web/` is a component library with no router). |
| ~~`mock-server/`~~ | **Deleted 2026-08-08** along with its `docker-compose.test.yml` service block (that file stays — `go/systemtest/` needs its `dind` + `registry`). The stack's mock mode is `go/mockmodel` + `go/modelproxy`. |
| `MIGRATION.md` | The standalone-ification + registry-agnostic + GCP roadmap and live status. |

## Run it (standalone stack)

One command brings up API + chat UI + container runtime (Docker required):

```sh
cp .env.example .env          # optionally set ANTHROPIC_API_KEY=sk-ant-...
docker compose up --build     # then open http://localhost:8080
```

- With `ANTHROPIC_API_KEY` → a real agent. Without → a deterministic mock model (works offline).
- **Those two lines assume a `.env` copied from `.env.example`.** A real `.env` (this project's
  actual GCP + Anthropic settings) exits at boot on the GCS key and would run a *billable* agent
  if it didn't — see **"If you have a real `.env`: two traps"** in `README-stack.md` for the
  known-good local/mock invocation and the boot-log line that proves mock mode.
- Services: `web` (UI), `agentd` (API+orchestrator+router+scheduler), `dind` (Docker-in-Docker,
  one container per session), `init-sandbox` (builds the sandbox image into DinD), `postgres`
  (pgvector image — sessions *and* the whole product layer).
- Point sessions at a custom base image with `BASE_IMAGE=<image>` in `.env` (see
  `installations/README.md` and `docs/15-standalone-stack.md`).
- **The product layer is wired only when `DATABASE_URL` is set.** On the sqlite fallback the
  router never routes, schedules never fire, the core MCP server is not mounted and project
  settings do not apply — and nothing fails at use time. Compose always sets it.
- **Sessions hold a running container — and one host port — until deleted or idle**. Since
  2026-07-26 `agentd` runs the archive loop: a session idle for `AGENTKIT_SESSION_IDLE_TIMEOUT`
  (default 30m) is snapshotted and its container released, and the next message restores it —
  reclamation, not deletion (`go/cmd/agentd/gc.go`). The snapshot TTL reaper runs too
  (`AGENTKIT_SNAPSHOT_REAP_INTERVAL`, default 6h).
  The port pool is still the hard ceiling on *concurrent* sessions per host: 100 by default,
  configurable with `AGENTKIT_PORT_RANGE_START`/`_END` (`go/cmd/agentd/portrange.go`, set both or
  neither). At zero free, every further session fails with "host port pool is exhausted". Delete
  sessions you finish with; `./e2e/run-stack-e2e.sh clean` clears leftovers and restarts `agentd`
  (which that command requires — see `README-stack.md`).
- The stack serves a **built** image of `examples/web`, so UI edits are invisible to browser
  tests until `docker compose up -d --build web`.

This is the *standalone demo*, not how you embed the engine as a library — for that, see
`docs/14-host-adapters.md` and `go/examples/standalone/`.

## Build & test the engine

```sh
cd go
go build ./...     # must stay green
go vet ./...
go test ./...      # some suites (systemtest/e2e) need Docker available
```

`agentdb`'s **live-Postgres** cases skip unless `AGENTKIT_TEST_POSTGRES_URL` is set — a green
`go test ./...` therefore does *not* prove the pgvector/jsonb-selector paths. Set it (a throwaway
database, not a shared one: an unmerged migration on a sibling branch has broken other agents'
runs before) when touching `agentdb`.

In-image agent + UI. Both packages define `typecheck` (`tsc --noEmit`) and `test` (`vitest run`);
CI runs `yarn install --frozen-lockfile && yarn typecheck && yarn test` in each. Locally:

```sh
cd sandbox && npm ci && npm test
cd web && npm ci && npm test
```

Lockfiles are inconsistent and it matters:

- `sandbox/` tracks **both** `package-lock.json` and a stale `yarn.lock`; npm≥7 keeps the yarn
  lockfile in sync, so an npm install dirties the tree — `git checkout sandbox/yarn.lock` after.
- `web/` tracks **only** `package-lock.json` (its `yarn.lock` was deleted on this branch), so
  `npm ci` is the right local command — and CI matches: its `web` job runs `npm ci`
  (`.github/workflows/ci.yml:84`, fixed in `59e1ea8`). *This entry previously warned that the job
  would break on merge to `main`; that was true when written and is no longer.*
- `examples/web/` tracks **only** `yarn.lock` (no `package-lock.json`), which is what
  `deploy/web.Dockerfile` uses. Use yarn there, not `npm ci`.

**`web/` is a published package as of O12** (2026-08-24), not a source folder consumed by alias.
`npm run build` is `tsc -p tsconfig.build.json` and really emits `dist/` (`tsconfig.json` still sets
`noEmit` — that one is the editor's and `npm run typecheck`'s). `exports` declares three doors:
`.` (everything), `./pure` (types + pure logic, no React in its graph) and `./components` (the
presentational components, themed by the *host's* `ThemeProvider`). `src/pure.test.ts` enforces
that tier line. **`examples/web` consumes the emitted `dist`**, so `web/` must be built before
`examples/web` is built or typechecked — `deploy/web.Dockerfile` and the `examples-web` CI job both
do it, and a fresh clone that skips it fails with "Cannot find module '@agentkit/chat-ui'".
*This entry previously said the build emitted nothing; that was true until O12.*

`web/scripts/verify-package.sh` is the gate that makes any of that trustworthy: it builds, packs,
installs the tarball into a throwaway app with its own react/MUI/emotion, renders `ArtifactPanel`
and asserts a status dot takes the **consumer's** `success.main`. `npm test` and `npm run typecheck`
cannot see any of those failures — they compile source in place. CI runs it in the `web` job.

## Core concepts (where to look)

- **Runner** (`go/agentkit.go`, `go/runner.go`) — create/message/stream/snapshot a session. The API surface a host calls.
- **ExecutionEnvironment** (`go/execenv/`) — the container seam; Docker + Docker-in-Docker adapters. `docs/02-execution-environment.md`.
- **ImageRegistry** (`go/imageregistry/`) — `EnsurePresent`/`Build`/`Persist`/`Materialize` (pull/build/push/restore). Adapters: `ociregistry` (registry push/pull — pluggable auth via `imageregistry/auth`: `auth.Static` basic or `auth.GCP` ADC tokens), `blobarchive` (snapshot to blob). `Build()` is stubbed (host builds). `docs/03-image-registry.md`, `installations/README.md`.
- **Installations** — layered base images sessions launch from: sandbox harness → `core` → `example` → per-project. `installations/README.md`.
- **Harness** (`sandbox/`) — wraps `@anthropic-ai/claude-agent-sdk`; pluggable per session. `docs/07-in-image-agent.md`.
- **Events / streaming** (`go/events/`) — one canonical SSE event vocabulary; `web/` reduces it. `docs/05-event-streaming.md`.
- **Persistence** (`go/agentdb/`, `go/extension/`) — sessions/events/artifacts; host-implemented store seams (Postgres in prod). `docs/14-host-adapters.md`.
- **Multi-tenancy** — `ContextScope{Customer, Job, ...}` + scoped tokens; auth is delegated to the host. The `customer` claim **is** the product layer's project. `docs/14-host-adapters.md`.

Product layer (all Postgres-only, all in `go/agentdb/` + `go/cmd/agentd/`):

- **Job composition** (`go/compose.go`) — one pure `ComposeJob`: image, prompt concatenation, MCP merge, event-as-first-message. Heavily pinned by test, including the core preamble byte-for-byte. `docs/product/02-workers.md`.
- **Memory** (`go/agentdb/memories.go`) — append-only, K8s labels + selectors, hybrid keyword/vector RRF search (§7.6). One selector parser; images and skills reuse it.
- **Router + scheduler + dispatch gate** (`go/cmd/agentd/router.go`, `scheduler.go`, **`dispatch.go`** — one gate shared by both; capacity is decided in exactly one place).
- **Core MCP server** (`go/cmd/agentd/mcpserver.go`) — JSON-RPC over POST `/mcp`, outside the JWT middleware, authenticated by session token. **Do not write a second one**: adding tools is one `mcp_*.go` file plus one `srv.register(...)` line in `main.go`.
- **Config log** (`go/agentdb/config_events.go`) — every configuration mutation writes through `WithConfigEvent` in the same transaction, and `TestMutationsAreLogged` fails if any store mutation method can bypass it. Emission of `config.changed` is a post-commit hook.

## Rules for working in this repo

1. **Liftability invariant.** The `go/` module must import **nothing** from any host app — CI
   (`.github/workflows/ci.yml`) enforces this. Keep the engine self-contained.
2. **Module path** is `github.com/badcodetv/agent-bob`. Don't reintroduce the old
   module path or any Platinum coupling.
3. **`migration-reference/` is gone** (removed 2026-09-11). The original host pipeline it held is
   no longer in this repo; don't recreate it.
4. **Installation Dockerfiles never set** `CMD`/`ENTRYPOINT`/`EXPOSE`/`HEALTHCHECK`/`WORKDIR` — the
   sandbox base owns those. Installations only add environment, tools, and `/workspace` content.
   (`WORKDIR` was added to that list on 2026-08-13: `core` and `example` both set it, which broke
   the base's then-relative `CMD` and made every session from those images fail its healthcheck.)
5. **Keep `go build ./...` green** and add tests with changes — the codebase is heavily tested
   (follow the existing table-test patterns).
6. **Migration work is phased** (`MIGRATION.md`): standalone-ify → genericize installations →
   registry-agnostic build+push → **GCP (priority)** → automation. Phase 4 is **built** — GCS
   `BlobStore` in `extension/gcsblob`, ADC registry auth in `imageregistry/auth`, selection in
   `cmd/agentd/backends.go` — and **recorded** as verified against the live project on 2026-06-25
   (`webkit-servers` / `europe-west1`, repo `agent-bob`, bucket `webkit-servers-agent-bob`;
   `deploy/gcp/setup.sh` provisions all of it). Nothing under those paths has changed since, so the
   record still describes today's code, but it is testimony rather than a re-runnable check — see
   MIGRATION.md §4a/§4b. Phases 2, 3 and 5 remain, plus two loose ends in Phase 1 (the
   `examples/standalone` DinD run and de-Platinum naming).

## Deeper context

(Order to read them in: **Reading path**, near the top.)

- `docs/01-architecture.md` — what it is and how it fits together.
- `docs/15-standalone-stack.md`, `README-stack.md` — running it.
- `docs/18-workers-memory-events.md` — operating the product layer: project settings, workers,
  triggers, memory and briefings, the core tools, images/skills, the config log, the env vars,
  and the current known limitations.
- `docs/19-embedding.md` — embedding Bob in another application: the three credentials, the
  project map's object form, named sessions, session-mode schedules, embed tokens and the iframe,
  the artifact-proxy pattern, full-content memory reads, the two-bot pattern — and a **Known
  hazards** section (embed-token authority, the CSP path binding, the unauthenticated
  `/agent-proxy/`, the missing `/webapp/…` and `workspace/files` routes) that should be read before
  any third party is pointed at the stack.
- `installations/README.md` — installations / image layering (derived-image tree, overlay model, `imagetree`).
- `docs/product/17-product-spec.md` — the authoritative product spec entry point (goal, atoms,
  principles, non-goals); component designs incl. the work-plan checklist in `docs/product/`.
  **`docs/product/06-work-plan.md`'s Discovered Issues Log is the best record of what was
  actually built and what surprised us** — read it before assuming anything about the layer.
- `MIGRATION.md` — current status + the registry/GCP roadmap.
