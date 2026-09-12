# Onboarding, the Guide and the Budget — executable work plan

> ## ⏸ RESUME HERE — fleet paused 2026-09-11 (Kai's call, mid wave 1)
>
> **State of `main`** (all local, nothing pushed): design + plan committed; **A1 (revert on the
> Activity rail), A3 (operator claim, budget guard, default budgets, `/agent/whoami`) and A6
> (project-map reload) are merged**. After the A3+A6 merge `go build ./... && go vet ./...` passed;
> **the full `go test ./...` has NOT been re-run on the merged tree** (each branch was green alone).
> `docs/guide/` (16 pages + field notes + glossary + README) is committed **after the B8 editorial
> pass** — the editor finished despite the stop signal and its edits landed in commit `b1af8b3`. The
> one broken link in the folder is `for-operators/inviting-someone.md`, which A7 writes.
>
> **Five branches hold finished-but-unverified work, committed as `WIP (fleet paused …)`** by the
> orchestrator when the agents were stopped one step before their own commit. Each agent had
> reported its tests passing and was typechecking/committing. Do not trust that; re-run each
> ticket's Validation first, then merge:
>
> | Ticket | Branch | Last agent status |
> |---|---|---|
> | A2 interview survives navigation | `worktree-agent-aa205520f26ade00b` | 59 unit tests passing, re-running typecheck |
> | A4 usage route | `worktree-agent-a31d868c69d559f1e` | waiting on full `go test` |
> | C1 guide route + renderer | `worktree-agent-a336db1df724ecdb8` | Docker build of `deploy/web.Dockerfile` succeeded, finishing web gate |
> | C3 chat empty state + copy fixes | `worktree-agent-a165e672dbaeb7334` | web build green, typechecking `examples/web` |
> | C4 write a note from Memory | `worktree-agent-a8928fdf78ef2c1d3` | about to commit |
>
> The worktrees are under `.claude/worktrees/agent-<id>/`. A2 and C3 both touch `DeskPage.tsx`,
> `Sidebar.tsx` and `App.tsx`; C1 also touches `App.tsx` — expect small merge conflicts there.
>
> **Resume procedure, in order:**
> 1. `cd go && go test ./...` on `main` (≈8 min; `agentdb` alone is ~470 s). Fix before anything else.
> 2. For each branch in the table: spawn one sonnet agent in that worktree with the instruction
>    "finish ticket <id>: re-run its Validation, fix, amend the WIP commit into a real one, report".
>    Merge each green branch into `main` (A4 first, then A2, C1, C3, C4), running the web gates
>    after the web merges.
> 3. Wave 2: A5 (budget panel; depends on A3+A4 merged), A7 (operator docs), C2 (About this
>    screen; depends on C1), C5 (Desk narrates firsts; depends on C1+C2), (B8 is done; no editor needed until B7 changes pages).
> 4. Wave 3: D1, D2 (stack e2e — needs port 8080 free and Docker), B7 (Ellen fixture + screenshots).
> 5. Append every agent's Notes to its ticket and every finding to the Discovered Issues Log below.
>
> **Open decision for Kai:** the default daily token limit (env `AGENTKIT_DEFAULT_DAILY_TOKENS_HARD`;
> hole marked `<!-- Kai: default daily limit -->` in `docs/guide/money.md`). Ships off until set.
>
> **First message to send on resume:** *"Resume the onboarding fleet: read the RESUME HERE block at
> the top of design/2026-09-11-onboarding-work-plan.md and continue from step 1."*

> **EXECUTION RULES (for agents):** Work ONE ticket at a time. Only the orchestrator changes a
> ticket's Status or ticks its box; executors may only append to the ticket's **Notes** and to the
> **Discovered Issues Log** at the end of this file. A ticket's box is ticked only after its
> Validation commands have been re-run by the orchestrator and pass. Do not expand scope; log
> surprises in the Discovered Issues Log instead. Every claim you make in a Note must be something
> you ran or read, with a `file:line` or a command.

Status: **approved for execution** (2026-09-11, Kai: "full autonomy and as much parallelism as
possible"). Design: `design/2026-09-11-onboarding-and-the-guide.md` — read its §0, §3, §4.1, §5.1
and §6 before touching anything; this file does not repeat the reasoning, only the work.

Decisions made by Kai on 2026-09-11 that this plan implements:

- **The token budget is first-class.** Visible to everyone in a project; **changeable only by the
  operator** (Kai); a low default on every new project; usage and cost reported in the console.
- **Hosting bills the API key, not the subscription.** The subscription can carry only so many
  bots; the metered key is what a per-project budget can actually bound. The ops doc and the
  console's credential badge must make the mode unmistakable.
- **No Stripe, no per-user billing.** One key, per-project limits, honest reporting. That is the
  whole billing story for invited friends.
- **Claude drafts the guide; Kai edits.** Ellen's bookshop is the running example.
- **Topology seed buttons are hidden while a project is in its interview.** The architect is the
  designed default; the seeds are for people who already know what team they want.

---

## 0. How the fleet is organised

Four streams. Tickets inside a stream that share no `Depends on` run in parallel.

| Stream | What | Where the agents work | Commits |
|---|---|---|---|
| **A — safe to invite** | the four blockers + the budget | one git worktree per ticket, branch `fleet/<ticket>` | commit on the branch; orchestrator merges |
| **B — the guide** | 16 pages + glossary + operator page, Ellen fixture | the main working tree, `docs/guide/` only, disjoint files | no commits; orchestrator commits |
| **C — console wiring** | the guidance layer G2–G9 | worktrees, branch `fleet/<ticket>` | commit on the branch; orchestrator merges |
| **D — integration** | merge, full gates, stack e2e, screenshots | main tree, orchestrator + one agent | orchestrator |

**Waves.**

- **Wave 1 (now, in parallel):** A1, A2, A3, A4, A6 · B1–B6 · C1, C3, C4.
- **Wave 2 (as dependencies clear):** A5, A7 · B8 · C2, C5.
- **Wave 3:** D1, D2, B7.

**Model:** every executor is `sonnet` unless a ticket says otherwise. The orchestrator is the
session that owns this file.

**Two rules that have bitten this repo before, restated for the fleet:**

1. *Storage is not delivery* (OM-9). A test that reads back what it just wrote is a storage test.
   Assert on the behaviour switch: the button that appears, the 403 that is returned, the row that
   renders.
2. *A fixture must be captured from a real writer, not authored to match the reader*
   (`docs/product/22-readiness.md:37-39`). Nothing in `docs/guide/` shows a prompt, memory,
   charter or changelog that was not produced by the code or lifted verbatim from a shipped prompt.

**Gates every engineering ticket runs before reporting** (from the repo's own CI):

```sh
cd go && go build ./... && go vet ./... && go test ./...       # Go
cd web && npm ci && npm run typecheck && npm test               # component library
cd web && npm run build && cd ../examples/web && yarn install --frozen-lockfile && yarn typecheck  # shell (web must be built first)
```

`agentdb`'s live-Postgres cases skip without `AGENTKIT_TEST_POSTGRES_URL`; tickets that touch SQL
say so and use `./stack test-go` where a database is available. Stack e2e (`e2e/features/*.stack.spec.ts`)
is **written by the ticket and run by D2**, because it needs the compose stack and the stack takes
one host's port 8080 — do not bring the stack up from a worktree.

---

## 1. Interfaces this plan adds (so parallel tickets agree)

### 1.1 The operator claim (A3)

- JWT claim `operator: true` on project tokens minted for: a wildcard login's per-project tokens
  (`writeLoginResponse`, `googleauth.go:391`) and the wildcard exchange (`authProjectTokenHandler`,
  `googleauth.go:552`); the test login (implicit wildcard). **Not** on a non-wildcard Google
  account's tokens. Not on embed tokens.
- `principal.operator bool` in `go/cmd/agentd/auth.go`, set from the claim; **also true** for API-key
  principals (`auth.go:131`) and the dev-open principal (`auth.go:170`).
- `httpapi.Identity.Operator bool`, filled by `identityFromRequest` (`auth.go:246-254`).
- `GET /agent/whoami` → `{"email": "...", "project": "...", "operator": true|false}`. Inside the
  JWT middleware like every `/agent/*` route.

### 1.2 Operator-only settings fields (A3)

`PUT /agent/project-settings` compares the incoming body against the stored row. If any of
`daily_tokens_soft`, `daily_tokens_hard`, `max_concurrent_jobs` differs and `Identity.Operator`
is false → `403 Forbidden` with body `"only the operator may change budgets and caps"`. Everything
else on the route is unchanged (whole-object write, rationale required by the store).

### 1.3 Default budgets (A3)

Env `AGENTKIT_DEFAULT_DAILY_TOKENS_HARD` and `AGENTKIT_DEFAULT_DAILY_TOKENS_SOFT` (int64 tokens;
unset or `0` = today's behaviour, off). Applied in `DefaultProjectSettings` for a project that has
**no stored row** — so every new project starts braked and existing projects are untouched.
Plumbed from `main.go` into `agentdb` through one setter or a field on `Store`, not a package
global read in `agentdb` (the engine must not read env). Compose forwards both; `.env.example`
documents them with a suggested low value.

### 1.4 Usage (A4)

`GET /agent/usage` → 

```json
{
  "project": "sams-shop",
  "day_starts_at": 1789000000000,
  "today":    {"input_tokens": 0, "output_tokens": 0, "cost_usd": 0.0, "queries": 0},
  "last_7d":  {"input_tokens": 0, "output_tokens": 0, "cost_usd": 0.0, "queries": 0},
  "last_30d": {"input_tokens": 0, "output_tokens": 0, "cost_usd": 0.0, "queries": 0},
  "budget":   {"daily_tokens_soft": 0, "daily_tokens_hard": 0},
  "credential_mode": "api-key" | "subscription" | "mock",
  "cost_known": true
}
```

- Tokens use the exact per-envelope expressions in `go/agentdb/token_usage.go`
  (`usageInputSQL`, `usageOutputSQL`, `usageEnvelopes`) — do not write a third reader.
- `cost_usd` sums `e->'data'->>'totalCostUsd'` (captured shape, `token_usage.go:20-40`) with a
  `total_cost_usd` snake_case fallback like the token fields. `cost_known` is false when tokens > 0
  and cost == 0 (a transport that reports no cost).
- "today" starts at stack-local midnight, the same `time.Location` the router's `tokenBudget` uses
  (`router.go:690-712`) — read it from the same place, do not compute a second midnight.
- Postgres-only like the budget; on sqlite the route returns `501`.
- `credential_mode` comes from the same `credentialMode(apiKey, oauthToken)` the badge reads
  (`main.go:544`).

### 1.5 The guide's on-disk contract (B, C1, C2)

- Pages live at `docs/guide/<slug>.md`, slugs exactly as in the design §4.2. Front matter:

  ```yaml
  ---
  title: A worker's instructions
  slug: a-workers-instructions
  part: 1            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
  order: 3
  surfaces: [workers, worker-configuration]   # console surfaces whose About line uses this page
  ---
  ```

- The first paragraph after the front matter is **the About text**: two to four sentences, no
  links, stands alone. Then the fixed skeleton from design §4.1.
- `docs/guide/README.md` lists the pages in order and states the skeleton and the voice rules.
- Screenshots go in `docs/guide/img/<slug>-<n>.png`, referenced with relative paths. Until B7
  captures them, a page uses an HTML comment `<!-- screenshot: <what it should show> -->`.

### 1.6 Surface ids (C2, C5)

`web/src/guide/surfaces.ts` exports the closed set:
`desk · chat · workers · worker-configuration · worker-triggers · worker-history · memory ·
activity · chart · settings · onboarding · project-create`. A surface id not in the set is a
compile error, the same trick `navReveal.ts:48-53` uses.

---

## 2. Tickets

### Stream A — safe to invite

### A1: revert is reachable from the Activity rail   [Status: merged, e2e pending D2 | Model: sonnet]
- **Scope:** Mount the existing `RevertControl` (`web/src/components/ChangelogView.tsx:324-395`)
  on the `changes` rows of `ActivityPage` (`web/src/components/ActivityPage.tsx`). The row already
  carries the config event; the control needs the entry and the revert block the API returns for
  it — reuse the fetch `ChangelogView` does (`:191`, `:216`, `:289`) rather than a new one; extract
  it into a hook if that is the cleanest way to share it. Keep the confirm dialog and its wording
  ("Revert to this version?", nothing is erased). The action appears only on config-event rows,
  never on events or jobs. Read design §6 PR0 for the evidence trail before starting.
- **Files:** modify `web/src/components/ActivityPage.tsx`, possibly `web/src/activity.ts`
  (the fold may need to carry the entry id); extract from `ChangelogView.tsx` if needed; tests in
  `ActivityPage.test.tsx`; **new** e2e `e2e/features/revert.stack.spec.ts` (create a worker, change
  its description via the API with a rationale, open Activity, revert the newest change, assert a
  compensating config event appears and the description is back; then assert a revert on the
  older of two changes to the same worker is refused with the readable reason).
- **Acceptance criteria:** the button renders on a change row and not on an event row (unit test
  with a fixture fold); the dialog text is unchanged; a non-newest revert shows the refusal
  message. `grep -n "Revert to this version" web/src/components/ActivityPage.tsx` is non-empty.
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm run typecheck && npm test`; the e2e spec typechecks
  (`cd e2e && npx tsc --noEmit -p .` if a tsconfig exists, else `npx playwright test --list`).
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Commit 316ce03 merged to main. `RevertControl` exported from ChangelogView and mounted on `kind === 'change'` rows of ActivityPage; revert blocks computed from the seq-ordered config log (`useActivity.ts`), not the rail's time sort; 3 unit tests; new `e2e/features/revert.stack.spec.ts` (revert newest from Activity, then 409 on the superseded entry). web typecheck green; full `npm test` 1546/1548 with 2 unrelated timeout flakes (DI 2). Stack e2e pending D2.

### A2: the interview survives navigation, and hides the seeds while it runs   [Status: merged, e2e pending D2 | Model: sonnet]
- **Scope:** Replace the `localStorage`-only gate on the onboarding view (`examples/web/src/App.tsx:36`,
  `:56-62`, `:290-294`, `:393-396`, `:446`) with a server-derived state: a project is **in
  interview** when `GET /agent/sessions/by-name/onboard` returns a session **and** no charter has
  been applied. Derive "applied" from what the console already reads for the charter panel
  (`web/src/charter.ts` / `useCharter`); if nothing there says applied, the architect worker's
  existence (`GET /agent/workers`) is the observable, and say so in a comment. While in interview:
  the Desk first-run panel (`web/src/components/DeskPage.tsx:514-547`) shows a
  **"Finish setting up this project"** row that opens the onboarding view; clicking the `onboard`
  session in the sidebar opens the onboarding view, not plain chat; nav clicks no longer clear
  anything; the **"Start from an org chart" / "Start from a topology"** buttons on Desk and Workers
  are not rendered. `onboarding` stays out of `NavEntry`. Keep the `localStorage` pending record
  only as the carrier for the goal text between project creation and the first seed message.
- **Files:** modify `examples/web/src/App.tsx`, `examples/web/src/onboarding.ts`,
  `examples/web/src/Sidebar.tsx`, `web/src/components/DeskPage.tsx`,
  `web/src/components/WorkersPage.tsx` (a prop to hide the seed button); tests beside each;
  **modify** `e2e/features/onboarding.stack.spec.ts`: mid-interview, click Desk, click "Finish
  setting up this project", assert the charter panel and Approve are present; assert the seed
  buttons are absent before approval and present after.
- **Acceptance criteria:** the e2e scenario above; a project with no `onboard` session behaves
  exactly as today (no new row, seed buttons visible); a project whose charter is applied shows no
  "Finish setting up" row.
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm run typecheck && npm test && npm run build && cd ../examples/web && yarn install --frozen-lockfile && yarn typecheck`
- **Depends on:** —
- [x] done
- Notes: (2026-09-11) Paused before the agent's commit; WIP committed by orchestrator as da9471b on worktree-agent-aa205520f26ade00b. Agent's last words: 59 tests pass, re-checking typecheck after a DeskPage change. Files: App.tsx, Sidebar.tsx, onboarding.ts, DeskPage(+test), WorkersPage(+test), onboarding.stack.spec.ts. (2026-09-12) Resumed: the WIP diff needed no fixes; amended to 6dcbcbf "A2: the interview survives navigation, and the seeds stay hidden until a charter lands" and merged as 05a41f1. Orchestrator re-ran the Validation on the merged tree: `npm run typecheck` clean, `npx vitest run --testTimeout=20000` 1552/1552 pass in 81 files, `npm run build` clean, `examples/web` `yarn typecheck` clean (the timeout flag is the DI8 flake, not the ticket). 'No onboard session behaves as today' and 'charter applied hides the row' are code traces, not tests — the e2e scenario at e2e/features/onboarding.stack.spec.ts:147-172 is written and waits on D2. Known gap logged as DI10.

### A3: operator claim, budget write guard, default budgets, whoami   [Status: merged, e2e pending D2 | Model: sonnet]
- **Scope:** Exactly §1.1, §1.2 and §1.3 above. The claim is added where project tokens are
  minted for wildcard holders and the test login; `principal` and `httpapi.Identity` gain
  `Operator`; `PutProjectSettings` (`go/httpapi/project_settings.go:66`) reads the stored row
  first and returns 403 on a non-operator budget/cap change; `GET /agent/whoami` is registered
  next to the other `/agent/*` routes in `main.go`; `DefaultProjectSettings` applies the env
  defaults for projects with no stored row, via a value handed to the store at construction (no
  env reads inside `agentdb`). `docker-compose.yml` forwards the two env vars; `.env.example`
  documents them beside the other `AGENTKIT_*` lines with a suggested value and the sentence
  *"applies to projects created after this is set; existing projects keep what they have"*.
- **Files:** modify `go/cmd/agentd/googleauth.go`, `auth.go`, `main.go`,
  `go/httpapi/httpapi.go` (Identity), `go/httpapi/project_settings.go`,
  `go/agentdb/project_settings.go` (+ the store field), `docker-compose.yml`, `.env.example`;
  **new** `go/httpapi/whoami.go`; tests: `googleauth_test.go` (claim present/absent per mode),
  `auth_test.go` (principal.operator per credential), `project_settings_test.go` in httpapi (403
  vs 200 matrix: operator/non-operator × budget-changed/unchanged), `agentdb/project_settings_test.go`
  (default applied only when no row exists).
- **Acceptance criteria:** the matrix test; a non-operator can still change the project prompt
  and briefing; `whoami` returns `operator:false` for a Google account listed against one project
  and `true` for the wildcard; a fresh project's `GET /agent/project-settings` shows the env
  default; an existing row is not rewritten by the default.
- **TDD:** yes
- **Validation:** `cd go && go build ./... && go vet ./... && go test ./...`
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Commit 2d5ea9e merged to main. Claim name `operator` (devclaims.OperatorClaim, IssueOperator); 403 body `only the operator may change budgets and caps`; `/agent/whoami` mounted through httpapi.Endpoints (not main.go, see DI 4); defaults via package-level `agentdb.SetDefaultBudgets` because compose.go:389 calls DefaultProjectSettings outside any Store (DI 5); compose + .env.example updated. All Go gates green on the branch.

### A4: the usage route   [Status: merged, live-PG verified | Model: sonnet]
- **Scope:** Exactly §1.4. Add `usageCostSQL` beside the two token expressions in
  `go/agentdb/token_usage.go` and one store method `ProjectUsageSince(ctx, project, since) (Usage, error)`
  returning input, output, cost and query count for `created_at >= since`, reusing
  `usageEnvelopes` and the join `CountProjectTokensSince` uses (`go/agentdb/leases.go:128`).
  The handler computes the three windows from the router's location and midnight rule; it reads
  budget fields from `GetProjectSettings` and the credential mode from the value `main.go`
  already computes. Register `GET /agent/usage` in `main.go`.
- **Files:** modify `go/agentdb/token_usage.go`, **new** `go/agentdb/usage.go` + test (a live-Postgres
  test that inserts two query rows with captured-shape envelopes — copy the JSON from
  `token_usage.go:20-40` — and asserts the sums; skips without `AGENTKIT_TEST_POSTGRES_URL`),
  **new** `go/httpapi/usage.go` + test with a fake store, `go/cmd/agentd/main.go`.
- **Acceptance criteria:** `cost_known` false when cost sums to zero with non-zero tokens;
  `today` excludes a row stamped before midnight; sqlite returns 501; the response shape is
  byte-pinned by a golden test the way the core preamble is.
- **TDD:** yes
- **Validation:** `cd go && go build ./... && go vet ./... && go test ./...`; `./stack test-go` if
  a live database is available (say in Notes whether it was).
- **Depends on:** —
- [x] done
- Notes: (2026-09-11) Paused mid `go test`; WIP committed as 375b313 on worktree-agent-a31d868c69d559f1e. Files: token_usage.go, new agentdb/usage.go(+test), new httpapi/usage.go(+test), main.go, router.go, httpapi.go. (2026-09-12) Resumed: the WIP diff needed no code changes; amended to 5dd6d5f and merged as a12755f. The agent's `go test ./...` was green but it reported honestly that the one acceptance criterion about the midnight boundary rested on a SKIPPED live-Postgres test, so the orchestrator closed that gap: a throwaway `pgvector/pgvector:pg16` on port 55432 (never the stack's shared database — CLAUDE.md warns a sibling branch's migration has broken other agents' runs), then `AGENTKIT_TEST_POSTGRES_URL=... go test ./agentdb/... -run Usage -v -count=1` → **all four `TestLivePG_GetProjectUsageSince_*` PASS**, including `ExcludesRowsBeforeSince`. Then the whole suite on the merged tree WITH the live database attached: `go build`/`go vet` clean, `go test ./... -count=1` → 34 packages ok, 0 FAIL, exit 0. So A4 is the one ticket here proven against real Postgres, not just sqlite.

### A5: the budget panel in the console   [Status: todo | Model: sonnet]
- **Scope:** A `BudgetPanel` component in `web/src/components/`: today's tokens against the hard
  limit as one hairline bar (no colour unless over the soft tier — `steel` for the bar, `rose`
  when a stop is in force, per the design palette), today's cost, last 7 and 30 days as two lines,
  the credential mode sentence (*"Billed to the API key"* / *"Running on the subscription — the
  cost shown is what this would cost on the API"* / *"Mock model — nothing is billed"*), and the
  limits. Editing the limits is a small form **rendered only when `whoami.operator` is true**;
  non-operators see the numbers and the sentence *"Only the operator can change this."* Mount it
  at the top of the Desk (above the three stacks, collapsed to one line when under budget) and in
  Settings' top tier. Do **G5** here: split `ProjectSettingsPage.tsx` into *"You may want to
  change these"* (background prose, project briefing, budget panel) and *"Advanced"* (everything
  else, collapsed by default, sticky in `localStorage` beside the navReveal keys). Add
  `web/src/usage.ts` (fetch + pure formatting, unit-tested) and `web/src/whoami.ts`.
- **Files:** **new** `web/src/components/BudgetPanel.tsx` + test, `web/src/usage.ts` + test,
  `web/src/whoami.ts`; modify `web/src/components/DeskPage.tsx`, `ProjectSettingsPage.tsx`,
  `web/src/components/index.ts`; **new** e2e `e2e/features/budget.stack.spec.ts` (as the test
  login, which is an operator: the panel shows the env default, edit the hard limit, save, reload,
  assert; then assert the panel renders with no form when `whoami` says non-operator — mock the
  route in Playwright if the stack has no non-operator login).
- **Acceptance criteria:** numbers format with thousands separators and cost to two decimals;
  `cost_known:false` renders "cost not reported" rather than $0.00; the operator gate is on
  `whoami`, never on a client-side guess; the Advanced section's collapsed state survives reload.
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm run typecheck && npm test && bash scripts/verify-package.sh`
- **Depends on:** A3, A4 (build against their branches merged into main by the orchestrator)
- [ ] done
- Notes:

### A6: the project map reloads without a restart   [Status: merged, live check pending | Model: sonnet]
- **Scope:** When `AGENTKIT_PROJECT_MAP_FILE` is set, re-read and re-parse it on `SIGHUP` **and**
  every `AGENTKIT_PROJECT_MAP_RELOAD` (duration, default `60s`, `0` disables the timer). A parse
  failure keeps the previous map and logs the error with the file path; a successful reload logs
  the user and project counts. The map is held behind an `atomic.Pointer` (or a RWMutex) and every
  reader (`resolve`, `allProjects`, the API-key resolution in `apikey.go`, the origin list, the git
  token env lookup) reads through it. Inline `AGENTKIT_PROJECT_MAP` is unchanged (no file to
  watch). `docs/ops.md` §11d's invite steps drop the restart.
- **Files:** modify `go/cmd/agentd/googleauth.go`, `apikey.go`, `main.go`, `docs/ops.md`,
  `.env.example`; tests: a reload test that writes a file, boots the holder, rewrites the file,
  triggers the reload path directly, and asserts the new email resolves; a bad-file test that
  asserts the old map survives.
- **Acceptance criteria:** a user added to the file logs in within one reload interval with no
  process restart; a malformed rewrite changes nothing and is logged; `go vet` clean (no data race
  under `go test -race ./cmd/agentd/...`).
- **TDD:** yes
- **Validation:** `cd go && go build ./... && go vet ./... && go test -race ./cmd/agentd/... && go test ./...`
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Commit 32b28cc merged to main (rebased on cd44582). `projectSettingsHolder` (atomic.Pointer; SIGHUP + `AGENTKIT_PROJECT_MAP_RELOAD`, default 60s) and `projectKeysHolder`; login handlers take a `userDirectory`; git-token-env closure reads the holder per call; embed-CSP origin list still a boot snapshot (DI 6); docs/ops.md switched to a mounted `projects.json` (DI 7). Gates incl. `-race` green on the branch.

### A7: the operator's story, written down   [Status: todo | Model: sonnet]
- **Scope:** Update `docs/ops.md` (§11d and wherever credentials are set) so the OVH box runs in
  **API-key mode** with the subscription token blank, states why (the budget can only bound a
  metered key; the subscription carries only so many bots), sets the two default-budget env vars,
  and includes the verification step OM-8 demands: create a throwaway project with a tiny hard
  limit, run a job, watch the delivery queue as `pending`, delete the project. Add the one-paragraph
  invitation text Kai sends a friend (money, the architect acts without asking, the isolation
  caveat from `design/2026-09-11-ovh-compose-hosting.md:288`, trusted people only). Update
  `README-stack.md`'s env table and `docs/15-standalone-stack.md` for the new env vars and route.
  Write `docs/guide/for-operators/inviting-someone.md` per §1.5 (this is the only guide page an
  engineering ticket writes).
- **Files:** modify `docs/ops.md`, `README-stack.md`, `docs/15-standalone-stack.md`; **new**
  `docs/guide/for-operators/inviting-someone.md`.
- **Acceptance criteria:** every env var named exists in `main.go` (grep); every route named
  exists; no step says "restart" for adding a user when the file mode is in use.
- **TDD:** n/a (docs)
- **Validation:** `grep -n AGENTKIT_DEFAULT_DAILY_TOKENS_HARD go/cmd/agentd/main.go docs/ops.md .env.example` all non-empty
- **Depends on:** A3, A6
- [ ] done
- Notes:

### Stream B — the guide

Common brief for B1–B6, read in full before writing: design §2 (the journeys), §4.1 (the
skeleton), §4.2 (your pages' rows — the trap column is **sourced**; keep the sources), §5.1
(voice) and §5.2 (Ellen), plus `go/orgprompts/interviewer.md` twice and the six editorial rules at
`docs/product/15-operator-console-design.md:699-719`. Target 150–400 words per page. Front matter
per §1.5. **Where you show a prompt, label, cron line, charter or memory it must be either lifted
verbatim from a shipped file (`go/orgprompts/*.md`, `go/topology/*.go`, `e2e/mock-scripts/*.json`,
`e2e/features/learning-stories.stack.spec.ts`) or marked `<!-- fixture: to be captured by B7 -->`.**
Never invent one. Every page ends with "What this will not do". The word for the thing is the
spec's word (`docs/product/17-product-spec.md` §3). No marketing adjectives. Second person, present
tense. No em-dashes in prose. Write only your own files; do not touch another ticket's pages.

### B1: pages 0, 1, 2 — what Bob is, your first hour, the goal and the charter   [Status: written, awaiting B8 | Model: sonnet]
- **Files:** **new** `docs/guide/what-bob-is.md`, `your-first-hour.md`, `the-goal-and-the-charter.md`,
  and `docs/guide/README.md` (page order, the skeleton, the voice rules, the front-matter contract
  — copy them from the design so a later writer needs only this folder).
- **Sources:** `README.md:13-39`, `docs/workflows.md:22-62`, `docs/18-workers-memory-events.md`
  §9a, `web/src/components/CharterPanel.tsx:88-200` (quote its caption verbatim), the bookshop
  charter in `interviewer.md`. Page 0 leaves a clearly marked `<!-- Kai/Jack: why "Bob" -->`
  paragraph hole; do not fill it.
- **Acceptance criteria:** page 1 says plainly where the waiting is and that approving switches on
  an enabled daily schedule; page 2 quotes "the gate is for comprehension, not correctness".
- **Validation:** each file has front matter with the right slug; `wc -w` per page ≤ 450.
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Written: what-bob-is (344w), your-first-hour (450w), the-goal-and-the-charter (449w), README.md (728w: order, skeleton, voice, front-matter contract). Quotes: interviewer.md:130-143 (charter goal/measure/background verbatim; label_rules deferred to the-rulebook), docs/18:772 verbatim, CharterPanel.tsx:88-136 caption (trimmed). The why-Bob hole is left. Flagged: heading convention differs between pages (see DI 1).

### B2: pages 3, 4, 5 — a worker's instructions, the rulebook, clocks and wake-ups   [Status: written, awaiting B8 | Model: sonnet]
- **Files:** **new** `docs/guide/a-workers-instructions.md`, `the-rulebook.md`, `clocks-and-wake-ups.md`.
- **Sources:** design §4.2 rows 3–5 and their citations; `web/src/components/WorkerEditor.tsx`
  helper text; `WorkerTriggers.tsx:216-222` (quote it); `web/src/nlAssist.ts:6-27` and its
  understood forms (`:112-120`); the label rules from the bookshop charter; learning story 1
  (`e2e/features/learning-stories.stack.spec.ts:308` and `e2e/mock-scripts/learning-stories.json`)
  for the "missing title" sidebar — quote the two poems and the rationale exactly.
- **Acceptance criteria:** page 3 says a required reason becomes the changelog entry and that a
  change affects the next job; page 4 says editing the rulebook is publishing a new version of one
  note and that any worker can do so; page 5 says the input text is the point and that the assist
  proposes and never saves.
- **Validation:** as B1.
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Written: a-workers-instructions (433w), the-rulebook (437w), clocks-and-wake-ups (384w). Quotes verified against live files (interviewer.md §3 + worked charter label_rules; WorkerEditor.tsx:387-389; learning-stories.json + spec:308; docs/18:884-886; MemoryBrowserPage.tsx:179; WorkerTriggers.tsx:216-220; 17-product-spec.md:141-142; nlAssist.ts:6-14; router.go:420). Fixture placeholders left for: a newsletter-writer prompt (p3), Ellen adding a label (p4), a compile capture (p5). Two em-dashes remain inside verbatim quotes only. Page 4 describes write-a-note as post-C4, with a comment.

### B3: pages 6, 7, 8 — talking to a worker, when a worker asks you, the Desk   [Status: written, awaiting B8 | Model: sonnet]
- **Files:** **new** `docs/guide/talking-to-a-worker.md`, `when-a-worker-asks-you.md`, `the-desk.md`.
- **Sources:** design §4.2 rows 6–8; `docs/18-…` "Known limits" (chat gets no briefing);
  `docs/workflows.md` §6 (always pass `expires_in`); `web/src/components/DeskPage.tsx:514-547`
  and its per-stack empty copy (quote "A quiet Desk means…" exactly); learning story 4
  (`learning-stories.stack.spec.ts:550`) for the "unasked question" sidebar; the glyph legend.
- **Acceptance criteria:** page 6 states that a chat is not a job in those words; page 7 says
  silence is not consent and a paused worker does nothing until answered or expired; page 8 is
  ≤ 250 words.
- **Validation:** as B1.
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Written: talking-to-a-worker (377w), when-a-worker-asks-you (392w), the-desk (232w). "A chat is not a job" and "Silence is not consent" present. Sources: docs/18 Known limits; go/runner.go:2539-2557 (transcript emitted on idle, verified in source); docs/workflows.md:162-166; learning-stories.stack.spec.ts:550 (LS4 rationale verbatim); DeskPage.tsx:260 (quoted up to the clause that names dead pages); DeskPage legend ~514-565; spine.tsx:1-39. Fixture placeholder: the architect's first ask (p7). Kept a final "Next" section (Back/Forward) following B2's on-disk precedent.

### B4: pages 9, 10, 11 — the rail and the changelog, memory, the chart   [Status: written, awaiting B8 | Model: sonnet]
- **Files:** **new** `docs/guide/the-rail-and-the-changelog.md`, `memory.md`, `the-chart.md`.
- **Sources:** design §4.2 rows 9–11; `docs/product/15-operator-console-design.md` §3.6 (the
  spine, quote the sentence "everything hangs off a rail…"), §3.2 (authorship is a colour), §3.7
  (the patchbay); `web/src/components/ChangelogView.tsx:324-438` (the revert dialog wording —
  page 9 describes revert **as it will be after ticket A1**, on the Activity rail);
  `go/orgprompts/registry.md` on retraction (quote "so the record of the mistake survives");
  the core preamble's "Memories are references, not rules" (`go/compose.go`);
  `MemoryBrowserPage.tsx` no-match copy ("no OR in a selector"). Page 10 describes writing a note
  **as it will be after ticket C4** and says so in an HTML comment.
- **Acceptance criteria:** page 9 never uses the word "undo"; page 9 says only the newest change
  to a thing can be reverted and that the refusal names what is in the way; page 11 says every
  gesture on the chart is a proposal except emitting a real event.
- **Validation:** as B1; `grep -ci undo docs/guide/the-rail-and-the-changelog.md` is 0.
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Written: the-rail-and-the-changelog (442w), memory (441w), the-chart (405w). Quotes: ChangelogView.tsx:427-431; doc 15 §3.2/§3.6; MemoryBrowserPage.tsx:179; registry.md; compose.go preamble; interviewer.md label_rules; EmitEventControl.tsx:118; OrgChartPage.tsx + navReveal.test.ts:58-68. Post-A1 and post-C4 caveats present as comments. `grep -ci undo` = 0. Three Ellen examples left as B7 fixtures.

### B5: pages 12, 13, 14 — the architect, what Bob will not do, money   [Status: written, awaiting B8 | Model: sonnet]
- **Files:** **new** `docs/guide/the-architect.md`, `what-bob-will-not-do.md`, `money.md`.
- **Sources:** `docs/18-workers-memory-events.md` §9a in full (page 12 quotes the no-brake
  paragraph **verbatim, in a block quote, unsoftened**, and the loop's six steps in plain words);
  `go/orgprompts/architect.md` (read it whole; quote STEP 2's "a verdict written afterwards is a
  justification, not a measurement"); `docs/workflows.md:175-210` for page 13 — keep every item —
  plus the known limits list from §9a; for page 14: design §1 "Money", `router.go:690-712`
  (the two tiers, interactive chat exempt, midnight reset), and **the interfaces in this plan
  §1.2–§1.4** — page 14 describes the budget panel and the operator rule as they will be after
  A3–A5, and says in an HTML comment which ticket makes each sentence true. Do not state a price
  or a default number; write `<!-- Kai: default daily limit -->` where one belongs.
- **Acceptance criteria:** page 12 says "a quiet architect is the alarm" and gives the two ways
  to stop it (toggle the schedule; freeze a worker); page 13 keeps all six "use something else"
  items; page 14 says the limit is visible to everyone and changeable only by the operator, that
  chat is exempt, and that the count resets at midnight.
- **Validation:** as B1.
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Written: the-architect (352w), what-bob-will-not-do (439w), money (240w). No-brake paragraph quoted verbatim from docs/18:845-847; architect.md STEP 0 quoted; workflows.md:175-212 all six items + three known limits (docs/18:880-891); router.go:693-702 for the tiers. money.md marks post-A3/A4/A5 sentences with comments; default limit hole left for Kai; week's-spend example is a B7 fixture.

### B6: field notes and the glossary   [Status: written, awaiting B8 | Model: sonnet]
- **Files:** **new** `docs/guide/field-notes.md`, `docs/guide/glossary.md`.
- **Sources:** the seven candidates in design §4.2 Part 4 plus "the forgotten sign-off"; each
  story's source is named there — read the source entry in `docs/product/06-work-plan.md`'s
  Discovered Issues Log, `docs/product/22-readiness.md`, `docs/product/20-operations-doctrine.md`,
  `design/2026-09-08-memory-coordinated-organisation.md` DI log, or `docs/product/runs/2026-09-08-architect-probe/`
  before writing, and quote at most one line from it. ≤ 120 words per story, each ending in the
  rule it produced. Glossary: the vocabulary from `17-product-spec.md` §3 and design §4.2
  "Reference", one line each, the same word the console uses (check the button labels in
  `web/src/components/` when unsure).
- **Acceptance criteria:** every story cites its source in an HTML comment; no story is invented
  or embellished beyond its source; the glossary has an entry for every term used as a heading
  anywhere in `docs/guide/`.
- **Validation:** `grep -c '^\*\*' docs/guide/glossary.md` ≥ 22.
- **Depends on:** —
- [ ] done
- Notes: (2026-09-11) Written: field-notes (899w, 8 stories, each ≤120w, each ending in the rule, sourced in comments: 06-work-plan.md:490-502, :722-725; 20-operations-doctrine.md:84,:87; 13-work-plan-self-improvement.md:471-473; 22-readiness.md:24-29; architect-probe README; 2026-09-08 DI4 :2007-2009; git-projection.md:36-72; learning-stories.json:87,105,124), glossary (633w, 31 entries; labels checked against navReveal.ts:161-167, ChangelogView.tsx:395, WorkerEditor.tsx:190-199).

### B7: Ellen's fixture project and the screenshots   [Status: todo | Model: sonnet]
- **Scope:** With the stack up in mock mode (D2 brings it up; this ticket runs after D1 merges),
  write a Playwright script under `e2e/guide/` that creates a project `ellens-bookshop`, drives
  the interview with the bookshop answers, approves the charter, runs the architect via a mock
  script that creates a small roster (start from `e2e/mock-scripts/architect-archivist.json` and
  `onboarding.json`; add `e2e/mock-scripts/guide-fixture.json`), makes one human prompt edit with
  a rationale and one revert, and then screenshots each surface listed in the pages'
  `<!-- screenshot: … -->` comments into `docs/guide/img/`. Replace each comment with the image.
  Replace each `<!-- fixture: … -->` in the pages with the real captured text (a prompt, a
  memory, a changelog entry), verbatim.
- **Files:** **new** `e2e/guide/capture-fixture.spec.ts`, `e2e/mock-scripts/guide-fixture.json`,
  `docs/guide/img/*.png`; modify the guide pages' placeholders.
- **Acceptance criteria:** `grep -rn 'fixture: to be captured\|screenshot:' docs/guide/` is empty;
  every image is under 400 KB; the script is re-runnable (`./e2e/run-stack-e2e.sh clean` first).
- **Validation:** the grep above; `ls docs/guide/img | wc -l` ≥ 10.
- **Depends on:** D1, D2, B1–B6
- [ ] done
- Notes:

### B8: the editorial pass   [Status: done | Model: sonnet]
- **Scope:** Read every page in `docs/guide/` in order. Enforce: the same word everywhere (build a
  list of the terms and their spellings; fix drift); every first paragraph stands alone with no
  links; every page has "What this will not do"; no marketing adjectives (grep for powerful,
  seamless, effortless, intelligent, magical, simply, just); no em-dashes; identifiers in
  backticks; lengths within budget; cross-links resolve to existing slugs; nothing contradicts
  `docs/18-workers-memory-events.md` (spot-check ten claims against source). Do not rewrite
  voice; fix defects. Append a list of what you changed to Notes.
- **Files:** modify `docs/guide/*.md`.
- **Acceptance criteria:** `grep -rn '—' docs/guide/*.md` is empty; every `](` link target exists;
  the ten spot-checks are listed with `file:line`.
- **Validation:** the two greps; `wc -w docs/guide/*.md`.
- **Depends on:** B1–B6
- [x] done
- Notes: (2026-09-11) Completed despite the pause; edits are in b1af8b3. All 16 pages on `##` headers (DI 1 closed); deep-link line on all 10 Part 1/2 pages; why-Bob hole moved above the closing sections; 2 prose em-dashes removed (3 remain, all inside verified verbatim quotes); glossary's revert entry no longer says "undoes"; field-notes Back link fixed; wall→boundary; what-bob-will-not-do trimmed 475→437; ten spot-checks against docs/18 (lines 70-71, 272-278, 772-773, 783-786, 843-851, 864-870, 880-894) with no contradiction. Only broken link: for-operators/inviting-someone.md (A7). 12 fixture/screenshot placeholders intact for B7.

### Stream C — console wiring

### C1: the guide route and renderer   [Status: WIP branch, unverified (paused) | Model: sonnet]
- **Scope:** G7. `examples/web` gains a `guide` view reachable at `#/guide/<slug>` (the shell has
  no router; follow how views are selected in `App.tsx` and add a hash-based deep link that also
  survives reload — read `web/src/permalink.ts` first, it may already do this) and a sidebar
  entry **Guide** placed after Settings (always visible; it is not content-driven, it is a book).
  A build step (`examples/web/scripts/build-guide.mjs`, run from the `build` and `dev` scripts)
  reads `docs/guide/*.md`, parses front matter, and emits `examples/web/src/guide/pages.generated.ts`
  (`{slug, title, part, order, surfaces, firstParagraph, html}`) and
  `web/src/guide/firstParagraphs.generated.json`. Render markdown with a small, pinned library
  from the allowlist the repo already uses (check `package.json` for an existing markdown
  dependency before adding one). Typography from the theme: prose in the content face, code in
  the identifier face, hairlines not shadows, max width ~68ch. Left rail: parts as small-caps
  eyebrows, pages beneath. If `docs/guide/` is empty the build emits an empty list and the view
  says "The guide has not been written yet." Commit the generated files' **absence**: they are
  gitignored and built.
- **Files:** **new** `examples/web/scripts/build-guide.mjs`, `examples/web/src/GuidePage.tsx`,
  `web/src/guide/surfaces.ts` (§1.6), `.gitignore` entries; modify `examples/web/package.json`,
  `examples/web/src/App.tsx`, `Sidebar.tsx`, `deploy/web.Dockerfile` (the build must see
  `docs/guide/` — check the Docker build context and add a `COPY` if needed; **this is the step
  most likely to be wrong, test it with `docker build -f deploy/web.Dockerfile .`**).
- **Acceptance criteria:** `#/guide/the-desk` renders that page after a reload; a missing slug
  renders a one-line "no such page" with the page list; the generated files are not tracked.
- **TDD:** unit test the front-matter parser and the surfaces set.
- **Validation:** `cd web && npm ci && npm run build && cd ../examples/web && yarn install --frozen-lockfile && yarn build && yarn typecheck`; `docker build -f deploy/web.Dockerfile . -t guide-check` succeeds.
- **Depends on:** — (uses placeholder pages until B lands; write two throwaway pages in a temp dir
  for the unit test, not in `docs/guide/`)
- [ ] done
- Notes: (2026-09-11) Paused after a successful `docker build -f deploy/web.Dockerfile`; WIP committed as ecdcc79 on worktree-agent-a336db1df724ecdb8. Files: examples/web/scripts/, GuidePage.tsx, web/src/guide/, App.tsx, package.json + yarn.lock, .gitignore(s), web/src/index.ts + pure.ts, deploy/web.Dockerfile.

### C2: "About this screen" on eleven surfaces   [Status: todo | Model: sonnet]
- **Scope:** G2. `web/src/components/AboutThisScreen.tsx`: props `{surface: SurfaceId, projectId}`;
  reads the first paragraph for that surface from `firstParagraphs.generated.json` (injected by
  the shell through a provider so `web/` stays free of build-time files — a `GuideProvider` with a
  `useGuideParagraph(surface)` hook; default provider returns nothing and the component renders
  nothing, so the library works without the guide); renders one collapsed line under the page
  title, the content face, a chevron; opens to the paragraph plus *"Read more in the guide →"*
  linking to `#/guide/<slug>`; a **Dismiss** that hides it, sticky per surface per project in
  `localStorage` (`agentkit.about.dismissed.<projectId>`), and a small text control *"About this
  screen"* that brings it back. 180ms fade gated on `usePrefersReducedMotion`, nothing else.
  Mount on: Desk, Chat, Workers list, Worker Configuration, Worker Triggers, Worker History,
  Memory, Activity, Chart, Settings, OnboardingPage right column, and the project create form
  (surface `project-create`, always expanded, no dismiss — it is one sentence).
- **Files:** **new** `web/src/components/AboutThisScreen.tsx` + test, `web/src/guide/GuideProvider.tsx`,
  `web/src/guide/aboutDismissal.ts` + test (pure); modify the eleven components and
  `examples/web/src/App.tsx` (provide the generated paragraphs); **new** e2e assertions appended to
  `e2e/features/console.stack.spec.ts`: the line is present on the Desk, opens, links to a guide
  URL that renders, stays dismissed after reload.
- **Acceptance criteria:** a surface with no paragraph renders nothing (no empty chrome);
  dismissal is per surface, not global; `SurfaceId` is the closed set from §1.6.
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm run typecheck && npm test && bash scripts/verify-package.sh`
- **Depends on:** C1 (surfaces.ts and the generated JSON), B1–B6 (real paragraphs; until then use
  the generated file from whatever pages exist)
- [ ] done
- Notes:

### C3: chat empty state, create-form parity, the four copy defects   [Status: merged | Model: sonnet]
- **Scope:** G3, G6 and G9 from the design. **G3:** `AgentChat.tsx` renders, when the transcript is
  empty, one sentence of what this session is, the sentence *"A chat is not a job: it gets none of
  the project's briefing, and nothing it says is remembered unless a worker writes a memory."*, and
  three plain-text suggestion buttons that fill the composer (base chat and worker chat variants
  as in design §3 G3). **G6:** one shared `CreateProjectForm` used by `ProjectPicker.tsx` and
  `Sidebar.tsx`; the Sidebar's copy wins. **G9:** the disabled-save hint beside the save button in
  `WorkerEditor.tsx`, `SubscriptionEditor.tsx`, `ProjectSettingsPage.tsx` (*"Saving needs a reason
  — it becomes this change's entry in the changelog."*); fix `WorkerChatPanel.tsx:60-79` to say
  chat receives no briefing; fix `DeskPage.tsx:258-263` to name Activity and Triggers; rename every
  reason field label to `Why?` (`ScheduleEditor.tsx`'s `Rationale`, the org chart's two long forms
  — keep their helper text, change the label).
- **Files:** modify `web/src/components/AgentChat.tsx`, `WorkerEditor.tsx`,
  `SubscriptionEditor.tsx`, `ScheduleEditor.tsx`, `ProjectSettingsPage.tsx`, `WorkerChatPanel.tsx`,
  `DeskPage.tsx`, `OrgChartPage.tsx` (labels only), **new** `web/src/components/CreateProjectForm.tsx`;
  modify `examples/web/src/ProjectPicker.tsx`, `Sidebar.tsx`; tests beside each; update any e2e
  spec that selects by the old label text (grep `e2e/features` for `Rationale`, `Project name`).
- **Acceptance criteria:** the empty-state test renders the three suggestions and clicking one
  fills the composer; `grep -rn '"Rationale"' web/src/components` is empty; `grep -rn 'Events view\|Automation' web/src/components/DeskPage.tsx` is empty.
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm run typecheck && npm test && npm run build && cd ../examples/web && yarn install --frozen-lockfile && yarn typecheck`
- **Depends on:** —
- [x] done
- Notes: (2026-09-11) Paused while typechecking examples/web; WIP committed as b1a700c on worktree-agent-a165e672dbaeb7334. 19 files incl. AgentChat(+test), ProjectPicker, Sidebar, DeskPage, OrgChartPage(+test), ProjectSettingsPage, ScheduleEditor, SubscriptionEditor, console.stack.spec.ts. (2026-09-12) Resumed: the agent found and fixed one real defect — the empty-state sentence read "unless **it** writes a memory" where this ticket quotes "unless **a worker** writes a memory" (`web/src/components/AgentChat.tsx:428` now matches). Amended to 348220a, merged as a merge of worktree-agent-a165e672dbaeb7334. **It shares DeskPage.tsx and Sidebar.tsx with A2 and git merged both cleanly — no conflict at all**, so the orchestrator checked the semantics rather than trusting the textual merge: A2's `showFirstRunPanel` (`DeskPage.tsx:147`) and `finish-onboarding` button (`:568`) are both still present, and C3's Activity/Triggers wording survived at `:286-287`. Both acceptance greps re-run on the MERGED tree and both still empty. Web gates on the merged tree: typecheck clean, `npx vitest run --testTimeout=20000` → 1563/1563 in 83 files, build clean, `examples/web` typecheck clean. Residue the agent flagged: the two `e2e/features` hits for `Rationale`/`Project name` are a history comment, not a selector — left as found. DI11 records the ticket-vs-design wording split.

### C4: write a note from the Memory page   [Status: merged, e2e pending D2 | Model: sonnet]
- **Scope:** G8. On `MemoryBrowserPage.tsx`: a **Write a note** button opening a small form
  (content, multiline; labels as one `key=value` per line in the identifier face, validated with
  the same parser the selector field uses if one is exposed, else a strict regex with a helpful
  error; required `Why?`). Submits to `POST /agent/memories` (read `go/httpapi` for the exact body
  and the provenance rule: **send no provenance fields**; the server stamps them empty and refuses
  a body that supplies them — `docs/20-datasets.md`). On the newest `name=label-registry` row, a
  **Publish a new version** action that opens the same form pre-filled with that row's labels and
  content. Replace the empty state's *"there is nothing to add here"* with a sentence that invites
  the first note. After a successful write the list refreshes and the new row is highlighted with
  the existing feed-highlight mechanism (`web/src/feedhighlight.ts`).
- **Files:** modify `web/src/components/MemoryBrowserPage.tsx`, `web/src/memories.ts` (the POST
  helper + label-line parser, unit-tested); **new** e2e `e2e/features/memory-write.stack.spec.ts`
  (write a note, assert it appears with empty provenance; publish a new registry version, assert
  `memory_current` semantics by reading `GET /agent/memories/current?name=label-registry`).
- **Acceptance criteria:** a body with a provenance field is never sent (unit test on the
  helper); a malformed label line blocks submit with the reason; the registry action pre-fills.
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm run typecheck && npm test`
- **Depends on:** —
- [x] done
- Notes: (2026-09-11) Paused at 'about to commit'; WIP committed as 3e5e92b on worktree-agent-a8928fdf78ef2c1d3. Files: MemoryBrowserPage(+test), memories.ts(+test), new e2e/features/memory-write.stack.spec.ts. (2026-09-12) Resumed: the WIP diff needed no code changes; amended to 39a709b and merged as b0f759a with no conflict. The orchestrator re-read the provenance claim rather than taking the agent's word, because it is the one thing this ticket exists to guarantee: `memoryWriteBody` returns exactly `{labels, content}` (`web/src/memories.ts:230-240`), `web/src/memories.test.ts:342-353` pins that key set and asserts neither provenance key is present, and the server refuses a body carrying either — even as "" or null — at `go/httpapi/memories.go:480-492`. So the client cannot send provenance and the server would reject it if it did. Malformed label lines block submit via `canSubmit` and surface the reason as `helperText` (`MemoryBrowserPage.tsx:453-455,513-514`); the registry action pre-fills from the newest `name=label-registry` row via `foldNamedMemories`' descending fold (`memories.ts:451-476`). DI9 records the gap the agent found: on a project with zero memories the Memory nav entry never reveals, so this button is unreachable until something else writes one.

### C5: the Desk narrates firsts   [Status: todo | Model: sonnet]
- **Scope:** G4. In `web/src/desk.ts`'s fold (pure, `now` passed in), tag each record with its
  kind: `first-worker`, `first-memory`, `first-schedule`, `first-subscription`, `first-rewrite`,
  `first-ask`, `first-revert`. A pure `web/src/firsts.ts` takes the folded records and a
  per-project seen-set and returns which rows should carry a narration; `useFirsts` persists the
  seen-set in `localStorage` (`agentkit.firsts.<projectId>`) beside the navReveal keys and marks
  a kind seen the first time it is rendered. The row gains **one sentence** in the content face and
  a link to the guide page (`#/guide/<slug>`): the seven sentences are in design §3 G4 and its
  table §4.4; write them once in `firsts.ts`, not in JSX. No checklist, no count, no progress bar.
  If the guide route is absent (no `GuideProvider`), render the sentence without the link.
- **Files:** modify `web/src/desk.ts` + test, **new** `web/src/firsts.ts` + test,
  `web/src/useFirsts.ts`; modify `web/src/components/DeskPage.tsx` + test; append to
  `e2e/features/console.stack.spec.ts`: after the first worker is created the Desk shows the
  first-worker sentence once; after reload it does not show again.
- **Acceptance criteria:** the fold is unit-tested with one record of each kind, including the J1
  seconds-vs-milliseconds hazard (`desk.ts` documents it); the same kind never narrates twice per
  project; deleting the worker does not reset the seen-set.
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm run typecheck && npm test`
- **Depends on:** C1 (the link target), C2 (the `GuideProvider`)
- [ ] done
- Notes:

### Stream D — integration

### D1: merge and the full gates   [Status: todo | Model: orchestrator]
- **Scope:** Merge each `fleet/*` branch into `main` in dependency order (A3 and A4 before A5;
  C1 before C2 and C5), resolving conflicts in `main.go`, `App.tsx`, `DeskPage.tsx`,
  `ProjectSettingsPage.tsx` by hand. Run the three gates from §0 plus `bash web/scripts/verify-package.sh`
  and `docker build -f deploy/web.Dockerfile .`. Commit `docs/guide/` once B8 has passed.
- **Validation:** all gates green on `main`.
- **Depends on:** the stream's tickets
- [ ] done
- Notes:

### D2: the stack e2e for everything new   [Status: todo | Model: sonnet]
- **Scope:** `./e2e/run-stack-e2e.sh up` (mock), then `./e2e/run-stack-e2e.sh test` for:
  `revert`, `onboarding`, `budget`, `memory-write`, `console` (the appended assertions), and the
  whole existing suite once. Fix what fails **inside the ticket that owns it** (append to its
  Notes) or log it in the Discovered Issues Log if it is pre-existing. `./e2e/run-stack-e2e.sh clean`
  after; do not leave the stack up with abandoned schedules (DI: "one abandoned cron line took out
  a whole host").
- **Validation:** the run's summary line, pasted into Notes; `e2e/stack-e2e-logs-mock.txt` captured.
- **Depends on:** D1
- [ ] done
- Notes:

---

## 3. What the orchestrator watches for

- An agent that reports green without pasting the gate command's last lines has not run it.
- An agent that "simplifies" the operator rule into a client-side check (A5) has built a
  decoration; the 403 is the feature.
- A writer that produces a prompt, memory or charter not present in a shipped file has invented
  a fixture; send it back.
- A ticket that touches `web/src/components/index.ts` must not remove an export (`web/` is a
  published package; removal is Kai's call).
- Nothing in this plan changes the architect's autonomy, the reveal doctrine, or the JWT shape
  beyond one boolean claim.

## Discovered Issues Log

*(append-only; newest last; `**N. Title.** (ticket, date)` then the finding, with `file:line`.)*

**1. Two heading conventions in the guide.** (B1, 2026-09-11) `a-workers-instructions.md` (B2) uses bold inline labels and a `→ #/guide/<slug>` deep-link line under "In the console"; `money.md` (B5) and the B1 pages use `##` headers and no deep-link line. `docs/guide/README.md` documents `##` headers as canonical. B8 reconciles every page to `##` headers and decides the deep-link line once (recommendation: keep it, it is what G2's "Read more" link resolves to, and C1 renders `#/guide/<slug>`).

**2. `web/` full-suite vitest timeouts under load.** (A1, 2026-09-11) `npm test` (1548 tests) intermittently times out 2–4 tests at 5000ms among `AutomationPage.test.tsx`, `ProjectSettingsPage.test.tsx`, `WorkersPage.test.tsx`; never the same set, each file green in isolation. Resource contention with a dozen agents running suites at once, most likely. D1 re-runs the suite on a quiet machine before believing any web failure.

**3. Stale comment on `e2e/helpers/api.ts` `WorkerBody`.** (A1, 2026-09-11) Says "PUT replaces"; the route is create-or-keep since T27/DI11 (`go/httpapi/workers.go:20-40`). Comment left as found; fix when next touching the file.

**4. `whoami` registered through `httpapi.Endpoints`, not `main.go`.** (A3, 2026-09-11) Matches how charter/config-events/memories mount; no behavioural difference. Ticket text said main.go.

**5. Default budgets are a package-level setter, not a Store field.** (A3, 2026-09-11) `DefaultProjectSettings` is also called as a free function at `go/compose.go:389`; a Store field would miss it. `agentdb` still reads no env.

**6. Embed-CSP origin list is still a boot-time snapshot.** (A6, 2026-09-11) `embedcsp.go:79-88` documents "computed once, at wiring time" and `main.go` passes a `[]string`; making it live means a `func() []string` per request. Small, separate change; not done.

**7. `docs/ops.md` §11d had no invite walkthrough and used the inline map.** (A6, 2026-09-11) Switched the OVH example to `AGENTKIT_PROJECT_MAP_FILE` at `/srv/apps/bob/secrets/projects.json` and added a one-paragraph "adding someone later"; A7 still owes the full invitation prose.

**8. The `web/` suite's 5000ms flake is a timeout, not a race — and it has a one-flag proof.** (orchestrator, 2026-09-12) Confirms **2** with the measurement that file was missing. On untouched `main`, `cd web && npm test` → 6 failed / 1542 passed across `AutomationPage.test.tsx`, `ProjectSettingsPage.test.tsx`, `WorkersPage.test.tsx`; re-running exactly those three files alone on the same tree → **71 passed, 0 failed**. A2's agent went further on its branch: the *whole* suite with `npx vitest run --testTimeout=20000` → **1549/1549 pass**. So every one of these is `@testing-library/user-event` typing past a wall-clock deadline under CPU contention, and the fleet's own parallelism is what causes it. Two consequences: (a) no agent should judge a web ticket by the full suite while siblings are running — re-run the ticket's own files in isolation; (b) raising `testTimeout` in `web/vitest.config.ts` would make the gate honest, but it is a shared file five tickets were editing, so it is **not** done here and wants its own ticket. D1 still re-runs the suite on a quiet machine.

**9. A brand-new project has no UI path to its own first memory.** (C4, 2026-09-12) The Memory nav entry only reveals once the project has ≥1 memory (K9, `examples/web/src/App.tsx:296-298`), so the "Write a note" button C4 adds is unreachable on a project that has never had one — `e2e/features/memory-write.stack.spec.ts:20-26` routes around it by seeding a memory over the API. Harmless today because onboarding seeds two memories at charter approval, but it makes the zero-memory case a dead end. Wants its own ticket if the first note ever matters before a charter.

**10. "Charter applied" is inferred from a worker's name, not reported by the API.** (A2, 2026-09-12) `useInterviewState` in `examples/web/src/onboarding.ts` decides an interview is over when `GET /agent/workers` contains a worker named `architect` (`ARCHITECT_WORKER_NAME`). The ticket text sanctioned this and the code documents it, but a charter that sets a custom `architect_name` defeats it and the project stays "in interview" forever. The real fix is an `applied` flag on the charter-current response; out of scope here.

**11. The ticket and the design doc quote the empty-chat sentence differently.** (C3, 2026-09-12) This plan's C3 scope quotes "nothing it says is remembered unless **a worker** writes a memory"; design §3 G3 writes "unless **it** writes a memory". The agent followed this plan, as the ticket is the authority for its own text, and `web/src/components/AgentChat.tsx:428` now reads "a worker". The design doc is the one out of step; reconcile it there, not in the code.

**12. Nothing in this wave was proven against real Postgres until it was asked for.** (orchestrator, 2026-09-12) A4's agent ran a full green `go test ./...` and still reported, correctly, that one of its four acceptance criteria rested on a test that had SKIPPED — `TestLivePG_GetProjectUsageSince_ExcludesRowsBeforeSince` needs `AGENTKIT_TEST_POSTGRES_URL`. That is CLAUDE.md's standing warning behaving exactly as advertised: a green suite does not prove the pgvector/jsonb paths. Closed here with a throwaway `pgvector/pgvector:pg16` on port 55432 (all four live cases PASS, and the whole suite re-run with it attached: 34 ok, 0 FAIL). Two things worth keeping: the fleet's Go gate should carry a live database whenever a ticket touches `agentdb`, and the throwaway must never be the compose stack's own `agent-bob-postgres-1` — a sibling branch's unmerged migration has broken other agents' runs before.
