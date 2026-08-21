# Agent Wolf report layer — Design & Implementation Plan

> **EXECUTION RULES (for agents):** Work ONE ticket at a time, in order unless dependencies say
> otherwise. Only the orchestrator changes ticket Status; workers may only append to Notes and the
> Discovered Issues Log. A ticket's checkbox is checked only after its Validation commands have
> been re-run by the orchestrator and pass. Do not expand scope; log surprises in the Discovered
> Issues Log instead.

Status: proposed
Relates: `design/2026-08-20-agent-wolf.md` (the main plan, revision 3),
`design/2026-08-21-agent-wolf-executability-audit.md` (26 blocking defects awaiting revision 4)

> **How this document lands.** It is written standalone so the design can be reviewed on its own
> merits. It is **not** dispatched standalone: revision 4 of the main plan folds these tickets into
> the `W` series and applies the § "Amendments to existing tickets" edits below, in the same pass
> that absorbs the audit's 26 defects. One source of truth, one ticket list, one dependency graph.

---

## Context

The main plan gives every hypothesis the same presentation: a scoreboard of typed conditions and a
line chart per metric. That is correct for *comparing* hypotheses and wrong for *understanding*
one. A thesis about drone-parts suppliers and a thesis about rainfall need the same verdict
arithmetic and completely different pictures.

The owner's framing: *"each hypothesis shouldn't have to report in the same way, but there should
be a way of giving a quick summary of how that hypothesis is doing without having to open and
resume the conversation"*, and *"if we just say that the ultimate interface for reports is a string
that will be interpreted as HTML, that gives total freedom to each hypothesis to decide what its
presentation layer will be."*

Those two requirements pull against each other. Freedom of presentation destroys comparability; a
uniform summary destroys freedom. **They are resolved by layering, not by choosing.**

They also pull against the plan's central invariant. § "The trust model" exists so that the model
under test cannot assert its own verdict — a researcher session that read a poisoned web page must
not be able to write `status=confirmed`. A block of model-authored HTML rendered in the operator's
browser is, structurally, that same assertion wearing different clothes.

So the design keeps the verdict where it already is — deterministic, server-computed, trusted — and
gives the model everything below it.

### The threat model, stated once

The adversary is **not** a Wolf user. Wolf is for a small trusted team. The adversary is whoever
wrote the web page the researcher worker read this morning; prompt injection through fetched
content is the standard path by which that worker's output gets steered.

The owner's decision, taken deliberately on 2026-08-21 with the exposure stated: the panel may load
scripts from **any HTTPS host**, because bundling a charting library would force an opinion about
which library each hypothesis may use, and that is the freedom the feature exists to provide.

The residual exposure that decision accepts, precisely:

- **In scope for an attacker:** the hypothesis's own series data and report text may be sent to a
  host of the model's choosing. `connect-src 'none'` raises the bar but does **not** close this —
  a `<script src="https://…?d=…">` query string is sufficient. Do not let a later reader believe
  otherwise.
- **Out of reach:** the operator's Wolf session, their Orange session, their Google identity, and
  Wolf's own API key. This holds because the document is forced into an **opaque origin** and
  because **no credential is ever placed inside the frame** — the series data is injected
  server-side.
- **Out of reach only because we check for it:** every *other* hypothesis. Labels on a
  `memory_create` call are chosen entirely by the caller, so a researcher session for `hyp-A` can
  append `kind=report, name=hyp-B` and own hypothesis B's headline and panel. Nothing structural
  prevents this. **R8 rejects any `kind=report` whose provenance does not name that hypothesis's
  own worker or session**, and surfaces it as tamper. Without that check the isolation claim is
  false; do not remove it.

⚠️ **The opaque origin is a property of the RESPONSE, not of the caller.** An earlier draft put
`sandbox` only on the parent's `<iframe>` element. That is not enough: `GET
/api/hypotheses/:id/report/frame` is an ordinary same-origin route, so anyone opening that URL
directly — or induced to — would load model-authored `'unsafe-inline'` script on Wolf's own origin
with `document.cookie` readable. The CSP **`sandbox allow-scripts` directive** is therefore
mandatory in the response header, where it applies however the document is loaded. The iframe
attribute stays as defence in depth.

The one structural mitigation that costs nothing and is therefore mandatory: **all JavaScript lives
in the locked template, reviewed by a human at go-live.** A poisoned source can change the day's
words. It cannot change the day's code.

---

## Architecture

### The two layers

```
┌─ HYPOTHESIS DETAIL ──────────────────────────────────────────────┐
│                                                                  │
│  ┌ VERDICT BAND ──────────────────────────────── TRUSTED ──────┐ │
│  │  ● live  score −0.42  1 tripped · 3 holding · 0 indeterminate│ │
│  │  source: kind=evaluation memory · server-computed           │ │
│  │  comparable and sortable across every hypothesis            │ │
│  └─────────────────────────────────────────────────────────────┘ │
│                                                                  │
│  ┌ EXPOSITION PANEL ──────────────────────────── UNTRUSTED ────┐ │
│  │  <iframe sandbox="allow-scripts"                            │ │
│  │          src="/api/hypotheses/:id/report/frame">            │ │
│  │    · opaque origin — no allow-same-origin                   │ │
│  │    · no cookie, no token, no credential inside              │ │
│  │    · locked template + today's slots + injected series      │ │
│  └─────────────────────────────────────────────────────────────┘ │
│                                                                  │
│  ┌ CONDITION TABLE (W14, unchanged) ───────────── TRUSTED ─────┐ │
│  └─────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
```

The verdict band is the answer to *"is this becoming more true or less true"*. It is a number, so
it sorts. The exposition panel is the answer to *"why"*, and it is whatever this hypothesis needs
it to be.

### The locking mechanism

A report is **two memories**, not one.

`kind=report-template` — written once at go-live, by Wolf's server, with empty provenance, after a
human has looked at it. It is an HTML **fragment**, not a full document: `composeFrame` owns the
`<!doctype>`, `<html>`, `<head>` and `<body>` skeleton, which is what lets it place the series
injection at a pinned position without rewriting the template. A template containing `<html>`,
`<head>`, `<body>` or a doctype is a validation error.

```html
<script src="https://cdn.jsdelivr.net/npm/uplot@1.6.31/dist/uPlot.iife.min.js"></script>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/uplot@1.6.31/dist/uPlot.min.css">
<h1>Drone suppliers vs the petrodollar</h1>

<div data-wolf-slot="headline"></div>

<div id="basket"></div>
<div data-wolf-fallback>Chart library unavailable — showing values only.</div>
<!-- the template's own script removes [data-wolf-fallback] once the chart renders;
     it is visible by default so a CDN failure inside an opaque frame is not silent -->
<script>
  const s = window.__WOLF_SERIES__["drone-suppliers-basket"];
  new uPlot({width:720, height:280}, [s.t, s.v], document.getElementById("basket"));
</script>

<div data-wolf-slot="commentary"></div>
```

`kind=report-candidate` — written by the **interview session** during the interview, untrusted,
labelled `kind=report-candidate, name=<id>`. This is the transport that carries a proposed template
out of the container to the Go Live screen; without it nothing produces the thing the human
reviews. The Go Live screen reads the newest one, renders it, and on acceptance posts its HTML to
`POST /api/hypotheses/:id/report-template`, which is where it becomes trusted.

`kind=report` — written by the researcher worker at the end of every tick, carrying non-empty
provenance and therefore untrusted. Its content is the headline on line 1, then a JSON object
mapping slot id to markup:

```
Basket +14% since live; petro-share still flat, 3 of 4 conditions holding.
{"headline":"<strong>+14%</strong> since live","commentary":"<p>AVAV led the move…</p>"}
```

**The rule that makes "locked" mean something: scripts, stylesheets and `src` attributes exist only
in the template. Slots are sanitised to inert markup.** The worker cannot add a chart, change a
chart, or introduce a script — only fill regions the human already approved. Adding a panel is a
`kind=report-amendment` proposal the human accepts, which writes a new template.

This mirrors the split the plan already uses for prompts (locked preamble / mutable method body,
main plan § W12), so there is one mental model in the product rather than two.

### Rejected alternatives, and why

- **A Wolf chart vocabulary** (`<wolf-chart metric=… kind=line>` rendered server-side). Stronger
  security and forced convergence, but the owner chose CDN libraries so that no hypothesis is
  limited to charts Wolf anticipated. Recorded because it remains the natural upgrade if the
  open-script decision is ever revisited.
- **Reports as structured JSON panels.** Machine-checkable and comparable, but it re-imposes
  exactly the uniformity this feature exists to remove.
- **Sanitising on write.** Rejected: a sanitiser bug would then corrupt stored evidence
  irreversibly, and the sanitiser will change. Sanitise on read, keep the original bytes.
- **A single `kind=report` memory holding both structure and content.** Rejected: there is then no
  locked region, the go-live review has no ongoing meaning, and drift is undetectable.
- **Serving the panel from a second origin/service.** Genuinely stronger, but with `script-src
  https:` chosen the marginal gain is small, and it doubles the local topology this project already
  found fragile (main plan § "Local topology and networking").

### Trust vocabulary, corrected

The main plan's § "The trust model" says a memory is trusted iff its provenance is empty **and its
labels are one of the five kinds in Wolf's own vocabulary**. That numeral was already wrong before
this feature (`evaluation` was added by R29 and never counted; the audit logged it), and this
feature adds a sixth trusted kind. **Revision 4 must replace the count with the explicit set.**

The rule keeps **all three** of its clauses — a memory is trusted iff its provenance is empty
**AND** its kind is in the set below **AND** its `name` matches an existing `hyp-<id>` session.
That third clause is the one that cannot be forged from inside a container, so do not drop it
while correcting the numeral.

Trusted kinds: `hypothesis`, `hypothesis-spec`, `verdict`, `evaluation`, **`report-template`**.
Untrusted kinds: `research-note`, `spec-amendment`, **`report`**, **`report-amendment`**,
**`report-candidate`**.

---

## Data flow

```
GO-LIVE ── once, human in the loop
  interview session
      │  renders a candidate report from research-phase data
      ▼
  candidate template + first slot fill
      │
      │  human reviews it rendered, in the Go Live screen
      ▼
  POST /api/hypotheses/:id/report-template
      │  Wolf server, project API key → empty provenance
      ▼
  kind=report-template  status=locked          [TRUSTED]

DAILY TICK ── researcher worker, untrusted
  researcher
      ├── dataset_put ─────────► the numbers (versioned, CAS, shrink-guarded)
      └── memory_create ───────► kind=report                [UNTRUSTED by provenance]
                                   line 1: headline text
                                   line 2+: {slotId: html}

VIEW
  GET /api/hypotheses/:id ─────► verdict band  (from kind=evaluation)
      │
      └── <iframe sandbox="allow-scripts" src="…/report/frame">
                    │
                    ▼
            GET /api/hypotheses/:id/report/frame
                    │
                    ├── locked template          (verbatim, human-reviewed)
                    ├── slots                    (sanitised, inert)
                    ├── window.__WOLF_SERIES__   (injected, capped, downsampled)
                    └── Content-Security-Policy  (response header)
                    ✗ no Set-Cookie · no token · no credential

BOARD ── still O(1) in hypothesis count
  GET /agent/memories?selector=kind%3Dhypothesis&latest_per=name   → state, owner, title
  GET /agent/memories?selector=kind%3Devaluation&latest_per=name   → score + counts
  GET /agent/memories?selector=kind%3Dreport&latest_per=name       → line 1 = headline
```

The third read is free in the sense that matters: it is one request regardless of how many
hypotheses exist, and the headline arrives inside the 500-byte snippet the board already receives
(main plan § "Memory kinds": `GET /agent/memories` returns a snippet and no `content` field). This
is what delivers *"a quick summary without having to open and resume the conversation"*.

---

## File Structure

### agent-wolf — create

| Path | Purpose |
| --- | --- |
| `api/src/report/template.ts` | Parse a template: extract slot ids, validate document shape, enforce `https:`-only on every `src`/`href`, compute `structureHash` |
| `api/src/report/template.test.ts` | Tests for the above |
| `api/src/report/sanitise.ts` | `SLOT_PROFILE` (the DOMPurify allow list) and `validateTemplate` (checks only, never mutates). Pinned to **`isomorphic-dompurify` 2.x**, which pulls `jsdom` into `api/` |
| `api/src/report/sanitise.test.ts` | Tests for the above |
| `api/src/report/series.ts` | Select the locked spec's metrics, downsample, and build the `__WOLF_SERIES__` payload |
| `api/src/report/series.test.ts` | Tests for the above |
| `api/src/report/frame.ts` | Compose template + slots + series into one document; produce the CSP header value |
| `api/src/report/frame.test.ts` | Tests for the above |
| `api/src/report/drift.ts` | Compare a tick's slot ids against the template's; classify orphan and unfilled slots |
| `api/src/report/drift.test.ts` | Tests for the above |
| `api/src/routes/report.ts` | The three routes |
| `api/src/routes/report.test.ts` | Tests for the above |
| `web/src/components/VerdictBand.tsx` | The trusted band |
| `web/src/components/ReportPanel.tsx` | The sandboxed iframe wrapper |
| `web/src/components/ReportDrift.tsx` | Drift and strip-count indicators |
| `web/src/pages/GoLiveReview.tsx` | Renders the candidate report and the Go Live gate |
| `web/src/components/*.test.tsx` | Tests beside each |
| `prompts/report-authoring.md` | The report contract, shared by the interviewer and the researcher |

### agent-wolf — modify

| Path | Change |
| --- | --- |
| `api/src/hypothesis/store.ts` | Read `report-template` as trusted; read `report` as untrusted; third board read |
| `api/src/routes/hypotheses.ts` | Board carries `headline`; detail carries `report` metadata and drift |
| `api/src/orange/client.ts` | R1 adds `getMemoryById` and `getCurrentMemory` — W2's scope omits the full-content reads (audit A5), and the frame cannot be served from snippets |
| `api/src/config.ts` | `WOLF_REPORT_MAX_BYTES` (default 512000) and `WOLF_SERIES_MAX_POINTS` (default 5000). **Owned by R2**, which is the first ticket to consume them — R7 must not re-add them |
| `api/src/app.ts` | Mount the report router |
| `.env.example` | Document both new variables |
| `web/src/pages/HypothesisDetail.tsx` | Compose `VerdictBand` + `ReportPanel` above the condition table |
| `prompts/interviewer.md` | Must produce a candidate report; must not go live |
| `prompts/researcher-preamble.md` | Must write `kind=report` each tick, filling only declared slots |
| `installations/wolf/Dockerfile` | No change — report authoring needs no new tooling |

---

## Interfaces

### Memory kinds added

| Kind | Labels | Content | Trusted |
| --- | --- | --- | --- |
| `report-template` | `kind=report-template, name=<id>, status=locked` | Line 1 is `structureHash`; line 2+ is the template HTML | **Yes** |
| `report` | `kind=report, name=<id>` | Line 1 is the headline (plain text, ≤ 400 **characters**); line 2+ is `{slotId: html}` as JSON | No |
| `report-candidate` | `kind=report-candidate, name=<id>` | Line 1 is a one-line summary; line 2+ is the proposed template HTML | No |
| `report-amendment` | `kind=report-amendment, name=<id>, status=proposed` | Line 1 is a one-line rationale; line 2+ is the proposed template HTML | No |

All three are appended with `embed: false`. Report HTML routinely exceeds the 24KB embedding
ceiling, and O7 returns **400** for `embed: true` above it (main plan § O7). None of them is
semantically searchable, so nothing is lost.

### Orange routes consumed — no Go changes

This feature adds nothing to agent-orange, but it consumes **four** routes, not two:

| Route | Used for |
| --- | --- |
| `POST /agent/memories` (O7) | Writing `report-template` with empty provenance |
| `GET /agent/memories?selector=…&latest_per=name` | The three board reads — **snippet only** |
| `GET /agent/memories/{id}` | Full template and slot content |
| `GET /agent/memories/current?name=…` | Newest template/report by name, full content |

⚠️ **The list route cannot serve the frame.** It returns a snippet capped at 500 characters and no
`content` field (`go/agentdb/memories.go:451-452`); a template is up to 512000 bytes. The frame
route must use the by-id or `current` read.

W2's client must expose all four. Its scope currently omits the full-content reads (audit finding
A5), so this plan amends it — see § "Amendments to existing tickets".

### TypeScript signatures (exact)

```ts
// api/src/report/template.ts   — OWNED BY R2. Structure only; knows nothing about sanitiser profiles.
export interface ParsedTemplate {
  html: string;            // verbatim, exactly as stored — never rewritten
  slotIds: string[];       // order of appearance; duplicates are an error
  structureHash: string;   // sha256 of the STORED BYTES, lowercase hex, NO normalisation
  scriptSrcs: string[];    // every external script/style URL, for the go-live review screen
}
export function parseTemplate(html: string, maxBytes: number): ParsedTemplate;  // throws WolfError "invalid"

// api/src/report/sanitise.ts   — OWNED BY R3. The security boundary.
export interface SanitiseResult { html: string; strippedCount: number; }
export function sanitiseSlot(html: string): SanitiseResult;

// Validation-only. NEVER returns HTML, NEVER mutates. There is deliberately no
// `sanitiseTemplate`: the template is emitted byte-for-byte, so a mutating template
// pass would silently invalidate structureHash and change what the human approved.
export function validateTemplate(html: string, maxBytes: number):
  { ok: true; parsed: ParsedTemplate } | { ok: false; reasons: string[] };

// api/src/report/series.ts   — OWNED BY R4.
export interface DatasetSeries {          // what a dataset read yields, before shaping
  slug: string; unit: string; version: number; points: { tMs: number; v: number }[];
}
export interface SeriesPayload {
  [metricSlug: string]: { tMs: number[]; v: number[]; unit: string; version: number };
}
export function buildSeriesPayload(
  spec: HypothesisSpec, datasets: DatasetSeries[], maxPoints: number,
): SeriesPayload;

// api/src/report/frame.ts   — OWNED BY R5.
export interface FrameDocument { html: string; csp: string; strippedCount: number; }
export function composeFrame(
  template: ParsedTemplate, slots: Record<string, string>, series: SeriesPayload,
): FrameDocument;

// api/src/report/drift.ts   — OWNED BY R6.
export interface ReportDrift { orphanSlots: string[]; unfilledSlots: string[]; }
export function detectDrift(template: ParsedTemplate, slots: Record<string, string>): ReportDrift;
```

### The slot sanitiser profile, pinned as an ALLOW list

DOMPurify is an allow-list sanitiser; specifying only what it strips leaves the boundary undefined
and two executors would ship materially different security while passing the same criteria.
`SLOT_PROFILE` is exactly:

```ts
ALLOWED_TAGS: ["p","br","hr","span","div","strong","em","b","i","u","s","code","pre",
               "ul","ol","li","dl","dt","dd","blockquote",
               "h1","h2","h3","h4","h5","h6",
               "table","thead","tbody","tr","th","td","caption","small","sub","sup"],
ALLOWED_ATTR: ["class","title","colspan","rowspan"],
ALLOWED_URI_REGEXP: /^$/            // no URLs at all
```

🔴 **No `img`, no `a`, no `src`, no `href`, no `style`.** A remote `src` in a slot would be a daily,
human-unreviewed egress channel, which would falsify the claim that a poisoned source can change
the day's words but not the day's code. Every URL-bearing element belongs in the reviewed template.

### The detail route's report block, pinned

Wire JSON is snake_case throughout, and units live in the field names:

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

### HTTP routes added

```
GET  /api/hypotheses/:id/report/frame
       → 200 text/html, the composed document
         Content-Security-Policy: <see below>
         X-Content-Type-Options: nosniff
       → 404 when no template exists (the UI renders an empty state, not a blank frame)

POST /api/hypotheses/:id/report-template     { html }
       → 201 { structure_hash }              writes kind=report-template, status=locked
       → 422 { errors: [{ path, message }] } template failed validation
       → 409                                 a locked template already exists; use an amendment

POST /api/hypotheses/:id/report-amendment    { amendment_id, decision, rationale }
       → 200                                 accepting writes a NEW kind=report-template
       → 422                                 the proposed template failed validation
```

`POST /api/hypotheses` and `GET /api/hypotheses/:id` are amended, not added — see § "Amendments to
existing tickets".

### The CSP header, byte-for-byte

```
sandbox allow-scripts; default-src 'none'; script-src https: 'unsafe-inline'; style-src https: 'unsafe-inline'; img-src https: data:; font-src https: data:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'
```

Each clause and why it is there:

| Clause | Reason |
| --- | --- |
| `sandbox allow-scripts` | **The load-bearing one.** Forces an opaque origin on the response itself, so direct navigation to this URL is as confined as the iframe. Omitting `allow-same-origin` is what makes it opaque |
| `default-src 'none'` | Deny by default; every allowance below is deliberate |
| `script-src https: 'unsafe-inline'` | The owner's decision: any HTTPS host. `'unsafe-inline'` is required because the template's chart code is inline |
| `style-src https: 'unsafe-inline'` | Charting libraries ship CSS; templates style inline |
| `img-src https: data:` | Sparkline data URIs and remote logos |
| `connect-src 'none'` | Blocks `fetch`/XHR/WebSocket. Defence in depth — **not** an exfiltration guarantee |
| `form-action 'none'` | A form post is another egress channel |
| `base-uri 'none'` | Stops a `<base>` tag re-pointing every relative URL |
| `frame-ancestors 'self'` | Only Wolf may frame this document |

### The iframe element, exactly

```tsx
<iframe
  title="Hypothesis report"
  src={`/api/hypotheses/${id}/report/frame`}
  sandbox="allow-scripts"          /* NEVER add allow-same-origin */
  referrerPolicy="no-referrer"
/>
```

`allow-scripts` without `allow-same-origin` yields an opaque origin. Adding `allow-same-origin`
alongside `allow-scripts` would let the panel reach Wolf's DOM, cookies and storage, and is the
single change that converts a bounded risk into a session compromise. It is asserted by test.

---

## Out of Scope

- **A Wolf chart vocabulary or server-side chart rendering.** Explicitly rejected above.
- **A CDN caching proxy.** Considered and declined; the report contract is designed so it could be
  added later without hypotheses noticing.
- **Report versioning or history UI.** Memories are append-only, so the history exists; nothing
  renders it in this feature.
- **Editing a report by hand in the UI.** Templates arrive from the interview or an amendment.
- **PDF/email export of reports.**
- **Any change to the condition evaluator, the support score, or the verdict.** The verdict band
  renders what W4 already computes; this feature adds no arithmetic.
- **Sanitising anything on write.** Bytes are stored as received.

---

## Amendments to existing tickets

Revision 4 applies these; they are listed here so nothing is lost in the fold.

| Ticket | Amendment |
| --- | --- |
| **W3** | The spec gains **no** report section. The report is a separate memory, validated by R2/R3, not by the spec validator. Add a note saying so, because "the interview designs the report" otherwise reads as a spec change. |
| **W5** | The trusted-kind set becomes explicit and includes `report-template`; the "five kinds" numeral is removed. `report` and `report-amendment` join the untrusted set. |
| **W8** | `GET /api/hypotheses` gains `headline` from the third `latest_per` read. `GET /api/hypotheses/:id` gains `report: { has_template, structure_hash, drift, stripped_count, updated_at }`. |
| **W9** | Go-live provisioning must refuse to proceed if no `report-template` exists — the human reviewed a report, so one must be stored. |
| **W12** | `prompts/interviewer.md` must produce a candidate report; `prompts/researcher-preamble.md` must write `kind=report` each tick and may fill only declared slots. Both reference `prompts/report-authoring.md`. |
| **W13** | The board card renders `headline`. `GoLiveReview` renders the candidate report **inside the real frame** before the Go Live button enables. |
| **W14** | `HypothesisDetail` composes `VerdictBand` + `ReportPanel` above the existing condition table. The condition table is unchanged. |
| **X1** | Gains the end-to-end report leg (see R12), and `e2e/run.sh` must accept an optional spec-name filter so `./e2e/run.sh report-layer` is a defined command. |
| **W2** | Its route list becomes exhaustive and closed, and must include `GET /agent/memories/{id}` and `GET /agent/memories/current?name=` — the full-content reads the frame route depends on (audit A5). |
| **§ Pinned technology choices** | New row: HTML sanitisation → **`isomorphic-dompurify` 2.x**, server-side only, one sanitiser in the tree, pulls `jsdom` into `api/`. |
| **§ Parallelism and file ownership** | New rows, all strictly serial: `api/src/hypothesis/store.ts` (R1 → R8), `api/src/config.ts` + `.env.example` (R2 only, within this feature), `api/src/app.ts` (R7), `api/src/routes/hypotheses.ts` (R8), `api/src/orange/client.ts` (W2 → R1), `web/package.json` (R9 → R10), `web/src/App.tsx` (R10). |

---

## Tickets

Twelve tickets, `R1`–`R12`. Revision 4 renumbers them into the `W` series and merges the dependency
graph; dependencies below are stated against the main plan's ticket ids so the fold is mechanical.

**File ownership within this feature is strict.** `template.ts` is R2's alone and `sanitise.ts` is
R3's alone — `parseTemplate` (structure) and `validateTemplate` (profile) are deliberately in
different files owned by different tickets, so read both before writing either.

### R1: Report vocabulary, trusted-kind set, and the store reads   [Status: pending | Model: opus]
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
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/report/kinds src/hypothesis/store src/orange/client && yarn typecheck`
- **Depends on:** W5, O11
- [ ] done
- Notes:

### R2: Template parser, structure hash, and the report config   [Status: pending | Model: opus]
- **Scope:** `parseTemplate` — slot extraction, fragment-shape validation, https-only enforcement,
  size limit, structure hashing. Plus the two config variables this feature introduces.
- **Repo:** agent-wolf
- **Files:** create `api/src/report/template.ts`, `api/src/report/template.test.ts`; modify
  `api/src/config.ts`, `.env.example`.
- **Acceptance criteria:**
  - `parseTemplate(html, maxBytes)` takes the limit as a **parameter**; it reads no config itself.
  - `WOLF_REPORT_MAX_BYTES` (default 512000) and `WOLF_SERIES_MAX_POINTS` (default 5000) are added
    to `api/src/config.ts` and documented in `.env.example` **by this ticket**, because R2 is the
    first consumer. R7 must not re-add them.
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
- **Validation:** `cd api && yarn test src/report/template && yarn typecheck`
- **Depends on:** R1
- [ ] done
- Notes:

### R3: The slot sanitiser and template validation   [Status: pending | Model: opus]
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
    returns the `ParsedTemplate` from R2's `parseTemplate`. A test asserts no exported function
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
- **Validation:** `cd api && yarn test src/report/sanitise && yarn typecheck`
- **Depends on:** R2
- [ ] done
- Notes:

### R4: Series selection and injection payload   [Status: pending | Model: sonnet]
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
- **Depends on:** R1
- [ ] done
- Notes:

### R5: Frame composition and the CSP value   [Status: pending | Model: opus]
- **Scope:** `composeFrame` — assemble the document, produce the CSP string.
- **Repo:** agent-wolf
- **Files:** create `api/src/report/frame.ts`, `api/src/report/frame.test.ts`.
- **Acceptance criteria:**
  - The CSP string equals the § "The CSP header, byte-for-byte" value **exactly**, asserted as a
    string literal — including the leading `sandbox allow-scripts` directive. A substring check
    does not satisfy this criterion.
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
- **Validation:** `cd api && yarn test src/report/frame && yarn typecheck`
- **Depends on:** R3, R4
- [ ] done
- Notes:

### R6: Drift detection   [Status: pending | Model: sonnet]
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
- **Depends on:** R2
- [ ] done
- Notes:

### R7: Report routes   [Status: pending | Model: opus]
- **Scope:** The three routes in § "HTTP routes added", including fetching the datasets the frame
  injects.
- **Repo:** agent-wolf
- **Files:** create `api/src/routes/report.ts`, `api/src/routes/report.test.ts`; modify
  `api/src/app.ts`.
- **Acceptance criteria:**
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
- **Validation:** `cd api && yarn test src/routes/report && yarn typecheck`
- **Depends on:** R5, R6, W8, W11
- [ ] done
- Notes:

### R8: Board integration and cross-hypothesis defence   [Status: pending | Model: opus]
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
    and carries `tamper` naming the retractor — reusing R1's store reads, not a second code path.
- **TDD:** yes.
- **Validation:** `cd api && yarn test src/hypothesis/store src/routes/hypotheses && yarn typecheck`
- **Depends on:** R7
- [ ] done
- Notes:

### R9: VerdictBand and ReportPanel   [Status: pending | Model: sonnet]
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
  - `stripped_count > 0` renders a visible notice with the count; drift renders a visible notice
    naming the orphan and unfilled slots.
  - `VerdictBand` renders status, score and the tripped/holding/**indeterminate** counts, with
    `indeterminate` visually distinct from `holding` — the same rule W14 applies to the condition
    table, for the same reason. The word is `indeterminate` throughout; never "unknown".
  - `VerdictBand` carries an explicit note that the score summarises and does not decide.
- **TDD:** yes for the sandbox attribute, the empty state and the notices; no for layout.
- **Validation:** `cd web && yarn test && yarn typecheck`
- **Depends on:** R8, W14
- [ ] done
- Notes:

### R10: Go Live review screen   [Status: pending | Model: sonnet]
- **Scope:** The screen where a human approves a candidate report before go-live.
- **Repo:** agent-wolf
- **Files:** create `web/src/pages/GoLiveReview.tsx` and its test; modify `web/src/App.tsx`.
- **Acceptance criteria:**
  - The screen reads the newest `kind=report-candidate` for the hypothesis — that is the transport
    by which the interview's proposed template leaves the container — and renders its HTML.
  - The candidate renders **inside the real frame component**, with the real CSP and the real
    sandbox. Reviewing a preview that differs from production defeats the purpose of reviewing.
  - The external script and stylesheet URLs (`scriptSrcs`) are listed explicitly above the preview.
    The human is approving remote code; they are shown exactly what it is.
  - The Go Live button is disabled until the spec validates **and** a template has been accepted,
    with the blocking reasons listed. Neither condition alone enables it.
  - Accepting posts the candidate's HTML to `POST …/report-template` and surfaces a `422` as
    per-path errors.
  - A hypothesis with no candidate shows a clear state saying the interview has not produced one
    yet, not an error.
- **TDD:** yes for the gate logic and the URL listing; no for layout.
- **Validation:** `cd web && yarn test && yarn typecheck`
- **Depends on:** R9, W13
- [ ] done
- Notes:

### R11: Report authoring prompts   [Status: pending | Model: opus]
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
- **Validation:** `cd api && yarn test src/report/fixture && yarn typecheck`
- **Depends on:** R3, W12
- [ ] done
- Notes:

### R12: End-to-end verification   [Status: pending | Model: opus]
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
  give `run.sh` an optional spec-name filter — see § "Amendments to existing tickets".)*
- **Depends on:** R10, R11, X1
- [ ] done
- Notes:

## Dependency graph

```
W5,O11 ──► R1 ──┬──► R2 ──┬──► R3 ──┬──► R5 ──► R7 ──► R8 ──► R9 ──► R10 ──┐
                │         │         │      (W8,W11)          (W14)  (W13)  │
                │         └──► R6 ──┘                                      ├──► R12
                └──► R4 ──────────────► R5                                 │
                          R3,W12 ──► R11 ─────────────────────────────────►┘
                                                                        (X1)
```

Parallel-eligible: **R2 ‖ R4** once R1 lands. **R6** any time after R2. **R11** any time after R3
and W12. Everything from R5 onward is serial, because each ticket modifies a file the next one
reads.

*(Note the corrected edges: R3 depends on R2 because `validateTemplate` calls `parseTemplate`; R4
does **not** depend on W6, which owns provider connectors rather than dataset reads; and R1 — not
R8 — owns the store reads R7 needs, which removes the R7↔R8 cycle an earlier draft had.)*

## Discovered Issues Log

*(appended by executors during implementation)*
