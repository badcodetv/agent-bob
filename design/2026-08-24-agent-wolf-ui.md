# Agent Wolf — UI Design

> **This is a companion to `design/2026-08-20-agent-wolf.md`, not a replacement.** The main plan
> specifies the UI tickets (W11, W13, W14, W23, W24) in detail; it never *designed* the UI. This
> document supplies what was missing — the information architecture, the screen flow, the visual
> language for trust, and the Wolf-versus-Orange surface split — and § "Reconciliation" states
> exactly which existing acceptance criteria it extends and which it **contradicts**.
>
> **It must be folded into the main plan before W13 is cut**, the way
> `2026-08-21-agent-wolf-report-layer.md` was folded into revision 4. Until it is, the main plan is
> still the authority for anything this document does not name.

Status: **approved** 2026-08-24 (D1–D5, § 2b visual direction, § 6b derived CSP + R45) — folding into the main plan as revision 5
Date: 2026-08-24
Relates: `design/2026-08-20-agent-wolf.md` (revision 4), `design/2026-08-21-agent-wolf-report-layer.md`,
`docs/19-embedding.md`, `docs/09-frontend-components.md`

---

## 1. The five decisions this document rests on

Each was taken by the owner on 2026-08-24, against evidence read out of the code at the time. Each
is reversible; the reasoning is recorded so a reversal is an informed act.

| # | Decision | Displaces |
| --- | --- | --- |
| **D1** | **Share Orange's UI code at tiers 1 and 2; keep the chat as an iframe.** Wolf installs `@agentkit/chat-ui` for types, pure logic and presentational components, rendered under **Wolf's own** `ThemeProvider`. Only `AgentChat` and the stateful pages stay behind the embed. | The main plan's § "Decisions taken during design" bullet **"Iframe-only UI reuse"**, which is now correct only for tier 3. |
| **D2** | **The board is an attention queue**, ordered by what demands a human, not by time. | W13's implicit chronological board. |
| **D3** | **Trust is rendered on two independent channels** — provenance (who wrote it, always on, never alarming) and severity (how much to trust it now, mostly absent, escalating). | Four tickets' independently-chosen treatments. |
| **D4** | **The detail page is two columns**: Wolf's content scrolls on the left, the Orange conversation is a persistent full-height rail on the right. | W14's "compose above the condition table without restructuring it". |
| **D5** | **One shared book.** `owner` is a byline, not a permission. Anyone allowlisted may act on anything. | Nothing — this ratifies what the code already does, and fixes the queue's wording. |

### Why D1 reverses the plan's standing decision

The plan recorded `web/` as "not installable" and concluded iframe-only reuse. Measured on
2026-08-24, that was a **packaging** fact, not an architectural one:

- `web/package.json` already declares `peerDependencies` on react/react-dom/MUI/emotion, already
  points `main` at `dist/index.js` and `types` at `dist/index.d.ts`, and already exports a curated
  896-line public surface (`web/src/index.ts`). Its header records that app coupling was
  deliberately factored out: *"No dependency on Platinum app state, routing, or contexts."*
- **It emits cleanly today.** With `"noEmit": false` and `"allowImportingTsExtensions": false`,
  `tsc -p` exits **0** with zero errors and produces 112 modules plus `.d.ts` and sourcemaps. The
  only things standing between the current state and a publishable package are `"private": true`,
  that one `noEmit` line, and five runtime imports misfiled under `devDependencies`.
- **45 of 53 components take props and nothing else.** Only eight touch context or `fetch()`:
  `AgentChat`, `AgentSessionList`, `ArtifactViewer`, `ArtifactPreviewDialog`,
  `InlineArtifactPreview`, `WorkersPage`, `WorkerChatPanel`, `ProjectSettingsPage`.

The coupling cost is therefore **not uniform**, which is what makes a tier line the right
instrument:

```
 TIER 1 ── types + pure logic ────────────────────────────── SHARE
   types.ts · agentEventReducer · artifactTree · artifactFilters
   permalink · replayEvents          — zero React, wire-format churn only
   NOT sharing this is actively harmful: Wolf hand-copies ArtifactInfo
   and learns about drift from a rendering bug.

 TIER 2 ── presentational components ─────────────────────── SHARE
   ArtifactPanel · ArtifactGrid · AgentMarkdown · DiffBlock
   DeliveryStatusChip · 41 others    — props in, callbacks out,
   styled by the HOST's ThemeProvider, which an iframe can never do.

 TIER 3 ── stateful pages + the chat ──────────────────────── IFRAME
   AgentChat · useAgentSession · EventsPage · ProjectSettingsPage
   These carry Orange's API contract AND its fetch behaviour. This is
   the only tier where an Orange change forces every client app to move,
   and the only tier the iframe genuinely earns its cost on.
```

**Distribution — orchestrator's call, flagged for override.** Consume the **built `dist`** as a
local tarball (`yarn pack` in `web/`, committed under `agent-wolf/vendor/`, referenced as
`"@agentkit/chat-ui": "file:./vendor/agentkit-chat-ui-<version>.tgz"`). Reasons: neither repo has an
`.npmrc` or a publish step, they sit in different GitHub orgs (`badcodetv` / `binocarlos`), and the
source has unresolved licensing — registry auth for a single consumer is machinery for nothing, and
this is reversible in an afternoon. Consuming `dist` rather than source also means
`peerDependencies` do the deduplication properly, instead of Wolf inheriting the ten-package Vite
dedupe list `examples/web/vite.config.ts:21-38` needs. Move to GitHub Packages when a **third**
application appears.

---

## 2. The trust language (D3)

**The product is the trust boundary, so it gets one vocabulary, defined once, used by every
ticket.** Ten distinct "do not fully trust this" signals are scattered across the four UI tickets.
They are not one axis. They are two, and conflating them is the failure mode:

> If model-authored content is styled as a warning, then the daily research notes — the normal,
> useful, everyday output — look permanently broken, and a real tamper alert loses all its force.

### Channel P — provenance. Always on. Never alarming.

A **stable property** of the content: did Wolf's deterministic evaluator compute this, or did a
model in a container write it?

| Value | Treatment | Applies to |
| --- | --- | --- |
| `machine` | **No treatment at all.** The default ground. | scoreboard, verdict band, condition table, metric charts, spec, verdict, evaluation, state changes |
| `model` | A **non-semantic** ground tint, a 2px left rule, and a provenance stamp reading `<worker-or-session> · <relative time>` | research notes, spec amendments, spec candidates, report candidates, report amendments, the report panel |

🔴 **The `model` tint MUST NOT be a `warning`, `error` or `info` palette colour.** It is a neutral
ground — derive it from `theme.palette.action.hover` or an explicit neutral token. The moment
provenance borrows a semantic colour, every research note reads as a problem and D3 has failed.

The stamp names `written_by_worker || written_by_session`. Both fields already exist on the pinned
`Tamper` shape and on `ReportRecord` (`createdByWorker` / `createdBySession`).

### Channel S — severity. Mostly absent. Escalates.

A **changing state**: how much should you trust this *right now*?

| Value | Treatment | Meaning |
| --- | --- | --- |
| `none` | Nothing rendered. | Fine. This is the overwhelming majority. |
| `degraded` | A `warning`-toned inline marker **plus a sentence naming the cause**. Never a bare icon. | We can see something, and it is less reliable than usual. |
| `attacked` | A full-width MUI `Alert severity="error"`, unmissable, naming the writer and the memory id. | Something wrote what it had no right to write. |

### The ten signals, mapped

| Signal | Channel P | Channel S | Rendered as |
| --- | --- | --- | --- |
| Research note in the timeline | `model` | `none` | tinted ground + stamp; prose via `AgentMarkdown` |
| Spec amendment / report amendment (proposed) | `model` | `none` | tinted ground + stamp + the word **Proposal**; accept/reject actions |
| Spec candidate / report candidate | `model` | `none` | tinted ground + stamp, on the go-live review screen |
| The report panel itself | `model` | `none` | the whole frame sits on the tinted ground, with the stamp above it |
| Tamper (`forged_row`, `hostile_retraction`, `cross_hypothesis_write`) | inherit | **`attacked`** | full-width `Alert severity="error"`, all three reasons distinguished **in words** |
| `indeterminate` condition | `machine` | **`degraded`** | a distinct colour treatment — **never** the `holding` treatment — plus W4's `reason` verbatim |
| Stale metric | `machine` | **`degraded`** | hatched trailing region on the chart + a caption: `never fetched` or `no update since <date>` |
| `stripped_count > 0` | `model` | **`degraded`** | a sentence above the panel with the count |
| Report drift (orphan / unfilled slots) | `model` | **`degraded`** | a sentence naming the slots |
| `titleTruncated` | `machine` | **`degraded`** | an ellipsis affordance; the UI never claims the title is complete |
| The support score | `machine` | `none` | labelled a **summary**, with an explicit note that it **decides nothing — only conditions do** |

Note how the two channels compose and why that matters: a research note is
`model` + `none` and looks **calm**; a tampered report is `model` + `attacked` and **screams**;
an indeterminate condition is `machine` + `degraded` — Wolf's own arithmetic telling you it could
not tell, which is a different thing again from a model being unreliable.

### Implementation

Two components in `web/src/components/trust/`, used by every other component:

```tsx
// Ground + stamp. Wraps model-authored content. Renders NOTHING for "machine".
<Provenance kind="model" worker="researcher-4f2a" session="" atMs={1789...}>
  {children}
</Provenance>

// Marker + mandatory cause sentence. Renders NOTHING for "none".
<Severity level="degraded" cause="no update since 12 Aug — FRED restated DGS10" />
<Severity level="attacked" cause="forged row — researcher-9c1b wrote memory mem_7f3a" />
```

`Severity` has **no** default cause and refuses to render without one. A bare marker that does not
say what happened is how a spec mistake stays invisible for three weeks.

---

## 2b. Visual direction

**Owned by us.** Jack is not involved in this project; there is no separate design authority and no
later restyle to defer to. This section is therefore normative, not a placeholder — W28 implements
it as the theme, and W13/W14/W23/W24 consume it.

**The product is an instrument, not a dashboard.** Its job is to make it hard to fool yourself
about a thesis you want to be true. Every rule below serves that.

### Five principles

1. **Colour is a scarce resource, spent almost entirely on severity.** D3 only works if the rest of
   the interface is quiet. A colourful UI has no headroom left for `degraded` and `attacked` to mean
   anything.
2. 🔴 **Never colour a metric by which way it moved.** The finance-UI reflex is green-up / red-down,
   and it is *actively wrong* here: a hypothesis that predicted a fall is **succeeding** when the
   line goes down. Movement is carried by the direction glyph and the expected-versus-realised
   pairing, never by colour. This follows straight from D3 and it is the easiest single way to wreck
   the trust language.
3. **Numbers are the interface.** `font-variant-numeric: tabular-nums` everywhere a figure appears,
   so a column of scores aligns and a changing digit never reflows its row.
4. **Density over air.** Day 30 is twenty rows plus a rail. MUI's defaults are too generous — the
   board is a table, not a card gallery. Target a 40px board row.
5. **Dark-first, following the OS.** This is a proven necessity, not a taste: the Orange rail reads
   `prefers-color-scheme` and cannot be told otherwise (`examples/web/src/EmbedSession.tsx:77-80`).
   Wolf follows the same rule so the two agree by construction.

### The palette

Neutral ground, one accent, two semantic colours — and metric movement uses none of them.

| Role | Light | Dark | Used for — and **only** for |
| --- | --- | --- | --- |
| ground / paper | `#fbfbfc` / `#ffffff` | `#0e1116` / `#161b22` | everything that is not below |
| text primary / secondary | `#12161c` / `#5a6472` | `#e6edf3` / `#8b949e` | prose and figures |
| **accent** | `#1f6feb` | `#58a6ff` | interactive affordances **and `tripped` conditions** |
| **warning** | `#9a6700` | `#d29922` | `indeterminate`, and severity `degraded` |
| **error** | `#cf222e` | `#f85149` | severity **`attacked`** — **nowhere else in the entire UI** |
| positive | `#1a7f37` | `#3fb950` | a `confirmed` verdict only |
| provenance ground | `rgba(0,0,0,0.025)` | `rgba(255,255,255,0.035)` | the `model` channel — **non-semantic by construction** |
| provenance rule | `rgba(0,0,0,0.12)` | `rgba(255,255,255,0.14)` | the 2px left rule on model-authored content |

🔴 **`error` red means exactly one thing in this product: something wrote what it had no right to
write.** That is why `tripped` is the **accent** and not red. A tripped condition is the system
working correctly and consequentially; an attack is the system being lied to. Sharing a colour
between them would blunt both.

`holding` is **neutral, low-emphasis** — deliberately not green. A hypothesis holding on every
condition can still be a bad thesis, and colouring it green quietly editorialises. It also keeps
`indeterminate` (warning) unmistakably distinct from `holding` (neutral), which is W14's and W23's
shared criterion.

### Condition and severity glyphs

**Never colour-alone.** Every state carries a glyph *and* a colour, and every severity additionally
carries a sentence. This is an accessibility floor, and it is also what lets these read in a
screenshot pasted into a chat.

| State | Glyph | Colour | Extra |
| --- | --- | --- | --- |
| `holding` | `●` | neutral | — |
| `tripped` | `◉` | accent | — |
| `indeterminate` | `△` | warning | a **dashed** rule on the row, so it survives colour-blindness |
| severity `degraded` | `△` | warning | **mandatory** cause sentence |
| severity `attacked` | `◉` | error | full-width `Alert`, names writer and memory id |

### Typography

- **UI and prose:** the system stack — `-apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial,
  sans-serif`. No web font: a font request is a remote fetch, and this product argues about remote
  fetches for a living.
- **Figures, ids, slugs, metric names, hashes, timestamps:** `ui-monospace, "SF Mono",
  "Cascadia Mono", Menlo, Consolas, monospace`, with `tabular-nums`. A metric slug is an identifier
  and should look like one.
- Base 14px, board rows 13px. MUI `typography` overrides in one place: `web/src/theme.ts`.

### Density

Override `MuiTableCell` padding to `6px 12px`, `MuiChip` to `size="small"` by default, and set
`spacing: 6` so MUI's `1` unit is 6px rather than 8px. Everything else is stock MUI 6 — **we are not
writing a design system**, we are constraining one.

---

## 3. Information architecture

The route table is W13's, unchanged, plus W24's:

```
/                       Board — the attention queue          (D2)
/new                    New hypothesis (title only) → interview
/hypotheses/:id         Detail — two columns                 (D4)
/hypotheses/:id/golive  Go Live review                       (W24)
/archive                Archive — terminal + retired, with restated_from lineage
```

Terminal states (`confirmed`, `invalidated`, `archived`) leave the board and live on `/archive`.
`StatusChip` still renders all six states, because a chip appears in both places — W13's six-state
table test is unaffected.

---

## 4. The board (D2, D5)

```
┌─ Agent Wolf ─────────────────────────────────── kai@badcode ▾ ─┐
│                                                                │
│  NEEDS A HUMAN  (2)                                            │
│  ● Petrodollar / drone parts        kai      CHALLENGED        │
│    C2 tripped 3 days ago · Brent held above $78 through July   │
│  ◉ Copper supply squeeze            jack     TAMPER            │
│    forged row — researcher-9c1b wrote memory mem_7f3a          │
│                                                                │
│  WATCH  (3)                                                    │
│  △ Yen carry unwind                 jack     −0.40   2 stale   │
│  △ Uranium restart                  kai      −0.10   1 indet.  │
│  △ Baltic dry recovery              kai      +0.05   no report │
│                                                                │
│  IN INTERVIEW  (1)                                             │
│  ○ Lithium oversupply               kai      draft · 2d        │
│                                                                │
│  HOLDING  (15)                                            ▸    │
│                                                                │
│                                            [ + New hypothesis ]│
└────────────────────────────────────────────────────────────────┘
```

### Tier membership — enumerated, computed server-side

| Tier | Rule | Sort within tier |
| --- | --- | --- |
| **NEEDS A HUMAN** | `status === "challenged"` **or** `tamper.length > 0` **or** an `attention_request` names this hypothesis | `updated_at_ms` descending |
| **WATCH** | not tier 1, `status === "live"`, and (`attention > 0` **or** `stale > 0` **or** `headline === null`) | `support_score` **ascending** — worst first |
| **IN INTERVIEW** | `status === "draft"` | `updated_at_ms` descending |
| **HOLDING** | everything else | `support_score` descending |

`HOLDING` is collapsed by default and shows its count. **An empty NEEDS A HUMAN section is itself
the signal** — render the heading with a count of zero rather than hiding it, so "nothing needs you"
is a thing you can see rather than an absence you have to infer.

D5 fixes the wording: the heading is **NEEDS A HUMAN**, not "needs you", because any allowlisted
person may act on any hypothesis. `owner` renders as a byline on every row.

### What the API must add — and how little it costs

The board's one-request fast path survives intact. W13's criterion *"a test with twelve hypotheses
asserts exactly one fetch"* is **unaffected**; all of this is computed server-side.

1. 🟢 **`attention` is already computed daily and then thrown away.** W10's poller derives it when
   a condition has been `indeterminate` for three consecutive evaluations and appends
   ` attention=<n>` to line 1 of the evaluation memory
   (`api/src/hypothesis/store.ts:583`). `parseEvaluationSummaryLine` tokenises the whole line into
   `found`, requires five keys, and returns only those five — `attention` is dropped on the floor
   (`store.ts:466-483`). **Cost: one optional field on `EvaluationSummaryLine` and one
   `found.get("attention")`.**

2. 🟡 **`stale=<n>` must be added to the summary line, by the same mechanism.** Staleness lives in
   `EvaluationResult.metrics[].stale`, inside the JSON body — and the board reads only the 500-byte
   snippet, so it cannot see it. Putting it on line 1 is the pattern W10 already established, and it
   carries the same justification the `attention` token does: *the memory is written only when line 1
   changes*, so a signal not on line 1 would have its own write suppressed. Emit the token only when
   `n > 0`; the parser already ignores unrecognised tokens, so this is backward-compatible with
   every evaluation memory written before it.

3. 🟡 **`attention_requests` must reach the board.** It is currently detail-only (W8,
   `GET /agent/attention-requests`). It is **one project-wide request**, not one per hypothesis, so
   it does not break W22's *"exactly three `latest_per` requests regardless of hypothesis count"* —
   state the count as "three `latest_per` requests plus one project-wide attention read".

4. ⬜ **Report drift is deliberately NOT a board signal.** Drift compares a template's
   `structureHash` against a report's slot ids, and both need full-content reads. It stays on the
   detail page. `headline === null` on a `live` hypothesis is the cheap board-level proxy for "the
   report layer is not working" and costs nothing — W22 already computes `headline`.

---

## 5. The detail page (D4)

```
┌──────────────────────────────────────────────┬──────────────────────┐
│  ← Board    Petrodollar / drone parts   kai  │  Conversation    ⟨⟩  │
│                                              │  ────────────────────│
│  ◉ CHALLENGED — condition C2 tripped         │  ┌────────────────┐  │
│    3 days ago · horizon 180d, 47 elapsed     │  │                │  │
│    [ Confirm ]  [ Invalidate ]               │  │  Orange embed  │  │
│    ── rationale required ──                  │  │  iframe        │  │
│                                              │  │                │  │
│  THE CASE                                    │  │  position:     │  │
│  C2  brent_crude  pct_change_from_start      │  │   sticky       │  │
│      −12.4%  <  −10%  ·  2026-06-01→08-21    │  │  height:100vh  │  │
│      58 observations                         │  │                │  │
│  ░ 3 research notes · researcher-4f2a  ░     │  │  NO SIZING     │  │
│  ░ the agent's untrusted evidence,     ░     │  │  PROBLEM       │  │
│  ░ not a recommendation                ░     │  │                │  │
│                                              │  │                │  │
│  ░ REPORT  · researcher-4f2a · 6h ago  ░     │  │                │  │
│  ░ △ 2 slots removed by the sanitiser  ░     │  │                │  │
│  ░ ┌────────────────────────────────┐  ░     │  │                │  │
│  ░ │ sandboxed frame, fixed height  │  ░     │  │                │  │
│  ░ │ internal scroll      [expand]  │  ░     │  │                │  │
│  ░ └────────────────────────────────┘  ░     │  │                │  │
│                                              │  │                │  │
│  SCOREBOARD   +0.10  summary only —          │  │                │  │
│               it decides nothing             │  │                │  │
│                                              │  │                │  │
│  CONDITIONS                                  │  │                │  │
│  C1 ● holding      C2 ◉ tripped              │  │                │  │
│  C3 △ indeterminate — insufficient_coverage  │  │                │  │
│                                              │  │                │  │
│  CHARTS   brent_crude ▁▂▃▅▃▂▁░░░ △ stale     │  │                │  │
│           no update since 12 Aug             │  │                │  │
│                                              │  │                │  │
│  TIMELINE                          ↓ scroll  │  └────────────────┘  │
└──────────────────────────────────────────────┴──────────────────────┘
   left: scrolls normally, ~1fr            right: 400px, sticky, 100vh
```

### The rail

- Width `400px` (`clamp(340px, 28vw, 460px)`), `position: sticky; top: 0; height: 100vh`,
  collapsible to a thin edge with a restore control.
- `OrangeChatFrame` fills it at `height: 100%`. **This is why D4 solves the sizing problem for the
  chat**: a rail's height is the viewport's, known without measuring anything.
- Below the `md` breakpoint the rail becomes a tab above the left column's content. It never becomes
  a fixed-height box in the middle of a scrolling document.
- **The rail is the `hyp-<id>` interview session and stays available after go-live.** The session
  persists; the daily researcher runs in its own fresh containers and writes notes, which appear in
  the timeline, not here.
- 🔴 **Theme agreement is by construction, not by coincidence.** The embed page picks Orange's own
  `darkTheme`/`lightTheme` from `prefers-color-scheme` (`examples/web/src/EmbedSession.tsx:77-80`),
  it cannot be told Wolf's theme, and cross-origin CSS cannot reach it. Wolf must therefore define
  its own light/dark pair and follow `prefers-color-scheme` **by the same rule**, so the rail and
  the page agree. A Wolf that is light-only will visibly clash with the rail for every user whose OS
  is dark.

### The report panel's height — the rule that does not exist anywhere today

- Default `height: clamp(480px, 70vh, 900px)`, `overflow: auto` inside the frame.
- An **expand** control opens a full-viewport dialog rendering **the same frame component**, with
  the same CSP and the same sandbox — the rule W24 already states for the go-live review, applied
  here for the same reason.
- 🔴 **No `postMessage`-driven resize.** An iframe with `sandbox="allow-scripts"` and no
  `allow-same-origin` can still `postMessage` to its parent. Accepting a height from it would let
  model-authored content set Wolf's layout, and a report asking for `40000px` pushes the verdict
  buttons off the screen. **Layout is not negotiable by untrusted content.** This is a new
  acceptance criterion and it should be asserted by test.

### The three moments the product exists for

**A condition just tripped.** The verdict band and the case lead the column, above the report:
the tripped condition's row in full (metric, statistic, value, threshold, window, observation
count), then the three most recent research notes on the `model` ground under a heading naming them
as the agent's untrusted evidence. Both verdict buttons, rationale required, submit disabled until
non-whitespace text is entered.

**The data went stale.** The chart shows the last known points, then a **hatched trailing region**
to today and a caption reading exactly `never fetched` or `no update since <date>`. Never a flat
line to today; never a silent gap. Any condition depending on that metric shows `indeterminate` with
`stale_series` as its reason, in the `degraded` treatment — visibly not `holding`.

**The news is bad but nothing has tripped.** This is the case the old board hid. The hypothesis sits
at the top of WATCH with its score ascending-sorted to the worst position, and the detail page shows
`realised_change_pct` beside the spec's expected `direction` on every metric, so *"expected down,
moved up"* is readable at a glance.

### One source of truth for staleness — a defect this design surfaces

W14's criterion defines a stale metric client-side as
`Date.now() - lastPoint.tMs > staleness_days * 86_400_000`. But `EvaluationResult.metrics[]` already
carries `stale: boolean` and `stale_reason: Reason | null`, computed by W4 — Wolf's own evaluator.
Two definitions in two places will disagree the first time the series route returns points newer
than the last evaluation.

**Resolution:** `metrics[].stale` + `stale_reason` from the evaluation is the **authority** for the
condition table and the scoreboard. The client-side rule survives for exactly one job — deciding
whether `MetricChart` draws its hatched region — and the series route's `state: "never_fetched"` is
what distinguishes "never written" from "the last tick failed". Say which is which in the ticket.

---

## 6. What Wolf imports from `@agentkit/chat-ui` (D1)

| Import | Tier | Replaces |
| --- | --- | --- |
| `ArtifactInfo`, `AgentSSEEvent`, `TodoItem` types | 1 | Wolf hand-copying the wire format |
| `artifactTree`, `artifactFilters` | 1 | grouping/filtering logic Wolf would rewrite |
| `ArtifactPanel` / `ArtifactGrid` | 2 | **the artifact list — the surface that prompted this whole review** |
| `AgentMarkdown` | 2 | hand-rolled markdown for research notes and report headlines |
| `DiffBlock` | 2 | showing a proposed spec amendment against the locked spec |

🟢 **`AgentMarkdown` is safe for untrusted prose.** It imports no `rehype-raw`, so react-markdown
escapes raw HTML by default. Research notes are model-authored and this matters — **assert it by
test in Wolf**, because it is a property of Orange's dependency list that Wolf is now relying on.

🔴 **Wolf's Orange client has no artifact support at all today** — no method, no route, no type.
The artifact surface is genuinely unbuilt: it needs client methods, a Wolf route, and then the
shared panel. That is a new ticket, not a wiring change.

Everything Wolf imports renders under **Wolf's** `ThemeProvider`, which is the whole point of D1 and
the thing the iframe could never do.

---

## 6b. The derived CSP, and R45's resolution

### The derived CSP — how R116's two accepted gaps close

The pinned CSP allows `script-src https:` and `img-src https:` — **any HTTPS host on earth**. Two
gaps followed, both recorded as accepted: (a) any CDN may serve script into the frame, and (b) an
approved template can exfiltrate through an image URL, while the human who approved it **never saw
that host**, because W16 reports only `script[src]`, `link[rel=stylesheet][href]` and CSS `@import`.

Both close by deriving the allowlist from the approved template. **Most of the mechanism already
exists**, which is why this is worth doing now rather than later:

- W16's validator **already walks every URL channel** — `src`, `href`, `srcset`, `poster`, `action`,
  `formaction`, `xlink:href`, `background`, `ping`, plus CSS `url()` and `@import` — and requires
  every one to be `https:` (`api/src/report/template.ts:596,895`). The inventory is computed and
  then discarded.
- `ParsedTemplate` gains **`remoteOrigins: string[]`**: the deduplicated, sorted set of
  `new URL(u).origin` for every URL that loop already visits. One push in an existing loop.
- **W24 lists `remoteOrigins` above the preview**, alongside `scriptSrcs`. The human approving remote
  code now sees every host it will contact, not only the executable ones. That alone closes (b)'s
  visibility half.
- **W21 substitutes them into the CSP.**

The skeleton, with the substitution points marked:

```
sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' <ORIGINS>; style-src 'unsafe-inline' <ORIGINS>; img-src <ORIGINS> data:; font-src <ORIGINS> data:; connect-src 'none'; form-action 'none'; frame-ancestors 'self'; frame-src 'none'; child-src 'none'; object-src 'none'; base-uri 'none'; manifest-src 'none'; media-src 'none'; worker-src 'none'
```

`<ORIGINS>` is the sorted, space-joined origin list. A template referencing nothing remote yields
`script-src 'unsafe-inline'` with no host at all — **strictly tighter than today**, and the common
case for a chart drawn from the injected series.

**Why it is safe to freeze:** `structureHash` already freezes the template, so the origin set is
frozen with it. A new origin means a different template, which means a new human review. That is the
property `https:` never had.

**W19's criterion changes** from *"equals this exact string as a string literal"* to *"equals this
exact skeleton with the origin list substituted at the four named positions"* — still asserted
exactly, now with a table test over: no origins; one origin; several; and the same origin arriving
from two different channels collapsing to one entry.

**What this deliberately does NOT close, recorded honestly:**

- **`'unsafe-inline'` stays.** The template's chart code is inline. Removing it needs hashes or a
  nonce, and a nonce must vary per response while `structureHash` freezes the body. Out of scope,
  and now the *only* remaining breadth in the policy.
- **A permitted origin can still receive an exfiltrating request.** The bound moves from *"every
  HTTPS host"* to *"the hosts a human approved for this template"*. That is a real bound, not a
  guarantee.

### R45 — resolved as a wording fix, not a redesign

The report-layer amendment says go-live refuses a hypothesis with no `report-template`, and W21 is
the only writer of one. That read as a cycle. It is not:

- 🟢 **Go-live does not require a template in the code today.** W9 shipped without it; the
  requirement exists only in the amendment's prose.
- 🟢 **The dependency graph has no cycle.** W24 → W23 → W22 → W21, so by the time the UI gate exists,
  its writer already does.

**The requirement moves from W9 to W22**, and the gate goes where the plan already puts its other
half. W13's criterion states that the Go Live gate has *exactly one server-side source* —
`spec_validation` on the detail payload — and that W9's `422` is *"the backstop for a race, not the
UI's source of truth."* The pinned report block already carries `has_template` as its first field:

```
GET /api/hypotheses/:id
  spec_validation: { valid, errors[] }     ← W8,  built
  report:          { has_template, … }     ← W22, pinned already

  W24's button is enabled iff  spec_validation.valid && report.has_template
  W22 adds the server-side 422 backstop — it already depends on W21 and
  already owns that block. W9 is never reopened.
```

---

## 7. Reconciliation with the existing tickets

### W11 — Embed tokens and the series proxy
**Unaffected.** Every criterion stands. Safe to cut before this document is approved.

### W13 — Board, new hypothesis, chat frame

| Criterion | Status |
| --- | --- |
| Router: React Router 7, one route table | ✅ survives |
| `@testing-library/react` 16.x + jest-dom | ✅ survives |
| Two `VITE_*` build args, read only in `env.ts` | ✅ survives |
| `OrangeChatFrame` src composed from the variable | ✅ survives |
| `expires_at_sec` T-120s refresh, fake-timer test | ✅ survives |
| Token in component state only, never storage | ✅ survives |
| Board renders from a **single** fetch | ✅ survives — tiers are server-computed |
| All six states render a distinct labelled chip | ✅ survives |
| `TamperWarning` = full-width `Alert severity="error"` | ✅ survives — this **is** `Severity level="attacked"` |
| Go Live gate from `spec_validation`, `web/` never imports the validator | ✅ survives |
| Archive shows `restated_from` lineage | ✅ survives |
| `NewHypothesis` posts `{title}`, surfaces errors verbatim | ✅ survives |
| Each card renders title, chip, score, condition summary, headline | 🟡 **extended** — adds tier, owner byline, `attention`/`stale` counts |
| **Files line** | 🔴 **contradicted** — gains `@agentkit/chat-ui`, `vendor/`, `web/src/theme.ts`, `web/src/components/trust/{Provenance,Severity}.tsx` |
| **`OrangeChatFrame` as a page component** | 🔴 **contradicted** — it is now a layout rail with a `height:100%` contract and a collapse state |

### W14 — Detail, scoreboard, conditions and verdict

| Criterion | Status |
| --- | --- |
| `ConditionTable` renders all pinned fields | ✅ survives |
| `indeterminate` visually distinct from `holding`; the word is never "unknown" | ✅ survives — now `Severity level="degraded"` |
| `indeterminate` shows W4's `reason` verbatim, unknown reasons rendered not dropped | ✅ survives |
| Status/staleness never from Orange's delivery status | ✅ survives |
| Expected `direction` beside `realised_change_pct` | ✅ survives |
| `support_score` labelled a summary that decides nothing | ✅ survives |
| `challenged`-only block: reason, tripped rows, three research notes | ✅ survives — notes now on the `model` ground via `AgentMarkdown` |
| Verdict buttons `challenged`-only, rationale required, whitespace refused | ✅ survives |
| Amendment posts the exact pinned body | ✅ survives |
| Proposals render as proposals, labelled untrusted | ✅ survives — now `Provenance kind="model"` + the word **Proposal** |
| `Timeline` newest-first, every row labelled trusted/untrusted | 🟡 **refined** — labelling becomes the provenance channel |
| `MetricChart` plots `Point[]`, UTC axis, no interpolation across gaps | ✅ survives |
| **"laid out so W23 composes above the condition table without restructuring it"** | 🔴 **CONTRADICTED** — W14 now ships the two-column shell; W23 composes into the left column |
| **client-side staleness rule** | 🔴 **contradicted** — `metrics[].stale` is the authority; the client rule survives only for the chart's hatching |

### W23 — VerdictBand and ReportPanel

| Criterion | Status |
| --- | --- |
| 🔴 `sandbox="allow-scripts"`, no `allow-same-origin`, asserted on the attribute string | ✅ **survives unchanged — still the highest-value test in the feature** |
| No `dangerouslySetInnerHTML` anywhere under `web/src` | ✅ survives |
| Testing library is a no-op if W13 added it | ✅ survives |
| No report → explicit empty state naming why | ✅ survives |
| `stripped_count > 0` and drift render visible notices | 🟡 **refined** — both are `Severity level="degraded"` with a mandatory cause sentence |
| `VerdictBand`: status, score, tripped/holding/indeterminate counts | ✅ survives |
| Score summarises, does not decide | ✅ survives |
| **new** | ➕ the panel's height rule, and **no `postMessage` resize**, asserted by test |
| **new** | ➕ the panel sits on the `model` ground with a provenance stamp |

### W24 — Go Live review screen
**All criteria survive.** The design reinforces one of them: the expand dialog on the detail page
uses the same real frame component W24 already demands, for the same reason.

---

## 8. New tickets this design requires

| Id | Repo | Scope | Model | Depends on |
| --- | --- | --- | --- | --- |
| **O12** | agent-orange | Make `@agentkit/chat-ui` publishable, **to the shape the probe below proved**: drop `private`, `noEmit: false`, drop `allowImportingTsExtensions`, exclude tests and `test-setup` from the emit, move the five runtime deps (`@mui/icons-material`, `react-markdown`, `remark-gfm`, `prism-react-renderer`, `@untitledui/file-icons`) out of `devDependencies`, **add subpath exports (`.`, `./pure`, `./components`)**, version it, `prepack`, and a CI step that fails if `dist` stops emitting. Update `examples/web` to consume `dist` and shrink its dedupe list. **Does NOT change Orange's source imports** — proved unnecessary. | opus | — |
| **W27** | agent-wolf | The attention model: `stale=<n>` on the evaluation summary line, `attention` surfaced by the parser, `attention_requests` on the board payload, tiers computed server-side. | opus | W22 |
| **W28** | agent-wolf | The design system: `web/src/theme.ts` implementing § 2b (palette, glyphs, typography, density, `prefers-color-scheme`), plus `Provenance` and `Severity` in `web/src/components/trust/`. Criteria must include the two rules § 2b makes load-bearing — **provenance uses no semantic palette colour**, and **`error` red appears nowhere but severity `attacked`** — each asserted by test. 🔴 **And `web/vite.config.ts` MUST set `test.server.deps.inline: [/@mui/, /@agentkit/]`** — see the probe finding below; without it every test importing a shared component dies pointing at MUI. | sonnet | O12 |
| **W29** | agent-wolf | Wolf's artifact surface: Orange client methods, `GET /api/hypotheses/:id/artifacts`, and the shared `ArtifactPanel` rendered under Wolf's theme. | sonnet | O12, W28 |

### The O12 consumption probe — run 2026-08-24, before these tickets were written

`web/` was built with the O12 config, packed (364KB), installed into a throwaway React 18.3.1 /
MUI 6 / vitest app sharing no code with Orange, and exercised. **This is the R120 discipline: a
package that typechecks and emits is not a package that works.** Results:

| Claim | Result |
| --- | --- |
| `ArtifactPanel` renders from `dist` in a foreign app | ✅ |
| One React copy — peer deps resolve under plain `npm install`, **no consumer dedupe list** | ✅ |
| **The host's theme reaches the shared component's DOM** — a `live` status dot computed to `rgb(0, 229, 160)`, the *consumer's* `success.main`, not Orange's | ✅ **this is D1's proof, and the thing an iframe can never do** |
| `AgentMarkdown` escapes raw HTML — `<img onerror>` and `<script>` both stripped, prose intact | ✅ |
| Tier-1 pure logic imports and runs with no React | ✅ |
| Orange's 11 `@mui/material/styles` imports need changing | ❌ **no** — isolated by rebuilding unpatched: still 4/4 green |

🔴 **The blocker, and it is not ours.** Without `server: { deps: { inline: [/@mui/, /@agentkit/] } }`
in the consumer's vitest config, every import fails with
`Directory import '.../@mui/material/utils' is not supported resolving ES modules imported from
.../@mui/icons-material/esm/utils/createSvgIcon.js`. That points at **MUI's own ESM build**, and an
executor would reasonably conclude the package is broken. It is a consumer-config requirement and it
belongs in W28's criteria verbatim.

🟡 **Import cost was 37–45s for one test file**, essentially all resolution, because the barrel pulls
the whole library including `AgentChat`. Without subpath exports the tier line is **notional** — the
module graph contradicts it. Hence `./pure` and `./components` in O12.

**Revised order.** The UI chain is `O12 → W28 → W13 → W14 → W23 → W24`; the report chain
`W30 → W19 → W21 → W22` runs beside it, and **W27 follows W22** (it restates W22's request-count
criterion) while **W29 follows W14** (it renders into the detail page). O12 depends on nothing and
is the natural place to start.

*(An earlier draft of this line read `… → W14 → W27 → W29 → W21 → W22 → …`, which put W27 **before**
the ticket it depends on. Caught by the revision-5 adversarial pass, along with five defects in the
plan-side fold — the eighth instance of the standing lesson, and the reason the pass exists.)*
W28 must land before W13 so the four UI tickets consume one trust vocabulary rather than each
inventing one — which is the failure this whole document exists to prevent.

---

## 9. Open items for the owner

1. ~~**Distribution.**~~ **Settled 2026-08-24:** tarball of `dist`, lockstep both repos, no semver
   ceremony — "this isn't an open release." Revisit only when a third application appears.
2. ~~**Visual direction.**~~ **Settled 2026-08-24:** ours, and now normative in § 2b.
3. ~~**R45.**~~ **Resolved** in § 6b — a wording fix; the gate moves to W22 and W9 is never reopened.
4. ~~**R116's residual risk.**~~ **Addressed** in § 6b by deriving the CSP origin list from the
   approved template. `'unsafe-inline'` remains, and is now the only breadth left in the policy.
5. **Does the rail belong on the board too?** It is not proposed here. A board-level conversation
   would need a session that is not `hyp-<id>`, and no ticket creates one.
6. 🔴 **A stale duplicate that will mislead an executor.** `design/2026-08-21-agent-wolf-report-layer.md:370,426`
   still carries pre-R116 copies of § "The slot sanitiser profile, pinned as an ALLOW list" and
   § "The CSP header, byte-for-byte", and the companion's CSP is a **different string** — fewer
   directives, and `script-src https: 'unsafe-inline'` instead of `script-src 'unsafe-inline' https:`.
   W19's criterion says "equals the § 'The CSP header, byte-for-byte' value exactly" without naming a
   document. **Mark the companion's copies superseded before W19 is cut.**
