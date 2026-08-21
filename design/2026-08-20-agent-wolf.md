# Agent Wolf — Design & Implementation Plan

> **EXECUTION RULES (for agents):** Work ONE ticket at a time, in order unless
> dependencies say otherwise. Only the orchestrator changes ticket Status;
> workers may only append to Notes and the Discovered Issues Log. A ticket's
> checkbox is checked only after its Validation commands have been re-run by
> the orchestrator and pass. Do not expand scope; log surprises in the
> Discovered Issues Log instead.
>
> **You do not need to read anything outside this document plus the files each
> ticket names.** § "Executor orientation" below contains every environment
> fact, command and house rule required. If you find yourself needing wider
> context, that is a defect in this plan — log it.

Status: approved
Revision: 3 (2026-08-20) — incorporates two adversarial reviews and one executability
audit; see the Discovered Issues Log, entries R1–R32.
Relates: `docs/19-embedding.md` (the integration guide written FOR this product),
`design/2026-08-06-embeddable-agent-orange.md`, `docs/18-workers-memory-events.md`,
`docs/06-artifacts.md`, `docs/product/17-product-spec.md`

---

## Context

**Agent Wolf is a platform for stating trading hypotheses and having them deeply researched
and continuously validated.** A user states a thesis — "the petrodollar is ending because of
drone warfare, therefore buy drone-parts suppliers" — and the system interviews them to sharpen
it, locks a falsifiable scoreboard, then researches and scores it every day until a human
confirms or invalidates it.

Three stages:

1. **Interview** — an agent session with deep web research that hones the thesis and produces a
   machine-checkable spec.
2. **Autonomous validation** — a daily job per hypothesis that fetches numbers, writes them to a
   shared dataset, and files evidence.
3. **Verdict** — a deterministic evaluator (not the model) trips an invalidation condition, the
   agent presents its case, and a human confirms or invalidates.

Agent Orange is the runtime. `docs/19-embedding.md` was written for exactly this integration and
names Agent Wolf as its driving use case; its § 8 "two-bot pattern" is the shape this plan
follows. Wolf owns the page, the vocabulary and the user allowlist; Orange owns prompts,
sessions, schedules, memories and (after this plan) datasets.

### The two problems Orange cannot solve today

**1. There is nowhere to put a shared numeric time series.** Orange has memories (append-only
text, whose content crosses the model's context window on every read and write) and session
snapshots (files, but scoped to the one session that wrote them — `docs/06-artifacts.md:37`
dedups artifacts on `session_id + file_path`, and the daily researcher runs in a **fresh**
container every tick). A 200KB series in a memory would cost tokens daily and could be silently
mangled by the model retyping it.

**2. An application embedding Orange cannot write a memory.** Verified:
`go/httpapi/httpapi.go:84` — *"Read-only by design — memories are appended by workers, never by
the UI"*; `go/httpapi/memories.go:43` — *"there is no POST counterpart — and no PUT or DELETE
anywhere"*. The only write surface is `memory_create` on the core MCP server, authenticated by a
**session token** an embedder does not hold. Wolf's entire system of record depends on closing
this gap.

So this plan adds **two** engine features — the **dataset atom** and an **authenticated memory
append route** — and builds Wolf on top of them.

### Decisions taken during design, with their reasons

- **Iframe-only UI reuse.** `web/` is `private: true` with `"noEmit": true`, consumed by
  `examples/web` through a Vite alias into `web/src/index.ts` plus a ten-package dedupe list
  (`examples/web/vite.config.ts:21-38`). It is not installable. Wolf builds its own components
  and shows Orange conversations only through `GET /embed/session/{name}#token=…`.
- **Dataset atom in Orange, not a file store in Wolf.** It matches Orange's existing atom grain
  (name + labels + version + provenance + selector search, exactly like images, skills and
  memories) and is reusable by every future embedder.
- **Worker per hypothesis.** A worker's prompt is what `worker_prompt_write` can rewrite with a
  mandatory rationale; per-hypothesis workers let each hypothesis's research method improve
  independently.
- **Locked scoreboard + open research log.** The interview cannot conclude without a spec naming
  metrics, directions, horizons and invalidation conditions. Those are locked at go-live;
  amendments require a rationale and a human. The researcher may propose, never enact.
- **Conditions are typed predicates evaluated by Wolf, never prose judged by the model.** See
  § "The trust model" — this is the single most important correction in revision 2.
- **Refetchable metrics are REPLACED, not appended.** Dividends re-adjust price history and FRED
  restates macro series months later. An append-only price file diverges from the source within
  weeks and nothing detects it.
- **Agent proposes, human decides**, enforced by **provenance**, not by prompts.
- **One `wolf` project, slug owner labels.** Orange label values are the K8s charset
  (`go/agentdb/labels.go:32`) — no `@`.

---

## Executor orientation

**Read this once before your first ticket. It is everything you need to know about the
environment.**

### The two repositories

| | Path | What it is |
| --- | --- | --- |
| **agent-orange** | `/home/kai/projects/badcode/agent-orange` | The existing engine. Go module `github.com/binocarlos/badcode-agent-orange`, plus a TypeScript in-image agent (`sandbox/`) and a React component library (`web/`). |
| **agent-wolf** | sibling directory `agent-wolf`, created by W1 | The new product. Node/TS API + React UI. Imports **nothing** from agent-orange. |

Ticket ids beginning `O` change agent-orange; ids beginning `W` change agent-wolf; `X1` spans
both.

### House rules that will fail your ticket if broken (agent-orange)

1. **The `go/` module imports nothing from any host app.** CI enforces it.
2. **Module path is `github.com/binocarlos/badcode-agent-orange`.** Never `bayes-price/agentkit`.
3. **`migration-reference/` is reference, not code.** Do not build, import or wire it.
4. **Installation Dockerfiles never set `CMD`, `ENTRYPOINT`, `EXPOSE`, `HEALTHCHECK` or
   `WORKDIR`.** The sandbox base owns all five. `WORKDIR` joined the list on 2026-08-13 after
   setting it broke every session launched from `core` and `example`.
5. **`go build ./...` stays green and changes come with tests**, following the existing
   table-test patterns.

### Commands

```sh
# agent-orange — the gates. All three must pass.
cd go && go build ./... && go vet ./... && go test ./...

# agentdb's live-Postgres cases SKIP unless this is set — see the next section.

# agent-orange — the stack (Docker required)
cp .env.example .env && docker compose up --build       # http://localhost:8080
docker compose up -d --build web                        # after ANY UI edit; the stack serves a BUILT image
./e2e/run-stack-e2e.sh clean                            # clear leftover sessions, restart agentd

# agent-wolf
cd api && yarn typecheck && yarn test
cd web && yarn typecheck && yarn test
```

### The throwaway Postgres — copy these four commands

`agentdb`'s live cases skip silently without `AGENTKIT_TEST_POSTGRES_URL`, so a green
`go test ./...` does **not** prove the jsonb-selector, CAS or pgvector paths. O2, O3, O7 and O11
all depend on those cases actually running.

CLAUDE.md requires a **throwaway** database, not a shared one — an unmerged migration on a sibling
branch has broken other agents' runs before. The commands below give each run a disposable
instance whose storage is a tmpfs, so removing the container erases everything. They were run and
verified on 2026-08-20: with this instance running and the variable exported, the **entire
`go test ./agentdb/... -count=1` suite passes with ZERO skipped tests** — which is the whole point,
since without the variable the live cases skip silently and a green run proves much less than it
appears to.

**1. Start it** (port 5433, not 5432, so it cannot collide with a local Postgres or the compose
stack; the image is the same `pgvector/pgvector:pg16` the stack uses, so the semantic-search leg is
available rather than silently absent):

```sh
docker run -d --name agentkit-testpg \
  -e POSTGRES_USER=throwaway -e POSTGRES_PASSWORD=throwaway -e POSTGRES_DB=agentkit_test \
  -p 5433:5432 --tmpfs /var/lib/postgresql/data:rw,size=1g \
  pgvector/pgvector:pg16
```

**2. Wait for it, and enable pgvector** (the migrations attempt `CREATE EXTENSION vector`
themselves and swallow failure, so doing it here as superuser is what guarantees the vector column
exists rather than being quietly skipped):

```sh
until docker exec agentkit-testpg pg_isready -U throwaway -d agentkit_test >/dev/null 2>&1; do sleep 1; done
docker exec agentkit-testpg psql -U throwaway -d agentkit_test -c 'CREATE EXTENSION IF NOT EXISTS vector;'
```

**3. Run the live tests:**

```sh
export AGENTKIT_TEST_POSTGRES_URL='postgres://throwaway:throwaway@localhost:5433/agentkit_test?sslmode=disable'
cd go && go test ./agentdb/... -count=1
```

Confirm the live cases really ran rather than skipped — the names contain `Live`:

```sh
go test ./agentdb/... -run 'Live' -count=1 -v | grep -E '^--- (PASS|SKIP|FAIL)'
```

A `SKIP` here means the variable did not reach the test, and any ticket claiming a green live run
is unproven.

**4. Reset between runs, or tear down.** A half-applied migration from a failed ticket will poison
every later run, and the fix is to throw the instance away — that is the whole point of it being
throwaway:

```sh
docker rm -f agentkit-testpg      # then repeat step 1
```

### Environment facts you must not rediscover the hard way

- **The product layer is wired only when `DATABASE_URL` is set.** On the sqlite fallback the
  router never routes, schedules never fire, `/mcp` is not mounted, and **nothing fails at use
  time** — it silently does nothing. Compose always sets it. Everything in this plan is
  Postgres-only.
- **Nothing in this plan works in dev-open mode.** Configuring a project API key is exactly what
  turns dev-open off (`go/cmd/agentd/auth.go:63`).
- **A real `.env` for this project exits at boot on the GCS key and would run a billable agent.**
  Use the mock-mode invocation in `README-stack.md` § "If you have a real `.env`: two traps", and
  assert the boot-log line that proves mock mode.
- **Sessions hold a container and a host port until deleted or idle for 30 minutes.** The port
  pool is 100 by default. At zero free, every create fails with "host port pool is exhausted".
  Delete sessions you finish with.
- **Lockfiles are inconsistent.** `sandbox/` tracks both `package-lock.json` and a stale
  `yarn.lock` — run `git checkout sandbox/yarn.lock` after any npm install there. `web/` tracks
  only `package-lock.json` (`npm ci`). `examples/web/` tracks only `yarn.lock` (use yarn).
- **Timestamp units differ across the product surface and this is deliberate.** `memories`,
  `config_events` and (new) `datasets` are unix **milliseconds**; the `agent_*` tables are unix
  **seconds**. Do not unify them. Encode the unit in every type you write.
- **Label values are the Kubernetes charset**, `^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`,
  ≤63 chars, ≤32 labels per object (`go/agentdb/labels.go:22-33`). No `@`, no `+`, no `/`,
  no spaces. This constrains dataset names, memory labels and metric slugs alike.
- **Selectors are Kubernetes selector semantics exactly**: `k=v`, `k!=v`, `k in (a,b)`,
  `exists k`, `!k`, comma means AND. **No OR and no nesting** — run two searches.

### Pinned technology choices — do not substitute

These are named here, once, so that fourteen tickets do not each pick differently.

| Concern | Choice | Notes |
| --- | --- | --- |
| Wolf API HTTP framework | **Express 5** | Plain middleware; no DI framework, no decorators |
| MCP server implementation | **`@modelcontextprotocol/sdk`**, HTTP transport | Do not hand-roll JSON-RPC |
| Test runner | **vitest 4** in both `api/` and `web/` | Matches Orange's JS packages |
| Vite (in `web/` only) | **6.x** | vitest 4's peer range is vite 6+, so `web/` **deliberately diverges** from `examples/web`'s vite 5. Owner decision 2026-08-21, **R40**. React/MUI still match `examples/web` — those are the versions the visual language depends on; vite is a build tool. Do NOT pin vite 5 here to "match", or `vite.config.ts` gets evaluated by two different resolvers |
| HTTP mocking in tests | **`undici` `MockAgent`** | No `msw`, no `nock`, no live network in any unit test |
| React | **18.3.1**, MUI **6** | Same versions as `examples/web` so the visual language matches |
| Charts | **Recharts** | One library; no d3 by hand |
| Dates | **native `Date` + explicit UTC helpers** | No moment, no dayjs — the units are the bug surface, keep them visible |
| Validation | **zod** | Spec and condition schemas, and every route body |
| Logging | **`pino`**, JSON to stdout | One line per request; never log a credential or a `download_url` |

### Shared error taxonomy

Defined once in `api/src/errors.ts` (created by W1, used by every later ticket). Do not invent a
second one:

```ts
export type WolfErrorKind =
  | "not_found"        // the thing does not exist
  | "unavailable"      // an upstream is down or timed out — RETRYABLE
  | "invalid"          // caller error; carries field-level details
  | "conflict"         // CAS or state-machine rejection
  | "forbidden"        // authenticated but not allowed
  | "misconfigured"     // an env var or an Orange-side setting is wrong; names the variable
  | "internal";        // WE have a bug — an unhandled throw. NOT retryable

export class WolfError extends Error {
  kind: WolfErrorKind; status: number; details?: unknown; upstreamBody?: string;
}
```

`not_found` and `unavailable` **must** be distinguishable: W10's poller treats a 404 dataset as
"not written yet" and an outage as a reason to skip without penalty, and conflating them makes a
provider outage look like a missing metric.

**Every catch-all handler maps an unrecognised throw to `internal`, never to `unavailable`.**
`unavailable` is the one retryable kind, so classifying a server bug as `unavailable` makes W10's
poller retry a crashing endpoint on every tick forever (owner decision 2026-08-21, **R39**).
`internal` maps to HTTP **500**, and its `message` is **not** passed through to the client — a
stack trace or a credential in a thrown error must not reach a response body. Log the real error
server-side with `pino`; return a fixed string and, where one exists, a correlation id.

### Parallelism and file ownership

Tickets that touch the same file **must not run concurrently**, whatever the dependency graph
allows:

| Shared file | Tickets | Rule |
| --- | --- | --- |
| `go/agentdb/datasets.go`, `datasets_test.go` | O1, O2, O3 | Strictly serial in that order |
| `go/agentdb/datasets_live_test.go` | O2, O3 | Strictly serial |
| `go/httpapi/httpapi.go` | O5, O7, O11 | Strictly serial; each adds a route constant and a registration line to the same two blocks |
| `go/httpapi/memories.go` | O7, O11 | Strictly serial |
| `go/cmd/agentd/main.go` | O6b, O8 | Strictly serial |
| `api/src/routes/hypotheses.ts` | W8, W9 | Strictly serial |
| `api/src/hypothesis/store.ts` | W5, W10 | Strictly serial |

Safe to run fully in parallel: **O1 ‖ O7 ‖ W1**, then **O4 ‖ W3 ‖ W6** once their deps land.

### Git conventions

One branch per ticket, named `<ticket-id>-<kebab-summary>` (e.g. `o6-dataset-mcp-tools`), branched
from `main`. One commit per ticket unless the work genuinely splits; the message's first line is
`<ticket-id>: <what changed>`. Do not push or open a PR unless the orchestrator asks. Never commit
to `main` directly.

### Vocabulary

| Term | Meaning here |
| --- | --- |
| **Project** | Orange's tenancy boundary. Wolf uses exactly one, id `wolf`. The `customer` claim in a token *is* the project. |
| **Session** | A container running the agent harness, with a conversation. Named, per hypothesis. |
| **Worker** | A row of prompt + tools + wiring. Woken by an event or a schedule; each firing gets a fresh container. |
| **Memory** | An append-only, labelled, project-scoped text row. No update, no delete — "changing" one means appending a newer one. |
| **Dataset** | **New in this plan.** An append-only, labelled, project-scoped, *versioned blob*. |
| **Delivery** | One dispatched job: the record that a worker was woken. |
| **Tick** | One firing of a hypothesis's daily schedule. |

---

## Architecture

```
  ┌─────────────────── agent-wolf (new repo, TypeScript) ────────────────┐
  │                                                                      │
  │  wolf-web (React + MUI, own components, imports no Orange code)      │
  │    │  /api/*              <iframe src=orange/embed/session/…#token>  │
  │    ▼                                     │                           │
  │  wolf-api (Node/TS)                      │                           │
  │    · holds WOLF_API_KEY (never leaves the server)                    │
  │    · mints embed tokens          · hypothesis lifecycle              │
  │    · proxies dataset bytes       · CONDITION EVALUATOR (deterministic)│
  │    · evaluation poller           · provider connectors + cache       │
  │    · /mcp — market-data tools ◄──┼───────────────┐                   │
  │  installations/wolf/Dockerfile   │               │ agent calls tools,│
  │    core + python3 + pandas + duckdb              │ curls signed URLs │
  └───────────┬──────────────────────┼───────────────┼───────────────────┘
              │ X-API-Key            │               │
              ▼                      ▼               │
  ┌──────────────── agent-orange (existing) ─────────┼───────────────────┐
  │  agentd — HTTP API — memories (+ NEW append route) — DATASETS (new) ─┘
  │           router / scheduler — workers — sessions — BlobStore        │
  │  DinD: one container per session, launched from the Wolf image       │
  └──────────────────────────────────────────────────────────────────────┘
```

### The dataset atom

A **dataset** is a project-scoped, named, versioned, labelled blob. Every write creates a new
immutable version; "current" is the highest version for `(project, name)`. This preserves
Orange's append-only posture while giving mutable-file ergonomics.

**Bytes never cross the model's context window, in either direction:**

| Direction | Mechanism | Why this one |
| --- | --- | --- |
| **Write** | `dataset_put(name, path, if_version)` names a file under `/workspace`; agentd pulls it with `Exec` + `cat` | This is exactly what `onArtifactRegistered` already does (`go/runner.go:2733-2736`, "pull the registered file from the running instance workspace via Exec+cat"). `Exec` is the only file seam on the environment interface (`go/execenv/execenv.go:54`). |
| **Read** | `dataset_get(name)` returns a short-lived scoped download URL; the agent `curl`s it to a file | Containers already reach agentd over `AGENTKIT_SELF_URL`. No push-into-container plumbing has to be invented. |

**Compare-and-swap is mandatory on write.** `if_version` must match the current version, or `0`
to mean "must not exist". Without it, two sessions that both read v5 and both write would
silently lose a day of data.

> ⚠️ **The bytes stay out of context; the CREDENTIAL does not.** `dataset_get`'s `download_url`
> carries a bearer token, is returned as a tool result, and therefore enters the model context,
> the persisted transcript, the SSE stream, and every subscriber of `worker.finished` (whose text
> is the full rendered transcript). It is bounded by a 300s TTL and a single-dataset scope. Nor
> does anything stop a model from `df.head()`-ing a CSV into its own context. The guarantee is
> "the plan never *requires* bytes in context", not "bytes can never appear there". Both caveats
> are stated in O10's documentation and in `dataset_get`'s own tool description.

**Rejected alternatives:**

- *Cross-session artifact hand-off* — works today with zero engine changes (session-mode
  schedules exist, workspaces survive archive/restore, artifacts upsert by path), but caps out on
  unbounded transcript growth in a session resumed daily for a year, and enforces a single writer.
- *A blob store in Wolf* — keeps machinery out of Orange, but makes Wolf a stateful service and
  produces nothing reusable for the next embedder.
- *MCP tools returning bytes* — defeats the purpose; an MCP result is model context.
- *A cumulative-snapshot memory* — the whole series would cross the model's context daily.

---

## The trust model

**This section is load-bearing. Two tickets exist only because of it. Read it before W4, W5 or
W10.**

Orange's memory is a genuine shared bus: project-scoped, append-only, **no per-worker permissions
and no origin check** (`docs/19-embedding.md` § 8). A chat session with core tools may do
"everything a worker may do", and labels are chosen entirely by the caller. `latest_per=name`
returns the newest row per name **regardless of who wrote it**.

Therefore, if Wolf derived a hypothesis's status from "the newest memory labelled
`kind=hypothesis, name=<id>`", then:

- the researcher could append `status=confirmed` and end its own trial;
- the interviewer session could do the same;
- **a prompt-injected web page read during "deep research" could do it** — and deep web research
  on adversarial content is this product's core activity.

### The rule

> **Authoritative hypothesis state is only ever read from memories with EMPTY PROVENANCE.**
> A memory is *trusted* if and only if `created_by_worker == ""` **and**
> `created_by_session == ""`.

Provenance is stamped by the server from the caller's credential and cannot be supplied by the
caller. Anything written from inside a container carries either a worker name or a session id,
and is therefore **advisory**.

⚠️ **Empty provenance is necessary but not sufficient.** Two paths produce it: the new
`POST /agent/memories` route (O7), and `agentdb.ApplyTopology`, which writes memory seeds with no
provenance (`go/agentdb/topology_apply.go`, asserted by `go/topology/topology_test.go:242-243`)
and is reachable at `POST /agent/topologies/apply` by any API-class credential. Today no shipped
topology can seed a `kind=hypothesis` label — the one that seeds memory uses fixed
`{kind:registry, name:label-registry}` labels and the request body cannot supply its own — so
there is no live forgery path. But the invariant Wolf relies on is therefore:

> **A memory is trusted iff its provenance is empty AND its labels are one of the five kinds in
> Wolf's own vocabulary AND `name` matches an existing `hyp-<id>` session.** The last clause is
> the one that cannot be forged from inside a container: creating a named session requires an API
> key or a console JWT.

Note also that an **embed token is API-class** and its `scope` claim is enforced only on
session-by-id routes (hazard H1), so it is not confined on `POST /agent/topologies/apply`. Wolf
must never hand an embed token to anything it would not trust with the project.

| Memory kind | Written by | Trusted? | Consequence |
| --- | --- | --- | --- |
| `hypothesis` (state + status) | Wolf API only | **Yes** | The board renders it |
| `hypothesis-spec` | Wolf API only, at go-live | **Yes** | The evaluator reads it |
| `verdict` | Wolf API only, human-initiated | **Yes** | Terminal |
| `research-note` | the researcher agent | No | Displayed as evidence, never as state |
| `spec-amendment` | the researcher agent (proposal) | No | Rendered as a *proposal*; a human accepting it makes Wolf write the trusted spec |

### Reading the board without giving up the one-request fast path

`latest_per` cannot filter on provenance, but **search results DO carry provenance** —
`MemorySearchResult` includes `created_by_worker` and `created_by_session`
(`go/agentdb/memories.go:113-121`). So:

1. One request: `GET /agent/memories?selector=kind%3Dhypothesis&latest_per=name&limit=100`.
2. Any row whose provenance is empty is authoritative — render it. **This is the normal case and
   costs exactly one request.**
3. Any row whose provenance is NOT empty is an anomaly: something inside a container wrote a
   `kind=hypothesis` memory. For those ids only, issue
   `GET /agent/memories?selector=kind%3Dhypothesis,name%3D<id>&limit=50`, take the newest trusted
   row, and render the hypothesis with a **tamper warning** naming the writer.

The forged row never becomes state, and the anomaly is surfaced rather than swallowed.

### Retraction — the back door, and the one that matters more

Appending a *newer* row is the obvious attack and the one the rule above defeats. **Retraction is
strictly more powerful and defeats the rule as originally written.**

`notRetractedSQL` (`go/agentdb/memories.go:284-288`) hides any row `X` for which *any* memory in
the project carries the label `retracts=<X.id>`. It correlates on the **retracted row's id and
project only — it never checks who wrote the retraction.** It is applied to exactly the two paths
Wolf's board depends on: `NewestMemory` (`:353`) and `SearchMemories` (`:407`).

`retracts` is an ordinary label, so any session with core MCP tools can call
`memory_create(labels: {retracts: "<trusted-id>"})`, and the trusted memory's uuid is discoverable
because `memory_search` results carry `ID`. A prompt-injected researcher therefore does not need
to forge a status — **it deletes the real one from every search path**, and the hypothesis either
silently vanishes from the board or falls through to whatever untrusted row remains.

Three facts make this fixable:

1. **`GET /agent/memories/{id}` is deliberately NOT retraction-filtered.** The comment at
   `go/agentdb/memories.go:281-283` says so in as many words: *"fetching a specific id is an
   explicit request for that row, and being able to read what was withdrawn — and the retraction
   that withdrew it — is the point of not deleting anything."* A retracted memory is readable if
   you know its id.
2. **The set of hypotheses is knowable without memory at all.** Every hypothesis has a named
   session `hyp-<id>`, and creating one requires an API key or console JWT. `GET /agent/sessions?user_email=*`
   is therefore an unforgeable index.
3. A retraction is itself a memory, carrying its own provenance.

**The rule, restated:**

> Wolf's authoritative index of hypotheses is the **session list**, not memory. For each
> hypothesis in that index, Wolf resolves state from trusted memories read with retractions
> **visible** (ticket O11's `include_retracted=1`), and **ignores any retraction whose own
> provenance is non-empty** — an untrusted actor cannot withdraw server-written state. A
> hypothesis present in the session index whose state row is retracted by an untrusted retractor
> is rendered from the retracted row, with a tamper warning naming the retractor.

A retraction written by Wolf itself (empty provenance) is honoured normally — that is how Wolf
corrects its own mistakes.

### Where the falsifiability actually lives

The locked scoreboard is enforced by three things, none of which is a prompt:

1. **The spec is a trusted memory** — the researcher cannot rewrite it, only propose.
2. **Conditions are typed predicates evaluated in Wolf** (§ "Condition semantics") over dataset
   CSV that Wolf parses itself. The model does not decide whether a condition tripped.
3. **The researcher's system prompt is composed as `locked preamble + mutable method body`**, and
   the critic may only rewrite the method body (W12). The preamble carries the spec and the
   scoring contract verbatim.

### Derived metrics are the weak point, and are pinned

A `derived` metric has no external source, so nothing can independently recompute it — its
methodology lives only in a prompt the critic rewrites weekly. Left alone, "improving the research
method" silently changes what the appended number *means*, and the invalidation condition then
tests a spliced series.

**Therefore a `derived` metric MUST carry a `method` object in the locked spec** (formula,
constituents, source series, units), that object is composed into the locked preamble verbatim,
and changing it requires a human-accepted amendment exactly like changing a threshold.

---

## Local topology and networking

**This is the fact that breaks a naive deployment.** In the compose stack, `agentd` shares DinD's
network namespace (`docker-compose.yml:85`, `network_mode: "service:dind"`). Consequences,
verified:

- Nested session containers reach `agentd` at the DinD bridge gateway, **`http://172.17.0.1:8099`**
  (`docker-compose.yml:98`, `AGENTKIT_SELF_URL`). This is **not** a browser-reachable URL.
- Other compose services reach `agentd` as **`http://dind:8099`** — which is why nginx proxies to
  exactly that (`deploy/web.nginx.conf:43,55,65,69`).
- Only the `web` service publishes a host port (`docker-compose.yml:209-210`).
- Nested containers cannot resolve compose service names: nested Docker does not inherit the
  compose DNS resolver.

**So a `wolf-api` sitting on the ordinary compose network is unreachable from session containers**,
and both `series_fetch` and Wolf's download URLs would fail. The topology below is mandatory, not
advisory:

```
   browser
      │  http://localhost:${WOLF_WEB_PORT:-8081}
      ▼
   wolf-web  (nginx; on network agent-orange_default)
      │  proxy_pass http://dind:8100   ← wolf-api shares dind's netns
      ▼
   wolf-api  network_mode: "container:${ORANGE_DIND_CONTAINER}"
      │        · reaches agentd at  http://localhost:8099   (same netns!)
      │        · reachable from session containers at  http://172.17.0.1:8100
      ▼
   agentd ──► DinD ──► session containers
                          curl http://172.17.0.1:8100/mcp        (market data)
                          curl http://172.17.0.1:8099/agent/…    (datasets)
```

Two rules follow, and both are ticket acceptance criteria:

- **`dataset_get`'s `download_url` MUST be built on `AGENTKIT_SELF_URL`, never on
  `AGENTKIT_PUBLIC_BASE_URL`.** They are deliberately different values
  (`go/cmd/agentd/permalink.go` documents the split); the public base is for humans clicking
  permalinks and is unreachable from a nested container.
- **`WOLF_MCP_URL`, the value written into the project's MCP config, is
  `http://<dind-gateway>:${WOLF_API_PORT}/mcp` in the compose stack.** In a real deployment both
  services are ordinary hosts and it is a normal URL; the variable exists so nothing hard-codes
  either form.

**`172.17.0.1` is a default, not a constant — discover it (owner decision 2026-08-21, R43).**
It is DinD's inner `docker0` gateway, and Docker allocates `172.17.0.0/16` only if that subnet is
free; W1's verifier reproduced it becoming `172.18.0.1`. Because session containers reach wolf-api
at that address, a shift makes every MCP tool call from inside a session time out with no obvious
cause. Therefore:

- `api/src/config.ts` resolves the gateway **at boot**: read the default route from inside DinD's
  netns (`ip route show default`, or `/proc/net/route`'s destination-`00000000` entry — wolf-api
  shares that namespace, so this reads DinD's own table). Fall back to the literal `172.17.0.1`
  when discovery fails, and `pino`-log which of the two was used, at info, once.
- If `WOLF_MCP_URL` is set explicitly in the environment it **wins** — discovery only fills the
  default. A real deployment sets it and never runs the probe.
- The same resolved value is what W12's bootstrap writes into the project's MCP config, so
  discovery happens in exactly one place.

---

## Hypothesis lifecycle

```
                     ┌──────────────────────────────────────────┐
                     │                                          │
  draft ──go-live──▶ live ──evaluator trips a condition──▶ challenged ──human──▶ confirmed
    │                 │                                     │                     invalidated
    │                 └──horizon_days elapsed──────────────▶ │                          ▲
    │                                                        └──human accepts amendment─┘
    └──human──▶ archived ◄────────────────human, from any non-terminal state─────────────┘
```

| State | Meaning | Who may write it |
| --- | --- | --- |
| `draft` | Interview in progress; no worker, no schedule, no datasets | Wolf, on create |
| `live` | Scoreboard locked; daily research running | Wolf, on human go-live |
| `challenged` | A condition tripped **or** the horizon elapsed; awaiting a human | Wolf, by the evaluator poller |
| `confirmed` | Terminal. The thesis held | Wolf, human-initiated only |
| `invalidated` | Terminal. The thesis failed | Wolf, human-initiated only |
| `archived` | Retired without a verdict | Wolf, human-initiated only |

**Transitions are serialised per hypothesis inside wolf-api** (an in-process async mutex keyed by
hypothesis id), and the current state is re-read from Orange *inside* the critical section. There
is no compare-and-swap on memories, so two concurrent writers would otherwise both append and the
later one would silently win. This is sufficient **only while wolf-api runs as a single
instance**, which is a stated constraint of this plan; running two replicas requires a real lock
and is out of scope.

### Orange atoms per hypothesis

| Atom | Name | Created | Torn down |
| --- | --- | --- | --- |
| Session (chat surface) | `hyp-<id>` | at draft, with `worker: interviewer` | third |
| Worker (daily researcher) | `researcher-<id>` | **at go-live**, not at draft | second |
| Schedule (daily, worker-mode) | — | at go-live | **first** |
| Datasets | `<id>-<metric-slug>` | first tick | **never** |

**Teardown order is load-bearing.** Disabling a worker does not stop its schedule: the scheduler
only checks that the worker *exists* (`go/cmd/agentd/scheduler.go:363-371`), and dispatch then
fails the delivery with `worker "x" is disabled` (`go/cmd/agentd/dispatch.go:246-248`) — one
failed job row per day until the five-consecutive-failure streak retires the schedule
(`go/cmd/agentd/scheduler.go:768-790`). Deleting the schedule first avoids all of it.

**Deleting the schedule is not sufficient on its own.** A delivery already queued behind the
capacity gate dispatches on a later tick, after the worker is gone. Teardown therefore also
**drains pending deliveries for that worker** before deleting it (W9), and a tick session still
in flight is allowed to finish — anything it writes is untrusted by construction and cannot
change state.

Two shared project-level workers exist alongside the per-hypothesis ones:

- **`interviewer`** — the prompt behind every hypothesis chat session.
- **`critic`** — a weekly schedule that reads recent research notes and rewrites the **method
  body** of `researcher-<id>` prompts through `worker_prompt_write`. A schedule rather than a
  `worker.finished` subscription: envelope filters are equality-only, so a per-worker subscription
  would need one row per hypothesis, and a filterless one would fire on every worker in the
  project.

---

## Condition semantics

**The single most important specification in this document.** Revision 1 had conditions as free
text (`"drawdown_pct > 25 sustained 30d"`), which nothing could evaluate — the only reader would
have been the researcher model, judging whether its own trial should end. Conditions are now
typed and evaluated deterministically by wolf-api.

### The condition object

```jsonc
{
  "id": "inv-1",                       // unique within the spec, kebab
  "metric": "drone-suppliers-basket",  // must name a metric slug in the same spec
  "stat": "drawdown_pct",              // level | change_abs | change_pct | drawdown_pct | ratio_to
  "ratio_metric": null,                // required iff stat == "ratio_to"; another slug
  "ratio_lookback_days": null,         // required iff stat == "ratio_to"; [1, 400]
  "reference": "peak_since_live",      // peak_since_live | value_at_live | trailing_n_days
  "reference_days": null,              // required iff reference == "trailing_n_days"
  "op": "gt",                          // gt | gte | lt | lte
  "threshold": 25,
  "sustained_days": 30,                // 0 means "a single observation is enough"
  "meaning": "the basket is not responding to the thesis"   // prose, for humans only
}
```

### Statistic definitions — implement exactly these

Let `v` be an observation's value, `t` its timestamp, `L` the go-live timestamp, and `R` the
reference value.

| `stat` | Reference `R` | Computed statistic |
| --- | --- | --- |
| `level` | not used | `v` |
| `change_abs` | per `reference` | `v - R` |
| `change_pct` | per `reference` | `100 * (v - R) / R` |
| `drawdown_pct` | per `reference` | `100 * (R - v) / R` |
| `ratio_to` | not used | `v / v_other`, where `v_other` is `ratio_metric`'s value at the nearest timestamp **at or before** `t`, within `ratio_lookback_days`; no such point ⇒ that observation is skipped |

**`change_abs` exists because percentages are undefined on series that cross zero.** Real rates,
net exports and trade balances all go negative, and this product's premise is FRED macro data.
Use `change_abs` or `level` for any series that can be ≤ 0.

| `reference` | `R` is |
| --- | --- |
| `value_at_live` | the metric's value at the first observation with `t >= L` |
| `peak_since_live` | the maximum value over `[L, t]` (a running peak, recomputed per observation) |
| `trailing_n_days` | the mean value over `[t - reference_days, t)` |

**Non-positive references — the rule, stated once and applying to every statistic that divides by
`R` (`change_pct` and `drawdown_pct`):**

- `R <= 0` ⇒ that observation is **skipped**. Not zero, not infinity, not an error — skipped.
  Dividing by a negative reference silently inverts the sign of the comparison, which is how a
  condition trips backwards.
- If **every** observation is skipped for this reason, the condition is `indeterminate` with
  reason `non_positive_reference`, and the UI shows that reason. It is a spec mistake — a
  percentage statistic on a zero-crossing series — and a human should fix it with an amendment.
- `v_other == 0` for `ratio_to` ⇒ that observation is skipped, same rule.

**Mixed-frequency `ratio_to`.** `ratio_lookback_days` is required and settable precisely because a
daily metric measured against a monthly FRED series resolves on only a handful of days per month
at a 7-day lookback, which combined with the coverage floor below makes such a condition
permanently `indeterminate`. Set it to at least twice the coarser series' period — the interviewer
prompt says so, and W3 rejects a `ratio_to` condition that omits it.

### Evaluation

For each condition, over observations with `t >= L`:

1. Compute the statistic at every observation.
2. `holds(obs)` is `op(stat, threshold)`.
3. If `sustained_days == 0`: the condition **trips** if `holds` at the most recent observation
   **and** that observation is fresher than `staleness_days` (spec-level, default `5`). An
   observation older than that yields `indeterminate` with reason `stale_series`.
4. Otherwise let `W` be the window `(nowMs - sustained_days, nowMs]` — **anchored on evaluation
   time, not on the last observation.** The condition **trips** iff `holds` is true at **every**
   observation in `W` **and** `W` contains at least `ceil(sustained_days * 0.6)` observations.

   > **This is the difference between two products.** Anchoring on `t_last` instead would mean a
   > feed that died sixty days ago mid-trip keeps tripping forever on ancient data, and its
   > coverage floor would still be satisfied because it is measured against `t_last`. Anchoring on
   > `nowMs` makes a dead feed's recent window empty, so it goes `indeterminate` and surfaces as a
   > problem for a human. `nowMs` is the fourth argument of W4's `evaluate` for exactly this
   > reason and for no other.
5. **Insufficient coverage ⇒ `indeterminate`, never `tripped`.** Weekends, holidays and provider
   outages must not be able to trip a condition by absence of contrary evidence.
6. Any metric with no observations at all ⇒ `indeterminate`.

Result per condition: `{ id, state: "tripped"|"holding"|"indeterminate", value, window_start,
window_end, observations_in_window, evaluated_at }`.

**A hypothesis moves `live → challenged` when any condition is `tripped`.** `indeterminate`
never trips anything; three consecutive evaluations at `indeterminate` for the same condition
raise a human-attention flag on the hypothesis instead (a scoreboard nobody can compute is a
problem to surface, not to ignore).

### The support score — what `weight` is for

Revision 1 validated `weight` and then never used it. It now has exactly one consumer.

For each metric, `direction_score s_i`:

- Let `c = change_pct` of the metric from `value_at_live` to its most recent observation.
- `flat_band_pct` defaults to `2.0` and is settable per spec.
- `expected up`: `s = +1 if c > flat_band else -1 if c < -flat_band else 0`
- `expected down`: `s = -1 if c > flat_band else +1 if c < -flat_band else 0`
- `expected flat`: `s = +1 if |c| <= flat_band else -1`
- No observations ⇒ `s = 0`.

`support_score = Σ (weight_i * s_i)`, in `[-1, 1]`. It is displayed on the board and on the
detail page. **It never trips anything** — only conditions do. It is a summary for humans, and
saying so in the UI is a W14 acceptance criterion.

### Where the board's numbers come from

`support_score` and the condition summary are computed by the poller from datasets. They are not
on the hypothesis memory, and they cannot be: a `live` hypothesis's newest trusted row is the
go-live row, and the board read returns **labels plus a 500-byte snippet, no content**. Two
non-answers, rejected explicitly:

- *Put the score in a label.* `support_score=-0.42` is an **illegal label value** — values must
  begin alphanumeric. And a poller that re-writes the row every 5 minutes appends ~14k memories a
  day, drowning `latest_per`.
- *Read each hypothesis's dataset from the board route.* That is N×M dataset fetches to paint one
  page.

**The answer: the poller appends a `kind=evaluation` trusted memory whose FIRST LINE is a compact
summary**, and the board reads it with a second `latest_per` call. Two requests paint the board,
both O(1) in the number of hypotheses.

```
GET /agent/memories?selector=kind%3Dhypothesis&latest_per=name    → state
GET /agent/memories?selector=kind%3Devaluation&latest_per=name    → score + condition summary
```

Line 1 format, which must fit inside 500 bytes and is parsed by Wolf:

```
score=-0.42 tripped=1 holding=3 indeterminate=0 evaluated=2026-08-20T06:05:00Z
```

The rest of the content is the full evaluation snapshot as JSON. **The poller appends a new
`kind=evaluation` memory only when that summary line differs from the last one, or when more than
20 hours have passed** — so a quiet hypothesis produces one row a day, not 288.

### Snapshotting, because the reaper will delete the evidence

Datasets are replaced whole every tick and O3's reaper keeps the newest 30 versions — roughly 30
days. A hypothesis invalidated on day 90 would, by day 121, have no surviving copy of the data the
verdict was made on. Worse, a provider restatement can make a tripped condition silently *un*-trip
with no record it ever fired.

**Therefore: whenever the evaluator moves a hypothesis to `challenged`, and whenever a human
records a verdict, Wolf writes the full evaluation result — every condition's state, value and
window, plus the observations inside each window — into the trusted memory it appends.** The
memory is the permanent record; the dataset is working storage.

### The shrink guard

`replace-whole` has a failure mode CAS cannot catch, because it is the same writer: a provider
outage returning a truncated history replaces a year of data with a stub. `dataset_put` therefore
refuses a replacement whose row count is below 50% of the current version's unless the caller
passes `allow_shrink: true`, and the researcher's prompt forbids passing it without filing a
research note that says why.

---

## File Structure

### agent-orange

| File | Action | Purpose |
| --- | --- | --- |
| `go/agentdb/migrations.go` | Modify | Append `045_datasets` (highest today is `044_agent_sessions_launch_image`, at `:1045`) |
| `go/agentdb/datasets.go` | Create | `Dataset` type + store: create-with-CAS, current, get, list, versions, reap, orphan sweep |
| `go/agentdb/datasets_test.go` | Create | Table tests |
| `go/agentdb/datasets_live_test.go` | Create | jsonb + CAS + concurrency; skip unless `AGENTKIT_TEST_POSTGRES_URL` |
| `go/httpapi/datasets.go` | Create | Four read routes |
| `go/httpapi/datasets_test.go` | Create | Route tests |
| `go/httpapi/memories.go` | Modify | Add `CreateMemory` handler (O7); add `include_retracted` (O11) |
| `go/agentdb/memories.go` | Modify | `MemoryQuery.IncludeRetracted` + `retracted_by` (O11) |
| `go/httpapi/memories_test.go` | Modify | Append-route tests incl. the provenance invariant |
| `go/httpapi/httpapi.go` | Modify | Route constants (`:359-389`), registration (`:463-475`), `MemoryStore` gains `CreateMemory` |
| `go/cmd/agentd/datasettoken.go` | Create | Short-TTL `scope: "dataset:<id>"` tokens, modelled on `embedtoken.go` |
| `go/cmd/agentd/mcp_datasets.go` | Create | `dataset_list` / `dataset_get` / `dataset_put` |
| `go/cmd/agentd/mcp_datasets_test.go` | Create | Tool contract tests |
| `go/cmd/agentd/main.go` | Modify | One `mcpSrv.register(...)` line beside `:572-573`; reaper wiring |
| `go/cmd/agentd/gc.go` | Modify | Dataset version reaper interval + boot log |
| `go/systemtest/datasets_test.go` | Create | Real-container round trip |
| `docker-compose.yml` | Modify | `WOLF_API_KEY`, `WOLF_MCP_TOKEN`, `AGENTKIT_MCP_ENV`, reaper interval |
| `.env.example` | Modify | Document all of the above plus a worked `AGENTKIT_PROJECT_MAP` for `wolf` |
| `docs/20-datasets.md` | Create | The atom, its contract, its hazards |
| `docs/19-embedding.md` | Modify | Route summary gains datasets + the memory append route |
| `CLAUDE.md` | Modify | Repo-map/status line |

### agent-wolf

| Path | Purpose |
| --- | --- |
| `api/src/config.ts` | Env parsing; fails fast with a named variable |
| `api/src/orange/client.ts` | Typed Orange client |
| `api/src/orange/types.ts` | Response shapes, timestamp units in the type names |
| `api/src/hypothesis/spec.ts` | Spec + condition schema and validator |
| `api/src/hypothesis/evaluate.ts` | The deterministic condition evaluator + support score |
| `api/src/hypothesis/lifecycle.ts` | State machine + per-id serialisation |
| `api/src/hypothesis/store.ts` | Hypotheses as trusted memories; tamper detection |
| `api/src/hypothesis/provision.ts` | Go-live provisioning; ordered teardown with delivery drain |
| `api/src/hypothesis/poller.ts` | Runs the evaluator after each tick; horizon expiry |
| `api/src/marketdata/{fred,stooq}.ts` | Provider connectors |
| `api/src/marketdata/normalise.ts` | Any provider shape → canonical CSV |
| `api/src/marketdata/cache.ts` | Response cache |
| `api/src/mcp/server.ts` | HTTP MCP: `series_search`, `series_fetch` |
| `api/src/routes/{auth,hypotheses,embed,series}.ts` | The nine Wolf API routes |
| `web/src/pages/{HypothesisList,HypothesisDetail,NewHypothesis,Archive}.tsx` | The four pages |
| `web/src/components/{OrangeChatFrame,Scoreboard,MetricChart,Timeline,StatusChip,ConditionTable,TamperWarning}.tsx` | Components |
| `installations/wolf/Dockerfile` | `core` + python3 + pandas + numpy + duckdb |
| `prompts/{interviewer,researcher-preamble,researcher-method,critic}.md` | The four prompt parts |
| `scripts/bootstrap-project.ts` | Idempotent project setup |
| `docker-compose.yml`, `.env.example`, `README.md` | Local topology per § "Local topology" |
| `e2e/run.sh`, `e2e/features/*.spec.ts`, `e2e/mock/*.json` | Playwright + mock-model scripts |

---

## Interfaces

### Migration `045_datasets`

```sql
CREATE TABLE datasets (
  id                 TEXT PRIMARY KEY,
  project            TEXT NOT NULL,
  name               TEXT NOT NULL,
  version            INTEGER NOT NULL,
  labels             JSONB NOT NULL DEFAULT '{}',
  blob_path          TEXT NOT NULL,
  size_bytes         BIGINT NOT NULL,
  row_count          INTEGER NOT NULL DEFAULT 0,
  sha256             TEXT NOT NULL,
  content_type       TEXT NOT NULL DEFAULT 'text/csv',
  created_by_worker  TEXT NOT NULL DEFAULT '',
  created_by_session TEXT NOT NULL DEFAULT '',
  created_at         BIGINT NOT NULL           -- unix MILLISECONDS, like memories
);
CREATE UNIQUE INDEX datasets_project_name_version ON datasets (project, name, version);
CREATE INDEX datasets_project_name_current ON datasets (project, name, version DESC);
CREATE INDEX datasets_labels ON datasets USING GIN (labels);
```

`row_count` exists for the shrink guard and is counted server-side from the pulled bytes, never
supplied by the caller. Dataset `name` uses the label-value charset, ≤63 chars.

### Go store signatures (O2, O3)

```go
// datasets.go
type Dataset struct {
    ID, Project, Name           string
    Version                     int
    Labels                      LabelSet
    BlobPath, SHA256, ContentType string
    SizeBytes                   int64
    RowCount                    int
    CreatedByWorker             string
    CreatedBySession            string
    CreatedAt                   int64 // unix MILLISECONDS
}

var ErrDatasetNotFound = errors.New("dataset not found")

// ErrDatasetVersionConflict carries the version the caller should have passed.
type ErrDatasetVersionConflict struct{ Current int }

func (s *Store) CreateDatasetVersion(ctx context.Context, d *Dataset, ifVersion int) (*Dataset, error)
func (s *Store) CurrentDataset(ctx context.Context, project, name string) (*Dataset, error)
func (s *Store) GetDatasetVersion(ctx context.Context, project, name string, version int) (*Dataset, error)
func (s *Store) ListDatasets(ctx context.Context, project, selector string, limit int) ([]*Dataset, error)
func (s *Store) ListDatasetVersions(ctx context.Context, project, name string, limit int) ([]*Dataset, error)
func (s *Store) ReapDatasetVersions(ctx context.Context, keepPerName int) (deleted int, err error)
func (s *Store) ListOrphanBlobPaths(ctx context.Context, knownPrefix string) ([]string, error)
```

### Dataset HTTP routes (O5)

```
GET /agent/datasets?selector=&limit=          → {"datasets":[…metadata…]}
GET /agent/datasets/{name}                    → current version metadata
GET /agent/datasets/{name}/versions?limit=    → version history, newest first
GET /agent/datasets/{name}/download?version=&token=
                                              → raw bytes
```

Auth: project API key, console JWT, **or** a matching scoped dataset token supplied as the
`token` query parameter on the download route only. (A query parameter, not a fragment: `curl`
cannot send a fragment, and a header would force the agent to compose one.) The token pins a
`(project, name)` pair, **not** a version — so a URL minted before a tick still resolves to that
name's requested version after it.

Status vocabulary, copied from the artifact route (`go/httpapi/artifacts_download.go:198-257`):
`404 not found` for absent, malformed and other-project alike; `410` when the row exists but the
blob is gone; `501` off Postgres; `403` when the credential names no project. Byte responses set
`Content-Type` from the row, `X-Content-Type-Options: nosniff`, `Content-Disposition: attachment`.

### Memory append route (O7) — the trust anchor

```http
POST /agent/memories
X-API-Key: $WOLF_API_KEY
Content-Type: application/json

{ "labels": {"kind":"hypothesis","name":"hyp-1a2b3c4d","status":"live","owner":"kai-at-badcode.dev"},
  "content": "…", "embed": false }
```

```json
{ "id": "…", "labels": {…}, "content": "…",
  "created_by_worker": "", "created_by_session": "", "created_at": 1755600000000 }
```

**Invariants, all of which are tests:**

- **Provenance is server-stamped EMPTY and the request body cannot influence it.** A body
  containing `created_by_worker` or `created_by_session` is rejected `400`, not ignored — silently
  dropping them would leave a caller believing it set them.
- Accepts **project API key or console JWT only**. A session token or an embed token is `403`:
  a credential that reaches a container must never be able to mint trusted state.
- Project comes from the credential; there is no project field.
- Labels go through the existing validator; a bad label is `400` with the validator's own message.
- `embed` defaults to `true`; content over 24KB with `embed: true` is `400` naming the
  `embed: false` remedy, matching `memory_create`'s behaviour. Hard ceiling 1MB.
- `501` when the memory store is not Postgres.
- Writes **no config event** — a memory is data, not configuration.

### Dataset MCP tools (O6b)

```
dataset_list(selector?: string, limit?: number)
  → { datasets: [{ name, version, size_bytes, row_count, sha256, content_type,
                   labels, created_at, created_by_worker, created_by_session }] }
    Metadata only, never bytes. limit default 20, max 100.

dataset_get(name: string, version?: number)
  → { name, version, size_bytes, row_count, sha256, content_type, labels,
      download_url, expires_at }
    download_url is built on AGENTKIT_SELF_URL and carries a scoped token in `token=`.
    Tool description MUST instruct: curl it to a file; do not echo the URL; do not
    print the file's contents.

dataset_put(name: string, path: string, if_version: number,
            labels?: object, content_type?: string, allow_shrink?: boolean)
  → { name, version, size_bytes, row_count, sha256 }
    path is relative to /workspace and must not escape it after normalisation.
    if_version must equal the current version; 0 means "must not exist".
    A conflict is an error naming the current version so the model can re-read and retry.
    Refuses a replacement with row_count < 50% of current unless allow_shrink is true.
```

### Market-data MCP tools (W7)

```
series_search(query: string, source?: "fred" | "stooq")
  → { results: [{ source, id, title, unit, frequency, first, last }] }

series_fetch(source: "fred"|"stooq", id: string, from?: string, to?: string)
  → { download_url, expires_at, rows, unit, source, id }
    CSV: header `timestamp,value`; RFC3339 UTC; ascending; no gap filling; no interpolation.
```

Authenticated by `WOLF_MCP_TOKEN`, a credential distinct from `WOLF_API_KEY`, reaching the
container through `AGENTKIT_MCP_ENV` (see O8).

### Wolf API routes

```
POST   /api/auth/google                     { credential } → session cookie
GET    /api/hypotheses                      → [{ id, title, owner, status, support_score,
                                                 conditions_summary, updated_at, tamper? }]
POST   /api/hypotheses                      { title } → { id }
GET    /api/hypotheses/:id                  → { hypothesis, spec, evaluation, notes,
                                                amendments, verdict, atoms }
POST   /api/hypotheses/:id/go-live          → 200 | 422 { errors: [{path, message}] }
POST   /api/hypotheses/:id/amend            { amendment_id, decision, rationale }
POST   /api/hypotheses/:id/verdict          { verdict, rationale }
POST   /api/hypotheses/:id/retire           { rationale }
GET    /api/hypotheses/:id/embed-token      → { token, expires_at }
GET    /api/hypotheses/:id/series/:metric   → { points:[{t,v}], unit, version, fetched_at }
POST   /mcp                                 → market-data MCP (WOLF_MCP_TOKEN)
```

### Spec JSON (W3)

```jsonc
{
  "thesis": "…restated tightly by the interviewer…",
  "horizon_days": 180,
  "flat_band_pct": 2.0,
  "metrics": [
    { "slug": "drone-suppliers-basket", "source": "stooq", "series_id": "avav.us",
      "direction": "up", "weight": 0.6, "unit": "USD" },
    { "slug": "petro-settlement-share", "source": "derived", "direction": "down",
      "weight": 0.4, "unit": "pct",
      "method": { "description": "share of oil trade settled in USD",
                  "formula": "usd_settled / total_settled * 100",
                  "constituents": ["…"], "source_series": ["…"] } }
  ],
  "invalidation": [ /* condition objects — see § Condition semantics */ ]
}
```

**Validation rules (all of them, W3):** at least one metric; every metric slug unique, label-legal
and ≤ `63 − len("hyp-") − 8 − 1 = 50` characters; weights sum to `1.0 ± 0.001`; each weight in
`(0, 1]`; `horizon_days` in `[7, 3650]`; `flat_band_pct` in `[0, 50]`; a non-`derived` metric has
`series_id`; a `derived` metric has `method` with a non-empty `description` and `formula`, and no
`series_id`; at least one invalidation condition; every condition's `metric` (and `ratio_metric`)
names a metric in this spec; `ratio_metric` present iff `stat == "ratio_to"`; `reference_days`
present iff `reference == "trailing_n_days"` and then in `[2, 365]`; `sustained_days` in
`[0, 365]`; `threshold` finite; at least one condition per metric carrying weight ≥ 0.25;
`ratio_lookback_days` present iff `stat == "ratio_to"` and then in `[1, 400]`;
`staleness_days` (spec level, default `5`) in `[1, 90]`.
Errors are returned **all at once**, each with its JSON path.

### Memory kinds (the Wolf vocabulary, all in the `wolf` Orange project)

| Kind | Labels | Content | Trusted |
| --- | --- | --- | --- |
| `hypothesis` | `kind=hypothesis, name=<id>, status=<state>, owner=<slug>` | **Line 1 is the title**, then the prose thesis, then (for `challenged`/terminal rows) the evaluation snapshot as fenced JSON | **Yes** |
| `hypothesis-spec` | `kind=hypothesis-spec, name=<id>, status=locked` | The spec JSON | **Yes** |
| `verdict` | `kind=verdict, name=<id>, status=confirmed\|invalidated` | Rationale, deciding user, evaluation snapshot | **Yes** |
| `evaluation` | `kind=evaluation, name=<id>` | **Line 1 is the summary line** (see § "Where the board's numbers come from"), then the full evaluation snapshot as JSON | **Yes** |
| `research-note` | `kind=research-note, name=<id>` | Daily prose + per-metric reads | No |
| `spec-amendment` | `kind=spec-amendment, name=<id>, status=proposed` | Proposed change + rationale | No |

Line 1 as the title is not stylistic: `GET /agent/memories` returns a **500-byte snippet** and no
`content` field, so the board must render from snippets alone or pay a read per hypothesis.

`restated_from=<other-id>` is an optional extra label on a `hypothesis` memory, set when a user
creates a hypothesis that restates a retired one. It exists so the Archive page can show that a
"new" thesis is a re-roll of a failed one — without it, archiving and relaunching is a free
scoreboard reset and survivorship bias walks in through the back door.

---

## Out of Scope

- **Trade execution, brokerage, portfolio tracking, position sizing.** Wolf judges theses.
- **Backtesting.** Validation is forward-looking from go-live.
- **A Wolf database.** No relational store, no ORM, no migrations in the Wolf repo.
- **Running wolf-api as more than one replica.** Transition serialisation is in-process.
- **Any change to `web/`'s packaging.**
- **Fixing Orange hazards H1–H13** (`docs/19-embedding.md`). H1 — an embed token carrying
  project-wide authority — is accepted, mitigated by 900s TTLs and a trusted-team audience.
- **Multi-tenant isolation between Wolf users.** One `wolf` project; everyone sees everything.
- **A notifications system.** `request_human_attention` posts to the project's attention channel.
- **Retention policy for concluded hypotheses.**
- **Sub-daily ticks, intraday data, or any provider beyond FRED and Stooq.**

---

## Tickets

27 tickets: **O1–O11** (O6 split into O6a/O6b) in agent-orange (front-loaded — Wolf's data path *and* its state path both
depend on them), **W1–W14** in agent-wolf, **X1** spanning both.

W1, W3 and W6 have no Orange dependency and may start immediately in parallel.

---

### O1: Dataset table, migration and type   [Status: done | Model: sonnet]
- **Scope:** Add migration `045_datasets` exactly as written in **Interfaces**, plus the
  `Dataset` struct, `ErrDatasetNotFound`, `ErrDatasetVersionConflict` and column mapping. No
  store methods yet.
- **Repo:** agent-orange
- **Files:** modify `go/agentdb/migrations.go` (append after `044_agent_sessions_launch_image`,
  `:1045`); create `go/agentdb/datasets.go`.
- **Acceptance criteria:**
  - The migration applies cleanly on a fresh database and is idempotent on re-run.
  - `Dataset.Labels` is the existing `LabelSet` type, so the shared validator and jsonb
    translator apply unchanged. No new label code.
  - `CreatedAt` carries a comment stating unix **milliseconds**, and cites `memories` as the
    precedent.
  - Dataset names are validated by the existing label-value helper in `go/agentdb/labels.go`.
    Do not write a second regex.
  - `ErrDatasetVersionConflict` is a struct carrying `Current int`, not a sentinel — the caller
    must be able to report the version it should have passed.
- **TDD:** no (schema + type declaration).
- **Validation:** `cd go && go build ./... && go vet ./...`
- **Depends on:** —
- [x] done
- Notes: **Implemented and verified 2026-08-20.** Branch `O1-dataset-table-migration`, commit
  `2bda0e8` (136 lines: `go/agentdb/migrations.go`, `go/agentdb/datasets.go`). Verified by an
  independent adversarial agent: build and vet clean; `TestLivePG_MigrationsApplyAndAreIdempotent`
  **PASS** against the throwaway (criterion 1 is not provable by the ticket's own Validation — see
  **R35**); `-run 'Live'` gave 72 PASS and **zero SKIP**.
  Executor decisions the plan did not specify: migration SQL uses `IF NOT EXISTS` (**R34**);
  `ValidateDatasetName` added as a standalone helper wrapping `ValidateLabelValue` because O1 has
  no store method to call it from; `ErrDatasetVersionConflict.Error()` uses a **value** receiver,
  so O2 returns it as `ErrDatasetVersionConflict{Current: n}`, not a pointer; `id` is `TEXT` per
  Interfaces rather than the older tables' `VARCHAR(36)`; no gorm `default:` tags, per migration
  042's stated convention.

### O2: Dataset store — versioned writes under compare-and-swap   [Status: pending | Model: opus]
- **Scope:** `CreateDatasetVersion`, `CurrentDataset`, `GetDatasetVersion`. The whole difficulty
  is that the next version number and the CAS check must be decided inside **one transaction**,
  or two concurrent writers both produce version N+1.
- **Repo:** agent-orange
- **Files:** modify `go/agentdb/datasets.go`; create `go/agentdb/datasets_test.go`,
  `go/agentdb/datasets_live_test.go`.
- **Acceptance criteria:**
  - `CreateDatasetVersion(ctx, d, ifVersion)` returns `ErrDatasetVersionConflict{Current: n}`
    when `ifVersion != n`.
  - `ifVersion == 0` succeeds only when the name does not exist in that project.
  - Versions are strictly monotonic per `(project, name)` with no gaps.
  - **Two concurrent writers at the same `ifVersion`: exactly one succeeds.** Asserted with real
    goroutines against real Postgres, not mocked.
  - The unique index on `(project, name, version)` is the backstop; the test asserts a conflict
    surfaces as `ErrDatasetVersionConflict`, never as a raw driver error.
  - Reads and writes filter on `project` first, in code, always.
  - `row_count` is stored as given by the caller (agentd counts it; the store does not parse CSV).
- **TDD:** yes.
- **Validation** — **both are required**, because the store is Postgres-only (jsonb + GIN) and the
  sqlite path can prove nothing about CAS:
  - `cd go && go build ./... && go vet ./...`
  - `AGENTKIT_TEST_POSTGRES_URL=<see § "The throwaway Postgres"> go test ./agentdb/... -run 'TestDataset' -count=1`
- **Depends on:** O1
- [ ] done
- Notes:

### O3: Listing, version history, reaper and orphan sweep   [Status: pending | Model: sonnet]
- **Scope:** `ListDatasets`, `ListDatasetVersions`, `ReapDatasetVersions`,
  `ListOrphanBlobPaths`. Retention is an **env knob only** — do NOT add a project setting;
  `ProjectSettings` is a typed struct whose columns require their own migration and console
  surface, and that is a separate feature.
- **Repo:** agent-orange
- **Files:** modify `go/agentdb/datasets.go`, `go/agentdb/datasets_test.go`,
  `go/agentdb/datasets_live_test.go`.
- **Acceptance criteria:**
  - `ListDatasets` reuses the existing selector parser. No second parser.
  - Listing returns one row per name — the current version — not every version.
  - The reaper **never** deletes the current version, whatever the retention number, including
    `keepPerName <= 0` which is treated as "keep everything" and logged.
  - The reaper deletes the blob **before** the row; a blob-delete failure leaves the row intact so
    the next sweep retries. `BlobStore.Delete` exists (`go/extension/extension.go`); no new seam.
  - `ListOrphanBlobPaths` returns blob paths under the dataset prefix with no matching row — the
    cleanup path for a crash between blob write and row insert (see O6a/O6b).
- **TDD:** yes.
- **Validation:**
  `AGENTKIT_TEST_POSTGRES_URL=<see § "The throwaway Postgres"> go test ./agentdb/... -run 'TestDataset' -count=1`
- **Depends on:** O2
- [ ] done
- Notes:

### O4: Scoped dataset download tokens   [Status: pending | Model: sonnet]
- **Scope:** A short-TTL token carrying `scope: "dataset:<project>/<name>"`, minted server-side,
  and the verification function that accepts it. Model it directly on
  `go/cmd/agentd/embedtoken.go`: same signing secret (`AGENTKIT_JWT_SECRET`), same
  deliberately-empty `sid` claim, same clamp-never-reject TTL treatment. Default 300s, clamped to
  `[60, 900]`.
- **Repo:** agent-orange
- **Files:** create `go/cmd/agentd/datasettoken.go`, `go/cmd/agentd/datasettoken_test.go`.
- **Acceptance criteria:**
  - The `sid` claim is empty, with a comment citing the reasoning at `embedtoken.go:158-172`
    (a non-empty `sid` would be a working core-MCP credential).
  - The token pins `(project, name)`, **not** a version.
  - `verifyDatasetToken` returns the pinned project and name; a token for `a` does not verify
    against `b`; an expired token fails; a token signed with a different secret fails.
  - TTL is clamped, never rejected.
  - **These are unit-level claims about the mint/verify pair.** Route-level behaviour is O5's.
- **TDD:** yes.
- **Validation:** `cd go && go test ./cmd/agentd/... -run 'TestDatasetToken' -count=1`
- **Depends on:** O1
- [ ] done
- Notes:

### O5: Dataset HTTP read routes   [Status: pending | Model: sonnet]
- **Scope:** The four routes in **Interfaces**. Follow `go/httpapi/artifacts_download.go` for byte
  serving and `go/httpapi/memories.go` for metadata shape and tenancy posture.
- **Repo:** agent-orange
- **Files:** create `go/httpapi/datasets.go`, `go/httpapi/datasets_test.go`; modify
  `go/httpapi/httpapi.go` (constants near `:359-389`, registration near `:463-475`).
- **Acceptance criteria:**
  - Project scope comes from the credential only. There is no project parameter.
  - Unknown, malformed and other-project names all return `404 not found` with a byte-identical
    body — the route is not an existence oracle.
  - Byte responses set `Content-Type` from the row, `X-Content-Type-Options: nosniff`, and
    `Content-Disposition: attachment`. No `Content-Length`.
  - A row whose blob is missing is `410`; `501` off Postgres; `403` when the credential names no
    project.
  - The download route accepts a project API key, a console JWT, **or** a matching scoped token in
    `?token=`. A token for another dataset is `404`, not `403` — same non-oracle rule.
  - A scoped token is accepted **only** on the download route; the three metadata routes reject
    it.
- **TDD:** yes.
- **Validation:** `cd go && go test ./httpapi/... -run 'TestDataset' -count=1`
- **Depends on:** O3, O4
- [ ] done
- Notes:

### O6a: The workspace pull pipeline   [Status: pending | Model: opus]
- **Scope:** One internal, testable function that gets a file out of a running session container
  safely. No MCP surface, no CAS — just: probe, cap, pull, hash, count, store the blob. O6b
  orchestrates it. Split from a single O6 because the pull is where every sharp edge lives and it
  deserves its own tests.
- **Repo:** agent-orange
- **Files:** create `go/cmd/agentd/datasetpull.go`, `go/cmd/agentd/datasetpull_test.go`.
- **The seams, named — do not go looking for them:**
  - The pattern to copy is `onArtifactRegistered` (`go/runner.go:2733-2736`), which resolves the
    instance with `r.get(q.SessionID)` (`:2743`) and execs through the environment's
    `Exec(ctx, id, cmd, opts)` (`go/execenv/execenv.go:54`).
  - **Copy the pattern, not its error handling.** That path is best-effort artifact capture: it
    checks only `err != nil` and **ignores `res.ExitCode`** (`go/runner.go:2824-2827`).
  - Blob storage is `BlobStore.Put` / `Delete` (`go/extension/extension.go`), the same seam O3
    uses.
- **Interface it must produce:**
  ```go
  type pulledFile struct {
      BlobPath  string // where the bytes were stored
      SizeBytes int64
      RowCount  int
      SHA256    string
  }
  func pullWorkspaceFile(ctx context.Context, env execenv.ExecutionEnvironment,
      inst execenv.InstanceID, relPath, contentType string, maxBytes int64,
      blobs extension.BlobStore) (*pulledFile, error)
  ```
- **Acceptance criteria:**
  - Rejects a `relPath` that escapes `/workspace` after normalisation (`..`, absolute paths
    outside, symlink-looking segments) **before** any exec runs.
  - **Probes with `test -f` and requires `ExitCode == 0`** from every command it runs.
    `cat /workspace/missing.csv` returns `err == nil`, `ExitCode == 1` and empty stdout —
    copying the artifact path verbatim would silently store a 0-row file. There is a test for the
    missing-path case and one for a directory path.
  - **Probes size with `wc -c` and refuses anything over `maxBytes` before reading content.**
    `execAndCollect` buffers all exec output into memory with no limit
    (`go/execenv/docker/client.go:238-240`), and agentd shares DinD's namespace and serves every
    session, so an unbounded pull is a memory DoS from inside a container.
  - Bytes are preserved exactly, including NUL bytes and non-ASCII — `stdcopy.StdCopy` is
    binary-safe, so nothing here may re-encode or line-split the payload before hashing.
  - `RowCount` is lines minus one header for `text/csv`, and 0 for any other content type.
  - **The blob key is unique per call** — derive it from a fresh uuid, never from the dataset name
    or a version number. Two concurrent pulls of the same dataset must not collide.
  - `SHA256` is computed over the exact bytes stored.
  - On any failure after the blob is written, the blob is best-effort deleted and the error is
    returned; a delete failure is logged, never fatal.
- **TDD:** yes.
- **Validation:**
  - `cd go && go test ./cmd/agentd/... -run 'TestPullWorkspaceFile' -count=1`
  - `cd go && go build ./... && go vet ./...`
- **Depends on:** O5
- [ ] done
- Notes:

### O6b: Dataset MCP tools   [Status: pending | Model: opus]
- **Scope:** `dataset_list`, `dataset_get`, `dataset_put` on the existing `core` server, built on
  O6a's pull function plus O2's CAS store. Register with one line beside
  `go/cmd/agentd/main.go:572-573`.
- **Repo:** agent-orange
- **Files:** create `go/cmd/agentd/mcp_datasets.go`, `go/cmd/agentd/mcp_datasets_test.go`;
  modify `go/cmd/agentd/main.go`.
- **The seams, named:**
  - The MCP layer authenticates by session token; the calling session id comes from that identity
    and is also how provenance is resolved.
  - `AGENTKIT_SELF_URL` is read once at boot in `go/cmd/agentd/main.go` and passed in — do not
    read the environment from inside a tool handler.
- **Acceptance criteria:**
  - No tool returns file bytes. `dataset_get` returns a URL; `dataset_put` returns metadata.
  - **`download_url` is built on `AGENTKIT_SELF_URL`**, never on `AGENTKIT_PUBLIC_BASE_URL`. A
    test asserts the prefix. Using the public base would be unreachable from a nested container
    and is the most likely wrong turn in this ticket.
  - `dataset_get`'s tool description instructs the model to curl the URL to a file, not to echo
    the URL, and not to print file contents.
  - `dataset_put` passes `AGENTKIT_DATASET_MAX_BYTES` (default **64MiB**) to O6a as `maxBytes`.
  - On CAS conflict, the blob O6a just wrote is best-effort deleted; failure to delete is logged,
    never fatal. O3's orphan sweep is the backstop.
  - The shrink guard refuses `row_count < 50%` of the current version unless `allow_shrink: true`,
    and the refusal message names both counts.
  - Every mutation reads the row back and echoes it (house rule, `docs/18` § 6).
  - Provenance comes from the authenticated session, never from an argument; an unreadable session
    row **refuses the write** rather than guessing (the RD4 invariant).
  - Datasets write **no config event** — they are data. Note in a comment that
    `TestMutationsAreLogged`'s sweep classifies by noun and does not cover datasets.
- **TDD:** yes.
- **Validation:**
  - `cd go && go test ./cmd/agentd/... -run 'TestDataset' -count=1`
  - `cd go && go test ./agentdb/... -run 'TestMutationsAreLogged' -count=1`
    *(that test lives in `go/agentdb/config_events_test.go:594`, NOT in `cmd/agentd` — running it
    against the wrong package matches nothing and passes vacuously)*
  - `cd go && go build ./... && go vet ./...`
- **Depends on:** O6a
- [ ] done
- Notes:

### O7: `POST /agent/memories` — the trust anchor   [Status: done | Model: opus]
- **Scope:** The authenticated memory append route specified in **Interfaces**. This is what makes
  an embedding application able to hold state at all, and its server-stamped empty provenance is
  what makes Wolf's "human decides" invariant enforceable rather than aspirational.
- **Repo:** agent-orange
- **Files:** modify `go/httpapi/memories.go`, `go/httpapi/memories_test.go`,
  `go/httpapi/httpapi.go` (the `MemoryStore` interface gains `CreateMemory`; the doc comment at
  `:84` saying the surface is read-only must be updated, not left lying).
- **Acceptance criteria:**
  - Provenance is stamped empty by the server. A body carrying `created_by_worker` or
    `created_by_session` is **`400`**, not silently ignored.
  - Accepts project API key or console JWT.
  - **A session token never reaches this handler at all.** Session tokens are signed with a
    separate secret (`go/cmd/agentd/sessionsecret.go`), and the middleware independently rejects
    any token carrying a non-empty `sid` claim with **401** (`go/cmd/agentd/auth.go:104-118`,
    "doc 22, RD30"). The test asserts **401 at the middleware**, not 403 at the handler — a
    session token that both authenticates and is then forbidden cannot be constructed.
  - **An embed token DOES reach this handler and must be rejected `403`.** Embed tokens are
    API-class: API secret, empty `sid`, confined only by a `scope` claim that
    `ownsSession` checks on session-by-id routes and nowhere else (hazard H1). The discriminator
    is **`Identity.SessionScope != ""`**; there is a test.
  - *(Note for O10: `docs/19-embedding.md`'s hazard H12 — "the session-token secret and the API
    secret are the same value" — no longer describes this code. Correct it.)*
  - Project comes from the credential; no project field exists in the body.
  - Labels go through the existing validator; a bad label is `400` carrying the validator's own
    message.
  - `embed` defaults true; >24KB with `embed: true` is `400` naming the `embed: false` remedy;
    >1MB is `400` regardless.
  - `501` when the memory store is not Postgres, matching the read routes.
  - No config event is written.
  - The response is the stored row read back, in `memoryRecordResp` shape.
- **TDD:** yes.
- **Validation:**
  - `cd go && go test ./httpapi/... -run 'TestMemor' -count=1`
  - `cd go && go build ./... && go vet ./...`
- **Depends on:** —
- [x] done
- Notes: **Implemented and verified 2026-08-20.** Branch `O7-memory-append-route`, commit
  `41e69fc` (905 insertions across `go/httpapi/memories.go`, `memories_test.go`, `httpapi.go`,
  `go/cmd/agentd/sessionsecret_test.go`). Passed adversarial verification **first time, zero fix
  rounds** — all eleven criteria PASS with evidence, including the two that were most likely to be
  met in letter only: the session-token rejection is asserted at **401 from the real middleware**
  (`sessionsecret_test.go:59-78` drives a genuinely minted `sid` token through `apiAuthMiddleware`),
  and the embed-token rejection uses the literal discriminator `if id.SessionScope != ""`
  (`memories.go:408`) before body decode or any store call. "No config event is written" was proven
  live, not assumed: `TestMemoryAppendRoute_LivePG` counts `config_events` before and after a real
  append and fails on any delta. The doc comment at `httpapi.go:84` was updated, not left lying.
  Executor decision the plan did not specify: the success status is **`201 Created`** — see
  **R36**; W2/W5/W8/W9/W10 must expect 201. A `project` key in the body is ignored rather than
  refused (the criterion reads both ways). Note **R42**: this branch also contains O1's commit.

### O8: Reaper wiring, compose and project-map configuration   [Status: pending | Model: sonnet]
- **Scope:** Run the dataset reaper and orphan sweep on a timer beside the snapshot reaper, and
  make every credential this integration needs actually reach `agentd` and the session containers.
  Compose forwards only what it names, so each variable needs its own line.
- **Repo:** agent-orange
- **Files:** modify `go/cmd/agentd/gc.go`, `go/cmd/agentd/gc_test.go`, `go/cmd/agentd/main.go`,
  `docker-compose.yml`, `.env.example`.
- **Acceptance criteria:**
  - `AGENTKIT_DATASET_REAP_INTERVAL` (default `6h`) and `AGENTKIT_DATASET_KEEP_VERSIONS`
    (default `30`) are parsed by the existing `parseGCDuration` / bounds helpers; `0` interval
    means never sweep. The boot log names both, in `describeReapInterval`'s style.
  - `docker-compose.yml`'s `agentd` service gains explicit lines for `WOLF_API_KEY` and
    `WOLF_MCP_TOKEN`, plus `AGENTKIT_MCP_ENV` already being forwarded — because compose "cannot
    pass a dynamic set of names, so add one line per credential you allowlisted"
    (`docker-compose.yml:166-169`).
  - `.env.example` documents a **worked** `AGENTKIT_PROJECT_MAP` for this integration, in object
    form, including `api_key_env: WOLF_API_KEY` and an `allowed_origins` entry for wolf-web's
    origin. Without an origins entry the embed page's CSP is the union of configured origins and
    **an unlisted origin cannot frame it at all** (`go/cmd/agentd/embedcsp.go`); with no map at all
    it is `frame-ancestors 'none'`.
  - `.env.example` sets `AGENTKIT_MCP_ENV=WOLF_MCP_TOKEN` in its worked example.
- **TDD:** no (wiring + config).
- **Validation:**
  - `cd go && go test ./cmd/agentd/... -run 'TestGC' -count=1`
  - `docker compose config | grep -E 'WOLF_API_KEY|WOLF_MCP_TOKEN|AGENTKIT_MCP_ENV'` shows all
    three on the `agentd` service
- **Depends on:** O3
- [ ] done
- Notes:

### O9: Dataset round-trip system test   [Status: pending | Model: sonnet]
- **Scope:** The only test that exercises the `Exec` + `cat` path against a real container.
- **Repo:** agent-orange
- **Files:** create `go/systemtest/datasets_test.go`.
- **Acceptance criteria:**
  - Write a file into `/workspace`, `dataset_put`, `dataset_get`, curl the URL **from inside the
    container**, and assert the bytes are byte-identical — including newlines and non-ASCII.
  - The curl uses the returned `download_url` verbatim; this is what proves the
    `AGENTKIT_SELF_URL` requirement in O6b.
  - A second `dataset_put` at a stale `if_version` fails and the stored version is unchanged.
  - A replacement below the shrink threshold is refused without `allow_shrink`, accepted with it.
  - Skips cleanly when Docker is unavailable, matching the suite's existing guard.
- **TDD:** no (the integration test is the deliverable).
- **Validation:** `cd go && go test ./systemtest/... -run 'TestDataset' -count=1`
- **Depends on:** O6b
- [ ] done
- Notes:

### O10: Document datasets and the memory append route   [Status: pending | Model: sonnet]
- **Scope:** `docs/20-datasets.md` plus the cross-references that keep the existing docs true.
- **Repo:** agent-orange
- **Files:** create `docs/20-datasets.md`; modify `docs/19-embedding.md` (route summary + a note
  that memories are no longer read-only over HTTP), `docs/18-workers-memory-events.md` (core-tools
  table), `CLAUDE.md`.
- **Acceptance criteria:**
  - Covers: the atom, CAS, the two byte paths and why each was chosen, the shrink guard,
    retention and the orphan sweep, and every new env var.
  - States plainly that **bytes never need to cross the model's context** *and* the two caveats:
    the `download_url` carries a bearer token that DOES enter context and transcripts, and nothing
    prevents a model printing a file itself.
  - `docs/19-embedding.md`'s § 7 claim that memory is "Read-only. There is no HTTP write" is
    corrected, and § 10's route table gains all five new routes.
  - The trust rule — empty provenance means server-written — is documented as the intended
    mechanism for an embedder holding authoritative state.
- **TDD:** no (docs).
- **Validation:** every route named in `docs/20-datasets.md` appears in `go/httpapi/httpapi.go`;
  `grep -n 'Read-only' docs/19-embedding.md` no longer claims memories cannot be written.
- **Depends on:** O7, O9
- [ ] done
- Notes:

### O11: Make retraction visible to a trusted reader   [Status: pending | Model: opus]
- **Scope:** Add `include_retracted=1` to `GET /agent/memories`, and return the retracting
  memory's own provenance alongside a retracted row, so a caller holding project authority can
  tell "this was withdrawn, by a worker" apart from "this does not exist". Without it, an
  untrusted session can erase Wolf's authoritative state and nothing can detect it — see
  § "The trust model" → "Retraction".
- **Repo:** agent-orange
- **Files:** modify `go/agentdb/memories.go`, `go/agentdb/memories_test.go`,
  `go/httpapi/memories.go`, `go/httpapi/memories_test.go`, `go/httpapi/httpapi.go`.
- **Acceptance criteria:**
  - `MemoryQuery` gains `IncludeRetracted bool`. When false — **the default, and every existing
    caller** — `notRetractedSQL` (`go/agentdb/memories.go:284-288`) is applied exactly as today.
    A test asserts existing behaviour is unchanged when the flag is absent.
  - When true, retracted rows are returned, each carrying `retracted_by`:
    `{ memory_id, created_by_worker, created_by_session, created_at }` for the **newest**
    retraction of that row. Non-retracted rows omit the field entirely.
  - Exposed as `?include_retracted=1` on `GET /agent/memories`. Any other value is `400`; absence
    means false.
  - **Available to project API keys and console JWTs only.** A session-scoped identity gets
    `403` — an actor who can retract must not be able to inspect the audit view.
  - `NewestMemory` and `GET /agent/memories/current` are **unchanged**. Briefings must keep
    honouring retraction; this flag is a read-side audit facility, not a new default.
  - The doc comment at `go/agentdb/memories.go:281-283` — explaining that `GetMemory` is
    deliberately unfiltered — is extended to mention this flag as the search-side equivalent.
- **TDD:** yes.
- **Validation:**
  - `cd go && go test ./agentdb/... -run 'TestMemor' -count=1`
  - `AGENTKIT_TEST_POSTGRES_URL=<see § "The throwaway Postgres"> go test ./agentdb/... -run 'TestMemor' -count=1`
  - `cd go && go test ./httpapi/... -run 'TestMemor' -count=1`
- **Depends on:** O7 *(shares `go/httpapi/memories.go` and `httpapi.go`; strictly serial)*
- [ ] done
- Notes:

---

### W1: Wolf repo scaffold and local topology   [Status: blocked — escalated | Model: sonnet]
- **Scope:** Create the repository with `api/` (Node + TypeScript + vitest) and `web/` (React 18 +
  MUI 6 + Vite + vitest), and the compose topology from § "Local topology and networking" — which
  is not negotiable, because a `wolf-api` on the ordinary compose network is unreachable from
  session containers.
- **Repo:** agent-wolf
- **Files:** `package.json` (workspaces), `tsconfig.base.json`, `api/`, `web/`,
  `docker-compose.yml`, `.env.example`, `README.md`, `.gitignore`.
- **Acceptance criteria:**
  - `wolf-api` uses `network_mode: "container:${ORANGE_DIND_CONTAINER}"` (default
    `agent-orange-dind-1`), so it shares DinD's namespace: it reaches agentd at
    `http://localhost:8099` and session containers reach it at
    `http://172.17.0.1:${WOLF_API_PORT}`.
  - `wolf-web` joins Orange's compose network as **external** (`agent-orange_default`), publishes
    `${WOLF_WEB_PORT:-8081}`, and proxies `/api` and `/mcp` to `http://dind:${WOLF_API_PORT}` —
    the same indirection nginx already uses for agentd (`deploy/web.nginx.conf:55`).
  - `.env.example` documents every variable `api/src/config.ts` reads, each with a comment, and
    `ORANGE_DIND_CONTAINER`, `WOLF_API_PORT`, `WOLF_WEB_PORT`, `WOLF_MCP_URL`.
  - **`web/` imports nothing from agent-orange — enforced by a test, and the test is graded on
    this explicit list, not on the phrase "any import specifier".** *(Rewritten 2026-08-21 after
    three failed verification rounds — see **R38**. This list is the criterion. An escape route
    not named here is a plan bug to be logged, not an executor failure.)*
    - **Files scanned:** every file under `web/src` and `web/` root config with an extension in
      `.ts .tsx .js .jsx .mjs .cjs` — i.e. every extension Vite + `@vitejs/plugin-react` will
      resolve. Plus `web/vite.config.ts`, `web/tsconfig.json`, `tsconfig.base.json` and
      `web/package.json` as described below. A regression case must write a temporary `.jsx`
      file with an outside-the-repo import and assert the collector picks it up, so the extension
      list cannot silently fall behind the bundler's again.
    - **Specifier forms caught:** static `import`/`export ... from`; **dynamic `import(...)`**;
      **`require(...)`**; type-only `import type`; and side-effect `import "..."`.
    - **Specifier kinds resolved:**
      - *relative* (`./`, `../`) — resolve and assert the realpath is inside the repo, by path
        segment, **not** by string prefix (`/repo-evil` must not count as inside `/repo`).
      - *absolute* (`/home/...`) — resolve with `realpathSync` when it exists and run through the
        same containment check. **Must not** fall through the bare-specifier branch: an empty
        package name resolving to `web/node_modules` is the exact hole round 3 found.
      - *bare* (`react`, `@mui/material`) — allowed only if declared in `web/package.json`'s
        `dependencies`/`devDependencies` or the root `package.json`'s. A declared-but-not-installed
        specifier passes; a specifier declared nowhere fails.
    - **Config escape routes:** `web/vite.config.ts`'s `resolve.alias` (every shape Vite accepts —
      object, or array of `{find, replacement}`), and `compilerOptions.paths` in **both**
      `web/tsconfig.json` and `tsconfig.base.json`. Any target resolving outside the repo fails.
    - **Manifest escape route:** any dependency in `web/package.json` or the root `package.json`
      whose specifier starts with `file:`, `link:` or `portal:` fails.
    - The suite must not fail against its own comments: use a placeholder token in prose rather
      than a literal quoted specifier.
  - **`api/` is held to the same boundary.** The architecture forbids it importing Orange code
    just as firmly, and nothing asserted it. Reuse the same checker; do not write a second one.
  - `README.md` gives the exact two-repo start sequence: bring Orange up, then Wolf, in that
    order, with the reason (the external network must exist first).
  - The pinned choices in § "Pinned technology choices" are installed and wired: Express 5,
    `@modelcontextprotocol/sdk`, vitest, `undici` `MockAgent`, React 18.3.1 + MUI 6, Recharts,
    zod, pino. No alternative is substituted; a later ticket needing something else must log it
    rather than swap it.
  - `api/src/errors.ts` exists with the `WolfError` class and the **seven** `WolfErrorKind`
    values exactly as written in § "Shared error taxonomy", plus a test asserting `not_found` and
    `unavailable` are distinct and that `misconfigured` carries the offending variable name.
  - **An unhandled throw maps to `internal`, never to `unavailable`** (**R39**). The Express
    error handler classifies a non-`WolfError` throw as `internal`/500, does **not** put the
    thrown message in the response body, and logs the real error server-side. Tested: throw a
    bare `Error("secret-ish detail")` from a route and assert the response body does not contain
    that string and that `kind` is `internal`.
  - **`web/` uses vite 6** (**R40**), so one resolver evaluates `web/vite.config.ts` under both
    `vitest run` and `vite build`. `yarn install` must emit **no unmet-peer warning** for vitest,
    and `web/node_modules/vitest/node_modules/vite` must not exist — assert the absence, because
    that nested copy is the actual defect. React/MUI stay at `examples/web`'s versions.
  - **React, react-dom and react-is are pinned exactly (`"18.3.1"`, no caret)**, matching
    `examples/web/package.json`'s form as § "Pinned technology choices" intends.
  - **The DinD gateway is discovered at boot, not hard-coded** (**R43**). `api/src/config.ts`
    resolves it as described in § "Local topology and networking": explicit `WOLF_MCP_URL` wins;
    otherwise read the default route from DinD's netns; otherwise fall back to `172.17.0.1` and
    log which was used. Unit-tested with a fake route source covering all three paths.
  - **A repo-root `.dockerignore` exists** listing at least `node_modules`, `**/node_modules`,
    `.git`, `dist`, `.env`, `coverage` — both Dockerfiles use the repo root as build context and
    `COPY api ./api` / `COPY web ./web` run *after* `yarn install`, so without it a host
    `node_modules` overwrites the installed tree inside the image.
  - **The `/mcp` nginx location carries the same SSE-safe directives as `/api/`**
    (`proxy_buffering off; proxy_cache off; proxy_read_timeout 3600s;`). Streamable HTTP's
    server-to-client leg is an SSE stream on `GET /mcp`; nginx's 60s default would cut it.
  - `yarn typecheck` and `yarn test` pass from a clean checkout in both packages.
- **TDD:** no for the scaffold; **yes** for `errors.ts`.
- **Validation:**
  - `yarn install --frozen-lockfile && yarn typecheck && yarn test` — from a **clean export**
    (`git archive <branch> | tar -x` into a temp dir), not from the working tree
  - `docker compose config` resolves and shows the `network_mode` and external network
  - **Empirical, not declarative** (**R41**): `docker build` both images, then run wolf-web
    against a stub backend aliased `dind` and assert a request to `/api/healthz` arrives at the
    backend as **`/api/healthz`** — the literal prefix, unstripped (**R37**)
- **Depends on:** —
- [ ] done
- Notes: **Implemented 2026-08-20; 10 of 11 criteria verified PASS; ESCALATED on the eleventh.**
  Branch `W1-wolf-repo-scaffold`, five commits ending `6b59b4f`. `yarn typecheck` and
  `yarn test` are green from a clean `git archive` export (20 tests), `docker compose config`
  resolves, and both images build; the verifier proved the `/api` proxy hop empirically rather
  than by reading the config. The owner-mandated archival landed as `c1f45b5` with git recording
  a rename, and the nine research briefs survive under
  `docs/archive-2026-05-hypothesis-bot/`.
  **Round 4 (2026-08-21), against the rewritten criteria — 19 of 22 sub-criteria PASS, 3 fail.**
  The rewrite worked. The verifier ran **21 escape probes** and **all 12 mandated ones were
  caught**, plus two it invented (a symlinked directory into agent-orange, and `export * from` an
  outside path). The branch adds a third yarn workspace `tools/import-boundary` holding one shared
  checker used by both `web/` and `api/`, `internal` as the seventh error kind, vite 6, boot-time
  gateway discovery (proven live: `{"mcpUrlSource":"discovered"}`), `.dockerignore`, SSE
  directives on `/mcp`, and an empirical proxy proof (`curl /api/healthz` arrives as
  `PATH=/api/healthz`, unstripped).
  **Three failures remain, all of them the implementation not matching a now-precise criterion —
  a different and much smaller problem than rounds 1–3:**
  - **F1 (blocking)** — root config files are never scanned. `checkImportBoundary` walks only
    `srcDir` (`tools/import-boundary/src/index.ts:246`), so `web/vite.config.ts` and
    `api/vitest.config.ts` may import anything. The criterion says "every file under `web/src`
    **and `web/` root config**".
  - **F2 (major)** — the bare-specifier rule is looser than written: any specifier resolving into
    `node_modules` passes regardless of declaration (`:291-306`); the `declared` set is consulted
    only when resolution *fails*. The criterion requires declaration.
  - **F3 (minor)** — template-literal specifiers are unmatched (`:42-46` restricts the quote class
    to `["']`), so a `require` with backticks escapes.
  Five further probes missed are **not** criterion failures and were correctly reported as plan
  gaps: `.mts`/`.cts` sources, computed/non-literal dynamic specifiers, and a bare specifier
  hoisted into `node_modules` but declared in no manifest.
  **Defects found in the rewritten criterion itself**, recorded so revision 4 does not repeat them:
  it names `web/node_modules/vitest/node_modules/vite` as the path to assert absent, but yarn
  classic hoists — that path never existed even when the defect did, and the real nested copy was
  at the repo root; raising vite to 6 alone does **not** close the hole, because vitest 4 carries
  vite as a real `dependencies` entry with its own range string that yarn classic resolves
  independently; the gloss "every extension Vite will resolve" is factually wrong (vite 6.4.3 also
  resolves `.mts` and `.cts`); `tools/import-boundary` is itself held to no boundary by any
  criterion; and only `compilerOptions.paths` is enumerated, while `baseUrl`, `extends` chains and
  additional tsconfigs are equivalent escapes. R41's first half is also still open — nothing ties
  `.env.example` to `config.ts`, and eight later tickets add config.

### W2: Orange client   [Status: pending | Model: sonnet]
- **Scope:** A typed client for every Orange route Wolf touches: session create/get-by-name/delete,
  **memory append** (O7) and the three memory reads, dataset list/get/download, worker
  create/delete, schedule create/delete/list, delivery list, embed-token mint, Google verify.
- **Repo:** agent-wolf
- **Files:** `api/src/orange/client.ts`, `api/src/orange/types.ts`,
  `api/src/orange/client.test.ts`.
- **Acceptance criteria:**
  - The API key is read from config once and never appears in a response, a log line or an error
    message. A test asserts a thrown error's serialised form contains no key material.
  - Timestamp units are in the type names — e.g. `createdAtMs` for memories and datasets,
    `createdAtSec` for sessions. A test asserts the two are not interchangeable.
  - Non-2xx responses become typed errors carrying Orange's status and body verbatim.
  - `GET /agent/sessions` is always called with `?user_email=*`; an API key's synthetic email
    (`api-key:<project>`) matches no session row and the list would otherwise be empty.
  - Every memory read returns provenance fields; the client does not strip them.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/orange && yarn typecheck`
- **Depends on:** W1 *(and O7 must be merged before this ticket's memory-append tests can run
  against a live stack; the unit tests mock the HTTP layer and do not block)*
- [ ] done
- Notes:

### W3: Spec and condition schema + validator   [Status: pending | Model: opus]
- **Scope:** The spec type, the condition type, and the validator that is the **sole gate** on
  go-live. Every rule listed under "Validation rules" in **Interfaces** is in scope.
- **Repo:** agent-wolf
- **Files:** `api/src/hypothesis/spec.ts`, `api/src/hypothesis/spec.test.ts`.
- **Acceptance criteria:**
  - Returns **all** errors at once, each with a JSON path (`metrics[1].weight`), never just the
    first.
  - A table test covers every rule in **Interfaces**, each with an accepting and a rejecting case.
  - Metric slugs are bounded at **50 characters**, not 63: the dataset name is
    `<hyp-id>-<slug>` and the id is 12 characters plus a separator, so a 63-character slug
    validates here and then fails `dataset_put` on every tick forever.
  - A `derived` metric without a `method.description` and `method.formula` is rejected — an
    unpinned derived metric is a scoreboard the critic can silently redefine.
  - Rejects a spec where a metric carrying weight ≥ 0.25 has no invalidation condition: a heavy
    metric that can never falsify anything is a scoreboard with a blind spot.
  - The worked example in **Interfaces** round-trips.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/hypothesis/spec && yarn typecheck`
- **Depends on:** W1
- [ ] done
- Notes:

### W4: The condition evaluator   [Status: pending | Model: opus]
- **Scope:** Implement § "Condition semantics" exactly: the four statistics, the three references,
  the sustained-window rule with its coverage floor, the indeterminate cases, and the support
  score. Pure functions over parsed points — no I/O in this module.
- **Repo:** agent-wolf
- **Files:** `api/src/hypothesis/evaluate.ts`, `api/src/hypothesis/evaluate.test.ts`.
- **Acceptance criteria:**
  - Signature is pure: `evaluate(spec, seriesByMetric, liveAtMs, nowMs) → EvaluationResult`.
  - Every statistic and every reference has a hand-computed fixture test — the expected numbers
    are written out in the test, not derived by calling the implementation.
  - **`nowMs` anchors the sustained window** (`(nowMs - sustained_days, nowMs]`), not `t_last`.
    A fixture proves it: a series that tripped continuously and then stopped updating 60 days ago
    yields `indeterminate`, **not** `tripped`. This single test is the difference between a
    product that flags a dead feed and one that invalidates a thesis on ancient data.
  - `sustained_days == 0` additionally requires the latest observation to be fresher than
    `staleness_days`, else `indeterminate` with reason `stale_series`.
  - **`R <= 0` skips the observation** for `change_pct` and `drawdown_pct` — a negative reference
    would invert the comparison. All observations skipped ⇒ `indeterminate` with reason
    `non_positive_reference`. There is a fixture with a zero-crossing series.
  - `change_abs` is implemented and is the recommended statistic for zero-crossing series.
  - `ratio_to` uses `ratio_lookback_days`, not a hard-coded constant, and skips when no prior
    point falls inside it. A fixture pairs a daily metric with a monthly one.
  - Reasons are enumerated, not free text: `stale_series`, `non_positive_reference`,
    `insufficient_coverage`, `no_observations`.
  - **Insufficient coverage yields `indeterminate`, never `tripped`.** A 30-day sustained window
    containing 4 observations does not trip. There is an explicit test for this, because it is the
    difference between "the evidence says so" and "there was no evidence to the contrary".
  - A zero or missing reference value skips that observation rather than dividing.
  - The support score is in `[-1, 1]`, uses `flat_band_pct`, and returns 0 for a metric with no
    observations.
  - The result carries, per condition, the window bounds and the observation count — this is what
    gets snapshotted into the verdict memory, so it must be self-describing.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/hypothesis/evaluate && yarn typecheck`
- **Depends on:** W3
- [ ] done
- Notes:

### W5: Lifecycle, trusted store and tamper detection   [Status: pending | Model: opus]
- **Scope:** The six-state machine with per-hypothesis serialisation, and reading/writing
  hypotheses as **trusted** memories per § "The trust model".
- **Repo:** agent-wolf
- **Files:** `api/src/hypothesis/lifecycle.ts`, `api/src/hypothesis/store.ts`,
  `api/src/hypothesis/lifecycle.test.ts`, `api/src/hypothesis/store.test.ts`.
- **Acceptance criteria:**
  - Every legal transition is allowed and every illegal one throws a typed error naming both
    states — an exhaustive table over all ordered state pairs.
  - Transitions are serialised per hypothesis id by an in-process async mutex, and the current
    state is **re-read inside the critical section**. A test drives two concurrent transitions and
    asserts one is rejected as illegal rather than both appending.
  - **Only memories with `created_by_worker === "" && created_by_session === ""` are treated as
    state.** A test appends a forged `status=confirmed` memory carrying a worker name and asserts
    the reported status is unchanged.
  - The board read is the two-step in § "The trust model": one `latest_per` request, and a
    per-name follow-up **only** for ids whose newest row is untrusted. A test asserts the
    all-trusted case issues exactly one request.
  - An untrusted newest row produces a `tamper` field naming the writing worker or session.
  - **The authoritative index of hypotheses is the session list**
    (`GET /agent/sessions?user_email=*`, names matching `hyp-*`), not memory. A hypothesis whose
    session exists but whose state row is absent from the board read is an anomaly, not an
    absence.
  - **Retraction is handled.** For any such anomaly, and always on the detail read, the store
    queries with `include_retracted=1` (ticket O11) and **ignores any retraction whose own
    provenance is non-empty**. A retraction written by Wolf itself (empty provenance) is honoured.
  - A test appends a `retracts=<trusted-id>` memory from a session, asserts the reported status is
    unchanged, and asserts a `tamper` field naming the retractor. **This test is the one that
    matters** — the forged-newer-row test passes even against a store that retraction defeats.
  - Ids are `hyp-<8 lowercase hex>`, generated, never derived from user text.
  - The owner slug mapping is **total**: lowercase, `@` → `-at-`, every remaining character
    outside `[a-z0-9._-]` → `-`, collapse runs of `-`, trim to 63, strip leading/trailing
    non-alphanumerics. `kai+test@gmail.com` and `KAI@BadCode.dev` both produce valid labels. The
    full address is kept in the memory content.
  - The title is line 1 of the content and is parsed back from a 500-byte snippet, including the
    case where the snippet truncates mid-multibyte-character.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/hypothesis/lifecycle src/hypothesis/store && yarn typecheck`
- **Depends on:** W2, W4, O11 *(the trusted store cannot detect retraction without O11)*
- [ ] done
- Notes:

### W6: Market-data providers and normaliser   [Status: pending | Model: sonnet]
- **Scope:** FRED and Stooq connectors, the normaliser to canonical CSV, and a TTL cache.
- **Repo:** agent-wolf
- **Files:** `api/src/marketdata/{fred,stooq,normalise,cache}.ts` and a `.test.ts` beside each;
  `api/src/marketdata/__fixtures__/` holding recorded real responses.
- **Acceptance criteria:**
  - Fixtures are **recorded real responses**, not hand-written, and each connector is tested
    against its own fixture with no network access.
  - Output is always RFC3339 UTC, ascending, deduplicated by timestamp, with **no gap filling and
    no interpolation**.
  - FRED's missing-value sentinel `.` omits the row; it never becomes `0`.
  - Errors are typed and distinguish **not-found** from **unavailable** — the tick treats them
    differently and the evaluator must not see an outage as an empty series.
  - The cache honours a TTL, is keyed by `(source, id, from, to)`, and is bypassable per call.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/marketdata && yarn typecheck`
- **Depends on:** W1
- [ ] done
- Notes:

### W7: Market-data MCP server   [Status: pending | Model: sonnet]
- **Scope:** An HTTP MCP server exposing `series_search` and `series_fetch`, authenticated by
  `WOLF_MCP_TOKEN`, returning short-lived download URLs on Wolf's own origin.
- **Repo:** agent-wolf
- **Files:** `api/src/mcp/server.ts`, `api/src/mcp/tools.ts`, `api/src/mcp/server.test.ts`.
- **Acceptance criteria:**
  - Neither tool returns CSV in its result body.
  - Download URLs expire (default 300s) and are single-series scoped.
  - An unauthenticated or wrongly-authenticated `/mcp` call is rejected **before** any provider is
    touched — a test asserts the connector was not called.
  - `tools/list` reports both tools with complete JSON Schemas.
  - The URL host is taken from `WOLF_MCP_URL`'s origin, so a container gets an address it can
    actually reach (`http://172.17.0.1:<port>` in compose).
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/mcp && yarn typecheck`
- **Depends on:** W6
- [ ] done
- Notes:

### W8: Auth and hypothesis read routes   [Status: pending | Model: sonnet]
- **Scope:** `POST /api/auth/google`, `GET /api/hypotheses`, `GET /api/hypotheses/:id`,
  `POST /api/hypotheses`.
- **Repo:** agent-wolf
- **Files:** `api/src/routes/auth.ts`, `api/src/routes/hypotheses.ts`, tests beside each.
- **Acceptance criteria:**
  - A Google identity Orange verifies but Wolf's allowlist does not contain is **rejected** —
    Orange verifying the token is necessary, not sufficient. Orange's route also 404s unless
    `GOOGLE_CLIENT_ID` is set **on Orange**; the client surfaces that as a configuration error
    naming the variable, not as an auth failure.
  - Cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` outside development.
  - `POST /api/hypotheses` creates the Orange session with
    `{ name: "hyp-<id>", worker: "interviewer" }` and appends the trusted `draft` memory. If
    session creation fails, no hypothesis memory is left behind.
  - Orange's create errors are surfaced **verbatim** — notably "host port pool is exhausted",
    which is operational, not a product bug, and must not be flattened into "could not create".
  - `GET /api/hypotheses` returns the board including `support_score`, a conditions summary and
    any `tamper` field.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/routes/auth src/routes/hypotheses && yarn typecheck`
- **Depends on:** W5
- [ ] done
- Notes:

### W9: Go-live provisioning and ordered teardown   [Status: pending | Model: opus]
- **Scope:** `POST /api/hypotheses/:id/go-live`, `/verdict`, `/retire`, `/amend`. Go-live
  validates the spec, writes it as a trusted locked memory, composes the researcher prompt as
  `locked preamble + method body`, creates `researcher-<id>` and the daily schedule.
- **Repo:** agent-wolf
- **Files:** `api/src/hypothesis/provision.ts`, `api/src/hypothesis/provision.test.ts`; modify
  `api/src/routes/hypotheses.ts`.
- **Acceptance criteria:**
  - Go-live is refused `422` with per-field errors when the spec does not validate; the hypothesis
    stays `draft` and **nothing** is provisioned.
  - The composed researcher prompt embeds the locked spec verbatim in the preamble, including
    every `derived` metric's `method` object.
  - **Teardown order is: delete schedule → wait for pending deliveries to clear → delete worker →
    delete session**, asserted by recorded call order, not merely that all four happened.
  - **There is no delivery-cancel API and none is being added.** Deliveries are read-only over
    HTTP — the only route is `GET /agent/deliveries` (`go/httpapi/httpapi.go:379`) — and
    `DeliveryQuery` has no `worker` field (`go/agentdb/events.go`), so Wolf lists
    `?status=pending` and filters on the row's own `worker` client-side. Draining therefore means
    **polling until none remain**, with a bounded wait (`WOLF_TEARDOWN_DRAIN_TIMEOUT`, default
    60s) after which teardown proceeds anyway and logs what it left behind.
  - **The schedule must be deleted FIRST, before draining.** Draining while the schedule still
    exists cannot terminate: the scheduler mints a new delivery every tick. Revision 2 had this
    order backwards.
  - A tick session still in flight is allowed to finish; anything it writes is untrusted by
    construction and cannot change state.
  - **Tick sessions are cleaned up.** Each firing creates a fresh session, and nothing else
    deletes them — the 30-minute idle archive returns the port but the rows accumulate one per day
    per hypothesis forever. Teardown deletes every session for this worker
    (`GET /agent/sessions?user_email=*&worker=<name>`, the `?worker=` filter exists at
    `go/httpapi/history.go:123`), and W10's poller does the same sweep for `live` hypotheses,
    keeping the most recent 7 tick sessions per hypothesis for debugging.
  - Teardown never deletes datasets — asserted explicitly.
  - A partial failure mid-provision leaves no half-live hypothesis: either everything exists and
    the status is `live`, or it is rolled back and the status is unchanged.
  - The cron is 5-field, in the stack's local timezone, and overridable by `WOLF_SCHEDULE_CRON`
    so X1 can run a per-minute schedule. `@daily` is never emitted — Orange refuses nicknames.
  - A verdict or an accepted amendment writes a trusted memory carrying the **evaluation
    snapshot** from W4, so the record survives the dataset reaper.
  - Only the human-initiated routes can produce `confirmed`, `invalidated` or `archived`.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/hypothesis/provision && yarn typecheck`
- **Depends on:** W8, W12 *(the preamble template is a W12 deliverable)*
- [ ] done
- Notes:

### W10: Evaluation poller — the `live → challenged` trigger   [Status: pending | Model: opus]
- **Scope:** The mechanism that actually moves a hypothesis to `challenged`. Nothing else does:
  Orange cannot call Wolf (no webhook; subscriptions dispatch workers, not HTTP), so Wolf polls.
- **Repo:** agent-wolf
- **Files:** `api/src/hypothesis/poller.ts`, `api/src/hypothesis/poller.test.ts`; wire into the
  api entrypoint.
- **Acceptance criteria:**
  - Runs on an interval (`WOLF_POLL_INTERVAL`, default 5m) over every `live` hypothesis: fetch each
    metric's current dataset, parse, run W4's evaluator, and act on the result.
  - **Dataset reads are version-gated.** Datasets change once a day; polling every 5 minutes would
    re-download and re-parse each CSV 288 times a day. The poller fetches
    `GET /agent/datasets/{name}` (metadata, carrying `version` and `sha256`) first and skips the
    byte fetch entirely when `version` is unchanged, caching parsed points by `(name, version)`.
    A test asserts a second poll over unchanged data issues no download.
  - **The poller writes the board's numbers.** It appends a trusted `kind=evaluation` memory whose
    line 1 is the summary line specified in § "Where the board's numbers come from" and whose body
    is the full snapshot — but **only when that summary line differs from the previous one, or
    more than 20 hours have passed**. A test asserts an unchanged evaluation appends nothing.
  - It sweeps completed tick sessions for each `live` hypothesis, keeping the most recent 7.
  - Any condition `tripped` ⇒ transition to `challenged` through W5's state machine, writing the
    trusted memory **with the evaluation snapshot embedded**.
  - **Notification is Wolf's own, not Orange's.** `request_human_attention` is an MCP tool
    callable only from inside a container; over HTTP, attention is **read-only**
    (`GET /agent/attention-requests`, `go/httpapi/httpapi.go:319`). The `challenged` state on the
    board IS the notification surface. The poller additionally READS
    `GET /agent/attention-requests` so asks raised by the researcher agent itself appear on the
    hypothesis detail page.
  - `horizon_days` elapsed with nothing tripped ⇒ also `challenged`, with a distinct reason
    (`horizon_reached`) so the UI can say "time's up, verdict?" rather than "your thesis failed".
  - Three consecutive polls where the same condition is `indeterminate` ⇒ raise a human-attention
    flag on the hypothesis, without changing state. A scoreboard nobody can compute is surfaced,
    not ignored.
  - The poller is **idempotent**: a hypothesis already `challenged` is not re-transitioned and does
    not re-notify. Asserted by running the poller twice over the same data.
  - A dataset that 404s (never written yet) is not an error and not an evaluation — it is skipped
    with a log line.
  - Poll failures never crash the process; each hypothesis is isolated.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/hypothesis/poller && yarn typecheck`
- **Depends on:** W9
- [ ] done
- Notes:

### W11: Embed tokens and the series proxy   [Status: pending | Model: sonnet]
- **Scope:** `GET /api/hypotheses/:id/embed-token` and `GET /api/hypotheses/:id/series/:metric`.
- **Repo:** agent-wolf
- **Files:** `api/src/routes/embed.ts`, `api/src/routes/series.ts`, tests beside each.
- **Acceptance criteria:**
  - Embed tokens are minted at the **900s default**, never at the 3600s ceiling. Hazard H1 means
    the token carries project-wide authority for its lifetime; the TTL is the only bound.
  - A caller who is not signed in gets no token.
  - The series route **proxies bytes** and never redirects the browser to Orange — Orange has no
    CORS by design and a redirect would fail.
  - A `404` from Orange for a dataset that has never been written becomes an empty series with a
    `never_fetched` marker, not a 500.
  - Parsed points are validated: a non-numeric value or an unparseable timestamp fails the request
    loudly rather than rendering a broken chart.
  - Responses carry the dataset `version` so the UI can show which snapshot it is looking at.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/routes/embed src/routes/series && yarn typecheck`
- **Depends on:** W9
- [ ] done
- Notes:

### W12: Wolf image, prompts and project bootstrap   [Status: pending | Model: opus]
- **Scope:** The installation image, the four prompt files, and an idempotent bootstrap script
  that configures the `wolf` project end to end.
- **Repo:** agent-wolf
- **Files:** `installations/wolf/Dockerfile`, `prompts/interviewer.md`,
  `prompts/researcher-preamble.md`, `prompts/researcher-method.md`, `prompts/critic.md`,
  `scripts/bootstrap-project.ts`, `scripts/bootstrap-project.test.ts`.
- **Acceptance criteria:**
  - The Dockerfile sets **no** `CMD`, `ENTRYPOINT`, `EXPOSE`, `HEALTHCHECK` or `WORKDIR` — house
    rule 4; `WORKDIR` is on that list because setting it broke every session from `core` and
    `example` on 2026-08-13.
  - `python3 -c "import pandas, numpy, duckdb"` succeeds inside the built image.
  - The bootstrap script is **idempotent**: running it twice produces no duplicate workers,
    schedules, settings or MCP entries. Asserted by a test that runs it twice.
  - It sets the project's `base_image` to the Wolf image, the `attention_channel`, and the MCP
    config pointing at `WOLF_MCP_URL` with the token referenced as `${WOLF_MCP_TOKEN}` — the
    sandbox resolves `${VAR}` references from the forwarded MCP env (O8).
  - `prompts/researcher-preamble.md` is a **template** into which W9 injects the locked spec. It
    states the replace-vs-append rule per metric `source`, forbids `allow_shrink` without a
    research note, mandates a single retry on a `dataset_put` CAS conflict, and states that the
    researcher may **propose** an amendment but never enact one and never write a `hypothesis`,
    `hypothesis-spec` or `verdict` memory.
  - `prompts/critic.md` states that it may rewrite only the **method body**, never the preamble,
    and requires a rationale on every `worker_prompt_write`.
  - `prompts/interviewer.md` states that it cannot mark a hypothesis live and that a human must.
- **TDD:** no for the Dockerfile and prompts; **yes** for the bootstrap script's idempotency.
- **Validation:**
  - `docker build -f installations/wolf/Dockerfile -t agent-wolf:dev installations/wolf`
  - `docker run --rm agent-wolf:dev python3 -c "import pandas, numpy, duckdb; print('ok')"`
  - `cd api && yarn test scripts/bootstrap-project`
- **Depends on:** W7
- [ ] done
- Notes:

### W13: UI — board, new hypothesis, chat frame   [Status: pending | Model: sonnet]
- **Scope:** `HypothesisList`, `NewHypothesis`, `Archive`, `OrangeChatFrame`, `StatusChip`,
  `TamperWarning`, and the app shell with routing and sign-in.
- **Repo:** agent-wolf
- **Files:** `web/src/pages/{HypothesisList,NewHypothesis,Archive}.tsx`,
  `web/src/components/{OrangeChatFrame,StatusChip,TamperWarning}.tsx`, `web/src/App.tsx`, tests.
- **Acceptance criteria:**
  - The board renders from a single `GET /api/hypotheses` response with no per-card follow-up.
  - `OrangeChatFrame` refreshes its embed token at **T-120s** and remounts; the token is held in
    memory only and never written to `localStorage` or `sessionStorage`.
  - The Go Live button is disabled with the blocking reasons listed until the spec validates.
  - All six lifecycle states render a distinct, labelled chip.
  - A hypothesis with a `tamper` field renders a visible warning naming the writer — this is a
    security signal and must not be a subtle icon.
  - The Archive page shows `restated_from` lineage when present.
- **TDD:** yes for the token-refresh timing, the Go Live gate and the tamper banner; no for layout.
- **Validation:** `cd web && yarn test && yarn typecheck`
- **Depends on:** W11
- [ ] done
- Notes:

### W14: UI — detail, scoreboard, conditions and verdict   [Status: pending | Model: sonnet]
- **Scope:** `HypothesisDetail` with the scoreboard, the condition table, the timeline and the
  human verdict/amendment actions.
- **Repo:** agent-wolf
- **Files:** `web/src/pages/HypothesisDetail.tsx`,
  `web/src/components/{Scoreboard,MetricChart,Timeline,ConditionTable}.tsx`, tests.
- **Acceptance criteria:**
  - `ConditionTable` shows every condition's state (`tripped`/`holding`/`indeterminate`), its
    computed value, its threshold and its window. **`indeterminate` is visually distinct from
    `holding`** — "we could not tell" must never read as "it is fine".
  - A metric whose latest tick failed renders as **stale with its reason**, never as a flat line
    or a silent gap.
  - Expected direction is shown beside the actual move for every metric.
  - `support_score` is labelled as a summary and carries an explicit note that it does not decide
    anything — only conditions do.
  - A `challenged` hypothesis shows the reason (`condition_tripped` vs `horizon_reached`), the
    agent's case, and both verdict buttons. No other state shows them.
  - Confirming or invalidating requires a rationale and refuses to submit without one.
  - Status comes from the hypothesis memory, never from Orange's delivery status — a delivery
    parked at `awaiting_human` never clears, which is a known Orange wart.
  - Amendment proposals render as proposals with accept/reject actions; accepting calls
    `/amend`, which is what makes the change trusted.
- **TDD:** yes for the condition/stale/verdict-gate logic; no for layout.
- **Validation:** `cd web && yarn test && yarn typecheck`
- **Depends on:** W13
- [ ] done
- Notes:

---

### X1: End-to-end verification   [Status: pending | Model: opus]
- **Scope:** Prove the whole product works against both stacks in mock-model mode: create a
  hypothesis, run the interview, go live, run a tick, confirm a dataset was written, confirm the
  chart renders, trip a condition, record a human verdict, verify teardown.
- **Repo:** agent-wolf (with agent-orange running alongside)
- **Files:** `e2e/run.sh`, `e2e/playwright.config.ts`,
  `e2e/features/{hypothesis-lifecycle,dataset-roundtrip,tamper-resistance}.spec.ts`,
  `e2e/mock/{interview,tick}.json`.
- **Acceptance criteria:**
  - Runs **offline** against the mock model — no `ANTHROPIC_API_KEY`, no billable call. The
    boot-log line proving mock mode is asserted, per `README-stack.md`.
  - The mock-model scripts are deliverables of this ticket, not assumptions: one drives the
    interview to a valid spec, one drives a tick that calls `series_fetch`, writes a CSV and calls
    `dataset_put`.
  - The schedule is created with `WOLF_SCHEDULE_CRON="* * * * *"` — there is **no force-fire
    route**; the scheduler fires on cron minutes only.
  - After the tick, `GET /agent/datasets/<id>-<metric>` reports version ≥ 1 and the detail page
    renders points.
  - `tamper-resistance.spec.ts` covers **both** attacks: (a) a forged
    `kind=hypothesis, status=confirmed` memory appended from a session, and (b) a
    `retracts=<trusted-id>` memory appended from a session. In both cases the board must still
    show the real status **and** display a tamper warning naming the writer. Attack (b) is the one
    that defeated revision 2's design; a suite that only covers (a) proves nothing.
  - After the verdict: the schedule is gone, the worker is gone, the session is gone, **and the
    dataset is still readable**.
  - The run cleans up after itself. No leftover sessions hold ports — including the per-tick job
    sessions, not just `hyp-<id>`.
- **TDD:** no (verification is the deliverable).
- **Validation — all four must pass:**
  - `cd go && go build ./... && go vet ./... && go test ./...`   *(agent-orange)*
  - `cd api && yarn typecheck && yarn test`   *(agent-wolf)*
  - `cd web && yarn typecheck && yarn test`   *(agent-wolf)*
  - `./e2e/run.sh`   *(agent-wolf)*
- **Depends on:** O10, W14
- [ ] done
- Notes:

---

## Dependency graph

```
  AGENT-ORANGE
  O1 ─┬─ O2 ── O3 ─┬─ O5 ─ O6a ─ O6b ─ O9 ──┐
      │            │                   ├── O10
      │            └─ O8               │
      └─ O4 ────────────────────────── │
                                       │
  O7 ── O11 ───────────────────────────┘
         │
         └──────────────┐   (O11 is also a hard dependency of W5)
                        │
  AGENT-WOLF            ▼
  W1 ─┬─ W2 ────────┐   │
      │             ├── W5 ── W8 ──┐
      ├─ W3 ── W4 ──┘              │
      │                            W9 ─┬─ W10
      └─ W6 ── W7 ── W12 ──────────┘   └─ W11 ── W13 ── W14

  X1 depends on O10 and W14.
```

Three tickets can start immediately with no dependency: **O1, O7, W1**.
**O7 is on the critical path for Wolf's state layer** and has no dependencies — start it early.

---

## Discovered Issues Log

Seeded from an adversarial review of revision 1 (2026-08-20, Fable at xhigh effort). Every entry
below was independently verified against the repo before revision 2 was written. Executors append
new entries here; do not edit existing ones.

| # | Finding | Where it landed in revision 2 |
| --- | --- | --- |
| **R1** | **Wolf cannot write memories.** `go/httpapi/httpapi.go:84` "Read-only by design"; `memories.go:43` "there is no POST counterpart". Revision 1's entire hypothesis store was built on a route that does not exist. | New ticket **O7**; `MemoryStore` gains `CreateMemory`; § "The trust model" |
| **R2** | **"Human decides" was enforced by nothing.** Memory is an open bus with no origin check; any session — including one that read a prompt-injected web page — could append `status=confirmed`. | § "The trust model"; W5's provenance rule; X1's `tamper-resistance` spec |
| **R3** | **Invalidation conditions were prose.** `"drawdown_pct > 25 sustained 30d"` had no parser, no evaluator and no ticket. The only possible reader was the researcher model judging its own trial. | § "Condition semantics"; new ticket **W4**; W3's typed schema |
| **R4** | **`weight` was validated and then consumed by nothing.** | The support score, § "Condition semantics"; W4 |
| **R5** | **Nothing ever triggered `live → challenged`,** and `horizon_days` fired nothing. Orange cannot call Wolf. | New ticket **W10** |
| **R6** | **Session containers cannot reach wolf-api** on the ordinary compose network: `agentd` shares DinD's netns and nested containers resolve no compose DNS. | § "Local topology and networking"; W1's `network_mode`; W7's URL origin |
| **R7** | **`download_url` built on the public base would be unreachable** from a nested container. | O6 acceptance criterion + O9's in-container curl |
| **R8** | **The critic could silently redefine derived metrics,** splicing incomparable points into a series the conditions test. | `method` object required in the spec (W3); locked preamble vs mutable method body (W9, W12) |
| **R9** | **"Datasets are never torn down" contradicted the 30-version reaper** — the evidence behind a verdict would be reaped ~30 days later. | Evaluation snapshots embedded in trusted memories (W9, W10) |
| **R10** | **`replace-whole` had no shrink guard**: a truncated provider response would replace a year of data, and CAS cannot object because it is the same writer. | `row_count` column (O1); shrink guard (O6); O9 test |
| **R11** | **O2's validation could not prove O2's criteria** — the store is Postgres-only, so the non-Postgres run exercises nothing. | O2's validation now requires `AGENTKIT_TEST_POSTGRES_URL` |
| **R12** | **`TestMutationsAreLogged` was run against the wrong package** and passed vacuously; it lives in `go/agentdb/config_events_test.go:594`. The "exempt datasets if the sweep objects" contingency was dead code — the sweep classifies by noun. | O6's validation and its comment |
| **R13** | **`dataset_put`'s blob key was unspecified;** keying by attempted version lets two racers collide, and the CAS winner points at the loser's bytes. Orphan blobs after a crash were unreclaimable. | O6 (unique per attempt, delete-on-conflict); O3 (`ListOrphanBlobPaths`) |
| **R14** | **Metric slugs bounded at 63 would overflow the dataset name** (`<hyp-id>-<slug>`) and fail every tick forever. | W3 bounds slugs at 50 |
| **R15** | **"Retention as a project setting" was a hidden second ticket** — `ProjectSettings` is a typed struct needing its own migration and console surface. | O3 uses an env knob only |
| **R16** | **Teardown left a queued delivery** able to dispatch after the worker was deleted; and hypothesis state had no concurrency control. | W9 drains deliveries first; W5 serialises transitions per id |
| **R17** | **X1 could not "force a scheduled tick"** — no such route exists — and the mock-model scripts were unticketed. | `WOLF_SCHEDULE_CRON` (W9); mock scripts are X1 deliverables |
| **R18** | **The `download_url` bearer token DOES cross the model context**, transcripts and `worker.finished` subscribers, contradicting the headline claim as stated. | Caveat box in § "The dataset atom"; O6 tool description; O10 docs |
| **R19** | **Project-map, `frame-ancestors` and MCP-credential forwarding were unowned.** With any map configured and no `allowed_origins` entry, Wolf's iframe is blocked outright. Compose forwards only variables it names by line. | O8 |
| **R20** | **The owner-slug mapping handled only `@`** (`kai+test@gmail.com` produces an illegal label); `/auth/verify-google` 404s unless `GOOGLE_CLIENT_ID` is set on Orange; archive-and-relaunch was a free scoreboard re-roll. | W5's total mapping; W8's config error; `restated_from` label |
| **R21** | **Wolf cannot raise an attention request.** `request_human_attention` is an MCP tool callable only from inside a container; over HTTP attention is read-only (`GET /agent/attention-requests`, `go/httpapi/httpapi.go:319`). Revision 2's first draft of W10 assumed a callable path. *(Found during revision-2 self-review.)* | W10's notification criterion |
| **R22** | **W9 composed a prompt from a template W12 had not yet authored.** Missing dependency. *(Found during revision-2 self-review.)* | W9 now depends on W8 **and** W12 |

### Revision 3 — second adversarial review + executability audit (2026-08-20)

| # | Finding | Where it landed in revision 3 |
| --- | --- | --- |
| **R23** | **CRITICAL — retraction bypassed the entire trust model.** `notRetractedSQL` (`go/agentdb/memories.go:284-288`) hides a row if *any* memory carries `retracts=<its id>`, **without checking the retractor's provenance**, and it is applied to both paths Wolf reads (`:353`, `:407`). A prompt-injected session did not need to forge a status — it could erase the real one, and nothing would surface. Revision 2 defended only against forged *newer* rows. | § "The trust model" → "Retraction"; new ticket **O11**; W5's criteria and dependency; X1's `tamper-resistance` attack (b) |
| **R24** | **The "only O7 produces empty provenance" invariant was false.** `ApplyTopology` also writes provenance-free memory seeds (`go/agentdb/topology_apply.go`, `go/topology/topology_test.go:242-243`), reachable at `POST /agent/topologies/apply` by any API-class credential — including an embed token, which is not scope-confined off session routes. | Invariant restated: trusted = empty provenance **AND** Wolf's own label vocabulary **AND** a matching `hyp-<id>` session |
| **R25** | **`nowMs` was in W4's signature and in none of its rules.** Anchoring the sustained window on `t_last` means a feed that died mid-trip keeps tripping forever on ancient data, and the coverage floor cannot catch it. Two implementers would have shipped opposite products. | Window anchors on `nowMs`; `staleness_days` for the single-observation case; a mandatory fixture in W4 |
| **R26** | **`change_pct`/`drawdown_pct` were undefined for `R <= 0`.** The `R == 0` rule was written narrowly for `ratio_to`. FRED macro series go negative routinely — a negative reference silently inverts the comparison. | One rule for every dividing statistic; `indeterminate` with reason `non_positive_reference`; new `change_abs` statistic for zero-crossing series |
| **R27** | **Mixed-frequency `ratio_to` was permanently indeterminate.** A hard-coded 7-day lookback against a monthly FRED series resolves ~7 days in 30, which the coverage floor then rejects forever. | `ratio_lookback_days` required and validated; interviewer prompt guidance |
| **R28** | **O7's "session token → 403" was unachievable, and the embed-token discriminator was unnamed.** Session tokens use a separate secret and are rejected **401 at the middleware** by the `sid` lock (`go/cmd/agentd/auth.go:104-118`). Embed tokens *do* reach the handler. *(This also means `docs/19-embedding.md`'s hazard H12 is stale.)* | O7's criteria corrected; `Identity.SessionScope` named; O10 gains the doc fix |
| **R29** | **The one-request board could not carry `support_score`.** It is computed from datasets, the board read returns a 500-byte snippet with no content, a negative score is an illegal label value, and a 5-minute re-write would append ~14k memories a day. | § "Where the board's numbers come from": a `kind=evaluation` trusted memory with a parseable summary line, written only on change or every 20h; second `latest_per` call |
| **R30** | **W9's teardown asked for an operation Orange does not expose, in an order that could not terminate.** There is no delivery-cancel route, `DeliveryQuery` has no `worker` field, and draining before deleting the schedule is unbounded because the scheduler keeps minting deliveries. Tick sessions also had no owner. | W9 rewritten: schedule first, then bounded poll-and-wait, then worker, then session; client-side worker filter; tick-session sweep in W9 and W10 |
| **R31** | **O6 had no size cap and inherited an ignored exit code.** `execAndCollect` buffers exec output with no limit (`go/execenv/docker/client.go:238-240`) and the artifact path checks only `err != nil`, never `ExitCode` (`go/runner.go:2824-2827`) — so `cat missing.csv` would have stored an empty dataset. Several seams were also unnamed. | O6 gains `AGENTKIT_DATASET_MAX_BYTES`, a `wc -c` probe, `test -f` + exit-code checks, and an explicit seam list |
| **R32** | **Systematic executability gaps.** No HTTP framework, MCP implementation, mock style or chart library was named; "typed errors" were asserted with no shared module; no git conventions; and the dependency graph implied O2/O3 (one file) and O5/O7 (one file) could run in parallel. | § "Pinned technology choices"; § "Shared error taxonomy"; § "Parallelism and file ownership"; § "Git conventions"; `errors.ts` in W1 |

| **R33** | **O6 was one ticket doing three jobs** — three MCP tools, the Exec-pull pipeline, CAS orchestration, path hardening and blob-key uniqueness. An executability audit judged it 2–3 tickets of surface. *(Split before execution, 2026-08-20.)* | Split into **O6a** (the pull pipeline as a tested internal function) and **O6b** (the three tools on top of it) |

### Confirmed correct — do NOT "fix" these in a later revision

- **Teardown order** (schedule before worker) and its justification: verified at
  `scheduler.go:363-371`, `dispatch.go:246-248`, `scheduler.go:768-790`.
- **The ms-vs-seconds split.** `memories` uses `UnixMilli`, `agent_*` uses `Unix()`. Matching the
  nearest neighbour is right. Do not unify.
- **Iframe-only reuse.** `examples/web/vite.config.ts:21-38` confirms `web/` is not installable.
  Do not attempt to package it.
- **`latest_per` exists and does what is claimed** (`go/httpapi/memories.go:159`), and search
  results **do** carry provenance (`go/agentdb/memories.go:113-121`) — which is what makes the
  one-request board read compatible with the trust model.
- **Exec+cat is the only file seam** (`go/runner.go:2733-2736`, `go/execenv/execenv.go:54`).
- **Refetchable-replace vs derived-append is domain-correct.** Dividend re-adjustment and FRED
  restatement are real. A reviewer demanding append-only everywhere would be wrong.
- **`@daily` really is refused**; the `404`-parity, `501`-off-Postgres and `410`-gone vocabularies
  are the house style and are copied deliberately.
- **`POST /agent/session` does accept `worker`** (added 2026-08-08). `docs/19-embedding.md`'s line
  about HTTP-created sessions having no `worker` column refers to the column, not the request
  field — W8 is correct as written.
- **Datasets deliberately write no config event.** They are data, not configuration.
- **`BlobStore` has `Delete`** — the reaper is implementable with no new seam.
- **The DinD topology in § "Local topology" is verified sound.** A service on
  `network_mode: "container:<dind>"` binding `:8100` reaches agentd at `localhost:8099`, is
  reachable from nested containers at `172.17.0.1:8100`, and from wolf-web at `dind:8100`. No
  collision with `8099` or `2375`. Two caveats now stated in that section: the container name
  hard-binds to Orange's compose project name and dind must already be up (compose `depends_on`
  cannot order across projects), and a `container:`-mode service has no compose-DNS membership —
  which is fine here, but do not try to give wolf-api a service alias.
- **Non-`ratio_to` conditions need no cross-metric timestamp alignment.** Each names exactly one
  metric. Do not invent alignment where none is required.
- **`Exec` + `cat` is binary-safe.** `stdcopy.StdCopy` preserves bytes including NULs, so O9's
  byte-identical assertion holds. The gap was size (R31), never fidelity.
- **Poller request volume is fine.** 50 hypotheses × 5 metrics every 5 minutes is ≈1.7 req/s and
  no rate limit exists to trip. R29/R10's caching is about waste, not about a stampede.

### Entries added during implementation

*(executors append below this line)*

| # | Finding | Disposition |
| --- | --- | --- |
| **R34** | **"Exactly as written in Interfaces" collides with the repo's own migration convention.** The Interfaces SQL for `045_datasets` uses bare `CREATE TABLE` / `CREATE INDEX`; every migration 001–044 without exception uses `IF NOT EXISTS`, and `go/agentdb/migrations_concurrent_test.go:158-165` deletes the newest migration's tracking row and re-runs it, so a bare `CREATE TABLE` would fail that test. O1's executor added `IF NOT EXISTS` and both its verifier and the orchestrator agreed. **Later tickets quoting Interfaces SQL must be read as "these columns, types, defaults and constraints", not as a byte-for-byte transcription.** | O1 shipped with `IF NOT EXISTS`; O2/O3 executors should assume the same reading |
| **R35** | **O1's Validation could not prove O1's first acceptance criterion.** The ticket lists only `go build && go vet`, which cannot execute SQL, while criterion 1 is "applies cleanly on a fresh database and is idempotent on re-run". Both the executor and the verifier flagged it independently. Proven instead by `go test ./agentdb/... -run 'TestLivePG_MigrationsApplyAndAreIdempotent'` against the throwaway (PASS, no SKIP). **Any ticket whose criteria describe runtime behaviour must list a Validation command capable of exercising it.** | O1 verified against live Postgres; O2/O3/O7/O11 already require the URL |
| **R36** | **The `POST /agent/memories` success status is unspecified.** O7 chose **`201 Created`** (`go/httpapi/memories.go:492`). Interfaces shows the response body but names no code. **W2, W5, W8, W9 and W10 must expect 201**, not 200. | Fixed by fiat: the route returns 201 |
| **R37** | **The `/api` prefix contract between wolf-api and its two proxies was never stated,** and that single ambiguity caused W1's entire fix round 1. Settled: **wolf-api serves the literal `/api` prefix and no proxy rewrites it** (nginx `proxy_pass` carries no URI part, matching `deploy/web.nginx.conf:55`). W8, W9 and W11 must mount routes under `/api` themselves. | Fixed in W1 commit `49324c2`; recorded here so it is not re-litigated |
| **R38** | **W1's import-boundary criterion is one sentence covering an open-ended problem, and it failed three verification rounds in a row** — each time on a different escape the sentence did not enumerate (dynamic `import()`/`require`, prefix-vs-path containment, Vite `resolve.alias`, tsconfig `paths`, then `.jsx`/`.js` sources and absolute-path specifiers). This is a **plan defect, not an executor defect**: the criterion names no file set and no specifier taxonomy. **A criterion of the form "a test that catches any X" must enumerate the X it is graded on.** | **ESCALATED to the owner** — W1 remains open on this one criterion |
| **R39** | **The shared error taxonomy has no kind for "the server has a bug".** § "Shared error taxonomy" fixes six kinds and forbids inventing a seventh; none means *unhandled internal error*. W1 mapped it to `unavailable`, which is the one **retryable** kind — so a genuine 500 would be retried forever. Every later ticket with a catch-all handler faces the same forced choice. | **Open** — needs an owner decision: add a seventh kind, or state that unclassified errors carry no `kind` |
| **R40** | **vitest 4 and vite 5 are peer-incompatible, and the plan pins one without the other.** § "Pinned technology choices" pins vitest and pins React/MUI to "same versions as `examples/web`" (which implies vite 5), giving no tie-breaker. Yarn resolves it by nesting a second vite 8 under vitest, so `web/vite.config.ts` is evaluated by vite 8 under `vitest run` and vite 5 under `vite build`. Green today; a latent split-brain for W13/W14. | **Open** — needs a version decision recorded in the pinned table |
| **R41** | **Two W1 criteria are self-referential and cannot fail an under-implementing executor.** ".env.example documents every variable `api/src/config.ts` reads" is satisfiable by writing a `config.ts` that reads nothing (W1 authors both files), and no test ties them together — while eight later tickets add config. Similarly the topology criteria assert runtime reachability but the only Validation command is `docker compose config`, which proves the declaration and nothing else; W1's fix-round-1 nginx bug survived the first pass for exactly that reason. | **Open** — X1 should own the reachability proof; a `.env.example` coverage test would close the first |
| **R42** | **Branch topology: parallel tickets inherit each other.** `O7-memory-append-route` was cut while O1 was in flight and therefore contains O1's commit `2bda0e8` as well as its own. § "Git conventions" says one branch per ticket from `main`; § "Parallelism" blesses O1 ‖ O7 without saying how two parallel branches stay disjoint. Harmless here (gates green with both present, no O7 test touches datasets) but **merging O7 merges O1's migration too**. | Recorded; orchestrator to merge in dependency order |
| **R43** | **`172.17.0.1` is asserted as fact but is not stable.** § "Local topology and networking" states the DinD inner `docker0` gateway as `172.17.0.1` and W1 repeats it in an acceptance criterion; W1's verifier reproduced it becoming `172.18.0.1` when that subnet was already taken. The plan gives no guidance on whether Wolf should discover the gateway rather than hard-code it. | **Open** — affects W7's `WOLF_MCP_URL` and O8's forwarding |
| **R44** | **`docs/19-embedding.md:415` ("Read-only. There is no HTTP write or delete") is a live lie on the O7 branch** until O10 lands, and nothing stops O7 merging first. O7's Files list is code-only and defers the doc fix to O10 by parenthetical. | Recorded; O10 must land before or with any merge to `main` |

### Executability audit, 2026-08-21 — READ BEFORE DISPATCHING ANY TICKET

After wave 1, six independent auditors reviewed the 24 unbuilt tickets for the defect shapes wave 1
actually cost us. They found **105 findings, 35 blocking (26 distinct after dedup), across 21 of the
24 tickets — none clean**. The verdict on "are the remaining tickets safe to run at scale as
written" is **no**, and the reasons are recorded in full in
**`design/2026-08-21-agent-wolf-executability-audit.md`**, which lists what must be fixed before
each wave and the seven questions needing an owner decision.

Two of the blocking findings are not executability problems but **live defects in the design**:
a dataset scoped token presented as `Authorization: Bearer` would authenticate as an unrestricted
project principal on every `/agent/*` route (audit B1), and O6a's mandated `sh -c` pattern is
command injection from inside a container (audit A8). Neither is fixed by this plan as it stands.

**Do not dispatch O2, O3, W3 or W6 until that document's "Before wave 2 starts" list is closed.**

### Owner decisions, 2026-08-21

Taken by Kai after wave 1. These close four entries above; the plan text has been edited in place
and **the edits are the authority** — these lines only record that a choice was made and why.

| Entry | Decision | Where the plan changed |
| --- | --- | --- |
| **R38** | Rewrite the criterion to enumerate exactly what the boundary test is graded on, then one targeted fix round. The implementer's three fixes were each correct; an open-ended criterion is a trap, not a specification. **A criterion of the form "a test that catches any X" must enumerate the X.** | W1's boundary criterion, replaced with an explicit file-set / specifier-form / specifier-kind / config-escape / manifest-escape list; `api/` brought under the same rule |
| **R39** | Add a seventh kind, **`internal`** — not retryable, HTTP 500, message never passed through to the client. Mapping a server bug to `unavailable` would make W10's poller retry a crashing endpoint every tick forever. | § "Shared error taxonomy" + a new W1 criterion with a leak test |
| **R40** | Raise `web/` to **vite 6** and keep vitest 4. `web/` deliberately diverges from `examples/web`'s vite 5; React/MUI still match, since those are what the visual language depends on and vite is a build tool. | § "Pinned technology choices" gains a Vite row; W1 asserts the nested `vitest/node_modules/vite` is **absent** |
| **R43** | **Discover the DinD gateway at boot**, fall back to `172.17.0.1`, log which was used; an explicit `WOLF_MCP_URL` always wins. Discovery lives in one place (`api/src/config.ts`) and W12's bootstrap consumes it. | § "Local topology and networking" + a new W1 criterion |

Also folded into W1 while it was open, from findings the first two rounds raised but that were
graded minor: the `.dockerignore` (host `node_modules` overwriting the installed tree inside the
image), SSE-safe directives on nginx's `/mcp` location, exact `18.3.1` React pins, and a Validation
step that **proves** the `/api` prefix survives the proxy rather than reading the config
(**R41** — the omission that let fix round 1's bug through the first pass).
