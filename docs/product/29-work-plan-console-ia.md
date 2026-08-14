# Work plan — the console IA change (Activity, Triggers, History, progressive nav)

*Created 2026-08-14. Implements the design in [`28-console-ia-design.md`](./28-console-ia-design.md),
which answers the four questions left open by the K7–K9 decisions in
[`15-operator-console-design.md`](./15-operator-console-design.md) §14. Read doc 28 first — it is
short, and every ticket below assumes its reasoning rather than repeating it.*

*Written to be executable by an agent with no other context than the repo.*

## EXECUTION RULES

1. **Ground yourself first**: doc 28 (the design), then doc 15 §3 (tokens, type rules) and §14
   (K7–K9), then the Standing traps in doc 16 and the Discovered Issues Logs of docs 16, 21 and 23.
   All bind you. Then the files an item names.
2. **This branch may be shared with other live sessions.** Never `git add -A`, never `git commit
   --amend`, never `git stash`, never rebase. Stage explicit paths; if `git status` shows files you
   did not touch, leave them alone and say so in your report.
3. **Run each item's validation verbatim before ticking it.** Tick boxes in THIS file as you go, and
   append surprises to the Discovered Issues Log at the bottom.
4. **Do not touch:** `.env`, `e2e/experiments/`, `sandbox/`, the shared test Postgres `ao-test-pg`
   on `localhost:5433` (use, never recreate).
5. **Search hygiene:** several `web/src` files contain non-UTF8 bytes and grep as binary — **always
   `grep -an`** in `web/src`.
6. **The compose stack is a serial resource.** The e2e stack may hold port 8080; the main stack runs
   fine on `WEB_PORT=8081`.
7. **Design authority:** doc 28 wins on IA and interaction; doc 15 §3 wins on tokens and type; doc
   21 §4–§5 wins on motion. Where this plan and any of them disagree, they win — say so in the DIL.
8. **`web/` rules that have not changed:** no new runtime deps, every export goes through
   `web/src/index.ts`, npm (`npm ci`) — while `examples/web/` is **yarn only**.

*Validation shorthand:*
- **WEB** = `cd web && npm ci && npm run typecheck && npm test`
- **SHELL** = `cd examples/web && yarn && yarn typecheck && yarn build`
- **RIG** = rebuild the shell (`SHELL`), then from `e2e/`: start `node ux-stub/stub-server.mjs` and
  run `node ux-stub/shoot-app3.mjs`; **read the screenshots with your own eyes, both themes.**

## Traps carried in from earlier work (all previously filed, all still true)

- **J1 — two unit systems.** Deliveries, events, schedules and attention requests stamp unix
  **seconds**; config events stamp unix **milliseconds** (`desk.ts` header). Every ticket that sorts
  records by time depends on this. Do not "tidy" one into the other.
- **`AutomationPage`'s `tab`/`selected` props are fully controlled.** Passing either without its
  matching `onTabChange`/`onSelect` freezes it — the human can never leave the row you deep-linked —
  *and* disables the page's own URL sync. (Found 2026-08-14 while fixing the chart's clock link;
  the fix writes the URL and lets the page self-initialise. See `App.tsx`.)
- **`WorkerLineage` reads the *Desk's* watermark** (`useFeedWatermark('desk', projectId)`) for its
  cumulative banner. B2 must break that borrowing, not preserve it.
- **The stack serves a BUILT web image** — `docker compose up -d --build web` before asserting any
  UI change in a browser test.
- **Port pool**: delete sessions in e2e teardown or the host runs out.
- **Worker names must not be substrings of each other** in fixtures.

---

# Tier A — the folds *(pure, testable, no UI; do these first)*

The codebase's own seam: pure folds live in `web/src/*.ts` with no React, no fetch and no clock;
components mount them. `desk.ts` is the model to copy — read it before writing A1.

## A1 — `activity.ts`, the project rail fold *(the real work of this plan)*

- [x] New pure module `web/src/activity.ts` + `activity.test.ts`, in the exact shape of `desk.ts`
  (no React, no `window`, no `fetch`, `nowSeconds`/`lastSeenMs` passed **in** as parameters —
  a fold whose output depends on the wall clock is untestable).
  It folds project events, event deliveries, jobs/sessions, config events, attention requests and
  schedule failures into **one time-ordered list of occurrences**.
  - **The unit is the occurrence, not the record** (doc 28 §1.2). An event that produced a delivery
    that ran a job is **one** row: event as headline, delivery as the preposition (`woke`), job as
    the outcome. A record earns its own row only when it has no parent occurrence — a human config
    change, an attention request, a schedule that failed to provision.
  - **Normalise timestamps exactly once**, at the fold boundary, in a named exported function.
    Test it with one record of *every* kind in the same table. **This is the highest-risk line in
    the plan**: get it wrong and every config change sorts ~50,000 years into the future, silently,
    at the top of the rail, looking entirely plausible.
  - Each occurrence carries a `SpineGlyphName` from the **closed set** (`spine.tsx`) — do not invent
    a glyph, and do not add one for "event" or "job"; the glyph answers *who did this*, never *which
    table this came from*.
  - Emit `SpineGap` markers for quiet stretches, as the Desk does.
  - Copy `desk.ts`'s editorial rules (design §11): the operator's vocabulary, and a failure with no
    reason column **says so** rather than rendering a blank cell.
  *Validation:* WEB. The test table must include: a config change and a delivery one second apart
  (proves J1), an event+delivery+job triple folding to one row, an orphan attention request, and a
  quiet gap.

## A2 — `workerHistory.ts`, the one-worker rail fold

- [x] New pure module + test folding **jobs and prompt versions** for a single worker into one
  time-ordered list — the same occurrence model as A1, scoped to one worker. This is what makes a
  rewrite and the runs either side of it adjacent rows, which is `BeforeAfterView`'s entire manual
  job (doc 28 §2.3).
  Reuse A1's normalisation function; do not write a second one.
  *Validation:* WEB, with a table proving a rewrite lands between the job before it and the job
  after it.

## A3 — `navReveal.ts`, the progressive-nav predicates

- [x] New pure module + test: given counts (workers, memories, events, subscriptions) and the
  sticky set already revealed for a project, return which nav entries are visible.
  Rules (doc 28 §3.3), each one a test case:
  - Always: Desk, Chat, Workers, Settings.
  - Memory on the first memory; Activity on the first event; **Chart on the first
    *subscription*** — not on a worker count (doc 28 §4.1). Schedules do **not** reveal the chart.
  - **Sticky**: once revealed for a project, an entry stays revealed even when the count returns to
    zero. Deleting the last worker un-reveals nothing.
  - **Order is fixed** — the function returns entries in the canonical order and never reorders.
  *Validation:* WEB.

---

# Tier B — the surfaces *(mount the folds; these are judged by eye as well as by test)*

## B1 — the Activity view

- [ ] New `web/src/components/ActivityPage.tsx` mounting `SpineRail`/`SpineRow`/`SpineGap` over A1.
  - Filter chips `all · events · jobs · changes` that **subset the rail in place** — they must not
    swap the surface, and the rail, the watermark and the scroll position survive a filter change
    (doc 28 §1.3). If a filter re-mounts the rail, it is a tab wearing a chip's clothes and the
    ticket is not done.
  - **Paging is by time window** ("Show earlier"), never `LIMIT n` per source (doc 28 §1.4).
  - Its own watermark, `ACTIVITY_SURFACE` — the Desk keeps its own.
  - Reuse the existing staged-arrival machinery (`useStagedFeed`, the "N new" pill, and the
    "Pause live updates" toggle WCAG 2.2.2 requires) exactly as `EventsPage` does today.
  *Validation:* WEB + RIG — look at the populated rail in both themes. Seven record kinds should be
  readable without a legend.

## B2 — Workers: `History` replaces the `Jobs` and `Lineage` tabs

- [ ] Mount A2 as a single **History** tab; delete the separate Jobs and Lineage tabs. Keep the
  diff rendering, the "mark viewed" affordance and the version-restore path — they move onto rows,
  they do not disappear. **Break the borrowed Desk watermark** (see Traps) and give History its own.
  `BeforeAfterView` becomes reachable-but-redundant here; do **not** delete it in this ticket —
  D2 owns that once the rail has proved itself.
  *Validation:* WEB + RIG. A rewrite must render between the run before and the run after it.

## B3 — Workers: the `Triggers` tab, and `Woken by` on Configuration

- [ ] Move `SubscriptionEditor` and `ScheduleEditor` intact into a new **Triggers** tab on
  `WorkersPage`, scoped to the selected worker. Every subscription targets exactly one worker, so
  each row has exactly one home.
  Add a **read-only** `Woken by` summary to the Configuration tab — the event type and filter, the
  cron in words, and `▸ edit triggers` (doc 28 §2.2). Read-only summary + edit one click away is
  the Desk's own rule; do not make it editable in two places.
  *Validation:* WEB + RIG.

## B4 — progressive nav in the shell

- [ ] Mount A3 in `examples/web/src/App.tsx`. Sticky set in `localStorage`, per project, keyed
  beside the existing watermark keys (`watermark.ts` is the precedent).
  - Appearance is **one 180ms fade + height**, gated on the existing `useReducedMotion` hook. No
    pulse, no badge, no glow — §3.2 forbids spending colour on chrome, and the asks badge is the
    only number the design allows in the chrome.
  - The **action that caused the reveal announces it in words** (doc 28 §3.2): creating the first
    worker confirms with what appeared and what it is for. Copy follows design §11.
  *Validation:* SHELL + RIG — confirm a fresh project shows exactly four entries.

---

# Tier C — the chart *(the bill for keeping it, K7)*

## C1 — merge Replay into the propagation panel

- [ ] Two additions and one deletion (doc 28 §4.2). Add template loading + `JsonObjectEditor` to
  the panel's paste-an-event field, and move `EventReplayPanel`'s emit flow across **with its
  confirm dialog intact**. **Delete the textual match list** — the answer renders on the shape, and
  keeping the list keeps the duplicate the merge exists to remove.
  **Trace is primary; Emit is never the default button** — it is the only real mutation on a
  surface whose rule is "every gesture is a proposal", and it is irreversible. The verb holds
  through the flow: *Emit this event* → *Emit a real event?* → *Emitted*, naming what it woke.
  *Validation:* WEB + RIG.

## C2 — the doc 21 chart defects

- [ ] Fix X1 (the clipped `SpineGlyph` — CSS cannot size an `<svg>` nested inside SVG), X2 (wire
  label collision at shared ranks), X3 (floating schedule dials), X4 (duplicated entry-pip label),
  X9 (a parked ask is invisible on the plate — the rose state exists on the Desk but not here) and
  X10 (a five-strike-disabled schedule renders as a normal dial).
  Read doc 21 §2 for the evidence screenshots before starting.
  *Validation:* WEB + RIG, both themes, against the populated fixture org.

## C3 — chart reveal threshold

- [ ] Wire A3's first-subscription predicate to the chart's nav entry.
  *Validation:* SHELL + RIG.

---

# Tier D — close-out

## D1 — the regression net

- [ ] Rewrite `e2e/features/console.stack.spec.ts` for the new IA. It holds ~94 nav and label
  references and is the single choke point for this whole plan; it is also the only thing standing
  between a fold bug and a silent regression. Cover: the reveal curve (a fresh project shows four
  entries; creating a worker reveals Activity), the Activity rail interleaving three record kinds in
  time order, Triggers editing a schedule from the worker page, and the chart's clock deep link
  landing on that schedule's row.
  *Validation:* `./e2e/run-stack-e2e.sh up mock` then
  `./e2e/run-stack-e2e.sh test mock -- e2e/features/console.stack.spec.ts`, green, plus the existing
  `product-ui` spec still green (shared stack state).

## D2 — retire what the folds replaced

- [ ] Once B1–B3 are green and looked at: remove `EventsPage`'s Events/Jobs/Changelog tabs (the view
  becomes Activity), retire `AutomationPage` as a top-level view, and delete `BeforeAfterView` if —
  and only if — B2's rail genuinely reads better. **State the judgement in the DIL either way.**
  Keep `ChangelogView`'s filtering if Activity's chips do not yet cover its nine lenses; that is a
  real feature and losing it silently would be a regression, not a simplification.
  *Validation:* WEB + SHELL + D1's spec.

## D3 — the artifact-viewer decision *(deferred, still open)*

- [ ] Not part of this IA change, recorded here so it is not lost: `ArtifactViewer` and six siblings
  are exported and mounted nowhere, and `AgentChat` keeps the state write-only
  (`const [, setViewerArtifact] = useState(...)`), so **clicking an artifact in the panel silently
  does nothing.** Either wire `onOpenArtifactViewer` through the shell or delete the family. The
  current state is the worst of both. Kai deferred this on 2026-08-14 as a small front-end bug —
  it needs a decision, not an investigation.

---

## Discovered Issues Log

*Append as you go. This section is the most valuable part of the file for whoever comes next — when
a design doc and this log disagree, the log is closer to the code.*

### Tier A, 2026-08-14 — the three folds (A1, A2, A3 done)

**1. The Desk has a blind spot, and the rail exposed it.** Design 28 §1.2 said a record earns its
own row only when it has no parent occurrence, and listed "an attention request" among them. Writing
A1 showed *why that matters*, and it is not a rail detail: the Desk's Asks stack is
`awaiting_human` **deliveries** joined to open requests, so it can only ever surface a worker that
an **event** woke. But `request_human_attention` is a core MCP tool, and a worker in an ordinary
interactive session — a chat, or a session an embedding application created over HTTP — calls it
with no subscription and no delivery anywhere. **Those questions are invisible on the Desk today.**
A1 emits them as standalone `ask` rows (`buildUnparkedAskRecords`, pairing checked against the rows
`buildWorkRecords` already produced so the two can never disagree). Whether the *Desk* should also
show them is a product call for Kai, filed here rather than fixed: doc 28 §6 says this pass does not
touch the Desk.

**2. The J1 tests were mutation-tested, deliberately.** All 29 A1 tests passed on the first run,
which is not evidence of anything — so the normalisation was inverted on purpose
(`toMs(entry.createdAt, 'ms')` → `'seconds'`) and the suite re-run: **3 tests failed**, including the
interleaving table and both window/gap cases. The bug this plan warns about is genuinely caught, not
merely described. Anyone touching `toMs` should repeat this rather than trust a green run.

**3. A type that looked right, tested green, and was silently `never`.** `NAV_ALWAYS` was declared
`readonly NavEntry[]`, so `(typeof NAV_ALWAYS)[number]` widened to `NavEntry` and
`Exclude<NavEntry, NavEntry>` collapsed to `never` — making `REVEAL_RULES` a `Record<never, …>`
whose three callbacks were implicitly `any` and whose lookup was "not callable". **All 17 tests
passed anyway**; only `tsc` caught it. Fixed with `as const` plus a derived `NavConditionalEntry`,
which now makes adding a nav entry without a reveal rule a *type error* rather than a button that
never appears. General lesson for the folds: `npm test` is not the gate, `typecheck && test` is.

**4. The merge's premise checks out in code.** `runsAround` — the before/after comparison that
`BeforeAfterView` performs by hand against re-fetched data — is an index walk on an ordered rail,
about fifteen lines. That is the clearest evidence so far that B2's merge is a simplification rather
than a rearrangement.

**5. Deliberate deviations from the ticket, both small.** (a) `buildWorkerHistory` takes
*deliveries* and filters by worker rather than consuming `GET /agent/sessions?worker=`, which is
what `WorkerJobHistory` uses today — the fold filters rather than trusting the query, so a page that
over-fetches cannot put another worker's job on the rail; B2 must decide which route feeds it.
(b) Filtering happens *inside* `buildActivity` rather than in the component, because gap markers are
only correct for the list actually on screen — a four-hour hole between two jobs is not a hole if a
config change sits in it. There is a test for exactly that.

*Validation run: `npm run typecheck` exit 0; full suite **1352 tests / 68 files green**
(104 in the four affected files); `examples/web` `tsc --noEmit` exit 0.*
