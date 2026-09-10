# 28 — The console's information architecture: the four open questions, answered

*Written 2026-08-14, immediately after the K7–K9 decisions in
[`15-operator-console-design.md`](15-operator-console-design.md) §14. Those decisions settled the
**shape** of the console — chart stays, K3 stands, nav is progressive. They left four questions
explicitly to this design pass, and warned against settling them by implementation default. This
document settles them.*

*Status: **design — nothing here is built.** It is written against the built code, and every claim
about what already exists was checked in the source rather than remembered.*

---

## 0. The finding that makes this document short

The console already contains the answer to most of these questions, in one component nobody has
finished using.

`web/src/spine.tsx` is the design's signature (§3.6) — *"one vertical hairline with ticks… Agent
Bob is the tool where everything hangs off a rail, because everything is an append."* It ships
as `SpineRail`, `SpineRow`, `SpineGap` and a **closed set of five glyphs**. Two properties of that
component decide this whole design:

1. **It is presentational only** — *"no state, no fetch, no data shaping. What goes on the rail is
   decided by `desk.ts` and by the pages that mount these."* The rail does not know or care what
   kind of record it is drawing.
2. **Its glyph set is keyed to authorship and state, not to record type.** Filled disc = a worker
   did this. Hollow = a person did this. Diamond = waiting for you. Cross = a failure. Lock = a
   frozen worker refused a rewrite. There is no "this is an event" glyph, and no "this is a job"
   glyph, *because the design never intended the rail to care.*

A rail whose vocabulary is *who did this* rather than *which table this came from* is a rail built
to carry every table at once. The console currently mounts it on the Desk and on Lineage, and then —
on the Events page — abandons it for five tabs of segregated MUI tables.

So the honest summary of this design pass: **finish the spine.** Three of the four answers below are
consequences of taking §3.6 literally.

---

## 1. Q1 — Activity: tabbed, or interleaved?

**Answer: interleaved. One rail, one row per *occurrence*, filters as lenses.**

### 1.1 Why not tabs

Tabs preserve exactly the separation that hides the product's thesis. §7.2 of the design names that
thesis — *"before / after — the product's thesis on one screen"* — and it is a sentence about time:
*the prompt changed at 03:12, and the job that ran at 04:00 came out different.* On tabbed
surfaces those two facts live on two screens and the reader must hold a timestamp in their head to
join them. On one rail they are adjacent rows and the story reads itself.

`BeforeAfterView` exists today precisely because the tabs make that story invisible: it is a
bespoke component built to manually re-join two records that a time-ordered rail would have shown
side by side for free. That component is the tab structure's bug report.

### 1.2 The row model — the part that makes interleaving work

The obvious objection to a merged timeline is that **one occurrence produces three records**: an
event arrives, a delivery is created, a job runs. Interleaving those naively yields three rows for
one thing, which is worse than tabs, not better.

So the rail's unit is the **occurrence, not the record**:

```
◆  09:14   email-answerer is waiting for you                    2h 40m
           "Reply drafted for the Ridley invoice query, but the
            amount doesn't match our records. Send as-is, or hold?"
                                                    ▸ open thread

●  05:40   email-reviewer rewrote email-answerer          +1 −1 lines
           "narrowing yesterday's rule: reference only when one exists"
                                                    ▸ show the diff

●  04:12   worker.finished {worker: email-answerer}
           woke  archivist  ·  ran 41s  ·  wrote 2 memories
                                                    ▸ open the session
```

That third row is *one* row carrying an event, its delivery and its job. The event is the headline
because the event is what happened; the delivery is a preposition (`woke`); the job is the outcome.
A reader who wants the envelope expands the row. Nobody needs to be told that a delivery row exists.

**Rule:** a record only earns its own row when it has no parent occurrence — a config change made by
a human, an attention request, a schedule that failed to provision.

### 1.3 Filters are lenses, not tabs

`all · events · jobs · changes` as chips above the rail. They **subset the rail in place**; they
never swap the surface. The rail, the watermark and the scroll position survive a filter change, so
switching lens is a narrowing rather than a navigation. This is the difference between a merge and
a rename, and it is the whole of it.

### 1.4 Three hazards this design must handle, named now

- **The unit mismatch is the highest-risk bug in this document.** `desk.ts` records it as J1:
  *"deliveries, events, schedules and attention requests stamp unix SECONDS; config events stamp
  unix MILLISECONDS. Do not 'tidy' one into the other."* A single rail **sorts by time**, so a
  merged fold that misses this places every config change roughly fifty thousand years in the
  future, silently, at the top of the rail. Normalise once, at the fold boundary, in a named
  function with a test that feeds it one record of each kind.
- **Pagination cannot be `LIMIT n` per source.** Merging four time-ordered sources by row count
  gives a rail that is wrong at its bottom edge. Page by **time window** — "Show earlier" extends
  the window across all sources — which is the correct interaction for a timeline anyway.
  `SpineGap` already exists to mark the quiet stretches this exposes.
- **One rail, one watermark.** Activity gets its own; the Desk keeps its own, because they answer
  different questions ("what happened?" vs "what happened *that I have not seen*?"). Note that
  `WorkerLineage` currently reads the **Desk's** watermark — that coupling should be broken when
  History is built (§2), not preserved.

### 1.5 What to build

A pure fold, `activity.ts`, in the exact shape of `desk.ts`: no React, no fetch, no clock, `now`
passed in as a parameter. `desk.ts` already merges five sources and is unit-tested as a table; this
is the same job with a different output shape. **The rail is not new work — only the fold is.**

---

## 2. Q2 — Triggers: a fifth Workers tab, or a section inside Configuration?

**Answer: a tab — but the tab count goes *down*, because Jobs and Lineage merge into History.**

### 2.1 The worker's four facets

| Tab | The question it answers |
| --- | --- |
| **Configuration** | What is this worker? |
| **Triggers** | What makes it run? |
| **History** | What has it done, and how has it changed? |
| **Chat** | Let me talk to it. |

Triggers earns a tab because subscriptions and schedules are **separate entities with their own
CRUD, their own editors and their own rationale fields**. Burying two full editors inside
`WorkerEditor` — which already carries prompt, image, MCP servers, max instances, briefing
selectors, enabled and frozen — produces a second `ProjectSettingsPage`: one long form where
everything is technically present and nothing is findable.

### 2.2 But the arrival question gets answered without a click

Doc 15 says the question a human arrives with is *"what makes this worker run?"*. A tab answers it
in one click; the design can do better in zero. **Configuration gains a read-only `Woken by` line**:

```
Woken by    worker.finished {worker: email-answerer}
            every weekday at 09:00
                                                      ▸ edit triggers
```

Read-only summary on the definition screen, editing behind one click, is exactly the Desk's own
rule — *"it is read-only; every item's action is to open the thing it names."*

### 2.3 Jobs + Lineage → History, and why this is the good part

"What it did" and "how it changed" are one story told in time, and separating them is the same
mistake §1.1 diagnoses on the Events page — at a smaller scale, with the same casualty. Merged onto
the rail, a prompt rewrite and the jobs either side of it are **adjacent rows**, which is
`BeforeAfterView`'s entire manual job, obtained for nothing.

This is also what makes the console a system rather than a set of pages. **The same rail appears at
three altitudes:**

| Altitude | Surface | Scope |
| --- | --- | --- |
| Everything, digested | **Desk** | what wants me / changed / broke, since I last looked |
| Everything, in order | **Activity** | the whole project's rail |
| One worker, in order | **Workers · History** | that worker's rail |

One component, one glyph vocabulary, one fold shape, three scopes. A reader who learns the rail on
the Desk can read every other surface in the console.

**Net: Workers goes from four tabs to four tabs** — and gains triggers while *losing* a screen.

---

## 3. Q3 — The reveal choreography

**Answer: the button appears quietly; the action that caused it says so; the reveal is sticky.**

### 3.1 No pulse, no badge, no glow

§3.2 is binding here: *"colour is never spent on prettiness, which keeps the surface calm enough for
the one thing that must shout — a worker asking you a question."* A nav item announcing its own
arrival in ember would be spending the authorship colour on chrome, and would compete with the one
badge the design permits (`asks`, §3.5: *"the only number in the chrome"*).

The transition is **one 180ms fade and height**, gated on the existing `useReducedMotion` hook.
That is the entire motion budget for this feature.

### 3.2 Words carry the news instead

The reveal is a *consequence of an action the human just took*, so the confirmation of that action
is where it belongs:

> **Worker created.** `archivist` will run when something wakes it.
> **Activity** is now in the sidebar — it lists everything this project does.

This follows the design's own editorial rule (§11, the operator's vocabulary) and costs nothing but
a sentence. An interface that explains a change in words does not need to flash.

### 3.3 The rules

- **Order never changes.** The nav is a fixed sequence — Desk, Chat, Workers, Memory, Activity,
  Chart, Settings — and items appear *in place*. A control that moves is worse than one that
  appears, because it breaks the muscle memory the reveal is supposed to be building.
- **Sticky per project, once revealed.** Stored in `localStorage` beside the existing watermark
  keys (`watermark.ts` is the precedent, and is already per-project).
- **Deleting the last worker does not un-reveal anything.** The history still exists, so the
  surface that reads it must remain. Un-revealing is how a console teaches a human that it cannot
  be trusted to stay where they left it.
- **Reveal is content-driven, not tutorial-driven.** Memory appears with the first memory, Activity
  with the first event, Chart with the first subscription (§4). Nothing appears on a timer, and
  nothing appears because a human "completed onboarding" — there is no onboarding state to keep.

---

## 4. Q4 — The chart's threshold, and the form of the Replay merge

### 4.1 The threshold is the first subscription, not a worker count

A worker count is the wrong trigger: five workers with nothing wired between them have **no shape**,
and drawing them is five plates in a row, which a list does better. The chart becomes worth drawing
at the moment the fleet stops being a list and becomes a **graph** — that is, when one worker wakes
another.

**So: the chart appears when the project has its first subscription.**

This lands exactly where the KISS doctrine wants it. `27-simplification-inventory.md` holds the line
at *"one subscription per project"*, and the recommended shape — `architect-archivist` — has
precisely one. The chart therefore appears the moment you have set up the architecture the product
recommends, and not before. Schedules deliberately do **not** trigger it: a clock is a per-worker
fact and the Triggers tab renders it better than a dial on a canvas.

### 4.2 Replay merges as two additions to the propagation panel — not as a panel

The chart's propagation panel and `EventReplayPanel` are near-duplicate answers to *"what would this
event wake?"*. The panel already accepts a pasted event and traces it with a depth ruler; Replay
contributes two things the panel lacks:

- **Templates and a real JSON editor** — the panel's *"…or paste an event"* textarea becomes
  `JsonObjectEditor` with a "Load a template" control, which is what `EventReplayPanel` does today.
- **Emitting for real** — the existing confirm dialog moves across intact.

And one thing is **deleted rather than moved**: Replay's textual *match list*. The whole value of
merging is that the answer renders **on the shape** — wires lighting along the traced path, the
depth ruler counting hops — instead of as a list of matching subscription rows beside a canvas that
already draws those subscriptions. Keeping both would be keeping the duplicate we merged to remove.

### 4.3 The one honest wart: emit is a mutation on a proposal surface

The chart's rule (K3, and its own header comment) is that *"every gesture is a PROPOSAL, never a
mutation."* **Emitting a real event is a mutation** — the only one on the canvas, and an
irreversible one: it wakes real workers and spends real tokens.

That does not disqualify it, but it constrains the design:

- **Trace is the primary action; Emit is secondary and never the default.** Different weight,
  different position, and Emit sits behind the confirm dialog it already has.
- **The verb stays the same through the flow** (§11, and the editorial rule that "Publish" produces
  "Published"): the button says **Emit this event**, the dialog asks **Emit a real event?**, the
  result says **Emitted** — and names what it woke, because that is the thing the human wants to
  know next and the canvas is already drawing it.

---

## 5. What this costs, honestly

| Work | Size | Note |
| --- | --- | --- |
| `activity.ts` — the pure fold | the real work | Same shape as `desk.ts`; the J1 normalisation is the part to test hardest |
| Activity view + filter chips | small | Mounts `SpineRail`/`SpineRow`, which exist |
| `Woken by` summary on Configuration | small | Read-only; the data is already fetched for the Triggers tab |
| Workers History fold (jobs + lineage) | medium | Retires `BeforeAfterView`'s manual re-join; breaks Lineage's borrowed Desk watermark |
| Triggers tab | small | `SubscriptionEditor` + `ScheduleEditor` move intact |
| Progressive nav | small | Reveal predicates + a sticky per-project key |
| Chart: Replay merge | medium | Two additions, one deletion (the match list) |
| Chart: doc 21 defects X1–X4, X9, X10 | medium | The bill for K7 |

Nothing in this document requires a new visual language, a new component or a backend route. It is
mostly **one fold, one merge, and finishing a component the design shipped and then stopped
mounting.**

---

## 6. What is deliberately not decided here

- **The chart's motion system** (doc 21 §5) stays unbuilt and unscheduled. K7 keeps the chart, which
  makes doc 21's defect list real work; it does not make its glisten real work.
- **Whether Activity's rail should stream** rather than poll at `LIVE_REFRESH_MS`. The staged-arrival
  machinery (`useStagedFeed`, the "N new" pill, the pause toggle) already handles polling honestly
  and WCAG 2.2.2 is already satisfied. Streaming is an optimisation, not a design question.
- **Any change to the Desk.** It was found to be the best surface in the console and this pass does
  not touch it, beyond it keeping its own watermark.
