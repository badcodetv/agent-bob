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

### A5: the budget panel in the console   [Status: merged, e2e pending D2 | Model: sonnet]
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
- [x] done
- Notes: (2026-09-12) Built, then sent back **twice**, then merged as `03142ec` (branch commit
  `f032ee2`). Round 1 delivered the panel, `usage.ts`, `whoami.ts` and the G5 split, green. Round 2
  fixed **DI18** — the lost-update defect the agent found itself and mis-framed as pre-existing;
  `BudgetPanel` now takes an optional `settings` prop so the settings page supplies its single
  instance (`ProjectSettingsPage.tsx:219`) while the Desk mount keeps its own. The agent improved on
  the fix asked for: instead of an `onSaved` callback it watches a genuine `saving` true→false with
  no error, which refreshes the numbers whichever button saved — right, because on a shared instance
  either can change the budget, and `settings.save()` never rejects so a `.then()` would also have
  fired on validation early-returns. **Its regression test is proven both ways**: it reverted the fix,
  saw `expected +0 to be 9000`, restored it, saw it pass. Round 3 closed **DI19**, the third guarded
  field. Orchestrator re-ran the Validation on the merged tree: typecheck clean, `vitest
  --testTimeout=20000` **1684/1684 in 91 files**, `verify-package.sh` PASS, web build clean,
  `examples/web` build and typecheck clean at 18 guide pages — and re-ran
  `ProjectSettingsPage.test.tsx` alone to confirm the three tests that matter survived the merge
  rather than inferring it from a rising total: the shared-instance regression and both
  `max_concurrent_jobs` gate cases pass (32/32 in that file).
  On the field moves: the design's "No field moves or changes; only the grouping" reads like a
  prohibition on what A5 did, and is not — you cannot build two tiers without moving fields between
  them, so the grouping *is* the move. What it forbids is a field renamed, revalidated, rewired or
  dropped, and none was: `s.update`, `s.draft`, `s.fieldErrors` still flow through one instance.
  Removing the two token budgets from the open numeric list was **necessary**, for a better reason
  than the ticket text the agent cited: left there, a non-operator could edit them and the
  whole-object PUT would 403, so taking them out is what makes the operator gate real at the point
  of use rather than only at the wire.

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

### A7: the operator's story, written down   [Status: merged | Model: sonnet]
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
- [x] done
- Notes: (2026-09-12) Written and merged as 3aa9bef → merge of `fleet/A7`; no conflict. `docs/ops.md`
  §11d now runs API-key mode with `CLAUDE_CODE_OAUTH_TOKEN` blank and says why, and a new §11f is the
  OM-8 spend-brake check. New `docs/guide/for-operators/inviting-someone.md`, plus env/route updates
  to `README-stack.md` and `docs/15-standalone-stack.md`. **The agent declined to call its own
  Validation green, correctly** — see DI14; `grep` on `main.go` returns nothing because A3 put the
  var names in `defaultbudgets.go` (DI5), so the command as written cannot pass and the ticket's own
  text is what is wrong. Orchestrator verified on the merged tree: the guide build goes **17 → 18
  page(s)**, so the new page's front matter parses and the generator does walk `for-operators/`
  (`build-guide.mjs:94-104`); there is genuinely no `DELETE /agent/project` route
  (`grep -rn "DELETE /agent/project\|deleteProject" go/httpapi go/cmd/agentd` → empty), which the
  page documents rather than papering over; and **`docs/guide/` now has zero broken relative links**
  (scanned all 18 pages), closing the one the RESUME block named. The two budget numbers are left as
  `FILL-BEFORE-FIRST-INVITE` at `docs/ops.md:783-784` and no number is stated anywhere in the new
  page — `money.md`'s hole is untouched. Kai has still not chosen them.

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

### B7: Ellen's fixture project and the screenshots   [Status: done | Model: sonnet]
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
- **Acceptance criteria:** `grep -rn 'fixture: to be captured\|screenshot:' docs/guide/
  --exclude=README.md` is empty; every image is under 400 KB; the script is re-runnable
  (`./e2e/run-stack-e2e.sh clean` first).
- **Validation:** the grep above; `ls docs/guide/img | wc -l` equals the number of
  `<!-- screenshot: … -->` placeholders the pages actually carry (**3**).
  *Both lines corrected by the orchestrator on 2026-09-12 — see DI33 and DI34 for why the
  originals ("is empty", "≥ 10") could not be satisfied by any correct implementation.*
- **Depends on:** D1, D2, B1–B6
- [x] done
- Notes: (2026-09-12) Built and run. `e2e/mock-scripts/guide-fixture.json` (three rules:
  interviewer reused verbatim from `onboarding.json`, an architect that builds
  `newsletter-writer` + `newsletter-archivist` with a schedule and a subscription, and the writer
  itself so the roster demonstrably runs) plus `e2e/guide/capture-fixture.spec.ts`, added to
  `playwright.stack.config.ts`'s `testMatch` — without which the file would never have been
  discovered. Ran 7 times while the agent fixed timing races and cropped captures, then an 8th
  with no changes to prove re-runnability: passed, and needed no `clean`, because its own reset
  step handles the fixed `ellens-bookshop` id.
  **All 9 `fixture:` and all 3 `screenshot:` placeholders in real pages are filled, every one from
  the running stack.** Orchestrator verified: the corrected grep is empty; 3 images, largest 117 KB
  (cap 400 KB); `e2e/guide/captured-fixtures.json` holds the evidence trail for 8 captured strings,
  and the ask fixture in `when-a-worker-asks-you.md` is the architect's real
  `request_human_attention` body quoted whole — message text, not row chrome. The charter's
  `label_rules` are lifted from `go/orgprompts/interviewer.md:138`, the worked example already
  shipped in the prompt, so even the seed is not invented. `captured-fixtures.json` is kept
  deliberately: it is what makes the "captured, never authored" rule auditable later.
  Residue: 4 of the pages it touched now exceed README's 450-word hard cap (461/457/452/475) —
  DI35, not fixed, because the only ways under were to truncate a verbatim quote or delete B3's
  prior sidebar.

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

### C1: the guide route and renderer   [Status: merged, browser check pending D2 | Model: sonnet]
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
- [x] done
- Notes: (2026-09-11) Paused after a successful `docker build -f deploy/web.Dockerfile`; WIP committed as ecdcc79 on worktree-agent-a336db1df724ecdb8. Files: examples/web/scripts/, GuidePage.tsx, web/src/guide/, App.tsx, package.json + yarn.lock, .gitignore(s), web/src/index.ts + pure.ts, deploy/web.Dockerfile. (2026-09-12) Resumed: no code changes needed; amended to 7b5ba44 and merged as cce006f. The agent's own gates were green (`src/guide` 45/45, `pure.test.ts` 14/14 so the `./pure` tier line still holds, `docker build -f deploy/web.Dockerfile` EXIT=0) **but every one of them ran on a branch that predates `docs/guide/`**, so its build logged "docs/guide does not exist yet — writing an empty guide" and the whole generator-plus-COPY path — the step this ticket calls the one most likely to be wrong — was never exercised with content. The orchestrator closed that on the merged tree: `yarn build` → `[build-guide] wrote 17 page(s).`, all 17 slugs present including `the-desk`, `firstParagraphs.generated.json` emitted (5992 bytes), and **the docker build repeated it inside the image** (`wrote 17 page(s)`, EXIT=0) which is the real proof the Dockerfile's COPY reaches `docs/guide/`. Both generated files confirmed still ignored (`git check-ignore -v` matches `examples/web/.gitignore:8` and `.gitignore:10`; `git status` clean after the build). Merge conflict and its resolution: see DI13 — it is the important one. Still owed: the two rendering criteria (a slug renders after reload, a missing slug lists the pages) are code reads, not browser observations; D2 owes them. The ticket says "sidebar entry after Settings" but the element is the horizontal `ViewNav` bar, where Settings is last in `NAV_ALWAYS` — visually the same place, different component; `Sidebar.tsx` was not touched.

### C2: "About this screen" on eleven surfaces   [Status: merged, e2e pending D2 | Model: sonnet]
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
- [x] done
- Notes: (2026-09-12) Built and merged as 2357287 → merge `bc73998`; **no conflict at all**, which
  the agent earned by keeping its `App.tsx` edits to six numbered additive regions and telling me
  which they were, plus what it did *not* touch (`onChange`, `ViewNav`, `RevealNotice`, the
  onboarding effects) — the direct answer to DI13. The provider seam is keyed by **surface, not
  slug** (`web/src/guide/GuideProvider.tsx`), the shell builds the map from C1's `GUIDE_PAGES` in
  `examples/web/src/App.tsx`, and the default context is `{}` so a consumer with no guide gets
  `undefined` and the component renders nothing — asserted with `toBeEmptyDOMElement()` for both an
  empty map and no provider at all. `surfaces.ts` untouched, so `SurfaceId` is still C1's closed set.
  Orchestrator re-ran the Validation on the merged tree: typecheck clean, `vitest
  --testTimeout=20000` **1647/1647 in 88 files**, `verify-package.sh` PASS (consumer theme honoured),
  web build clean, `examples/web` build **and** typecheck clean at 18 guide pages.
  **The finding worth keeping is its own:** MUI `Collapse` leaves children in the DOM at height zero
  unless given `unmountOnExit`, so "renders nothing" and "collapsed shows only the toggle" would have
  been true to the eye and false to a test — the agent caught it with its own test and fixed it at
  `AboutThisScreen.tsx:127`. That is OM-9 (storage is not delivery) in a new costume. DI17 records
  the `projectId` prop it had to add to five components.

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

### C5: the Desk narrates firsts   [Status: merged, e2e pending D2 | Model: sonnet]
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
- [x] done
- Notes: (2026-09-12) Built and merged as `5e4f6bc` → merge with **no conflict at all**, which its
  region discipline earned: it stayed clear of the top of `DeskPage.tsx` where A5 had just mounted
  the budget panel, and listed what it did not touch. Orchestrator re-ran the Validation on the
  merged tree: typecheck clean, `vitest --testTimeout=20000` **1711/1711 in 93 files**, web build
  clean, `verify-package.sh` PASS, `examples/web` build and typecheck clean, `go build`/`go vet`
  clean. Verified all three wave-2 features coexist on the Desk: `AboutThisScreen` at
  `DeskPage.tsx:210`, `BudgetPanel` at `:223`, the firsts wiring at `:146-157` and
  `FirstNarrationLine` at `:470`/`:533`.
  Two things it got right that are worth naming. The fold is built from the **unwindowed** changelog,
  so a first older than `earlierChangesLimit` still narrates — pinned by its own test. And it found,
  mid-build, that naively recomputing from storage made a narration vanish in the same paint as the
  mark-as-seen effect; it split `seenAtMount` from `shown` so a sentence survives the visit. It also
  reused C2's `useGuideParagraph('desk') !== undefined` as the "is a guide mounted" signal rather
  than inventing a second one. Its report claimed it had left the open-ask caveat out of the source;
  it had not — the reasoning is at `desk.ts:484-498`. Four findings: **DI22** (the sentences), and
  **DI23–DI25** below.

### Stream D — integration

### D1: merge and the full gates   [Status: done | Model: orchestrator]
- **Scope:** Merge each `fleet/*` branch into `main` in dependency order (A3 and A4 before A5;
  C1 before C2 and C5), resolving conflicts in `main.go`, `App.tsx`, `DeskPage.tsx`,
  `ProjectSettingsPage.tsx` by hand. Run the three gates from §0 plus `bash web/scripts/verify-package.sh`
  and `docker build -f deploy/web.Dockerfile .`. Commit `docs/guide/` once B8 has passed.
- **Validation:** all gates green on `main`.
- **Depends on:** the stream's tickets
- [x] done
- Notes: (2026-09-12) Done incrementally rather than as one batch at the end: every ticket merged in
  dependency order with the gates re-run on the merged tree immediately after, so a failure could
  only ever belong to the merge just made. Final state of `main`: `go build`/`go vet` clean;
  `go test ./...` 34 packages ok **and separately green with a live Postgres attached** (throwaway
  `pgvector:pg16` on :55432 — never the stack's shared database); `web` typecheck clean,
  `vitest --testTimeout=20000` **1711/1711 in 93 files**; `web/scripts/verify-package.sh` PASS;
  `examples/web` build and typecheck clean at 18 guide pages; `docker build -f
  deploy/web.Dockerfile` EXIT=0. Conflicts resolved by hand in `App.tsx` (twice), `DeskPage.tsx`,
  `ProjectSettingsPage.tsx` and `components/index.ts` — DI13 is the one that mattered.

### D2: the stack e2e for everything new   [Status: DONE — 5 defects found and fixed | Model: orchestrator]
- **Scope:** `./e2e/run-stack-e2e.sh up` (mock), then `./e2e/run-stack-e2e.sh test` for:
  `revert`, `onboarding`, `budget`, `memory-write`, `console` (the appended assertions), and the
  whole existing suite once. Fix what fails **inside the ticket that owns it** (append to its
  Notes) or log it in the Discovered Issues Log if it is pre-existing. `./e2e/run-stack-e2e.sh clean`
  after; do not leave the stack up with abandoned schedules (DI: "one abandoned cron line took out
  a whole host").
- **Validation:** the run's summary line, pasted into Notes; `e2e/stack-e2e-logs-mock.txt` captured.
- **Depends on:** D1
- [x] done
- Notes: **(2026-09-12) RUN, and it earned its place: five real defects every offline gate missed —
  DI29 the worst, DI27/DI28/DI30/DI31 the rest, fixed in `1a60902` and `3649e3e`.** Mock mode was
  proved from the boot log before anything ran (`[agentd] ANTHROPIC_API_KEY unset → MOCK model
  proxy`) and the two lines that would mean billing — `real model proxy →`, `subscription mode →` —
  were absent. That mattered: this checkout has a REAL `.env` (108-char `ANTHROPIC_API_KEY` **and**
  a real `CLAUDE_CODE_OAUTH_TOKEN`), and the only reason a stack run did not bill is that
  `e2e/run-stack-e2e.sh:44-45` blanks both and `docker-compose.stack-e2e.yml:36-43` forces the local
  backends with no `override.yml` — README-stack's traps (a) and (b) are closed on that path by
  construction, not by anyone's discipline.
  **Targeted specs, all green:** `revert` 1/1, `budget` 2/2, `memory-write` 2/2, `console` 8/8, and
  `onboarding` 1/1 — the last needs its own invocation, `--mock-script
  e2e/mock-scripts/onboarding.json`, or it SKIPS, and A2's entire scenario lives inside it, so A2
  was unproven until that run.
  **Whole suite:** `78 passed, 1 failed, 39 skipped (8.9m)`. The 39 skip by design (each wants a
  particular mock script). The 1 is DI32, load flake, proven by a 9.8s isolated pass.
  `--trace on` is what made three of the five diagnosable: the trace's `resources/` holds request
  and response bodies by sha, which is how the budget defect was pinned on the spec rather than the
  server — the PUT carried `daily_tokens_hard: 5000` and the 200 echoed it back, so the product was
  right and the poll was reading a different project.
  Earlier pre-flight, still true: `e2e/` has no typecheck, so `playwright --list` is how the specs
  get compiled — 115 tests in 21 files parsed, every selector in A2's new scenario verified.
  **Superseded pre-flight note:** Two things done
  ahead of D2 so it does not burn a whole stack cycle on them. **(1) Every spec parses.** `e2e/` has
  no `typecheck` script and no `tsconfig.json`, so nothing had ever compiled the specs A2 and C4
  wrote; `npx playwright test --config playwright.stack.config.ts --list` does it without a stack
  and reports **115 tests in 21 files**, including `memory-write.stack.spec.ts` (2 tests) and A2's
  addition, which is appended *inside* the existing `onboarding.stack.spec.ts` test rather than as a
  new one — which is why the listing still shows one test there. **(2) Every selector A2's scenario
  uses exists.** `finish-onboarding` (`web/src/components/DeskPage.tsx:568`), `charter-panel`
  (`CharterPanel.tsx:89`), `charter-approve` (`CharterPanel.tsx:194`); `nav-desk`/`nav-workers` are
  generated as `` `nav-${key}` `` (`examples/web/src/App.tsx:612`) from `NAV_ALWAYS =
  ['desk','chat','workers','settings']` (`web/src/navReveal.ts:43`), so both are always present. The
  two seed-button assertions are sound in both directions: while in interview `DeskPage.tsx:556-589`
  renders the finish button *instead of* "Start from an org chart" (mutually exclusive branches), and
  `WorkersPage.tsx:219-251` withholds both "Start from a topology" buttons and swaps the surrounding
  prose so the phrase is not even present as text — and the assertion is `getByRole('button', …)`
  anyway, which would not match prose. After approval the architect worker makes the project
  non-empty, so the populated branch at `:247-251` renders the seed again, which is what the last
  assertion wants. **None of this substitutes for D2**: it proves the specs compile and their
  selectors exist, not that the behaviour happens.

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

**13. C1's merge would have silently reverted A2, and no test would have caught it.** (orchestrator, 2026-09-12) The only conflict in the whole wave was one hunk of `examples/web/src/App.tsx` — `ViewNav`'s `onChange`. A2's side deleted `if (view === "onboarding") onOnboardingDone()`, because ending the interview on a nav click *is* the defect A2 exists to fix. C1's branch predates A2, so its side still carried that line, plus its own new guide-hash cleanup. Taking C1's side — which is what "keep the incoming change" habitually means, and what a merge tool's default would offer — reverts A2 completely while leaving every unit test green, because nothing offline asserts that clicking away *keeps* an interview alive; only A2's stack e2e does, and that waits on D2. Resolved by keeping A2's deletion and C1's cleanup, with the reason written at the site so the next person does not re-add it. The general lesson for the fleet: **a branch that predates a merged ticket carries that ticket's old behaviour as its conflict side, and the danger is inverse to the conflict's size.** Every other file in the wave auto-merged, including two that A2 and C3 shared — and those needed a semantic check too (done: A2's `showFirstRunPanel`/`finish-onboarding` and C3's Activity/Triggers wording both verified present on the merged tree).

**14. A7's Validation command cannot pass as written, and the agent said so instead of fudging it.** (A7, 2026-09-12) The ticket asks for `grep -n AGENTKIT_DEFAULT_DAILY_TOKENS_HARD go/cmd/agentd/main.go docs/ops.md .env.example` to be non-empty for all three. `main.go` has no match and cannot have one: A3 resolved the defaults in `go/cmd/agentd/defaultbudgets.go` (constant `defaultDailyTokensHardVar`), reached from `main.go` only through `resolveDefaultBudgets(os.Getenv)` — which DI5 already recorded. So the literal string never appears in `main.go`. The ticket text is the thing that is wrong; the code is fine. Worth keeping as a pattern: a Validation line written from a design doc can outlive the layout it assumed, and the right move is the one taken here — report it, do not edit code to satisfy a stale grep.

**15. The repo already contained a guessed default daily budget, and it is 5× lower than the orchestrator's own recommendation.** (A7, 2026-09-12) `.env.example:189-190` carries commented `AGENTKIT_DEFAULT_DAILY_TOKENS_SOFT=50000` / `_HARD=100000`, added by A3, beside a comment stating the design intent plainly: *"A low value here is what keeps an invited friend's first project braked before anyone visits the console to raise it."* The orchestrator had independently recommended 250,000/500,000 to Kai, reasoning from "a normal working day should not be strangled" — which is the **opposite** intent to the one already written down. On re-reading, the repo's intent is the better one: the default is a brake the operator raises deliberately, not an allowance, and chat is exempt from the budget (`go/cmd/agentd/router.go:699-702`) so no budget can ever lock a human out of talking to their workers. Measured floor for the arithmetic: the core preamble alone is ~1.5 KB ≈ 380 tokens (`go/compose.go:623`), before project background, briefing, worker prompt, tool definitions and per-turn re-sending, and **no real-API token observation is recorded anywhere in this repo** — the only captured usage envelope is a mock row of 10 in / 6 out (`go/agentdb/token_usage.go:15-45`). So any number here is a judgment, not a measurement, and the honest recommendation is to adopt the low one already written: **50,000 soft / 100,000 hard**. Kai's call, still open.

**16. Removing a project does not purge it.** (A7, 2026-09-12) There is no `DELETE /agent/project` route. OM-8's "delete the throwaway project" can only mean removing it from `projects.json`, which revokes login but leaves its sessions, workers and memory in the database. A7 documented that in `docs/ops.md` §11f and in the new guide page rather than implying a clean delete exists. If a real tenant ever needs erasing, that route does not exist yet.

**17. Five page components had no `projectId` prop at all.** (C2, 2026-09-12) `ProjectSettingsPage`, `MemoryBrowserPage`, `WorkerEditor`, `WorkerTriggers` and `OnboardingPage` never took one; only `WorkersPage`, `DeskPage`, `ActivityPage`, `OrgChartPage` and `WorkerHistory` did. C2 needs it to key per-project dismissal, so it added the prop to each as optional defaulting to `''`. That is the right shape for a published package — no consumer breaks — and our shell passes it everywhere, so nothing is wrong today. The residue: a host that omits it gets dismissal scoped to the empty-string project, which only matters to a multi-project embedder, and none exists. Worth a follow-up only if one appears.

**18. A5 mounted a second, independent settings loader inside the settings page, and a save from the main form silently reverted the budget.** (A5 → orchestrator, 2026-09-12) A5 reported this itself, which is how it was caught — but framed it as "a pre-existing shape of risk ... not something A5 introduced structurally". That framing is wrong and the orchestrator rejected it. Before A5, `ProjectSettingsPage` held exactly one `useProjectSettings` instance; A5 mounts `BudgetPanel` *inside* that page (`ProjectSettingsPage.tsx:204`) and `BudgetPanel.tsx:62` gives it its own. Two independent GETs and two whole-object PUTs then render on one page — and the write path makes the consequence certain, not merely possible: `projectSettings.ts:311-313` builds the body by spreading the whole settings object minus `project`/`updated_at`, and `useProjectSettings.ts:9-13` states the route has no patch semantics. So the main form always sends `daily_tokens_soft`/`_hard` as read at mount. An operator who raises the hard limit in the panel and then saves anything in Advanced reverts it, with a success message and a changelog entry asserting the opposite. For the one control in this wave whose purpose is to be a trustworthy brake on spend, that is a defect, not a footnote. Sent back to A5 with the narrow fix: an optional `settings` prop on `BudgetPanel` so the parent supplies the single instance on the settings page while the Desk mount keeps its own, plus a test that saves from the main form and asserts the PUT carries the **new** limit — a behaviour switch, not a storage round-trip. **General lesson: two independent readers of one whole-object PUT on the same screen is a lost-update bug by construction, and the second one is always the one that introduced it.**

**19. The console gated two of the three fields the server calls operator-only.** (orchestrator, 2026-09-12) `go/httpapi/project_settings.go:96-106` refuses a non-operator whose body differs from the stored row on `DailyTokensSoft`, `DailyTokensHard` **or `MaxConcurrentJobs`** — three fields. A5 moved the two token budgets into its operator-gated panel and left `max_concurrent_jobs` in the open Advanced list (`ADVANCED_NUMERICS`), which made the page *worse* than before it started: the UI now looked like it gated operator-only settings while one of the three stayed editable, and because the PUT is whole-object a non-operator who nudged it lost **every other edit in the draft** to a 403 whose message — "only the operator may change budgets and caps" — never names the field responsible. Closed inside A5 as a deliberate scope call by the orchestrator: the field stays in Advanced and editable for an operator, and renders read-only with "Only the operator can change this." otherwise (`ProjectSettingsPage.tsx:302-320`, `ReadOnlyNumericSetting` at `:471`), on the same fail-closed `useWhoami` default as everywhere else. `briefing_max_bytes` and `snapshot_ttl_days` are untouched — the server does not guard them. **General rule: a UI gate that covers a subset of a server's guarded set is worse than no gate, because it teaches the reader that ungated fields are safe.**

**20. Two "Why?" fields now coexist on the settings page, and that is fine.** (orchestrator, 2026-09-12) Since `BudgetPanel` shares the page's settings instance, its rationale input binds to `settings.rationale` (`BudgetPanel.tsx:310`) — the very value the main form's field binds to. So there is one reason per save and the config log cannot be handed a reason belonging to the other form, which was the thing worth checking given how much this repo rests on the changelog being true. Two inputs onto one field is a presentational wart, not a defect; tests disambiguate with `getAllByLabelText('Why?')`. Recorded rather than fixed.

**21. The Desk's "About this screen" was showing the wrong page, and nothing could have failed.** (orchestrator, 2026-09-12) Found while integrating C2, by asking a question no ticket owned: *which page does each surface actually get?* Three pages declared `surfaces: [desk]` — `the-desk` (part 2, order 8), `when-a-worker-asks-you` (part 1, order 7) and `the-architect` (part 3, order 12) — and the consumer resolves a collision by taking the first page in part/order (`buildGuideParagraphs`, `examples/web/src/App.tsx:98-106`). So the Desk's About line was the paragraph about *a worker asking you a question*. Design **§4.4** is the authority and it is unambiguous: Desk → `the-desk`; `when-a-worker-asks-you` and `the-architect` are **Desk ROW links, fired by G4 (ticket C5) on the first ask and the first rewrite** — a different mechanism that reads slugs from `firsts.ts`, not front matter. Both pages were claiming a G2 surface they were never assigned. Fixed by setting both to `surfaces: []` with the reason in the front matter. Then checked every surface against §4.4: **12 of 12 now match, 0 mismatches** (`memory` → `the-rulebook` and `onboarding` → `your-first-hour` are both collisions the table sanctions, and the winner matches the order §4.4 lists). Three things make this worth recording. **(a) No test could have caught it.** C2's acceptance criteria are "a surface with no paragraph renders nothing" and "dismissal is per surface" — both true, both green, while the content was wrong; the unit tests use fixtures, and "first wins" is deterministic, so there was nothing flaky to notice. **(b) It sat in the gap between three tickets** — B wrote the front matter, C1 built the generator, C2 built the consumer, and none of them owned "the mapping is right". **(c) The silence was the defect.** Added `reportSurfaceCollisions` to `examples/web/scripts/build-guide.mjs`, which now prints every contested surface and names the winner on each build. It reports rather than throws, because §4.4 sanctions two of them — a hard failure would break a build the design blesses.

**22. The ticket claimed the seven sentences were in the design. Six of them were not.** (C5 → orchestrator, 2026-09-12) C5's scope says "the seven sentences are in design §3 G4 and its table §4.4; write them once in `firsts.ts`", and the orchestrator's brief repeated it as "use those, verbatim; do not invent your own wording". Checked: design §3 G4 (`design/2026-09-11-onboarding-and-the-guide.md:237-258`) gives **one** worked sentence, for `first-worker`, and then specifies a shape — "Seven kinds, seven sentences, one link each. Not a checklist, not a progress bar." §4.4 is the surface-wiring table, not copy. So the instruction was impossible as written, the same class of defect as **DI14**. C5 neither invented prose nor blocked: it took, for each remaining kind, the closest **already-shipped verbatim phrase** for that concept — `WorkerTriggers.tsx`'s own two sentences for schedule and subscription, and the §4.2 guide table's own trap lines for rewrite, ask, revert and memory — and sourced every one in a comment at `web/src/firsts.ts:10-24`. That is the right move under the plan's fixture rule, and the result reads consistently ("This is the project's first X. <the thing it is> — read …"). **But it means six of the seven sentences are effectively authored here rather than specified, and they are the first words a new human reads about each concept.** One is worth a second look: `first-rewrite` reads "A quiet architect is the alarm — read about its loop", which is a warning about the architect going *silent*, not an explanation of what a rewrite is — sound as a guide-table trap line, oblique as a first encounter. It is one object to edit (`FIRST_NARRATIONS`), so this is a copy review for Kai and Jack, not a code change. Not blocking.

**23. `first-revert` will essentially never fire for a human revert, and that is a Go-side limitation.** (C5, 2026-09-12) A revert is not its own action: `go/cmd/agentd/mcp_config_log.go` says a revert "is an ordinary `worker_prompt_write`/… whose rationale names the event being restored", and `go/agentdb/config_revert.go:90-92` only *defaults* the rationale to `revert of <action> (seq <n>, event <id>)` when the caller supplies none. The console's own `RevertControl` (`ChangelogView.tsx`) **requires** a human-typed rationale — so a revert done through the shipped UI never matches the pattern, and only a worker- or API-driven revert with no rationale would. C5 detects on `/^revert of /i` and falls back to the underlying action's kind, so such a revert narrates as `first-rewrite` rather than as nothing. Correctly left alone: the real fix is a marker field on `ConfigEvent` set by `RevertEvent`, which is `go/agentdb` migration territory and not an M-sized web ticket. Consequence to be honest about: **one of the seven sentences is close to dead copy until that field exists.**

**24. `first-memory` needed a second fetch, and its timestamp is the newest memory, not the earliest.** (C5, 2026-09-12) Memory writes are not config events — confirmed in `go/agentdb/memories.go` and `go/cmd/agentd/mcpserver.go`, neither emits one — so the fold has no route to them and `useDesk` was not in the ticket's file list. C5 merged a synthetic record client-side from `useMemories({limit:1})` in `DeskPage.tsx`. The read route returns newest-first, so on a project that already has memory history the synthetic record carries the *newest* row's timestamp, not the first. Harmless where it matters (a brand-new project has one row, which is both) and self-limiting (once the kind fires it is locked forever, so the cost is firing on the wrong visit, never twice). The clean fix is a route into the fold, which means touching `useDesk`.

**25. `first-ask` narrates the earliest still-OPEN ask, not the earliest ask ever.** (C5, 2026-09-12) Deliberate and documented at `web/src/desk.ts:484-498`: an answered ask has no row left on the Desk to carry the sentence, so the record it narrates on must still be open. The residue is retrofit-only — switch this on for a project whose true first ask was already answered and whatever is oldest-and-still-open gets labelled "the first ask". Nothing to do for a new project, which is what this wave is for.

**26. Two client helpers, one word apart in meaning, and the wrong one fails silently.** (D2, 2026-09-12) `projectClient(request, project)` binds to the project you name; `newProjectClient(request, prefix)` takes a **prefix** and mints a brand-new project of its own (`e2e/helpers/api.ts:803-813` vs `:821-829`). A5's `budget.stack.spec.ts` passed the page's project into `newProjectClient`, so it polled a different, empty project — where `GET /agent/project-settings` answers with `DefaultProjectSettings`, i.e. `0/0`, forever. Nothing errors, because the wrong project is a *valid* project and its settings read back as plausible zeros. What settled it was the Playwright trace's stored bodies: the PUT sent `daily_tokens_hard: 5000` and the 200 echoed it back for `e2e-budget-mtycxcve-sn0wz`, proving the product right and the spec wrong. Worth renaming one of the two, or giving `newProjectClient` an options object; a helper whose argument means something different from its neighbour's is a trap that will be re-sprung.

**27. An assertion that can pass without the thing under test happening.** (D2, 2026-09-12) `memory-write.stack.spec.ts` proved "the new registry version landed" with `expect(page.getByText(NEW_RULES)).toBeVisible()` — but `NEW_RULES` is also the text sitting in the form's own textarea, so it matches whether or not the POST completed. The test then read `/agent/memories/current` once and got the seed. It failed in a serial run and passed in isolation, the signature of a race rather than a broken route: `NewestMemory` orders `created_at DESC, id DESC` (`go/agentdb/memories.go:646`) and is correct. Fixed by polling the route for the expected content. Second defect in the same spec: it asserted `content` on the LIST route, which returns `MemorySearchResult` carrying **`snippet`** (`memories.go:180-197`), so it compared `undefined` to the note. Full content has its own route and the spec now reads it.

**28. `getByLabel` matches a substring, and the budget bar's label contains the input's.** (D2, 2026-09-12) `page.getByLabel('Hard limit')` resolved to two elements: the `<input aria-label="Hard limit">` and the progress bar labelled *"Today's tokens against the hard limit"*. A locator artefact, not an accessibility defect — a screen reader hears two clear, distinct names — so the spec changed, to `getByRole('spinbutton', { name: 'Hard limit' })`. Worth distinguishing from DI20/DI29, where the ambiguity was real.

**29. A2 shipped unable to do the one thing it existed for, and no offline gate could have known.** (D2, 2026-09-12) The worst finding of the wave. `useInterviewState` (`examples/web/src/onboarding.ts`) stopped polling the instant `inInterview` was false — and the FIRST check runs at mount, before the `onboard` session exists, which is exactly that state, for the ordinary reason that the interview has not begun. `settled.current` latched there, the interval never fired again, the Desk could never learn an interview had started, and **"Finish setting up this project" never appeared** — the precise defect A2 was written to prevent. Its comment said the flag meant "the interview is confirmedly over"; the code could not tell "over" from "not started" — the same conflation as DI21 and DI23. Now only `hasArchitect` ends the watch, at the cost of a project that never onboards polling two cheap GETs every 4s while its tab is open: the same order as the Desk's own live refresh, and the right side to err on. **Why nothing caught it:** `examples/web` has no test runner at all — no `test` script, no test files — so the app shell's logic is only ever covered by stack e2e, and A2's 59 unit tests all fed `inInterview` in as a prop rather than computing it. A2's own e2e scenario would have caught it, and it was written; it never ran, because `onboarding.stack.spec.ts` SKIPS without `--mock-script`. Three gaps had to line up, and they did.

**30. A2's e2e insertion broke the assertion that followed it.** (D2, 2026-09-12) A2 spliced its navigation checks into the middle of the existing onboarding test and ended on the Workers view; the pre-existing assertion two lines later expects `run-architect`, mounted only on the onboarding view (`OnboardingPage.tsx:166`). The old assertion failed because of where the new one had walked the browser, not because anything was broken. Reordered so view-dependent assertions run last, each on the view that owns it. General rule for appending to a long browser test: **the browser has one position, and it is shared state.**

**31. A UI reorganisation must grep the specs that select the fields it moves.** (D2, 2026-09-12) A5's G5 split moved `base_image` into the collapsed "Advanced" tier. `product-ui.stack.spec.ts:42` has filled that field since long before, and spent **four minutes** timing out on a field that is now one click away. Neither A5's Files list nor the orchestrator's review checked which existing specs select the moved fields. Fixed by opening Advanced first; a grep of `e2e/features` for the other moved fields found no further UI locators, the remaining hits being API-level.

**32. The full stack suite has its own load flake, distinct from the web unit suite's.** (D2, 2026-09-12) `product-ui.stack.spec.ts:114` ("a session permalink opens that session directly") waited out its 120s for an assistant turn during the full run and **passes in isolation in 9.8s**. No `port pool is exhausted` line appears anywhere in the run, so this is not the documented port ceiling; it is a full suite standing up many session containers in DinD on a machine also running several other threads' work. Same shape as DI8 (the web suite's `user-event` timeouts) and the same remedy: judge a stack failure by an isolated re-run before believing it.

**33. B7's acceptance grep could never be empty, because the convention document has to quote the convention.** (B7, 2026-09-12) The criterion was `grep -rn 'fixture: to be captured\|screenshot:' docs/guide/` is empty. But `docs/guide/README.md:63` explains the fixture rule *using* the literal marker (`` `<!-- fixture: to be captured by B7 -->` rather than inventing one ``) and `:112` does the same for screenshots — so the grep matches the documentation of the rule, for ever, no matter what any implementation fills in. The agent reported it rather than mangling README to satisfy a grep, which was right. Fixed by adding `--exclude=README.md` to the criterion: a convention document must be able to state its own convention, and the acceptance check is what was wrong. Same family as **DI14** (a Validation line that cannot pass as written) and **DI22** (a ticket asserting the design contained something it did not).

**34. "≥ 10 images" was a number nobody could hit honestly.** (B7, 2026-09-12) The Validation asked for at least ten files in `docs/guide/img/`, while the pages carry exactly **3** `<!-- screenshot: … -->` placeholders — all in `your-first-hour.md`, none anywhere else including `for-operators/`. §1.5 ties images one-to-one to those comments, so the count was fixed by B1–B6 long before B7 could run, and reaching ten meant either inventing seven decorative images nothing asked for or pushing pages further past their word budget for pictures with no narrative role. The agent declined to redefine the criterion on its own authority and asked. **Criterion corrected to "equals the number of placeholders the pages carry".** The general point: an acceptance number written in one ticket about another ticket's output is a guess wearing the clothes of a requirement.

**35. The 450-word cap assumed placeholders cost nothing.** (B7, 2026-09-12) `docs/guide/README.md` sets 150–400 words target, 450 hard. Four pages now exceed it: `when-a-worker-asks-you.md` (475), `a-workers-instructions.md` (461), `memory.md` (457), `the-rulebook.md` (452). All four were already at 413–448 **before** B7, so the budget was set against pages whose fixtures were one-line HTML comments; a real captured prompt, memory or ask message costs 30–60 words. The worst case is instructive: `when-a-worker-asks-you.md` is over because the architect's real message runs four sentences and the page already carried B3's "unasked question" sidebar, so getting under 450 meant truncating a verbatim quote — which README itself forbids ("shown in mono, whole, never truncated with …") — or deleting another ticket's work. Left real and over rather than fake and compliant. Kai's call whether the cap moves or those pages lose a paragraph.

**36. `DELETE /agent/session/{id}` does not clear an outstanding ask.** (B7, 2026-09-12) Found while making a fixed-id fixture project re-runnable. `DeskPage.tsx`'s own words — "an ask leaves the Desk's Asks stack when it is answered or times out" — turn out to be exhaustive: deleting the session sitting under an `awaiting_human` ask does **not** answer it, so repeated runs accumulated duplicate "architect" ask rows (2, then 3, then 4) even after every session had been deleted. Worked around in the capture spec by photographing the single real ask row rather than the whole Desk, commented in place. The underlying gap matters anywhere `DELETE /agent/session` is relied on to retire an open ask — it will not.

**37. Another session committed this thread's in-flight files as `wip`.** (orchestrator, 2026-09-12) Commit `88ae096` ("wip", no body) landed on `main` at 14:20:53 carrying exactly B7's then-current working set — the three screenshots, `capture-fixture.spec.ts`, `guide-fixture.json`, `captured-fixtures.json` and the `playwright.stack.config.ts` edit. Neither the orchestrator nor B7 made it, and every session in this repo commits as the same git identity, so authorship cannot be told apart from the log. It is almost certainly a broad `git add` from another thread working in the shared checkout — the precise hazard the launch rules name ("stage **only your own files**, never `git add -A`"). Harm here was nil: the files were this thread's, nothing was lost, and the later versions were still in the tree. The lesson is that it could as easily have swept up a half-finished edit of someone else's, committed under a message describing something unrelated. History left as found rather than rewritten — never rewrite a commit you did not author, even a local one — and the content recommitted properly on top.
